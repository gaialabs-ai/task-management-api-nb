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

var testNow = time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	calls []string
	ids   []string
}

func (f *fakeNotifier) NotifyTaskAssigned(task models.Task, assignedTo string) error {
	f.calls = append(f.calls, assignedTo)
	f.ids = append(f.ids, task.ID)
	return nil
}

func setup(t *testing.T) (*gin.Engine, *services.TaskService, *fakeNotifier) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	n := &fakeNotifier{}
	svc := services.NewTaskService(
		services.WithClock(func() time.Time { return testNow }),
		services.WithNotifier(n),
	)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return r, svc, n
}

func do(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var buf *bytes.Buffer
	if body != "" {
		buf = bytes.NewBufferString(body)
	} else {
		buf = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, buf)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func createTask(t *testing.T, svc *services.TaskService, title string, due time.Duration, hasDue bool) *models.Task {
	t.Helper()
	req := models.CreateTaskRequest{Title: title}
	if hasDue {
		d := testNow.Add(due)
		req.DueDate = &d
	}
	task, err := svc.CreateTask(req)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func decodeList(t *testing.T, w *httptest.ResponseRecorder) models.ListTasksResponse {
	t.Helper()
	var resp models.ListTasksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return resp
}

func TestOverdueEndpoint(t *testing.T) {
	r, svc, _ := setup(t)
	createTask(t, svc, "future", time.Hour, true)
	createTask(t, svc, "none", 0, false)
	createTask(t, svc, "b-recent", -time.Hour, true)
	createTask(t, svc, "a-oldest", -48*time.Hour, true)
	done := createTask(t, svc, "done", -24*time.Hour, true)
	canc := createTask(t, svc, "canc", -24*time.Hour, true)
	svc.UpdateTaskStatus(done.ID, models.TaskStatusCompleted)
	svc.UpdateTaskStatus(canc.ID, models.TaskStatusCancelled)

	// Also proves the route is not captured by /:id (which would be 404 NOT_FOUND).
	w := do(r, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	resp := decodeList(t, w)
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
	if resp.Tasks[0].Title != "a-oldest" || resp.Tasks[1].Title != "b-recent" {
		t.Fatalf("bad order: %s, %s", resp.Tasks[0].Title, resp.Tasks[1].Title)
	}
}

func TestOverdueEndpoint_Pagination(t *testing.T) {
	r, svc, _ := setup(t)
	for i := 0; i < 3; i++ {
		createTask(t, svc, string(rune('a'+i)), -time.Duration(10-i)*time.Hour, true)
	}

	tests := []struct {
		name     string
		path     string
		code     int
		wantLen  int
		wantNext string
	}{
		{"first page", "/api/v1/tasks/overdue?page_size=2", 200, 2, "2"},
		{"second page", "/api/v1/tasks/overdue?page_size=2&page_token=2", 200, 1, ""},
		{"page size too large", "/api/v1/tasks/overdue?page_size=101", 400, 0, ""},
		{"page size zero", "/api/v1/tasks/overdue?page_size=0", 400, 0, ""},
		{"page size not a number", "/api/v1/tasks/overdue?page_size=abc", 400, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := do(r, http.MethodGet, tc.path, "")
			if w.Code != tc.code {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tc.code != 200 {
				var e models.ErrorResponse
				if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error.Code != "VALIDATION_ERROR" {
					t.Fatalf("bad error body: %s", w.Body.String())
				}
				return
			}
			resp := decodeList(t, w)
			if len(resp.Tasks) != tc.wantLen || resp.NextPageToken != tc.wantNext || resp.TotalCount != 3 {
				t.Fatalf("got %s", w.Body.String())
			}
		})
	}
}

func TestGetTaskStillWorksAndUnknownIDIs404(t *testing.T) {
	r, svc, _ := setup(t)
	task := createTask(t, svc, "x", 0, false)
	if w := do(r, http.MethodGet, "/api/v1/tasks/"+task.ID, ""); w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if w := do(r, http.MethodGet, "/api/v1/tasks/nope", ""); w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
}

func TestAssignEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		unknownID  bool
		body       string
		wantCode   int
		wantErr    string
		wantNotify bool
	}{
		{"success", false, `{"assigned_to":"bob@example.com"}`, 200, "", true},
		{"invalid email", false, `{"assigned_to":"not-an-email"}`, 400, "VALIDATION_ERROR", false},
		{"display name form rejected", false, `{"assigned_to":"Bob <bob@example.com>"}`, 400, "VALIDATION_ERROR", false},
		{"missing field", false, `{}`, 400, "VALIDATION_ERROR", false},
		{"empty email", false, `{"assigned_to":""}`, 400, "VALIDATION_ERROR", false},
		{"malformed json", false, `{"assigned_to":`, 400, "VALIDATION_ERROR", false},
		{"wrong type", false, `{"assigned_to":123}`, 400, "VALIDATION_ERROR", false},
		{"unknown task", true, `{"assigned_to":"bob@example.com"}`, 404, "NOT_FOUND", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, svc, n := setup(t)
			task := createTask(t, svc, "work", 0, false)
			id := task.ID
			if tc.unknownID {
				id = "missing"
			}
			w := do(r, http.MethodPost, "/api/v1/tasks/"+id+"/assign", tc.body)
			if w.Code != tc.wantCode {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tc.wantErr != "" {
				var e models.ErrorResponse
				if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error.Code != tc.wantErr {
					t.Fatalf("bad error body: %s", w.Body.String())
				}
			} else {
				var got models.Task
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.ID != id || got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
					t.Fatalf("bad task: %s", w.Body.String())
				}
			}
			if tc.wantNotify {
				if len(n.calls) != 1 || n.calls[0] != "bob@example.com" || n.ids[0] != id {
					t.Fatalf("notifier calls = %v %v", n.calls, n.ids)
				}
			} else if len(n.calls) != 0 {
				t.Fatalf("unexpected notifier calls: %v", n.calls)
			}
		})
	}
}
