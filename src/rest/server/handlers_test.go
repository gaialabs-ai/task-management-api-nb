package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

var now = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

type recNotifier struct {
	assignees []string
	err       error
}

func (r *recNotifier) NotifyAssigned(task models.Task, assignee string) error {
	r.assignees = append(r.assignees, assignee)
	return r.err
}

func newRouter(n services.Notifier) (*gin.Engine, *services.TaskService) {
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService(
		services.WithClock(func() time.Time { return now }),
		services.WithNotifier(n),
	)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return r, svc
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

func create(t *testing.T, r http.Handler, title string, due *time.Time) models.Task {
	t.Helper()
	body := map[string]interface{}{"title": title}
	if due != nil {
		body["due_date"] = due.Format(time.RFC3339)
	}
	b, _ := json.Marshal(body)
	w := do(r, "POST", "/api/v1/tasks", string(b))
	if w.Code != http.StatusCreated {
		t.Fatalf("create failed: %d %s", w.Code, w.Body)
	}
	var task models.Task
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	return task
}

func decodeList(t *testing.T, w *httptest.ResponseRecorder) models.ListTasksResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	var resp models.ListTasksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e models.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("bad error body %q: %v", w.Body, err)
	}
	return e.Error.Code
}

func hours(h int) *time.Time {
	t := now.Add(time.Duration(h) * time.Hour)
	return &t
}

func titles(tasks []models.Task) string {
	var out []string
	for _, t := range tasks {
		out = append(out, t.Title)
	}
	return strings.Join(out, ",")
}

