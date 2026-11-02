package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

type fakeNotifier struct{ calls []string }

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.calls = append(f.calls, assignee)
	return nil
}

var now = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*gin.Engine, *services.TaskService, *fakeNotifier) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	n := &fakeNotifier{}
	svc := services.NewTaskService(services.WithClock(func() time.Time { return now }), services.WithNotifier(n))
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return r, svc, n
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func addTask(t *testing.T, svc *services.TaskService, title string, due time.Time) *models.Task {
	t.Helper()
	task, err := svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: &due})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e models.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	return e.Error.Code
}

func TestOverdueRouteNotCapturedByID(t *testing.T) {
	r, svc, _ := setup(t)
	addTask(t, svc, "late", now.Add(-time.Hour))
	w := do(r, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var resp models.ListTasksResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.TotalCount != 1 || resp.Tasks[0].Title != "late" {
		t.Fatalf("unexpected: %+v", resp)
	}
}

func TestOverduePagination(t *testing.T) {
	r, svc, _ := setup(t)
	for i := 0; i < 3; i++ {
		addTask(t, svc, "t", now.Add(-time.Duration(i+1)*time.Hour))
	}
	w := do(r, "GET", "/api/v1/tasks/overdue?page_size=2", "")
	var resp models.ListTasksResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Tasks) != 2 || resp.NextPageToken != "2" || resp.TotalCount != 3 {
		t.Fatalf("page1: %+v", resp)
	}
	w = do(r, "GET", "/api/v1/tasks/overdue?page_size=2&page_token=2", "")
	resp = models.ListTasksResponse{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Tasks) != 1 || resp.NextPageToken != "" {
		t.Fatalf("page2: %+v", resp)
	}
}

func TestOverdueBadPageSize(t *testing.T) {
	r, _, _ := setup(t)
	for _, q := range []string{"page_size=0", "page_size=101", "page_size=abc"} {
		w := do(r, "GET", "/api/v1/tasks/overdue?"+q, "")
		if w.Code != http.StatusBadRequest || errCode(t, w) != "VALIDATION_ERROR" {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body)
		}
	}
}

func TestAssignSuccess(t *testing.T) {
	r, svc, n := setup(t)
	task := addTask(t, svc, "x", now.Add(time.Hour))
	w := do(r, "POST", "/api/v1/tasks/"+task.ID+"/assign", `{"email":"bob@example.com"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got models.Task
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" || !got.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected: %+v", got)
	}
	if len(n.calls) != 1 {
		t.Fatal("notifier not called")
	}
}

func TestAssignBadRequests(t *testing.T) {
	r, svc, n := setup(t)
	task := addTask(t, svc, "x", now)
	for _, body := range []string{`{"email":"not-an-email"}`, `{}`, `{invalid json`, ``} {
		w := do(r, "POST", "/api/v1/tasks/"+task.ID+"/assign", body)
		if w.Code != http.StatusBadRequest || errCode(t, w) != "VALIDATION_ERROR" {
			t.Fatalf("%q: %d %s", body, w.Code, w.Body)
		}
	}
	if len(n.calls) != 0 {
		t.Fatal("notifier must not be called")
	}
}

func TestAssignNotFound(t *testing.T) {
	r, _, _ := setup(t)
	w := do(r, "POST", "/api/v1/tasks/nope/assign", `{"email":"bob@example.com"}`)
	if w.Code != http.StatusNotFound || errCode(t, w) != "NOT_FOUND" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
