package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/rest/server"
	"github.com/gin-gonic/gin"
)

type obj = map[string]any

type env struct {
	t      *testing.T
	router *gin.Engine
}

// newEnv builds a fresh router with a fresh service (2 seeded tasks).
func newEnv(t *testing.T) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	server.NewHandler(services.NewTaskService()).RegisterRoutes(router)
	return &env{t: t, router: router}
}

// do performs a request. body may be nil, a string (raw), or any JSON-marshalable value.
func (e *env) do(method, path string, body any) (*httptest.ResponseRecorder, obj) {
	e.t.Helper()
	var rdr *bytes.Reader
	switch b := body.(type) {
	case nil:
		rdr = bytes.NewReader(nil)
	case string:
		rdr = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	var parsed obj
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &parsed)
	}
	return w, parsed
}

func (e *env) expect(method, path string, body any, status int) obj {
	e.t.Helper()
	w, m := e.do(method, path, body)
	if w.Code != status {
		e.t.Fatalf("%s %s: status = %d, want %d; body=%s", method, path, w.Code, status, w.Body.String())
	}
	return m
}

func (e *env) expectError(method, path string, body any, status int, code string) {
	e.t.Helper()
	m := e.expect(method, path, body, status)
	errObj, ok := m["error"].(obj)
	if !ok {
		e.t.Fatalf("%s %s: no error object in %v", method, path, m)
	}
	if errObj["code"] != code {
		e.t.Fatalf("%s %s: error code = %v, want %s", method, path, errObj["code"], code)
	}
	if msg, _ := errObj["message"].(string); msg == "" {
		e.t.Fatalf("%s %s: error message empty", method, path)
	}
}

func (e *env) create(body obj) obj {
	e.t.Helper()
	return e.expect(http.MethodPost, "/api/v1/tasks", body, http.StatusCreated)
}

// clear deletes every task so tests control the full data set.
func (e *env) clear() {
	e.t.Helper()
	for _, tk := range e.list("") {
		e.expect(http.MethodDelete, "/api/v1/tasks/"+tk["id"].(string), nil, http.StatusNoContent)
	}
}

func (e *env) list(query string) []obj {
	e.t.Helper()
	path := "/api/v1/tasks"
	if query != "" {
		path += "?" + query
	}
	m := e.expect(http.MethodGet, path, nil, http.StatusOK)
	return tasksOf(m)
}

func tasksOf(m obj) []obj {
	raw, _ := m["tasks"].([]any)
	out := make([]obj, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(obj))
	}
	return out
}

func titles(ts []obj) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t["title"].(string))
	}
	return out
}

func assertTitles(t *testing.T, got []obj, want ...string) {
	t.Helper()
	g := titles(got)
	if strings.Join(g, "|") != strings.Join(want, "|") {
		t.Fatalf("titles = %v, want %v", g, want)
	}
}

// createOrdered creates tasks sequentially with a small sleep so created_at is strictly increasing.
func (e *env) createOrdered(bodies ...obj) []obj {
	var out []obj
	for _, b := range bodies {
		out = append(out, e.create(b))
		time.Sleep(3 * time.Millisecond)
	}
	return out
}

const missingID = "00000000-0000-0000-0000-000000000000"

func TestHealth(t *testing.T) {
	e := newEnv(t)
	m := e.expect(http.MethodGet, "/health", nil, http.StatusOK)
	if m["status"] != "healthy" || m["service"] != "task-api-go" || m["version"] != "1.1.0" {
		t.Fatalf("unexpected health: %v", m)
	}
	ts, _ := m["timestamp"].(string)
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Fatalf("timestamp not RFC3339: %q", ts)
	}
	p, ok := m["process"].(obj)
	if !ok {
		t.Fatalf("process missing: %v", m)
	}
	for _, k := range []string{"owner", "hostname", "pid", "uptime", "go_version"} {
		if _, ok := p[k]; !ok {
			t.Errorf("process.%s missing", k)
		}
	}
	if pid, _ := p["pid"].(float64); pid <= 0 {
		t.Errorf("pid = %v", p["pid"])
	}
	if s, _ := p["uptime"].(string); s == "" {
		t.Errorf("uptime empty")
	}
	if s, _ := p["go_version"].(string); !strings.HasPrefix(s, "go") {
		t.Errorf("go_version = %q", s)
	}
}

