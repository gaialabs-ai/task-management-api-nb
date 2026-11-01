package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

var now = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

type recNotifier struct {
	calls []string
	err   error
}

func (r *recNotifier) NotifyAssigned(task models.Task, to string) error {
	r.calls = append(r.calls, task.ID+"|"+to)
	return r.err
}

func setup(t *testing.T) (*gin.Engine, *services.TaskService, *recNotifier) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService()
	svc.SetClock(func() time.Time { return now })
	n := &recNotifier{}
	svc.SetNotifier(n)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return r, svc, n
}

func do(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func mk(t *testing.T, svc *services.TaskService, title string, due *time.Time) *models.Task {
	t.Helper()
	task, err := svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func ago(h int) *time.Time {
	v := now.Add(-time.Duration(h) * time.Hour)
	return &v
}

func decodeList(t *testing.T, w *httptest.ResponseRecorder) models.ListTasksResponse {
	t.Helper()
	var resp models.ListTasksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return resp
}

func TestOverdue_OrderingAndFilter(t *testing.T) {
	r, svc, _ := setup(t)
	mk(t, svc, "later", ago(1))
	mk(t, svc, "earliest", ago(48))
	mk(t, svc, "nodue", nil)
	fut := now.Add(time.Hour)
	mk(t, svc, "future", &fut)
	done := mk(t, svc, "done", ago(5))
	svc.UpdateTaskStatus(done.ID, models.TaskStatusCompleted)

	w := do(r, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	resp := decodeList(t, w)
	if len(resp.Tasks) != 2 || resp.Tasks[0].Title != "earliest" || resp.Tasks[1].Title != "later" || resp.TotalCount != 2 {
		t.Fatalf("unexpected: %s", w.Body)
	}
}

func TestOverdue_PrecedenceOverID(t *testing.T) {
	r, _, _ := setup(t)
	w := do(r, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != 200 {
		t.Fatalf("overdue shadowed by /:id: %d %s", w.Code, w.Body)
	}
}

func TestOverdue_Pagination(t *testing.T) {
	r, svc, _ := setup(t)
	for i := 1; i <= 3; i++ {
		mk(t, svc, "t", ago(10-i))
	}
	p1 := decodeList(t, do(r, "GET", "/api/v1/tasks/overdue?page_size=2", ""))
	if len(p1.Tasks) != 2 || p1.NextPageToken != "2" || p1.TotalCount != 3 {
		t.Fatalf("p1: %+v", p1)
	}
	p2 := decodeList(t, do(r, "GET", "/api/v1/tasks/overdue?page_size=2&page_token="+p1.NextPageToken, ""))
	if len(p2.Tasks) != 1 || p2.NextPageToken != "" {
		t.Fatalf("p2: %+v", p2)
	}
}

func TestOverdue_BadQuery(t *testing.T) {
	r, _, _ := setup(t)
	w := do(r, "GET", "/api/v1/tasks/overdue?page_size=abc", "")
	if w.Code != 400 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestListTasks_PaginationRegression(t *testing.T) {
	r, svc, _ := setup(t)
	for i := 0; i < 3; i++ {
		mk(t, svc, "t", nil)
	}
	// 2 sample tasks + 3 created
	p1 := decodeList(t, do(r, "GET", "/api/v1/tasks?page_size=2", ""))
	if len(p1.Tasks) != 2 || p1.NextPageToken != "2" || p1.TotalCount != 5 {
		t.Fatalf("p1: %+v", p1)
	}
	p3 := decodeList(t, do(r, "GET", "/api/v1/tasks?page_size=2&page_token=4", ""))
	if len(p3.Tasks) != 1 || p3.NextPageToken != "" {
		t.Fatalf("p3: %+v", p3)
	}
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e models.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	return e.Error.Code
}

func TestAssign(t *testing.T) {
	r, svc, n := setup(t)
	task := mk(t, svc, "x", nil)
	tests := []struct {
		name, id, body string
		status         int
		code           string
		notified       bool
	}{
		{"ok", task.ID, `{"assigned_to":"alice@example.com"}`, 200, "", true},
		{"invalid email", task.ID, `{"assigned_to":"nope"}`, 400, "VALIDATION_ERROR", false},
		{"display name", task.ID, `{"assigned_to":"A <a@b.co>"}`, 400, "VALIDATION_ERROR", false},
		{"missing field", task.ID, `{}`, 400, "VALIDATION_ERROR", false},
		{"bad json", task.ID, `{`, 400, "VALIDATION_ERROR", false},
		{"unknown task", "missing", `{"assigned_to":"alice@example.com"}`, 404, "NOT_FOUND", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n.calls = nil
			w := do(r, "POST", "/api/v1/tasks/"+tt.id+"/assign", tt.body)
			if w.Code != tt.status {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if tt.code != "" && errCode(t, w) != tt.code {
				t.Fatalf("code: %s", w.Body)
			}
			if (len(n.calls) == 1) != tt.notified {
				t.Fatalf("notifier calls: %v", n.calls)
			}
			if tt.status == 200 {
				var got models.Task
				json.Unmarshal(w.Body.Bytes(), &got)
				if got.AssignedTo == nil || *got.AssignedTo != "alice@example.com" || !strings.Contains(n.calls[0], "alice@example.com") {
					t.Fatalf("body: %s", w.Body)
				}
			}
		})
	}
}

func TestAssign_NotifierFailureStill200(t *testing.T) {
	r, svc, n := setup(t)
	n.err = errors.New("smtp down")
	task := mk(t, svc, "x", nil)
	w := do(r, "POST", "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"a@b.co"}`)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
}
