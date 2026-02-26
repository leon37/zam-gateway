package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"zam/core"
	"zam/openai"
)

// ChatHandler handles OpenAI-compatible chat completion requests
type ChatHandler struct {
	router  core.Router
	workers []core.Worker
}

// NewChatHandler creates a new ChatHandler
func NewChatHandler(router core.Router, workers []core.Worker) *ChatHandler {
	return &ChatHandler{
		router:  router,
		workers: workers,
	}
}

// ServeHTTP implements http.Handler interface
func (h *ChatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed", "invalid_request_error")
		return
	}

	// 1. 解析请求体
	var req openai.ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, "Invalid request body", "invalid_request_error")
		return
	}

	// 2. 验证必需参数
	if req.Model == "" {
		h.sendError(w, http.StatusBadRequest, "model is required", "invalid_request_error")
		return
	}

	if len(req.Messages) == 0 {
		h.sendError(w, http.StatusBadRequest, "messages is required", "invalid_request_error")
		return
	}

	// 3. 构建推理请求
	traceID := uuid.New().String()
	inferenceReq := &core.InferenceRequest{
		TraceID:     traceID,
		Model:       req.Model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		Stream:      req.Stream,
	}

	// 4. 获取 Workers 列表
	if len(h.workers) == 0 {
		h.sendError(w, http.StatusServiceUnavailable, "No workers available", "server_error")
		return
	}

	// 5. 路由选择
	selectedWorker, err := h.router.Select(r.Context(), h.workers, inferenceReq)
	if err != nil {
		h.sendError(w, http.StatusServiceUnavailable, fmt.Sprintf("Failed to select worker: %v", err), "server_error")
		return
	}

	// 6. 执行请求
	if req.Stream {
		h.handleStreamRequest(w, r, selectedWorker, inferenceReq)
	} else {
		h.handleNonStreamRequest(w, r, selectedWorker, inferenceReq)
	}
}

// handleStreamRequest handles streaming responses
func (h *ChatHandler) handleStreamRequest(w http.ResponseWriter, r *http.Request, worker core.Worker, req *core.InferenceRequest) {
	// 设置 SSE 响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // 禁用 Nginx 缓冲

	// 设置 HTTP 状态码
	w.WriteHeader(http.StatusOK)

	// 创建 sender 回调
	senderFunc := func(chunk core.StreamChunk) error {
		// 检查错误
		if chunk.Error != nil {
			// 发送错误事件
			errorData := map[string]interface{}{
				"error": map[string]interface{}{
					"message": chunk.Error.Error(),
					"type":    "server_error",
					"code":    "stream_error",
				},
			}
			if err := writeSSEEvent(w, "error", errorData); err != nil {
				return fmt.Errorf("failed to write error event: %w", err)
			}
			return chunk.Error
		}

		// 构建 OpenAI 标准 SSE 响应
		response := openai.ChatCompletionStreamResponse{
			ID:      "chatcmpl-" + req.TraceID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []openai.StreamChoice{
				{
					Index: 0,
					Delta: openai.Delta{
						Content: chunk.Content,
					},
				},
			},
		}

		// 如果有完成原因，设置 finish_reason
		if chunk.FinishReason != "" {
			response.Choices[0].FinishReason = &chunk.FinishReason
		}

		// 序列化并发送
		if err := writeSSEEvent(w, "data", response); err != nil {
			return fmt.Errorf("failed to write chunk: %w", err)
		}

		return nil
	}

	// 执行推理
	if err := worker.Execute(r.Context(), req, senderFunc); err != nil {
		// 检查错误类型
		if errors.Is(err, http.ErrHandlerTimeout) || errors.Is(err, context.DeadlineExceeded) {
			// 超时错误
			_ = writeSSEEvent(w, "error", map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Request timeout",
					"type":    "timeout_error",
					"code":    "timeout",
				},
			})
			return
		}

		// 其他错误
		_ = writeSSEEvent(w, "error", map[string]interface{}{
			"error": map[string]interface{}{
				"message": err.Error(),
				"type":    "server_error",
				"code":    "internal_error",
			},
		})
		return
	}

	// 发送 [DONE] 标记
	_, _ = w.Write([]byte("data: [DONE]\n\n"))

	// 确保所有数据已发送
	flusher, ok := w.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}

// handleNonStreamRequest handles non-streaming responses
func (h *ChatHandler) handleNonStreamRequest(w http.ResponseWriter, r *http.Request, worker core.Worker, req *core.InferenceRequest) {
	var fullContent string

	// 创建 sender 回调，收集所有内容
	senderFunc := func(chunk core.StreamChunk) error {
		if chunk.Error != nil {
			return chunk.Error
		}
		fullContent += chunk.Content
		return nil
	}

	// 执行推理
	if err := worker.Execute(r.Context(), req, senderFunc); err != nil {
		if errors.Is(err, http.ErrHandlerTimeout) || errors.Is(err, context.DeadlineExceeded) {
			h.sendError(w, http.StatusRequestTimeout, "Request timeout", "timeout_error")
			return
		}

		h.sendError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}

	// 构建响应
	response := openai.ChatCompletionResponse{
		ID:      "chatcmpl-" + req.TraceID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []openai.Choice{
			{
				Index: 0,
				Message: openai.Message{
					Role:    "assistant",
					Content: fullContent,
				},
				FinishReason: "stop",
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

// sendError sends an error response in OpenAI format
func (h *ChatHandler) sendError(w http.ResponseWriter, statusCode int, message, errorType string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(ginH{
		"error": ginH{
			"message": message,
			"type":    errorType,
		},
	})
}

// writeSSEEvent writes an SSE event to the response writer
func writeSSEEvent(w http.ResponseWriter, eventType string, data interface{}) error {
	// 序列化数据
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	// 写入事件行
	if eventType != "" && eventType != "data" {
		if _, err := w.Write([]byte(fmt.Sprintf("event: %s\n", eventType))); err != nil {
			return fmt.Errorf("failed to write event type: %w", err)
		}
	}

	// 写入数据行
	if _, err := w.Write([]byte(fmt.Sprintf("data: %s\n\n", jsonData))); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	// 立即刷新，确保数据推送给客户端
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	return nil
}

// ginH is a simple map[string]interface{} for JSON responses
type ginH map[string]interface{}
