package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

const (
	// waitTimeout bounds how long a test waits for the asynchronous notification.
	waitTimeout = 2 * time.Second
	// quietPeriod is the bounded window used only to assert that nothing was sent.
	quietPeriod = 100 * time.Millisecond
)

// notifyCall records one TaskAssigned invocation.
type notifyCall struct {
	assignee string
	title    string
}

// fakeNotifier is a assignmentNotifier that reports calls on a buffered channel.
type fakeNotifier struct {
	enabled bool
	err     error
	calls   chan notifyCall
	count   int32
}

func newFakeNotifier(enabled bool, err error) *fakeNotifier {
	return &fakeNotifier{enabled: enabled, err: err, calls: make(chan notifyCall, 16)}
}

func (f *fakeNotifier) Enabled() bool { return f.enabled }

func (f *fakeNotifier) TaskAssigned(_ context.Context, assignee, title string) error {
	atomic.AddInt32(&f.count, 1)
	f.calls <- notifyCall{assignee: assignee, title: title}
	return f.err
}

// waitForCall blocks until a notification arrives or the timeout elapses.
func (f *fakeNotifier) waitForCall(t *testing.T) notifyCall {
	t.Helper()
	select {
	case c := <-f.calls:
		return c
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for notifier call")
		return notifyCall{}
	}
}

// expectNoCall asserts that no notification is delivered within the quiet period.
func (f *fakeNotifier) expectNoCall(t *testing.T) {
	t.Helper()
	select {
	case c := <-f.calls:
		t.Fatalf("unexpected notifier call: %+v", c)
	case <-time.After(quietPeriod):
	}
	if n := atomic.LoadInt32(&f.count); n != 0 {
		t.Fatalf("notifier call count = %d, want 0", n)
	}
}

// testEnv bundles a router, its service and its fake notifier.
type testEnv struct {
	router   *gin.Engine
	svc      *services.TaskService
	notifier *fakeNotifier
}

func newTestEnv(t *testing.T, notifier *fakeNotifier) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService()
	router := gin.New()
	NewHandlerWithNotifier(svc, notifier).RegisterRoutes(router)
	return &testEnv{router: router, svc: svc, notifier: notifier}
}

func (e *testEnv) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// addTask creates a task through the service. due may be nil.
func (e *testEnv) addTask(t *testing.T, title string, due *time.Time, status models.TaskStatus) *models.Task {
	t.Helper()
	task, err := e.svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due, Tags: []string{"x"}})
	if err != nil {
		t.Fatalf("CreateTask(%q): %v", title, err)
	}
	if status != "" && status != models.TaskStatusPending {
		if _, err := e.svc.UpdateTaskStatus(task.ID, status); err != nil {
			t.Fatalf("UpdateTaskStatus(%q): %v", title, err)
		}
	}
	return task
}

// ago returns a due date well in the past relative to the real clock.
func ago(d time.Duration) *time.Time {
	v := time.Now().Add(-d)
	return &v
}

// fromNow returns a due date well in the future relative to the real clock.
func fromNow(d time.Duration) *time.Time {
	v := time.Now().Add(d)
	return &v
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) models.ErrorDetail {
	t.Helper()
	var resp models.ErrorResponse
	decode(t, rec, &resp)
	return resp.Error
}

func titles(tasks []models.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.Title)
	}
	return out
}

