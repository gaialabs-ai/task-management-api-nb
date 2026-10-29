package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// baseTime is the fixed instant the frozen test clock starts at.
var baseTime = time.Date(2024, time.March, 15, 12, 0, 0, 0, time.UTC)

// testClock is a goroutine-safe, manually advanced clock.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

type assignmentCall struct {
	Task     models.Task
	Assignee string
}

// recordingNotifier is a goroutine-safe fake notifications.Notifier.
type recordingNotifier struct {
	mu    sync.Mutex
	calls []assignmentCall
}

func (n *recordingNotifier) NotifyAssignment(task models.Task, assignee string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, assignmentCall{Task: task, Assignee: assignee})
	return nil
}

func (n *recordingNotifier) Calls() []assignmentCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]assignmentCall, len(n.calls))
	copy(out, n.calls)
	return out
}

// testEnv bundles an isolated service and the real router built from it.
type testEnv struct {
	svc      *services.TaskService
	clock    *testClock
	notifier *recordingNotifier
	router   *gin.Engine
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	clock := &testClock{now: baseTime}
	notifier := &recordingNotifier{}
	svc := services.NewTaskServiceWithOptions(
		services.WithClock(clock.Now),
		services.WithNotifier(notifier),
		services.WithoutSampleData(),
	)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return &testEnv{svc: svc, clock: clock, notifier: notifier, router: r}
}

func (e *testEnv) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// createTask creates a task through the service and optionally changes its status.
func (e *testEnv) createTask(t *testing.T, title string, due *time.Time, status models.TaskStatus) string {
	t.Helper()
	task, err := e.svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatalf("CreateTask(%q) returned error: %v", title, err)
	}
	id := task.ID
	if status != "" && status != models.TaskStatusPending {
		if _, err := e.svc.UpdateTaskStatus(id, status); err != nil {
			t.Fatalf("UpdateTaskStatus(%q) returned error: %v", id, err)
		}
	}
	return id
}

func timePtr(t time.Time) *time.Time { return &t }

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decoding response body %q: %v", rec.Body.String(), err)
	}
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, want, rec.Body.String())
	}
}

// expectError asserts the status code and the standard error envelope.
func expectError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) models.ErrorResponse {
	t.Helper()
	expectStatus(t, rec, wantStatus)
	var resp models.ErrorResponse
	decodeBody(t, rec, &resp)
	if resp.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", resp.Error.Code, wantCode)
	}
	if resp.Error.Message == "" {
		t.Error("error message is empty")
	}
	return resp
}

