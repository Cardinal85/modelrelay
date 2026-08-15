package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var id [16]byte
	copy(id[:], "0123456789abcdef")
	f := &Frame{
		Type:      FrameResponseBody,
		First:     true,
		Last:      true,
		RequestID: id,
		Seq:       7,
		Encoding:  EncodingRaw,
		Payload:   []byte("data: [DONE]\n\n"),
	}
	enc, err := f.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeFrame(bytes.NewReader(enc))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Type != f.Type || got.First != f.First || got.Last != f.Last {
		t.Errorf("flags mismatch: %+v", got)
	}
	if got.Seq != 7 || !bytes.Equal(got.Payload, f.Payload) {
		t.Errorf("payload/seq mismatch: seq=%d payload=%q", got.Seq, got.Payload)
	}
	if got.RequestID != id {
		t.Errorf("request id mismatch")
	}
}

func TestFrameBadMagic(t *testing.T) {
	_, err := DecodeFrame(bytes.NewReader(make([]byte, FrameHeaderSize)))
	if !errors.Is(err, ErrBadMagic) {
		t.Fatalf("want ErrBadMagic, got %v", err)
	}
}

func TestFrameTooLarge(t *testing.T) {
	f := NewFrame(FrameResponseBody, [16]byte{}, 1, make([]byte, MaxFramePayload+1))
	if _, err := f.Encode(); !errors.Is(err, ErrBadLength) {
		t.Fatalf("want ErrBadLength, got %v", err)
	}
}

func TestControlParse(t *testing.T) {
	raw := `{"type":"hello","protocol":1,"node_id":"gpu-1"}`
	typ, payload, err := ParseControl([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if typ != MsgHello {
		t.Fatalf("type=%s", typ)
	}
	var h Hello
	if err := json.Unmarshal(payload, &h); err != nil {
		t.Fatalf("unmarshal hello: %v", err)
	}
	if h.NodeID != "gpu-1" || h.Protocol != 1 {
		t.Errorf("hello mismatch: %+v", h)
	}
}

func TestControlUnknown(t *testing.T) {
	if _, _, err := ParseControl([]byte(`{"type":"nope"}`)); err == nil {
		t.Fatal("want error for unknown type")
	}
}

func TestHTTPStatusMapping(t *testing.T) {
	cases := map[string]int{
		ErrQueueFull:      429,
		ErrNoAvailableNode: 503,
		ErrBodyTooLarge:   413,
		ErrCanceled:       499,
	}
	for code, want := range cases {
		if got := HTTPStatus(code); got != want {
			t.Errorf("%s: got %d want %d", code, got, want)
		}
	}
}

func TestRequestIDBytes(t *testing.T) {
	a := RequestIDBytes("req-1234567890")
	b := RequestIDBytes("req-1234567890")
	if a != b {
		t.Fatal("not stable")
	}
}
