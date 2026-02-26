package core

import (
	"context"
	"sync"
)

// InMemoryRateLimiter implements RateLimiter interface with thread-safe in-memory storage
type InMemoryRateLimiter struct {
	mu      sync.RWMutex
	balances map[string]int
}

// NewInMemoryRateLimiter creates a new InMemoryRateLimiter with a test account
func NewInMemoryRateLimiter() *InMemoryRateLimiter {
	rl := &InMemoryRateLimiter{
		balances: make(map[string]int),
	}
	// 硬编码测试账户：test-key-123，初始余额 100 个 Token
	rl.balances["test-key-123"] = 100
	return rl
}

// Allow performs pre-flight check to verify if the API key exists and has sufficient balance
func (r *InMemoryRateLimiter) Allow(ctx context.Context, apiKey string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 如果 apiKey 不存在，直接返回 false
	balance, exists := r.balances[apiKey]
	if !exists {
		return false, nil
	}

	// 检查余额是否 > 0
	return balance > 0, nil
}

// Consume deducts the actual token consumption from the API key's balance
// Balance is allowed to go negative (overdraft)
func (r *InMemoryRateLimiter) Consume(ctx context.Context, apiKey string, actualTokens int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 直接扣除，允许余额为负数（透支）
	r.balances[apiKey] -= actualTokens
	return nil
}