func responseIDs(tasks []models.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

// ---------------------------------------------------------------------------
// Health (in-process healthcheck)
// ---------------------------------------------------------------------------

func TestHealthCheck(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "GET /health", method: http.MethodGet, path: "/health"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			rec := env.do(t, tc.method, tc.path, "")
			expectStatus(t, rec, http.StatusOK)

			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			if !strings.Contains(rec.Body.String(), `"status":"healthy"`) {
				t.Errorf("body = %s, want it to contain \"status\":\"healthy\"", rec.Body.String())
			}

			var body map[string]interface{}
			decodeBody(t, rec, &body)
			if body["status"] != "healthy" {
				t.Errorf("status = %v, want healthy", body["status"])
			}
			if body["service"] != "task-api-go" {
				t.Errorf("service = %v, want task-api-go", body["service"])
			}
			if _, ok := body["process"].(map[string]interface{}); !ok {
				t.Errorf("process = %v, want an object", body["process"])
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GET /api/v1/tasks/overdue
// ---------------------------------------------------------------------------

func TestListOverdueTasksHandlerOrderAndTotal(t *testing.T) {
	env := newTestEnv(t)
	now := env.clock.Now()

	mid := env.createTask(t, "two hours late", timePtr(now.Add(-2*time.Hour)), models.TaskStatusInProgress)
	latest := env.createTask(t, "one hour late", timePtr(now.Add(-time.Hour)), models.TaskStatusOnHold)
	tieA := env.createTask(t, "three hours late A", timePtr(now.Add(-3*time.Hour)), "")
	tieB := env.createTask(t, "three hours late B", timePtr(now.Add(-3*time.Hour)), "")
	// Not overdue.
	env.createTask(t, "completed", timePtr(now.Add(-time.Hour)), models.TaskStatusCompleted)
	env.createTask(t, "cancelled", timePtr(now.Add(-time.Hour)), models.TaskStatusCancelled)
	env.createTask(t, "future", timePtr(now.Add(time.Hour)), "")
	env.createTask(t, "due now", timePtr(now), "")
	env.createTask(t, "no due date", nil, "")

	ties := []string{tieA, tieB}
	sort.Strings(ties)
	wantIDs := []string{ties[0], ties[1], mid, latest}

	rec := env.do(t, http.MethodGet, "/api/v1/tasks/overdue", "")
	expectStatus(t, rec, http.StatusOK)

	var resp models.ListTasksResponse
	decodeBody(t, rec, &resp)
	if got := responseIDs(resp.Tasks); !reflect.DeepEqual(got, wantIDs) {
		t.Errorf("order = %v, want %v", got, wantIDs)
	}
	if resp.TotalCount != 4 {
		t.Errorf("total_count = %d, want 4", resp.TotalCount)
	}
	if !strings.Contains(rec.Body.String(), `"total_count":4`) {
		t.Errorf("body = %s, want it to contain \"total_count\":4", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "next_page_token") {
		t.Errorf("body = %s, want no next_page_token on a single page", rec.Body.String())
	}
	for _, task := range resp.Tasks {
		if task.DueDate == nil || !task.DueDate.Before(now) {
			t.Errorf("task %s due_date = %v, want before %v", task.ID, task.DueDate, now)
		}
	}
}

func TestListOverdueTasksHandlerPagination(t *testing.T) {
	env := newTestEnv(t)
	now := env.clock.Now()

	ordered := make([]string, 5)
	// Create in reverse so creation order differs from due-date order.
	for i := 4; i >= 0; i-- {
		ordered[i] = env.createTask(t, fmt.Sprintf("overdue %d", i), timePtr(now.Add(-time.Duration(10-i)*time.Hour)), "")
	}
	env.createTask(t, "future", timePtr(now.Add(time.Hour)), "")

	tests := []struct {
		name          string
		query         string
		wantIDs       []string
		wantNextToken string
	}{
		{name: "default page size", query: "", wantIDs: ordered, wantNextToken: ""},
		{name: "first page", query: "?page_size=2", wantIDs: ordered[0:2], wantNextToken: "2"},
		{name: "second page", query: "?page_size=2&page_token=2", wantIDs: ordered[2:4], wantNextToken: "4"},
		{name: "last page has no token", query: "?page_size=2&page_token=4", wantIDs: ordered[4:5], wantNextToken: ""},
		{name: "minimum page size", query: "?page_size=1", wantIDs: ordered[0:1], wantNextToken: "1"},
		{name: "maximum page size", query: "?page_size=100", wantIDs: ordered, wantNextToken: ""},
		{name: "non-numeric token treated as zero", query: "?page_size=2&page_token=abc", wantIDs: ordered[0:2], wantNextToken: "2"},
		{name: "token past end", query: "?page_size=2&page_token=99", wantIDs: []string{}, wantNextToken: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := env.do(t, http.MethodGet, "/api/v1/tasks/overdue"+tc.query, "")
			expectStatus(t, rec, http.StatusOK)

			var resp models.ListTasksResponse
			decodeBody(t, rec, &resp)
			if got := responseIDs(resp.Tasks); !reflect.DeepEqual(got, tc.wantIDs) {
				t.Errorf("task IDs = %v, want %v", got, tc.wantIDs)
			}
			if resp.NextPageToken != tc.wantNextToken {
				t.Errorf("next_page_token = %q, want %q", resp.NextPageToken, tc.wantNextToken)
			}
			if resp.TotalCount != 5 {
				t.Errorf("total_count = %d, want 5", resp.TotalCount)
			}
			if tc.wantNextToken == "" && strings.Contains(rec.Body.String(), "next_page_token") {
				t.Errorf("body = %s, want next_page_token omitted", rec.Body.String())
			}
		})
	}
}

func TestListOverdueTasksHandlerValidation(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{name: "page_size zero", query: "?page_size=0"},
		{name: "page_size above maximum", query: "?page_size=101"},
		{name: "page_size negative", query: "?page_size=-1"},
		{name: "page_size non-numeric", query: "?page_size=abc"},
		{name: "page_size decimal", query: "?page_size=1.5"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			env.createTask(t, "overdue", timePtr(env.clock.Now().Add(-time.Hour)), "")

			rec := env.do(t, http.MethodGet, "/api/v1/tasks/overdue"+tc.query, "")
			expectError(t, rec, http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

func TestListOverdueTasksHandlerEmpty(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, env *testEnv)
	}{
		{name: "empty store", setup: func(t *testing.T, env *testEnv) {}},
		{name: "no overdue tasks", setup: func(t *testing.T, env *testEnv) {
			now := env.clock.Now()
			env.createTask(t, "future", timePtr(now.Add(time.Hour)), "")
			env.createTask(t, "done", timePtr(now.Add(-time.Hour)), models.TaskStatusCompleted)
			env.createTask(t, "no due", nil, "")
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			tc.setup(t, env)

			rec := env.do(t, http.MethodGet, "/api/v1/tasks/overdue", "")
			expectStatus(t, rec, http.StatusOK)

			var resp models.ListTasksResponse
			decodeBody(t, rec, &resp)
			if resp.TotalCount != 0 {
				t.Errorf("total_count = %d, want 0", resp.TotalCount)
			}
			if len(resp.Tasks) != 0 {
				t.Errorf("len(tasks) = %d, want 0", len(resp.Tasks))
			}
			if resp.NextPageToken != "" {
				t.Errorf("next_page_token = %q, want empty", resp.NextPageToken)
			}
			if !strings.Contains(rec.Body.String(), `"total_count":0`) {
				t.Errorf("body = %s, want it to contain \"total_count\":0", rec.Body.String())
			}
		})
	}
}

func TestOverdueRouteNotHandledByGetTask(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, env *testEnv)
	}{
		{name: "empty store", setup: func(t *testing.T, env *testEnv) {}},
		{name: "store with tasks", setup: func(t *testing.T, env *testEnv) {
			env.createTask(t, "late", timePtr(env.clock.Now().Add(-time.Hour)), "")
			env.createTask(t, "on time", nil, "")
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			tc.setup(t, env)

			rec := env.do(t, http.MethodGet, "/api/v1/tasks/overdue", "")
			if rec.Code == http.StatusNotFound {
				t.Fatalf("GET /api/v1/tasks/overdue returned 404 (routed to getTask?); body = %s", rec.Body.String())
			}
			expectStatus(t, rec, http.StatusOK)
			body := rec.Body.String()
			if strings.Contains(body, "NOT_FOUND") || strings.Contains(body, "task with ID overdue not found") {
				t.Errorf("body = %s, looks like a getTask not-found response", body)
			}
			var resp models.ListTasksResponse
			decodeBody(t, rec, &resp)
		})
	}

	t.Run("getTask still serves real ids", func(t *testing.T) {
		env := newTestEnv(t)
		id := env.createTask(t, "real", nil, "")
		rec := env.do(t, http.MethodGet, "/api/v1/tasks/"+id, "")
		expectStatus(t, rec, http.StatusOK)
		var task models.Task
		decodeBody(t, rec, &task)
		if task.ID != id {
			t.Errorf("id = %q, want %q", task.ID, id)
		}
	})
}

// ---------------------------------------------------------------------------
// POST /api/v1/tasks/:id/assign
// ---------------------------------------------------------------------------

func TestAssignTaskHandlerSuccess(t *testing.T) {
	tests := []struct {
		name  string
		email string
	}{
		{name: "simple email", email: "alice@example.com"},
		{name: "email with plus and subdomain", email: "first.last+tasks@mail.example.org"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			id := env.createTask(t, "Assign me", timePtr(env.clock.Now().Add(-time.Hour)), "")
			wantUpdatedAt := env.clock.Advance(30 * time.Minute)

			body := fmt.Sprintf(`{"assigned_to":%q}`, tc.email)
			rec := env.do(t, http.MethodPost, "/api/v1/tasks/"+id+"/assign", body)
			expectStatus(t, rec, http.StatusOK)

			var task models.Task
			decodeBody(t, rec, &task)
			if task.ID != id {
				t.Errorf("id = %q, want %q", task.ID, id)
			}
			if task.AssignedTo == nil || *task.AssignedTo != tc.email {
				t.Errorf("assigned_to = %v, want %q", task.AssignedTo, tc.email)
			}
			if !task.UpdatedAt.Equal(wantUpdatedAt) {
				t.Errorf("updated_at = %v, want %v", task.UpdatedAt, wantUpdatedAt)
			}
			if !strings.Contains(rec.Body.String(), fmt.Sprintf(`"assigned_to":%q`, tc.email)) {
				t.Errorf("body = %s, want assigned_to %q", rec.Body.String(), tc.email)
			}

			calls := env.notifier.Calls()
			if len(calls) != 1 {
				t.Fatalf("notifier called %d times, want 1", len(calls))
			}
			if calls[0].Assignee != tc.email {
				t.Errorf("notified assignee = %q, want %q", calls[0].Assignee, tc.email)
			}
			if calls[0].Task.ID != id {
				t.Errorf("notified task id = %q, want %q", calls[0].Task.ID, id)
			}

			// The assignment is visible through GET /tasks/:id.
			getRec := env.do(t, http.MethodGet, "/api/v1/tasks/"+id, "")
			expectStatus(t, getRec, http.StatusOK)
			var stored models.Task
			decodeBody(t, getRec, &stored)
			if stored.AssignedTo == nil || *stored.AssignedTo != tc.email {
				t.Errorf("stored assigned_to = %v, want %q", stored.AssignedTo, tc.email)
			}
		})
	}
}

func TestAssignTaskHandlerValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing field", body: `{}`},
		{name: "other field only", body: `{"assignee":"alice@example.com"}`},
		{name: "empty string", body: `{"assigned_to":""}`},
		{name: "invalid email", body: `{"assigned_to":"not-an-email"}`},
		{name: "email without domain", body: `{"assigned_to":"alice@"}`},
		{name: "wrong type", body: `{"assigned_to":123}`},
		{name: "null value", body: `{"assigned_to":null}`},
		{name: "malformed JSON", body: `{"assigned_to":`},
		{name: "not JSON", body: `assigned_to=alice@example.com`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			id := env.createTask(t, "Validate me", nil, "")

			rec := env.do(t, http.MethodPost, "/api/v1/tasks/"+id+"/assign", tc.body)
			expectError(t, rec, http.StatusBadRequest, "VALIDATION_ERROR")

			if calls := env.notifier.Calls(); len(calls) != 0 {
				t.Errorf("notifier called %d times, want 0", len(calls))
			}
			stored, err := env.svc.GetTask(id)
			if err != nil {
				t.Fatalf("GetTask returned error: %v", err)
			}
			if stored.AssignedTo != nil {
				t.Errorf("assigned_to = %q, want nil after a rejected request", *stored.AssignedTo)
			}
		})
	}

	t.Run("empty body", func(t *testing.T) {
		env := newTestEnv(t)
		id := env.createTask(t, "Validate me", nil, "")
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+id+"/assign", nil)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		env.router.ServeHTTP(rec, req)
		expectError(t, rec, http.StatusBadRequest, "VALIDATION_ERROR")
		if calls := env.notifier.Calls(); len(calls) != 0 {
			t.Errorf("notifier called %d times, want 0", len(calls))
		}
	})
}

