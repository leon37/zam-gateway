package openai

// ChatCompletionStreamResponse represents an OpenAI SSE streaming response
type ChatCompletionStreamResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object,omitempty"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model,omitempty"`
	Choices []StreamChoice         `json:"choices"`
	Usage   *Usage                 `json:"usage,omitempty"`
	Error   *ErrorResponse         `json:"error,omitempty"`
}

// StreamChoice represents a choice in streaming response with delta content
type StreamChoice struct {
	Index        int     `json:"index,omitempty"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason,omitempty"`
}

// Delta represents the incremental content in streaming mode
type Delta struct {
	Content      string `json:"content,omitempty"`
	Role         string `json:"role,omitempty"`
	FunctionCall *struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function_call,omitempty"`
	ToolCalls []struct {
		Index    int    `json:"index,omitempty"`
		ID       string `json:"id,omitempty"`
		Type     string `json:"type,omitempty"`
		Function struct {
			Name      string `json:"name,omitempty"`
			Arguments string `json:"arguments,omitempty"`
		} `json:"function,omitempty"`
	} `json:"tool_calls,omitempty"`
}

// Usage represents token usage information
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
}
