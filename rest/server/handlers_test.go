package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/task-management-api-nb/task-management-api-nb/internal/models"
	"github.com/task-management-api-nb/task-management-api-nb/internal/services"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// setupRouter creates a fresh Gin engine, TaskService, and Handler for each test.
// The returned service is pre-populated with 2 sample tasks from NewTaskService().
func setupRouter() (*gin.Engine, *services.TaskService) {
	router := gin.New()
	svc := services.NewTaskService()
	handler := NewHandler(svc)
	handler.RegisterRoutes(router)
	return router, svc
}

// setupRouterWithCORS creates a router that includes the same CORS middleware
// as defined in cmd/rest-server/main.go (replicated here because corsMiddleware
// lives in the main package and cannot be imported).
func setupRouterWithCORS() *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	svc := services.NewTaskService()
	handler := NewHandler(svc)
	handler.RegisterRoutes(router)
	return router
}

// createTaskViaAPI is a helper that creates a task through the API and returns
// the created task. It fatally fails the test if the request does not succeed.
func createTaskViaAPI(t *testing.T, router *gin.Engine, body string) models.Task {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "helper: create task should return 201")

	var task models.Task
	err := json.Unmarshal(w.Body.Bytes(), &task)
	require.NoError(t, err, "helper: should unmarshal created task")
	return task
}

// =============================================================================
// Health Check Tests
// =============================================================================

func TestHealthCheck(t *testing.T) {
	router, _ := setupRouter()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	assert.Equal(t, "healthy", body["status"])
	assert.Equal(t, "task-api-go", body["service"])
	assert.Equal(t, "1.0.0", body["version"])
	assert.NotEmpty(t, body["timestamp"], "timestamp should be present")
}

// =============================================================================
// List Tasks Tests
// =============================================================================

func TestListTasks(t *testing.T) {
	t.Run("returns 200 with tasks array from pre-populated service", func(t *testing.T) {
		router, _ := setupRouter()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp models.ListTasksResponse
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)

		assert.Len(t, resp.Tasks, 2, "should have 2 pre-populated sample tasks")
		assert.Equal(t, 2, resp.TotalCount)
	})

	t.Run("returns correct JSON structure with tasks, total_count, next_page_token", func(t *testing.T) {
		router, _ := setupRouter()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var raw map[string]json.RawMessage
		err := json.Unmarshal(w.Body.Bytes(), &raw)
		require.NoError(t, err)

		_, hasTasks := raw["tasks"]
		assert.True(t, hasTasks, "response should contain 'tasks' field")

		_, hasTotalCount := raw["total_count"]
		assert.True(t, hasTotalCount, "response should contain 'total_count' field")

		// next_page_token may be omitted when empty (omitempty), so we just
		// verify the two required fields are present.
	})

	t.Run("filter by status query parameter", func(t *testing.T) {
		router, _ := setupRouter()

		// The 2 sample tasks are both in "pending" status, so filtering
		// by "completed" should yield zero results.
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?status=completed", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp models.ListTasksResponse
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)

		assert.Len(t, resp.Tasks, 0, "no tasks should match status=completed")
		assert.Equal(t, 0, resp.TotalCount)

		// Now filter by "pending" — both sample tasks should appear.
		req2 := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?status=pending", nil)
		w2 := httptest.NewRecorder()
		router.ServeHTTP(w2, req2)

		assert.Equal(t, http.StatusOK, w2.Code)

		var resp2 models.ListTasksResponse
		err = json.Unmarshal(w2.Body.Bytes(), &resp2)
		require.NoError(t, err)

		assert.Len(t, resp2.Tasks, 2, "both sample tasks should match status=pending")
	})

	t.Run("pagination with page_size works", func(t *testing.T) {
		router, _ := setupRouter()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?page_size=1", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp models.ListTasksResponse
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)

		assert.Len(t, resp.Tasks, 1, "should return only 1 task when page_size=1")
		assert.Equal(t, 2, resp.TotalCount, "total_count should still reflect all matching tasks")
		assert.NotEmpty(t, resp.NextPageToken, "should have next_page_token when more items exist")
	})

	t.Run("invalid page_size returns 400 VALIDATION_ERROR", func(t *testing.T) {
		router, _ := setupRouter()

		// page_size has binding:"min=1,max=100" — 0 is invalid
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?page_size=0", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var errResp models.ErrorResponse
		err := json.Unmarshal(w.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Equal(t, "VALIDATION_ERROR", errResp.Error.Code)
	})
}

