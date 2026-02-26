package core

import (
	"context"
	"sync"
	"time"
)

// RegisteredWorker wraps WorkerProfile with last heartbeat time
type RegisteredWorker struct {
	Profile  WorkerProfile
	Worker   Worker
	LastSeen time.Time
}

// WorkerRegistry defines the interface for dynamic worker registration
type WorkerRegistry interface {
	// Heartbeat registers or updates a worker's profile
	Heartbeat(profile WorkerProfile) error
	// GetAvailableWorkers returns all alive workers for router scheduling
	GetAvailableWorkers() []Worker
}

// InMemoryRegistry implements WorkerRegistry with thread-safe in-memory storage
type InMemoryRegistry struct {
	mu      sync.RWMutex
	workers map[string]*RegisteredWorker
}

// NewInMemoryRegistry creates a new InMemoryRegistry with a cleanup goroutine
func NewInMemoryRegistry(ctx context.Context) *InMemoryRegistry {
	registry := &InMemoryRegistry{
		workers: make(map[string]*RegisteredWorker),
	}

	// 启动清理协程：每 5 秒清理一次超时 15 秒的僵尸节点
	go registry.cleanupDeadWorkers(ctx)

	return registry
}

// Heartbeat registers or updates a worker's profile
func (r *InMemoryRegistry) Heartbeat(profile WorkerProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 查找已注册的 Worker
	if existing, exists := r.workers[profile.WorkerID]; exists {
		// 更新 Profile 和 LastSeen
		existing.Profile = profile
		existing.LastSeen = time.Now()
		return nil
	}

	// Worker 不存在，但 Heartbeat 不负责创建 Worker 实例
	// Worker 需要在首次注册时通过其他方式注入
	// 这里只记录 Profile 和 LastSeen
	r.workers[profile.WorkerID] = &RegisteredWorker{
		Profile:  profile,
		Worker:   nil, // 需要后续注入
		LastSeen: time.Now(),
	}

	return nil
}

// RegisterWorker manually registers a worker with its implementation
func (r *InMemoryRegistry) RegisterWorker(worker Worker, profile WorkerProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.workers[profile.WorkerID] = &RegisteredWorker{
		Profile:  profile,
		Worker:   worker,
		LastSeen: time.Now(),
	}

	return nil
}

// GetAvailableWorkers returns all alive workers
func (r *InMemoryRegistry) GetAvailableWorkers() []Worker {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var workers []Worker
	for _, rw := range r.workers {
		if rw.Worker != nil {
			workers = append(workers, rw.Worker)
		}
	}

	return workers
}

// cleanupDeadWorkers removes workers that haven't sent heartbeat for > 15 seconds
func (r *InMemoryRegistry) cleanupDeadWorkers(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Context 取消，优雅退出
			return
		case <-ticker.C:
			r.mu.Lock()
			now := time.Now()
			for workerID, rw := range r.workers {
				if now.Sub(rw.LastSeen) > 15*time.Second {
					// 超过 15 秒未心跳，清理僵尸节点
					delete(r.workers, workerID)
				}
			}
			r.mu.Unlock()
		}
	}
}
