package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

// Handler groups the HTTP handlers for the task REST API. It holds the task and
// comment services that back every route and the process start time reported by
// the health check. The handlers hold no mutable state of their own; thread
// safety is provided by the mutexes inside the services, so one Handler can serve
// concurrent requests.
type Handler struct {
	taskService    *services.TaskService
	commentService *services.CommentService
	startedAt      time.Time
}

// NewHandler creates a Handler backed by the given task service. It also builds a
// new comment service on top of taskService and records the current time as the
// server start time used for the uptime in the health check.
func NewHandler(taskService *services.TaskService) *Handler {
	return &Handler{
		taskService:    taskService,
		commentService: services.NewCommentService(taskService),
		startedAt:      time.Now(),
	}
}

// RegisterRoutes registers the health check and all /api/v1/tasks routes on the
// given Gin engine. Static paths (/search, /stats, /overdue) are registered next
// to the parameterised /:id routes; Gin gives static segments precedence, so they
// are not captured as task IDs. Comment routes are nested under /:id/comments.
func (h *Handler) RegisterRoutes(router *gin.Engine) {
	// Health check
	router.GET("/health", h.healthCheck)

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		tasks := v1.Group("/tasks")
		{
			tasks.GET("", h.listTasks)
			tasks.GET("/search", h.searchTasks)
			tasks.GET("/stats", h.getTaskStats)
			tasks.GET("/overdue", h.listOverdueTasks)
			tasks.GET("/:id", h.getTask)
			tasks.POST("", h.createTask)
			tasks.PUT("/:id", h.updateTask)
			tasks.POST("/:id/assign", h.assignTask)
			tasks.PATCH("/:id/status", h.updateTaskStatus)
			tasks.DELETE("/:id", h.deleteTask)

			tasks.GET("/:id/comments", h.listComments)
			tasks.POST("/:id/comments", h.addComment)
			tasks.DELETE("/:id/comments/:commentId", h.deleteComment)
		}
	}
}

// healthCheck handles GET /health. It takes no parameters and always responds
// 200 OK with a JSON body containing status, timestamp (RFC 3339), service,
// version and a process object (owner, hostname, pid, uptime and Go version).
// The owner is "unknown" if the current OS user cannot be determined. It has no
// side effects and does not touch the task store.
func (h *Handler) healthCheck(c *gin.Context) {
	uptime := time.Since(h.startedAt).Round(time.Second).String()

	processOwner := "unknown"
	if u, err := user.Current(); err == nil {
		processOwner = u.Username
	}

	hostname, _ := os.Hostname()
	pid := os.Getpid()

	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"service":   "task-api-go",
		"version":   "1.1.0",
		"process": gin.H{
			"owner":    processOwner,
			"hostname": hostname,
			"pid":      pid,
			"uptime":   uptime,
			"go_version": runtime.Version(),
		},
	})
}

// listTasks handles GET /api/v1/tasks. Query parameters are bound into
// models.ListTasksQuery: page_size, page_token, status, assigned_to, tags and
// sort_order. A page_size of 0 (omitted) defaults to 20. It responds 200 OK with
// a page of tasks, next_page_token and total_count; 400 VALIDATION_ERROR if the
// query cannot be bound; 500 INTERNAL_ERROR if the service fails. Read-only; the
// service holds its read lock while listing.
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

// searchTasks handles GET /api/v1/tasks/search. The required query parameter q
// holds the search text; page_size (defaults to 20 when 0) and page_token
// control pagination. It responds 200 OK with the matching tasks, or 400
// VALIDATION_ERROR ("Query parameter 'q' is required") if binding fails. Read-only.
func (h *Handler) searchTasks(c *gin.Context) {
	var query models.SearchTasksQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "VALIDATION_ERROR",
				Message: "Query parameter 'q' is required",
			},
		})
		return
	}

	if query.PageSize == 0 {
		query.PageSize = 20
	}

	response := h.taskService.SearchTasks(query)
	c.JSON(http.StatusOK, response)
}

// getTaskStats handles GET /api/v1/tasks/stats. It takes no parameters and
// responds 200 OK with aggregate task counts grouped by status and priority. It
// cannot fail and has no side effects.
func (h *Handler) getTaskStats(c *gin.Context) {
	stats := h.taskService.GetStats()
	c.JSON(http.StatusOK, stats)
}

// listOverdueTasks handles GET /api/v1/tasks/overdue. Query parameters page_size
// and page_token are bound into models.OverdueTasksQuery. Overdue tasks are those
// with a due date in the past whose status is neither completed nor cancelled,
// ordered by due date ascending with ID as tiebreak. It responds 200 OK with a
// task list, next_page_token and total_count, or 400 VALIDATION_ERROR if the
// query cannot be bound. Read-only; no 404 is possible.
func (h *Handler) listOverdueTasks(c *gin.Context) {
	var query models.OverdueTasksQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "VALIDATION_ERROR",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, h.taskService.ListOverdueTasks(query))
}

