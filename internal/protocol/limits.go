package protocol

// AgentOrigin 是 Agent WebSocket 握手使用的固定 Origin。
// Relay 只接受该 Origin，拒绝浏览器跨站连接。
const AgentOrigin = "https://modelrelay.agent"

// MaxControlMessage 是 WSS JSON 控制消息上限（1 MiB）。
const MaxControlMessage = 1 << 20

// MaxWSMessage 是单条 WebSocket 消息上限（200 MiB），覆盖分片前的上传体。
const MaxWSMessage = 200 << 20
