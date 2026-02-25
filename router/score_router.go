package router

import (
	"context"
	"fmt"
	"strings"

	"zam/core"
)

// ScoreRouter implements core.Router with dynamic scoring based routing
type ScoreRouter struct {
	// vramWeight defines the weight for VRAM in scoring (higher = more important)
	vramWeight float64
	// loadWeight defines the weight for active tasks in scoring (higher = more important)
	loadWeight float64
}

// NewScoreRouter creates a new ScoreRouter with default weights
func NewScoreRouter() *ScoreRouter {
	return &ScoreRouter{
		vramWeight: 1.0,
		loadWeight: 1.0,
	}
}

// Select chooses the best worker for the given request
func (r *ScoreRouter) Select(ctx context.Context, workers []core.Worker, req *core.InferenceRequest) (core.Worker, error) {
	var fallbackWorker core.Worker
	var candidateWorkers []workerScore

	// Required VRAM for the requested model
	requiredVRAM := estimateModelVRAM(req.Model)

	// Phase 1: Pre-filtering and collect candidates
	for _, worker := range workers {
		profile, err := worker.Heartbeat(ctx)
		if err != nil {
			// Skip worker on heartbeat error
			continue
		}

		// Identify fallback/cloud worker
		if isFallbackWorker(worker.ID()) {
			fallbackWorker = worker
			continue
		}

		// Hard filter: check model support
		if !isModelSupported(req.Model, profile.Supported) {
			continue
		}

		// Hard filter: check VRAM availability
		if profile.AvailableVRAM < requiredVRAM {
			continue
		}

		// Hard filter: check if worker is at max capacity
		if profile.ActiveTasks >= profile.MaxTasks {
			continue
		}

		// Pass all filters, add to candidate pool
		candidateWorkers = append(candidateWorkers, workerScore{
			worker:    worker,
			profile:   profile,
			vramScore: calculateVRAMScore(profile.AvailableVRAM, profile.TotalVRAM),
			loadScore: calculateLoadScore(profile.ActiveTasks, profile.MaxTasks),
		})
	}

	// Phase 2: If no local candidates, return fallback
	if len(candidateWorkers) == 0 {
		if fallbackWorker != nil {
			return fallbackWorker, nil
		}
		return nil, fmt.Errorf("no available workers for request")
	}

	// Phase 3: Score and select best worker
	bestWorker := selectBestWorker(candidateWorkers, r.vramWeight, r.loadWeight)
	return bestWorker, nil
}

// workerScore holds a worker and its calculated scores
type workerScore struct {
	worker  core.Worker
	profile core.WorkerProfile
	vramScore float64
	loadScore float64
}

// estimateModelVRAM estimates required VRAM based on model name
func estimateModelVRAM(model string) uint64 {
	modelLower := strings.ToLower(model)

	// Large models (8B, 7B, 13B, etc.) require more VRAM
	if strings.Contains(modelLower, "8b") || strings.Contains(modelLower, "7b") {
		return 6 * 1024 * 1024 * 1024 // 6GB
	}
	if strings.Contains(modelLower, "13b") || strings.Contains(modelLower, "14b") {
		return 12 * 1024 * 1024 * 1024 // 12GB
	}
	if strings.Contains(modelLower, "30b") || strings.Contains(modelLower, "34b") || strings.Contains(modelLower, "32b") {
		return 20 * 1024 * 1024 * 1024 // 20GB
	}
	if strings.Contains(modelLower, "70b") || strings.Contains(modelLower, "72b") || strings.Contains(modelLower, "67b") {
		return 40 * 1024 * 1024 * 1024 // 40GB
	}

	// Small models or unknown models
	return 2 * 1024 * 1024 * 1024 // 2GB
}

// isModelSupported checks if the model is in the supported list
func isModelSupported(model string, supported []string) bool {
	for _, s := range supported {
		if s == "*" || strings.EqualFold(s, model) {
			return true
		}
	}
	return false
}

// isFallbackWorker checks if the worker is a fallback/cloud worker
func isFallbackWorker(workerID string) bool {
	idLower := strings.ToLower(workerID)
	return strings.Contains(idLower, "fallback") || strings.Contains(idLower, "cloud")
}

// calculateVRAMScore calculates score based on physical VRAM percentage
// Simple linear normalization: (AvailableVRAM / TotalVRAM) * 100
func calculateVRAMScore(availableVRAM, totalVRAM uint64) float64 {
	if totalVRAM == 0 {
		return 0
	}
	percentage := float64(availableVRAM) / float64(totalVRAM) * 100
	if percentage > 100 {
		percentage = 100
	}
	if percentage < 0 {
		percentage = 0
	}
	return percentage
}

// calculateLoadScore calculates score based on available capacity percentage
// Simple linear normalization: (MaxTasks - ActiveTasks) / MaxTasks * 100
// Returns 0 if ActiveTasks >= MaxTasks
func calculateLoadScore(activeTasks, maxTasks int) float64 {
	if maxTasks <= 0 {
		return 0
	}
	if activeTasks >= maxTasks {
		return 0
	}
	availableCapacity := float64(maxTasks - activeTasks) / float64(maxTasks) * 100
	if availableCapacity > 100 {
		availableCapacity = 100
	}
	return availableCapacity
}

// selectBestWorker selects the worker with highest combined score
func selectBestWorker(candidates []workerScore, vramWeight, loadWeight float64) core.Worker {
	var bestWorker core.Worker
	var bestScore float64 = -1

	for _, candidate := range candidates {
		// Combined weighted score
		totalScore := candidate.vramScore*vramWeight + candidate.loadScore*loadWeight

		if totalScore > bestScore {
			bestScore = totalScore
			bestWorker = candidate.worker
		}
	}

	return bestWorker
}
