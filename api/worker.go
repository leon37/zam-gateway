package api

import (
	"net/http"

	"zam/core"

	"github.com/gin-gonic/gin"
)

// WorkerAPI handles worker-related API endpoints
type WorkerAPI struct {
	registry core.WorkerRegistry
}

// NewWorkerAPI creates a new WorkerAPI
func NewWorkerAPI(registry core.WorkerRegistry) *WorkerAPI {
	return &WorkerAPI{
		registry: registry,
	}
}

// HandleHeartbeat handles worker heartbeat requests
func (api *WorkerAPI) HandleHeartbeat(c *gin.Context) {
	// 解析 Worker Profile
	var profile core.WorkerProfile
	if err := c.ShouldBindJSON(&profile); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "Invalid request body: " + err.Error(),
				"type":    "invalid_request_error",
			},
		})
		return
	}

	// 验证必需字段
	if profile.WorkerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "worker_id is required",
				"type":    "invalid_request_error",
			},
		})
		return
	}

	// 更新注册中心
	if err := api.registry.Heartbeat(profile); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "Failed to update registry: " + err.Error(),
				"type":    "server_error",
			},
		})
		return
	}

	// 返回成功响应
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"worker_id": profile.WorkerID,
	})
}
