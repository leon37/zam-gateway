package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"zam/core"
	"zam/handler"
	"zam/router"
	"zam/worker"
)

func main() {
	// 1. 初始化 Mock Workers
	workers := initMockWorkers()

	// 2. 初始化路由器
	scoreRouter := router.NewScoreRouter()

	// 3. 初始化 Handler
	chatHandler := handler.NewChatHandler(scoreRouter, workers)

	// 4. 设置路由
	mux := http.NewServeMux()

	// OpenAI 兼容的 API 端点
	mux.Handle("/v1/chat/completions", chatHandler)

	// 健康检查端点
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"workers": len(workers),
		})
	})

	// 5. 启动服务器
	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	addr := fmt.Sprintf(":%s", port)

	// 优雅关闭
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// 在 goroutine 中启动服务器
	go func() {
		log.Printf("Starting server on %s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// 等待中断信号以优雅关闭服务器
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// 设置超时上下文
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 关闭服务器
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}

// initMockWorkers 初始化 Mock Workers
func initMockWorkers() []core.Worker {
	var workers []core.Worker

	// 模拟 4070TiS Worker (12GB VRAM)
	workers = append(workers, &MockWorker{
		id:          "gpu-4070tis-01",
		models:      []string{"gpt-3.5-turbo", "gpt-4", "llama-7b", "llama-13b"},
		totalVRAM:   12 * 1024 * 1024 * 1024, // 12GB
		maxTasks:    2,
		activeTasks: 0,
	})

	// 模拟 2060 Worker (6GB VRAM)
	workers = append(workers, &MockWorker{
		id:          "gpu-2060-01",
		models:      []string{"gpt-3.5-turbo", "llama-7b"},
		totalVRAM:   6 * 1024 * 1024 * 1024, // 6GB
		maxTasks:    1,
		activeTasks: 0,
	})

	// 模拟 Fallback Cloud Worker
	workers = append(workers, &MockWorker{
		id:          "cloud-fallback",
		models:      []string{"*"}, // 支持所有模型
		totalVRAM:   0,              // 云端无 VRAM 限制
		maxTasks:    100,
		activeTasks: 0,
		isFallback:  true,
	})

	return workers
}

// MockWorker 是一个用于测试的 Mock Worker 实现
type MockWorker struct {
	id          string
	models      []string
	totalVRAM   uint64
	maxTasks    int
	activeTasks int
	isFallback  bool
}

func (m *MockWorker) ID() string {
	return m.id
}

func (m *MockWorker) Heartbeat(ctx context.Context) (core.WorkerProfile, error) {
	// 模拟 VRAM 使用：根据 activeTasks 计算
	availableVRAM := m.totalVRAM
	if m.totalVRAM > 0 {
		// 假设每个任务使用 2GB VRAM
		usedVRAM := uint64(m.activeTasks * 2 * 1024 * 1024 * 1024)
		if usedVRAM > m.totalVRAM {
			availableVRAM = 0
		} else {
			availableVRAM = m.totalVRAM - usedVRAM
		}
	}

	return core.WorkerProfile{
		WorkerID:      m.id,
		Supported:     m.models,
		TotalVRAM:     m.totalVRAM,
		AvailableVRAM: availableVRAM,
		ActiveTasks:   m.activeTasks,
		MaxTasks:      m.maxTasks,
	}, nil
}

func (m *MockWorker) Execute(ctx context.Context, req *core.InferenceRequest, sender func(chunk core.StreamChunk) error) error {
	// 增加 activeTasks 计数
	m.activeTasks++
	defer func() {
		m.activeTasks--
	}()

	// 模拟流式响应
	mockResponse := []string{
		"Hello", "!", " ", "I", " am", " a", " mock", " worker", " on", " ",
		m.id, ".", "\n",
		"I", " received", " your", " request", " for", " model", " '", req.Model, "'.",
		"\n",
		"This", " is", " a", " simulated", " streaming", " response", " to",
		" demonstrate", " the", " SSE", " functionality", ".",
	}

	for i, content := range mockResponse {
		// 模拟网络延迟
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}

		// 发送 chunk
		chunk := core.StreamChunk{
			Content:      content,
			FinishReason: "",
			Error:        nil,
		}

		// 最后一个 chunk 设置 finish_reason
		if i == len(mockResponse)-1 {
			chunk.FinishReason = "stop"
		}

		if err := sender(chunk); err != nil {
			return err
		}
	}

	return nil
}

// NewHTTPWorkerFactory 创建真实的 HTTP Worker
func NewHTTPWorkerFactory(id, url string) *worker.HTTPWorker {
	return worker.NewHTTPWorker(id, url)
}
