package core

import (
	"context"
	"testing"
	"time"
)

type mockExecutor struct{}

func (m *mockExecutor) Execute(ctx context.Context, req *InferenceRequest) <-chan StreamChunk {
	ch := make(chan StreamChunk)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				ch <- StreamChunk{Error: ctx.Err()}
				return
			case <-ticker.C:
				ch <- StreamChunk{Content: "chunk"}
			}
		}
	}()
	return ch
}

func TestExecuteContextCancellation(t *testing.T) {
	executor := &mockExecutor{}
	ctx, cancel := context.WithCancel(context.Background())

	req := &InferenceRequest{
		TraceID:  "test-001",
		Model:    "test-model",
		Messages: []string{"hello"},
	}

	ch := executor.Execute(ctx, req)

	// Wait a bit then cancel
	time.AfterFunc(50*time.Millisecond, cancel)

	// Verify first chunk is the cancellation error
	chunk, ok := <-ch
	if !ok {
		t.Fatal("channel closed unexpectedly")
	}

	if chunk.Error != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", chunk.Error)
	}
}