func TestCreateTask(t *testing.T) {
	e := newEnv(t)

	t.Run("defaults", func(t *testing.T) {
		e := newEnv(t)
		w, m := e.do(http.MethodPost, "/api/v1/tasks", obj{"title": "Hello"})
		if w.Code != http.StatusCreated {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		id, _ := m["id"].(string)
		if id == "" {
			t.Fatal("id empty")
		}
		if loc := w.Header().Get("Location"); loc != "/api/v1/tasks/"+id {
			t.Fatalf("Location = %q", loc)
		}
		if m["title"] != "Hello" || m["status"] != "pending" || m["priority"] != "medium" ||
			m["created_by"] != "system" || m["description"] != "" {
			t.Fatalf("unexpected defaults: %v", m)
		}
		if tags, ok := m["tags"].([]any); !ok || len(tags) != 0 {
			t.Fatalf("tags should be empty array, got %v", m["tags"])
		}
		for _, k := range []string{"assigned_to", "due_date", "completed_at"} {
			if _, ok := m[k]; ok {
				t.Errorf("%s should be omitted", k)
			}
		}
		for _, k := range []string{"created_at", "updated_at"} {
			s, _ := m[k].(string)
			if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
				t.Errorf("%s not RFC3339: %q", k, s)
			}
		}
		// Persisted
		got := e.expect(http.MethodGet, "/api/v1/tasks/"+id, nil, http.StatusOK)
		if got["title"] != "Hello" {
			t.Fatalf("not persisted: %v", got)
		}
	})

	t.Run("all fields", func(t *testing.T) {
		m := e.create(obj{"title": "Full", "description": "d", "priority": "critical",
			"tags": []string{"a", "b"}, "assigned_to": "alice", "due_date": "2030-01-02T03:04:05Z"})
		if m["priority"] != "critical" || m["description"] != "d" || m["assigned_to"] != "alice" {
			t.Fatalf("unexpected: %v", m)
		}
		if tags := m["tags"].([]any); len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
			t.Fatalf("tags: %v", m["tags"])
		}
		due, _ := time.Parse(time.RFC3339, m["due_date"].(string))
		if !due.Equal(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)) {
			t.Fatalf("due_date: %v", m["due_date"])
		}
	})

	t.Run("invalid priority", func(t *testing.T) {
		e.expectError(http.MethodPost, "/api/v1/tasks", obj{"title": "x", "priority": "urgent"}, 400, "VALIDATION_ERROR")
	})
	t.Run("bad json", func(t *testing.T) {
		e.expectError(http.MethodPost, "/api/v1/tasks", "{not json", 400, "VALIDATION_ERROR")
	})
	t.Run("missing title", func(t *testing.T) {
		e.expectError(http.MethodPost, "/api/v1/tasks", obj{"description": "x"}, 400, "VALIDATION_ERROR")
	})
	t.Run("title too long", func(t *testing.T) {
		e.expectError(http.MethodPost, "/api/v1/tasks", obj{"title": strings.Repeat("a", 201)}, 400, "VALIDATION_ERROR")
	})
	t.Run("title max length ok", func(t *testing.T) {
		e.create(obj{"title": strings.Repeat("a", 200)})
	})
	t.Run("bad due_date", func(t *testing.T) {
		e.expectError(http.MethodPost, "/api/v1/tasks", obj{"title": "x", "due_date": "not-a-date"}, 400, "VALIDATION_ERROR")
	})
	t.Run("invalid does not create", func(t *testing.T) {
		e := newEnv(t)
		e.do(http.MethodPost, "/api/v1/tasks", obj{"title": "x", "priority": "urgent"})
		m := e.expect(http.MethodGet, "/api/v1/tasks", nil, 200)
		if m["total_count"] != float64(2) {
			t.Fatalf("total_count = %v", m["total_count"])
		}
	})
}

