package services

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

func fixedClock(t time.Time) Option { return WithClock(func() time.Time { return t }) }

func addTask(s *TaskService, id string, status models.TaskStatus, due *time.Time) {
	s.tasks[id] = &models.Task{ID: id, Title: id, Status: status, Priority: models.TaskPriorityMedium, CreatedAt: baseTime, DueDate: due}
}

func at(d time.Duration) *time.Time {
	t := baseTime.Add(d)
	return &t
}

func TestListOverdueTasksFiltering(t *testing.T) {
	tests := []struct {
		name   string
		status models.TaskStatus
		due    *time.Time
		want   bool
	}{
		{"pending past due", models.TaskStatusPending, at(-time.Hour), true},
		{"in progress past due", models.TaskStatusInProgress, at(-time.Hour), true},
		{"on hold past due", models.TaskStatusOnHold, at(-time.Hour), true},
		{"completed past due", models.TaskStatusCompleted, at(-time.Hour), false},
		{"cancelled past due", models.TaskStatusCancelled, at(-time.Hour), false},
		{"nil due date", models.TaskStatusPending, nil, false},
		{"future due date", models.TaskStatusPending, at(time.Hour), false},
		{"due exactly now is not overdue", models.TaskStatusPending, at(0), false},
		{"one nanosecond past due", models.TaskStatusPending, at(-time.Nanosecond), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEmptyService(fixedClock(baseTime))
			addTask(s, "x", tt.status, tt.due)
			resp, err := s.ListOverdueTasks(models.ListTasksQuery{})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(resp.Tasks) == 1; got != tt.want {
				t.Fatalf("overdue = %v, want %v", got, tt.want)
			}
			if resp.TotalCount != len(resp.Tasks) {
				t.Errorf("total = %d, len = %d", resp.TotalCount, len(resp.Tasks))
			}
		})
	}
}

func TestListOverdueTasksOrdering(t *testing.T) {
	s := newEmptyService(fixedClock(baseTime))
	addTask(s, "c", models.TaskStatusPending, at(-time.Hour))
	addTask(s, "a", models.TaskStatusPending, at(-time.Hour))
	addTask(s, "z", models.TaskStatusPending, at(-48*time.Hour))
	addTask(s, "b", models.TaskStatusPending, at(-time.Hour))
	addTask(s, "m", models.TaskStatusPending, at(-2*time.Hour))
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{})
	got := strings.Join(ids(resp.Tasks), ",")
	if want := "z,m,a,b,c"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
}

func TestListOverdueTasksFakeClock(t *testing.T) {
	now := baseTime
	s := newEmptyService(WithClock(func() time.Time { return now }))
	addTask(s, "x", models.TaskStatusPending, at(time.Hour))
	if resp, _ := s.ListOverdueTasks(models.ListTasksQuery{}); resp.TotalCount != 0 {
		t.Fatalf("expected none overdue, got %d", resp.TotalCount)
	}
	now = baseTime.Add(2 * time.Hour)
	if resp, _ := s.ListOverdueTasks(models.ListTasksQuery{}); resp.TotalCount != 1 {
		t.Fatalf("expected one overdue after clock advance, got %d", resp.TotalCount)
	}
}

func TestListOverdueTasksPagination(t *testing.T) {
	s := newEmptyService(fixedClock(baseTime))
	for i := 0; i < 7; i++ {
		addTask(s, fmt.Sprintf("t%d", i), models.TaskStatusPending, at(-time.Duration(10-i)*time.Hour))
	}
	addTask(s, "done", models.TaskStatusCompleted, at(-100*time.Hour))

	tests := []struct {
		name, token string
		size        int
		want        string
		next        string
	}{
		{"first page", "", 3, "t0,t1,t2", "3"},
		{"second page", "3", 3, "t3,t4,t5", "6"},
		{"last page", "6", 3, "t6", ""},
		{"past end", "9", 3, "", ""},
		{"default size", "", 0, "t0,t1,t2,t3,t4,t5,t6", ""},
		{"size capped", "", 1000, "t0,t1,t2,t3,t4,t5,t6", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _ := s.ListOverdueTasks(models.ListTasksQuery{PageSize: tt.size, PageToken: tt.token})
			if got := strings.Join(ids(resp.Tasks), ","); got != tt.want {
				t.Errorf("tasks = %q, want %q", got, tt.want)
			}
			if resp.NextPageToken != tt.next {
				t.Errorf("next = %q, want %q", resp.NextPageToken, tt.next)
			}
			if resp.TotalCount != 7 {
				t.Errorf("total = %d, want 7", resp.TotalCount)
			}
		})
	}
}

