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

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignedTo string) error {
	f.calls = append(f.calls, task.ID+"|"+assignedTo)
	return f.err
}

func newTestService() *TaskService {
	s := NewTaskService()
	s.tasks = map[string]*models.Task{}
	s.SetClock(func() time.Time { return fixedNow })
	return s
}

func addTask(t *testing.T, s *TaskService, id string, status models.TaskStatus, due *time.Time) {
	t.Helper()
	s.tasks[id] = &models.Task{ID: id, Title: id, Status: status, Priority: models.TaskPriorityMedium, DueDate: due}
}

func at(d time.Duration) *time.Time {
	t := fixedNow.Add(d)
	return &t
}

func ids(r *models.ListTasksResponse) []string {
	var out []string
	for _, t := range r.Tasks {
		out = append(out, t.ID)
	}
	return out
}

func TestListOverdueTasks(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, s *TaskService)
		want  []string
	}{
		{"empty", func(t *testing.T, s *TaskService) {}, nil},
		{"nil due date excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "a", models.TaskStatusPending, nil)
		}, nil},
		{"future and exactly-now excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "a", models.TaskStatusPending, at(time.Hour))
			addTask(t, s, "b", models.TaskStatusPending, at(0))
		}, nil},
		{"completed and cancelled excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "a", models.TaskStatusCompleted, at(-time.Hour))
			addTask(t, s, "b", models.TaskStatusCancelled, at(-time.Hour))
			addTask(t, s, "c", models.TaskStatusOnHold, at(-time.Hour))
			addTask(t, s, "d", models.TaskStatusInProgress, at(-2*time.Hour))
		}, []string{"d", "c"}},
		{"sorted by due date then id", func(t *testing.T, s *TaskService) {
			addTask(t, s, "z", models.TaskStatusPending, at(-3*time.Hour))
			addTask(t, s, "b", models.TaskStatusPending, at(-time.Hour))
			addTask(t, s, "a", models.TaskStatusPending, at(-time.Hour))
			addTask(t, s, "m", models.TaskStatusPending, at(-48*time.Hour))
		}, []string{"m", "z", "a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService()
			tt.setup(t, s)
			got, err := s.ListOverdueTasks(models.ListTasksQuery{})
			if err != nil {
				t.Fatal(err)
			}
			g := ids(got)
			if len(g) != len(tt.want) {
				t.Fatalf("got %v want %v", g, tt.want)
			}
			for i := range g {
				if g[i] != tt.want[i] {
					t.Fatalf("got %v want %v", g, tt.want)
				}
			}
			if got.TotalCount != len(tt.want) {
				t.Errorf("total=%d want %d", got.TotalCount, len(tt.want))
			}
		})
	}
}

func TestListOverdueTasksPagination(t *testing.T) {
	s := newTestService()
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		addTask(t, s, id, models.TaskStatusPending, at(-time.Duration(10-i)*time.Hour))
	}
	r, _ := s.ListOverdueTasks(models.ListTasksQuery{PageSize: 2})
	if g := ids(r); len(g) != 2 || g[0] != "a" || g[1] != "b" || r.NextPageToken != "2" || r.TotalCount != 5 {
		t.Fatalf("page1: %v %q %d", g, r.NextPageToken, r.TotalCount)
	}
	r, _ = s.ListOverdueTasks(models.ListTasksQuery{PageSize: 2, PageToken: "2"})
	if g := ids(r); len(g) != 2 || g[0] != "c" || g[1] != "d" || r.NextPageToken != "4" {
		t.Fatalf("page2: %v %q", g, r.NextPageToken)
	}
	r, _ = s.ListOverdueTasks(models.ListTasksQuery{PageSize: 2, PageToken: "4"})
	if g := ids(r); len(g) != 1 || g[0] != "e" || r.NextPageToken != "" {
		t.Fatalf("page3: %v %q", g, r.NextPageToken)
	}
	r, _ = s.ListOverdueTasks(models.ListTasksQuery{PageSize: 2, PageToken: "99"})
	if len(r.Tasks) != 0 || r.NextPageToken != "" {
		t.Fatalf("beyond end: %v", ids(r))
	}
}

func TestListTasksPaginationRegression(t *testing.T) {
	s := newTestService()
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		s.tasks[id] = &models.Task{ID: id, CreatedAt: fixedNow.Add(time.Duration(len(id)) * time.Second), Priority: models.TaskPriorityMedium}
	}
	// distinct created_at for deterministic order
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		s.tasks[id].CreatedAt = fixedNow.Add(time.Duration(i) * time.Second)
	}
	q := models.ListTasksQuery{PageSize: 2, SortOrder: models.SortOrderCreatedAtAsc}
	r, _ := s.ListTasks(q)
	if g := ids(r); len(g) != 2 || g[0] != "a" || g[1] != "b" || r.NextPageToken != "2" || r.TotalCount != 5 {
		t.Fatalf("page1: %v %q %d", g, r.NextPageToken, r.TotalCount)
	}
	q.PageToken = "4"
	r, _ = s.ListTasks(q)
	if g := ids(r); len(g) != 1 || g[0] != "e" || r.NextPageToken != "" {
		t.Fatalf("last page: %v %q", g, r.NextPageToken)
	}
	q.PageToken = "10"
	r, _ = s.ListTasks(q)
	if len(r.Tasks) != 0 || r.NextPageToken != "" {
		t.Fatalf("beyond end: %v", ids(r))
	}
	r, _ = s.ListTasks(models.ListTasksQuery{PageSize: 500})
	if len(r.Tasks) != 5 {
		t.Fatalf("clamped page size: %d", len(r.Tasks))
	}
}

func TestAssignTask(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		email     string
		notifyErr error
		wantErr   string // "", "invalid", "notfound"
		notified  bool
	}{
		{"success", "t1", "bob@example.com", nil, "", true},
		{"notifier failure does not fail", "t1", "bob@example.com", errors.New("smtp down"), "", true},
		{"invalid email", "t1", "not-an-email", nil, "invalid", false},
		{"display name rejected", "t1", "Bob <bob@example.com>", nil, "invalid", false},
		{"empty email", "t1", "", nil, "invalid", false},
		{"unknown task", "nope", "bob@example.com", nil, "notfound", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService()
			n := &fakeNotifier{err: tt.notifyErr}
			s.SetNotifier(n)
			addTask(t, s, "t1", models.TaskStatusPending, nil)
			task, err := s.AssignTask(tt.id, tt.email)
			switch tt.wantErr {
			case "invalid":
				if !errors.Is(err, ErrInvalidEmail) {
					t.Fatalf("err=%v", err)
				}
			case "notfound":
				if err == nil || errors.Is(err, ErrInvalidEmail) {
					t.Fatalf("err=%v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if task.AssignedTo == nil || *task.AssignedTo != tt.email {
					t.Fatalf("assigned_to=%v", task.AssignedTo)
				}
				if got := s.tasks["t1"].AssignedTo; got == nil || *got != tt.email {
					t.Fatalf("stored assigned_to=%v", got)
				}
			}
			if (len(n.calls) == 1) != tt.notified {
				t.Fatalf("notifier calls=%v", n.calls)
			}
		})
	}
}
