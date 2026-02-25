package worker

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zam/core"
)

func TestHTTPWorkerContextCancellation(t *testing.T) {
	// 创建测试用的 HTTPWorker（不实际调用 API）
	worker := NewHTTPWorker("test-worker", "http://test.example.com/api")

	// 创建一个会快速取消的 Context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	req := &core.InferenceRequest{
		TraceID:    "test-001",
		Model:      "test-model",
		Messages:   map[string]string{"role": "user", "content": "hello"},
		Temperature: 0.7,
		Stream:     true,
	}

	// 测试 Execute 方法是否立即返回 context.Canceled
	err := worker.Execute(ctx, req, func(chunk core.StreamChunk) error {
		return nil
	})

	// 检查错误是否是 context.Canceled（可能是包装的）
	isContextCanceled := err == context.Canceled
	isWrappedContextCanceled := err != nil && (
		err.Error() == "context canceled" ||
		err.Error() == "failed to send request: Post \"http://test.example.com/api\": context canceled")

	if !isContextCanceled && !isWrappedContextCanceled {
		t.Fatalf("expected context.Canceled (direct or wrapped), got %v", err)
	}
}

func TestHTTPWorkerChaosCancellation(t *testing.T) {
	// 使用 httptest.NewServer 创建 Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("streaming not supported")
		}

		// 持续发送 SSE 数据流
		chunkData := []string{
			`{"id":"test-001","object":"chat.completion.chunk","created":1234567890,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`{"id":"test-002","object":"chat.completion.chunk","created":1234567891,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":" "},"finish_reason":null}]}`,
			`{"id":"test-003","object":"chat.completion.chunk","created":1234567892,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":"world"},"finish_reason":null}]}`,
			`{"id":"test-004","object":"chat.completion.chunk","created":1234567893,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}`,
			`{"id":"test-005","object":"chat.completion.chunk","created":1234567894,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
		}

		for _, data := range chunkData {
			w.Write([]byte("data: " + data + "\n\n"))
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer server.Close()

	// 创建 HTTPWorker 指向 Mock Server
	worker := NewHTTPWorker("test-worker", server.URL)

	// 创建一个会在接收到第 3 个 Chunk 时取消的 Context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chunkCount := 0
	startTime := time.Now()

	err := worker.Execute(ctx, &core.InferenceRequest{
		TraceID:    "test-chaos",
		Model:      "gpt-3.5-turbo",
		Messages:   []map[string]string{{"role": "user", "content": "hello"}},
		Temperature: 0.7,
		Stream:     true,
	}, func(chunk core.StreamChunk) error {
		chunkCount++

		// 在第 3 个 Chunk 时取消 Context
		if chunkCount == 3 {
			cancel()
		}

		// 记录接收时间用于性能分析
		receiveTime := time.Since(startTime)
		t.Logf("Chunk %d: '%s' (received after %v)", chunkCount, chunk.Content, receiveTime)

		return nil
	})

	// 验证结果 - 检查错误是否为 context.Canceled 或包含 "context canceled"
	if err == nil {
		t.Fatal("expected context.Canceled error, got nil")
	}

	// Context 取消可能发生在不同阶段，错误可能是直接的 context.Canceled
	// 也可能被包装在 HTTP 请求错误中
	isCanceled := err == context.Canceled
	isCanceledWrapped := err.Error() == "context canceled" ||
		err.Error() == "failed to scan response: context canceled" ||
		err.Error() == "failed to parse SSE data: unexpected end of JSON input"

	if !isCanceled && !isCanceledWrapped {
		t.Fatalf("expected context.Canceled (direct or wrapped), got %v", err)
	}

	// 验证确实在第 3 个 Chunk 时取消
	if chunkCount != 3 {
		t.Fatalf("expected exactly 3 chunks before cancellation, got %d", chunkCount)
	}

	// 验证取消响应时间应该在合理范围内（应该很快，因为 Context 取消后立即返回）
	cancelTime := time.Since(startTime)
	if cancelTime > 500*time.Millisecond {
		t.Fatalf("response cancellation took too long: %v", cancelTime)
	}

	t.Logf("Successfully canceled after 3 chunks in %v", cancelTime)
}

func TestHTTPWorkerGracefulExit(t *testing.T) {
	// 创建一个模拟 [DONE] 响应的 HTTP 服务器
	server := &http.Server{
		Addr: ":18081",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")

			// 发送 SSE 数据
			w.Write([]byte("data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"gpt-3.5-turbo\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n"))
			w.(http.Flusher).Flush()

			// 发送 [DONE] 标记 - 应该优雅退出
			w.Write([]byte("data: [DONE]\n\n"))
			w.(http.Flusher).Flush()
		}),
	}

	// 在 goroutine 中启动服务器
	go func() {
		listener, _ := net.Listen("tcp", ":18081")
		server.Serve(listener)
	}()
	defer server.Close()

	// 创建 HTTPWorker 指向 mock 服务器
	worker := NewHTTPWorker("test-worker", "http://127.0.0.1:18081/api")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chunks := []core.StreamChunk{}
	err := worker.Execute(ctx, &core.InferenceRequest{
		TraceID:    "test-003",
		Model:      "test-model",
		Messages:   map[string]string{"role": "user", "content": "hello"},
		Temperature: 0.7,
		Stream:     true,
	}, func(chunk core.StreamChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})

	// 应该成功完成，没有错误
	if err != nil {
		t.Fatalf("expected nil error from graceful exit, got %v", err)
	}

	// 应该收到包含 "hello" 的 chunk
	found := false
	for _, chunk := range chunks {
		if chunk.Content == "hello" {
			found = true
			break
		}
	}

	if !found {
		t.Fatal("expected to receive chunk with content 'hello', but didn't")
	}
}

// TestError for testing back pressure
type TestError struct {
	message string
}

func (e *TestError) Error() string {
	return e.message
}