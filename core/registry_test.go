package core

import (
	"context"
	"sync"
	"testing"
	"time"
)

// MockWorker 实现 Worker 接口用于测试
type MockWorker struct {
	id string
}

func (m *MockWorker) ID() string {
	return m.id
}

func (m *MockWorker) Heartbeat(ctx context.Context) (WorkerProfile, error) {
	return WorkerProfile{
		WorkerID: m.id,
	}, nil
}

func (m *MockWorker) Execute(ctx context.Context, req *InferenceRequest, sender func(chunk StreamChunk) error) error {
	return nil
}

func TestInMemoryRegistry_Heartbeat(t *testing.T) {
	// 创建测试 Context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建注册中心
	registry := NewInMemoryRegistry(ctx)

	// 测试 Worker Profile
	profile := WorkerProfile{
		WorkerID:      "worker-1",
		Supported:     []string{"gpt-3.5-turbo"},
		TotalVRAM:     12884901888,
		AvailableVRAM: 12884901888,
		ActiveTasks:   0,
		MaxTasks:      2,
	}

	// 发送心跳
	err := registry.Heartbeat(profile)
	if err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}

	// 验证：GetAvailableWorkers 应该返回空（因为 Heartbeat 只记录 Profile，Worker 为 nil）
	workers := registry.GetAvailableWorkers()
	if len(workers) != 0 {
		t.Errorf("Expected 0 available workers, got %d", len(workers))
	}
}

func TestInMemoryRegistry_RegisterWorker(t *testing.T) {
	// 创建测试 Context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建注册中心
	registry := NewInMemoryRegistry(ctx)

	// 创建 Mock Worker
	worker := &MockWorker{id: "worker-1"}

	// 注册 Worker
	profile := WorkerProfile{
		WorkerID:      "worker-1",
		Supported:     []string{"gpt-3.5-turbo"},
		TotalVRAM:     12884901888,
		AvailableVRAM: 12884901888,
		ActiveTasks:   0,
		MaxTasks:      2,
	}
	err := registry.RegisterWorker(worker, profile)
	if err != nil {
		t.Fatalf("RegisterWorker failed: %v", err)
	}

	// 验证：GetAvailableWorkers 应该返回 1 个 worker
	workers := registry.GetAvailableWorkers()
	if len(workers) != 1 {
		t.Errorf("Expected 1 available worker, got %d", len(workers))
	}

	// 验证 Worker ID
	if workers[0].ID() != "worker-1" {
		t.Errorf("Expected worker ID 'worker-1', got '%s'", workers[0].ID())
	}
}

func TestInMemoryRegistry_RegisterMultipleWorkers(t *testing.T) {
	// 创建测试 Context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建注册中心
	registry := NewInMemoryRegistry(ctx)

	// 创建多个 Mock Workers
	workers := []*MockWorker{
		{id: "worker-1"},
		{id: "worker-2"},
		{id: "worker-3"},
	}

	// 注册所有 Workers
	for i, w := range workers {
		profile := WorkerProfile{
			WorkerID:      w.ID(),
			Supported:     []string{"gpt-3.5-turbo"},
			TotalVRAM:     uint64((i + 1) * 1024 * 1024 * 1024),
			AvailableVRAM: uint64((i + 1) * 1024 * 1024 * 1024),
			ActiveTasks:   0,
			MaxTasks:      2,
		}
		err := registry.RegisterWorker(w, profile)
		if err != nil {
			t.Fatalf("RegisterWorker failed for %s: %v", w.ID(), err)
		}
	}

	// 验证：GetAvailableWorkers 应该返回 3 个 workers
	availableWorkers := registry.GetAvailableWorkers()
	if len(availableWorkers) != 3 {
		t.Errorf("Expected 3 available workers, got %d", len(availableWorkers))
	}
}

func TestInMemoryRegistry_HeartbeatUpdate(t *testing.T) {
	// 创建测试 Context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建注册中心
	registry := NewInMemoryRegistry(ctx)

	// 创建 Mock Worker
	worker := &MockWorker{id: "worker-1"}

	// 注册 Worker（初始 AvailableVRAM = 12884901888）
	profile1 := WorkerProfile{
		WorkerID:      "worker-1",
		Supported:     []string{"gpt-3.5-turbo"},
		TotalVRAM:     12884901888,
		AvailableVRAM: 12884901888,
		ActiveTasks:   0,
		MaxTasks:      2,
	}
	err := registry.RegisterWorker(worker, profile1)
	if err != nil {
		t.Fatalf("RegisterWorker failed: %v", err)
	}

	// 发送心跳更新（AvailableVRAM = 10737418240，ActiveTasks = 1）
	profile2 := WorkerProfile{
		WorkerID:      "worker-1",
		Supported:     []string{"gpt-3.5-turbo"},
		TotalVRAM:     12884901888,
		AvailableVRAM: 10737418240,
		ActiveTasks:   1,
		MaxTasks:      2,
	}
	err = registry.Heartbeat(profile2)
	if err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}

	// 验证：Worker 仍然存在
	workers := registry.GetAvailableWorkers()
	if len(workers) != 1 {
		t.Errorf("Expected 1 available worker, got %d", len(workers))
	}

	// 验证：LastSeen 已更新（注册中心内部状态）
	registry.mu.RLock()
	registeredWorker := registry.workers["worker-1"]
	registry.mu.RUnlock()

	if registeredWorker.Profile.AvailableVRAM != 10737418240 {
		t.Errorf("Expected AvailableVRAM 10737418240, got %d", registeredWorker.Profile.AvailableVRAM)
	}

	if registeredWorker.Profile.ActiveTasks != 1 {
		t.Errorf("Expected ActiveTasks 1, got %d", registeredWorker.Profile.ActiveTasks)
	}

	// 验证：LastSeen 在最近 1 秒内
	if time.Since(registeredWorker.LastSeen) > time.Second {
		t.Errorf("LastSeen should be very recent, but was %v ago", time.Since(registeredWorker.LastSeen))
	}
}

