package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

var fixedNow = time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

type noteCall struct {
	id    string
	email string
}

type recNotifier struct{ calls []noteCall }

func (r *recNotifier) NotifyAssignment(task models.Task, assignedTo string) error {
	r.calls = append(r.calls, noteCall{task.ID, assignedTo})
	return nil
}

type env struct {
	router *gin.Engine
	svc    *services.TaskService
	note   *recNotifier
}

func newEnv(t *testing.T) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)
	n := &recNotifier{}
	svc := services.NewTaskService(
		services.WithClock(func() time.Time { return fixedNow }),
		services.WithNotifier(n),
	)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return &env{router: r, svc: svc, note: n}
}

func (e *env) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return v
}

func expectStatus(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, code, w.Body.String())
	}
}

func expectErr(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	expectStatus(t, w, status)
	er := decode[models.ErrorResponse](t, w)
	if er.Error.Code != code {
		t.Fatalf("error code = %q, want %q (%s)", er.Error.Code, code, w.Body.String())
	}
}

func (e *env) mk(t *testing.T, title string, due *time.Time, status models.TaskStatus) models.Task {
	t.Helper()
	task, err := e.svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		if _, err := e.svc.UpdateTaskStatus(task.ID, status); err != nil {
			t.Fatal(err)
		}
	}
	return *task
}

func past(h int) *time.Time { d := fixedNow.Add(-time.Duration(h) * time.Hour); return &d }

// ---- /tasks/overdue ----

func TestOverdue_OK(t *testing.T) {
	e := newEnv(t)
	e.mk(t, "old", past(48), "")
	e.mk(t, "older", past(72), "")
	e.mk(t, "done", past(10), models.TaskStatusCompleted)
	e.mk(t, "cancelled", past(10), models.TaskStatusCancelled)
	fut := fixedNow.Add(time.Hour)
	e.mk(t, "future", &fut, "")

	w := e.do("GET", "/api/v1/tasks/overdue", "")
	expectStatus(t, w, 200)
	resp := decode[models.ListTasksResponse](t, w)
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 {
		t.Fatalf("unexpected %s", w.Body.String())
	}
	if resp.Tasks[0].Title != "older" || resp.Tasks[1].Title != "old" {
		t.Errorf("order: %s, %s", resp.Tasks[0].Title, resp.Tasks[1].Title)
	}
	if resp.NextPageToken != "" {
		t.Errorf("token = %q", resp.NextPageToken)
	}
}

func TestOverdue_EmptyList(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/api/v1/tasks/overdue", "")
	expectStatus(t, w, 200)
	resp := decode[models.ListTasksResponse](t, w)
	if resp.TotalCount != 0 || len(resp.Tasks) != 0 {
		t.Fatalf("unexpected %s", w.Body.String())
	}
}

func TestOverdue_Pagination(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		e.mk(t, "t", past(100-i), "")
	}
	w := e.do("GET", "/api/v1/tasks/overdue?page_size=2", "")
	expectStatus(t, w, 200)
	r1 := decode[models.ListTasksResponse](t, w)
	if len(r1.Tasks) != 2 || r1.NextPageToken != "2" || r1.TotalCount != 5 {
		t.Fatalf("page1 %s", w.Body.String())
	}
	w = e.do("GET", "/api/v1/tasks/overdue?page_size=2&page_token=4", "")
	expectStatus(t, w, 200)
	r3 := decode[models.ListTasksResponse](t, w)
	if len(r3.Tasks) != 1 || r3.NextPageToken != "" || r3.TotalCount != 5 {
		t.Fatalf("page3 %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "next_page_token") {
		t.Errorf("empty token should be omitted")
	}
}

func TestOverdue_InvalidQuery(t *testing.T) {
	e := newEnv(t)
	for _, q := range []string{"page_size=0", "page_size=101", "page_size=-1", "page_size=abc"} {
		expectErr(t, e.do("GET", "/api/v1/tasks/overdue?"+q, ""), 400, "VALIDATION_ERROR")
	}
}

