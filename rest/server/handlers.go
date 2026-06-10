package server

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/task-management-api-nb/task-management-api-nb/internal/models"
	"github.com/task-management-api-nb/task-management-api-nb/internal/services"
)

// Handler holds the HTTP handlers
type Handler struct {
	taskService *services.TaskService
}

// NewHandler creates a new handler instance
func NewHandler(taskService *services.TaskService) *Handler {
	return &Handler{
		taskService: taskService,
	}
}

// RegisterRoutes registers all API routes
func (h *Handler) RegisterRoutes(router *gin.Engine) {
	// Health check
	router.GET("/health", h.healthCheck)

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		tasks := v1.Group("/tasks")
		{
			tasks.GET("", h.listTasks)
			tasks.GET("/:id", h.getTask)
			tasks.POST("", h.createTask)
			tasks.PUT("/:id", h.updateTask)
			tasks.PATCH("/:id/status", h.updateTaskStatus)
			tasks.DELETE("/:id", h.deleteTask)
		}
	}
}

// healthCheck handles health check requests
func (h *Handler) healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"service":   "task-api-go",
		"version":   "1.0.0",
	})
}

// listTasks handles GET /tasks
func (h *Handler) listTasks(c *gin.Context) {
	var query models.ListTasksQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "VALIDATION_ERROR",
				Message: err.Error(),
			},
		})
		return
	}

	// Set default page size if not provided
	if query.PageSize == 0 {
		query.PageSize = 20
	}

	response, err := h.taskService.ListTasks(query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "INTERNAL_ERROR",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// getTask handles GET /tasks/:id
func (h *Handler) getTask(c *gin.Context) {
	id := c.Param("id")

	task, err := h.taskService.GetTask(id)
	if err != nil {
		status := http.StatusInternalServerError
		code := "INTERNAL_ERROR"

		if errors.Is(err, services.ErrTaskNotFound) {
			status = http.StatusNotFound
			code = "NOT_FOUND"
		}

		c.JSON(status, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    code,
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, task)
}

// createTask handles POST /tasks
func (h *Handler) createTask(c *gin.Context) {
	var req models.CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "VALIDATION_ERROR",
				Message: err.Error(),
			},
		})
		return
	}

	task, err := h.taskService.CreateTask(req)
	if err != nil {
		status := http.StatusBadRequest
		code := "VALIDATION_ERROR"

		if !errors.Is(err, services.ErrInvalidPriority) {
			status = http.StatusInternalServerError
			code = "INTERNAL_ERROR"
		}

		c.JSON(status, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    code,
				Message: err.Error(),
			},
		})
		return
	}

	c.Header("Location", "/api/v1/tasks/"+task.ID)
	c.JSON(http.StatusCreated, task)
}

// updateTask handles PUT /tasks/:id
func (h *Handler) updateTask(c *gin.Context) {
	id := c.Param("id")

	var req models.UpdateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "VALIDATION_ERROR",
				Message: err.Error(),
			},
		})
		return
	}

	task, err := h.taskService.UpdateTask(id, req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "INTERNAL_ERROR"

		if errors.Is(err, services.ErrTaskNotFound) {
			status = http.StatusNotFound
			code = "NOT_FOUND"
		} else if errors.Is(err, services.ErrInvalidStatus) || errors.Is(err, services.ErrInvalidPriority) {
			status = http.StatusBadRequest
			code = "VALIDATION_ERROR"
		}

		c.JSON(status, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    code,
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, task)
}

// updateTaskStatus handles PATCH /tasks/:id/status
func (h *Handler) updateTaskStatus(c *gin.Context) {
	id := c.Param("id")

	var req models.UpdateTaskStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "VALIDATION_ERROR",
				Message: err.Error(),
			},
		})
		return
	}

	if !req.Status.IsValid() {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "VALIDATION_ERROR",
				Message: fmt.Sprintf("invalid status: %s", req.Status),
			},
		})
		return
	}

	task, err := h.taskService.UpdateTaskStatus(id, req.Status)
	if err != nil {
		status := http.StatusInternalServerError
		code := "INTERNAL_ERROR"

		if errors.Is(err, services.ErrTaskNotFound) {
			status = http.StatusNotFound
			code = "NOT_FOUND"
		} else if errors.Is(err, services.ErrInvalidStatus) {
			status = http.StatusBadRequest
			code = "VALIDATION_ERROR"
		}

		c.JSON(status, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    code,
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, task)
}

// deleteTask handles DELETE /tasks/:id
func (h *Handler) deleteTask(c *gin.Context) {
	id := c.Param("id")

	err := h.taskService.DeleteTask(id)
	if err != nil {
		status := http.StatusInternalServerError
		code := "INTERNAL_ERROR"

		if errors.Is(err, services.ErrTaskNotFound) {
			status = http.StatusNotFound
			code = "NOT_FOUND"
		}

		c.JSON(status, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    code,
				Message: err.Error(),
			},
		})
		return
	}

	c.Status(http.StatusNoContent)
}
