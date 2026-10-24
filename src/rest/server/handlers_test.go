package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

var fixedNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, task.ID+"|"+assignee)
	return f.err
}

func (f *fakeNotifier) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type env struct {
	router *gin.Engine
	svc    *services.TaskService
	n      *fakeNotifier
}

func newEnv(t *testing.T) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService()
	svc.SetClock(func() time.Time { return fixedNow })
	n := &fakeNotifier{}
	svc.SetNotifier(n)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return &env{router: r, svc: svc, n: n}
}

func (e *env) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
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

func (e *env) addTask(t *testing.T, title string, due *time.Time) *models.Task {
	t.Helper()
	task, err := e.svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func ago(d time.Duration) *time.Time {
	v := fixedNow.Add(-d)
	return &v
}

func decodeList(t *testing.T, w *httptest.ResponseRecorder) models.ListTasksResponse {
	t.Helper()
	var resp models.ListTasksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	return resp
}

func decodeError(t *testing.T, w *httptest.ResponseRecorder) models.ErrorDetail {
	t.Helper()
	var resp models.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	return resp.Error
}

func TestOverdueEndpoint(t *testing.T) {
	e := newEnv(t)
	e.addTask(t, "newer", ago(time.Hour))
	e.addTask(t, "oldest", ago(48*time.Hour))
	e.addTask(t, "future", ago(-time.Hour))
	done := e.addTask(t, "done", ago(time.Hour))
	if _, err := e.svc.UpdateTaskStatus(done.ID, models.TaskStatusCompleted); err != nil {
		t.Fatal(err)
	}

	w := e.do(t, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	resp := decodeList(t, w)
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Tasks[0].Title != "oldest" || resp.Tasks[1].Title != "newer" {
		t.Errorf("order = %s, %s", resp.Tasks[0].Title, resp.Tasks[1].Title)
	}
}

func TestOverdueEndpointEmpty(t *testing.T) {
	e := newEnv(t)
	w := e.do(t, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	resp := decodeList(t, w)
	if resp.TotalCount != 0 || len(resp.Tasks) != 0 {
		t.Errorf("resp = %+v", resp)
	}
}

func TestOverdueEndpointPagination(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		e.addTask(t, fmt.Sprintf("t%d", i), ago(time.Duration(10-i)*time.Hour))
	}
	tests := []struct {
		name     string
		url      string
		code     int
		wantLen  int
		wantNext string
	}{
		{"first", "/api/v1/tasks/overdue?page_size=2", 200, 2, "2"},
		{"second", "/api/v1/tasks/overdue?page_size=2&page_token=2", 200, 2, "4"},
		{"last", "/api/v1/tasks/overdue?page_size=2&page_token=4", 200, 1, ""},
		{"page size too large", "/api/v1/tasks/overdue?page_size=101", 400, 0, ""},
		{"page size zero", "/api/v1/tasks/overdue?page_size=0", 400, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := e.do(t, http.MethodGet, tc.url, "")
			if w.Code != tc.code {
				t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
			}
			if tc.code != 200 {
				if code := decodeError(t, w).Code; code != "VALIDATION_ERROR" {
					t.Errorf("code = %s", code)
				}
				return
			}
			resp := decodeList(t, w)
			if len(resp.Tasks) != tc.wantLen || resp.NextPageToken != tc.wantNext || resp.TotalCount != 5 {
				t.Errorf("resp len=%d next=%q total=%d", len(resp.Tasks), resp.NextPageToken, resp.TotalCount)
			}
		})
	}
}

func TestOverdueRouteTakesPrecedenceOverID(t *testing.T) {
	e := newEnv(t)
	w := e.do(t, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["tasks"]; !ok {
		t.Errorf("expected list response, got %s", w.Body.String())
	}
	// Other IDs still resolve through /:id.
	w = e.do(t, http.MethodGet, "/api/v1/tasks/does-not-exist", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d", w.Code)
	}
}

func TestAssignEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		id       func(*models.Task) string
		body     string
		notifErr error
		code     int
		errCode  string
		notified bool
	}{
		{"success", func(t *models.Task) string { return t.ID }, `{"assigned_to":"bob@example.com"}`, nil, 200, "", true},
		{"notifier failure ignored", func(t *models.Task) string { return t.ID }, `{"assigned_to":"bob@example.com"}`, errors.New("down"), 200, "", true},
		{"unknown task", func(*models.Task) string { return "nope" }, `{"assigned_to":"bob@example.com"}`, nil, 404, "NOT_FOUND", false},
		{"invalid email", func(t *models.Task) string { return t.ID }, `{"assigned_to":"not-an-email"}`, nil, 400, "VALIDATION_ERROR", false},
		{"display name rejected", func(t *models.Task) string { return t.ID }, `{"assigned_to":"Bob <bob@example.com>"}`, nil, 400, "VALIDATION_ERROR", false},
		{"missing field", func(t *models.Task) string { return t.ID }, `{}`, nil, 400, "VALIDATION_ERROR", false},
		{"empty value", func(t *models.Task) string { return t.ID }, `{"assigned_to":""}`, nil, 400, "VALIDATION_ERROR", false},
		{"malformed json", func(t *models.Task) string { return t.ID }, `{`, nil, 400, "VALIDATION_ERROR", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.n.err = tc.notifErr
			task := e.addTask(t, "x", nil)
			w := e.do(t, http.MethodPost, "/api/v1/tasks/"+tc.id(task)+"/assign", tc.body)
			if w.Code != tc.code {
				t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
			}
			if tc.errCode != "" {
				if got := decodeError(t, w); got.Code != tc.errCode || got.Message == "" {
					t.Errorf("error = %+v", got)
				}
			} else {
				var got models.Task
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.ID != task.ID || got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
					t.Errorf("task = %+v", got)
				}
				if !got.UpdatedAt.Equal(fixedNow) {
					t.Errorf("updated_at = %v", got.UpdatedAt)
				}
			}
			calls := e.n.snapshot()
			if tc.notified {
				if len(calls) != 1 || calls[0] != task.ID+"|bob@example.com" {
					t.Errorf("notifier calls = %v", calls)
				}
			} else if len(calls) != 0 {
				t.Errorf("notifier unexpectedly called: %v", calls)
			}
		})
	}
}

func TestListTasksPaginationRegression(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		e.addTask(t, fmt.Sprintf("t%d", i), nil)
	}
	// 2 seeded + 3 created = 5 tasks.
	tests := []struct {
		name     string
		url      string
		wantLen  int
		wantNext string
	}{
		{"default", "/api/v1/tasks", 5, ""},
		{"first", "/api/v1/tasks?page_size=2", 2, "2"},
		{"second", "/api/v1/tasks?page_size=2&page_token=2", 2, "4"},
		{"last", "/api/v1/tasks?page_size=2&page_token=4", 1, ""},
		{"beyond end", "/api/v1/tasks?page_size=2&page_token=10", 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := e.do(t, http.MethodGet, tc.url, "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			resp := decodeList(t, w)
			if len(resp.Tasks) != tc.wantLen || resp.NextPageToken != tc.wantNext || resp.TotalCount != 5 {
				t.Errorf("len=%d next=%q total=%d", len(resp.Tasks), resp.NextPageToken, resp.TotalCount)
			}
		})
	}
}
