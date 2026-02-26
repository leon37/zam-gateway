package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"zam/core"
	"zam/openai"
)

type HTTPWorker struct {
	id         string
	URL        string
	HTTPClient *http.Client
}

func NewHTTPWorker(id, url string) *HTTPWorker {
	return &HTTPWorker{
		id:  id,
		URL: url,
		HTTPClient: &http.Client{
			Timeout: 0, // 不设置超时，由外部控制
		},
	}
}

func (w *HTTPWorker) ID() string {
	return w.id
}

func (w *HTTPWorker) Heartbeat(ctx context.Context) (core.WorkerProfile, error) {
	// TODO: 实现心跳检测
	profile := core.WorkerProfile{
		WorkerID:      w.id,
		Supported:     []string{"gpt-3.5-turbo", "gpt-4"},
		TotalVRAM:     8192,
		AvailableVRAM: 4096,
		ActiveTasks:   1,
	}
	return profile, nil
}

func (w *HTTPWorker) Execute(ctx context.Context, req *core.InferenceRequest, sender func(chunk core.StreamChunk) error) error {
	traceID, _ := ctx.Value(core.TraceKey).(string)
	if traceID == "" {
		traceID = "unknown"
	}

	log.Printf("[Worker %s] [TraceID: %s] 收到请求，开始物理调用...", w.ID, traceID)

	// 创建请求体
	requestBody, err := json.Marshal(map[string]interface{}{
		"model":       req.Model,
		"messages":    req.Messages,
		"temperature": req.Temperature,
		"stream":      req.Stream,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// 创建 HTTP 请求，使用外部 Context
	httpReq, err := http.NewRequestWithContext(ctx, "POST", w.URL, strings.NewReader(string(requestBody)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")

	// 发送请求
	resp, err := w.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	// FD 泄漏防护：必须关闭 Body
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// 创建 SSE 扫描器
	scanner := bufio.NewScanner(resp.Body)
	// 扩容 Scanner 缓冲区：初始 1MB，最大允许 8MB 的单行 SSE 报文 (防止大模型长思考/Base64把网关撑爆)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 8*1024*1024)
	var lineBuffer []string

	// 主循环：处理 SSE 流
	for scanner.Scan() {
		// Context 自毁引信：检查是否已取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// 继续处理
		}

		line := scanner.Text()

		// 空行表示消息结束
		if line == "" {
			if len(lineBuffer) > 0 {
				if err := processSSEMessage(lineBuffer, sender); err != nil {
					// 背压熔断：sender 返回错误时立即停止
					return err
				}
				lineBuffer = lineBuffer[:0] // 清空缓冲区
			}
			continue
		}

		lineBuffer = append(lineBuffer, line)
	}

	// 检查扫描错误
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to scan response: %w", err)
	}

	// 处理最后一个未完成的消息
	if len(lineBuffer) > 0 {
		if err := processSSEMessage(lineBuffer, sender); err != nil {
			return err
		}
	}

	return nil
}

// processSSEMessage 处理 SSE 消息并调用 sender
func processSSEMessage(lines []string, sender func(chunk core.StreamChunk) error) error {
	var data string

	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
			break
		}
	}

	// 检查 [DONE] 标记 - 优雅退出
	if data == "[DONE]" {
		return nil
	}

	// 解析 JSON 响应
	var response openai.ChatCompletionStreamResponse
	if err := json.Unmarshal([]byte(data), &response); err != nil {
		return fmt.Errorf("failed to parse SSE data: %w", err)
	}

	// 处理流式响应
	for _, choice := range response.Choices {
		// 检查 Context 是否已取消
		chunk := core.StreamChunk{
			Content:      choice.Delta.Content,
			FinishReason: "",
			Error:        nil,
		}

		if choice.FinishReason != nil {
			chunk.FinishReason = *choice.FinishReason
		}

		// 背压熔断：sender 返回错误时立即停止
		if err := sender(chunk); err != nil {
			return err
		}
	}

	return nil
}
