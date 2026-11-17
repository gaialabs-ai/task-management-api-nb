package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
)

// emptyEnv builds a router over a service with the sample data removed.
func emptyEnv(t *testing.T) (http.Handler, *services.TaskService) {
	t.Helper()
	r, svc, _ := setup(t)
	resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range resp.Tasks {
		if err := svc.DeleteTask(task.ID); err != nil {
			t.Fatal(err)
		}
	}
	return r, svc
}

func decode(t *testing.T, body []byte, v interface{}) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("invalid JSON %q: %v", body, err)
	}
}

func assertError(t *testing.T, body []byte, wantCode string) {
	t.Helper()
	var e models.ErrorResponse
	decode(t, body, &e)
	if e.Error.Code != wantCode || e.Error.Message == "" {
		t.Errorf("error = %+v, want code %s with a message", e.Error, wantCode)
	}
}

func TestHealthCheck(t *testing.T) {
	r, _, _ := setup(t)
	w := do(r, http.MethodGet, "/health", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]interface{}
	decode(t, w.Body.Bytes(), &body)
	if body["status"] != "healthy" || body["service"] != "task-api-go" || body["version"] == "" {
		t.Errorf("unexpected body: %s", w.Body)
	}
	if _, err := time.Parse(time.RFC3339, body["timestamp"].(string)); err != nil {
		t.Errorf("timestamp: %v", err)
	}
	proc, ok := body["process"].(map[string]interface{})
	if !ok {
		t.Fatalf("process missing: %s", w.Body)
	}
	for _, k := range []string{"owner", "hostname", "pid", "uptime", "go_version"} {
		if _, ok := proc[k]; !ok {
			t.Errorf("process.%s missing", k)
		}
	}
}