func TestAssignTaskHandlerNotFound(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{name: "unknown id", id: "missing-task"},
		{name: "uuid-shaped id", id: "00000000-0000-0000-0000-000000000000"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			env.createTask(t, "Existing", nil, "")

			rec := env.do(t, http.MethodPost, "/api/v1/tasks/"+tc.id+"/assign", `{"assigned_to":"alice@example.com"}`)
			resp := expectError(t, rec, http.StatusNotFound, "NOT_FOUND")
			wantMsg := fmt.Sprintf("task with ID %s not found", tc.id)
			if resp.Error.Message != wantMsg {
				t.Errorf("message = %q, want %q", resp.Error.Message, wantMsg)
			}
			if calls := env.notifier.Calls(); len(calls) != 0 {
				t.Errorf("notifier called %d times, want 0", len(calls))
			}
		})
	}

	t.Run("validation runs before lookup", func(t *testing.T) {
		env := newTestEnv(t)
		rec := env.do(t, http.MethodPost, "/api/v1/tasks/missing-task/assign", `{"assigned_to":"nope"}`)
		expectError(t, rec, http.StatusBadRequest, "VALIDATION_ERROR")
	})
}

// ---------------------------------------------------------------------------
// Existing routes regression
// ---------------------------------------------------------------------------

