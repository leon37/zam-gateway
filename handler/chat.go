package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"zam/core"
	"zam/openai"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ChatHandler handles OpenAI-compatible chat completion requests
type ChatHandler struct {
	router   core.Router
	registry core.WorkerRegistry
	limiter  core.RateLimiter
}

// NewChatHandler creates a new ChatHandler with static worker list
func NewChatHandler(router core.Router, workers []core.Worker, limiter core.RateLimiter) *ChatHandler {
	return &ChatHandler{
		router:   router,
		registry: nil,
		limiter:  limiter,
	}
}

// NewChatHandlerWithRegistry creates a new ChatHandler with dynamic worker registry
func NewChatHandlerWithRegistry(router core.Router, registry core.WorkerRegistry, limiter core.RateLimiter) *ChatHandler {
	return &ChatHandler{
		router:   router,
		registry: registry,
		limiter:  limiter,
	}
}

// extractAPIKey extracts the API key from Authorization header
// Expected format: "Bearer <api_key>"
func (h *ChatHandler) extractAPIKey(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return ""
	}

	// Split "Bearer <token>"
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return ""
	}

	return parts[1]
}

// Handle is the Gin handler function for chat completion requests
func (h *ChatHandler) Handle(c *gin.Context) {
	// 0. 提取 API Key 并进行限流预检
	apiKey := h.extractAPIKey(c)
	if apiKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"message": "Missing or invalid Authorization header",
				"type":    "authentication_error",
			},
		})
		return
	}

	// 阶段一：限流预检
	allowed, err := h.limiter.Allow(c.Request.Context(), apiKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "Rate limiter error: " + err.Error(),
				"type":    "server_error",
			},
		})
		return
	}

	if !allowed {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": gin.H{
				"message": "Insufficient quota or invalid API key",
				"type":    "insufficient_quota",
			},
		})
		return
	}

	// 1. 解析请求体 - 使用 Gin 标准的 ShouldBindJSON
	var req openai.ChatCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "Invalid request body: " + err.Error(),
				"type":    "invalid_request_error",
			},
		})
		return
	}

	// 2. 验证必需参数
	if req.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "model is required",
				"type":    "invalid_request_error",
			},
		})
		return
	}

	if len(req.Messages) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "messages is required",
				"type":    "invalid_request_error",
			},
		})
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

	// 4. 获取 Workers 列表（从注册中心）
	workers := h.registry.GetAvailableWorkers()
	if len(workers) == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{
				"message": "No workers available",
				"type":    "server_error",
			},
		})
		return
	}

	baseCtx := c.Request.Context()
	ctx := context.WithValue(baseCtx, core.TraceKey, traceID)
	selectedWorker, err := h.router.Select(ctx, workers, inferenceReq)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("Failed to select worker: %v", err),
				"type":    "server_error",
			},
		})
		return
	}

	c.Request = c.Request.WithContext(ctx)

	// 7. 根据是否流式执行请求
	if req.Stream {
		h.handleStreamRequest(c, selectedWorker, inferenceReq, apiKey)
	} else {
		h.handleNonStreamRequest(c, selectedWorker, inferenceReq, apiKey)
	}
}

func estimateTokens(text string) int {
	// 强制转换为 rune 切片，计算真实的字符数（而不是 UTF-8 字节数）
	return len([]rune(text))
}

// handleStreamRequest handles streaming responses
func (h *ChatHandler) handleStreamRequest(c *gin.Context, worker core.Worker, req *core.InferenceRequest, apiKey string) {
	// 设置 SSE 响应头 - 使用 Gin 标准方式
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // 禁用 Nginx 缓冲

	// 设置 HTTP 状态码
	c.Status(http.StatusOK)

	// Token 计数器
	totalTokens := 0
	maxAllowed := 50

	// 创建 sender 回调 - 必须使用 c.Writer.Write() 和 c.Writer.Flush()
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
			if err := writeSSEEvent(c, "error", errorData); err != nil {
				return fmt.Errorf("failed to write error event: %w", err)
			}
			return chunk.Error
		}

		// 累计 Token 数量（简单使用字符数估算）
		totalTokens += estimateTokens(chunk.Content)
		if totalTokens > maxAllowed {
			// 这里必须 return error！
			// 这会将错误抛给底层的 Worker，触发 defer resp.Body.Close()，瞬间断网！
			log.Printf("[网关拦截] 达到配额上限 %d，强行熔断连接！", maxAllowed)

			// 优雅地给前端发一个错误事件，告诉用户没钱了
			_ = writeSSEEvent(c, "error", map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Token quota exceeded mid-stream",
					"type":    "quota_error",
				},
			})
			return fmt.Errorf("quota exceeded")
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
		if err := writeSSEEvent(c, "data", response); err != nil {
			return fmt.Errorf("failed to write chunk: %w", err)
		}

		return nil
	}

	// 执行推理 - 透传 c.Request.Context()
	if err := worker.Execute(c.Request.Context(), req, senderFunc); err != nil {
		// 检查错误类型
		if errors.Is(err, http.ErrHandlerTimeout) || errors.Is(err, context.DeadlineExceeded) {
			// 超时错误
			_ = writeSSEEvent(c, "error", map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Request timeout",
					"type":    "timeout_error",
					"code":    "timeout",
				},
			})
			return
		}

		// 其他错误
		_ = writeSSEEvent(c, "error", map[string]interface{}{
			"error": map[string]interface{}{
				"message": err.Error(),
				"type":    "server_error",
				"code":    "internal_error",
			},
		})
		return
	}

	// 发送 [DONE] 标记
	_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))

	// 确保所有数据已刷新
	c.Writer.Flush()

	// 阶段二：请求完成后扣费
	_ = h.limiter.Consume(c.Request.Context(), apiKey, totalTokens)
}

// handleNonStreamRequest handles non-streaming responses
func (h *ChatHandler) handleNonStreamRequest(c *gin.Context, worker core.Worker, req *core.InferenceRequest, apiKey string) {
	var fullContent string
	totalTokens := 0

	// 创建 sender 回调，收集所有内容
	senderFunc := func(chunk core.StreamChunk) error {
		if chunk.Error != nil {
			return chunk.Error
		}
		fullContent += chunk.Content
		totalTokens += len(chunk.Content)
		return nil
	}

	// 执行推理 - 透传 c.Request.Context()
	if err := worker.Execute(c.Request.Context(), req, senderFunc); err != nil {
		if errors.Is(err, http.ErrHandlerTimeout) || errors.Is(err, context.DeadlineExceeded) {
			c.JSON(http.StatusRequestTimeout, gin.H{
				"error": gin.H{
					"message": "Request timeout",
					"type":    "timeout_error",
				},
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "server_error",
			},
		})
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

	// 使用 Gin 的 JSON 响应
	c.JSON(http.StatusOK, response)

	// 阶段二：请求完成后扣费
	_ = h.limiter.Consume(c.Request.Context(), apiKey, totalTokens)
}

// writeSSEEvent writes an SSE event to the Gin response writer
func writeSSEEvent(c *gin.Context, eventType string, data interface{}) error {
	// 序列化数据
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	// 写入事件行
	if eventType != "" && eventType != "data" {
		if _, err := c.Writer.Write([]byte(fmt.Sprintf("event: %s\n", eventType))); err != nil {
			return fmt.Errorf("failed to write event type: %w", err)
		}
	}

	// 写入数据行
	if _, err := c.Writer.Write([]byte(fmt.Sprintf("data: %s\n\n", jsonData))); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	// 立即刷新，确保数据推送给客户端 - 必须使用 c.Writer.Flush()
	c.Writer.Flush()

	return nil
}
