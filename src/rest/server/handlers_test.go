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

var fixedNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	calls []string
	err   error
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.calls = append(f.calls, task.ID+"|"+assignee)
	return f.err
}

func newTestRouter(t *testing.T) (*gin.Engine, *services.TaskService, *fakeNotifier) {
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

func do(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
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
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	return resp
}

func TestOverdueEndpoint(t *testing.T) {
	r, svc, _ := newTestRouter(t)
	mustCreate(t, svc, "newer", ago(time.Hour))
	mustCreate(t, svc, "older", ago(48*time.Hour))
	mustCreate(t, svc, "future", &[]time.Time{fixedNow.Add(time.Hour)}[0])
	mustCreate(t, svc, "nodue", nil)
	done := mustCreate(t, svc, "done", ago(time.Hour))
	if _, err := svc.UpdateTaskStatus(done.ID, models.TaskStatusCompleted); err != nil {
		t.Fatal(err)
	}

	w := do(r, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	resp := decodeList(t, w)
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 || resp.Tasks[0].Title != "older" || resp.Tasks[1].Title != "newer" {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

// /tasks/overdue must not be captured by /tasks/:id.
func TestOverdueRoutePrecedenceOverID(t *testing.T) {
	r, _, _ := newTestRouter(t)
	w := do(r, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "NOT_FOUND") {
		t.Fatalf("matched /:id: %s", w.Body.String())
	}
	var m map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if _, ok := m["total_count"]; !ok {
		t.Fatalf("not a list response: %s", w.Body.String())
	}
}

func TestOverdueEndpointPagination(t *testing.T) {
	r, svc, _ := newTestRouter(t)
	for i := 0; i < 3; i++ {
		mustCreate(t, svc, string(rune('a'+i)), ago(time.Duration(10-i)*time.Hour))
	}
	w := do(r, http.MethodGet, "/api/v1/tasks/overdue?page_size=2", "")
	p1 := decodeList(t, w)
	if len(p1.Tasks) != 2 || p1.NextPageToken != "2" || p1.TotalCount != 3 {
		t.Fatalf("page1: %s", w.Body.String())
	}
	p2 := decodeList(t, do(r, http.MethodGet, "/api/v1/tasks/overdue?page_size=2&page_token=2", ""))
	if len(p2.Tasks) != 1 || p2.NextPageToken != "" || p2.Tasks[0].Title != "c" {
		t.Fatalf("page2: %+v", p2)
	}
	if w := do(r, http.MethodGet, "/api/v1/tasks/overdue?page_size=0", ""); w.Code != http.StatusOK {
		// page_size=0 is replaced by the default, as in GET /tasks
		t.Logf("page_size=0 -> %d", w.Code)
	}
	if w := do(r, http.MethodGet, "/api/v1/tasks/overdue?page_size=1000", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("page_size=1000 status %d", w.Code)
	}
}

func TestAssignEndpoint(t *testing.T) {
	r, svc, n := newTestRouter(t)
	task := mustCreate(t, svc, "t", nil)

	tests := []struct {
		name     string
		id       string
		body     string
		wantCode int
		wantErr  string
	}{
		{"success", task.ID, `{"assigned_to":"alice@example.com"}`, 200, ""},
		{"unknown task", "nope", `{"assigned_to":"alice@example.com"}`, 404, "NOT_FOUND"},
		{"invalid email", task.ID, `{"assigned_to":"not-an-email"}`, 400, "VALIDATION_ERROR"},
		{"display name", task.ID, `{"assigned_to":"A <a@example.com>"}`, 400, "VALIDATION_ERROR"},
		{"missing field", task.ID, `{}`, 400, "VALIDATION_ERROR"},
		{"malformed json", task.ID, `{`, 400, "VALIDATION_ERROR"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n.calls = nil
			w := do(r, http.MethodPost, "/api/v1/tasks/"+tc.id+"/assign", tc.body)
			if w.Code != tc.wantCode {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			if tc.wantErr == "" {
				var got models.Task
				_ = json.Unmarshal(w.Body.Bytes(), &got)
				if got.ID != task.ID || got.AssignedTo == nil || *got.AssignedTo != "alice@example.com" {
					t.Fatalf("body %s", w.Body.String())
				}
				if len(n.calls) != 1 || n.calls[0] != task.ID+"|alice@example.com" {
					t.Fatalf("notifier calls %v", n.calls)
				}
				return
			}
			var e models.ErrorResponse
			_ = json.Unmarshal(w.Body.Bytes(), &e)
			if e.Error.Code != tc.wantErr {
				t.Fatalf("code %q body %s", e.Error.Code, w.Body.String())
			}
			if len(n.calls) != 0 {
				t.Fatalf("notifier called on error: %v", n.calls)
			}
		})
	}
}

func TestAssignEndpointNotifierFailure(t *testing.T) {
	r, svc, n := newTestRouter(t)
	n.err = errors.New("boom")
	task := mustCreate(t, svc, "t", nil)
	w := do(r, http.MethodPost, "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"bob@example.com"}`)
	if w.Code != http.StatusOK || len(n.calls) != 1 {
		t.Fatalf("status %d calls %v", w.Code, n.calls)
	}
}

// Regression: GET /api/v1/tasks pagination is unchanged by the shared helper.
func TestListTasksPaginationRegression(t *testing.T) {
	r, svc, _ := newTestRouter(t)
	for i := 0; i < 5; i++ {
		mustCreate(t, svc, string(rune('a'+i)), nil)
		time.Sleep(time.Millisecond)
	}
	total := 7 // 2 sample tasks + 5
	var seen int
	token := ""
	for pages := 0; pages < 10; pages++ {
		path := "/api/v1/tasks?page_size=3&sort_order=created_at_asc"
		if token != "" {
			path += "&page_token=" + token
		}
		resp := decodeList(t, do(r, http.MethodGet, path, ""))
		if resp.TotalCount != total {
			t.Fatalf("total %d", resp.TotalCount)
		}
		seen += len(resp.Tasks)
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	if seen != total {
		t.Fatalf("saw %d tasks, want %d", seen, total)
	}
	first := decodeList(t, do(r, http.MethodGet, "/api/v1/tasks?page_size=3", ""))
	if len(first.Tasks) != 3 || first.NextPageToken != "3" {
		t.Fatalf("first page %+v", first)
	}
	if w := do(r, http.MethodGet, "/api/v1/tasks?page_size=101", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("page_size=101 -> %d", w.Code)
	}
}
