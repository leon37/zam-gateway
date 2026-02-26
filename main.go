package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"zam/api"
	"zam/core"
	"zam/handler"
	"zam/router"
	"zam/worker"

	"github.com/gin-gonic/gin"
)

func main() {
	// 创建根 Context，用于优雅关闭所有后台协程
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. 初始化注册中心
	registry := core.NewInMemoryRegistry(ctx)

	// 2. 初始化 Mock Workers 并注册到注册中心
	_ = initMockWorkers(ctx, registry)

	// 3. 初始化路由器
	scoreRouter := router.NewScoreRouter()

	// 4. 初始化限流器
	rateLimiter := core.NewInMemoryRateLimiter()

	// 5. 初始化 Handler - 使用注册中心
	chatHandler := handler.NewChatHandlerWithRegistry(scoreRouter, registry, rateLimiter)

	// 6. 初始化 Worker API
	workerAPI := api.NewWorkerAPI(registry)

	// 7. 创建 Gin 路由引擎
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// 使用 Gin 的中间件
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

	// OpenAI 兼容的 API 端点
	r.POST("/v1/chat/completions", chatHandler.Handle)

	// Worker 心跳端点
	r.POST("/v1/workers/heartbeat", workerAPI.HandleHeartbeat)

	// 健康检查端点
	r.GET("/health", func(c *gin.Context) {
		workers := registry.GetAvailableWorkers()
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"workers": len(workers),
		})
	})

	// 8. 启动服务器
	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	addr := ":" + port

	// 创建 HTTP Server 用于优雅关闭
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// 在 goroutine 中启动服务器
	go func() {
		log.Printf("Starting server on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// 等待中断信号以优雅关闭服务器
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// 取消所有后台协程
	cancel()

	// 设置超时上下文
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()

	// 关闭服务器
	if err := srv.Shutdown(ctxShutdown); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}

// initMockWorkers 初始化 Mock Workers 并注册到注册中心
func initMockWorkers(ctx context.Context, registry *core.InMemoryRegistry) []core.Worker {
	var workers []core.Worker

	// 模拟 4070TiS Worker (12GB VRAM)
	w1 := &MockWorker{
		id:          "gpu-4070tis-01",
		models:      []string{"gpt-3.5-turbo", "gpt-4", "llama-7b", "llama-13b"},
		totalVRAM:   12 * 1024 * 1024 * 1024, // 12GB
		maxTasks:    2,
		activeTasks: 0,
	}
	workers = append(workers, w1)
	profile1 := core.WorkerProfile{
		WorkerID:      "gpu-4070tis-01",
		Supported:     []string{"gpt-3.5-turbo", "gpt-4", "llama-7b", "llama-13b"},
		TotalVRAM:     12 * 1024 * 1024 * 1024,
		AvailableVRAM: 12 * 1024 * 1024 * 1024,
		ActiveTasks:   0,
		MaxTasks:      2,
	}
	registry.RegisterWorker(w1, profile1)

	// 模拟 2060 Worker (6GB VRAM)
	w2 := &MockWorker{
		id:          "gpu-2060-01",
		models:      []string{"gpt-3.5-turbo", "llama-7b"},
		totalVRAM:   6 * 1024 * 1024 * 1024, // 6GB
		maxTasks:    1,
		activeTasks: 0,
	}
	workers = append(workers, w2)
	profile2 := core.WorkerProfile{
		WorkerID:      "gpu-2060-01",
		Supported:     []string{"gpt-3.5-turbo", "llama-7b"},
		TotalVRAM:     6 * 1024 * 1024 * 1024,
		AvailableVRAM: 6 * 1024 * 1024 * 1024,
		ActiveTasks:   0,
		MaxTasks:      1,
	}
	registry.RegisterWorker(w2, profile2)

	// 模拟 Fallback Cloud Worker
	w3 := &MockWorker{
		id:          "cloud-fallback",
		models:      []string{"*"}, // 支持所有模型
		totalVRAM:   0,              // 云端无 VRAM 限制
		maxTasks:    100,
		activeTasks: 0,
		isFallback:  true,
	}
	workers = append(workers, w3)
	profile3 := core.WorkerProfile{
		WorkerID:      "cloud-fallback",
		Supported:     []string{"*"},
		TotalVRAM:     0,
		AvailableVRAM: 0,
		ActiveTasks:   0,
		MaxTasks:      100,
	}
	registry.RegisterWorker(w3, profile3)

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
