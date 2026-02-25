package core

import "context"

// WorkerProfile represents a worker's current state and capabilities
type WorkerProfile struct {
	WorkerID      string
	Supported     []string
	TotalVRAM     uint64
	AvailableVRAM uint64
	ActiveTasks   int
	MaxTasks      int // Maximum concurrent tasks this worker can handle
}

// StreamChunk represents a single chunk of streaming response
type StreamChunk struct {
	Content      string
	FinishReason string
	Error        error
}

// InferenceRequest represents an inference request
type InferenceRequest struct {
	TraceID     string
	Model       string
	Messages    interface{}
	Temperature float32
	Stream      bool
}

// Worker defines the interface for inference workers
type Worker interface {
	ID() string
	Heartbeat(ctx context.Context) (WorkerProfile, error)
	Execute(ctx context.Context, req *InferenceRequest, sender func(chunk StreamChunk) error) error
}

// Router defines the interface for routing inference requests to workers
type Router interface {
	Select(ctx context.Context, workers []Worker, req *InferenceRequest) (Worker, error)
}

type RateLimiter interface {
	Allow(ctx context.Context, apiKey string) (bool, error)
	Consume(ctx context.Context, apiKey string, actualTokens int) error
}
