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

type recNotifier struct {
	calls int
	err   error
}

func (r *recNotifier) NotifyAssignment(models.Task, string) error {
	r.calls++
	return r.err
}

var now = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T, n services.Notifier) (*gin.Engine, *services.TaskService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService(services.WithClock(func() time.Time { return now }), services.WithNotifier(n))
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return r, svc
}

func do(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func create(t *testing.T, svc *services.TaskService, title string, due *time.Time) models.Task {
	t.Helper()
	task, err := svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatal(err)
	}
	return *task
}

func TestOverdueEndpoint(t *testing.T) {
	r, svc := setup(t, &recNotifier{})
	past1 := now.Add(-48 * time.Hour)
	past2 := now.Add(-24 * time.Hour)
	future := now.Add(24 * time.Hour)
	create(t, svc, "later-overdue", &past2)
	first := create(t, svc, "first-overdue", &past1)
	create(t, svc, "not-due", &future)
	done := create(t, svc, "done", &past1)
	if _, err := svc.UpdateTaskStatus(done.ID, models.TaskStatusCompleted); err != nil {
		t.Fatal(err)
	}

	w := do(r, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var resp models.ListTasksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 || resp.Tasks[0].ID != first.ID || resp.Tasks[1].Title != "later-overdue" {
		t.Errorf("unexpected response: %+v", resp)
	}

	var raw map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	if _, ok := raw["tasks"]; !ok {
		t.Error("missing tasks key")
	}
	if _, ok := raw["total_count"]; !ok {
		t.Error("missing total_count key")
	}

	// pagination
	w = do(r, http.MethodGet, "/api/v1/tasks/overdue?page_size=1", "")
	resp = models.ListTasksResponse{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Tasks) != 1 || resp.NextPageToken != "1" {
		t.Errorf("page 1: %+v", resp)
	}
}

func TestOverdueEmptyShapeAndPrecedence(t *testing.T) {
	r, _ := setup(t, &recNotifier{})
	w := do(r, http.MethodGet, "/api/v1/tasks/overdue", "")
	// must hit the overdue handler, not getTask("overdue") which would be 404
	if w.Code != http.StatusOK {
		t.Fatalf("/overdue shadowed by /:id: %d %s", w.Code, w.Body)
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["total_count"]; !ok {
		t.Errorf("not a list response: %s", w.Body)
	}
}

func TestOverdueBadQuery(t *testing.T) {
	r, _ := setup(t, &recNotifier{})
	for _, q := range []string{"page_size=0", "page_size=101", "page_size=abc"} {
		w := do(r, http.MethodGet, "/api/v1/tasks/overdue?"+q, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", q, w.Code)
			continue
		}
		var e models.ErrorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &e)
		if e.Error.Code != "VALIDATION_ERROR" {
			t.Errorf("%s: code %q", q, e.Error.Code)
		}
	}
}

func TestAssignEndpoint(t *testing.T) {
	n := &recNotifier{}
	r, svc := setup(t, n)
	task := create(t, svc, "t", nil)

	tests := []struct {
		name     string
		path     string
		body     string
		wantCode int
		wantErr  string
	}{
		{"success", "/api/v1/tasks/" + task.ID + "/assign", `{"assigned_to":"bob@example.com"}`, 200, ""},
		{"invalid email", "/api/v1/tasks/" + task.ID + "/assign", `{"assigned_to":"not-an-email"}`, 400, "VALIDATION_ERROR"},
		{"missing field", "/api/v1/tasks/" + task.ID + "/assign", `{}`, 400, "VALIDATION_ERROR"},
		{"malformed json", "/api/v1/tasks/" + task.ID + "/assign", `{`, 400, "VALIDATION_ERROR"},
		{"unknown task", "/api/v1/tasks/nope/assign", `{"assigned_to":"bob@example.com"}`, 404, "NOT_FOUND"},
		{"unknown task bad body is 400", "/api/v1/tasks/nope/assign", `{"assigned_to":"x"}`, 400, "VALIDATION_ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := do(r, http.MethodPost, tt.path, tt.body)
			if w.Code != tt.wantCode {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.wantCode, w.Body)
			}
			if tt.wantErr != "" {
				var e models.ErrorResponse
				if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error.Code != tt.wantErr || e.Error.Message == "" {
					t.Errorf("error body: %s", w.Body)
				}
				return
			}
			var got models.Task
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.ID != task.ID || got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
				t.Errorf("response: %+v", got)
			}
			if !got.UpdatedAt.Equal(now) {
				t.Errorf("updated_at = %v", got.UpdatedAt)
			}
		})
	}
	if n.calls != 1 {
		t.Errorf("notifier calls = %d, want 1", n.calls)
	}
}

func TestAssignNotifierFailureStill200(t *testing.T) {
	n := &recNotifier{err: errors.New("down")}
	r, svc := setup(t, n)
	task := create(t, svc, "t", nil)
	w := do(r, http.MethodPost, "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"a@b.co"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	got, _ := svc.GetTask(task.ID)
	if got.AssignedTo == nil || *got.AssignedTo != "a@b.co" {
		t.Error("assignment undone")
	}
}
