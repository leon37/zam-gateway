package router

import (
	"context"
	"testing"

	"zam/core"
)

// mockWorker implements core.Worker for testing
type mockWorker struct {
	id         string
	profile    core.WorkerProfile
	heartbeatErr error
}

func (m *mockWorker) ID() string {
	return m.id
}

func (m *mockWorker) Heartbeat(ctx context.Context) (core.WorkerProfile, error) {
	if m.heartbeatErr != nil {
		return core.WorkerProfile{}, m.heartbeatErr
	}
	return m.profile, nil
}

func (m *mockWorker) Execute(ctx context.Context, req *core.InferenceRequest, sender func(chunk core.StreamChunk) error) error {
	return nil
}

// Test table-driven tests
func TestScoreRouter_Select(t *testing.T) {
	tests := []struct {
		name         string
		workers      []core.Worker
		req          *core.InferenceRequest
		expectedID   string
		description  string
	}{
		{
			name: "显存隔离-8B模型路由到4070TiS",
			workers: []core.Worker{
				// 4070TiS: 16GB VRAM, MaxTasks=20, supports Llama-8B
				&mockWorker{
					id: "local-4070tis",
					profile: core.WorkerProfile{
						WorkerID:      "local-4070tis",
						Supported:     []string{"llama-8b", "llama-7b", "gemma-2b"},
						TotalVRAM:     16 * 1024 * 1024 * 1024,
						AvailableVRAM: 14 * 1024 * 1024 * 1024,
						ActiveTasks:   2,
						MaxTasks:      20,
					},
				},
				// 2060: 6GB VRAM, MaxTasks=5, supports Llama-8B but insufficient VRAM
				&mockWorker{
					id: "local-2060",
					profile: core.WorkerProfile{
						WorkerID:      "local-2060",
						Supported:     []string{"llama-8b", "gemma-2b"},
						TotalVRAM:     6 * 1024 * 1024 * 1024,
						AvailableVRAM: 5 * 1024 * 1024 * 1024,
						ActiveTasks:   0,
						MaxTasks:      5,
					},
				},
				// Cloud fallback
				&mockWorker{
					id: "cloud-openai-fallback",
					profile: core.WorkerProfile{
						WorkerID:      "cloud-openai-fallback",
						Supported:     []string{"*"},
						TotalVRAM:     0,
						AvailableVRAM: 0,
						ActiveTasks:   0,
						MaxTasks:      0,
					},
				},
			},
			req: &core.InferenceRequest{
				TraceID: "test-001",
				Model:   "llama-8b",
			},
			expectedID:  "local-4070tis",
			description: "2060被显存过滤剔除，4070TiS成为唯一候选",
		},
		{
			name: "负载均衡-小模型路由到闲置节点",
			workers: []core.Worker{
				// 4070TiS: MaxTasks=20, supports Gemma-2B but heavily loaded (10/20 = 50%)
				&mockWorker{
					id: "local-4070tis",
					profile: core.WorkerProfile{
						WorkerID:      "local-4070tis",
						Supported:     []string{"llama-8b", "gemma-2b"},
						TotalVRAM:     16 * 1024 * 1024 * 1024,
						AvailableVRAM: 12 * 1024 * 1024 * 1024,
						ActiveTasks:   10,
						MaxTasks:      20,
					},
				},
				// 2060: MaxTasks=5, supports Gemma-2B, idle (0/5 = 0%)
				&mockWorker{
					id: "local-2060",
					profile: core.WorkerProfile{
						WorkerID:      "local-2060",
						Supported:     []string{"gemma-2b"},
						TotalVRAM:     6 * 1024 * 1024 * 1024,
						AvailableVRAM: 5 * 1024 * 1024 * 1024,
						ActiveTasks:   0,
						MaxTasks:      5,
					},
				},
				// Cloud fallback
				&mockWorker{
					id: "cloud-gpt4-fallback",
					profile: core.WorkerProfile{
						WorkerID:      "cloud-gpt4-fallback",
						Supported:     []string{"*"},
						TotalVRAM:     0,
						AvailableVRAM: 0,
						ActiveTasks:   0,
						MaxTasks:      0,
					},
				},
			},
			req: &core.InferenceRequest{
				TraceID: "test-002",
				Model:   "gemma-2b",
			},
			expectedID:  "local-2060",
			description: "4070TiS负载50%，2060闲置0%，2060得分更高",
		},
		{
			name: "灾难降级-本地节点全不可用返回Fallback",
			workers: []core.Worker{
				// 4070TiS: MaxTasks=20, supports Llama-8B but VRAM exhausted
				&mockWorker{
					id: "local-4070tis",
					profile: core.WorkerProfile{
						WorkerID:      "local-4070tis",
						Supported:     []string{"llama-8b"},
						TotalVRAM:     16 * 1024 * 1024 * 1024,
						AvailableVRAM: 1 * 1024 * 1024 * 1024, // Only 1GB left
						ActiveTasks:   5,
						MaxTasks:      20,
					},
				},
				// 2060: heartbeat fails (simulated network issue)
				&mockWorker{
					id: "local-2060",
					profile: core.WorkerProfile{
						WorkerID:      "local-2060",
						Supported:     []string{"llama-8b"},
						TotalVRAM:     6 * 1024 * 1024 * 1024,
						AvailableVRAM: 0,
						ActiveTasks:   0,
						MaxTasks:      5,
					},
					heartbeatErr: context.Canceled,
				},
				// Cloud fallback - the savior
				&mockWorker{
					id: "cloud-azure-fallback",
					profile: core.WorkerProfile{
						WorkerID:      "cloud-azure-fallback",
						Supported:     []string{"*"},
						TotalVRAM:     0,
						AvailableVRAM: 0,
						ActiveTasks:   0,
						MaxTasks:      0,
					},
				},
			},
			req: &core.InferenceRequest{
				TraceID: "test-003",
				Model:   "llama-8b",
			},
			expectedID:  "cloud-azure-fallback",
			description: "本地节点全部失败，自动降级到云兜底节点",
		},
		{
			name: "模型不支持-全部被过滤返回Fallback",
			workers: []core.Worker{
				// 4070TiS: does not support requested model
				&mockWorker{
					id: "local-4070tis",
					profile: core.WorkerProfile{
						WorkerID:      "local-4070tis",
						Supported:     []string{"gemma-2b"},
						TotalVRAM:     16 * 1024 * 1024 * 1024,
						AvailableVRAM: 14 * 1024 * 1024 * 1024,
						ActiveTasks:   0,
						MaxTasks:      20,
					},
				},
				// 2060: does not support requested model
				&mockWorker{
					id: "local-2060",
					profile: core.WorkerProfile{
						WorkerID:      "local-2060",
						Supported:     []string{"gemma-2b"},
						TotalVRAM:     6 * 1024 * 1024 * 1024,
						AvailableVRAM: 5 * 1024 * 1024 * 1024,
						ActiveTasks:   0,
						MaxTasks:      5,
					},
				},
				// Cloud fallback supports everything
				&mockWorker{
					id: "cloud-anthropic-fallback",
					profile: core.WorkerProfile{
						WorkerID:      "cloud-anthropic-fallback",
						Supported:     []string{"*"},
						TotalVRAM:     0,
						AvailableVRAM: 0,
						ActiveTasks:   0,
						MaxTasks:      0,
					},
				},
			},
			req: &core.InferenceRequest{
				TraceID: "test-004",
				Model:   "llama-8b",
			},
			expectedID:  "cloud-anthropic-fallback",
			description: "请求的本地模型不支持，自动路由到云节点",
		},
		{
			name: "70B大模型-显存严格过滤",
			workers: []core.Worker{
				// 4070TiS: 16GB, not enough for 70B
				&mockWorker{
					id: "local-4070tis",
					profile: core.WorkerProfile{
						WorkerID:      "local-4070tis",
						Supported:     []string{"llama-70b"},
						TotalVRAM:     16 * 1024 * 1024 * 1024,
						AvailableVRAM: 15 * 1024 * 1024 * 1024,
						ActiveTasks:   0,
						MaxTasks:      20,
					},
				},
				// Cloud fallback
				&mockWorker{
					id: "cloud-huggingface-fallback",
					profile: core.WorkerProfile{
						WorkerID:      "cloud-huggingface-fallback",
						Supported:     []string{"*"},
						TotalVRAM:     0,
						AvailableVRAM: 0,
						ActiveTasks:   0,
						MaxTasks:      0,
					},
				},
			},
			req: &core.InferenceRequest{
				TraceID: "test-005",
				Model:   "llama-70b",
			},
			expectedID:  "cloud-huggingface-fallback",
			description: "70B模型需要40GB+，4070TiS 16GB显存不足被过滤",
		},
		{
			name: "无fallback-无可用节点时返回错误",
			workers: []core.Worker{
				// Only local worker, insufficient VRAM
				&mockWorker{
					id: "local-2060",
					profile: core.WorkerProfile{
						WorkerID:      "local-2060",
						Supported:     []string{"llama-8b"},
						TotalVRAM:     6 * 1024 * 1024 * 1024,
						AvailableVRAM: 1 * 1024 * 1024 * 1024,
						ActiveTasks:   0,
						MaxTasks:      5,
					},
				},
			},
			req: &core.InferenceRequest{
				TraceID: "test-006",
				Model:   "llama-8b",
			},
			expectedID:  "",
			description: "无可用节点且无fallback时返回错误",
		},
		{
			name: "达到最大并发-硬过滤触发Fallback",
			workers: []core.Worker{
				// 2060: MaxTasks=5, currently at 5 (at capacity)
				&mockWorker{
					id: "local-2060",
					profile: core.WorkerProfile{
						WorkerID:      "local-2060",
						Supported:     []string{"gemma-2b"},
						TotalVRAM:     6 * 1024 * 1024 * 1024,
						AvailableVRAM: 5 * 1024 * 1024 * 1024,
						ActiveTasks:   5,
						MaxTasks:      5,
					},
				},
				// Cloud fallback
				&mockWorker{
					id: "cloud-gemini-fallback",
					profile: core.WorkerProfile{
						WorkerID:      "cloud-gemini-fallback",
						Supported:     []string{"*"},
						TotalVRAM:     0,
						AvailableVRAM: 0,
						ActiveTasks:   0,
						MaxTasks:      0,
					},
				},
			},
			req: &core.InferenceRequest{
				TraceID: "test-007",
				Model:   "gemma-2b",
			},
			expectedID:  "cloud-gemini-fallback",
			description: "2060达到最大并发5/5，硬过滤触发云降级",
		},
		{
			name: "物理边界-4070TiS高并发仍可调度",
			workers: []core.Worker{
				// 4070TiS: MaxTasks=20, currently at 18/20 = 90% load
				&mockWorker{
					id: "local-4070tis",
					profile: core.WorkerProfile{
						WorkerID:      "local-4070tis",
						Supported:     []string{"llama-8b", "gemma-2b"},
						TotalVRAM:     16 * 1024 * 1024 * 1024,
						AvailableVRAM: 14 * 1024 * 1024 * 1024,
						ActiveTasks:   18,
						MaxTasks:      20,
					},
				},
				// 2060: MaxTasks=5, currently at 0/5 = 0% load
				&mockWorker{
					id: "local-2060",
					profile: core.WorkerProfile{
						WorkerID:      "local-2060",
						Supported:     []string{"gemma-2b"},
						TotalVRAM:     6 * 1024 * 1024 * 1024,
						AvailableVRAM: 5 * 1024 * 1024 * 1024,
						ActiveTasks:   0,
						MaxTasks:      5,
					},
				},
				// Cloud fallback
				&mockWorker{
					id: "cloud-cohere-fallback",
					profile: core.WorkerProfile{
						WorkerID:      "cloud-cohere-fallback",
						Supported:     []string{"*"},
						TotalVRAM:     0,
						AvailableVRAM: 0,
						ActiveTasks:   0,
						MaxTasks:      0,
					},
				},
			},
			req: &core.InferenceRequest{
				TraceID: "test-008",
				Model:   "gemma-2b",
			},
			expectedID:  "local-2060",
			description: "4070TiS 18/20=10%容量，2060 0/5=100%容量，路由到2060",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewScoreRouter()
			ctx := context.Background()

			selected, err := router.Select(ctx, tt.workers, tt.req)

			if tt.expectedID == "" {
				// Expect error case
				if err == nil {
					t.Errorf("%s: expected error but got none", tt.description)
				}
				return
			}

			// Expect valid selection
			if err != nil {
				t.Errorf("%s: Select() error = %v", tt.description, err)
				return
			}

			if selected.ID() != tt.expectedID {
				t.Errorf("%s: expected worker ID %s, got %s", tt.description, tt.expectedID, selected.ID())
			}
		})
	}
}