func TestOverdue_RoutePrecedenceOverID(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/api/v1/tasks/overdue", "")
	expectStatus(t, w, 200)
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["total_count"]; !ok {
		t.Errorf("expected list response, got %s", w.Body.String())
	}
	if _, ok := raw["error"]; ok {
		t.Errorf("got error response %s", w.Body.String())
	}
}

func TestGetTaskByID_StillWorksAlongsideOverdue(t *testing.T) {
	e := newEnv(t)
	task := e.mk(t, "x", nil, "")
	w := e.do("GET", "/api/v1/tasks/"+task.ID, "")
	expectStatus(t, w, 200)
	if got := decode[models.Task](t, w); got.ID != task.ID {
		t.Errorf("id = %s", got.ID)
	}
	expectErr(t, e.do("GET", "/api/v1/tasks/does-not-exist", ""), 404, "NOT_FOUND")
}

// ---- /tasks/:id/assign ----

func TestAssign_OK(t *testing.T) {
	e := newEnv(t)
	task := e.mk(t, "x", nil, "")
	w := e.do("POST", "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"eve@example.com"}`)
	expectStatus(t, w, 200)
	got := decode[models.Task](t, w)
	if got.ID != task.ID || got.AssignedTo == nil || *got.AssignedTo != "eve@example.com" {
		t.Fatalf("body %s", w.Body.String())
	}
	if !got.UpdatedAt.Equal(fixedNow) {
		t.Errorf("updated_at = %v", got.UpdatedAt)
	}
	if len(e.note.calls) != 1 || e.note.calls[0] != (noteCall{task.ID, "eve@example.com"}) {
		t.Errorf("notifier calls = %+v", e.note.calls)
	}
	g := decode[models.Task](t, e.do("GET", "/api/v1/tasks/"+task.ID, ""))
	if g.AssignedTo == nil || *g.AssignedTo != "eve@example.com" {
		t.Errorf("not persisted")
	}
}

func TestAssign_ValidationErrors(t *testing.T) {
	e := newEnv(t)
	task := e.mk(t, "x", nil, "")
	url := "/api/v1/tasks/" + task.ID + "/assign"
	bodies := map[string]string{
		"bad email":     `{"assigned_to":"nope"}`,
		"empty email":   `{"assigned_to":""}`,
		"missing field": `{}`,
		"bad json":      `{"assigned_to":`,
		"wrong type":    `{"assigned_to":123}`,
		"no body":       ``,
	}
	for name, b := range bodies {
		b := b
		t.Run(name, func(t *testing.T) {
			expectErr(t, e.do("POST", url, b), 400, "VALIDATION_ERROR")
		})
	}
	if len(e.note.calls) != 0 {
		t.Errorf("notifier must not be called on validation failure")
	}
	g := decode[models.Task](t, e.do("GET", "/api/v1/tasks/"+task.ID, ""))
	if g.AssignedTo != nil {
		t.Errorf("task assigned despite failures")
	}
}

func TestAssign_NotFound(t *testing.T) {
	e := newEnv(t)
	w := e.do("POST", "/api/v1/tasks/ghost/assign", `{"assigned_to":"a@example.com"}`)
	expectErr(t, w, 404, "NOT_FOUND")
	er := decode[models.ErrorResponse](t, w)
	if er.Error.Message != "task with ID ghost not found" {
		t.Errorf("message = %q", er.Error.Message)
	}
	if len(e.note.calls) != 0 {
		t.Errorf("notifier must not be called")
	}
}

func TestAssign_InvalidBodyOnMissingTaskIs400(t *testing.T) {
	e := newEnv(t)
	expectErr(t, e.do("POST", "/api/v1/tasks/ghost/assign", `{"assigned_to":"bad"}`), 400, "VALIDATION_ERROR")
}

func TestAssign_WrongMethod(t *testing.T) {
	e := newEnv(t)
	task := e.mk(t, "x", nil, "")
	w := e.do("GET", "/api/v1/tasks/"+task.ID+"/assign", "")
	if w.Code != 404 && w.Code != 405 {
		t.Errorf("status = %d", w.Code)
	}
}

// ---- regression on existing endpoints ----

func TestRegression_Health(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/health", "")
	expectStatus(t, w, 200)
	m := decode[map[string]any](t, w)
	if m["status"] != "healthy" || m["service"] != "task-api-go" {
		t.Errorf("body %s", w.Body.String())
	}
	if _, ok := m["process"].(map[string]any); !ok {
		t.Errorf("missing process info")
	}
}

func TestRegression_List(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/api/v1/tasks", "")
	expectStatus(t, w, 200)
	r := decode[models.ListTasksResponse](t, w)
	if r.TotalCount != 2 || len(r.Tasks) != 2 {
		t.Errorf("body %s", w.Body.String())
	}
	w = e.do("GET", "/api/v1/tasks?page_size=1", "")
	r = decode[models.ListTasksResponse](t, w)
	if len(r.Tasks) != 1 || r.NextPageToken != "1" || r.TotalCount != 2 {
		t.Errorf("paged %s", w.Body.String())
	}
	expectErr(t, e.do("GET", "/api/v1/tasks?page_size=500", ""), 400, "VALIDATION_ERROR")
	w = e.do("GET", "/api/v1/tasks?status=pending&tags=grpc", "")
	r = decode[models.ListTasksResponse](t, w)
	if r.TotalCount != 1 {
		t.Errorf("filtered %s", w.Body.String())
	}
	w = e.do("GET", "/api/v1/tasks?sort_order=priority_desc", "")
	r = decode[models.ListTasksResponse](t, w)
	if r.Tasks[0].Title != "Implement Go REST API" {
		t.Errorf("sort %s", w.Body.String())
	}
}

func TestRegression_CreateGetUpdatePatchDelete(t *testing.T) {
	e := newEnv(t)

	w := e.do("POST", "/api/v1/tasks", `{"title":"New","priority":"high","tags":["a"]}`)
	expectStatus(t, w, 201)
	task := decode[models.Task](t, w)
	if task.ID == "" || task.Status != models.TaskStatusPending || task.Priority != models.TaskPriorityHigh {
		t.Fatalf("created %s", w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/api/v1/tasks/"+task.ID {
		t.Errorf("Location = %q", loc)
	}
	expectErr(t, e.do("POST", "/api/v1/tasks", `{"description":"no title"}`), 400, "VALIDATION_ERROR")
	expectErr(t, e.do("POST", "/api/v1/tasks", `{"title":"x","priority":"bogus"}`), 400, "VALIDATION_ERROR")
	expectErr(t, e.do("POST", "/api/v1/tasks", `{bad`), 400, "VALIDATION_ERROR")

	expectStatus(t, e.do("GET", "/api/v1/tasks/"+task.ID, ""), 200)

	w = e.do("PUT", "/api/v1/tasks/"+task.ID, `{"title":"Renamed","status":"in_progress"}`)
	expectStatus(t, w, 200)
	up := decode[models.Task](t, w)
	if up.Title != "Renamed" || up.Status != models.TaskStatusInProgress {
		t.Errorf("updated %s", w.Body.String())
	}
	expectErr(t, e.do("PUT", "/api/v1/tasks/"+task.ID, `{"status":"bogus"}`), 400, "VALIDATION_ERROR")
	expectErr(t, e.do("PUT", "/api/v1/tasks/ghost", `{"title":"x"}`), 404, "NOT_FOUND")

	w = e.do("PATCH", "/api/v1/tasks/"+task.ID+"/status", `{"status":"completed"}`)
	expectStatus(t, w, 200)
	p := decode[models.Task](t, w)
	if p.Status != models.TaskStatusCompleted || p.CompletedAt == nil {
		t.Errorf("patched %s", w.Body.String())
	}
	expectErr(t, e.do("PATCH", "/api/v1/tasks/"+task.ID+"/status", `{"status":"bogus"}`), 400, "VALIDATION_ERROR")
	expectErr(t, e.do("PATCH", "/api/v1/tasks/"+task.ID+"/status", `{}`), 400, "VALIDATION_ERROR")
	expectErr(t, e.do("PATCH", "/api/v1/tasks/ghost/status", `{"status":"pending"}`), 404, "NOT_FOUND")

	expectStatus(t, e.do("DELETE", "/api/v1/tasks/"+task.ID, ""), 204)
	expectErr(t, e.do("GET", "/api/v1/tasks/"+task.ID, ""), 404, "NOT_FOUND")
	expectErr(t, e.do("DELETE", "/api/v1/tasks/"+task.ID, ""), 404, "NOT_FOUND")
}

func TestRegression_Search(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/api/v1/tasks/search?q=grpc", "")
	expectStatus(t, w, 200)
	r := decode[models.SearchTasksResponse](t, w)
	if r.TotalCount != 1 || r.Query != "grpc" || r.Tasks[0].Title != "Add gRPC support" {
		t.Errorf("body %s", w.Body.String())
	}
	expectErr(t, e.do("GET", "/api/v1/tasks/search", ""), 400, "VALIDATION_ERROR")
	w = e.do("GET", "/api/v1/tasks/search?q=zzzzzz", "")
	expectStatus(t, w, 200)
	if r := decode[models.SearchTasksResponse](t, w); r.TotalCount != 0 {
		t.Errorf("expected none")
	}
}

func TestRegression_Stats(t *testing.T) {
	e := newEnv(t)
	e.mk(t, "x", nil, models.TaskStatusCompleted)
	w := e.do("GET", "/api/v1/tasks/stats", "")
	expectStatus(t, w, 200)
	s := decode[models.TaskStats](t, w)
	if s.Total != 3 || s.ByStatus[models.TaskStatusPending] != 2 || s.ByStatus[models.TaskStatusCompleted] != 1 {
		t.Errorf("stats %s", w.Body.String())
	}
	if _, ok := s.ByStatus[models.TaskStatusOnHold]; !ok {
		t.Errorf("missing zero-initialized key")
	}
}

func TestRegression_Comments(t *testing.T) {
	e := newEnv(t)
	task := e.mk(t, "x", nil, "")
	base := "/api/v1/tasks/" + task.ID + "/comments"

	w := e.do("GET", base, "")
	expectStatus(t, w, 200)
	if l := decode[models.ListCommentsResponse](t, w); l.Total != 0 {
		t.Errorf("expected no comments")
	}

	w = e.do("POST", base, `{"author":"al","body":"hello"}`)
	expectStatus(t, w, 201)
	c := decode[models.Comment](t, w)
	if c.ID == "" || c.TaskID != task.ID || c.Body != "hello" {
		t.Errorf("comment %s", w.Body.String())
	}
	expectErr(t, e.do("POST", base, `{"author":"al"}`), 400, "VALIDATION_ERROR")
	expectErr(t, e.do("POST", "/api/v1/tasks/ghost/comments", `{"author":"al","body":"b"}`), 404, "NOT_FOUND")
	expectErr(t, e.do("GET", "/api/v1/tasks/ghost/comments", ""), 404, "NOT_FOUND")

	w = e.do("GET", base, "")
	if l := decode[models.ListCommentsResponse](t, w); l.Total != 1 {
		t.Errorf("expected one comment")
	}
	expectStatus(t, e.do("DELETE", base+"/"+c.ID, ""), 204)
	expectErr(t, e.do("DELETE", base+"/"+c.ID, ""), 404, "NOT_FOUND")
}
