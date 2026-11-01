package services

import (
	"fmt"
	"testing"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// newEmptyService returns a service with no tasks (sample data removed).
func newEmptyService() *TaskService {
	s := NewTaskService()
	s.mu.Lock()
	s.tasks = make(map[string]*models.Task)
	s.mu.Unlock()
	return s
}

func seedN(t *testing.T, s *TaskService, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		task, err := s.CreateTask(models.CreateTaskRequest{Title: fmt.Sprintf("task-%03d", i)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, task.ID)
	}
	return ids
}

func TestListTasksPagination(t *testing.T) {
	s := newEmptyService()
	seedN(t, s, 25)
	base := models.ListTasksQuery{SortOrder: models.SortOrderCreatedAtAsc}

	tests := []struct {
		name      string
		pageSize  int
		token     string
		wantLen   int
		wantNext  string
		wantTotal int
	}{
		{"default page size when zero", 0, "", 20, "20", 25},
		{"negative page size uses default", -5, "", 20, "20", 25},
		{"explicit page size", 10, "", 10, "10", 25},
		{"clamped to 100", 500, "", 25, "", 25},
		{"second page", 10, "10", 10, "20", 25},
		{"last partial page", 10, "20", 5, "", 25},
		{"token at total", 10, "25", 0, "", 25},
		{"token beyond total", 10, "1000", 0, "", 25},
		{"invalid token starts at 0", 10, "abc", 10, "10", 25},
		{"exact fit has no next token", 25, "", 25, "", 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := base
			q.PageSize = tt.pageSize
			q.PageToken = tt.token
			resp, err := s.ListTasks(q)
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Tasks) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(resp.Tasks), tt.wantLen)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("next = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
			if resp.TotalCount != tt.wantTotal {
				t.Errorf("total = %d, want %d", resp.TotalCount, tt.wantTotal)
			}
		})
	}
}

func TestListTasksPagesAreContiguous(t *testing.T) {
	s := newEmptyService()
	seedN(t, s, 7)
	seen := map[string]bool{}
	token := ""
	for pages := 0; pages < 10; pages++ {
		resp, err := s.ListTasks(models.ListTasksQuery{PageSize: 3, PageToken: token, SortOrder: models.SortOrderCreatedAtAsc})
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range resp.Tasks {
			if seen[task.ID] {
				t.Fatalf("duplicate task %s", task.ID)
			}
			seen[task.ID] = true
		}
		token = resp.NextPageToken
		if token == "" {
			break
		}
	}
	if len(seen) != 7 {
		t.Errorf("saw %d tasks, want 7", len(seen))
	}
}