func sameStrings(a, b []string) bool {
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

// ---- GET /api/v1/tasks/overdue ----

func TestOverdue_ResponseShapeAndFiltering(t *testing.T) {
	env := newTestEnv(t, newFakeNotifier(false, nil))
	env.addTask(t, "late pending", ago(48*time.Hour), models.TaskStatusPending)
	env.addTask(t, "late in progress", ago(24*time.Hour), models.TaskStatusInProgress)
	env.addTask(t, "late on hold", ago(12*time.Hour), models.TaskStatusOnHold)
	env.addTask(t, "late completed", ago(24*time.Hour), models.TaskStatusCompleted)
	env.addTask(t, "late cancelled", ago(24*time.Hour), models.TaskStatusCancelled)
	env.addTask(t, "future", fromNow(48*time.Hour), models.TaskStatusPending)
	env.addTask(t, "no due date", nil, models.TaskStatusPending)

	rec := env.do(t, http.MethodGet, "/api/v1/tasks/overdue", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	// Raw JSON keys of the ListTasksResponse envelope.
	var raw map[string]json.RawMessage
	decode(t, rec, &raw)
	for _, key := range []string{"tasks", "total_count"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("response missing key %q: %s", key, rec.Body.String())
		}
	}
	if _, ok := raw["next_page_token"]; ok {
		t.Errorf("next_page_token should be omitted on a single page: %s", rec.Body.String())
	}

	var resp models.ListTasksResponse
	decode(t, rec, &resp)
	want := []string{"late pending", "late in progress", "late on hold"}
	if got := titles(resp.Tasks); !sameStrings(got, want) {
		t.Errorf("tasks = %v, want %v", got, want)
	}
	if resp.TotalCount != 3 {
		t.Errorf("total_count = %d, want 3", resp.TotalCount)
	}
	for _, task := range resp.Tasks {
		if task.DueDate == nil || !task.DueDate.Before(time.Now()) {
			t.Errorf("task %q has due date %v, want past", task.Title, task.DueDate)
		}
		if task.Status == models.TaskStatusCompleted || task.Status == models.TaskStatusCancelled {
			t.Errorf("task %q has excluded status %s", task.Title, task.Status)
		}
	}
}

func TestOverdue_EmptyWhenNothingOverdue(t *testing.T) {
	env := newTestEnv(t, newFakeNotifier(false, nil))
	env.addTask(t, "future", fromNow(time.Hour), models.TaskStatusPending)

	rec := env.do(t, http.MethodGet, "/api/v1/tasks/overdue", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp models.ListTasksResponse
	decode(t, rec, &resp)
	if resp.TotalCount != 0 || len(resp.Tasks) != 0 {
		t.Errorf("got total=%d tasks=%d, want none", resp.TotalCount, len(resp.Tasks))
	}
}

func TestOverdue_OrderedByDueDateAscending(t *testing.T) {
	env := newTestEnv(t, newFakeNotifier(false, nil))
	env.addTask(t, "mid", ago(24*time.Hour), models.TaskStatusPending)
	env.addTask(t, "newest", ago(time.Hour), models.TaskStatusPending)
	env.addTask(t, "oldest", ago(240*time.Hour), models.TaskStatusPending)

	rec := env.do(t, http.MethodGet, "/api/v1/tasks/overdue", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp models.ListTasksResponse
	decode(t, rec, &resp)
	want := []string{"oldest", "mid", "newest"}
	if got := titles(resp.Tasks); !sameStrings(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestOverdue_TieBreakIsDeterministic(t *testing.T) {
	env := newTestEnv(t, newFakeNotifier(false, nil))
	due := ago(24 * time.Hour)
	for i := 0; i < 6; i++ {
		env.addTask(t, fmt.Sprintf("same-%d", i), due, models.TaskStatusPending)
	}

	var first []string
	for run := 0; run < 10; run++ {
		rec := env.do(t, http.MethodGet, "/api/v1/tasks/overdue", "")
		var resp models.ListTasksResponse
		decode(t, rec, &resp)
		ids := make([]string, 0, len(resp.Tasks))
		for _, task := range resp.Tasks {
			ids = append(ids, task.ID)
		}
		for i := 1; i < len(ids); i++ {
			if ids[i-1] >= ids[i] {
				t.Fatalf("ids not ascending on tie: %v", ids)
			}
		}
		if run == 0 {
			first = ids
		} else if !sameStrings(first, ids) {
			t.Fatalf("run %d order %v differs from first %v", run, ids, first)
		}
	}
}

func TestOverdue_Pagination(t *testing.T) {
	env := newTestEnv(t, newFakeNotifier(false, nil))
	for i := 0; i < 5; i++ {
		env.addTask(t, fmt.Sprintf("t%d", i), ago(time.Duration(100-i)*time.Hour), models.TaskStatusPending)
	}

	tests := []struct {
		name      string
		query     string
		wantTitle []string
		wantNext  string
	}{
		{"first page", "?page_size=2", []string{"t0", "t1"}, "2"},
		{"second page", "?page_size=2&page_token=2", []string{"t2", "t3"}, "4"},
		{"last page", "?page_size=2&page_token=4", []string{"t4"}, ""},
		{"token beyond range", "?page_size=2&page_token=50", nil, ""},
		{"negative token is offset 0", "?page_size=2&page_token=-3", []string{"t0", "t1"}, "2"},
		{"non-numeric token is offset 0", "?page_size=2&page_token=abc", []string{"t0", "t1"}, "2"},
		{"default page size", "", []string{"t0", "t1", "t2", "t3", "t4"}, ""},
		{"max page size", "?page_size=100", []string{"t0", "t1", "t2", "t3", "t4"}, ""},
		{"other filters are ignored", "?status=completed&sort_order=due_date_desc&tags=nope", []string{"t0", "t1", "t2", "t3", "t4"}, ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			rec := env.do(t, http.MethodGet, "/api/v1/tasks/overdue"+tt.query, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
			}
			var resp models.ListTasksResponse
			decode(t, rec, &resp)
			if got := titles(resp.Tasks); !sameStrings(got, tt.wantTitle) {
				t.Errorf("tasks = %v, want %v", got, tt.wantTitle)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("next_page_token = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
			if resp.TotalCount != 5 {
				t.Errorf("total_count = %d, want 5", resp.TotalCount)
			}
		})
	}
}

func TestOverdue_InvalidPageSize(t *testing.T) {
	env := newTestEnv(t, newFakeNotifier(false, nil))
	env.addTask(t, "late", ago(time.Hour), models.TaskStatusPending)

	tests := []struct {
		name  string
		query string
	}{
		{"zero", "?page_size=0"},
		{"negative", "?page_size=-1"},
		{"above max", "?page_size=101"},
		{"far above max", "?page_size=100000"},
		{"non-numeric", "?page_size=abc"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			rec := env.do(t, http.MethodGet, "/api/v1/tasks/overdue"+tt.query, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
			}
			if got := decodeError(t, rec); got.Code != "VALIDATION_ERROR" || got.Message == "" {
				t.Errorf("error = %+v, want VALIDATION_ERROR with a message", got)
			}
		})
	}
}

func TestOverdue_NotCapturedByIDRoute(t *testing.T) {
	env := newTestEnv(t, newFakeNotifier(false, nil))
	env.addTask(t, "late", ago(time.Hour), models.TaskStatusPending)

	rec := env.do(t, http.MethodGet, "/api/v1/tasks/overdue", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a 404 would mean /:id captured it); body %s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	decode(t, rec, &raw)
	if _, ok := raw["tasks"]; !ok {
		t.Errorf("body is not a list response: %s", rec.Body.String())
	}
	if _, ok := raw["error"]; ok {
		t.Errorf("body is an error envelope: %s", rec.Body.String())
	}
	if _, ok := raw["id"]; ok {
		t.Errorf("body looks like a single task: %s", rec.Body.String())
	}
}

// ---- POST /api/v1/tasks/:id/assign ----

func TestAssign_Success(t *testing.T) {
	notifier := newFakeNotifier(true, nil)
	env := newTestEnv(t, notifier)
	task := env.addTask(t, "Write docs", nil, models.TaskStatusPending)

	rec := env.do(t, http.MethodPost, "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"alice@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	var got models.Task
	decode(t, rec, &got)
	if got.ID != task.ID || got.Title != "Write docs" {
		t.Errorf("returned task = %+v, want id %s", got, task.ID)
	}
	if got.AssignedTo == nil || *got.AssignedTo != "alice@example.com" {
		t.Errorf("assigned_to = %v, want alice@example.com", got.AssignedTo)
	}
	if got.Status != models.TaskStatusPending {
		t.Errorf("status = %s, want unchanged pending", got.Status)
	}

	// The change must be visible through the existing GET endpoint.
	rec = env.do(t, http.MethodGet, "/api/v1/tasks/"+task.ID, "")
	var fetched models.Task
	decode(t, rec, &fetched)
	if fetched.AssignedTo == nil || *fetched.AssignedTo != "alice@example.com" {
		t.Errorf("persisted assigned_to = %v", fetched.AssignedTo)
	}

	notifier.waitForCall(t) // drain so the goroutine finishes inside the test
}

func TestAssign_BadRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"empty object", `{}`},
		{"missing field", `{"other":"alice@example.com"}`},
		{"empty string", `{"assigned_to":""}`},
		{"malformed json", `{"assigned_to": `},
		{"not json", `assigned_to=alice@example.com`},
		{"non-email", `{"assigned_to":"not-an-email"}`},
		{"missing domain", `{"assigned_to":"alice@"}`},
		{"missing local part", `{"assigned_to":"@example.com"}`},
		{"spaces", `{"assigned_to":"alice smith@example.com"}`},
		{"wrong type", `{"assigned_to":123}`},
		{"null", `{"assigned_to":null}`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			notifier := newFakeNotifier(true, nil)
			env := newTestEnv(t, notifier)
			task := env.addTask(t, "T", nil, models.TaskStatusPending)

			rec := env.do(t, http.MethodPost, "/api/v1/tasks/"+task.ID+"/assign", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
			}
			if got := decodeError(t, rec); got.Code != "VALIDATION_ERROR" || got.Message == "" {
				t.Errorf("error = %+v, want VALIDATION_ERROR with a message", got)
			}

			stored, _ := env.svc.GetTask(task.ID)
			if stored.AssignedTo != nil {
				t.Errorf("task was assigned to %q despite a 400", *stored.AssignedTo)
			}
			notifier.expectNoCall(t)
		})
	}
}

func TestAssign_NotFound(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"unknown uuid", "00000000-0000-0000-0000-000000000000"},
		{"arbitrary id", "does-not-exist"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			notifier := newFakeNotifier(true, nil)
			env := newTestEnv(t, notifier)

			rec := env.do(t, http.MethodPost, "/api/v1/tasks/"+tt.id+"/assign", `{"assigned_to":"alice@example.com"}`)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != "NOT_FOUND" {
				t.Errorf("code = %q, want NOT_FOUND", got.Code)
			}
			if !strings.Contains(got.Message, tt.id) {
				t.Errorf("message %q should mention id %q", got.Message, tt.id)
			}
			notifier.expectNoCall(t)
		})
	}
}

