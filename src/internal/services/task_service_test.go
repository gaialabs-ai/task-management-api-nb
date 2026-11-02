package services

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

type fakeNotifier struct {
	calls []string
	err   error
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.calls = append(f.calls, task.ID+"|"+assignee)
	return f.err
}

var baseTime = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

func newTestService(n Notifier) *TaskService {
	return NewTaskService(WithClock(func() time.Time { return baseTime }), WithNotifier(n))
}

func mustCreate(t *testing.T, s *TaskService, title string, due *time.Time) *models.Task {
	t.Helper()
	task, err := s.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func at(d time.Duration) *time.Time {
	v := baseTime.Add(d)
	return &v
}

func TestListTasksUnchanged(t *testing.T) {
	s := NewTaskService()
	for i := 0; i < 25; i++ {
		mustCreate(t, s, fmt.Sprintf("t%d", i), nil)
	}
	r, _ := s.ListTasks(models.ListTasksQuery{PageSize: 10})
	if r.TotalCount != 27 || len(r.Tasks) != 10 || r.NextPageToken != "10" {
		t.Fatalf("unexpected: %d %d %q", r.TotalCount, len(r.Tasks), r.NextPageToken)
	}
	r, _ = s.ListTasks(models.ListTasksQuery{PageSize: 10, PageToken: "20"})
	if len(r.Tasks) != 7 || r.NextPageToken != "" {
		t.Fatalf("unexpected last page: %d %q", len(r.Tasks), r.NextPageToken)
	}
	r, _ = s.ListTasks(models.ListTasksQuery{PageToken: "100"})
	if len(r.Tasks) != 0 || r.TotalCount != 27 {
		t.Fatal("past-end page should be empty")
	}
	r, _ = s.ListTasks(models.ListTasksQuery{})
	if len(r.Tasks) != 20 {
		t.Fatalf("default page size, got %d", len(r.Tasks))
	}
}

func TestListOverdueTasks(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	late := mustCreate(t, s, "late", at(-48*time.Hour))
	later := mustCreate(t, s, "later", at(-time.Hour))
	mustCreate(t, s, "future", at(time.Hour))
	mustCreate(t, s, "nodue", nil)
	done := mustCreate(t, s, "done", at(-72*time.Hour))
	canc := mustCreate(t, s, "canc", at(-72*time.Hour))
	s.UpdateTaskStatus(done.ID, models.TaskStatusCompleted)
	s.UpdateTaskStatus(canc.ID, models.TaskStatusCancelled)

	r, err := s.ListOverdueTasks(models.ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if r.TotalCount != 2 || r.Tasks[0].ID != late.ID || r.Tasks[1].ID != later.ID {
		t.Fatalf("unexpected overdue: %+v", r)
	}

	// Status filter is honoured.
	r, _ = s.ListOverdueTasks(models.ListTasksQuery{Status: models.TaskStatusInProgress})
	if r.TotalCount != 0 {
		t.Fatal("status filter not applied")
	}
}

func TestListOverdueTasksTieBreakAndPagination(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	due := at(-time.Hour)
	for i := 0; i < 5; i++ {
		mustCreate(t, s, fmt.Sprintf("t%d", i), due)
	}
	var ids []string
	token := ""
	for pages := 0; pages < 10; pages++ {
		r, _ := s.ListOverdueTasks(models.ListTasksQuery{PageSize: 2, PageToken: token})
		if r.TotalCount != 5 {
			t.Fatalf("total %d", r.TotalCount)
		}
		for _, tk := range r.Tasks {
			ids = append(ids, tk.ID)
		}
		token = r.NextPageToken
		if token == "" {
			break
		}
	}
	if len(ids) != 5 {
		t.Fatalf("got %d ids", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatal("ties not sorted by ID")
		}
	}
}

func TestOverdueUsesClock(t *testing.T) {
	now := baseTime
	s := NewTaskService(WithClock(func() time.Time { return now }))
	mustCreate(t, s, "x", at(time.Hour))
	if r, _ := s.ListOverdueTasks(models.ListTasksQuery{}); r.TotalCount != 0 {
		t.Fatal("should not be overdue yet")
	}
	now = baseTime.Add(2 * time.Hour)
	if r, _ := s.ListOverdueTasks(models.ListTasksQuery{}); r.TotalCount != 1 {
		t.Fatal("should be overdue after clock advance")
	}
}

func TestAssignTask(t *testing.T) {
	n := &fakeNotifier{}
	s := newTestService(n)
	task := mustCreate(t, s, "a", nil)
	got, err := s.AssignTask(task.ID, "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
		t.Fatal("assignee not set")
	}
	if !got.UpdatedAt.Equal(baseTime) {
		t.Fatalf("UpdatedAt %v", got.UpdatedAt)
	}
	if len(n.calls) != 1 || n.calls[0] != task.ID+"|bob@example.com" {
		t.Fatalf("notifier calls: %v", n.calls)
	}
}

func TestAssignTaskErrors(t *testing.T) {
	n := &fakeNotifier{}
	s := newTestService(n)
	task := mustCreate(t, s, "a", nil)

	_, err := s.AssignTask("missing", "bob@example.com")
	if err == nil || err.Error() != "task with ID missing not found" {
		t.Fatalf("not found err: %v", err)
	}
	var nf *TaskNotFoundError
	if !errors.As(err, &nf) {
		t.Fatal("expected TaskNotFoundError")
	}

	for _, bad := range []string{"", "nope", "a@", "Bob <bob@example.com>"} {
		_, err = s.AssignTask(task.ID, bad)
		if err == nil || !strings.Contains(err.Error(), "invalid email") || !errors.Is(err, ErrInvalidEmail) {
			t.Fatalf("%q: err %v", bad, err)
		}
	}
	if len(n.calls) != 0 {
		t.Fatal("notifier must not be called on failure")
	}
	if got, _ := s.GetTask(task.ID); got.AssignedTo != nil {
		t.Fatal("task must stay unassigned")
	}
}

func TestAssignTaskNotifierFailureDoesNotFail(t *testing.T) {
	n := &fakeNotifier{err: errors.New("smtp down")}
	s := newTestService(n)
	task := mustCreate(t, s, "a", nil)
	got, err := s.AssignTask(task.ID, "bob@example.com")
	if err != nil || got.AssignedTo == nil {
		t.Fatalf("assignment should succeed: %v", err)
	}
	if len(n.calls) != 1 {
		t.Fatal("notifier should have been called")
	}
}

func TestDefaults(t *testing.T) {
	s := NewTaskService()
	if s.clock == nil || s.notifier == nil {
		t.Fatal("defaults not set")
	}
	if _, ok := s.notifier.(LogNotifier); !ok {
		t.Fatal("default notifier should be LogNotifier")
	}
}