func TestGetTask(t *testing.T) {
	e := newEnv(t)
	c := e.create(obj{"title": "G"})
	m := e.expect(http.MethodGet, "/api/v1/tasks/"+c["id"].(string), nil, 200)
	if m["id"] != c["id"] || m["title"] != "G" {
		t.Fatalf("unexpected: %v", m)
	}
	e.expectError(http.MethodGet, "/api/v1/tasks/"+missingID, nil, 404, "NOT_FOUND")
}

func TestUpdateTask(t *testing.T) {
	e := newEnv(t)
	c := e.create(obj{"title": "Orig", "description": "od", "priority": "low", "tags": []string{"x"}})
	id := c["id"].(string)
	path := "/api/v1/tasks/" + id

	t.Run("fields", func(t *testing.T) {
		m := e.expect(http.MethodPut, path, obj{"title": "New", "description": "nd", "priority": "high",
			"status": "in_progress", "tags": []string{"p", "q"}, "assigned_to": "bob",
			"due_date": "2031-05-06T07:08:09Z"}, 200)
		if m["id"] != id || m["title"] != "New" || m["description"] != "nd" || m["priority"] != "high" ||
			m["status"] != "in_progress" || m["assigned_to"] != "bob" {
			t.Fatalf("unexpected: %v", m)
		}
		if tags := m["tags"].([]any); len(tags) != 2 || tags[0] != "p" {
			t.Fatalf("tags: %v", m["tags"])
		}
		if _, ok := m["completed_at"]; ok {
			t.Fatal("completed_at must not be set for in_progress")
		}
		if m["created_at"] != c["created_at"] {
			t.Fatal("created_at changed")
		}
		created, _ := time.Parse(time.RFC3339Nano, m["created_at"].(string))
		updated, _ := time.Parse(time.RFC3339Nano, m["updated_at"].(string))
		if updated.Before(created) {
			t.Fatal("updated_at before created_at")
		}
		got := e.expect(http.MethodGet, path, nil, 200)
		if got["title"] != "New" {
			t.Fatalf("not persisted: %v", got)
		}
	})

	t.Run("partial keeps other fields", func(t *testing.T) {
		m := e.expect(http.MethodPut, path, obj{"description": "only"}, 200)
		if m["description"] != "only" || m["title"] != "New" || m["priority"] != "high" {
			t.Fatalf("unexpected: %v", m)
		}
	})

	t.Run("completed sets completed_at", func(t *testing.T) {
		m := e.expect(http.MethodPut, path, obj{"status": "completed"}, 200)
		s, _ := m["completed_at"].(string)
		if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
			t.Fatalf("completed_at = %v", m["completed_at"])
		}
		// Re-completing keeps the original completed_at.
		m2 := e.expect(http.MethodPut, path, obj{"status": "completed"}, 200)
		if m2["completed_at"] != s {
			t.Fatalf("completed_at changed: %v -> %v", s, m2["completed_at"])
		}
		// Moving away from completed keeps completed_at.
		m3 := e.expect(http.MethodPut, path, obj{"status": "pending"}, 200)
		if m3["completed_at"] != s || m3["status"] != "pending" {
			t.Fatalf("unexpected: %v", m3)
		}
	})

	t.Run("not found", func(t *testing.T) {
		e.expectError(http.MethodPut, "/api/v1/tasks/"+missingID, obj{"title": "x"}, 404, "NOT_FOUND")
	})
	t.Run("invalid status", func(t *testing.T) {
		e.expectError(http.MethodPut, path, obj{"status": "bogus"}, 400, "VALIDATION_ERROR")
	})
	t.Run("invalid priority", func(t *testing.T) {
		e.expectError(http.MethodPut, path, obj{"priority": "bogus"}, 400, "VALIDATION_ERROR")
	})
	t.Run("bad json", func(t *testing.T) {
		e.expectError(http.MethodPut, path, "{nope", 400, "VALIDATION_ERROR")
	})
	t.Run("empty title", func(t *testing.T) {
		e.expectError(http.MethodPut, path, obj{"title": ""}, 400, "VALIDATION_ERROR")
	})
	t.Run("title too long", func(t *testing.T) {
		e.expectError(http.MethodPut, path, obj{"title": strings.Repeat("a", 201)}, 400, "VALIDATION_ERROR")
	})
}

