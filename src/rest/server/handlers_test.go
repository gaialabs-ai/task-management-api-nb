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

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.calls = append(f.calls, task.ID+":"+assignee)
	return f.err
}

func newTestRouter(t *testing.T) (*gin.Engine, *services.TaskService, *fakeNotifier) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService()
	n := &fakeNotifier{}
	svc.SetClock(func() time.Time { return fixedNow })
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

func createTask(t *testing.T, router *gin.Engine, title string, due *time.Time) models.Task {
	t.Helper()
	req := models.CreateTaskRequest{Title: title, DueDate: due}
	b, _ := json.Marshal(req)
	w := do(router, http.MethodPost, "/api/v1/tasks", string(b))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var task models.Task
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
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
		t.Fatalf("decode: %v: %s", err, w.Body)
	}
	return resp
}

func TestListOverdueEndpoint(t *testing.T) {
	router, _, _ := newTestRouter(t)
	createTask(t, router, "newer", ago(time.Hour))
	createTask(t, router, "oldest", ago(48*time.Hour))
	createTask(t, router, "future", ago(-time.Hour))
	createTask(t, router, "nodue", nil)
	done := createTask(t, router, "done", ago(time.Hour))
	if w := do(router, http.MethodPatch, "/api/v1/tasks/"+done.ID+"/status", `{"status":"completed"}`); w.Code != 200 {
		t.Fatalf("status: %d", w.Code)
	}

	w := do(router, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	resp := decodeList(t, w)
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 || resp.Tasks[0].Title != "oldest" || resp.Tasks[1].Title != "newer" {
		t.Fatalf("unexpected: %+v", resp)
	}
}

func TestOverdueRouteNotShadowedByID(t *testing.T) {
	router, _, _ := newTestRouter(t)
	w := do(router, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	var raw map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	if _, ok := raw["total_count"]; !ok {
		t.Fatalf("expected list response, got %s", w.Body)
	}
}

func TestOverdueEndpointPagination(t *testing.T) {
	router, _, _ := newTestRouter(t)
	for i := 0; i < 3; i++ {
		createTask(t, router, string(rune('a'+i)), ago(time.Duration(10-i)*time.Hour))
	}
	resp := decodeList(t, do(router, http.MethodGet, "/api/v1/tasks/overdue?page_size=2", ""))
	if len(resp.Tasks) != 2 || resp.NextPageToken != "2" || resp.TotalCount != 3 {
		t.Fatalf("page1: %+v", resp)
	}
	resp = decodeList(t, do(router, http.MethodGet, "/api/v1/tasks/overdue?page_size=2&page_token=2", ""))
	if len(resp.Tasks) != 1 || resp.Tasks[0].Title != "c" || resp.NextPageToken != "" {
		t.Fatalf("page2: %+v", resp)
	}
	if w := do(router, http.MethodGet, "/api/v1/tasks/overdue?page_size=0", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("page_size=0: %d", w.Code)
	}
	if w := do(router, http.MethodGet, "/api/v1/tasks/overdue?page_size=101", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("page_size=101: %d", w.Code)
	}
}

func TestListTasksPaginationRegression(t *testing.T) {
	router, svc, _ := newTestRouter(t)
	total := 2 + 3 // sample data + created
	for i := 0; i < 3; i++ {
		createTask(t, router, "t", nil)
	}
	_ = svc
	resp := decodeList(t, do(router, http.MethodGet, "/api/v1/tasks?page_size=2", ""))
	if len(resp.Tasks) != 2 || resp.NextPageToken != "2" || resp.TotalCount != total {
		t.Fatalf("page1: %+v", resp)
	}
	resp = decodeList(t, do(router, http.MethodGet, "/api/v1/tasks?page_size=2&page_token=4", ""))
	if len(resp.Tasks) != 1 || resp.NextPageToken != "" {
		t.Fatalf("last: %+v", resp)
	}
	resp = decodeList(t, do(router, http.MethodGet, "/api/v1/tasks", ""))
	if len(resp.Tasks) != total {
		t.Fatalf("default: %+v", resp)
	}
}

func decodeErr(t *testing.T, w *httptest.ResponseRecorder) models.ErrorResponse {
	t.Helper()
	var e models.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestAssignEndpoint(t *testing.T) {
	router, _, n := newTestRouter(t)
	task := createTask(t, router, "x", nil)

	tests := []struct {
		name     string
		id       string
		body     string
		code     int
		errCode  string
		notified bool
	}{
		{"success", task.ID, `{"assigned_to":"bob@example.com"}`, 200, "", true},
		{"unknown task", "nope", `{"assigned_to":"bob@example.com"}`, 404, "NOT_FOUND", false},
		{"invalid email", task.ID, `{"assigned_to":"bob"}`, 400, "VALIDATION_ERROR", false},
		{"display name", task.ID, `{"assigned_to":"Bob <bob@example.com>"}`, 400, "VALIDATION_ERROR", false},
		{"missing field", task.ID, `{}`, 400, "VALIDATION_ERROR", false},
		{"malformed json", task.ID, `{`, 400, "VALIDATION_ERROR", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n.calls = nil
			w := do(router, http.MethodPost, "/api/v1/tasks/"+tt.id+"/assign", tt.body)
			if w.Code != tt.code {
				t.Fatalf("code %d want %d: %s", w.Code, tt.code, w.Body)
			}
			if tt.errCode != "" {
				if got := decodeErr(t, w).Error.Code; got != tt.errCode {
					t.Fatalf("error code %q", got)
				}
			} else {
				var got models.Task
				_ = json.Unmarshal(w.Body.Bytes(), &got)
				if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
					t.Fatalf("body %s", w.Body)
				}
			}
			if (len(n.calls) == 1) != tt.notified {
				t.Fatalf("notifier calls %v", n.calls)
			}
		})
	}
}

func TestAssignNotifierFailureDoesNotFailRequest(t *testing.T) {
	router, _, n := newTestRouter(t)
	n.err = errors.New("boom")
	task := createTask(t, router, "x", nil)
	w := do(router, http.MethodPost, "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"bob@example.com"}`)
	if w.Code != http.StatusOK || len(n.calls) != 1 {
		t.Fatalf("code %d calls %v", w.Code, n.calls)
	}
	got := do(router, http.MethodGet, "/api/v1/tasks/"+task.ID, "")
	var tk models.Task
	_ = json.Unmarshal(got.Body.Bytes(), &tk)
	if tk.AssignedTo == nil || *tk.AssignedTo != "bob@example.com" {
		t.Fatalf("not persisted: %s", got.Body)
	}
}