type fakeNotifier struct {
	mu    sync.Mutex
	calls []struct {
		task     models.Task
		assignee string
	}
	err error
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct {
		task     models.Task
		assignee string
	}{task, assignee})
	return f.err
}

func TestAssignTask(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		email   string
		wantErr error
		notify  bool
	}{
		{"valid", "t1", "alice@example.com", nil, true},
		{"valid with plus tag", "t1", "alice+x@example.co.uk", nil, true},
		{"empty email", "t1", "", ErrInvalidEmail, false},
		{"no at sign", "t1", "alice", ErrInvalidEmail, false},
		{"display name not allowed", "t1", "Alice <alice@example.com>", ErrInvalidEmail, false},
		{"surrounding space", "t1", " alice@example.com", ErrInvalidEmail, false},
		{"unknown task", "nope", "alice@example.com", ErrTaskNotFound, false},
		{"invalid email wins over unknown task", "nope", "bad", ErrInvalidEmail, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &fakeNotifier{}
			later := baseTime.Add(time.Hour)
			s := newEmptyService(WithNotifier(n), fixedClock(later))
			addTask(s, "t1", models.TaskStatusPending, nil)
			task, err := s.AssignTask(tt.id, tt.email)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if task != nil {
					t.Error("expected nil task on error")
				}
				if s.tasks["t1"].AssignedTo != nil {
					t.Error("task must not be modified on error")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if task.AssignedTo == nil || *task.AssignedTo != tt.email {
					t.Errorf("assigned_to = %v", task.AssignedTo)
				}
				if !task.UpdatedAt.Equal(later) {
					t.Errorf("updated_at = %v, want %v", task.UpdatedAt, later)
				}
				if got := s.tasks["t1"].AssignedTo; got == nil || *got != tt.email {
					t.Error("stored task not updated")
				}
			}
			if tt.notify != (len(n.calls) == 1) {
				t.Fatalf("notifier calls = %d, want called=%v", len(n.calls), tt.notify)
			}
			if tt.notify {
				if n.calls[0].assignee != tt.email || n.calls[0].task.ID != "t1" {
					t.Errorf("notifier args = %+v", n.calls[0])
				}
				if a := n.calls[0].task.AssignedTo; a == nil || *a != tt.email {
					t.Error("notifier should receive the updated task")
				}
			}
		})
	}
}

func TestAssignTaskNotFoundMessage(t *testing.T) {
	s := newEmptyService()
	_, err := s.AssignTask("abc", "a@b.com")
	if err == nil || err.Error() != "task with ID abc not found" {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.GetTask("abc"); !errors.Is(err, ErrTaskNotFound) || err.Error() != "task with ID abc not found" {
		t.Fatalf("GetTask err = %v", err)
	}
}

func TestAssignTaskNotifierFailureDoesNotRollBack(t *testing.T) {
	n := &fakeNotifier{err: errors.New("smtp down")}
	s := newEmptyService(WithNotifier(n))
	addTask(s, "t1", models.TaskStatusPending, nil)
	task, err := s.AssignTask("t1", "bob@example.com")
	if err != nil {
		t.Fatalf("assignment must succeed despite notifier failure: %v", err)
	}
	if task.AssignedTo == nil || *task.AssignedTo != "bob@example.com" {
		t.Error("returned task not assigned")
	}
	if got := s.tasks["t1"].AssignedTo; got == nil || *got != "bob@example.com" {
		t.Error("assignment rolled back")
	}
	if len(n.calls) != 1 {
		t.Errorf("notifier calls = %d", len(n.calls))
	}
}

func TestDefaultsAreInstalled(t *testing.T) {
	s := NewTaskService()
	if s.now == nil || s.notifier == nil {
		t.Fatal("defaults not set")
	}
	if len(s.tasks) != 2 {
		t.Errorf("sample data = %d tasks", len(s.tasks))
	}
	WithNotifier(nil)(s)
	WithClock(nil)(s)
	if s.now == nil || s.notifier == nil {
		t.Fatal("nil options must keep defaults")
	}
	task, err := s.AssignTask(firstID(s), "a@b.com")
	if err != nil || task == nil {
		t.Fatalf("default notifier assign failed: %v", err)
	}
}

func firstID(s *TaskService) string {
	for id := range s.tasks {
		return id
	}
	return ""
}

func TestLogNotifier(t *testing.T) {
	if err := (LogNotifier{}).NotifyAssigned(models.Task{ID: "1", Title: "x"}, "a@b.com"); err != nil {
		t.Fatal(err)
	}
}