func TestOverdueRouteNotShadowedByID(t *testing.T) {
	r, _ := newRouter(&recNotifier{})
	w := do(r, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"total_count"`) {
		t.Fatalf("not a list response: %s", w.Body)
	}
	// An ordinary id still resolves via /:id.
	if w := do(r, "GET", "/api/v1/tasks/does-not-exist", ""); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestListOverdueEndpoint(t *testing.T) {
	r, svc := newRouter(&recNotifier{})
	create(t, r, "future", hours(5))
	create(t, r, "nodue", nil)
	late := create(t, r, "late", hours(-1))
	create(t, r, "later", hours(-10))
	done := create(t, r, "done", hours(-20))
	cancelled := create(t, r, "cancelled", hours(-30))
	hold := create(t, r, "hold", hours(-2))
	svc.UpdateTaskStatus(done.ID, models.TaskStatusCompleted)
	svc.UpdateTaskStatus(cancelled.ID, models.TaskStatusCancelled)
	svc.UpdateTaskStatus(hold.ID, models.TaskStatusOnHold)
	_ = late

	resp := decodeList(t, do(r, "GET", "/api/v1/tasks/overdue", ""))
	if got, want := titles(resp.Tasks), "later,hold,late"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	if resp.TotalCount != 3 || resp.NextPageToken != "" {
		t.Errorf("total=%d next=%q", resp.TotalCount, resp.NextPageToken)
	}
}

func TestListOverduePaginationAndValidation(t *testing.T) {
	r, _ := newRouter(&recNotifier{})
	for i := 0; i < 5; i++ {
		create(t, r, fmt.Sprintf("o%d", i), hours(-10+i))
	}
	var all []string
	path := "/api/v1/tasks/overdue?page_size=2"
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("too many pages")
		}
		resp := decodeList(t, do(r, "GET", path, ""))
		if resp.TotalCount != 5 {
			t.Fatalf("total = %d", resp.TotalCount)
		}
		all = append(all, titles(resp.Tasks))
		if resp.NextPageToken == "" {
			break
		}
		path = "/api/v1/tasks/overdue?page_size=2&page_token=" + resp.NextPageToken
	}
	if got := strings.Join(all, "|"); got != "o0,o1|o2,o3|o4" {
		t.Fatalf("pages = %s", got)
	}

	for _, q := range []string{"page_size=0", "page_size=101", "page_size=-1", "page_size=abc"} {
		t.Run(q, func(t *testing.T) {
			w := do(r, "GET", "/api/v1/tasks/overdue?"+q, "")
			if w.Code != http.StatusBadRequest || errCode(t, w) != "VALIDATION_ERROR" {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}

func TestListTasksPaginationEndpoint(t *testing.T) {
	r, _ := newRouter(&recNotifier{})
	for i := 0; i < 4; i++ {
		create(t, r, fmt.Sprintf("p%d", i), nil)
	}
	total := len(decodeList(t, do(r, "GET", "/api/v1/tasks?page_size=100", "")).Tasks)
	seen := map[string]bool{}
	path := "/api/v1/tasks?page_size=2&sort_order=created_at_asc"
	for pages := 0; pages < 10; pages++ {
		resp := decodeList(t, do(r, "GET", path, ""))
		if resp.TotalCount != total {
			t.Fatalf("total = %d, want %d", resp.TotalCount, total)
		}
		for _, task := range resp.Tasks {
			if seen[task.ID] {
				t.Fatalf("duplicate %s across pages", task.ID)
			}
			seen[task.ID] = true
		}
		if resp.NextPageToken == "" {
			break
		}
		path = "/api/v1/tasks?page_size=2&sort_order=created_at_asc&page_token=" + resp.NextPageToken
	}
	if len(seen) != total {
		t.Fatalf("saw %d tasks, want %d", len(seen), total)
	}
	if w := do(r, "GET", "/api/v1/tasks?page_size=101", ""); w.Code != http.StatusBadRequest || errCode(t, w) != "VALIDATION_ERROR" {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestAssignEndpoint(t *testing.T) {
	n := &recNotifier{}
	r, _ := newRouter(n)
	task := create(t, r, "assign me", nil)
	url := "/api/v1/tasks/" + task.ID + "/assign"

	tests := []struct {
		name     string
		path     string
		body     string
		status   int
		code     string
		assignee string
	}{
		{"valid", url, `{"assigned_to":"carol@example.com"}`, 200, "", "carol@example.com"},
		{"missing field", url, `{}`, 400, "VALIDATION_ERROR", ""},
		{"empty email", url, `{"assigned_to":""}`, 400, "VALIDATION_ERROR", ""},
		{"invalid email", url, `{"assigned_to":"not-an-email"}`, 400, "VALIDATION_ERROR", ""},
		{"display name email", url, `{"assigned_to":"Carol <carol@example.com>"}`, 400, "VALIDATION_ERROR", ""},
		{"bad json", url, `{"assigned_to":`, 400, "VALIDATION_ERROR", ""},
		{"empty body", url, ``, 400, "VALIDATION_ERROR", ""},
		{"wrong type", url, `{"assigned_to":5}`, 400, "VALIDATION_ERROR", ""},
		{"unknown task", "/api/v1/tasks/missing/assign", `{"assigned_to":"carol@example.com"}`, 404, "NOT_FOUND", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(n.assignees)
			w := do(r, "POST", tt.path, tt.body)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tt.status, w.Body)
			}
			if tt.code != "" {
				if got := errCode(t, w); got != tt.code {
					t.Fatalf("code = %s, want %s", got, tt.code)
				}
				if len(n.assignees) != before {
					t.Error("notifier must not be called on failure")
				}
				return
			}
			var got models.Task
			json.Unmarshal(w.Body.Bytes(), &got)
			if got.ID != task.ID || got.AssignedTo == nil || *got.AssignedTo != tt.assignee {
				t.Fatalf("unexpected task %+v", got)
			}
			if len(n.assignees) != before+1 || n.assignees[len(n.assignees)-1] != tt.assignee {
				t.Errorf("notifier assignees = %v", n.assignees)
			}
		})
	}

	// The assignment persisted.
	w := do(r, "GET", "/api/v1/tasks/"+task.ID, "")
	var got models.Task
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &got)
	if got.AssignedTo == nil || *got.AssignedTo != "carol@example.com" {
		t.Fatalf("not persisted: %s", w.Body)
	}
}

func TestAssignNotifierFailureStill200(t *testing.T) {
	r, _ := newRouter(&recNotifier{err: errors.New("boom")})
	task := create(t, r, "t", nil)
	w := do(r, "POST", "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"d@example.com"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	var got models.Task
	json.Unmarshal(do(r, "GET", "/api/v1/tasks/"+task.ID, "").Body.Bytes(), &got)
	if got.AssignedTo == nil || *got.AssignedTo != "d@example.com" {
		t.Fatal("assignment rolled back")
	}
}
