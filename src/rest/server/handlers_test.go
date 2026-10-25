package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

func newTestEngine(svc *services.TaskService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewHandler(svc).RegisterRoutes(engine)
	return engine
}

func doRequest(engine *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// walkPages follows next_page_token from the first page of GET basePath?query until
// the token is empty and returns every page in order. It fails the test on a
// non-200 status, bad JSON, or more than maxPages pages (runaway token loop).
func walkPages(t *testing.T, engine *gin.Engine, basePath string, query url.Values) []models.ListTasksResponse {
	t.Helper()
	const maxPages = 10
	var pages []models.ListTasksResponse
	q := url.Values{}
	for k, v := range query {
		q[k] = append([]string(nil), v...)
	}
	for {
		w := doRequest(engine, http.MethodGet, basePath+"?"+q.Encode(), "")
		var resp models.ListTasksResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || w.Code != http.StatusOK {
			t.Fatalf("%s: status %d body %s (err %v)", basePath, w.Code, w.Body.String(), err)
		}
		pages = append(pages, resp)
		if resp.NextPageToken == "" {
			return pages
		}
		if len(pages) >= maxPages {
			t.Fatal("too many pages")
		}
		q.Set("page_token", resp.NextPageToken)
	}
}

func TestListTasksPaginationRegression(t *testing.T) {
	svc := services.NewTaskService()
	engine := newTestEngine(svc)

	// NewTaskService seeds 2 sample tasks; add more so total exceeds page sizes.
	const extra = 10
	for i := 0; i < extra; i++ {
		w := doRequest(engine, http.MethodPost, "/api/v1/tasks", fmt.Sprintf(`{"title":"task %d"}`, i))
		if w.Code != http.StatusCreated {
			t.Fatalf("create: status %d body %s", w.Code, w.Body.String())
		}
	}

	var total int
	{
		w := doRequest(engine, http.MethodGet, "/api/v1/tasks", "")
		var resp models.ListTasksResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		total = resp.TotalCount
		if total != 2+extra || len(resp.Tasks) != total || resp.NextPageToken != "" {
			t.Fatalf("default page: total=%d len=%d token=%q", total, len(resp.Tasks), resp.NextPageToken)
		}
	}

	pages := walkPages(t, engine, "/api/v1/tasks", url.Values{"page_size": {"5"}, "sort_order": {"created_at_asc"}})
	seen := map[string]bool{}
	for i, resp := range pages {
		if resp.TotalCount != total {
			t.Fatalf("total_count = %d, want %d", resp.TotalCount, total)
		}
		last := i == len(pages)-1
		if last && len(resp.Tasks) != total-5*i {
			t.Fatalf("last page len = %d", len(resp.Tasks))
		}
		if !last && len(resp.Tasks) != 5 {
			t.Fatalf("page %d len = %d, want 5", i+1, len(resp.Tasks))
		}
		for _, task := range resp.Tasks {
			if seen[task.ID] {
				t.Fatalf("duplicate task %s across pages", task.ID)
			}
			seen[task.ID] = true
		}
	}
	if len(pages) != 3 || len(seen) != total {
		t.Fatalf("pages=%d seen=%d total=%d", len(pages), len(seen), total)
	}

	// Out-of-range token yields an empty page, same total, no next token.
	w := doRequest(engine, http.MethodGet, "/api/v1/tasks?page_token=999", "")
	var resp models.ListTasksResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != http.StatusOK || len(resp.Tasks) != 0 || resp.TotalCount != total || resp.NextPageToken != "" {
		t.Fatalf("out of range: %d %+v", w.Code, resp)
	}

	// Invalid page_size is rejected.
	if w := doRequest(engine, http.MethodGet, "/api/v1/tasks?page_size=101", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("page_size=101: status %d", w.Code)
	}
}

type recNotifier struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (r *recNotifier) NotifyAssignment(task models.Task, assignee string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, task.ID+"|"+assignee)
	return r.err
}