func TestUpdateTaskStatus(t *testing.T) {
	e := newEnv(t)
	id := e.create(obj{"title": "S"})["id"].(string)
	path := "/api/v1/tasks/" + id + "/status"

	m := e.expect(http.MethodPatch, path, obj{"status": "on_hold"}, 200)
	if m["status"] != "on_hold" || m["id"] != id {
		t.Fatalf("unexpected: %v", m)
	}
	if _, ok := m["completed_at"]; ok {
		t.Fatal("completed_at set unexpectedly")
	}
	m = e.expect(http.MethodPatch, path, obj{"status": "completed"}, 200)
	if m["status"] != "completed" || m["completed_at"] == nil {
		t.Fatalf("unexpected: %v", m)
	}
	got := e.expect(http.MethodGet, "/api/v1/tasks/"+id, nil, 200)
	if got["status"] != "completed" {
		t.Fatalf("not persisted: %v", got)
	}

	e.expectError(http.MethodPatch, path, obj{"status": "bogus"}, 400, "VALIDATION_ERROR")
	e.expectError(http.MethodPatch, path, obj{}, 400, "VALIDATION_ERROR")
	e.expectError(http.MethodPatch, path, "{bad", 400, "VALIDATION_ERROR")
	e.expectError(http.MethodPatch, "/api/v1/tasks/"+missingID+"/status", obj{"status": "pending"}, 404, "NOT_FOUND")
	// Validation happens before lookup.
	e.expectError(http.MethodPatch, "/api/v1/tasks/"+missingID+"/status", obj{"status": "bogus"}, 400, "VALIDATION_ERROR")
}

func TestDeleteTask(t *testing.T) {
	e := newEnv(t)
	id := e.create(obj{"title": "D"})["id"].(string)
	w, _ := e.do(http.MethodDelete, "/api/v1/tasks/"+id, nil)
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	e.expectError(http.MethodGet, "/api/v1/tasks/"+id, nil, 404, "NOT_FOUND")
	e.expectError(http.MethodDelete, "/api/v1/tasks/"+id, nil, 404, "NOT_FOUND")
}

func TestListTasksBasics(t *testing.T) {
	e := newEnv(t)
	m := e.expect(http.MethodGet, "/api/v1/tasks", nil, 200)
	if m["total_count"] != float64(2) {
		t.Fatalf("total_count = %v", m["total_count"])
	}
	if _, ok := m["next_page_token"]; ok {
		t.Fatal("next_page_token should be omitted")
	}
	ts := tasksOf(m)
	if len(ts) != 2 {
		t.Fatalf("len = %d", len(ts))
	}
	got := map[string]bool{}
	for _, tk := range ts {
		got[tk["title"].(string)] = true
		if tk["status"] != "pending" || tk["created_by"] != "system" {
			t.Fatalf("unexpected: %v", tk)
		}
	}
	if !got["Implement Go REST API"] || !got["Add gRPC support"] {
		t.Fatalf("seeded tasks missing: %v", got)
	}
}

func TestListTasksFilters(t *testing.T) {
	e := newEnv(t)
	e.clear()
	alice, bob := "alice", "bob"
	_ = bob
	a := e.createOrdered(
		obj{"title": "A", "tags": []string{"go", "api"}, "assigned_to": alice},
		obj{"title": "B", "tags": []string{"go"}, "assigned_to": "bob"},
		obj{"title": "C", "tags": []string{"api", "web"}},
		obj{"title": "D", "tags": []string{"go", "api", "web"}, "assigned_to": alice},
	)
	e.expect(http.MethodPatch, "/api/v1/tasks/"+a[1]["id"].(string)+"/status", obj{"status": "completed"}, 200)
	e.expect(http.MethodPatch, "/api/v1/tasks/"+a[2]["id"].(string)+"/status", obj{"status": "completed"}, 200)
	const asc = "&sort_order=created_at_asc"

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"all", "x=1", []string{"A", "B", "C", "D"}},
		{"status completed", "status=completed", []string{"B", "C"}},
		{"status pending", "status=pending", []string{"A", "D"}},
		{"status none", "status=cancelled", nil},
		{"assigned_to", "assigned_to=alice", []string{"A", "D"}},
		{"assigned_to none", "assigned_to=nobody", nil},
		{"tag single", "tags=go", []string{"A", "B", "D"}},
		{"tags AND", "tags=go,api", []string{"A", "D"}},
		{"tags AND with spaces", "tags=go,%20web", []string{"D"}},
		{"tag unknown", "tags=zzz", nil},
		{"combined", "status=pending&assigned_to=alice&tags=web", []string{"D"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := e.expect(http.MethodGet, "/api/v1/tasks?"+c.query+asc, nil, 200)
			assertTitles(t, tasksOf(m), c.want...)
			if m["total_count"] != float64(len(c.want)) {
				t.Fatalf("total_count = %v, want %d", m["total_count"], len(c.want))
			}
		})
	}
}

