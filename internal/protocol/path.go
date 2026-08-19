package protocol

// IsAllowedPath 判断 HTTP 方法与路径是否允许经 Relay 转发到 Agent。
func IsAllowedPath(method, path string) bool {
	switch path {
	case "/v1/chat/completions", "/v1/completions", "/v1/embeddings", "/v1/responses",
		"/v1/audio/transcriptions", "/v1/audio/translations", "/v1/audio/speech",
		"/v1/images/generations", "/v1/images/edits", "/v1/images/variations",
		"/v1/moderations", "/v1/rerank", "/v1/reranking":
		return method == "POST" || method == "GET"
	case "/v1/models", "/v1/models/":
		return method == "GET"
	default:
		return false
	}
}