// assignTask handles POST /api/v1/tasks/:id/assign. The path parameter id
// selects the task and the JSON body (models.AssignTaskRequest) carries the
// assignee email. It responds 200 OK with the updated task; 400 VALIDATION_ERROR
// if the body is malformed or the email is invalid (services.ErrInvalidEmail);
// 404 NOT_FOUND if no task has that ID (services.ErrTaskNotFound); 500
// INTERNAL_ERROR for any other error. Side effects: sets the task's assigned_to
// and refreshes UpdatedAt, then calls the configured services.Notifier (the
// default only logs). The service updates the task under its write lock but
// invokes the notifier after releasing it, so a slow notifier does not block
// other requests.
func (h *Handler) assignTask(c *gin.Context) {
	var req models.AssignTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "VALIDATION_ERROR",
				Message: err.Error(),
			},
		})
		return
	}

	task, err := h.taskService.AssignTask(c.Param("id"), req.Email)
	if err != nil {
		status := http.StatusInternalServerError
		code := "INTERNAL_ERROR"
		switch {
		case errors.Is(err, services.ErrInvalidEmail):
			status = http.StatusBadRequest
			code = "VALIDATION_ERROR"
		case errors.Is(err, services.ErrTaskNotFound):
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

// getTask handles GET /api/v1/tasks/:id. The path parameter id is the task ID.
// It responds 200 OK with the task, or 404 NOT_FOUND if the lookup fails. Read-only.
func (h *Handler) getTask(c *gin.Context) {
	id := c.Param("id")

	task, err := h.taskService.GetTask(id)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "NOT_FOUND",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, task)
}

// createTask handles POST /api/v1/tasks. The JSON body is
// models.CreateTaskRequest (title, description, priority, tags, assigned_to,
// due_date). It responds 201 Created with the new task and a Location header of
// /api/v1/tasks/<id>. A new task starts as pending, with a generated ID, CreatedBy
// "system" and CreatedAt/UpdatedAt set to the current time; an omitted priority
// defaults to medium. Responds 400 VALIDATION_ERROR if the body cannot be bound
// or the service rejects it (for example an invalid priority). The task is stored
// under the service's write lock.
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
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "VALIDATION_ERROR",
				Message: err.Error(),
			},
		})
		return
	}

	c.Header("Location", "/api/v1/tasks/"+task.ID)
	c.JSON(http.StatusCreated, task)
}

// updateTask handles PUT /api/v1/tasks/:id. The path parameter id selects the
// task and the JSON body (models.UpdateTaskRequest) carries optional fields;
// only fields present are changed (partial update semantics). It responds 200 OK
// with the updated task; 400 VALIDATION_ERROR if the body cannot be bound or the
// service error message contains "invalid" (such as an invalid status or
// priority); 404 NOT_FOUND if the error message is exactly "task with ID <id> not
// found"; otherwise 500 INTERNAL_ERROR. The error classification is based on
// message text. Side effects: UpdatedAt is set to the current time, and
// CompletedAt is set the first time the status becomes completed. The service
// applies the update under its write lock.
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
		
		// Check if it's a not found error
		if err.Error() == fmt.Sprintf("task with ID %s not found", id) {
			status = http.StatusNotFound
			code = "NOT_FOUND"
		} else if strings.Contains(err.Error(), "invalid") {
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

// updateTaskStatus handles PATCH /api/v1/tasks/:id/status. The path parameter id
// selects the task and the JSON body (models.UpdateTaskStatusRequest) carries the
// new status. It responds 200 OK with the updated task; 400 VALIDATION_ERROR if
// the body cannot be bound or the status is not valid; 404 NOT_FOUND if the error
// message is exactly "task with ID <id> not found"; otherwise 500 INTERNAL_ERROR.
// Side effects: UpdatedAt is refreshed, and CompletedAt is set the first time the
// status becomes completed. It delegates to the service's UpdateTask, which holds
// the write lock for the change.
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
		
		if err.Error() == fmt.Sprintf("task with ID %s not found", id) {
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

// deleteTask handles DELETE /api/v1/tasks/:id. The path parameter id selects the
// task. It responds 204 No Content with an empty body on success, or 404
// NOT_FOUND if the service returns an error. The task is removed from the store
// under the service's write lock.
func (h *Handler) deleteTask(c *gin.Context) {
	id := c.Param("id")

	err := h.taskService.DeleteTask(id)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "NOT_FOUND",
				Message: err.Error(),
			},
		})
		return
	}

	c.Status(http.StatusNoContent)
}

// listComments handles GET /api/v1/tasks/:id/comments. The path parameter id is
// the task ID. It responds 200 OK with the task's comments, ordered by creation
// time, and their total; 404 NOT_FOUND if the task does not exist. Read-only.
func (h *Handler) listComments(c *gin.Context) {
	taskID := c.Param("id")

	response, err := h.commentService.ListComments(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "NOT_FOUND",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// addComment handles POST /api/v1/tasks/:id/comments. The path parameter id is
// the task ID and the JSON body is models.CreateCommentRequest (author, body). It
// responds 201 Created with the new comment (generated ID, CreatedAt and
// UpdatedAt set to now); 400 VALIDATION_ERROR if the body cannot be bound; 404
// NOT_FOUND if the service returns an error, which happens when the task does not
// exist. The comment is stored under the comment service's write lock. The parent
// task's UpdatedAt is not modified.
func (h *Handler) addComment(c *gin.Context) {
	taskID := c.Param("id")

	var req models.CreateCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "VALIDATION_ERROR",
				Message: err.Error(),
			},
		})
		return
	}

	comment, err := h.commentService.AddComment(taskID, req)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "NOT_FOUND",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusCreated, comment)
}

// deleteComment handles DELETE /api/v1/tasks/:id/comments/:commentId. The path
// parameters id (task) and commentId (comment) identify the comment. It responds
// 204 No Content on success, or 404 NOT_FOUND if the task or the comment does not
// exist. The comment is removed under the comment service's write lock.
func (h *Handler) deleteComment(c *gin.Context) {
	taskID := c.Param("id")
	commentID := c.Param("commentId")

	err := h.commentService.DeleteComment(taskID, commentID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error: models.ErrorDetail{
				Code:    "NOT_FOUND",
				Message: err.Error(),
			},
		})
		return
	}

	c.Status(http.StatusNoContent)
}