func TestListTasksSorting(t *testing.T) {
	e := newEnv(t)
	e.clear()
	d := func(day int) string { return fmt.Sprintf("2030-01-%02dT00:00:00Z", day) }
	e.createOrdered(
		obj{"title": "T1", "priority": "medium", "due_date": d(10)},
		obj{"title": "T2", "priority": "critical", "due_date": d(5)},
		obj{"title": "T3", "priority": "low", "due_date": d(20)},
		obj{"title": "T4", "priority": "high", "due_date": d(15)},
	)
	cases := []struct {
		order string
		want  []string
	}{
		{"created_at_asc", []string{"T1", "T2", "T3", "T4"}},
		{"created_at_desc", []string{"T4", "T3", "T2", "T1"}},
		{"due_date_asc", []string{"T2", "T1", "T4", "T3"}},
		{"due_date_desc", []string{"T3", "T4", "T1", "T2"}},
		{"priority_asc", []string{"T3", "T1", "T4", "T2"}},
		{"priority_desc", []string{"T2", "T4", "T1", "T3"}},
	}
	for _, c := range cases {
		t.Run(c.order, func(t *testing.T) {
			assertTitles(t, e.list("sort_order="+c.order), c.want...)
		})
	}

	t.Run("due date nil sorts last", func(t *testing.T) {
		e.create(obj{"title": "NoDue"})
		for _, o := range []string{"due_date_asc", "due_date_desc"} {
			got := e.list("sort_order=" + o)
			if len(got) != 5 || got[4]["title"] != "NoDue" {
				t.Fatalf("%s: %v", o, titles(got))
			}
		}
	})

	t.Run("unknown sort order accepted", func(t *testing.T) {
		if got := e.list("sort_order=bogus"); len(got) != 5 {
			t.Fatalf("len = %d", len(got))
		}
	})
}

