package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

var testNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	assignees []string
	err       error
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.assignees = append(f.assignees, assignee)
	return f.err
}

type testEnv struct {
	router   *gin.Engine
	service  *services.TaskService
	notifier *fakeNotifier
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	n := &fakeNotifier{}
	svc := services.NewTaskService(
		services.WithClock(func() time.Time { return testNow }),
		services.WithNotifier(n),
	)
	router := gin.New()
	NewHandler(svc).RegisterRoutes(router)
	return &testEnv{router: router, service: svc, notifier: n}
}

func (e *testEnv) do(method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *testEnv) addTask(t *testing.T, title string, due *time.Time) *models.Task {
	t.Helper()
	task, err := e.service.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task
}

func timePtr(t time.Time) *time.Time { return &t }

func decodeList(t *testing.T, w *httptest.ResponseRecorder) models.ListTasksResponse {
	t.Helper()
	var resp models.ListTasksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v (body %s)", err, w.Body.String())
	}
	return resp
}

func decodeError(t *testing.T, w *httptest.ResponseRecorder) models.ErrorDetail {
	t.Helper()
	var resp models.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error: %v (body %s)", err, w.Body.String())
	}
	return resp.Error
}

func titleList(tasks []models.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.Title)
	}
	return out
}

// ---- GET /api/v1/tasks/overdue ----

func TestOverdueRouteNotCapturedByIDRoute(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["tasks"]; !ok {
		t.Errorf("response is not a list response: %s", w.Body.String())
	}
	if _, ok := raw["error"]; ok {
		t.Errorf("overdue was handled as a task ID lookup: %s", w.Body.String())
	}

	// A real task ID still resolves through /:id.
	task := e.addTask(t, "t", nil)
	if w := e.do(http.MethodGet, "/api/v1/tasks/"+task.ID, ""); w.Code != http.StatusOK {
		t.Errorf("GET /:id status = %d, want 200", w.Code)
	}
	if w := e.do(http.MethodGet, "/api/v1/tasks/does-not-exist", ""); w.Code != http.StatusNotFound {
		t.Errorf("GET unknown /:id status = %d, want 404", w.Code)
	}
}

func TestListOverdueEndpoint(t *testing.T) {
	e := newTestEnv(t)
	e.addTask(t, "no-due", nil)
	e.addTask(t, "future", timePtr(testNow.Add(time.Hour)))
	e.addTask(t, "late", timePtr(testNow.Add(-time.Hour)))
	e.addTask(t, "later-still-overdue", timePtr(testNow.Add(-time.Minute)))
	e.addTask(t, "oldest", timePtr(testNow.Add(-48*time.Hour)))
	done := e.addTask(t, "done", timePtr(testNow.Add(-72*time.Hour)))
	cancelled := e.addTask(t, "cancelled", timePtr(testNow.Add(-72*time.Hour)))
	if _, err := e.service.UpdateTaskStatus(done.ID, models.TaskStatusCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.UpdateTaskStatus(cancelled.ID, models.TaskStatusCancelled); err != nil {
		t.Fatal(err)
	}

	w := e.do(http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	resp := decodeList(t, w)
	want := []string{"oldest", "late", "later-still-overdue"}
	got := titleList(resp.Tasks)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if resp.TotalCount != 3 || resp.NextPageToken != "" {
		t.Errorf("total=%d token=%q", resp.TotalCount, resp.NextPageToken)
	}
}

func TestListOverdueEndpointPagination(t *testing.T) {
	e := newTestEnv(t)
	for i := 0; i < 5; i++ {
		e.addTask(t, fmt.Sprintf("t%d", i), timePtr(testNow.Add(-time.Duration(10-i)*time.Hour)))
	}

	var all []string
	token := ""
	for page := 0; page < 10; page++ {
		path := "/api/v1/tasks/overdue?page_size=2"
		if token != "" {
			path += "&page_token=" + token
		}
		w := e.do(http.MethodGet, path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
		}
		resp := decodeList(t, w)
		if resp.TotalCount != 5 {
			t.Errorf("total = %d, want 5", resp.TotalCount)
		}
		all = append(all, titleList(resp.Tasks)...)
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	if fmt.Sprint(all) != fmt.Sprint([]string{"t0", "t1", "t2", "t3", "t4"}) {
		t.Errorf("paged order = %v", all)
	}
}

func TestListOverdueEndpointValidation(t *testing.T) {
	tests := []struct {
		name string
		path string
		code int
	}{
		{"page_size zero", "/api/v1/tasks/overdue?page_size=0", http.StatusBadRequest},
		{"page_size negative", "/api/v1/tasks/overdue?page_size=-1", http.StatusBadRequest},
		{"page_size too large", "/api/v1/tasks/overdue?page_size=101", http.StatusBadRequest},
		{"page_size not a number", "/api/v1/tasks/overdue?page_size=abc", http.StatusBadRequest},
		{"page_size max ok", "/api/v1/tasks/overdue?page_size=100", http.StatusOK},
		{"bad token falls back to start", "/api/v1/tasks/overdue?page_token=abc", http.StatusOK},
	}
	e := newTestEnv(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := e.do(http.MethodGet, tc.path, "")
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.code, w.Body.String())
			}
			if tc.code == http.StatusBadRequest {
				if d := decodeError(t, w); d.Code != "VALIDATION_ERROR" {
					t.Errorf("code = %q", d.Code)
				}
			}
		})
	}
}

