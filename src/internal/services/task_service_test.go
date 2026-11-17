package services

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

type fakeNotifier struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, task.ID+"|"+email)
}

var fixedNow = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

func newTestService() *TaskService {
	s := &TaskService{
		tasks:    make(map[string]*models.Task),
		now:      func() time.Time { return fixedNow },
		notifier: LogNotifier{},
	}
	return s
}

// seed inserts a fully specified task directly into the map.
func seed(s *TaskService, task models.Task) {
	t := task
	s.tasks[t.ID] = &t
}

func addTask(s *TaskService, id string, status models.TaskStatus, due *time.Time) {
	seed(s, models.Task{ID: id, Title: id, Status: status, Priority: models.TaskPriorityMedium, DueDate: due})
}

func at(d time.Duration) *time.Time {
	t := fixedNow.Add(d)
	return &t
}

func ids(tasks []models.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListOverdueTasks(t *testing.T) {
	tests := []struct {
		name  string
		setup func(s *TaskService)
		query models.OverdueTasksQuery
		want  []string
		total int
		next  string
	}{
		{
			name: "filters by due date and status",
			setup: func(s *TaskService) {
				addTask(s, "past-pending", models.TaskStatusPending, at(-time.Hour))
				addTask(s, "past-hold", models.TaskStatusOnHold, at(-2*time.Hour))
				addTask(s, "past-progress", models.TaskStatusInProgress, at(-3*time.Hour))
				addTask(s, "past-done", models.TaskStatusCompleted, at(-time.Hour))
				addTask(s, "past-cancelled", models.TaskStatusCancelled, at(-time.Hour))
				addTask(s, "future", models.TaskStatusPending, at(time.Hour))
				addTask(s, "no-due", models.TaskStatusPending, nil)
				addTask(s, "due-now", models.TaskStatusPending, at(0))
			},
			want:  []string{"past-progress", "past-hold", "past-pending"},
			total: 3,
		},
		{
			name: "ties broken by id",
			setup: func(s *TaskService) {
				addTask(s, "b", models.TaskStatusPending, at(-time.Hour))
				addTask(s, "c", models.TaskStatusPending, at(-time.Hour))
				addTask(s, "a", models.TaskStatusPending, at(-time.Hour))
			},
			want:  []string{"a", "b", "c"},
			total: 3,
		},
		{
			name:  "empty",
			setup: func(s *TaskService) {},
			want:  []string{},
			total: 0,
		},
		{
			name: "pagination first page",
			setup: func(s *TaskService) {
				for i := 0; i < 5; i++ {
					addTask(s, fmt.Sprintf("t%d", i), models.TaskStatusPending, at(-time.Duration(10-i)*time.Hour))
				}
			},
			query: models.OverdueTasksQuery{PageSize: 2},
			want:  []string{"t0", "t1"},
			total: 5,
			next:  "2",
		},
		{
			name: "pagination last page",
			setup: func(s *TaskService) {
				for i := 0; i < 5; i++ {
					addTask(s, fmt.Sprintf("t%d", i), models.TaskStatusPending, at(-time.Duration(10-i)*time.Hour))
				}
			},
			query: models.OverdueTasksQuery{PageSize: 2, PageToken: "4"},
			want:  []string{"t4"},
			total: 5,
		},
		{
			name: "token beyond end",
			setup: func(s *TaskService) {
				addTask(s, "t0", models.TaskStatusPending, at(-time.Hour))
			},
			query: models.OverdueTasksQuery{PageToken: "10"},
			want:  []string{},
			total: 1,
		},
		{
			name: "page size capped at 100",
			setup: func(s *TaskService) {
				for i := 0; i < 120; i++ {
					addTask(s, fmt.Sprintf("t%03d", i), models.TaskStatusPending, at(-time.Hour))
				}
			},
			query: models.OverdueTasksQuery{PageSize: 500},
			want:  nil, // checked by length below
			total: 120,
			next:  "100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService()
			tt.setup(s)
			resp := s.ListOverdueTasks(tt.query)
			if tt.want != nil && !equal(ids(resp.Tasks), tt.want) {
				t.Errorf("tasks = %v, want %v", ids(resp.Tasks), tt.want)
			}
			if tt.want == nil && len(resp.Tasks) != 100 {
				t.Errorf("len(tasks) = %d, want 100", len(resp.Tasks))
			}
			if resp.TotalCount != tt.total {
				t.Errorf("total = %d, want %d", resp.TotalCount, tt.total)
			}
			if resp.NextPageToken != tt.next {
				t.Errorf("next = %q, want %q", resp.NextPageToken, tt.next)
			}
		})
	}
}