func TestListTasksPagination(t *testing.T) {
	e := newEnv(t)
	e.clear()
	var bodies []obj
	for i := 1; i <= 5; i++ {
		bodies = append(bodies, obj{"title": fmt.Sprintf("P%d", i)})
	}
	e.createOrdered(bodies...)
	base := "/api/v1/tasks?sort_order=created_at_asc&page_size=2"

	m := e.expect(http.MethodGet, base, nil, 200)
	assertTitles(t, tasksOf(m), "P1", "P2")
	if m["next_page_token"] != "2" || m["total_count"] != float64(5) {
		t.Fatalf("unexpected: %v", m)
	}
	m = e.expect(http.MethodGet, base+"&page_token="+m["next_page_token"].(string), nil, 200)
	assertTitles(t, tasksOf(m), "P3", "P4")
	if m["next_page_token"] != "4" {
		t.Fatalf("token = %v", m["next_page_token"])
	}
	m = e.expect(http.MethodGet, base+"&page_token=4", nil, 200)
	assertTitles(t, tasksOf(m), "P5")
	if _, ok := m["next_page_token"]; ok {
		t.Fatal("last page must omit next_page_token")
	}
	if m["total_count"] != float64(5) {
		t.Fatalf("total_count = %v", m["total_count"])
	}

	t.Run("token past end", func(t *testing.T) {
		m := e.expect(http.MethodGet, base+"&page_token=99", nil, 200)
		if len(tasksOf(m)) != 0 || m["total_count"] != float64(5) {
			t.Fatalf("unexpected: %v", m)
		}
		if _, ok := m["next_page_token"]; ok {
			t.Fatal("unexpected next_page_token")
		}
	})
	t.Run("non-numeric token restarts at beginning", func(t *testing.T) {
		m := e.expect(http.MethodGet, base+"&page_token=abc", nil, 200)
		assertTitles(t, tasksOf(m), "P1", "P2")
	})
	t.Run("exact fit has no next token", func(t *testing.T) {
		m := e.expect(http.MethodGet, "/api/v1/tasks?sort_order=created_at_asc&page_size=5", nil, 200)
		if len(tasksOf(m)) != 5 {
			t.Fatalf("len = %d", len(tasksOf(m)))
		}
		if _, ok := m["next_page_token"]; ok {
			t.Fatal("unexpected next_page_token")
		}
	})
	t.Run("default page size is 20", func(t *testing.T) {
		e := newEnv(t)
		e.clear()
		for i := 0; i < 25; i++ {
			e.create(obj{"title": fmt.Sprintf("N%02d", i)})
		}
		m := e.expect(http.MethodGet, "/api/v1/tasks", nil, 200)
		if len(tasksOf(m)) != 20 || m["next_page_token"] != "20" || m["total_count"] != float64(25) {
			t.Fatalf("len=%d token=%v total=%v", len(tasksOf(m)), m["next_page_token"], m["total_count"])
		}
	})
	t.Run("max page size 100 accepted and capped", func(t *testing.T) {
		e := newEnv(t)
		e.clear()
		for i := 0; i < 105; i++ {
			e.create(obj{"title": fmt.Sprintf("N%03d", i)})
		}
		m := e.expect(http.MethodGet, "/api/v1/tasks?page_size=100", nil, 200)
		if len(tasksOf(m)) != 100 || m["next_page_token"] != "100" || m["total_count"] != float64(105) {
			t.Fatalf("len=%d token=%v total=%v", len(tasksOf(m)), m["next_page_token"], m["total_count"])
		}
	})
	t.Run("page_size outside 1..100 rejected", func(t *testing.T) {
		for _, ps := range []string{"0", "101", "-1", "abc"} {
			e.expectError(http.MethodGet, "/api/v1/tasks?page_size="+ps, nil, 400, "VALIDATION_ERROR")
		}
	})
}

func TestSearchTasks(t *testing.T) {
	e := newEnv(t)
	e.clear()
	e.create(obj{"title": "Alpha", "description": "nothing", "tags": []string{"zebra"}})
	e.create(obj{"title": "Zebra crossing", "description": "road", "tags": []string{"x"}})
	e.create(obj{"title": "Gamma", "description": "A ZEBRA appears", "tags": []string{"y"}})
	e.create(obj{"title": "Zebra", "description": "zebra", "tags": []string{"zebra"}})
	e.create(obj{"title": "Unrelated", "description": "none", "tags": []string{}})

	t.Run("score ordering", func(t *testing.T) {
		m := e.expect(http.MethodGet, "/api/v1/tasks/search?q=zebra", nil, 200)
		// Scores: "Zebra"=6, "Zebra crossing"=3, "Gamma"=2, "Alpha"=1.
		assertTitles(t, tasksOf(m), "Zebra", "Zebra crossing", "Gamma", "Alpha")
		if m["query"] != "zebra" || m["total_count"] != float64(4) {
			t.Fatalf("unexpected: %v", m)
		}
	})
	t.Run("case insensitive", func(t *testing.T) {
		m := e.expect(http.MethodGet, "/api/v1/tasks/search?q=ZeBrA", nil, 200)
		if m["query"] != "ZeBrA" || m["total_count"] != float64(4) {
			t.Fatalf("unexpected: %v", m)
		}
	})
	t.Run("page_size limits results not total", func(t *testing.T) {
		m := e.expect(http.MethodGet, "/api/v1/tasks/search?q=zebra&page_size=2", nil, 200)
		assertTitles(t, tasksOf(m), "Zebra", "Zebra crossing")
		if m["total_count"] != float64(4) {
			t.Fatalf("total_count = %v", m["total_count"])
		}
	})
	t.Run("no match", func(t *testing.T) {
		m := e.expect(http.MethodGet, "/api/v1/tasks/search?q=nomatchxyz", nil, 200)
		ts, ok := m["tasks"].([]any)
		if !ok || len(ts) != 0 || m["total_count"] != float64(0) || m["query"] != "nomatchxyz" {
			t.Fatalf("unexpected: %v", m)
		}
	})
	t.Run("q required", func(t *testing.T) {
		e.expectError(http.MethodGet, "/api/v1/tasks/search", nil, 400, "VALIDATION_ERROR")
		e.expectError(http.MethodGet, "/api/v1/tasks/search?q=", nil, 400, "VALIDATION_ERROR")
		e.expectError(http.MethodGet, "/api/v1/tasks/search?page_size=5", nil, 400, "VALIDATION_ERROR")
	})
	t.Run("page_size outside 1..100 rejected", func(t *testing.T) {
		for _, ps := range []string{"0", "101", "-3"} {
			e.expectError(http.MethodGet, "/api/v1/tasks/search?q=zebra&page_size="+ps, nil, 400, "VALIDATION_ERROR")
		}
	})
	t.Run("search route is not shadowed by :id", func(t *testing.T) {
		m := e.expect(http.MethodGet, "/api/v1/tasks/search?q=gamma", nil, 200)
		if _, ok := m["tasks"]; !ok {
			t.Fatalf("unexpected: %v", m)
		}
	})
}

