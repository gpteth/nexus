package gateway

import "time"

// ChatRequest 仅声明路由与治理需要解析的字段；请求体其余字段原样透传，避免丢失供应商扩展参数。
type ChatRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

// Message 是标准对话消息。
type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// Usage 是 token 用量。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse 是非流式响应，用于提取用量。
type ChatResponse struct {
	ID     string `json:"id"`
	Object string `json:"object"`
	Model  string `json:"model"`
	Usage  *Usage `json:"usage,omitempty"`
}

// StreamChunk 是流式响应块，用于在开启 include_usage 时提取用量。
type StreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Usage   *Usage `json:"usage,omitempty"`
	Choices []struct {
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
}

// ModelInfo 是 /v1/models 返回的模型信息。
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelsResponse 是模型列表响应。
type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// ErrorResponse 是统一错误体。
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody 是错误详情。
type ErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

func newErrorResponse(message, code string) ErrorResponse {
	return ErrorResponse{Error: ErrorBody{Message: message, Type: "nexus_error", Code: code}}
}

// callStats 汇总一次调用的治理信息。
type callStats struct {
	upstream string
	model    string
	usage    Usage
	stream   bool
	start    time.Time
	success  bool
}
