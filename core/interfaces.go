package core

import "context"

// WorkerProfile represents a worker's current state and capabilities
type WorkerProfile struct {
	WorkerID      string
	Supported     []string
	TotalVRAM     uint64
	AvailableVRAM uint64
	ActiveTasks   int
}

// StreamChunk represents a single chunk of streaming response
type StreamChunk struct {
	Content      string
	FinishReason string
	Error        error
}

// InferenceRequest represents an inference request
type InferenceRequest struct {
	TraceID    string
	Model      string
	Messages   interface{}
	Temperature float32
	Stream     bool
}

// Worker defines the interface for inference workers
type Worker interface {
	ID() string
	Heartbeat(ctx context.Context) (WorkerProfile, error)
	Execute(ctx context.Context, req *InferenceRequest, sender func(chunk StreamChunk) error) error
}