func TestTaskStats(t *testing.T) {
	e := newEnv(t)
	statuses := []string{"pending", "in_progress", "completed", "cancelled", "on_hold"}
	priorities := []string{"low", "medium", "high", "critical"}

	m := e.expect(http.MethodGet, "/api/v1/tasks/stats", nil, 200)
	bs, bp := m["by_status"].(obj), m["by_priority"].(obj)
	if m["total"] != float64(2) || len(bs) != 5 || len(bp) != 4 {
		t.Fatalf("unexpected: %v", m)
	}
	for _, s := range statuses {
		if _, ok := bs[s]; !ok {
			t.Errorf("by_status.%s missing", s)
		}
	}
	for _, p := range priorities {
		if _, ok := bp[p]; !ok {
			t.Errorf("by_priority.%s missing", p)
		}
	}
	if bs["pending"] != float64(2) || bp["high"] != float64(1) || bp["medium"] != float64(1) ||
		bp["low"] != float64(0) || bp["critical"] != float64(0) {
		t.Fatalf("seed counts: %v", m)
	}

	id := e.create(obj{"title": "S", "priority": "critical"})["id"].(string)
	e.expect(http.MethodPatch, "/api/v1/tasks/"+id+"/status", obj{"status": "completed"}, 200)
	m = e.expect(http.MethodGet, "/api/v1/tasks/stats", nil, 200)
	bs, bp = m["by_status"].(obj), m["by_priority"].(obj)
	if m["total"] != float64(3) || bs["pending"] != float64(2) || bs["completed"] != float64(1) ||
		bp["critical"] != float64(1) {
		t.Fatalf("after update: %v", m)
	}

	e.clear()
	m = e.expect(http.MethodGet, "/api/v1/tasks/stats", nil, 200)
	if m["total"] != float64(0) || len(m["by_status"].(obj)) != 5 || len(m["by_priority"].(obj)) != 4 {
		t.Fatalf("empty: %v", m)
	}
}