// ---- POST /api/v1/tasks/:id/assign ----

func TestAssignEndpointSuccess(t *testing.T) {
	e := newTestEnv(t)
	task := e.addTask(t, "t", nil)

	w := e.do(http.MethodPost, "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"bob@example.com"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var got models.Task
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != task.ID || got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
		t.Errorf("unexpected task: %+v", got)
	}
	if !got.UpdatedAt.Equal(testNow) {
		t.Errorf("updated_at = %v, want %v", got.UpdatedAt, testNow)
	}
	if len(e.notifier.assignees) != 1 || e.notifier.assignees[0] != "bob@example.com" {
		t.Errorf("notifier calls = %v", e.notifier.assignees)
	}

	// The change is visible through GET.
	w = e.do(http.MethodGet, "/api/v1/tasks/"+task.ID, "")
	var fetched models.Task
	if err := json.Unmarshal(w.Body.Bytes(), &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.AssignedTo == nil || *fetched.AssignedTo != "bob@example.com" {
		t.Errorf("assignment not persisted: %+v", fetched)
	}
}

func TestAssignEndpointNotifierFailureStill200(t *testing.T) {
	e := newTestEnv(t)
	e.notifier.err = errors.New("boom")
	task := e.addTask(t, "t", nil)

	w := e.do(http.MethodPost, "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"bob@example.com"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	stored, _ := e.service.GetTask(task.ID)
	if stored.AssignedTo == nil || *stored.AssignedTo != "bob@example.com" {
		t.Errorf("assignment rolled back")
	}
}

func TestAssignEndpointErrors(t *testing.T) {
	tests := []struct {
		name     string
		useID    string // "" means use a real task ID
		body     string
		wantCode int
		wantErr  string
	}{
		{"malformed json", "", `{`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"empty body", "", ``, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"missing field", "", `{}`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"empty email", "", `{"assigned_to":""}`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"wrong type", "", `{"assigned_to":42}`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"invalid email", "", `{"assigned_to":"not-an-email"}`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"display name rejected", "", `{"assigned_to":"Bob <bob@example.com>"}`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"unknown task", "no-such-task", `{"assigned_to":"bob@example.com"}`, http.StatusNotFound, "NOT_FOUND"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			task := e.addTask(t, "t", nil)
			id := tc.useID
			if id == "" {
				id = task.ID
			}

			w := e.do(http.MethodPost, "/api/v1/tasks/"+id+"/assign", tc.body)
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.wantCode, w.Body.String())
			}
			d := decodeError(t, w)
			if d.Code != tc.wantErr || d.Message == "" {
				t.Errorf("error = %+v, want code %s", d, tc.wantErr)
			}
			if tc.wantErr == "NOT_FOUND" && d.Message != "task with ID no-such-task not found" {
				t.Errorf("message = %q", d.Message)
			}
			if len(e.notifier.assignees) != 0 {
				t.Errorf("notifier should not be called")
			}
			stored, _ := e.service.GetTask(task.ID)
			if stored.AssignedTo != nil {
				t.Errorf("task must stay unassigned")
			}
		})
	}
}

// ---- existing endpoint regression ----

func TestListTasksEndpointUnchanged(t *testing.T) {
	e := newTestEnv(t)
	for i := 0; i < 25; i++ {
		e.addTask(t, fmt.Sprintf("t%d", i), nil)
	}
	// 2 sample tasks + 25.
	w := e.do(http.MethodGet, "/api/v1/tasks", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	resp := decodeList(t, w)
	if len(resp.Tasks) != 20 || resp.NextPageToken != "20" || resp.TotalCount != 27 {
		t.Errorf("len=%d token=%q total=%d", len(resp.Tasks), resp.NextPageToken, resp.TotalCount)
	}
	w = e.do(http.MethodGet, "/api/v1/tasks?page_size=20&page_token=20", "")
	resp = decodeList(t, w)
	if len(resp.Tasks) != 7 || resp.NextPageToken != "" {
		t.Errorf("second page len=%d token=%q", len(resp.Tasks), resp.NextPageToken)
	}
	if w := e.do(http.MethodGet, "/api/v1/tasks?page_size=0", ""); w.Code != http.StatusBadRequest {
		t.Errorf("page_size=0 status = %d, want 400", w.Code)
	}
}
