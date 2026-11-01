package server_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/rest/server"
	"github.com/gin-gonic/gin"
)

var fixedNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	calls []string
	err   error
}

func (f *fakeNotifier) NotifyAssigned(_ models.Task, assignee string) error {
	f.calls = append(f.calls, assignee)
	return f.err
}

type env struct {
	t      *testing.T
	svc    *services.TaskService
	router *gin.Engine
	n      *fakeNotifier
}

func newEnv(t *testing.T) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService()
	n := &fakeNotifier{}
	svc.SetClock(func() time.Time { return fixedNow })
	svc.SetNotifier(n)
	r := gin.New()
	server.NewHandler(svc).RegisterRoutes(r)
	return &env{t: t, svc: svc, router: r, n: n}
}

func (e *env) do(method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *env) create(title string, due *time.Time, status models.TaskStatus) *models.Task {
	e.t.Helper()
	task, err := e.svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		e.t.Fatal(err)
	}
	if status != "" {
		if _, err := e.svc.UpdateTaskStatus(task.ID, status); err != nil {
			e.t.Fatal(err)
		}
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
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return resp
}

func decodeErr(t *testing.T, w *httptest.ResponseRecorder) models.ErrorResponse {
	t.Helper()
	var resp models.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return resp
}

func titles(tasks []models.Task) []string {
	out := make([]string, len(tasks))
	for i, task := range tasks {
		out[i] = task.Title
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

func TestOverdue_Empty(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/api/v1/tasks/overdue", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	resp := decodeList(t, w)
	if resp.TotalCount != 0 || len(resp.Tasks) != 0 || resp.NextPageToken != "" {
		t.Errorf("unexpected %+v", resp)
	}
}

func TestOverdue_FilterAndOrder(t *testing.T) {
	e := newEnv(t)
	e.create("b", ago(10*time.Hour), "")
	e.create("a", ago(30*time.Hour), models.TaskStatusInProgress)
	e.create("done", ago(50*time.Hour), models.TaskStatusCompleted)
	e.create("cancelled", ago(50*time.Hour), models.TaskStatusCancelled)
	e.create("future", ago(-time.Hour), "")
	e.create("nodue", nil, "")

	w := e.do("GET", "/api/v1/tasks/overdue", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct == "" {
		t.Error("missing content type")
	}
	resp := decodeList(t, w)
	if got, want := titles(resp.Tasks), []string{"a", "b"}; !eq(got, want) {
		t.Errorf("tasks = %v, want %v", got, want)
	}
	if resp.TotalCount != 2 {
		t.Errorf("total_count = %d", resp.TotalCount)
	}

	// Raw JSON uses snake_case keys.
	var raw map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &raw)
	if _, ok := raw["total_count"]; !ok {
		t.Errorf("missing total_count in %s", w.Body)
	}
	if _, ok := raw["next_page_token"]; ok {
		t.Errorf("next_page_token should be omitted on last page: %s", w.Body)
	}
}

func TestOverdue_IDTiebreak(t *testing.T) {
	e := newEnv(t)
	due := ago(time.Hour)
	var ids []string
	for i := 0; i < 6; i++ {
		ids = append(ids, e.create(fmt.Sprintf("t%d", i), due, "").ID)
	}
	sort.Strings(ids)
	resp := decodeList(t, e.do("GET", "/api/v1/tasks/overdue", ""))
	for i, task := range resp.Tasks {
		if task.ID != ids[i] {
			t.Fatalf("position %d: %s, want %s", i, task.ID, ids[i])
		}
	}
}

func TestOverdue_Pagination(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		e.create(fmt.Sprintf("t%d", i), ago(time.Duration(10-i)*time.Hour), "")
	}
	tests := []struct {
		name     string
		query    string
		want     []string
		wantNext string
	}{
		{"first", "?page_size=2", []string{"t0", "t1"}, "2"},
		{"second", "?page_size=2&page_token=2", []string{"t2", "t3"}, "4"},
		{"last", "?page_size=2&page_token=4", []string{"t4"}, ""},
		{"out of range", "?page_size=2&page_token=50", []string{}, ""},
		{"default size", "", []string{"t0", "t1", "t2", "t3", "t4"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := e.do("GET", "/api/v1/tasks/overdue"+tt.query, "")
			if w.Code != 200 {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			resp := decodeList(t, w)
			if got := titles(resp.Tasks); !eq(got, tt.want) {
				t.Errorf("tasks = %v, want %v", got, tt.want)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("next = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
			if resp.TotalCount != 5 {
				t.Errorf("total = %d", resp.TotalCount)
			}
		})
	}
}

func TestOverdue_InvalidQuery(t *testing.T) {
	e := newEnv(t)
	for _, q := range []string{"?page_size=0", "?page_size=101", "?page_size=-1", "?page_size=abc"} {
		t.Run(q, func(t *testing.T) {
			w := e.do("GET", "/api/v1/tasks/overdue"+q, "")
			if w.Code != 400 {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if c := decodeErr(t, w).Error.Code; c != "VALIDATION_ERROR" {
				t.Errorf("code = %q", c)
			}
		})
	}
}

// /tasks/overdue must not be captured by /tasks/:id.
func TestOverdue_RoutePrecedenceOverID(t *testing.T) {
	e := newEnv(t)
	e.create("late", ago(time.Hour), "")
	w := e.do("GET", "/api/v1/tasks/overdue", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var raw map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &raw)
	if _, ok := raw["tasks"]; !ok {
		t.Errorf("got a task/error shape instead of a list: %s", w.Body)
	}
	// Other ids still resolve to getTask.
	if w := e.do("GET", "/api/v1/tasks/nonexistent", ""); w.Code != 404 {
		t.Errorf("status %d, want 404", w.Code)
	}
}

func TestAssign_Success(t *testing.T) {
	e := newEnv(t)
	task := e.create("a", nil, "")
	w := e.do("POST", "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"alice@example.com"}`)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got models.Task
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != task.ID || got.AssignedTo == nil || *got.AssignedTo != "alice@example.com" {
		t.Errorf("unexpected body %s", w.Body)
	}
	if len(e.n.calls) != 1 || e.n.calls[0] != "alice@example.com" {
		t.Errorf("notifier calls = %v", e.n.calls)
	}
	// Persisted: visible via GET.
	w = e.do("GET", "/api/v1/tasks/"+task.ID, "")
	var fetched models.Task
	json.Unmarshal(w.Body.Bytes(), &fetched)
	if fetched.AssignedTo == nil || *fetched.AssignedTo != "alice@example.com" {
		t.Errorf("not persisted: %s", w.Body)
	}
}

func TestAssign_NotifierFailureStill200(t *testing.T) {
	e := newEnv(t)
	e.n.err = errors.New("boom")
	task := e.create("a", nil, "")
	w := e.do("POST", "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"a@b.co"}`)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if len(e.n.calls) != 1 {
		t.Errorf("calls = %v", e.n.calls)
	}
}

func TestAssign_Errors(t *testing.T) {
	e := newEnv(t)
	task := e.create("a", nil, "")
	tests := []struct {
		name     string
		id       string
		body     string
		wantCode int
		wantErr  string
	}{
		{"malformed json", task.ID, `{not json`, 400, "VALIDATION_ERROR"},
		{"empty body", task.ID, ``, 400, "VALIDATION_ERROR"},
		{"missing field", task.ID, `{}`, 400, "VALIDATION_ERROR"},
		{"empty email", task.ID, `{"assigned_to":""}`, 400, "VALIDATION_ERROR"},
		{"display name", task.ID, `{"assigned_to":"Name <a@b.c>"}`, 400, "VALIDATION_ERROR"},
		{"bad email", task.ID, `{"assigned_to":"bad"}`, 400, "VALIDATION_ERROR"},
		{"trailing at", task.ID, `{"assigned_to":"a@"}`, 400, "VALIDATION_ERROR"},
		{"unknown task", "missing", `{"assigned_to":"a@b.co"}`, 404, "NOT_FOUND"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := e.do("POST", "/api/v1/tasks/"+tt.id+"/assign", tt.body)
			if w.Code != tt.wantCode {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.wantCode, w.Body)
			}
			er := decodeErr(t, w)
			if er.Error.Code != tt.wantErr || er.Error.Message == "" {
				t.Errorf("error = %+v", er.Error)
			}
		})
	}
	if len(e.n.calls) != 0 {
		t.Errorf("notifier must not be called on errors: %v", e.n.calls)
	}
	stored, _ := e.svc.GetTask(task.ID)
	if stored.AssignedTo != nil {
		t.Errorf("task modified by failed assignment: %v", *stored.AssignedTo)
	}
}

// Regression: GET /api/v1/tasks pagination over a stable ordering.
func TestListTasks_PaginationRegression(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		e.create(fmt.Sprintf("t%d", i), nil, "")
	}
	const total = 7 // two sample tasks + five

	w := e.do("GET", "/api/v1/tasks?page_size=3", "")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	first := decodeList(t, w)
	if len(first.Tasks) != 3 || first.NextPageToken != "3" || first.TotalCount != total {
		t.Fatalf("first page: len=%d next=%q total=%d", len(first.Tasks), first.NextPageToken, first.TotalCount)
	}

	for rep := 0; rep < 5; rep++ {
		seen := map[string]bool{}
		token := ""
		count := 0
		for i := 0; i < 10; i++ {
			path := "/api/v1/tasks?page_size=3"
			if token != "" {
				path += "&page_token=" + token
			}
			resp := decodeList(t, e.do("GET", path, ""))
			for _, task := range resp.Tasks {
				if seen[task.ID] {
					t.Fatalf("duplicate %s", task.ID)
				}
				seen[task.ID] = true
				count++
			}
			if resp.NextPageToken == "" {
				break
			}
			token = resp.NextPageToken
		}
		if count != total {
			t.Fatalf("walked %d, want %d", count, total)
		}
	}

	resp := decodeList(t, e.do("GET", "/api/v1/tasks?page_size=3&page_token=6", ""))
	if len(resp.Tasks) != 1 || resp.NextPageToken != "" {
		t.Errorf("last page: len=%d next=%q", len(resp.Tasks), resp.NextPageToken)
	}
	resp = decodeList(t, e.do("GET", "/api/v1/tasks?page_size=3&page_token=99", ""))
	if len(resp.Tasks) != 0 || resp.NextPageToken != "" || resp.TotalCount != total {
		t.Errorf("out of range: %+v", resp)
	}
	if w := e.do("GET", "/api/v1/tasks?page_size=0", ""); w.Code != 400 {
		t.Errorf("page_size=0 status %d, want 400", w.Code)
	}
	if w := e.do("GET", "/api/v1/tasks?page_size=101", ""); w.Code != 400 {
		t.Errorf("page_size=101 status %d, want 400", w.Code)
	}
}
