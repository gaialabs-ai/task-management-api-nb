package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

func newTestRouter(t *testing.T) (*gin.Engine, *services.TaskService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := services.NewTaskService()
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return r, svc
}

func doReq(r http.Handler, method, path string, body interface{}) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func seedTasks(t *testing.T, svc *services.TaskService, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := svc.CreateTask(models.CreateTaskRequest{Title: fmt.Sprintf("seed %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
}

// Regression test for GET /api/v1/tasks pagination (added before the helper refactor).
func TestListTasksPaginationRegression(t *testing.T) {
	r, svc := newTestRouter(t)
	seedTasks(t, svc, 28) // 2 sample + 28 = 30 total

	decode := func(w *httptest.ResponseRecorder) models.ListTasksResponse {
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var resp models.ListTasksResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Default page size is 20.
	resp := decode(doReq(r, "GET", "/api/v1/tasks?sort_order=created_at_asc", nil))
	if len(resp.Tasks) != 20 || resp.TotalCount != 30 || resp.NextPageToken != "20" {
		t.Fatalf("default page: len=%d total=%d next=%q", len(resp.Tasks), resp.TotalCount, resp.NextPageToken)
	}
	// Second page uses numeric token and has no next token.
	resp = decode(doReq(r, "GET", "/api/v1/tasks?sort_order=created_at_asc&page_token=20", nil))
	if len(resp.Tasks) != 10 || resp.NextPageToken != "" {
		t.Fatalf("second page: len=%d next=%q", len(resp.Tasks), resp.NextPageToken)
	}
	// Explicit page size.
	resp = decode(doReq(r, "GET", "/api/v1/tasks?page_size=7&page_token=14", nil))
	if len(resp.Tasks) != 7 || resp.NextPageToken != "21" {
		t.Fatalf("page_size=7: len=%d next=%q", len(resp.Tasks), resp.NextPageToken)
	}
	// Token past the end yields empty page.
	resp = decode(doReq(r, "GET", "/api/v1/tasks?page_token=500", nil))
	if len(resp.Tasks) != 0 || resp.NextPageToken != "" || resp.TotalCount != 30 {
		t.Fatalf("past end: len=%d next=%q total=%d", len(resp.Tasks), resp.NextPageToken, resp.TotalCount)
	}
	// Over-max page size is rejected by query validation.
	if w := doReq(r, "GET", "/api/v1/tasks?page_size=101", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("page_size=101 expected 400, got %d", w.Code)
	}
	// Max page size 100 accepted.
	resp = decode(doReq(r, "GET", "/api/v1/tasks?page_size=100", nil))
	if len(resp.Tasks) != 30 {
		t.Fatalf("page_size=100: len=%d", len(resp.Tasks))
	}
	// Pages walk without overlap.
	seen := map[string]bool{}
	token := ""
	for {
		path := "/api/v1/tasks?page_size=8&sort_order=created_at_asc"
		if token != "" {
			path += "&page_token=" + token
		}
		resp = decode(doReq(r, "GET", path, nil))
		for _, tk := range resp.Tasks {
			if seen[tk.ID] {
				t.Fatalf("duplicate %s", tk.ID)
			}
			seen[tk.ID] = true
		}
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	if len(seen) != 30 {
		t.Fatalf("walked %d tasks, want 30", len(seen))
	}
}

func TestOverdueRouteTakesPrecedenceOverID(t *testing.T) {
	r, svc := newTestRouter(t)
	svc.SetClock(func() time.Time { return time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC) })
	due := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	task, _ := svc.CreateTask(models.CreateTaskRequest{Title: "late", DueDate: &due})

	w := doReq(r, "GET", "/api/v1/tasks/overdue", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp models.ListTasksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 1 || resp.Tasks[0].ID != task.ID {
		t.Fatalf("unexpected body %s", w.Body.String())
	}
	if w := doReq(r, "GET", "/api/v1/tasks/overdue?page_size=0", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("page_size=0 expected 400, got %d", w.Code)
	}
}

func TestAssignEndpoint(t *testing.T) {
	r, svc := newTestRouter(t)
	task, _ := svc.CreateTask(models.CreateTaskRequest{Title: "t"})
	path := "/api/v1/tasks/" + task.ID + "/assign"

	w := doReq(r, "POST", path, map[string]string{"assignee_email": "bob@example.com"})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got models.Task
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.ID != task.ID || got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
		t.Fatalf("bad body %s", w.Body.String())
	}

	// alias field
	if w := doReq(r, "POST", path, map[string]string{"assignee": "al@example.com"}); w.Code != http.StatusOK {
		t.Fatalf("alias status %d", w.Code)
	}

	assertErr := func(w *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		var e models.ErrorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &e)
		if w.Code != status || e.Error.Code != code {
			t.Fatalf("got %d %s, want %d %s (%s)", w.Code, e.Error.Code, status, code, w.Body.String())
		}
	}
	assertErr(doReq(r, "POST", path, map[string]string{"assignee_email": "not-an-email"}), 400, "VALIDATION_ERROR")
	assertErr(doReq(r, "POST", path, map[string]string{}), 400, "VALIDATION_ERROR")
	assertErr(doReq(r, "POST", path, map[string]string{"assignee_email": "Bob <b@example.com>"}), 400, "VALIDATION_ERROR")
	assertErr(doReq(r, "POST", "/api/v1/tasks/missing/assign", map[string]string{"assignee_email": "bob@example.com"}), 404, "NOT_FOUND")

	req := httptest.NewRequest("POST", path, bytes.NewBufferString("{bad"))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertErr(w, 400, "VALIDATION_ERROR")
}
