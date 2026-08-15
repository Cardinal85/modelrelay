package protocol

// 错误码（规范见 docs/protocol.md §6）。
const (
	ErrUnsupportedProtocol       = "unsupported_protocol"
	ErrIdentityMismatch          = "identity_mismatch"
	ErrDuplicateNode             = "duplicate_node"
	ErrUnauthorized              = "unauthorized"
	ErrInvalidPath               = "invalid_path"
	ErrInvalidMethod             = "invalid_method"
	ErrInvalidRequest            = "invalid_request"
	ErrBodyTooLarge              = "body_too_large"
	ErrModelNotFound             = "model_not_found"
	ErrCapabilityNotSupported    = "capability_not_supported"
	ErrNoAvailableNode           = "no_available_node"
	ErrQueueFull                 = "queue_full"
	ErrQueueTimeout              = "queue_timeout"
	ErrLocalBusy                 = "local_busy"
	ErrUpstreamConnectionFailed  = "upstream_connection_failed"
	ErrUpstreamTimeout           = "upstream_timeout"
	ErrTTFTTimeout               = "ttft_timeout"
	ErrIdleTimeout               = "idle_timeout"
	ErrCanceled                  = "canceled"
	ErrDraining                  = "draining"
	ErrInternal                  = "internal_error"
)

// HTTPStatus 返回错误码建议映射的 HTTP 状态码。
func HTTPStatus(code string) int {
	switch code {
	case ErrUnauthorized:
		return 401
	case ErrInvalidPath:
		return 404
	case ErrInvalidMethod:
		return 405
	case ErrInvalidRequest:
		return 400
	case ErrBodyTooLarge:
		return 413
	case ErrModelNotFound:
		return 404
	case ErrCapabilityNotSupported:
		return 422
	case ErrQueueFull:
		return 429
	case ErrQueueTimeout, ErrNoAvailableNode, ErrLocalBusy, ErrDraining:
		return 503
	case ErrUpstreamConnectionFailed:
		return 502
	case ErrUpstreamTimeout, ErrTTFTTimeout, ErrIdleTimeout:
		return 504
	case ErrCanceled:
		return 499
	default:
		return 500
	}
}
