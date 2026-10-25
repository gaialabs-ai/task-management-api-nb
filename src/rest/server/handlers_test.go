package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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

	seen := map[string]bool{}
	token := ""
	pages := 0
	for {
		q := url.Values{"page_size": {"5"}, "sort_order": {"created_at_asc"}}
		if token != "" {
			q.Set("page_token", token)
		}
		w := doRequest(engine, http.MethodGet, "/api/v1/tasks?"+q.Encode(), "")
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var resp models.ListTasksResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		pages++
		if resp.TotalCount != total {
			t.Fatalf("total_count = %d, want %d", resp.TotalCount, total)
		}
		for _, task := range resp.Tasks {
			if seen[task.ID] {
				t.Fatalf("duplicate task %s across pages", task.ID)
			}
			seen[task.ID] = true
		}
		if resp.NextPageToken == "" {
			if len(resp.Tasks) != total-5*(pages-1) {
				t.Fatalf("last page len = %d", len(resp.Tasks))
			}
			break
		}
		if len(resp.Tasks) != 5 {
			t.Fatalf("page %d len = %d, want 5", pages, len(resp.Tasks))
		}
		token = resp.NextPageToken
		if pages > 10 {
			t.Fatal("too many pages")
		}
	}
	if pages != 3 || len(seen) != total {
		t.Fatalf("pages=%d seen=%d total=%d", pages, len(seen), total)
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