// =============================================================================
// Get Task Tests
// =============================================================================

func TestGetTask(t *testing.T) {
	t.Run("returns 200 with task for valid ID", func(t *testing.T) {
		router, _ := setupRouter()

		// Create a task so we have a known ID
		created := createTaskViaAPI(t, router, `{"title":"Get Me"}`)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+created.ID, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var task models.Task
		err := json.Unmarshal(w.Body.Bytes(), &task)
		require.NoError(t, err)
		assert.Equal(t, created.ID, task.ID)
		assert.Equal(t, "Get Me", task.Title)
	})

	t.Run("returns 404 NOT_FOUND for non-existing ID", func(t *testing.T) {
		router, _ := setupRouter()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/nonexistent-id", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)

		var errResp models.ErrorResponse
		err := json.Unmarshal(w.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Equal(t, "NOT_FOUND", errResp.Error.Code)
	})
}

// =============================================================================
// Create Task Tests
// =============================================================================

func TestCreateTask(t *testing.T) {
	t.Run("returns 201 with created task for valid request", func(t *testing.T) {
		router, _ := setupRouter()

		body := `{"title":"New Task","description":"A new task","priority":"high","tags":["go","test"]}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)

		var task models.Task
		err := json.Unmarshal(w.Body.Bytes(), &task)
		require.NoError(t, err)

		assert.NotEmpty(t, task.ID)
		assert.Equal(t, "New Task", task.Title)
		assert.Equal(t, "A new task", task.Description)
		assert.Equal(t, models.TaskPriorityHigh, task.Priority)
		assert.Equal(t, models.TaskStatusPending, task.Status)
		assert.Equal(t, []string{"go", "test"}, task.Tags)
	})

	t.Run("returns Location header pointing to created resource", func(t *testing.T) {
		router, _ := setupRouter()

		body := `{"title":"Located Task"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)

		var task models.Task
		err := json.Unmarshal(w.Body.Bytes(), &task)
		require.NoError(t, err)

		location := w.Header().Get("Location")
		assert.Equal(t, "/api/v1/tasks/"+task.ID, location)
	})

	t.Run("returns 400 VALIDATION_ERROR for missing title (empty body)", func(t *testing.T) {
		router, _ := setupRouter()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var errResp models.ErrorResponse
		err := json.Unmarshal(w.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Equal(t, "VALIDATION_ERROR", errResp.Error.Code)
	})

	t.Run("returns 400 VALIDATION_ERROR for invalid priority", func(t *testing.T) {
		router, _ := setupRouter()

		body := `{"title":"Bad Priority","priority":"ultra"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var errResp models.ErrorResponse
		err := json.Unmarshal(w.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Equal(t, "VALIDATION_ERROR", errResp.Error.Code)
	})
}

// =============================================================================
// Update Task Tests
// =============================================================================

func TestUpdateTask(t *testing.T) {
	t.Run("returns 200 with updated task", func(t *testing.T) {
		router, _ := setupRouter()

		created := createTaskViaAPI(t, router, `{"title":"Original Title"}`)

		body := `{"title":"Updated Title","description":"Updated desc"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/tasks/"+created.ID, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var task models.Task
		err := json.Unmarshal(w.Body.Bytes(), &task)
		require.NoError(t, err)

		assert.Equal(t, "Updated Title", task.Title)
		assert.Equal(t, "Updated desc", task.Description)
	})

	t.Run("returns 404 NOT_FOUND for non-existing task", func(t *testing.T) {
		router, _ := setupRouter()

		body := `{"title":"Ghost"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/tasks/nonexistent-id", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)

		var errResp models.ErrorResponse
		err := json.Unmarshal(w.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Equal(t, "NOT_FOUND", errResp.Error.Code)
	})

	t.Run("returns 400 VALIDATION_ERROR for invalid status", func(t *testing.T) {
		router, _ := setupRouter()

		created := createTaskViaAPI(t, router, `{"title":"Status Test"}`)

		body := `{"status":"bogus"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/tasks/"+created.ID, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var errResp models.ErrorResponse
		err := json.Unmarshal(w.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Equal(t, "VALIDATION_ERROR", errResp.Error.Code)
	})

	t.Run("returns 400 VALIDATION_ERROR for invalid priority", func(t *testing.T) {
		router, _ := setupRouter()

		created := createTaskViaAPI(t, router, `{"title":"Priority Test"}`)

		body := `{"priority":"ultra"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/tasks/"+created.ID, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var errResp models.ErrorResponse
		err := json.Unmarshal(w.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Equal(t, "VALIDATION_ERROR", errResp.Error.Code)
	})
}

// =============================================================================
// Update Task Status Tests
// =============================================================================

func TestUpdateTaskStatus(t *testing.T) {
	t.Run("returns 200 with updated task", func(t *testing.T) {
		router, _ := setupRouter()

		created := createTaskViaAPI(t, router, `{"title":"Status Update"}`)

		body := `{"status":"in_progress"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+created.ID+"/status", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var task models.Task
		err := json.Unmarshal(w.Body.Bytes(), &task)
		require.NoError(t, err)

		assert.Equal(t, models.TaskStatusInProgress, task.Status)
	})

	t.Run("returns 404 NOT_FOUND for non-existing task", func(t *testing.T) {
		router, _ := setupRouter()

		body := `{"status":"in_progress"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/nonexistent-id/status", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)

		var errResp models.ErrorResponse
		err := json.Unmarshal(w.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Equal(t, "NOT_FOUND", errResp.Error.Code)
	})

	t.Run("returns 400 VALIDATION_ERROR for invalid status", func(t *testing.T) {
		router, _ := setupRouter()

		created := createTaskViaAPI(t, router, `{"title":"Bad Status"}`)

		body := `{"status":"bogus"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+created.ID+"/status", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var errResp models.ErrorResponse
		err := json.Unmarshal(w.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Equal(t, "VALIDATION_ERROR", errResp.Error.Code)
	})
}

// =============================================================================
// Delete Task Tests
// =============================================================================

func TestDeleteTask(t *testing.T) {
	t.Run("returns 204 No Content for existing task", func(t *testing.T) {
		router, _ := setupRouter()

		created := createTaskViaAPI(t, router, `{"title":"Delete Me"}`)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+created.ID, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, w.Body.Bytes(), "body should be empty for 204")

		// Verify the task is actually gone
		getReq := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+created.ID, nil)
		getW := httptest.NewRecorder()
		router.ServeHTTP(getW, getReq)
		assert.Equal(t, http.StatusNotFound, getW.Code)
	})

	t.Run("returns 404 NOT_FOUND for non-existing task", func(t *testing.T) {
		router, _ := setupRouter()

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/nonexistent-id", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)

		var errResp models.ErrorResponse
		err := json.Unmarshal(w.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Equal(t, "NOT_FOUND", errResp.Error.Code)
	})
}

// =============================================================================
// CORS Middleware Tests
// =============================================================================

func TestCORSMiddleware(t *testing.T) {
	t.Run("OPTIONS request returns 204 with correct CORS headers", func(t *testing.T) {
		router := setupRouterWithCORS()

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/tasks", nil)
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "Content-Type")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "GET")
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "POST")
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "PUT")
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "PATCH")
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "DELETE")
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "OPTIONS")
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Content-Type")
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Authorization")
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Origin")
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Accept")
	})

	t.Run("regular requests include Access-Control-Allow-Origin header", func(t *testing.T) {
		router := setupRouterWithCORS()

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set("Origin", "http://example.com")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	})
}
