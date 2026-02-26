package openai

// ChatCompletionRequest represents a chat completion request
type ChatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []Message     `json:"messages"`
	Temperature float32       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	TopP        float32       `json:"top_p,omitempty"`
	N           int           `json:"n,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
	Frequency   float32       `json:"frequency_penalty,omitempty"`
	Presence    float32       `json:"presence_penalty,omitempty"`
}

// ChatCompletionResponse represents a non-streaming chat completion response
type ChatCompletionResponse struct {
	ID      string  `json:"id"`
	Object  string  `json:"object"`
	Created int64   `json:"created"`
	Model   string  `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage  `json:"usage,omitempty"`
}

// Choice represents a choice in non-streaming response
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

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