func TestAssign_AllowedForAnyStatus(t *testing.T) {
	statuses := []models.TaskStatus{
		models.TaskStatusPending, models.TaskStatusInProgress, models.TaskStatusCompleted,
		models.TaskStatusCancelled, models.TaskStatusOnHold,
	}
	for _, status := range statuses {
		status := status
		t.Run(string(status), func(t *testing.T) {
			notifier := newFakeNotifier(true, nil)
			env := newTestEnv(t, notifier)
			task := env.addTask(t, "T", nil, status)

			rec := env.do(t, http.MethodPost, "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"a@example.com"}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
			}
			notifier.waitForCall(t)
		})
	}
}

// ---- Notification behaviour ----

func TestAssign_NotifiesOnceWithAssigneeAndTitle(t *testing.T) {
	notifier := newFakeNotifier(true, nil)
	env := newTestEnv(t, notifier)
	task := env.addTask(t, "Ship the release", nil, models.TaskStatusPending)

	rec := env.do(t, http.MethodPost, "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"carol@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	call := notifier.waitForCall(t)
	if call.assignee != "carol@example.com" || call.title != "Ship the release" {
		t.Errorf("notifier called with %+v, want carol@example.com / Ship the release", call)
	}

	// Exactly once: nothing further arrives.
	select {
	case extra := <-notifier.calls:
		t.Errorf("unexpected second notifier call: %+v", extra)
	case <-time.After(quietPeriod):
	}
	if n := atomic.LoadInt32(&notifier.count); n != 1 {
		t.Errorf("notifier call count = %d, want 1", n)
	}
}

func TestAssign_ReassignNotifiesNewAssignee(t *testing.T) {
	notifier := newFakeNotifier(true, nil)
	env := newTestEnv(t, notifier)
	task := env.addTask(t, "Reassign me", nil, models.TaskStatusPending)

	for _, email := range []string{"one@example.com", "two@example.com"} {
		rec := env.do(t, http.MethodPost, "/api/v1/tasks/"+task.ID+"/assign", fmt.Sprintf(`{"assigned_to":%q}`, email))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		if call := notifier.waitForCall(t); call.assignee != email {
			t.Errorf("notified %q, want %q", call.assignee, email)
		}
	}
}

func TestAssign_DisabledNotifierStillSucceeds(t *testing.T) {
	notifier := newFakeNotifier(false, nil)
	env := newTestEnv(t, notifier)
	task := env.addTask(t, "No key", nil, models.TaskStatusPending)

	rec := env.do(t, http.MethodPost, "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"dave@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var got models.Task
	decode(t, rec, &got)
	if got.AssignedTo == nil || *got.AssignedTo != "dave@example.com" {
		t.Errorf("assigned_to = %v, want dave@example.com", got.AssignedTo)
	}
	notifier.expectNoCall(t)
}

func TestAssign_FailingNotifierStillSucceeds(t *testing.T) {
	notifier := newFakeNotifier(true, errors.New("sendgrid is down"))
	env := newTestEnv(t, notifier)
	task := env.addTask(t, "Flaky mail", nil, models.TaskStatusPending)

	rec := env.do(t, http.MethodPost, "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"erin@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var got models.Task
	decode(t, rec, &got)
	if got.AssignedTo == nil || *got.AssignedTo != "erin@example.com" {
		t.Errorf("assigned_to = %v, want erin@example.com", got.AssignedTo)
	}

	// The notifier was attempted (and failed) without affecting the response.
	if call := notifier.waitForCall(t); call.assignee != "erin@example.com" {
		t.Errorf("notified %q, want erin@example.com", call.assignee)
	}
}

// ---- Smoke tests for existing endpoints ----

func TestSmoke_ListTasks(t *testing.T) {
	env := newTestEnv(t, newFakeNotifier(false, nil))
	env.addTask(t, "extra", nil, models.TaskStatusPending)

	rec := env.do(t, http.MethodGet, "/api/v1/tasks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp models.ListTasksResponse
	decode(t, rec, &resp)
	// Two seeded sample tasks plus the one added here.
	if resp.TotalCount != 3 || len(resp.Tasks) != 3 {
		t.Errorf("total=%d len=%d, want 3", resp.TotalCount, len(resp.Tasks))
	}

	rec = env.do(t, http.MethodGet, "/api/v1/tasks?page_size=2", "")
	decode(t, rec, &resp)
	if len(resp.Tasks) != 2 || resp.NextPageToken != "2" {
		t.Errorf("len=%d next=%q, want 2 and \"2\"", len(resp.Tasks), resp.NextPageToken)
	}

	rec = env.do(t, http.MethodGet, "/api/v1/tasks?page_size=0", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("page_size=0 status = %d, want 400", rec.Code)
	}

	// A negative token must no longer panic the list endpoint.
	rec = env.do(t, http.MethodGet, "/api/v1/tasks?page_size=2&page_token=-5", "")
	if rec.Code != http.StatusOK {
		t.Errorf("negative token status = %d, want 200", rec.Code)
	}
}

func TestSmoke_GetTask(t *testing.T) {
	env := newTestEnv(t, newFakeNotifier(false, nil))
	task := env.addTask(t, "find me", nil, models.TaskStatusPending)

	rec := env.do(t, http.MethodGet, "/api/v1/tasks/"+task.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var got models.Task
	decode(t, rec, &got)
	if got.ID != task.ID || got.Title != "find me" {
		t.Errorf("got %+v, want id %s title %q", got, task.ID, "find me")
	}

	rec = env.do(t, http.MethodGet, "/api/v1/tasks/missing-id", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if e := decodeError(t, rec); e.Code != "NOT_FOUND" {
		t.Errorf("code = %q, want NOT_FOUND", e.Code)
	}
}

func TestSmoke_OtherStaticRoutesStillRegistered(t *testing.T) {
	env := newTestEnv(t, newFakeNotifier(false, nil))
	tests := []struct {
		path string
		want int
	}{
		{"/health", http.StatusOK},
		{"/api/v1/tasks/stats", http.StatusOK},
		{"/api/v1/tasks/search?q=go", http.StatusOK},
		{"/api/v1/tasks/search", http.StatusBadRequest},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.path, func(t *testing.T) {
			rec := env.do(t, http.MethodGet, tt.path, "")
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d; body %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}
