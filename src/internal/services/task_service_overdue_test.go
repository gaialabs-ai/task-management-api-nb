package services

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

var fixedNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	mu    sync.Mutex
	calls []fakeCall
	err   error
}

type fakeCall struct {
	task     models.Task
	assignee string
}

func (f *fakeNotifier) NotifyAssignment(task models.Task, assignee string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{task, assignee})
	return f.err
}

type seed struct {
	id     string
	status models.TaskStatus
	due    *time.Time
}

func at(d time.Duration) *time.Time {
	t := fixedNow.Add(d)
	return &t
}

func newSeeded(t *testing.T, seeds []seed, opts ...Option) *TaskService {
	t.Helper()
	opts = append([]Option{WithClock(func() time.Time { return fixedNow })}, opts...)
	s := NewTaskService(opts...)
	s.mu.Lock()
	s.tasks = make(map[string]*models.Task)
	for _, sd := range seeds {
		s.tasks[sd.id] = &models.Task{ID: sd.id, Title: sd.id, Status: sd.status, DueDate: sd.due}
	}
	s.mu.Unlock()
	return s
}

func ids(tasks []models.Task) []string {
	out := []string{}
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
	p := models.TaskStatusPending
	tests := []struct {
		name  string
		seeds []seed
		want  []string
	}{
		{"past due included, future excluded", []seed{
			{"past", p, at(-time.Hour)}, {"future", p, at(time.Hour)},
		}, []string{"past"}},
		{"due exactly now is not overdue", []seed{{"now", p, at(0)}}, []string{}},
		{"nil due dates excluded", []seed{{"nil", p, nil}, {"past", p, at(-time.Minute)}}, []string{"past"}},
		{"completed and cancelled excluded", []seed{
			{"done", models.TaskStatusCompleted, at(-time.Hour)},
			{"cancelled", models.TaskStatusCancelled, at(-time.Hour)},
			{"progress", models.TaskStatusInProgress, at(-time.Hour)},
			{"hold", models.TaskStatusOnHold, at(-time.Hour)},
		}, []string{"hold", "progress"}},
		{"ascending by due date with ID tiebreak", []seed{
			{"c", p, at(-time.Hour)}, {"b", p, at(-48 * time.Hour)},
			{"a", p, at(-time.Hour)}, {"z", p, at(-72 * time.Hour)},
		}, []string{"z", "b", "a", "c"}},
		{"empty", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSeeded(t, tt.seeds)
			resp, err := s.ListOverdueTasks(models.ListTasksQuery{})
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(resp.Tasks); !equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if resp.TotalCount != len(tt.want) {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, len(tt.want))
			}
		})
	}
}

func TestListOverdueTasksUsesInjectedClock(t *testing.T) {
	s := newSeeded(t, []seed{{"t", models.TaskStatusPending, at(time.Hour)}})
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{})
	if len(resp.Tasks) != 0 {
		t.Fatal("expected none at fixed clock")
	}
	s.now = func() time.Time { return fixedNow.Add(2 * time.Hour) }
	resp, _ = s.ListOverdueTasks(models.ListTasksQuery{})
	if len(resp.Tasks) != 1 {
		t.Fatal("expected one after advancing clock")
	}
}

func TestListOverdueTasksPagination(t *testing.T) {
	var seeds []seed
	for i := 0; i < 5; i++ {
		seeds = append(seeds, seed{fmt.Sprintf("t%d", i), models.TaskStatusPending, at(-time.Duration(10-i) * time.Hour)})
	}
	tests := []struct {
		name     string
		query    models.ListTasksQuery
		want     []string
		wantNext string
	}{
		{"first page", models.ListTasksQuery{PageSize: 2}, []string{"t0", "t1"}, "2"},
		{"middle page", models.ListTasksQuery{PageSize: 2, PageToken: "2"}, []string{"t2", "t3"}, "4"},
		{"last page", models.ListTasksQuery{PageSize: 2, PageToken: "4"}, []string{"t4"}, ""},
		{"past end", models.ListTasksQuery{PageSize: 2, PageToken: "9"}, []string{}, ""},
		{"default page size", models.ListTasksQuery{}, []string{"t0", "t1", "t2", "t3", "t4"}, ""},
		{"invalid token is offset zero", models.ListTasksQuery{PageSize: 1, PageToken: "x"}, []string{"t0"}, "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSeeded(t, seeds)
			resp, err := s.ListOverdueTasks(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(resp.Tasks); !equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("next = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
			if resp.TotalCount != 5 {
				t.Errorf("TotalCount = %d, want 5", resp.TotalCount)
			}
		})
	}
}

func TestAssignTask(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		notifyErr  error
		wantErr    bool
		wantNotify bool
	}{
		{"success", "a", nil, false, true},
		{"unknown task", "missing", nil, true, false},
		{"notifier failure does not undo", "a", errors.New("smtp down"), false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &fakeNotifier{err: tt.notifyErr}
			s := newSeeded(t, []seed{{"a", models.TaskStatusPending, nil}}, WithNotifier(n))
			s.now = func() time.Time { return fixedNow.Add(time.Minute) }

			task, err := s.AssignTask(tt.id, "bob@example.com")
			if tt.wantErr {
				if err == nil || !errors.Is(err, ErrTaskNotFound) {
					t.Fatalf("want ErrTaskNotFound, got %v", err)
				}
				if want := "task with ID missing not found"; err.Error() != want {
					t.Errorf("message = %q, want %q", err.Error(), want)
				}
				if len(n.calls) != 0 {
					t.Errorf("notifier must not be called")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if task.AssignedTo == nil || *task.AssignedTo != "bob@example.com" {
				t.Errorf("returned AssignedTo = %v", task.AssignedTo)
			}
			if !task.UpdatedAt.Equal(fixedNow.Add(time.Minute)) {
				t.Errorf("UpdatedAt = %v", task.UpdatedAt)
			}
			if len(n.calls) != 1 || n.calls[0].assignee != "bob@example.com" || n.calls[0].task.ID != "a" {
				t.Fatalf("notifier calls = %+v", n.calls)
			}
			if n.calls[0].task.AssignedTo == nil || *n.calls[0].task.AssignedTo != "bob@example.com" {
				t.Errorf("notifier got task without assignment")
			}
			stored, err := s.GetTask("a")
			if err != nil || stored.AssignedTo == nil || *stored.AssignedTo != "bob@example.com" {
				t.Errorf("assignment not persisted: %v %v", stored, err)
			}
		})
	}
}

func TestNotFoundErrorsAreDistinguishable(t *testing.T) {
	s := NewTaskService()
	if _, err := s.GetTask("nope"); !errors.Is(err, ErrTaskNotFound) || err.Error() != "task with ID nope not found" {
		t.Errorf("GetTask: %v", err)
	}
	if err := s.DeleteTask("nope"); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("DeleteTask: %v", err)
	}
}

// Notifier runs outside the lock: a notifier that calls back into the service
// must not deadlock.
type reentrantNotifier struct{ s *TaskService }

func (r reentrantNotifier) NotifyAssignment(task models.Task, _ string) error {
	_, err := r.s.GetTask(task.ID)
	return err
}

func TestAssignTaskNotifierRunsOutsideLock(t *testing.T) {
	s := newSeeded(t, []seed{{"a", models.TaskStatusPending, nil}})
	s.notifier = reentrantNotifier{s}
	done := make(chan struct{})
	go func() { _, _ = s.AssignTask("a", "x@example.com"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock: notifier invoked under lock")
	}
}
