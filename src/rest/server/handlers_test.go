package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

// testNow is the frozen clock value used by the HTTP tests
var testNow = time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

// notifyCall records a single NotifyAssignment invocation
type notifyCall struct {
	task     models.Task
	assignee string
}

// recordingNotifier is a fake Notifier that records every call
type recordingNotifier struct {
	mu    sync.Mutex
	calls []notifyCall
}

// NotifyAssignment records the call
func (n *recordingNotifier) NotifyAssignment(task models.Task, assignee string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, notifyCall{task: task, assignee: assignee})
	return nil
}

// recorded returns a snapshot of the recorded calls
func (n *recordingNotifier) recorded() []notifyCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]notifyCall(nil), n.calls...)
}

// newTestRouter builds an empty service with a frozen clock and a recording
// notifier, and registers the real routes on a fresh Gin engine
func newTestRouter() (*gin.Engine, *services.TaskService, *recordingNotifier) {
	gin.SetMode(gin.TestMode)
	n := &recordingNotifier{}
	svc := services.NewTaskServiceWithOptions(
		services.WithClock(func() time.Time { return testNow }),
		services.WithNotifier(n),
		services.WithoutSampleData(),
	)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return r, svc, n
}

// doRequest performs an in-process request against the router
func doRequest(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// decodeJSON decodes the recorder body into v
func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decoding response body %q: %v", rec.Body.String(), err)
	}
}

// ptrTime returns a pointer to the given time
func ptrTime(t time.Time) *time.Time {
	return &t
}

// mustCreateTask creates a task through the service with the given status and due date
func mustCreateTask(t *testing.T, svc *services.TaskService, title string, status models.TaskStatus, due *time.Time) string {
	t.Helper()
	task, err := svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatalf("CreateTask(%q) returned error: %v", title, err)
	}
	if status != "" && status != models.TaskStatusPending {
		if _, err := svc.UpdateTask(task.ID, models.UpdateTaskRequest{Status: &status}); err != nil {
			t.Fatalf("UpdateTask(%q) status %q returned error: %v", task.ID, status, err)
		}
	}
	return task.ID
}

// taskIDs returns the IDs of the given tasks in order
func taskIDs(tasks []models.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

// equalStrings reports whether two string slices have the same elements in order
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// assertError checks the status code and error code of an error response
func assertError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) models.ErrorResponse {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, wantStatus, rec.Body.String())
	}
	var resp models.ErrorResponse
	decodeJSON(t, rec, &resp)
	if resp.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", resp.Error.Code, wantCode)
	}
	if resp.Error.Message == "" {
		t.Error("error message is empty")
	}
	return resp
}

