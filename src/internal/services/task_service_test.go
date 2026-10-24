package services

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

type fakeNotifier struct {
	calls []string
	err   error
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.calls = append(f.calls, task.ID+":"+assignee)
	return f.err
}

var fixedNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

// newEmptyService returns a service with no tasks, a fixed clock and a fake notifier.
func newEmptyService() (*TaskService, *fakeNotifier) {
	s := NewTaskService()
	s.mu.Lock()
	s.tasks = make(map[string]*models.Task)
	s.mu.Unlock()
	n := &fakeNotifier{}
	s.SetClock(func() time.Time { return fixedNow })
	s.SetNotifier(n)
	return s, n
}

func addTask(t *testing.T, s *TaskService, title string, status models.TaskStatus, due *time.Time) *models.Task {
	t.Helper()
	task, err := s.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		if _, err := s.UpdateTaskStatus(task.ID, status); err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func at(d time.Duration) *time.Time {
	v := fixedNow.Add(d)
	return &v
}

func titles(tasks []models.Task) []string {
	var out []string
	for _, t := range tasks {
		out = append(out, t.Title)
	}
	return out
}

func TestListOverdueTasksFilteringAndOrder(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, s *TaskService)
		want  []string
	}{
		{"empty", func(t *testing.T, s *TaskService) {}, nil},
		{"past pending is overdue", func(t *testing.T, s *TaskService) {
			addTask(t, s, "a", "", at(-time.Hour))
		}, []string{"a"}},
		{"future is not overdue", func(t *testing.T, s *TaskService) {
			addTask(t, s, "a", "", at(time.Hour))
		}, nil},
		{"due exactly now is not overdue", func(t *testing.T, s *TaskService) {
			addTask(t, s, "a", "", at(0))
		}, nil},
		{"nil due date excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "a", "", nil)
		}, nil},
		{"completed and cancelled excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "done", models.TaskStatusCompleted, at(-time.Hour))
			addTask(t, s, "cancelled", models.TaskStatusCancelled, at(-time.Hour))
		}, nil},
		{"in progress and on hold included", func(t *testing.T, s *TaskService) {
			addTask(t, s, "ip", models.TaskStatusInProgress, at(-2*time.Hour))
			addTask(t, s, "oh", models.TaskStatusOnHold, at(-3*time.Hour))
		}, []string{"oh", "ip"}},
		{"sorted by due date ascending", func(t *testing.T, s *TaskService) {
			addTask(t, s, "newer", "", at(-time.Hour))
			addTask(t, s, "oldest", "", at(-72*time.Hour))
			addTask(t, s, "middle", "", at(-24*time.Hour))
		}, []string{"oldest", "middle", "newer"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newEmptyService()
			tt.setup(t, s)
			resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 20})
			if err != nil {
				t.Fatal(err)
			}
			got := titles(resp.Tasks)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
			if resp.TotalCount != len(tt.want) {
				t.Fatalf("total %d want %d", resp.TotalCount, len(tt.want))
			}
		})
	}
}

func TestListOverdueTasksTiebreakByID(t *testing.T) {
	s, _ := newEmptyService()
	due := at(-time.Hour)
	for i := 0; i < 5; i++ {
		addTask(t, s, fmt.Sprintf("t%d", i), "", due)
	}
	resp, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 20})
	for i := 1; i < len(resp.Tasks); i++ {
		if resp.Tasks[i-1].ID >= resp.Tasks[i].ID {
			t.Fatalf("tiebreak by ID violated at %d", i)
		}
	}
}

func TestListOverdueTasksPagination(t *testing.T) {
	s, _ := newEmptyService()
	for i := 0; i < 5; i++ {
		addTask(t, s, fmt.Sprintf("t%d", i), "", at(-time.Duration(10-i)*time.Hour))
	}
	var got []string
	token := ""
	pages := 0
	for {
		resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 2, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		got = append(got, titles(resp.Tasks)...)
		if resp.TotalCount != 5 {
			t.Fatalf("total %d", resp.TotalCount)
		}
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	if pages != 3 || fmt.Sprint(got) != "[t0 t1 t2 t3 t4]" {
		t.Fatalf("pages=%d got=%v", pages, got)
	}

	resp, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 2, PageToken: "99"})
	if len(resp.Tasks) != 0 || resp.NextPageToken != "" {
		t.Fatalf("out-of-range token: %+v", resp)
	}
}

func TestListTasksPaginationRegression(t *testing.T) {
	s, _ := newEmptyService()
	for i := 0; i < 5; i++ {
		addTask(t, s, fmt.Sprintf("t%d", i), "", nil)
	}
	resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 2, SortOrder: models.SortOrderCreatedAtAsc})
	if len(resp.Tasks) != 2 || resp.NextPageToken != "2" || resp.TotalCount != 5 {
		t.Fatalf("page1: %+v", resp)
	}
	resp, _ = s.ListTasks(models.ListTasksQuery{PageSize: 2, PageToken: "4", SortOrder: models.SortOrderCreatedAtAsc})
	if len(resp.Tasks) != 1 || resp.NextPageToken != "" {
		t.Fatalf("last page: %+v", resp)
	}
	resp, _ = s.ListTasks(models.ListTasksQuery{PageSize: 500})
	if len(resp.Tasks) != 5 {
		t.Fatalf("clamp: %+v", resp)
	}
	resp, _ = s.ListTasks(models.ListTasksQuery{PageSize: 2, PageToken: "bogus"})
	if len(resp.Tasks) != 2 {
		t.Fatalf("bogus token should start at 0: %+v", resp)
	}
}

func TestAssignTask(t *testing.T) {
	tests := []struct {
		name      string
		email     func(id string) (string, string) // returns id, email
		wantErr   error
		wantNotif bool
		notifErr  error
	}{
		{"success", func(id string) (string, string) { return id, "alice@example.com" }, nil, true, nil},
		{"notifier failure does not fail", func(id string) (string, string) { return id, "alice@example.com" }, nil, true, errors.New("smtp down")},
		{"unknown task", func(id string) (string, string) { return "missing", "alice@example.com" }, ErrTaskNotFound, false, nil},
		{"invalid email", func(id string) (string, string) { return id, "not-an-email" }, ErrInvalidEmail, false, nil},
		{"empty email", func(id string) (string, string) { return id, "" }, ErrInvalidEmail, false, nil},
		{"display name rejected", func(id string) (string, string) { return id, "Alice <alice@example.com>" }, ErrInvalidEmail, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, n := newEmptyService()
			n.err = tt.notifErr
			task := addTask(t, s, "x", "", nil)
			id, email := tt.email(task.ID)
			got, err := s.AssignTask(id, email)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if got.AssignedTo == nil || *got.AssignedTo != email {
					t.Fatalf("assigned_to = %v", got.AssignedTo)
				}
				if !got.UpdatedAt.Equal(fixedNow) {
					t.Fatalf("updated_at = %v", got.UpdatedAt)
				}
				stored, _ := s.GetTask(task.ID)
				if stored.AssignedTo == nil || *stored.AssignedTo != email {
					t.Fatal("not persisted")
				}
			}
			if (len(n.calls) == 1) != tt.wantNotif {
				t.Fatalf("notifier calls = %v", n.calls)
			}
		})
	}
}
