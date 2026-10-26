package services

import (
	"fmt"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

var baseTime = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

// newEmptyService returns a service with the sample data removed.
func newEmptyService(opts ...Option) *TaskService {
	s := NewTaskService(opts...)
	s.tasks = make(map[string]*models.Task)
	return s
}

// seedTasks inserts n tasks with ids "t000".. and created_at increasing by a minute.
func seedTasks(s *TaskService, n int) {
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("t%03d", i)
		s.tasks[id] = &models.Task{
			ID:        id,
			Title:     id,
			Status:    models.TaskStatusPending,
			Priority:  models.TaskPriorityMedium,
			CreatedAt: baseTime.Add(time.Duration(i) * time.Minute),
		}
	}
}

func ids(tasks []models.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

// Characterization tests for the existing ListTasks pagination behavior.
func TestListTasksPagination(t *testing.T) {
	tests := []struct {
		name      string
		seed      int
		pageSize  int
		token     string
		wantLen   int
		wantFirst string
		wantNext  string
		wantTotal int
	}{
		{"default page size is 20", 25, 0, "", 20, "t000", "20", 25},
		{"negative page size falls back to 20", 25, -5, "", 20, "t000", "20", 25},
		{"page size capped at 100", 150, 500, "", 100, "t000", "100", 150},
		{"explicit page size", 25, 10, "", 10, "t000", "10", 25},
		{"second page via token", 25, 10, "10", 10, "t010", "20", 25},
		{"last partial page has no next token", 25, 10, "20", 5, "t020", "", 25},
		{"exact fit has no next token", 20, 20, "", 20, "t000", "", 20},
		{"token at end yields empty page", 5, 10, "5", 0, "", "", 5},
		{"token past end yields empty page", 5, 10, "50", 0, "", "", 5},
		{"non-numeric token starts at beginning", 5, 2, "abc", 2, "t000", "2", 5},
		{"negative token starts at beginning", 5, 2, "-1", 2, "t000", "2", 5},
		{"empty collection", 0, 10, "", 0, "", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEmptyService()
			seedTasks(s, tt.seed)
			resp, err := s.ListTasks(models.ListTasksQuery{
				PageSize:  tt.pageSize,
				PageToken: tt.token,
				SortOrder: models.SortOrderCreatedAtAsc,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(resp.Tasks) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(resp.Tasks), tt.wantLen)
			}
			if tt.wantLen > 0 && resp.Tasks[0].ID != tt.wantFirst {
				t.Errorf("first = %s, want %s", resp.Tasks[0].ID, tt.wantFirst)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("next token = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
			if resp.TotalCount != tt.wantTotal {
				t.Errorf("total = %d, want %d", resp.TotalCount, tt.wantTotal)
			}
		})
	}
}

func TestListTasksWalkAllPages(t *testing.T) {
	s := newEmptyService()
	seedTasks(s, 23)
	var got []string
	token := ""
	pages := 0
	for {
		resp, err := s.ListTasks(models.ListTasksQuery{PageSize: 5, PageToken: token, SortOrder: models.SortOrderCreatedAtAsc})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ids(resp.Tasks)...)
		pages++
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
		if pages > 10 {
			t.Fatal("too many pages")
		}
	}
	if pages != 5 || len(got) != 23 {
		t.Fatalf("pages=%d items=%d, want 5/23", pages, len(got))
	}
	for i, id := range got {
		if want := fmt.Sprintf("t%03d", i); id != want {
			t.Fatalf("item %d = %s, want %s", i, id, want)
		}
	}
}

func TestListTasksTotalCountReflectsFilter(t *testing.T) {
	s := newEmptyService()
	seedTasks(s, 6)
	s.tasks["t000"].Status = models.TaskStatusCompleted
	s.tasks["t001"].Status = models.TaskStatusCompleted
	resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 1, Status: models.TaskStatusCompleted, SortOrder: models.SortOrderCreatedAtAsc})
	if resp.TotalCount != 2 || len(resp.Tasks) != 1 || resp.NextPageToken != "1" {
		t.Fatalf("unexpected response: total=%d len=%d next=%q", resp.TotalCount, len(resp.Tasks), resp.NextPageToken)
	}
}

func TestListTasksReturnsCopies(t *testing.T) {
	s := newEmptyService()
	seedTasks(s, 1)
	resp, _ := s.ListTasks(models.ListTasksQuery{})
	resp.Tasks[0].Title = "changed"
	if s.tasks["t000"].Title != "t000" {
		t.Fatal("ListTasks must return copies")
	}
}