func TestHealthCheck(t *testing.T) {
	r, _, _ := newTestRouter()

	rec := doRequest(r, http.MethodGet, "/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]interface{}
	decodeJSON(t, rec, &body)
	if body["status"] != "healthy" {
		t.Errorf("status field = %v, want %q", body["status"], "healthy")
	}
	if body["service"] != "task-api-go" {
		t.Errorf("service field = %v, want %q", body["service"], "task-api-go")
	}
}

func TestListOverdueTasks_HTTP(t *testing.T) {
	r, svc, _ := newTestRouter()

	dueLate := mustCreateTask(t, svc, "due 1h ago", models.TaskStatusPending, ptrTime(testNow.Add(-1*time.Hour)))
	dueEarly := mustCreateTask(t, svc, "due 3h ago", models.TaskStatusOnHold, ptrTime(testNow.Add(-3*time.Hour)))
	dueMid := mustCreateTask(t, svc, "due 2h ago", models.TaskStatusInProgress, ptrTime(testNow.Add(-2*time.Hour)))
	mustCreateTask(t, svc, "completed", models.TaskStatusCompleted, ptrTime(testNow.Add(-5*time.Hour)))
	mustCreateTask(t, svc, "cancelled", models.TaskStatusCancelled, ptrTime(testNow.Add(-5*time.Hour)))
	mustCreateTask(t, svc, "future", models.TaskStatusPending, ptrTime(testNow.Add(time.Hour)))
	mustCreateTask(t, svc, "no due date", models.TaskStatusPending, nil)
	mustCreateTask(t, svc, "due now", models.TaskStatusPending, ptrTime(testNow))

	ordered := []string{dueEarly, dueMid, dueLate}

	tests := []struct {
		name     string
		query    string
		wantIDs  []string
		wantNext string
	}{
		{name: "default page size", query: "", wantIDs: ordered, wantNext: ""},
		{name: "first page of two", query: "?page_size=2", wantIDs: ordered[:2], wantNext: "2"},
		{name: "second page via token", query: "?page_size=2&page_token=2", wantIDs: ordered[2:], wantNext: ""},
		{name: "page size of one", query: "?page_size=1&page_token=1", wantIDs: ordered[1:2], wantNext: "2"},
		{name: "page size of 100", query: "?page_size=100", wantIDs: ordered, wantNext: ""},
		{name: "out of range token", query: "?page_size=2&page_token=10", wantIDs: []string{}, wantNext: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(r, http.MethodGet, "/api/v1/tasks/overdue"+tt.query, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
			}

			var resp models.ListTasksResponse
			decodeJSON(t, rec, &resp)
			if resp.TotalCount != len(ordered) {
				t.Errorf("total_count = %d, want %d", resp.TotalCount, len(ordered))
			}
			if got := taskIDs(resp.Tasks); !equalStrings(got, tt.wantIDs) {
				t.Errorf("task IDs = %v, want %v", got, tt.wantIDs)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("next_page_token = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
			if hasToken := strings.Contains(rec.Body.String(), `"next_page_token"`); hasToken != (tt.wantNext != "") {
				t.Errorf("next_page_token present in body = %v, want %v", hasToken, tt.wantNext != "")
			}
		})
	}
}

func TestListOverdueTasks_HTTPPaginationOverFive(t *testing.T) {
	r, svc, _ := newTestRouter()

	var ids []string
	for i := 0; i < 5; i++ {
		due := testNow.Add(-time.Duration(5-i) * time.Hour)
		ids = append(ids, mustCreateTask(t, svc, fmt.Sprintf("overdue %d", i), models.TaskStatusPending, ptrTime(due)))
	}

	tests := []struct {
		name     string
		token    string
		from     int
		to       int
		wantNext string
	}{
		{name: "first page", token: "", from: 0, to: 2, wantNext: "2"},
		{name: "second page", token: "2", from: 2, to: 4, wantNext: "4"},
		{name: "last page", token: "4", from: 4, to: 5, wantNext: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "/api/v1/tasks/overdue?page_size=2"
			if tt.token != "" {
				path += "&page_token=" + tt.token
			}
			rec := doRequest(r, http.MethodGet, path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
			}

			var resp models.ListTasksResponse
			decodeJSON(t, rec, &resp)
			if resp.TotalCount != 5 {
				t.Errorf("total_count = %d, want 5", resp.TotalCount)
			}
			if got, want := taskIDs(resp.Tasks), ids[tt.from:tt.to]; !equalStrings(got, want) {
				t.Errorf("task IDs = %v, want %v", got, want)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("next_page_token = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
		})
	}
}

func TestListOverdueTasks_HTTPValidation(t *testing.T) {
	r, svc, _ := newTestRouter()
	mustCreateTask(t, svc, "overdue", models.TaskStatusPending, ptrTime(testNow.Add(-time.Hour)))

	tests := []struct {
		name  string
		query string
	}{
		{name: "page_size zero", query: "?page_size=0"},
		{name: "page_size above maximum", query: "?page_size=101"},
		{name: "page_size negative", query: "?page_size=-1"},
		{name: "page_size non-numeric", query: "?page_size=abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(r, http.MethodGet, "/api/v1/tasks/overdue"+tt.query, "")
			assertError(t, rec, http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

func TestListOverdueTasks_HTTPEmpty(t *testing.T) {
	r, svc, _ := newTestRouter()
	// Nothing here is overdue
	mustCreateTask(t, svc, "future", models.TaskStatusPending, ptrTime(testNow.Add(time.Hour)))
	mustCreateTask(t, svc, "completed", models.TaskStatusCompleted, ptrTime(testNow.Add(-time.Hour)))

	rec := doRequest(r, http.MethodGet, "/api/v1/tasks/overdue", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"tasks":[]`) {
		t.Errorf("body = %s, want an empty tasks array", rec.Body.String())
	}
	var resp models.ListTasksResponse
	decodeJSON(t, rec, &resp)
	if resp.TotalCount != 0 {
		t.Errorf("total_count = %d, want 0", resp.TotalCount)
	}
	if len(resp.Tasks) != 0 {
		t.Errorf("got %d tasks, want 0", len(resp.Tasks))
	}
	if resp.NextPageToken != "" {
		t.Errorf("next_page_token = %q, want empty", resp.NextPageToken)
	}
}

func TestListOverdueTasks_NotRoutedToGetTask(t *testing.T) {
	r, _, _ := newTestRouter()

	// With an empty store, getTask would answer 404 "task with ID overdue not found"
	rec := doRequest(r, http.MethodGet, "/api/v1/tasks/overdue", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "NOT_FOUND") || strings.Contains(rec.Body.String(), "task with ID overdue not found") {
		t.Errorf("body = %s, looks like the getTask not-found response", rec.Body.String())
	}
	var resp models.ListTasksResponse
	decodeJSON(t, rec, &resp)
	if resp.TotalCount != 0 {
		t.Errorf("total_count = %d, want 0", resp.TotalCount)
	}

	// Contrast: an unknown ID still goes to getTask
	rec = doRequest(r, http.MethodGet, "/api/v1/tasks/not-overdue", "")
	errResp := assertError(t, rec, http.StatusNotFound, "NOT_FOUND")
	if want := "task with ID not-overdue not found"; errResp.Error.Message != want {
		t.Errorf("error message = %q, want %q", errResp.Error.Message, want)
	}
}

func TestAssignTask_HTTPSuccess(t *testing.T) {
	r, svc, n := newTestRouter()
	id := mustCreateTask(t, svc, "Write docs", models.TaskStatusPending, nil)

	rec := doRequest(r, http.MethodPost, "/api/v1/tasks/"+id+"/assign", `{"assigned_to":"alice@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var task models.Task
	decodeJSON(t, rec, &task)
	if task.ID != id {
		t.Errorf("id = %q, want %q", task.ID, id)
	}
	if task.AssignedTo == nil || *task.AssignedTo != "alice@example.com" {
		t.Errorf("assigned_to = %v, want %q", task.AssignedTo, "alice@example.com")
	}
	if !task.UpdatedAt.Equal(testNow) {
		t.Errorf("updated_at = %v, want %v", task.UpdatedAt, testNow)
	}
	if !strings.Contains(rec.Body.String(), `"assigned_to":"alice@example.com"`) {
		t.Errorf("body = %s, want it to contain assigned_to", rec.Body.String())
	}

	calls := n.recorded()
	if len(calls) != 1 {
		t.Fatalf("notifier called %d times, want 1", len(calls))
	}
	if calls[0].assignee != "alice@example.com" {
		t.Errorf("notified assignee = %q, want %q", calls[0].assignee, "alice@example.com")
	}
	if calls[0].task.ID != id {
		t.Errorf("notified task ID = %q, want %q", calls[0].task.ID, id)
	}
	if calls[0].task.AssignedTo == nil || *calls[0].task.AssignedTo != "alice@example.com" {
		t.Errorf("notified task AssignedTo = %v, want %q", calls[0].task.AssignedTo, "alice@example.com")
	}

	// The assignment is persisted and visible through GET /tasks/:id
	rec = doRequest(r, http.MethodGet, "/api/v1/tasks/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var fetched models.Task
	decodeJSON(t, rec, &fetched)
	if fetched.AssignedTo == nil || *fetched.AssignedTo != "alice@example.com" {
		t.Errorf("persisted assigned_to = %v, want %q", fetched.AssignedTo, "alice@example.com")
	}
	if !fetched.UpdatedAt.Equal(testNow) {
		t.Errorf("persisted updated_at = %v, want %v", fetched.UpdatedAt, testNow)
	}
}

func TestAssignTask_HTTPValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing field", body: `{}`},
		{name: "empty string", body: `{"assigned_to":""}`},
		{name: "invalid email", body: `{"assigned_to":"not-an-email"}`},
		{name: "wrong type", body: `{"assigned_to":123}`},
		{name: "malformed JSON", body: `{"assigned_to":`},
		{name: "not an object", body: `"alice@example.com"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, svc, n := newTestRouter()
			id := mustCreateTask(t, svc, "Write docs", models.TaskStatusPending, nil)

			rec := doRequest(r, http.MethodPost, "/api/v1/tasks/"+id+"/assign", tt.body)
			assertError(t, rec, http.StatusBadRequest, "VALIDATION_ERROR")

			if calls := n.recorded(); len(calls) != 0 {
				t.Errorf("notifier called %d times, want 0", len(calls))
			}
			stored, err := svc.GetTask(id)
			if err != nil {
				t.Fatalf("GetTask returned error: %v", err)
			}
			if stored.AssignedTo != nil {
				t.Errorf("stored AssignedTo = %q, want nil", *stored.AssignedTo)
			}
		})
	}

	t.Run("empty body", func(t *testing.T) {
		r, svc, n := newTestRouter()
		id := mustCreateTask(t, svc, "Write docs", models.TaskStatusPending, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+id+"/assign", nil)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		assertError(t, rec, http.StatusBadRequest, "VALIDATION_ERROR")
		if calls := n.recorded(); len(calls) != 0 {
			t.Errorf("notifier called %d times, want 0", len(calls))
		}
	})
}

func TestAssignTask_HTTPNotFound(t *testing.T) {
	r, svc, n := newTestRouter()
	mustCreateTask(t, svc, "existing", models.TaskStatusPending, nil)

	rec := doRequest(r, http.MethodPost, "/api/v1/tasks/missing-id/assign", `{"assigned_to":"alice@example.com"}`)
	resp := assertError(t, rec, http.StatusNotFound, "NOT_FOUND")
	if want := "task with ID missing-id not found"; resp.Error.Message != want {
		t.Errorf("error message = %q, want %q", resp.Error.Message, want)
	}
	if calls := n.recorded(); len(calls) != 0 {
		t.Errorf("notifier called %d times, want 0", len(calls))
	}
}

func TestExistingRoutes_Smoke(t *testing.T) {
	r, svc, _ := newTestRouter()
	mustCreateTask(t, svc, "pending", models.TaskStatusPending, nil)
	mustCreateTask(t, svc, "in progress", models.TaskStatusInProgress, nil)
	mustCreateTask(t, svc, "completed", models.TaskStatusCompleted, nil)

	t.Run("GET /api/v1/tasks", func(t *testing.T) {
		rec := doRequest(r, http.MethodGet, "/api/v1/tasks", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp models.ListTasksResponse
		decodeJSON(t, rec, &resp)
		if resp.TotalCount != 3 || len(resp.Tasks) != 3 {
			t.Errorf("total_count = %d, len = %d, want 3 and 3", resp.TotalCount, len(resp.Tasks))
		}
	})

	t.Run("GET /api/v1/tasks with page_size and status", func(t *testing.T) {
		rec := doRequest(r, http.MethodGet, "/api/v1/tasks?page_size=2", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp models.ListTasksResponse
		decodeJSON(t, rec, &resp)
		if resp.TotalCount != 3 || len(resp.Tasks) != 2 || resp.NextPageToken != "2" {
			t.Errorf("got total_count=%d len=%d next=%q, want 3, 2 and \"2\"", resp.TotalCount, len(resp.Tasks), resp.NextPageToken)
		}

		rec = doRequest(r, http.MethodGet, "/api/v1/tasks?status=in_progress", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		resp = models.ListTasksResponse{}
		decodeJSON(t, rec, &resp)
		if resp.TotalCount != 1 || len(resp.Tasks) != 1 || resp.Tasks[0].Status != models.TaskStatusInProgress {
			t.Errorf("status filter returned total_count=%d tasks=%+v, want one in_progress task", resp.TotalCount, resp.Tasks)
		}
	})

	t.Run("GET /api/v1/tasks/stats", func(t *testing.T) {
		rec := doRequest(r, http.MethodGet, "/api/v1/tasks/stats", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var stats models.TaskStats
		decodeJSON(t, rec, &stats)
		if stats.Total != 3 {
			t.Errorf("total = %d, want 3", stats.Total)
		}
		if stats.ByStatus[models.TaskStatusCompleted] != 1 {
			t.Errorf("by_status.completed = %d, want 1", stats.ByStatus[models.TaskStatusCompleted])
		}
	})

	t.Run("POST /api/v1/tasks", func(t *testing.T) {
		rec := doRequest(r, http.MethodPost, "/api/v1/tasks", `{"title":"Created over HTTP"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
		}
		var task models.Task
		decodeJSON(t, rec, &task)
		if task.Title != "Created over HTTP" || task.ID == "" {
			t.Errorf("created task = %+v, want title %q and an ID", task, "Created over HTTP")
		}
	})
}
