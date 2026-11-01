package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	assignees []string
	taskIDs   []string
	err       error
}

func (f *fakeNotifier) Notify(task models.Task, assignee string) error {
	f.assignees = append(f.assignees, assignee)
	f.taskIDs = append(f.taskIDs, task.ID)
	return f.err
}

// newTestEnv builds a fresh engine with a service that has no sample tasks, a
// fixed clock and a fake notifier.
func newTestEnv(t *testing.T) (*gin.Engine, *services.TaskService, *fakeNotifier) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService()
	// Remove sample data.
	all, _ := svc.ListTasks(models.ListTasksQuery{PageSize: 100})
	for _, tk := range all.Tasks {
		if err := svc.DeleteTask(tk.ID); err != nil {
			t.Fatal(err)
		}
	}
	svc.SetClock(func() time.Time { return fixedNow })
	n := &fakeNotifier{}
	svc.SetNotifier(n)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return r, svc, n
}

func mkTask(t *testing.T, svc *services.TaskService, title string, due *time.Time) *models.Task {
	t.Helper()
	tk, err := svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func do(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != "" {
		rd = bytes.NewReader([]byte(body))
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeList(t *testing.T, w *httptest.ResponseRecorder) models.ListTasksResponse {
	t.Helper()
	var resp models.ListTasksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json %q: %v", w.Body.String(), err)
	}
	return resp
}

func decodeErr(t *testing.T, w *httptest.ResponseRecorder) models.ErrorResponse {
	t.Helper()
	var resp models.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json %q: %v", w.Body.String(), err)
	}
	return resp
}

func titles(l models.ListTasksResponse) []string {
	out := []string{}
	for _, tk := range l.Tasks {
		out = append(out, tk.Title)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ago(h int) *time.Time {
	d := fixedNow.Add(-time.Duration(h) * time.Hour)
	return &d
}

func TestListOverdueHTTP(t *testing.T) {
	r, svc, _ := newTestEnv(t)
	future := fixedNow.Add(time.Hour)
	mkTask(t, svc, "late-10", ago(10))
	mkTask(t, svc, "late-50", ago(50))
	mkTask(t, svc, "future", &future)
	mkTask(t, svc, "nodue", nil)
	done := mkTask(t, svc, "done", ago(5))
	if _, err := svc.UpdateTaskStatus(done.ID, models.TaskStatusCompleted); err != nil {
		t.Fatal(err)
	}

	w := do(r, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	resp := decodeList(t, w)
	if got := titles(resp); !eq(got, []string{"late-50", "late-10"}) {
		t.Errorf("got %v", got)
	}
	if resp.TotalCount != 2 || resp.NextPageToken != "" {
		t.Errorf("total=%d next=%q", resp.TotalCount, resp.NextPageToken)
	}
}

func TestListOverdueEmptyHTTP(t *testing.T) {
	r, _, _ := newTestEnv(t)
	w := do(r, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var raw map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &raw)
	if raw["total_count"].(float64) != 0 {
		t.Errorf("body %s", w.Body.String())
	}
}

func TestOverdueRoutePrecedenceOverID(t *testing.T) {
	r, svc, _ := newTestEnv(t)
	mkTask(t, svc, "late", ago(1))
	w := do(r, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var raw map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &raw)
	if _, ok := raw["tasks"]; !ok {
		t.Errorf("expected list response, got %s", w.Body.String())
	}
	// A real ID still resolves via /:id.
	tk := mkTask(t, svc, "x", nil)
	if w := do(r, "GET", "/api/v1/tasks/"+tk.ID, ""); w.Code != http.StatusOK {
		t.Errorf("get by id: %d", w.Code)
	}
}

func TestListOverduePaginationHTTP(t *testing.T) {
	r, svc, _ := newTestEnv(t)
	for i := 0; i < 5; i++ {
		mkTask(t, svc, fmt.Sprintf("t%d", i), ago(100-i))
	}
	w := do(r, "GET", "/api/v1/tasks/overdue?page_size=2", "")
	p1 := decodeList(t, w)
	if !eq(titles(p1), []string{"t0", "t1"}) || p1.NextPageToken != "2" || p1.TotalCount != 5 {
		t.Fatalf("page1 %+v", p1)
	}
	p2 := decodeList(t, do(r, "GET", "/api/v1/tasks/overdue?page_size=2&page_token=2", ""))
	if !eq(titles(p2), []string{"t2", "t3"}) || p2.NextPageToken != "4" {
		t.Fatalf("page2 %+v", p2)
	}
	p3 := decodeList(t, do(r, "GET", "/api/v1/tasks/overdue?page_size=2&page_token=4", ""))
	if !eq(titles(p3), []string{"t4"}) || p3.NextPageToken != "" {
		t.Fatalf("page3 %+v", p3)
	}
}

func TestListOverdueBadQueryHTTP(t *testing.T) {
	for _, q := range []string{"page_size=0", "page_size=101", "page_size=abc", "page_size=-1"} {
		t.Run(q, func(t *testing.T) {
			r, _, _ := newTestEnv(t)
			w := do(r, "GET", "/api/v1/tasks/overdue?"+q, "")
			// page_size=0 is rejected by the min=1 binding only if provided explicitly;
			// the existing listTasks has identical behaviour, so compare against it.
			ref := do(r, "GET", "/api/v1/tasks?"+q, "")
			if w.Code != ref.Code {
				t.Errorf("overdue=%d list=%d", w.Code, ref.Code)
			}
			if w.Code == http.StatusBadRequest && decodeErr(t, w).Error.Code != "VALIDATION_ERROR" {
				t.Errorf("body %s", w.Body.String())
			}
		})
	}
	r, _, _ := newTestEnv(t)
	if w := do(r, "GET", "/api/v1/tasks/overdue?page_size=101", ""); w.Code != http.StatusBadRequest {
		t.Errorf("page_size=101 gave %d", w.Code)
	}
}

func TestAssignHTTP(t *testing.T) {
	tests := []struct {
		name       string
		realID     bool
		body       string
		wantCode   int
		wantErr    string
		wantNotify int
	}{
		{"success", true, `{"assigned_to":"bob@example.com"}`, 200, "", 1},
		{"invalid json", true, `{not json`, 400, "VALIDATION_ERROR", 0},
		{"missing field", true, `{}`, 400, "VALIDATION_ERROR", 0},
		{"empty email", true, `{"assigned_to":""}`, 400, "VALIDATION_ERROR", 0},
		{"bad email", true, `{"assigned_to":"nope"}`, 400, "VALIDATION_ERROR", 0},
		{"display name", true, `{"assigned_to":"Bob <bob@example.com>"}`, 400, "VALIDATION_ERROR", 0},
		{"unknown task", false, `{"assigned_to":"bob@example.com"}`, 404, "NOT_FOUND", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, svc, n := newTestEnv(t)
			tk := mkTask(t, svc, "work", nil)
			id := tk.ID
			if !tt.realID {
				id = "does-not-exist"
			}
			w := do(r, "POST", "/api/v1/tasks/"+id+"/assign", tt.body)
			if w.Code != tt.wantCode {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tt.wantErr != "" {
				e := decodeErr(t, w)
				if e.Error.Code != tt.wantErr || e.Error.Message == "" {
					t.Errorf("error %+v", e)
				}
				if tt.wantCode == 404 && e.Error.Message != "task with ID does-not-exist not found" {
					t.Errorf("message %q", e.Error.Message)
				}
			} else {
				var got models.Task
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.ID != id || got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
					t.Errorf("task %+v", got)
				}
				if !got.UpdatedAt.Equal(fixedNow) {
					t.Errorf("updated_at %v", got.UpdatedAt)
				}
			}
			if len(n.assignees) != tt.wantNotify {
				t.Errorf("notify calls %d, want %d", len(n.assignees), tt.wantNotify)
			}
			if tt.wantNotify == 1 && (n.assignees[0] != "bob@example.com" || n.taskIDs[0] != id) {
				t.Errorf("notified %v %v", n.assignees, n.taskIDs)
			}
		})
	}
}

func TestAssignNotifierFailureHTTP(t *testing.T) {
	r, svc, n := newTestEnv(t)
	n.err = errors.New("smtp down")
	tk := mkTask(t, svc, "work", nil)
	w := do(r, "POST", "/api/v1/tasks/"+tk.ID+"/assign", `{"assigned_to":"bob@example.com"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if len(n.assignees) != 1 {
		t.Errorf("notify calls %d", len(n.assignees))
	}
}

func TestListTasksPaginationRegressionHTTP(t *testing.T) {
	r, svc, _ := newTestEnv(t)
	for i := 0; i < 5; i++ {
		mkTask(t, svc, fmt.Sprintf("t%d", i), nil)
	}
	base := "/api/v1/tasks?sort_order=created_at_asc"

	all := decodeList(t, do(r, "GET", base, ""))
	if len(all.Tasks) != 5 || all.TotalCount != 5 || all.NextPageToken != "" {
		t.Fatalf("default page %+v", all)
	}
	p1 := decodeList(t, do(r, "GET", base+"&page_size=2", ""))
	if len(p1.Tasks) != 2 || p1.NextPageToken != "2" || p1.TotalCount != 5 {
		t.Fatalf("p1 %+v", p1)
	}
	p3 := decodeList(t, do(r, "GET", base+"&page_size=2&page_token=4", ""))
	if len(p3.Tasks) != 1 || p3.NextPageToken != "" {
		t.Fatalf("p3 %+v", p3)
	}
	beyond := decodeList(t, do(r, "GET", base+"&page_size=2&page_token=50", ""))
	if len(beyond.Tasks) != 0 || beyond.NextPageToken != "" || beyond.TotalCount != 5 {
		t.Fatalf("beyond %+v", beyond)
	}
	if w := do(r, "GET", "/api/v1/tasks?page_size=1000", ""); w.Code != http.StatusBadRequest {
		t.Errorf("page_size=1000 gave %d", w.Code)
	}
}