func TestExistingRoutesRegression(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T, env *testEnv) (method, path, body string)
		check func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder)
	}{
		{
			name: "list tasks",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				env.createTask(t, "a", nil, "")
				env.createTask(t, "b", nil, "")
				env.createTask(t, "c", nil, "")
				return http.MethodGet, "/api/v1/tasks?page_size=2", ""
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectStatus(t, rec, http.StatusOK)
				var resp models.ListTasksResponse
				decodeBody(t, rec, &resp)
				if resp.TotalCount != 3 || len(resp.Tasks) != 2 || resp.NextPageToken != "2" {
					t.Errorf("got total=%d len=%d next=%q, want 3/2/\"2\"", resp.TotalCount, len(resp.Tasks), resp.NextPageToken)
				}
			},
		},
		{
			name: "list tasks rejects page_size=0",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				return http.MethodGet, "/api/v1/tasks?page_size=0", ""
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectError(t, rec, http.StatusBadRequest, "VALIDATION_ERROR")
			},
		},
		{
			name: "list tasks with status filter",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				env.createTask(t, "pending", nil, "")
				env.createTask(t, "done", nil, models.TaskStatusCompleted)
				return http.MethodGet, "/api/v1/tasks?status=completed", ""
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectStatus(t, rec, http.StatusOK)
				var resp models.ListTasksResponse
				decodeBody(t, rec, &resp)
				if resp.TotalCount != 1 || len(resp.Tasks) != 1 || resp.Tasks[0].Title != "done" {
					t.Errorf("got %+v, want only the completed task", resp)
				}
			},
		},
		{
			name: "get task",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				id := env.createTask(t, "fetch me", nil, "")
				return http.MethodGet, "/api/v1/tasks/" + id, ""
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectStatus(t, rec, http.StatusOK)
				var task models.Task
				decodeBody(t, rec, &task)
				if task.Title != "fetch me" {
					t.Errorf("title = %q, want %q", task.Title, "fetch me")
				}
			},
		},
		{
			name: "get unknown task",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				return http.MethodGet, "/api/v1/tasks/nope", ""
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				resp := expectError(t, rec, http.StatusNotFound, "NOT_FOUND")
				if resp.Error.Message != "task with ID nope not found" {
					t.Errorf("message = %q", resp.Error.Message)
				}
			},
		},
		{
			name: "create task",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				return http.MethodPost, "/api/v1/tasks", `{"title":"New task","priority":"high","tags":["x"]}`
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectStatus(t, rec, http.StatusCreated)
				var task models.Task
				decodeBody(t, rec, &task)
				if task.Title != "New task" || task.Priority != models.TaskPriorityHigh || task.Status != models.TaskStatusPending {
					t.Errorf("created task = %+v", task)
				}
				if !task.CreatedAt.Equal(baseTime) {
					t.Errorf("created_at = %v, want %v", task.CreatedAt, baseTime)
				}
				if loc := rec.Header().Get("Location"); loc != "/api/v1/tasks/"+task.ID {
					t.Errorf("Location = %q, want %q", loc, "/api/v1/tasks/"+task.ID)
				}
			},
		},
		{
			name: "create task without title",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				return http.MethodPost, "/api/v1/tasks", `{}`
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectError(t, rec, http.StatusBadRequest, "VALIDATION_ERROR")
			},
		},
		{
			name: "update task",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				id := env.createTask(t, "old", nil, "")
				return http.MethodPut, "/api/v1/tasks/" + id, `{"title":"new"}`
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectStatus(t, rec, http.StatusOK)
				var task models.Task
				decodeBody(t, rec, &task)
				if task.Title != "new" {
					t.Errorf("title = %q, want new", task.Title)
				}
			},
		},
		{
			name: "update task status",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				id := env.createTask(t, "finish", nil, "")
				return http.MethodPatch, "/api/v1/tasks/" + id + "/status", `{"status":"completed"}`
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectStatus(t, rec, http.StatusOK)
				var task models.Task
				decodeBody(t, rec, &task)
				if task.Status != models.TaskStatusCompleted || task.CompletedAt == nil {
					t.Errorf("status = %q completed_at = %v, want completed with timestamp", task.Status, task.CompletedAt)
				}
			},
		},
		{
			name: "update task status invalid",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				id := env.createTask(t, "finish", nil, "")
				return http.MethodPatch, "/api/v1/tasks/" + id + "/status", `{"status":"bogus"}`
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectError(t, rec, http.StatusBadRequest, "VALIDATION_ERROR")
			},
		},
		{
			name: "delete task",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				id := env.createTask(t, "remove", nil, "")
				return http.MethodDelete, "/api/v1/tasks/" + id, ""
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				if rec.Code != http.StatusNoContent {
					t.Fatalf("status = %d, want 204; body = %s", rec.Code, rec.Body.String())
				}
				resp, err := env.svc.ListTasks(models.ListTasksQuery{})
				if err != nil {
					t.Fatalf("ListTasks returned error: %v", err)
				}
				if resp.TotalCount != 0 {
					t.Errorf("total after delete = %d, want 0", resp.TotalCount)
				}
			},
		},
		{
			name: "stats",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				env.createTask(t, "a", nil, "")
				env.createTask(t, "b", nil, models.TaskStatusCompleted)
				return http.MethodGet, "/api/v1/tasks/stats", ""
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectStatus(t, rec, http.StatusOK)
				var stats models.TaskStats
				decodeBody(t, rec, &stats)
				if stats.Total != 2 {
					t.Errorf("total = %d, want 2", stats.Total)
				}
				if stats.ByStatus[models.TaskStatusPending] != 1 || stats.ByStatus[models.TaskStatusCompleted] != 1 {
					t.Errorf("by_status = %v, want pending=1 completed=1", stats.ByStatus)
				}
			},
		},
		{
			name: "search",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				env.createTask(t, "Quarterly report", nil, "")
				env.createTask(t, "Unrelated", nil, "")
				return http.MethodGet, "/api/v1/tasks/search?q=report", ""
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectStatus(t, rec, http.StatusOK)
				var resp models.SearchTasksResponse
				decodeBody(t, rec, &resp)
				if resp.TotalCount != 1 || resp.Query != "report" || len(resp.Tasks) != 1 {
					t.Errorf("search response = %+v, want one match for report", resp)
				}
			},
		},
		{
			name: "search without q",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				return http.MethodGet, "/api/v1/tasks/search", ""
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectError(t, rec, http.StatusBadRequest, "VALIDATION_ERROR")
			},
		},
		{
			name: "add comment",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				id := env.createTask(t, "discuss", nil, "")
				return http.MethodPost, "/api/v1/tasks/" + id + "/comments", `{"author":"alice","body":"looks good"}`
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectStatus(t, rec, http.StatusCreated)
				var comment models.Comment
				decodeBody(t, rec, &comment)
				if comment.Author != "alice" || comment.Body != "looks good" {
					t.Errorf("comment = %+v", comment)
				}
			},
		},
		{
			name: "list comments",
			build: func(t *testing.T, env *testEnv) (string, string, string) {
				id := env.createTask(t, "discuss", nil, "")
				return http.MethodGet, "/api/v1/tasks/" + id + "/comments", ""
			},
			check: func(t *testing.T, env *testEnv, rec *httptest.ResponseRecorder) {
				expectStatus(t, rec, http.StatusOK)
				var resp models.ListCommentsResponse
				decodeBody(t, rec, &resp)
				if resp.Total != 0 {
					t.Errorf("total = %d, want 0", resp.Total)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			method, path, body := tc.build(t, env)
			rec := env.do(t, method, path, body)
			tc.check(t, env, rec)
		})
	}
}

func TestDefaultServiceRoutes(t *testing.T) {
	// NewTaskService (as used by main.go) wired into the real router.
	r := gin.New()
	NewHandler(services.NewTaskService()).RegisterRoutes(r)

	tests := []struct {
		name      string
		path      string
		wantTotal int
	}{
		{name: "list returns seeded tasks", path: "/api/v1/tasks", wantTotal: 2},
		{name: "seeded tasks are not overdue", path: "/api/v1/tasks/overdue", wantTotal: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			expectStatus(t, rec, http.StatusOK)
			var resp models.ListTasksResponse
			decodeBody(t, rec, &resp)
			if resp.TotalCount != tc.wantTotal {
				t.Errorf("total_count = %d, want %d", resp.TotalCount, tc.wantTotal)
			}
		})
	}
}
