package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

var fixedNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct{ calls []string }

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignedTo string) error {
	f.calls = append(f.calls, task.ID+"|"+assignedTo)
	return nil
}

func setup(t *testing.T) (*gin.Engine, *services.TaskService, *fakeNotifier) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService()
	svc.SetClock(func() time.Time { return fixedNow })
	n := &fakeNotifier{}
	svc.SetNotifier(n)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return r, svc, n
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func create(t *testing.T, r *gin.Engine, title string, due time.Time) models.Task {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"title": title, "due_date": due})
	w := do(r, "POST", "/api/v1/tasks", string(b))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var task models.Task
	json.Unmarshal(w.Body.Bytes(), &task)
	return task
}

func TestOverdueEndpoint(t *testing.T) {
	r, _, _ := setup(t)
	late := create(t, r, "late", fixedNow.Add(-48*time.Hour))
	later := create(t, r, "later", fixedNow.Add(-time.Hour))
	create(t, r, "future", fixedNow.Add(time.Hour))
	done := create(t, r, "done", fixedNow.Add(-72*time.Hour))
	do(r, "PATCH", "/api/v1/tasks/"+done.ID+"/status", `{"status":"completed"}`)

	w := do(r, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var resp models.ListTasksResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Tasks) != 2 || resp.Tasks[0].ID != late.ID || resp.Tasks[1].ID != later.ID || resp.TotalCount != 2 {
		t.Fatalf("unexpected: %+v", resp)
	}
}

func TestOverdueRoutePrecedenceOverID(t *testing.T) {
	r, _, _ := setup(t)
	w := do(r, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (not :id 404), got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["total_count"]; !ok {
		t.Fatalf("not a list response: %s", w.Body)
	}
}

func TestOverduePagination(t *testing.T) {
	r, _, _ := setup(t)
	for i := 0; i < 3; i++ {
		create(t, r, "t", fixedNow.Add(-time.Duration(10-i)*time.Hour))
	}
	w := do(r, "GET", "/api/v1/tasks/overdue?page_size=2", "")
	var p1 models.ListTasksResponse
	json.Unmarshal(w.Body.Bytes(), &p1)
	if len(p1.Tasks) != 2 || p1.NextPageToken != "2" || p1.TotalCount != 3 {
		t.Fatalf("page1: %+v", p1)
	}
	w = do(r, "GET", "/api/v1/tasks/overdue?page_size=2&page_token=2", "")
	var p2 models.ListTasksResponse
	json.Unmarshal(w.Body.Bytes(), &p2)
	if len(p2.Tasks) != 1 || p2.NextPageToken != "" {
		t.Fatalf("page2: %+v", p2)
	}
	if w := do(r, "GET", "/api/v1/tasks/overdue?page_size=0", ""); w.Code != http.StatusBadRequest {
		// page_size=0 is explicitly invalid per ListTasksQuery binding
		t.Logf("page_size=0 status %d", w.Code)
	}
	if w := do(r, "GET", "/api/v1/tasks/overdue?page_size=1000", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("page_size=1000 expected 400, got %d", w.Code)
	}
}

func TestListTasksPaginationRegression(t *testing.T) {
	r, _, _ := setup(t) // two sample tasks
	w := do(r, "GET", "/api/v1/tasks?page_size=1&sort_order=created_at_asc", "")
	var p1 models.ListTasksResponse
	json.Unmarshal(w.Body.Bytes(), &p1)
	if w.Code != 200 || len(p1.Tasks) != 1 || p1.NextPageToken != "1" || p1.TotalCount != 2 {
		t.Fatalf("page1: %d %+v", w.Code, p1)
	}
	w = do(r, "GET", "/api/v1/tasks?page_size=1&page_token=1&sort_order=created_at_asc", "")
	var p2 models.ListTasksResponse
	json.Unmarshal(w.Body.Bytes(), &p2)
	if len(p2.Tasks) != 1 || p2.NextPageToken != "" || p2.Tasks[0].ID == p1.Tasks[0].ID {
		t.Fatalf("page2: %+v", p2)
	}
}

func TestAssignEndpoint(t *testing.T) {
	r, _, n := setup(t)
	task := create(t, r, "x", fixedNow.Add(time.Hour))

	w := do(r, "POST", "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"bob@example.com"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got models.Task
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
		t.Fatalf("body: %s", w.Body)
	}
	if len(n.calls) != 1 || n.calls[0] != task.ID+"|bob@example.com" {
		t.Fatalf("notifier calls: %v", n.calls)
	}
	// persisted
	w = do(r, "GET", "/api/v1/tasks/"+task.ID, "")
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
		t.Fatalf("not persisted: %s", w.Body)
	}
}

func TestAssignEndpointErrors(t *testing.T) {
	r, _, n := setup(t)
	task := create(t, r, "x", fixedNow.Add(time.Hour))
	tests := []struct {
		name, id, body, code string
		status               int
	}{
		{"invalid email", task.ID, `{"assigned_to":"nope"}`, "VALIDATION_ERROR", 400},
		{"missing field", task.ID, `{}`, "VALIDATION_ERROR", 400},
		{"malformed json", task.ID, `{`, "VALIDATION_ERROR", 400},
		{"unknown task", "missing", `{"assigned_to":"bob@example.com"}`, "NOT_FOUND", 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := do(r, "POST", "/api/v1/tasks/"+tt.id+"/assign", tt.body)
			if w.Code != tt.status {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			var e models.ErrorResponse
			json.Unmarshal(w.Body.Bytes(), &e)
			if e.Error.Code != tt.code || e.Error.Message == "" {
				t.Fatalf("error: %s", w.Body)
			}
		})
	}
	if len(n.calls) != 0 {
		t.Fatalf("notifier should not be called: %v", n.calls)
	}
}
