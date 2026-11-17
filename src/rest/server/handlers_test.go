package server

import (
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

func (f *fakeNotifier) NotifyAssigned(task models.Task, email string) {
	f.calls = append(f.calls, task.ID+"|"+email)
}

func setup(t *testing.T) (*gin.Engine, *services.TaskService, *fakeNotifier) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService()
	n := &fakeNotifier{}
	svc.SetNotifier(n)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return r, svc, n
}

func do(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func create(t *testing.T, svc *services.TaskService, title string, due *time.Time) *models.Task {
	t.Helper()
	task, err := svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestOverdueRoute(t *testing.T) {
	r, svc, _ := setup(t)
	past := time.Now().Add(-48 * time.Hour)
	earlier := time.Now().Add(-72 * time.Hour)
	future := time.Now().Add(48 * time.Hour)
	a := create(t, svc, "older", &earlier)
	b := create(t, svc, "newer", &past)
	create(t, svc, "future", &future)
	done := create(t, svc, "done", &past)
	if _, err := svc.UpdateTaskStatus(done.ID, models.TaskStatusCompleted); err != nil {
		t.Fatal(err)
	}

	w := do(r, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s (route shadowed by /:id?)", w.Code, w.Body)
	}
	var resp models.ListTasksResponse
	decode(t, w.Body.Bytes(), &resp)
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 || resp.Tasks[0].ID != a.ID || resp.Tasks[1].ID != b.ID {
		t.Errorf("unexpected response: %s", w.Body)
	}

	w = do(r, http.MethodGet, "/api/v1/tasks/overdue?page_size=1", "")
	decode(t, w.Body.Bytes(), &resp)
	if len(resp.Tasks) != 1 || resp.NextPageToken != "1" {
		t.Errorf("page 1: %s", w.Body)
	}
	w = do(r, http.MethodGet, "/api/v1/tasks/overdue?page_size=1&page_token=1", "")
	resp = models.ListTasksResponse{}
	decode(t, w.Body.Bytes(), &resp)
	if len(resp.Tasks) != 1 || resp.Tasks[0].ID != b.ID || resp.NextPageToken != "" {
		t.Errorf("page 2: %s", w.Body)
	}
}

func TestOverdueInvalidPageSize(t *testing.T) {
	r, _, _ := setup(t)
	for _, q := range []string{"page_size=0", "page_size=101", "page_size=abc"} {
		if w := do(r, http.MethodGet, "/api/v1/tasks/overdue?"+q, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, w.Code)
		}
	}
}

func TestAssignRoute(t *testing.T) {
	r, svc, n := setup(t)
	task := create(t, svc, "assignable", nil)

	tests := []struct {
		name string
		id   string
		body string
		want int
	}{
		{"valid", task.ID, `{"email":"bob@example.com"}`, http.StatusOK},
		{"invalid email", task.ID, `{"email":"nope"}`, http.StatusBadRequest},
		{"missing email", task.ID, `{}`, http.StatusBadRequest},
		{"malformed json", task.ID, `{`, http.StatusBadRequest},
		{"unknown task", "does-not-exist", `{"email":"bob@example.com"}`, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := do(r, http.MethodPost, "/api/v1/tasks/"+tt.id+"/assign", tt.body)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tt.want, w.Body)
			}
			if tt.want == http.StatusOK {
				var got models.Task
				decode(t, w.Body.Bytes(), &got)
				if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
					t.Errorf("assigned_to = %v", got.AssignedTo)
				}
			}
		})
	}
	if len(n.calls) != 1 || n.calls[0] != task.ID+"|bob@example.com" {
		t.Errorf("notifier calls = %v", n.calls)
	}
}

func TestExistingRoutesUnaffected(t *testing.T) {
	r, svc, _ := setup(t)
	task := create(t, svc, "x", nil)
	for _, p := range []string{"/health", "/api/v1/tasks", "/api/v1/tasks/stats", "/api/v1/tasks/search?q=x", "/api/v1/tasks/" + task.ID} {
		if w := do(r, http.MethodGet, p, ""); w.Code != http.StatusOK {
			t.Errorf("%s: status = %d", p, w.Code)
		}
	}
}
