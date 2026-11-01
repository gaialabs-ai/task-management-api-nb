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
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/testutil"
	"github.com/gin-gonic/gin"
)

var fixedNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

// newTestRouter builds a fresh engine with sample data and a fixed clock.
func newTestRouter(t *testing.T) (*gin.Engine, *services.TaskService, *testutil.FakeNotifier) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService()
	svc.SetClock(func() time.Time { return fixedNow })
	n := &testutil.FakeNotifier{}
	svc.SetNotifier(n)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return r, svc, n
}

func do(r http.Handler, method, path string, body interface{}) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func create(t *testing.T, r http.Handler, title string, due *time.Time) models.Task {
	t.Helper()
	w := do(r, "POST", "/api/v1/tasks", models.CreateTaskRequest{Title: title, DueDate: due})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var task models.Task
	json.Unmarshal(w.Body.Bytes(), &task)
	return task
}

func ago(d time.Duration) *time.Time {
	v := fixedNow.Add(-d)
	return &v
}

func TestListTasksPaginationRegression(t *testing.T) {
	r, _, _ := newTestRouter(t)
	for i := 0; i < 3; i++ {
		create(t, r, fmt.Sprintf("t%d", i), nil)
	}
	// 2 sample + 3 created = 5 tasks
	var seen int
	token := ""
	for pages := 0; pages < 10; pages++ {
		path := "/api/v1/tasks?page_size=2&sort_order=created_at_asc"
		if token != "" {
			path += "&page_token=" + token
		}
		w := do(r, "GET", path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		var resp models.ListTasksResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.TotalCount != 5 {
			t.Fatalf("total = %d", resp.TotalCount)
		}
		seen += len(resp.Tasks)
		if resp.NextPageToken == "" {
			if pages != 2 || seen != 5 {
				t.Fatalf("pages=%d seen=%d", pages, seen)
			}
			return
		}
		if len(resp.Tasks) != 2 {
			t.Fatalf("page size = %d", len(resp.Tasks))
		}
		token = resp.NextPageToken
	}
	t.Fatal("pagination did not terminate")
}

func TestOverdueEndpoint(t *testing.T) {
	r, _, _ := newTestRouter(t)
	create(t, r, "recent", ago(time.Hour))
	create(t, r, "old", ago(48*time.Hour))
	create(t, r, "future", ago(-time.Hour))

	w := do(r, "GET", "/api/v1/tasks/overdue", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var resp models.ListTasksResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 || resp.Tasks[0].Title != "old" || resp.Tasks[1].Title != "recent" {
		t.Fatalf("unexpected: %s", w.Body)
	}
}

func TestOverdueRoutePrecedenceOverID(t *testing.T) {
	r, _, _ := newTestRouter(t)
	w := do(r, "GET", "/api/v1/tasks/overdue", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/overdue was shadowed by /:id: %d %s", w.Code, w.Body)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["tasks"]; !ok {
		t.Errorf("expected list response, got %s", w.Body)
	}
}

func TestOverdueEndpointPagination(t *testing.T) {
	r, _, _ := newTestRouter(t)
	for i := 1; i <= 3; i++ {
		create(t, r, fmt.Sprintf("o%d", i), ago(time.Duration(10-i)*time.Hour))
	}
	w := do(r, "GET", "/api/v1/tasks/overdue?page_size=2", nil)
	var p1 models.ListTasksResponse
	json.Unmarshal(w.Body.Bytes(), &p1)
	if len(p1.Tasks) != 2 || p1.NextPageToken != "2" || p1.TotalCount != 3 {
		t.Fatalf("page1: %s", w.Body)
	}
	w = do(r, "GET", "/api/v1/tasks/overdue?page_size=2&page_token="+p1.NextPageToken, nil)
	var p2 models.ListTasksResponse
	json.Unmarshal(w.Body.Bytes(), &p2)
	if len(p2.Tasks) != 1 || p2.NextPageToken != "" || p2.Tasks[0].Title != "o3" {
		t.Fatalf("page2: %s", w.Body)
	}
}

func TestOverdueEndpointBadPageSize(t *testing.T) {
	r, _, _ := newTestRouter(t)
	w := do(r, "GET", "/api/v1/tasks/overdue?page_size=1000", nil)
	assertError(t, w, http.StatusBadRequest, "VALIDATION_ERROR")
}

func assertError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body)
	}
	var e models.ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &e)
	if e.Error.Code != code {
		t.Fatalf("code %q, want %q", e.Error.Code, code)
	}
}

func TestAssignEndpoint(t *testing.T) {
	r, _, n := newTestRouter(t)
	task := create(t, r, "x", nil)
	path := "/api/v1/tasks/" + task.ID + "/assign"

	t.Run("success", func(t *testing.T) {
		w := do(r, "POST", path, map[string]string{"assigned_to": "alice@example.com"})
		if w.Code != http.StatusOK {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		var got models.Task
		json.Unmarshal(w.Body.Bytes(), &got)
		if got.AssignedTo == nil || *got.AssignedTo != "alice@example.com" {
			t.Fatalf("body: %s", w.Body)
		}
		if len(n.Calls) != 1 || n.Calls[0] != task.ID+"|alice@example.com" {
			t.Fatalf("notifier calls: %v", n.Calls)
		}
	})

	t.Run("invalid email", func(t *testing.T) {
		before := len(n.Calls)
		assertError(t, do(r, "POST", path, map[string]string{"assigned_to": "nope"}), 400, "VALIDATION_ERROR")
		if len(n.Calls) != before {
			t.Error("notifier called on invalid email")
		}
	})

	t.Run("missing field", func(t *testing.T) {
		assertError(t, do(r, "POST", path, map[string]string{}), 400, "VALIDATION_ERROR")
	})

	t.Run("unknown task", func(t *testing.T) {
		assertError(t, do(r, "POST", "/api/v1/tasks/nope/assign", map[string]string{"assigned_to": "a@example.com"}), 404, "NOT_FOUND")
	})

	t.Run("notifier failure does not fail request", func(t *testing.T) {
		n.Err = errors.New("boom")
		w := do(r, "POST", path, map[string]string{"assigned_to": "bob@example.com"})
		if w.Code != http.StatusOK {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	})
}