// TestEstimateModelVRAM tests the VRAM estimation function
func TestEstimateModelVRAM(t *testing.T) {
	tests := []struct {
		model       string
		minExpected uint64
		maxExpected uint64
	}{
		{"llama-8b", 5 * 1024 * 1024 * 1024, 7 * 1024 * 1024 * 1024}, // ~6GB
		{"llama-7b", 5 * 1024 * 1024 * 1024, 7 * 1024 * 1024 * 1024}, // ~6GB
		{"llama-13b", 11 * 1024 * 1024 * 1024, 13 * 1024 * 1024 * 1024}, // ~12GB
		{"llama-30b", 19 * 1024 * 1024 * 1024, 21 * 1024 * 1024 * 1024}, // ~20GB
		{"llama-70b", 39 * 1024 * 1024 * 1024, 41 * 1024 * 1024 * 1024}, // ~40GB
		{"gemma-2b", 1 * 1024 * 1024 * 1024, 3 * 1024 * 1024 * 1024}, // ~2GB
		{"unknown-model", 1 * 1024 * 1024 * 1024, 3 * 1024 * 1024 * 1024}, // ~2GB default
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			estimated := estimateModelVRAM(tt.model)
			if estimated < tt.minExpected || estimated > tt.maxExpected {
				t.Errorf("Model %s: estimated VRAM %d out of range [%d, %d]",
					tt.model, estimated, tt.minExpected, tt.maxExpected)
			}
		})
	}
}