func createTask(t *testing.T, engine *gin.Engine, body string) models.Task {
	t.Helper()
	w := doRequest(engine, http.MethodPost, "/api/v1/tasks", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var task models.Task
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestOverdueTasks(t *testing.T) {
	svc := services.NewTaskService()
	fixed := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return fixed })
	engine := newTestEngine(svc)

	// Empty case: tasks must serialize as [].
	w := doRequest(engine, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"tasks":[]`) {
		t.Fatalf("empty: %d %s", w.Code, w.Body.String())
	}

	d := func(days int) string { return fixed.AddDate(0, 0, days).Format(time.RFC3339) }
	late2 := createTask(t, engine, `{"title":"late2","due_date":"`+d(-2)+`"}`)
	late5 := createTask(t, engine, `{"title":"late5","due_date":"`+d(-5)+`"}`)
	late1 := createTask(t, engine, `{"title":"late1","due_date":"`+d(-1)+`"}`)
	createTask(t, engine, `{"title":"future","due_date":"`+d(3)+`"}`)
	createTask(t, engine, `{"title":"nodue"}`)
	done := createTask(t, engine, `{"title":"done","due_date":"`+d(-9)+`"}`)
	canc := createTask(t, engine, `{"title":"canc","due_date":"`+d(-9)+`"}`)
	for id, st := range map[string]string{done.ID: "completed", canc.ID: "cancelled"} {
		if w := doRequest(engine, http.MethodPatch, "/api/v1/tasks/"+id+"/status", `{"status":"`+st+`"}`); w.Code != 200 {
			t.Fatalf("status: %s", w.Body.String())
		}
	}

	var ids []string
	for _, resp := range walkPages(t, engine, "/api/v1/tasks/overdue", url.Values{"page_size": {"2"}}) {
		if resp.TotalCount != 3 {
			t.Fatalf("total_count %d", resp.TotalCount)
		}
		for _, x := range resp.Tasks {
			ids = append(ids, x.ID)
		}
	}
	want := []string{late5.ID, late2.ID, late1.ID}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Fatalf("order %v want %v", ids, want)
	}

	if w := doRequest(engine, http.MethodGet, "/api/v1/tasks/overdue?page_size=0", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("page_size=0: %d", w.Code)
	}
	if w := doRequest(engine, http.MethodGet, "/api/v1/tasks/overdue?page_size=500", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("page_size=500: %d", w.Code)
	}
}

func TestAssignTask(t *testing.T) {
	svc := services.NewTaskService()
	fixed := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return fixed })
	n := &recNotifier{err: errors.New("smtp down")}
	svc.SetNotifier(n)
	engine := newTestEngine(svc)
	task := createTask(t, engine, `{"title":"t"}`)
	path := "/api/v1/tasks/" + task.ID + "/assign"

	w := doRequest(engine, http.MethodPost, path, `{"assigned_to":"bob@example.com"}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var got models.Task
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" || !got.UpdatedAt.Equal(fixed) {
		t.Fatalf("bad task %+v", got)
	}
	if len(n.calls) != 1 || n.calls[0] != task.ID+"|bob@example.com" {
		t.Fatalf("notifier calls %v", n.calls)
	}

	for _, body := range []string{`{`, `{}`, `{"assigned_to":""}`, `{"assigned_to":"  "}`, `{"assigned_to":"nope"}`, `{"assigned_to":"Bob <b@x.com>"}`} {
		w := doRequest(engine, http.MethodPost, path, body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "VALIDATION_ERROR") {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	w = doRequest(engine, http.MethodPost, "/api/v1/tasks/missing/assign", `{"assigned_to":"a@b.com"}`)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "NOT_FOUND") || !strings.Contains(w.Body.String(), "task with ID missing not found") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(n.calls) != 1 {
		t.Fatalf("notifier called on failures: %v", n.calls)
	}

	if _, err := svc.AssignTask("missing", "a@b.com"); !errors.Is(err, services.ErrTaskNotFound) {
		t.Fatal(err)
	}
	if _, err := svc.AssignTask(task.ID, "x"); !errors.Is(err, services.ErrInvalidEmail) {
		t.Fatal(err)
	}
}
