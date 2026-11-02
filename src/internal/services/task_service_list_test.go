package services

import (
	"fmt"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// newListFixture builds a service holding exactly n tasks with strictly
// increasing CreatedAt values and titles "t00".."t(n-1)".
func newListFixture(t *testing.T, n int) *TaskService {
	t.Helper()
	s := NewTaskService()
	s.mu.Lock()
	s.tasks = make(map[string]*models.Task)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("id-%02d", i)
		s.tasks[id] = &models.Task{
			ID:        id,
			Title:     fmt.Sprintf("t%02d", i),
			Status:    models.TaskStatusPending,
			Priority:  models.TaskPriorityMedium,
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}
	}
	s.mu.Unlock()
	return s
}

func titles(tasks []models.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.Title)
	}
	return out
}

// TestListTasksPaginationRegression pins the pagination behavior of ListTasks.
func TestListTasksPaginationRegression(t *testing.T) {
	tests := []struct {
		name      string
		total     int
		query     models.ListTasksQuery
		wantFirst string
		wantLen   int
		wantNext  string
	}{
		{"default page size 20 when zero", 25, models.ListTasksQuery{}, "t00", 20, "20"},
		{"explicit page size", 25, models.ListTasksQuery{PageSize: 10}, "t00", 10, "10"},
		{"second page via token", 25, models.ListTasksQuery{PageSize: 10, PageToken: "10"}, "t10", 10, "20"},
		{"last partial page has no next token", 25, models.ListTasksQuery{PageSize: 10, PageToken: "20"}, "t20", 5, ""},
		{"page size capped at 100", 120, models.ListTasksQuery{PageSize: 500}, "t00", 100, "100"},
		{"negative page size uses default", 25, models.ListTasksQuery{PageSize: -3}, "t00", 20, "20"},
		{"invalid token treated as zero", 5, models.ListTasksQuery{PageSize: 2, PageToken: "abc"}, "t00", 2, "2"},
		{"token past end yields empty page", 5, models.ListTasksQuery{PageSize: 2, PageToken: "50"}, "", 0, ""},
		{"exact fit has no next token", 4, models.ListTasksQuery{PageSize: 4}, "t00", 4, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newListFixture(t, tt.total)
			tt.query.SortOrder = models.SortOrderCreatedAtAsc
			resp, err := s.ListTasks(tt.query)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.TotalCount != tt.total {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, tt.total)
			}
			if len(resp.Tasks) != tt.wantLen {
				t.Fatalf("len(Tasks) = %d, want %d", len(resp.Tasks), tt.wantLen)
			}
			if tt.wantLen > 0 && resp.Tasks[0].Title != tt.wantFirst {
				t.Errorf("first = %s, want %s", resp.Tasks[0].Title, tt.wantFirst)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
		})
	}
}

func TestListTasksFilterAndSortRegression(t *testing.T) {
	s := newListFixture(t, 4)
	s.mu.Lock()
	s.tasks["id-01"].Status = models.TaskStatusCompleted
	s.tasks["id-02"].Status = models.TaskStatusCompleted
	s.mu.Unlock()

	resp, err := s.ListTasks(models.ListTasksQuery{Status: models.TaskStatusCompleted, SortOrder: models.SortOrderCreatedAtDesc})
	if err != nil {
		t.Fatal(err)
	}
	got := titles(resp.Tasks)
	if len(got) != 2 || got[0] != "t02" || got[1] != "t01" || resp.TotalCount != 2 {
		t.Errorf("got %v total %d", got, resp.TotalCount)
	}
}