// TestCalculateVRAMScore tests the VRAM scoring function (linear normalization)
func TestCalculateVRAMScore(t *testing.T) {
	tests := []struct {
		totalVRAM   uint64
		available   uint64
		minScore    float64
		maxScore    float64
	}{
		{16 * 1024 * 1024 * 1024, 14 * 1024 * 1024 * 1024, 86, 88},     // 14/16 = 87.5%
		{16 * 1024 * 1024 * 1024, 8 * 1024 * 1024 * 1024, 49, 51},       // 8/16 = 50%
		{16 * 1024 * 1024 * 1024, 4 * 1024 * 1024 * 1024, 24, 26},       // 4/16 = 25%
		{6 * 1024 * 1024 * 1024, 5 * 1024 * 1024 * 1024, 82, 84},        // 5/6 = 83.33%
		{6 * 1024 * 1024 * 1024, 1 * 1024 * 1024 * 1024, 16, 17},        // 1/6 = 16.67%
		{6 * 1024 * 1024 * 1024, 0, 0, 0},                                 // 0/6 = 0%
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			score := calculateVRAMScore(tt.available, tt.totalVRAM)
			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("Available %d / Total %d: score %.2f out of range [%.2f, %.2f]",
					tt.available, tt.totalVRAM, score, tt.minScore, tt.maxScore)
			}
		})
	}
}