func TestAssignTask(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		email   string
		wantErr error
		want    string
	}{
		{name: "valid", id: "t1", email: "bob@example.com", want: "bob@example.com"},
		{name: "named address", id: "t1", email: "Bob <bob@example.com>", want: "bob@example.com"},
		{name: "invalid email", id: "t1", email: "not-an-email", wantErr: ErrInvalidEmail},
		{name: "empty email", id: "t1", email: "", wantErr: ErrInvalidEmail},
		{name: "unknown task", id: "missing", email: "bob@example.com", wantErr: ErrTaskNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService()
			n := &fakeNotifier{}
			s.SetNotifier(n)
			addTask(s, "t1", models.TaskStatusPending, nil)

			task, err := s.AssignTask(tt.id, tt.email)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if len(n.calls) != 0 {
					t.Errorf("notifier called on failure: %v", n.calls)
				}
				if s.tasks["t1"].AssignedTo != nil {
					t.Errorf("task modified on failure")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if task.AssignedTo == nil || *task.AssignedTo != tt.want {
				t.Errorf("assigned_to = %v, want %s", task.AssignedTo, tt.want)
			}
			if !task.UpdatedAt.Equal(fixedNow) {
				t.Errorf("updated_at = %v, want %v", task.UpdatedAt, fixedNow)
			}
			if len(n.calls) != 1 || n.calls[0] != "t1|"+tt.want {
				t.Errorf("notifier calls = %v", n.calls)
			}
		})
	}
}

func TestSetNilRestoresDefaults(t *testing.T) {
	s := NewTaskService()
	s.SetNotifier(nil)
	s.SetClock(nil)
	if s.notifier == nil || s.now == nil {
		t.Fatal("defaults not restored")
	}
}

// ListTasks must keep its pagination behavior after the helper extraction.
func TestListTasksPaginationRegression(t *testing.T) {
	s := newTestService()
	for i := 0; i < 25; i++ {
		addTask(s, fmt.Sprintf("t%02d", i), models.TaskStatusPending, nil)
	}

	resp, err := s.ListTasks(models.ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Tasks) != 20 || resp.NextPageToken != "20" || resp.TotalCount != 25 {
		t.Errorf("default page: len=%d next=%q total=%d", len(resp.Tasks), resp.NextPageToken, resp.TotalCount)
	}

	resp, _ = s.ListTasks(models.ListTasksQuery{PageSize: 20, PageToken: "20"})
	if len(resp.Tasks) != 5 || resp.NextPageToken != "" {
		t.Errorf("last page: len=%d next=%q", len(resp.Tasks), resp.NextPageToken)
	}

	resp, _ = s.ListTasks(models.ListTasksQuery{PageSize: 1000})
	if len(resp.Tasks) != 25 || resp.NextPageToken != "" {
		t.Errorf("capped page: len=%d next=%q", len(resp.Tasks), resp.NextPageToken)
	}

	resp, _ = s.ListTasks(models.ListTasksQuery{PageToken: "99"})
	if len(resp.Tasks) != 0 || resp.TotalCount != 25 {
		t.Errorf("beyond end: len=%d total=%d", len(resp.Tasks), resp.TotalCount)
	}
}
