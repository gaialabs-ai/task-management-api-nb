package services

import (
	"errors"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

var fixedNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	calls []string
	err   error
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.calls = append(f.calls, task.ID+"|"+assignee)
	return f.err
}

// newEmptyService returns a service with no sample data and a fixed clock.
func newEmptyService() *TaskService {
	s := NewTaskService()
	s.tasks = map[string]*models.Task{}
	s.SetClock(func() time.Time { return fixedNow })
	return s
}

func addTask(t *testing.T, s *TaskService, title string, due *time.Time, status models.TaskStatus) *models.Task {
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

func titles(r *models.ListTasksResponse) []string {
	var out []string
	for _, t := range r.Tasks {
		out = append(out, t.Title)
	}
	return out
}

func TestListOverdueTasks(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, s *TaskService)
		want  []string
	}{
		{"empty", func(*testing.T, *TaskService) {}, nil},
		{"nil due date excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "none", nil, "")
		}, nil},
		{"future excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "future", at(time.Hour), "")
		}, nil},
		{"due exactly now excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "now", at(0), "")
		}, nil},
		{"completed and cancelled excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "done", at(-time.Hour), models.TaskStatusCompleted)
			addTask(t, s, "cancelled", at(-time.Hour), models.TaskStatusCancelled)
		}, nil},
		{"other statuses included, sorted ascending", func(t *testing.T, s *TaskService) {
			addTask(t, s, "newer", at(-time.Hour), models.TaskStatusPending)
			addTask(t, s, "oldest", at(-72*time.Hour), models.TaskStatusOnHold)
			addTask(t, s, "middle", at(-24*time.Hour), models.TaskStatusInProgress)
		}, []string{"oldest", "middle", "newer"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEmptyService()
			tt.setup(t, s)
			resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 20})
			if err != nil {
				t.Fatal(err)
			}
			got := titles(resp)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
			if resp.TotalCount != len(tt.want) {
				t.Errorf("total_count = %d, want %d", resp.TotalCount, len(tt.want))
			}
		})
	}
}

func TestListOverdueTasksTiebreakByID(t *testing.T) {
	s := newEmptyService()
	due := at(-time.Hour)
	for i := 0; i < 5; i++ {
		addTask(t, s, "t", due, "")
	}
	resp, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 20})
	for i := 1; i < len(resp.Tasks); i++ {
		if resp.Tasks[i-1].ID >= resp.Tasks[i].ID {
			t.Fatalf("tie not ordered by ID: %s >= %s", resp.Tasks[i-1].ID, resp.Tasks[i].ID)
		}
	}
}

func TestListOverdueTasksPagination(t *testing.T) {
	s := newEmptyService()
	names := []string{"a", "b", "c", "d", "e"}
	for i, n := range names {
		addTask(t, s, n, at(-time.Duration(len(names)-i)*time.Hour), "")
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
		if resp.TotalCount != 5 {
			t.Errorf("total_count = %d", resp.TotalCount)
		}
		got = append(got, titles(resp)...)
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	if pages != 3 || len(got) != 5 || got[0] != "a" || got[4] != "e" {
		t.Fatalf("pages=%d got=%v", pages, got)
	}

	resp, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 2, PageToken: "99"})
	if len(resp.Tasks) != 0 || resp.NextPageToken != "" {
		t.Errorf("token beyond end: %+v", resp)
	}
}

func TestAssignTask(t *testing.T) {
	tests := []struct {
		name     string
		assignee string
		unknown  bool
		wantErr  error
		notNotFn bool
	}{
		{name: "success", assignee: "alice@example.com"},
		{name: "trimmed", assignee: "  bob@example.com "},
		{name: "invalid email", assignee: "not-an-email", wantErr: ErrInvalidEmail},
		{name: "display name rejected", assignee: "Alice <alice@example.com>", wantErr: ErrInvalidEmail},
		{name: "empty", assignee: "", wantErr: ErrInvalidEmail},
		{name: "unknown task", assignee: "a@example.com", unknown: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEmptyService()
			n := &fakeNotifier{}
			s.SetNotifier(n)
			task := addTask(t, s, "x", nil, "")
			id := task.ID
			if tt.unknown {
				id = "missing"
			}
			got, err := s.AssignTask(id, tt.assignee)
			switch {
			case tt.unknown:
				var nf *TaskNotFoundError
				if !errors.As(err, &nf) {
					t.Fatalf("want not found, got %v", err)
				}
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want %v, got %v", tt.wantErr, err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if got.AssignedTo == nil || *got.AssignedTo != trim(tt.assignee) {
					t.Errorf("assigned_to = %v", got.AssignedTo)
				}
				stored, _ := s.GetTask(task.ID)
				if stored.AssignedTo == nil || *stored.AssignedTo != trim(tt.assignee) {
					t.Errorf("not persisted")
				}
				if len(n.calls) != 1 {
					t.Errorf("notifier calls = %v", n.calls)
				}
				return
			}
			if len(n.calls) != 0 {
				t.Errorf("notifier should not be called: %v", n.calls)
			}
		})
	}
}

func trim(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}

func TestAssignTaskNotifierFailureIgnored(t *testing.T) {
	s := newEmptyService()
	s.SetNotifier(&fakeNotifier{err: errors.New("smtp down")})
	task := addTask(t, s, "x", nil, "")
	got, err := s.AssignTask(task.ID, "a@example.com")
	if err != nil || got == nil {
		t.Fatalf("notification failure must not fail assignment: %v", err)
	}
}
