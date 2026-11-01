package services

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

var fixedNow = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

func newSvc() *TaskService {
	s := NewTaskService()
	s.SetClock(func() time.Time { return fixedNow })
	return s
}

func mkTask(t *testing.T, s *TaskService, title string, due *time.Time, status models.TaskStatus) *models.Task {
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

func at(d time.Duration) *time.Time { v := fixedNow.Add(d); return &v }

func TestListOverdueFilteringAndOrder(t *testing.T) {
	s := newSvc()
	late := mkTask(t, s, "late", at(-48*time.Hour), "")
	later := mkTask(t, s, "later", at(-time.Hour), models.TaskStatusInProgress)
	mkTask(t, s, "future", at(time.Hour), "")
	mkTask(t, s, "nodue", nil, "")
	mkTask(t, s, "done", at(-72*time.Hour), models.TaskStatusCompleted)
	mkTask(t, s, "cancelled", at(-72*time.Hour), models.TaskStatusCancelled)
	mkTask(t, s, "exactly now", at(0), "")

	resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 {
		t.Fatalf("got %d/%d", len(resp.Tasks), resp.TotalCount)
	}
	if resp.Tasks[0].ID != late.ID || resp.Tasks[1].ID != later.ID {
		t.Fatalf("wrong order: %v", resp.Tasks)
	}
}

func TestListOverdueTiebreakByID(t *testing.T) {
	s := newSvc()
	due := at(-time.Hour)
	for i := 0; i < 10; i++ {
		mkTask(t, s, "tie", due, "")
	}
	for run := 0; run < 5; run++ {
		resp, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
		for i := 1; i < len(resp.Tasks); i++ {
			if resp.Tasks[i-1].ID >= resp.Tasks[i].ID {
				t.Fatalf("not sorted by ID at %d", i)
			}
		}
	}
}

func TestListOverduePagination(t *testing.T) {
	s := newSvc()
	for i := 0; i < 25; i++ {
		mkTask(t, s, "o", at(-time.Duration(i+1)*time.Hour), "")
	}
	r1, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 10})
	if len(r1.Tasks) != 10 || r1.NextPageToken != "10" || r1.TotalCount != 25 {
		t.Fatalf("page1 %d %q %d", len(r1.Tasks), r1.NextPageToken, r1.TotalCount)
	}
	r3, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 10, PageToken: "20"})
	if len(r3.Tasks) != 5 || r3.NextPageToken != "" {
		t.Fatalf("page3 %d %q", len(r3.Tasks), r3.NextPageToken)
	}
	rd, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
	if len(rd.Tasks) != 20 || rd.NextPageToken != "20" {
		t.Fatalf("default page %d %q", len(rd.Tasks), rd.NextPageToken)
	}
	rm, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 1000})
	if len(rm.Tasks) != 25 {
		t.Fatalf("max cap: %d", len(rm.Tasks))
	}
	re, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageToken: "99"})
	if len(re.Tasks) != 0 {
		t.Fatal("expected empty")
	}
}

func TestClockInjection(t *testing.T) {
	s := newSvc()
	mkTask(t, s, "x", at(time.Hour), "")
	r, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
	if r.TotalCount != 0 {
		t.Fatal("should not be overdue yet")
	}
	s.SetClock(func() time.Time { return fixedNow.Add(2 * time.Hour) })
	r, _ = s.ListOverdueTasks(models.ListOverdueTasksQuery{})
	if r.TotalCount != 1 {
		t.Fatal("should be overdue after clock advance")
	}
}

type fakeNotifier struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (f *fakeNotifier) Notify(task models.Task, assignee string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, task.ID+"|"+assignee)
	return f.err
}

func TestAssignTask(t *testing.T) {
	s := newSvc()
	n := &fakeNotifier{}
	s.SetNotifier(n)
	task := mkTask(t, s, "a", nil, "")

	got, err := s.AssignTask(task.ID, "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" || !got.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("bad result %+v", got)
	}
	stored, _ := s.GetTask(task.ID)
	if *stored.AssignedTo != "bob@example.com" {
		t.Fatal("not persisted")
	}
	if len(n.calls) != 1 || n.calls[0] != task.ID+"|bob@example.com" {
		t.Fatalf("calls %v", n.calls)
	}
}

func TestAssignTaskValidation(t *testing.T) {
	s := newSvc()
	n := &fakeNotifier{}
	s.SetNotifier(n)
	task := mkTask(t, s, "a", nil, "")

	for _, bad := range []string{"", "nope", "Bob <bob@example.com>", "a@", "@b.com", "a b@c.com"} {
		if _, err := s.AssignTask(task.ID, bad); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("%q: expected ErrInvalidEmail, got %v", bad, err)
		}
	}
	if _, err := s.AssignTask("missing", "bob@example.com"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if len(n.calls) != 0 {
		t.Fatal("notifier must not be called on failure")
	}
	stored, _ := s.GetTask(task.ID)
	if stored.AssignedTo != nil {
		t.Fatal("task mutated on invalid input")
	}
}

func TestAssignTaskNotifierFailureIgnored(t *testing.T) {
	s := newSvc()
	n := &fakeNotifier{err: errors.New("smtp down")}
	s.SetNotifier(n)
	task := mkTask(t, s, "a", nil, "")
	if _, err := s.AssignTask(task.ID, "bob@example.com"); err != nil {
		t.Fatalf("notification failure must not fail: %v", err)
	}
	if len(n.calls) != 1 {
		t.Fatal("notifier not called")
	}
}

func TestDefaultNotifierDoesNotFail(t *testing.T) {
	s := NewTaskService()
	task := mkTask(t, s, "a", nil, "")
	if _, err := s.AssignTask(task.ID, "bob@example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := newSvc()
	s.SetNotifier(&fakeNotifier{})
	task := mkTask(t, s, "a", at(-time.Hour), "")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(4)
		go func() { defer wg.Done(); s.ListOverdueTasks(models.ListOverdueTasksQuery{}) }()
		go func() { defer wg.Done(); s.AssignTask(task.ID, "bob@example.com") }()
		go func() { defer wg.Done(); s.SetClock(func() time.Time { return fixedNow }) }()
		go func() { defer wg.Done(); mkTask(t, s, "c", at(-time.Hour), "") }()
	}
	wg.Wait()
}
