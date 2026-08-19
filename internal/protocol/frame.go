package protocol

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// 数据帧类型。
const (
	FrameRequestBody  = 1 // Agent 收到的请求体
	FrameResponseBody = 2 // Agent 发出的响应体/SSE/二进制
)

// 帧标志位。
const (
	FlagFirst     = 0x01
	FlagLast      = 0x02
	FlagCompressed = 0x04
)

// 编码类型。
const (
	EncodingRaw  = 0
	EncodingGzip = 1
)

// FrameHeaderSize 是固定帧头大小：2+1+1+1+1+16+4+1+4+1 = 31 字节。
const FrameHeaderSize = 31

// MaxFramePayload 是单帧负载上限（256 KiB）。
const MaxFramePayload = 256 * 1024

var (
	// ErrBadMagic 表示帧头 Magic 不正确。
	ErrBadMagic = errors.New("protocol: bad frame magic")
	// ErrBadVersion 表示帧版本不支持。
	ErrBadVersion = errors.New("protocol: bad frame version")
	// ErrBadType 表示帧类型不支持。
	ErrBadType = errors.New("protocol: bad frame type")
	// ErrBadLength 表示负载长度超过上限。
	ErrBadLength = errors.New("protocol: frame payload too large")
	// ErrBadRequestID 表示 request_id 长度错误（必须 16 字节）。
	ErrBadRequestID = errors.New("protocol: request id must be 16 bytes")
)

// Frame 是一个二进制数据帧。
type Frame struct {
	Type      byte
	First     bool
	Last      bool
	Compressed bool
	RequestID [16]byte
	Seq       uint32
	Encoding  byte
	Payload   []byte
}

// NewFrame 构造一个帧。
func NewFrame(typ byte, requestID [16]byte, seq uint32, payload []byte) *Frame {
	return &Frame{
		Type:      typ,
		RequestID: requestID,
		Seq:       seq,
		Encoding:  EncodingRaw,
		Payload:   payload,
	}
}

// Encode 将帧编码为字节流（含帧头与负载）。
func (f *Frame) Encode() ([]byte, error) {
	if len(f.Payload) > MaxFramePayload {
		return nil, ErrBadLength
	}
	out := make([]byte, FrameHeaderSize+len(f.Payload))
	out[0] = 'M'
	out[1] = 'R'
	out[2] = ProtocolVersion
	out[3] = f.Type
	var flags byte
	if f.First {
		flags |= FlagFirst
	}
	if f.Last {
		flags |= FlagLast
	}
	if f.Compressed {
		flags |= FlagCompressed
	}
	out[4] = flags
	copy(out[5:21], f.RequestID[:])
	binary.LittleEndian.PutUint32(out[21:25], f.Seq)
	out[25] = f.Encoding
	binary.LittleEndian.PutUint32(out[26:30], uint32(len(f.Payload)))
	out[30] = 0
	copy(out[31:], f.Payload)
	return out, nil
}

// DecodeFrame 从 reader 读取并解码一个完整帧。
func DecodeFrame(r io.Reader) (*Frame, error) {
	header := make([]byte, FrameHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	if header[0] != 'M' || header[1] != 'R' {
		return nil, ErrBadMagic
	}
	if header[2] != ProtocolVersion {
		return nil, fmt.Errorf("%w: got %d", ErrBadVersion, header[2])
	}
	if header[3] != FrameRequestBody && header[3] != FrameResponseBody {
		return nil, fmt.Errorf("%w: got %d", ErrBadType, header[3])
	}
	length := binary.LittleEndian.Uint32(header[26:30])
	if length > MaxFramePayload {
		return nil, ErrBadLength
	}
	f := &Frame{
		Type:      header[3],
		First:     header[4]&FlagFirst != 0,
		Last:      header[4]&FlagLast != 0,
		Compressed: header[4]&FlagCompressed != 0,
		Encoding:  header[25],
		Seq:       binary.LittleEndian.Uint32(header[21:25]),
		Payload:   make([]byte, 0, length),
	}
	copy(f.RequestID[:], header[5:21])
	if length > 0 {
		f.Payload = make([]byte, length)
		if _, err := io.ReadFull(r, f.Payload); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// RequestIDBytes 将字符串 request_id 转为 16 字节。
// 标准 UUID（可带连字符）按 128-bit 解析；其它字符串取 SHA-256 前 16 字节。
func RequestIDBytes(id string) [16]byte {
	var out [16]byte
	compact := strings.ReplaceAll(id, "-", "")
	if b, err := hex.DecodeString(compact); err == nil && len(b) == 16 {
		copy(out[:], b)
		return out
	}
	sum := sha256.Sum256([]byte(id))
	copy(out[:], sum[:16])
	return out
}