func TestInMemoryRegistry_CleanupDeadWorkers(t *testing.T) {
	// 创建测试 Context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建注册中心
	registry := NewInMemoryRegistry(ctx)

	// 创建 3 个 Workers
	workers := []*MockWorker{
		{id: "worker-1"},
		{id: "worker-2"},
		{id: "worker-3"},
	}

	// 注册所有 Workers
	for _, w := range workers {
		profile := WorkerProfile{
			WorkerID:  w.ID(),
			Supported: []string{"gpt-3.5-turbo"},
			MaxTasks:   2,
		}
		err := registry.RegisterWorker(w, profile)
		if err != nil {
			t.Fatalf("RegisterWorker failed for %s: %v", w.ID(), err)
		}
	}

	// 验证：3 个 Workers
	workersBefore := registry.GetAvailableWorkers()
	if len(workersBefore) != 3 {
		t.Errorf("Expected 3 available workers, got %d", len(workersBefore))
	}

	// 手动更新 LastSeen，模拟 worker-2 和 worker-3 超时
	registry.mu.Lock()
	registry.workers["worker-1"].LastSeen = time.Now()                          // 活跃
	registry.workers["worker-2"].LastSeen = time.Now().Add(-20 * time.Second) // 超时
	registry.workers["worker-3"].LastSeen = time.Now().Add(-20 * time.Second) // 超时
	registry.mu.Unlock()

	// 等待清理协程运行（清理间隔是 5 秒）
	time.Sleep(6 * time.Second)

	// 验证：只有 worker-1 存活
	workersAfter := registry.GetAvailableWorkers()
	if len(workersAfter) != 1 {
		t.Errorf("Expected 1 available worker after cleanup, got %d", len(workersAfter))
	}

	if len(workersAfter) > 0 && workersAfter[0].ID() != "worker-1" {
		t.Errorf("Expected remaining worker ID 'worker-1', got '%s'", workersAfter[0].ID())
	}
}

func TestInMemoryRegistry_CleanupOnContextCancel(t *testing.T) {
	// 创建测试 Context
	ctx, cancel := context.WithCancel(context.Background())

	// 创建注册中心
	registry := NewInMemoryRegistry(ctx)

	// 注册一个 Worker
	worker := &MockWorker{id: "worker-1"}
	profile := WorkerProfile{
		WorkerID:  "worker-1",
		Supported: []string{"gpt-3.5-turbo"},
		MaxTasks:   2,
	}
	err := registry.RegisterWorker(worker, profile)
	if err != nil {
		t.Fatalf("RegisterWorker failed: %v", err)
	}

	// 取消 Context（模拟服务关闭）
	cancel()

	// 等待清理协程退出
	time.Sleep(1 * time.Second)

	// 验证：Worker 仍然存在（因为服务关闭了，不再清理）
	workers := registry.GetAvailableWorkers()
	if len(workers) != 1 {
		t.Errorf("Expected 1 available worker after context cancel, got %d", len(workers))
	}
}

func TestInMemoryRegistry_ConcurrentAccess(t *testing.T) {
	// 创建测试 Context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建注册中心
	registry := NewInMemoryRegistry(ctx)

	// 创建大量 Workers
	numWorkers := 100
	var wg sync.WaitGroup

	// 并发注册 Workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			worker := &MockWorker{id: "worker-" + string(rune('0'+index))}
			profile := WorkerProfile{
				WorkerID:  worker.ID(),
				Supported: []string{"gpt-3.5-turbo"},
				MaxTasks:   2,
			}

			err := registry.RegisterWorker(worker, profile)
			if err != nil {
				t.Errorf("RegisterWorker failed: %v", err)
			}
		}(i)
	}

	// 并发获取 Workers
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = registry.GetAvailableWorkers()
		}()
	}

	// 并发发送心跳
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			profile := WorkerProfile{
				WorkerID:    "worker-" + string(rune('0'+index%numWorkers)),
				Supported:   []string{"gpt-3.5-turbo"},
				ActiveTasks: index,
				MaxTasks:    2,
			}
			_ = registry.Heartbeat(profile)
		}(i)
	}

	// 等待所有 goroutine 完成
	wg.Wait()

	// 验证：至少有一部分 Worker 存在（可能有重复 ID 覆盖）
	workers := registry.GetAvailableWorkers()
	if len(workers) == 0 {
		t.Error("Expected some workers to be registered, got 0")
	}
}

func TestInMemoryRegistry_UpdateNonExistentWorker(t *testing.T) {
	// 创建测试 Context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建注册中心
	registry := NewInMemoryRegistry(ctx)

	// 尝试对不存在的 Worker 发送心跳
	profile := WorkerProfile{
		WorkerID:  "non-existent",
		Supported: []string{"gpt-3.5-turbo"},
		MaxTasks:   2,
	}
	err := registry.Heartbeat(profile)
	if err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}

	// 验证：没有 Worker 被创建（因为 Worker 为 nil）
	workers := registry.GetAvailableWorkers()
	if len(workers) != 0 {
		t.Errorf("Expected 0 available workers, got %d", len(workers))
	}
}
