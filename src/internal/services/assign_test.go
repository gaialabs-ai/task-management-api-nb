package services

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

type fakeNotifier struct {
	mu    sync.Mutex
	calls []struct {
		task models.Task
		to   string
	}
	err error
}

func (f *fakeNotifier) NotifyAssignment(task models.Task, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct {
		task models.Task
		to   string
	}{task, to})
	return f.err
}

func TestAssignTask_Success(t *testing.T) {
	n := &fakeNotifier{}
	later := baseTime.Add(time.Hour)
	s := NewTaskService(WithNotifier(n), WithClock(fixedClock(later)))
	seedTask(s, "a", models.TaskStatusPending, nil, 0)

	got, err := s.AssignTask("a", "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
		t.Fatalf("assigned_to %v", got.AssignedTo)
	}
	if !got.UpdatedAt.Equal(later) {
		t.Fatalf("updated_at %v", got.UpdatedAt)
	}
	if len(n.calls) != 1 || n.calls[0].to != "bob@example.com" || n.calls[0].task.ID != "a" {
		t.Fatalf("calls %+v", n.calls)
	}
	if n.calls[0].task.AssignedTo == nil || *n.calls[0].task.AssignedTo != "bob@example.com" {
		t.Fatal("notifier should see the updated task")
	}
	// stored task updated, and the returned value is a copy
	if stored := s.tasks["a"]; stored == got || *stored.AssignedTo != "bob@example.com" {
		t.Fatal("stored task not updated or pointer shared")
	}
	*got.AssignedTo = "mutated@example.com"
	if *s.tasks["a"].AssignedTo != "bob@example.com" {
		t.Fatal("returned AssignedTo aliases stored value")
	}
}

func TestAssignTask_InvalidEmail(t *testing.T) {
	n := &fakeNotifier{}
	s := NewTaskService(WithNotifier(n))
	seedTask(s, "a", models.TaskStatusPending, nil, 0)
	for _, email := range []string{"", "   ", "nope", "a@", "@b.com", "Bob <bob@example.com>", " bob@example.com", "bob@example.com ", "a@b.com, c@d.com"} {
		_, err := s.AssignTask("a", email)
		if !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("%q: err=%v", email, err)
		}
	}
	if len(n.calls) != 0 || s.tasks["a"].AssignedTo != nil {
		t.Fatal("invalid email must not assign or notify")
	}
}

func TestAssignTask_NotFound(t *testing.T) {
	n := &fakeNotifier{}
	s := NewTaskService(WithNotifier(n))
	_, err := s.AssignTask("missing", "bob@example.com")
	if err == nil || err.Error() != "task with ID missing not found" || errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("err=%v", err)
	}
	if len(n.calls) != 0 {
		t.Fatal("notified for unknown task")
	}
}

func TestAssignTask_NotifierErrorTolerated(t *testing.T) {
	n := &fakeNotifier{err: errors.New("smtp down")}
	s := NewTaskService(WithNotifier(n))
	seedTask(s, "a", models.TaskStatusPending, nil, 0)
	got, err := s.AssignTask("a", "bob@example.com")
	if err != nil || got == nil || *got.AssignedTo != "bob@example.com" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if len(n.calls) != 1 {
		t.Fatalf("calls %d", len(n.calls))
	}
	if *s.tasks["a"].AssignedTo != "bob@example.com" {
		t.Fatal("assignment must persist despite notifier failure")
	}
}

func TestAssignTask_DefaultNotifierLogs(t *testing.T) {
	s := NewTaskService()
	seedTask(s, "a", models.TaskStatusPending, nil, 0)
	if _, err := s.AssignTask("a", "bob@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := (LogNotifier{}).NotifyAssignment(models.Task{}, "x@y.z"); err != nil {
		t.Fatal(err)
	}
}

// Exercised under -race: concurrent assign, overdue listing and list calls.
func TestAssignTask_ConcurrentWithReads(t *testing.T) {
	n := &fakeNotifier{}
	s := newEmptyService()
	s.notifier = n
	s.now = fixedClock(baseTime.Add(time.Hour))
	seedTask(s, "a", models.TaskStatusPending, timePtr(baseTime), 0)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); s.AssignTask("a", "bob@example.com") }()
		go func() { defer wg.Done(); s.ListOverdueTasks(models.ListTasksQuery{}) }()
		go func() { defer wg.Done(); s.ListTasks(models.ListTasksQuery{}) }()
	}
	wg.Wait()
	if len(n.calls) != 20 {
		t.Fatalf("calls %d", len(n.calls))
	}
}