func TestListTasksRoute(t *testing.T) {
	r, svc := emptyEnv(t)
	for _, title := range []string{"one", "two", "three"} {
		create(t, svc, title, nil)
	}

	t.Run("lists all", func(t *testing.T) {
		w := do(r, http.MethodGet, "/api/v1/tasks", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		var resp models.ListTasksResponse
		decode(t, w.Body.Bytes(), &resp)
		if resp.TotalCount != 3 || len(resp.Tasks) != 3 || resp.NextPageToken != "" {
			t.Errorf("resp = %s", w.Body)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		w := do(r, http.MethodGet, "/api/v1/tasks?page_size=2", "")
		var resp models.ListTasksResponse
		decode(t, w.Body.Bytes(), &resp)
		if len(resp.Tasks) != 2 || resp.NextPageToken != "2" || resp.TotalCount != 3 {
			t.Fatalf("page 1 = %s", w.Body)
		}
		w = do(r, http.MethodGet, "/api/v1/tasks?page_size=2&page_token=2", "")
		resp = models.ListTasksResponse{}
		decode(t, w.Body.Bytes(), &resp)
		if len(resp.Tasks) != 1 || resp.NextPageToken != "" {
			t.Errorf("page 2 = %s", w.Body)
		}
	})

	t.Run("filters and sorting", func(t *testing.T) {
		w := do(r, http.MethodGet, "/api/v1/tasks?status=completed", "")
		var resp models.ListTasksResponse
		decode(t, w.Body.Bytes(), &resp)
		if w.Code != http.StatusOK || resp.TotalCount != 0 {
			t.Errorf("completed filter: %d %s", w.Code, w.Body)
		}
		w = do(r, http.MethodGet, "/api/v1/tasks?status=pending", "")
		resp = models.ListTasksResponse{}
		decode(t, w.Body.Bytes(), &resp)
		if w.Code != http.StatusOK || resp.TotalCount != 3 {
			t.Errorf("pending filter: %d %s", w.Code, w.Body)
		}
	})

	t.Run("invalid page_size", func(t *testing.T) {
		for _, q := range []string{"page_size=0", "page_size=101", "page_size=-1", "page_size=abc"} {
			w := do(r, http.MethodGet, "/api/v1/tasks?"+q, "")
			if w.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d", q, w.Code)
				continue
			}
			assertError(t, w.Body.Bytes(), "VALIDATION_ERROR")
		}
	})
}

func TestListTasksRouteQueryBinding(t *testing.T) {
	r, svc := emptyEnv(t)
	d1 := time.Now().Add(24 * time.Hour)
	d2 := time.Now().Add(48 * time.Hour)
	d3 := time.Now().Add(72 * time.Hour)
	alice, bob := "alice@example.com", "bob@example.com"
	for _, req := range []models.CreateTaskRequest{
		{Title: "a", Tags: []string{"x", "y"}, AssignedTo: &alice, DueDate: &d2},
		{Title: "b", Tags: []string{"x"}, AssignedTo: &bob, DueDate: &d3},
		{Title: "c", Tags: []string{"y"}, DueDate: &d1},
	} {
		if _, err := svc.CreateTask(req); err != nil {
			t.Fatal(err)
		}
	}
	titles := func(q string) []string {
		w := do(r, http.MethodGet, "/api/v1/tasks?"+q, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d %s", q, w.Code, w.Body)
		}
		var resp models.ListTasksResponse
		decode(t, w.Body.Bytes(), &resp)
		out := make([]string, len(resp.Tasks))
		for i, task := range resp.Tasks {
			out[i] = task.Title
		}
		return out
	}
	tests := []struct {
		query string
		want  string
	}{
		{"assigned_to=alice@example.com", "a"},
		{"tags=x", "a,b"},
		{"tags=x,y", "a"},
		{"tags=x&assigned_to=bob@example.com", "b"},
		{"sort_order=due_date_asc", "c,a,b"},
		{"sort_order=due_date_desc", "b,a,c"},
	}
	for _, tt := range tests {
		got := titles(tt.query)
		if tt.query == "tags=x" { // order unspecified without sort_order
			sort.Strings(got)
		}
		if strings.Join(got, ",") != tt.want {
			t.Errorf("%s: got %v, want %s", tt.query, got, tt.want)
		}
	}
}

func TestSearchRoute(t *testing.T) {
	r, svc := emptyEnv(t)
	create(t, svc, "Deploy the server", nil)
	create(t, svc, "Write docs", nil)

	w := do(r, http.MethodGet, "/api/v1/tasks/search?q=deploy", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (route shadowed by /:id?) %s", w.Code, w.Body)
	}
	var resp models.SearchTasksResponse
	decode(t, w.Body.Bytes(), &resp)
	if resp.Query != "deploy" || resp.TotalCount != 1 || len(resp.Tasks) != 1 || resp.Tasks[0].Title != "Deploy the server" {
		t.Errorf("resp = %s", w.Body)
	}

	create(t, svc, "Deploy docs", nil)
	w = do(r, http.MethodGet, "/api/v1/tasks/search?q=deploy&page_size=1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("page_size: status = %d", w.Code)
	}
	resp = models.SearchTasksResponse{}
	decode(t, w.Body.Bytes(), &resp)
	if len(resp.Tasks) != 1 || resp.TotalCount != 2 {
		t.Errorf("page_size=1: want 1 task of total 2, got %s", w.Body)
	}

	for _, p := range []string{"/api/v1/tasks/search", "/api/v1/tasks/search?q=", "/api/v1/tasks/search?q=x&page_size=0", "/api/v1/tasks/search?q=x&page_size=101"} {
		w := do(r, http.MethodGet, p, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", p, w.Code)
			continue
		}
		assertError(t, w.Body.Bytes(), "VALIDATION_ERROR")
	}
}

func TestStatsRoute(t *testing.T) {
	r, svc := emptyEnv(t)
	a := create(t, svc, "a", nil)
	create(t, svc, "b", nil)
	if _, err := svc.UpdateTaskStatus(a.ID, models.TaskStatusCompleted); err != nil {
		t.Fatal(err)
	}

	w := do(r, http.MethodGet, "/api/v1/tasks/stats", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (route shadowed by /:id?) %s", w.Code, w.Body)
	}
	var stats models.TaskStats
	decode(t, w.Body.Bytes(), &stats)
	if stats.Total != 2 || stats.ByStatus[models.TaskStatusCompleted] != 1 || stats.ByStatus[models.TaskStatusPending] != 1 ||
		stats.ByPriority[models.TaskPriorityMedium] != 2 || len(stats.ByStatus) != 5 || len(stats.ByPriority) != 4 {
		t.Errorf("stats = %s", w.Body)
	}
}

func TestGetTaskRoute(t *testing.T) {
	r, svc := emptyEnv(t)
	task := create(t, svc, "findme", nil)

	w := do(r, http.MethodGet, "/api/v1/tasks/"+task.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got models.Task
	decode(t, w.Body.Bytes(), &got)
	if got.ID != task.ID || got.Title != "findme" {
		t.Errorf("got = %+v", got)
	}

	w = do(r, http.MethodGet, "/api/v1/tasks/does-not-exist", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
	assertError(t, w.Body.Bytes(), "NOT_FOUND")
}

func TestCreateTaskRoute(t *testing.T) {
	r, _ := emptyEnv(t)

	t.Run("success with defaults", func(t *testing.T) {
		w := do(r, http.MethodPost, "/api/v1/tasks", `{"title":"new task"}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d %s", w.Code, w.Body)
		}
		var task models.Task
		decode(t, w.Body.Bytes(), &task)
		if task.ID == "" || task.Status != models.TaskStatusPending || task.Priority != models.TaskPriorityMedium {
			t.Errorf("task = %+v", task)
		}
		if loc := w.Header().Get("Location"); loc != "/api/v1/tasks/"+task.ID {
			t.Errorf("Location = %q", loc)
		}
		if got := do(r, http.MethodGet, "/api/v1/tasks/"+task.ID, ""); got.Code != http.StatusOK {
			t.Errorf("created task not retrievable: %d", got.Code)
		}
	})

	t.Run("success with all fields", func(t *testing.T) {
		body := `{"title":"full","description":"d","priority":"critical","tags":["a","b"],"assigned_to":"x@y.com","due_date":"2030-01-02T03:04:05Z"}`
		w := do(r, http.MethodPost, "/api/v1/tasks", body)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d %s", w.Code, w.Body)
		}
		var task models.Task
		decode(t, w.Body.Bytes(), &task)
		if task.Priority != models.TaskPriorityCritical || len(task.Tags) != 2 || task.AssignedTo == nil ||
			*task.AssignedTo != "x@y.com" || task.DueDate == nil || task.Description != "d" {
			t.Errorf("task = %+v", task)
		}
	})

	t.Run("validation failures", func(t *testing.T) {
		tests := []struct{ name, body string }{
			{"missing title", `{"description":"x"}`},
			{"empty title", `{"title":""}`},
			{"title too long", `{"title":"` + strings.Repeat("a", 201) + `"}`},
			{"malformed json", `{`},
			{"invalid priority", `{"title":"x","priority":"urgent"}`},
			{"bad due date", `{"title":"x","due_date":"tomorrow"}`},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				w := do(r, http.MethodPost, "/api/v1/tasks", tt.body)
				if w.Code != http.StatusBadRequest {
					t.Fatalf("status = %d %s", w.Code, w.Body)
				}
				assertError(t, w.Body.Bytes(), "VALIDATION_ERROR")
			})
		}
	})
}

func TestUpdateTaskRoute(t *testing.T) {
	r, svc := emptyEnv(t)
	task := create(t, svc, "orig", nil)
	path := "/api/v1/tasks/" + task.ID

	t.Run("success", func(t *testing.T) {
		w := do(r, http.MethodPut, path, `{"title":"changed","status":"in_progress","priority":"high","tags":["z"]}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d %s", w.Code, w.Body)
		}
		var got models.Task
		decode(t, w.Body.Bytes(), &got)
		if got.Title != "changed" || got.Status != models.TaskStatusInProgress || got.Priority != models.TaskPriorityHigh || len(got.Tags) != 1 {
			t.Errorf("got = %+v", got)
		}
	})

	t.Run("completing sets completed_at", func(t *testing.T) {
		w := do(r, http.MethodPut, path, `{"status":"completed"}`)
		var got models.Task
		decode(t, w.Body.Bytes(), &got)
		if w.Code != http.StatusOK || got.CompletedAt == nil {
			t.Errorf("status=%d got=%+v", w.Code, got)
		}
	})

	t.Run("invalid values", func(t *testing.T) {
		for _, body := range []string{`{"status":"bogus"}`, `{"priority":"urgent"}`} {
			w := do(r, http.MethodPut, path, body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d", body, w.Code)
				continue
			}
			assertError(t, w.Body.Bytes(), "VALIDATION_ERROR")
		}
	})

	t.Run("invalid status after title keeps earlier mutation", func(t *testing.T) {
		// UpdateTask is not atomic; flip this if it is made atomic.
		w := do(r, http.MethodPut, path, `{"title":"partial","status":"bogus"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d %s", w.Code, w.Body)
		}
		assertError(t, w.Body.Bytes(), "VALIDATION_ERROR")
		w = do(r, http.MethodGet, path, "")
		var got models.Task
		decode(t, w.Body.Bytes(), &got)
		if got.Title != "partial" || got.Status == "bogus" {
			t.Errorf("got = %+v", got)
		}
	})

	t.Run("binding failures", func(t *testing.T) {
		for _, body := range []string{`{`, `{"title":"` + strings.Repeat("a", 201) + `"}`} {
			w := do(r, http.MethodPut, path, body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("%q: status = %d", body, w.Code)
				continue
			}
			assertError(t, w.Body.Bytes(), "VALIDATION_ERROR")
		}
	})

	t.Run("not found", func(t *testing.T) {
		w := do(r, http.MethodPut, "/api/v1/tasks/nope", `{"title":"x"}`)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d", w.Code)
		}
		assertError(t, w.Body.Bytes(), "NOT_FOUND")
	})
}

func TestUpdateTaskStatusRoute(t *testing.T) {
	r, svc := emptyEnv(t)
	task := create(t, svc, "x", nil)
	path := "/api/v1/tasks/" + task.ID + "/status"

	w := do(r, http.MethodPatch, path, `{"status":"on_hold"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	var got models.Task
	decode(t, w.Body.Bytes(), &got)
	if got.Status != models.TaskStatusOnHold {
		t.Errorf("got = %+v", got)
	}

	w = do(r, http.MethodPatch, path, `{"status":"completed"}`)
	got = models.Task{}
	decode(t, w.Body.Bytes(), &got)
	if got.CompletedAt == nil {
		t.Errorf("completed_at missing: %s", w.Body)
	}

	for _, body := range []string{`{"status":"bogus"}`, `{}`, `{`} {
		w := do(r, http.MethodPatch, path, body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", body, w.Code)
			continue
		}
		assertError(t, w.Body.Bytes(), "VALIDATION_ERROR")
	}

	w = do(r, http.MethodPatch, "/api/v1/tasks/nope/status", `{"status":"pending"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("not found: status = %d", w.Code)
	}
	assertError(t, w.Body.Bytes(), "NOT_FOUND")
}

func TestDeleteTaskRoute(t *testing.T) {
	r, svc := emptyEnv(t)
	task := create(t, svc, "x", nil)

	w := do(r, http.MethodDelete, "/api/v1/tasks/"+task.ID, "")
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("status = %d body=%q", w.Code, w.Body)
	}
	if got := do(r, http.MethodGet, "/api/v1/tasks/"+task.ID, ""); got.Code != http.StatusNotFound {
		t.Errorf("task still present: %d", got.Code)
	}
	w = do(r, http.MethodDelete, "/api/v1/tasks/"+task.ID, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("second delete: status = %d", w.Code)
	}
	assertError(t, w.Body.Bytes(), "NOT_FOUND")
}

func TestCommentRoutes(t *testing.T) {
	r, svc := emptyEnv(t)
	task := create(t, svc, "x", nil)
	base := "/api/v1/tasks/" + task.ID + "/comments"

	// empty list
	w := do(r, http.MethodGet, base, "")
	var list models.ListCommentsResponse
	decode(t, w.Body.Bytes(), &list)
	if w.Code != http.StatusOK || list.Total != 0 || list.Comments == nil {
		t.Fatalf("empty list: %d %s", w.Code, w.Body)
	}

	// add
	w = do(r, http.MethodPost, base, `{"author":"alice","body":"hello"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("add: status = %d %s", w.Code, w.Body)
	}
	var c models.Comment
	decode(t, w.Body.Bytes(), &c)
	if c.ID == "" || c.TaskID != task.ID || c.Author != "alice" || c.Body != "hello" {
		t.Errorf("comment = %+v", c)
	}

	// list
	w = do(r, http.MethodGet, base, "")
	list = models.ListCommentsResponse{}
	decode(t, w.Body.Bytes(), &list)
	if list.Total != 1 || list.Comments[0].ID != c.ID {
		t.Errorf("list = %s", w.Body)
	}

	// validation
	for _, body := range []string{`{"body":"x"}`, `{"author":"x"}`, `{"author":"","body":"x"}`,
		`{"author":"` + strings.Repeat("a", 101) + `","body":"x"}`,
		`{"author":"a","body":"` + strings.Repeat("b", 5001) + `"}`, `{`} {
		w := do(r, http.MethodPost, base, body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%.40s: status = %d", body, w.Code)
			continue
		}
		assertError(t, w.Body.Bytes(), "VALIDATION_ERROR")
	}

	// delete
	w = do(r, http.MethodDelete, base+"/"+c.ID, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d", w.Code)
	}
	w = do(r, http.MethodDelete, base+"/"+c.ID, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete again: status = %d", w.Code)
	}
	assertError(t, w.Body.Bytes(), "NOT_FOUND")
}

func TestCommentRoutesUnknownTask(t *testing.T) {
	r, _ := emptyEnv(t)
	tests := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v1/tasks/nope/comments", ""},
		{http.MethodPost, "/api/v1/tasks/nope/comments", `{"author":"a","body":"b"}`},
		{http.MethodDelete, "/api/v1/tasks/nope/comments/c1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			w := do(r, tt.method, tt.path, tt.body)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d", w.Code)
			}
			assertError(t, w.Body.Bytes(), "NOT_FOUND")
		})
	}
}

func TestDeleteUnknownCommentOnKnownTask(t *testing.T) {
	r, svc := emptyEnv(t)
	task := create(t, svc, "x", nil)
	w := do(r, http.MethodDelete, "/api/v1/tasks/"+task.ID+"/comments/nope", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
	assertError(t, w.Body.Bytes(), "NOT_FOUND")
}

func TestStaticRoutesTakePrecedenceOverID(t *testing.T) {
	r, svc := emptyEnv(t)
	create(t, svc, "findable", nil)
	// If /:id captured these, the body would be a NOT_FOUND error shape.
	checks := map[string]string{
		"/api/v1/tasks/search?q=findable": `"query":"findable"`,
		"/api/v1/tasks/stats":             `"by_status"`,
		"/api/v1/tasks/overdue":           `"total_count"`,
	}
	for path, marker := range checks {
		w := do(r, http.MethodGet, path, "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), marker) {
			t.Errorf("%s: status=%d body=%s", path, w.Code, w.Body)
		}
	}
}

func TestUnknownRoutesAndMethods(t *testing.T) {
	r, _, _ := setup(t)
	for _, tt := range []struct{ method, path string }{
		{http.MethodGet, "/nope"},
		{http.MethodGet, "/api/v1/unknown"},
		{http.MethodPost, "/health"},
		{http.MethodPut, "/api/v1/tasks"},
		{http.MethodDelete, "/api/v1/tasks"},
	} {
		if w := do(r, tt.method, tt.path, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404", tt.method, tt.path, w.Code)
		}
	}
}

func TestNewHandlerWiresServices(t *testing.T) {
	svc := services.NewTaskService()
	h := NewHandler(svc)
	if h.taskService != svc || h.commentService == nil || h.startedAt.IsZero() {
		t.Errorf("handler not fully wired: %+v", h)
	}
}
