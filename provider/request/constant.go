package request

import (
	"time"
)

// Timeout 请求超时时间
const Timeout = 120 * time.Second

// API endpoints
const (
	ChatCompletionsEndpoint = "/chat/completions"
	EmbeddingsEndpoint      = "/embeddings"
)

// SSE constants
const (
	SSEDataPrefix = "data: "
	SSEDoneMarker = "[DONE]"
)