// TestCalculateLoadScore tests the load scoring function (linear normalization)
func TestCalculateLoadScore(t *testing.T) {
	tests := []struct {
		activeTasks int
		maxTasks    int
		minScore    float64
		maxScore    float64
	}{
		{0, 20, 99, 100},    // 20/20 = 100% available
		{5, 20, 74, 76},     // 15/20 = 75% available
		{10, 20, 49, 51},    // 10/20 = 50% available
		{15, 20, 24, 26},    // 5/20 = 25% available
		{19, 20, 4, 6},      // 1/20 = 5% available
		{0, 5, 99, 100},     // 5/5 = 100% available
		{2, 5, 59, 61},      // 3/5 = 60% available
		{4, 5, 19, 21},      // 1/5 = 20% available
		{5, 5, 0, 0},        // 0/5 = 0% available (at max)
		{10, 20, 49, 51},    // 10/20 = 50% available
		{20, 20, 0, 0},      // 0/20 = 0% available (at max)
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			score := calculateLoadScore(tt.activeTasks, tt.maxTasks)
			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("ActiveTasks %d / MaxTasks %d: score %.2f out of range [%.2f, %.2f]",
					tt.activeTasks, tt.maxTasks, score, tt.minScore, tt.maxScore)
			}
		})
	}
}

// TestIsModelSupported tests the model support checking function
func TestIsModelSupported(t *testing.T) {
	tests := []struct {
		supported  []string
		model      string
		expected   bool
	}{
		{[]string{"llama-8b", "gemma-2b"}, "llama-8b", true},
		{[]string{"llama-8b", "gemma-2b"}, "Llama-8B", true}, // Case insensitive
		{[]string{"*"}, "any-model", true},                    // Wildcard
		{[]string{"llama-8b", "gemma-2b"}, "llama-70b", false},
		{[]string{}, "any-model", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := isModelSupported(tt.model, tt.supported)
			if result != tt.expected {
				t.Errorf("Model %s in %v: expected %v, got %v",
					tt.model, tt.supported, tt.expected, result)
			}
		})
	}
}

// TestIsFallbackWorker tests the fallback worker detection function
func TestIsFallbackWorker(t *testing.T) {
	tests := []struct {
		workerID  string
		expected  bool
	}{
		{"cloud-openai-fallback", true},
		{"cloud-gpt4", true},
		{"local-worker-fallback", true},
		{"local-4070tis", false},
		{"remote-edge", false},
		{"CLOUD-UPPERCASE", true}, // Case insensitive
	}

	for _, tt := range tests {
		t.Run(tt.workerID, func(t *testing.T) {
			result := isFallbackWorker(tt.workerID)
			if result != tt.expected {
				t.Errorf("WorkerID %s: expected %v, got %v",
					tt.workerID, tt.expected, result)
			}
		})
	}
}
