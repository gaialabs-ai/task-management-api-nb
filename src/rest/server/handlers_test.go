package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

var fixedNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	calls []string
	err   error
}

func (f *fakeNotifier) NotifyAssignment(task models.Task, assignee string) error {
	f.calls = append(f.calls, task.ID+"|"+assignee)
	return f.err
}

func newTestRouter(t *testing.T) (*gin.Engine, *services.TaskService, *fakeNotifier) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService()
	// Drop sample data for deterministic assertions.
	all, _ := svc.ListTasks(models.ListTasksQuery{PageSize: 100})
	for _, task := range all.Tasks {
		if err := svc.DeleteTask(task.ID); err != nil {
			t.Fatal(err)
		}
	}
	svc.SetClock(func() time.Time { return fixedNow })
	n := &fakeNotifier{}
	svc.SetNotifier(n)
	router := gin.New()
	NewHandler(svc).RegisterRoutes(router)
	return router, svc, n
}

func do(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func mustCreate(t *testing.T, svc *services.TaskService, title string, due *time.Time) *models.Task {
	t.Helper()
	task, err := svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
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
		t.Fatalf("bad JSON %q: %v", w.Body.String(), err)
	}
	return resp
}

func decodeError(t *testing.T, w *httptest.ResponseRecorder) models.ErrorResponse {
	t.Helper()
	var resp models.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad JSON %q: %v", w.Body.String(), err)
	}
	return resp
}

func TestOverdueEndpoint_OrderAndFilter(t *testing.T) {
	router, svc, _ := newTestRouter(t)
	mustCreate(t, svc, "recent", ago(time.Hour))
	mustCreate(t, svc, "oldest", ago(72*time.Hour))
	mustCreate(t, svc, "future", ago(-time.Hour))
	mustCreate(t, svc, "no due", nil)
	done := mustCreate(t, svc, "done", ago(10*time.Hour))
	svc.UpdateTaskStatus(done.ID, models.TaskStatusCompleted)

	w := do(router, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	resp := decodeList(t, w)
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 || resp.Tasks[0].Title != "oldest" || resp.Tasks[1].Title != "recent" {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
}

func TestOverdueEndpoint_EmptyIsArray(t *testing.T) {
	router, _, _ := newTestRouter(t)
	w := do(router, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var raw map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &raw)
	if string(raw["tasks"]) != "[]" {
		t.Errorf("tasks = %s, want []", raw["tasks"])
	}
}

func TestOverdueEndpoint_Pagination(t *testing.T) {
	router, svc, _ := newTestRouter(t)
	for i, title := range []string{"a", "b", "c"} {
		mustCreate(t, svc, title, ago(time.Duration(10-i)*time.Hour))
	}
	r1 := decodeList(t, do(router, "GET", "/api/v1/tasks/overdue?page_size=2", ""))
	if len(r1.Tasks) != 2 || r1.NextPageToken != "2" || r1.TotalCount != 3 || r1.Tasks[0].Title != "a" {
		t.Fatalf("page 1: %+v", r1)
	}
	r2 := decodeList(t, do(router, "GET", "/api/v1/tasks/overdue?page_size=2&page_token="+r1.NextPageToken, ""))
	if len(r2.Tasks) != 1 || r2.NextPageToken != "" || r2.Tasks[0].Title != "c" {
		t.Fatalf("page 2: %+v", r2)
	}
}

func TestOverdueEndpoint_InvalidPageSize(t *testing.T) {
	router, _, _ := newTestRouter(t)
	for _, q := range []string{"page_size=0", "page_size=101", "page_size=abc"} {
		w := do(router, "GET", "/api/v1/tasks/overdue?"+q, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", q, w.Code)
			continue
		}
		if e := decodeError(t, w); e.Error.Code != "VALIDATION_ERROR" {
			t.Errorf("%s: code %q", q, e.Error.Code)
		}
	}
}

func TestOverdueRoutePrecedenceOverID(t *testing.T) {
	router, _, _ := newTestRouter(t)
	w := do(router, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("/overdue was shadowed by /:id: %d %s", w.Code, w.Body.String())
	}
	var resp models.ListTasksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// A task lookup of an unknown ID still 404s.
	if w := do(router, "GET", "/api/v1/tasks/does-not-exist", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown id status %d", w.Code)
	}
}

func TestListTasksPaginationRegression(t *testing.T) {
	router, svc, _ := newTestRouter(t)
	for _, title := range []string{"t0", "t1", "t2", "t3", "t4"} {
		mustCreate(t, svc, title, nil)
	}
	r1 := decodeList(t, do(router, "GET", "/api/v1/tasks?page_size=2&sort_order=created_at_asc", ""))
	if len(r1.Tasks) != 2 || r1.NextPageToken != "2" || r1.TotalCount != 5 || r1.Tasks[0].Title != "t0" {
		t.Fatalf("page 1: %+v", r1)
	}
	r3 := decodeList(t, do(router, "GET", "/api/v1/tasks?page_size=2&sort_order=created_at_asc&page_token=4", ""))
	if len(r3.Tasks) != 1 || r3.NextPageToken != "" || r3.Tasks[0].Title != "t4" {
		t.Fatalf("page 3: %+v", r3)
	}
	if w := do(router, "GET", "/api/v1/tasks?page_size=101", ""); w.Code != http.StatusBadRequest {
		t.Errorf("page_size=101 status %d", w.Code)
	}
}

func TestAssignEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		unknownID  bool
		wantStatus int
		wantCode   string
		wantNotify bool
	}{
		{name: "success", body: `{"assigned_to":"bob@example.com"}`, wantStatus: 200, wantNotify: true},
		{name: "invalid email", body: `{"assigned_to":"bob"}`, wantStatus: 400, wantCode: "VALIDATION_ERROR"},
		{name: "display name", body: `{"assigned_to":"Bob <bob@example.com>"}`, wantStatus: 400, wantCode: "VALIDATION_ERROR"},
		{name: "missing field", body: `{}`, wantStatus: 400, wantCode: "VALIDATION_ERROR"},
		{name: "malformed JSON", body: `{`, wantStatus: 400, wantCode: "VALIDATION_ERROR"},
		{name: "unknown task", body: `{"assigned_to":"bob@example.com"}`, unknownID: true, wantStatus: 404, wantCode: "NOT_FOUND"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, svc, n := newTestRouter(t)
			task := mustCreate(t, svc, "work", nil)
			id := task.ID
			if tt.unknownID {
				id = "missing"
			}
			w := do(router, "POST", "/api/v1/tasks/"+id+"/assign", tt.body)
			if w.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantStatus == 200 {
				var got models.Task
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" || got.ID != id {
					t.Errorf("unexpected task %+v", got)
				}
			} else if e := decodeError(t, w); e.Error.Code != tt.wantCode || e.Error.Message == "" {
				t.Errorf("error = %+v", e)
			}
			if tt.wantNotify != (len(n.calls) == 1) {
				t.Errorf("notifier calls = %v", n.calls)
			}
			if tt.wantNotify && n.calls[0] != id+"|bob@example.com" {
				t.Errorf("notifier call = %q", n.calls[0])
			}
		})
	}
}

func TestAssignEndpoint_NotifierFailureStill200(t *testing.T) {
	router, svc, n := newTestRouter(t)
	n.err = errors.New("smtp down")
	task := mustCreate(t, svc, "work", nil)
	w := do(router, "POST", "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"bob@example.com"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if len(n.calls) != 1 {
		t.Errorf("notifier calls = %v", n.calls)
	}
}
