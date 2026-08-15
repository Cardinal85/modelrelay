// Package protocol 定义 Relay-Agent 内部协议：控制消息与二进制数据帧。
//
// 规范见 docs/protocol.md。
package protocol

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion 是当前协议版本。
const ProtocolVersion = 1

// 控制消息类型（Text Frame + JSON 的 type 字段）。
const (
	MsgHello        = "hello"
	MsgHelloAck     = "hello_ack"
	MsgHeartbeat    = "heartbeat"
	MsgHeartbeatAck = "heartbeat_ack"
	MsgRequest      = "request"
	MsgResponseHdr  = "response_headers"
	MsgDone         = "done"
	MsgError        = "error"
	MsgCancel       = "cancel"
	MsgDrain        = "drain"
	MsgDrainAck     = "drain_ack"
	MsgModelsUpdate = "models_update"
	MsgProbe        = "probe"
	MsgBye          = "bye"
)

// Platform 描述 Agent 运行平台。
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// ModelInfo 描述节点上的一个模型及其已知能力。
type ModelInfo struct {
	ID                   string   `json:"id"`
	Capabilities         []string `json:"capabilities"`
	CapabilitiesComplete bool     `json:"capabilities_complete,omitempty"`
}

// Limits 描述节点/Relay 的并发限制。
type Limits struct {
	MaxConcurrency int `json:"max_concurrency"`
}

// Hello 由 Agent 在连接建立后首先发送。
type Hello struct {
	Type             string      `json:"type"`
	Protocol         int         `json:"protocol"`
	NodeID           string      `json:"node_id"`
	AgentVersion     string      `json:"agent_version"`
	Platform         Platform    `json:"platform"`
	Models           []ModelInfo `json:"models"`
	Limits           Limits      `json:"limits"`
	HeartbeatSeconds int         `json:"heartbeat_interval"`
}

// HelloAck 由 Relay 回复 Hello。
type HelloAck struct {
	Type             string `json:"type"`
	Protocol         int    `json:"protocol"`
	RelayID          string `json:"relay_id"`
	Accepted         bool   `json:"accepted"`
	Reason           string `json:"reason,omitempty"`
	RegisteredModels int    `json:"registered_models,omitempty"`
	MaxConcurrency   int    `json:"max_concurrency,omitempty"`
	HeartbeatSeconds int    `json:"heartbeat_interval,omitempty"`
}

// Heartbeat 由 Agent 周期性发送。
type Heartbeat struct {
	Type         string `json:"type"`
	TS           int64  `json:"ts"`
	Seq          uint64 `json:"seq"`
	ActiveReqs   int    `json:"active_requests"`
	LocalModelOK bool   `json:"local_model_ok"`
	LastProbeOK  bool   `json:"last_probe_ok"`
	GPU          *GPU   `json:"gpu,omitempty"`
}

// HeartbeatAck 由 Relay 回复 Heartbeat。
type HeartbeatAck struct {
	Type string `json:"type"`
	TS   int64  `json:"ts"`
	OK   bool   `json:"ok"`
}

// GPU 是可选的 GPU 指标。
type GPU struct {
	Util       float64 `json:"util"`
	MemUsedMB  int64   `json:"mem_used_mb"`
	MemTotalMB int64   `json:"mem_total_mb"`
}

// Request 由 Relay 发送给 Agent，指示向本地模型发起一次请求。
type Request struct {
	Type         string            `json:"type"`
	RequestID    string            `json:"request_id"`
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	Query        string            `json:"query,omitempty"`
	Headers      map[string]string `json:"headers"`
	BodyLen      int64             `json:"body_len"`
	BodyEncoding string            `json:"body_encoding"`
	Stream       bool              `json:"stream"`
}

// ResponseHeaders 由 Agent 在读取到本地上游响应头后、发送响应体前发出。
type ResponseHeaders struct {
	Type       string            `json:"type"`
	RequestID  string            `json:"request_id"`
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
}

// Done 由 Agent 发送，表示响应结束。
type Done struct {
	Type       string            `json:"type"`
	RequestID  string            `json:"request_id"`
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	TTFTMs     int64             `json:"ttft_ms,omitempty"`
	DurationMs int64             `json:"duration_ms,omitempty"`
	Canceled   bool              `json:"canceled,omitempty"`
}

// Error 是双向错误消息。
type Error struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id,omitempty"`
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
}

// Cancel 由 Relay 发送给 Agent。
type Cancel struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Reason    string `json:"reason,omitempty"`
}

// Drain 由 Relay 发送给 Agent。
type Drain struct {
	Type         string `json:"type"`
	GraceSeconds int    `json:"grace_seconds"`
}

// DrainAck 由 Agent 回复 Drain。
type DrainAck struct {
	Type           string `json:"type"`
	ActiveRequests int    `json:"active_requests"`
	Draining       bool   `json:"draining"`
}

// ModelsUpdate 由 Agent 在能力探测完成后发送，更新节点模型与能力。
type ModelsUpdate struct {
	Type   string      `json:"type"`
	Models []ModelInfo `json:"models"`
}

// Probe 由 Relay 发送给 Agent，触发一次能力探测。
type Probe struct {
	Type string `json:"type"`
}

// Bye 是正常关闭消息。
type Bye struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}

// ParseControl 解析一条控制消息。
// 返回的 type 为消息类型；无法识别时返回错误。
func ParseControl(data []byte) (string, json.RawMessage, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return "", nil, fmt.Errorf("protocol: malformed control message: %w", err)
	}
	switch head.Type {
	case MsgHello, MsgHelloAck, MsgHeartbeat, MsgHeartbeatAck, MsgRequest,
		MsgResponseHdr, MsgDone, MsgError, MsgCancel, MsgDrain, MsgDrainAck,
		MsgModelsUpdate, MsgProbe, MsgBye:
		return head.Type, json.RawMessage(data), nil
	default:
		return "", nil, fmt.Errorf("protocol: unknown control type %q", head.Type)
	}
}