func TestComments(t *testing.T) {
	e := newEnv(t)
	taskID := e.create(obj{"title": "WithComments"})["id"].(string)
	base := "/api/v1/tasks/" + taskID + "/comments"

	t.Run("empty list", func(t *testing.T) {
		m := e.expect(http.MethodGet, base, nil, 200)
		cs, ok := m["comments"].([]any)
		if !ok || len(cs) != 0 || m["total"] != float64(0) {
			t.Fatalf("unexpected: %v", m)
		}
	})

	var ids []string
	for i, body := range []string{"first", "second", "third"} {
		m := e.expect(http.MethodPost, base, obj{"author": "ann", "body": body}, 201)
		id, _ := m["id"].(string)
		if id == "" || m["task_id"] != taskID || m["author"] != "ann" || m["body"] != body {
			t.Fatalf("comment %d: %v", i, m)
		}
		for _, k := range []string{"created_at", "updated_at"} {
			if s, _ := m[k].(string); s == "" {
				t.Errorf("%s missing", k)
			}
		}
		ids = append(ids, id)
		time.Sleep(3 * time.Millisecond)
	}

	t.Run("list ordered by creation", func(t *testing.T) {
		m := e.expect(http.MethodGet, base, nil, 200)
		cs := m["comments"].([]any)
		if m["total"] != float64(3) || len(cs) != 3 {
			t.Fatalf("unexpected: %v", m)
		}
		for i, want := range []string{"first", "second", "third"} {
			c := cs[i].(obj)
			if c["body"] != want || c["id"] != ids[i] {
				t.Fatalf("comment %d: %v", i, c)
			}
		}
	})

	t.Run("comments are per task", func(t *testing.T) {
		other := e.create(obj{"title": "Other"})["id"].(string)
		m := e.expect(http.MethodGet, "/api/v1/tasks/"+other+"/comments", nil, 200)
		if m["total"] != float64(0) {
			t.Fatalf("unexpected: %v", m)
		}
	})

	t.Run("validation", func(t *testing.T) {
		e.expectError(http.MethodPost, base, "{bad", 400, "VALIDATION_ERROR")
		e.expectError(http.MethodPost, base, obj{"body": "x"}, 400, "VALIDATION_ERROR")
		e.expectError(http.MethodPost, base, obj{"author": "a"}, 400, "VALIDATION_ERROR")
		e.expectError(http.MethodPost, base, obj{"author": "", "body": "x"}, 400, "VALIDATION_ERROR")
		e.expectError(http.MethodPost, base, obj{"author": strings.Repeat("a", 101), "body": "x"}, 400, "VALIDATION_ERROR")
		e.expectError(http.MethodPost, base, obj{"author": "a", "body": strings.Repeat("b", 5001)}, 400, "VALIDATION_ERROR")
		e.expect(http.MethodPost, base, obj{"author": strings.Repeat("a", 100), "body": strings.Repeat("b", 5000)}, 201)
		e.expect(http.MethodDelete, base+"/"+e.expect(http.MethodGet, base, nil, 200)["comments"].([]any)[3].(obj)["id"].(string), nil, 204)
	})

	t.Run("delete", func(t *testing.T) {
		w, _ := e.do(http.MethodDelete, base+"/"+ids[1], nil)
		if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
			t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
		}
		m := e.expect(http.MethodGet, base, nil, 200)
		cs := m["comments"].([]any)
		if m["total"] != float64(2) || cs[0].(obj)["body"] != "first" || cs[1].(obj)["body"] != "third" {
			t.Fatalf("unexpected: %v", m)
		}
		e.expectError(http.MethodDelete, base+"/"+ids[1], nil, 404, "NOT_FOUND")
	})

	t.Run("404 paths", func(t *testing.T) {
		missing := "/api/v1/tasks/" + missingID + "/comments"
		e.expectError(http.MethodGet, missing, nil, 404, "NOT_FOUND")
		e.expectError(http.MethodPost, missing, obj{"author": "a", "body": "b"}, 404, "NOT_FOUND")
		e.expectError(http.MethodDelete, missing+"/"+ids[0], nil, 404, "NOT_FOUND")
		e.expectError(http.MethodDelete, base+"/"+missingID, nil, 404, "NOT_FOUND")
		// Comment id from a different task is not found here.
		other := e.create(obj{"title": "Other2"})["id"].(string)
		e.expectError(http.MethodDelete, "/api/v1/tasks/"+other+"/comments/"+ids[0], nil, 404, "NOT_FOUND")
	})

	t.Run("invalid body validated before task lookup", func(t *testing.T) {
		e.expectError(http.MethodPost, "/api/v1/tasks/"+missingID+"/comments", obj{"author": "a"}, 400, "VALIDATION_ERROR")
	})
}

func TestUnknownRoute(t *testing.T) {
	e := newEnv(t)
	w, _ := e.do(http.MethodGet, "/nope", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}
