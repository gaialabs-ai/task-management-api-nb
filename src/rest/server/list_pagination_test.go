package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
)

// seedViaAPI creates n tasks through the HTTP API on a service with only those tasks.
func seedViaAPI(t *testing.T, n int) (*services.TaskService, http.Handler) {
	t.Helper()
	svc := services.NewTaskService()
	r := newTestRouter(svc)
	// Remove the two sample tasks so counts are exact.
	list := decodeList(t, doRequest(r, "GET", "/api/v1/tasks", ""))
	for _, task := range list.Tasks {
		doRequest(r, "DELETE", "/api/v1/tasks/"+task.ID, "")
	}
	for i := 0; i < n; i++ {
		w := doRequest(r, "POST", "/api/v1/tasks", fmt.Sprintf(`{"title":"t%d"}`, i))
		if w.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
	}
	return svc, r
}

func TestListTasksHTTP_PaginationCharacterization(t *testing.T) {
	_, r := seedViaAPI(t, 25)

	// default page size 20
	resp := decodeList(t, doRequest(r, "GET", "/api/v1/tasks", ""))
	if len(resp.Tasks) != 20 || resp.TotalCount != 25 || resp.NextPageToken != "20" {
		t.Fatalf("default page: %d total=%d next=%q", len(resp.Tasks), resp.TotalCount, resp.NextPageToken)
	}

	// follow token to the last page
	w := doRequest(r, "GET", "/api/v1/tasks?page_token=20", "")
	resp = decodeList(t, w)
	if len(resp.Tasks) != 5 || resp.NextPageToken != "" {
		t.Fatalf("last page: %d next=%q", len(resp.Tasks), resp.NextPageToken)
	}

	// explicit page size
	resp = decodeList(t, doRequest(r, "GET", "/api/v1/tasks?page_size=10", ""))
	if len(resp.Tasks) != 10 || resp.NextPageToken != "10" {
		t.Fatalf("size 10: %d next=%q", len(resp.Tasks), resp.NextPageToken)
	}

	// out-of-range page_size is rejected with VALIDATION_ERROR
	for _, ps := range []string{"0", "-1", "101", "abc"} {
		w := doRequest(r, "GET", "/api/v1/tasks?page_size="+ps, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("page_size=%s: got %d", ps, w.Code)
			continue
		}
		if e := decodeErr(t, w); e.Error.Code != "VALIDATION_ERROR" {
			t.Errorf("page_size=%s: code %q", ps, e.Error.Code)
		}
	}

	// bad token falls back to offset 0
	resp = decodeList(t, doRequest(r, "GET", "/api/v1/tasks?page_token=zzz&page_size=5", ""))
	if len(resp.Tasks) != 5 || resp.NextPageToken != "5" {
		t.Fatalf("bad token: %d next=%q", len(resp.Tasks), resp.NextPageToken)
	}
}
