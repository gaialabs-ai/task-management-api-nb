package services

import (
	"fmt"
	"testing"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Characterization tests for ListTasks pagination. They pin the behaviour that
// existed before the pagination logic was extracted into a shared helper.

func listIDs(r *models.ListTasksResponse) []string {
	ids := make([]string, 0, len(r.Tasks))
	for _, t := range r.Tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

func TestListTasksPagination_DefaultPageSize(t *testing.T) {
	s := newEmptyService()
	seedN(s, 25)
	resp, err := s.ListTasks(models.ListTasksQuery{SortOrder: models.SortOrderCreatedAtAsc})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Tasks) != 20 || resp.TotalCount != 25 || resp.NextPageToken != "20" {
		t.Fatalf("got %d tasks total=%d next=%q", len(resp.Tasks), resp.TotalCount, resp.NextPageToken)
	}
}

func TestListTasksPagination_Clamp(t *testing.T) {
	s := newEmptyService()
	seedN(s, 150)
	cases := []struct {
		size, want int
		next       string
	}{
		{0, 20, "20"}, {-5, 20, "20"}, {1, 1, "1"}, {100, 100, "100"}, {101, 100, "100"}, {1000, 100, "100"},
	}
	for _, c := range cases {
		resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: c.size, SortOrder: models.SortOrderCreatedAtAsc})
		if len(resp.Tasks) != c.want || resp.NextPageToken != c.next || resp.TotalCount != 150 {
			t.Errorf("size %d: got %d next=%q total=%d", c.size, len(resp.Tasks), resp.NextPageToken, resp.TotalCount)
		}
	}
}

func TestListTasksPagination_WalkPages(t *testing.T) {
	s := newEmptyService()
	seedN(s, 7)
	var all []string
	token := ""
	pages := 0
	for {
		resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 3, PageToken: token, SortOrder: models.SortOrderCreatedAtAsc})
		all = append(all, listIDs(resp)...)
		pages++
		if resp.TotalCount != 7 {
			t.Fatalf("total %d", resp.TotalCount)
		}
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	if pages != 3 || len(all) != 7 {
		t.Fatalf("pages=%d all=%v", pages, all)
	}
	for i, id := range all {
		if id != fmt.Sprintf("t%03d", i) {
			t.Fatalf("order wrong: %v", all)
		}
	}
}

func TestListTasksPagination_ExactBoundaryHasNoNextToken(t *testing.T) {
	s := newEmptyService()
	seedN(s, 6)
	resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 3, PageToken: "3", SortOrder: models.SortOrderCreatedAtAsc})
	if len(resp.Tasks) != 3 || resp.NextPageToken != "" {
		t.Fatalf("got %d next=%q", len(resp.Tasks), resp.NextPageToken)
	}
}

func TestListTasksPagination_BadOrOutOfRangeToken(t *testing.T) {
	s := newEmptyService()
	seedN(s, 5)
	// Non-numeric token falls back to offset 0.
	resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 2, PageToken: "abc", SortOrder: models.SortOrderCreatedAtAsc})
	if got := listIDs(resp); len(got) != 2 || got[0] != "t000" || resp.NextPageToken != "2" {
		t.Fatalf("got %v next=%q", got, resp.NextPageToken)
	}
	// Offset beyond the end yields an empty page and no next token.
	resp, _ = s.ListTasks(models.ListTasksQuery{PageSize: 2, PageToken: "99", SortOrder: models.SortOrderCreatedAtAsc})
	if len(resp.Tasks) != 0 || resp.NextPageToken != "" || resp.TotalCount != 5 {
		t.Fatalf("got %d next=%q total=%d", len(resp.Tasks), resp.NextPageToken, resp.TotalCount)
	}
}

func TestListTasksPagination_TotalCountReflectsFilter(t *testing.T) {
	s := newEmptyService()
	seedN(s, 4)
	seedTask(s, "done", models.TaskStatusCompleted, nil, 100)
	resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 2, Status: models.TaskStatusPending, SortOrder: models.SortOrderCreatedAtAsc})
	if resp.TotalCount != 4 || len(resp.Tasks) != 2 {
		t.Fatalf("total=%d len=%d", resp.TotalCount, len(resp.Tasks))
	}
}
func TestListTasksPagination_NegativeTokenDoesNotPanic(t *testing.T) {
	s := newEmptyService()
	seedN(s, 3)
	resp, err := s.ListTasks(models.ListTasksQuery{PageSize: 2, PageToken: "-1", SortOrder: models.SortOrderCreatedAtAsc})
	if err != nil || resp.TotalCount != 3 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}
