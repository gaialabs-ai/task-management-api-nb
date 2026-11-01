package services

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

var fixedNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	calls []string
	err   error
}

func (f *fakeNotifier) NotifyAssignment(task models.Task, assignee string) error {
	f.calls = append(f.calls, task.ID+"|"+assignee)
	return f.err
}

// newEmptyService returns a service without sample data and a fixed clock.
func newEmptyService(t *testing.T) *TaskService {
	t.Helper()
	s := NewTaskService()
	for id := range s.tasks {
		delete(s.tasks, id)
	}
	s.SetClock(func() time.Time { return fixedNow })
	return s
}

func addTask(t *testing.T, s *TaskService, title string, due *time.Time, status models.TaskStatus) *models.Task {
	t.Helper()
	task, err := s.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if status != "" {
		if _, err := s.UpdateTaskStatus(task.ID, status); err != nil {
			t.Fatalf("UpdateTaskStatus: %v", err)
		}
	}
	return task
}

func at(d time.Duration) *time.Time {
	v := fixedNow.Add(d)
	return &v
}

func titles(tasks []models.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.Title)
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

func TestListOverdueTasks_FilterAndSort(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *TaskService)
		want  []string
	}{
		{
			name: "past due pending included, sorted ascending",
			setup: func(t *testing.T, s *TaskService) {
				addTask(t, s, "newer", at(-1*time.Hour), "")
				addTask(t, s, "oldest", at(-48*time.Hour), models.TaskStatusInProgress)
				addTask(t, s, "middle", at(-24*time.Hour), models.TaskStatusOnHold)
			},
			want: []string{"oldest", "middle", "newer"},
		},
		{
			name: "completed and cancelled excluded",
			setup: func(t *testing.T, s *TaskService) {
				addTask(t, s, "done", at(-2*time.Hour), models.TaskStatusCompleted)
				addTask(t, s, "cancelled", at(-2*time.Hour), models.TaskStatusCancelled)
				addTask(t, s, "open", at(-2*time.Hour), "")
			},
			want: []string{"open"},
		},
		{
			name: "nil and future due dates excluded",
			setup: func(t *testing.T, s *TaskService) {
				addTask(t, s, "no due", nil, "")
				addTask(t, s, "future", at(time.Hour), "")
				addTask(t, s, "exactly now", at(0), "")
				addTask(t, s, "past", at(-time.Second), "")
			},
			want: []string{"past"},
		},
		{
			name:  "no tasks",
			setup: func(*testing.T, *TaskService) {},
			want:  []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEmptyService(t)
			tt.setup(t, s)
			resp, err := s.ListOverdueTasks(20, "")
			if err != nil {
				t.Fatal(err)
			}
			if resp.Tasks == nil {
				t.Fatal("Tasks should be non-nil")
			}
			if got := titles(resp.Tasks); !equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if resp.TotalCount != len(tt.want) {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, len(tt.want))
			}
		})
	}
}

func TestListOverdueTasks_TiebreakDeterministic(t *testing.T) {
	s := newEmptyService(t)
	due := at(-time.Hour)
	for i := 0; i < 5; i++ {
		addTask(t, s, fmt.Sprintf("t%d", i), due, "")
	}
	first, _ := s.ListOverdueTasks(20, "")
	for i := 0; i < 20; i++ {
		again, _ := s.ListOverdueTasks(20, "")
		if !equal(titles(first.Tasks), titles(again.Tasks)) {
			t.Fatalf("order changed: %v vs %v", titles(first.Tasks), titles(again.Tasks))
		}
	}
	if want := []string{"t0", "t1", "t2", "t3", "t4"}; !equal(titles(first.Tasks), want) {
		// created_at ties fall back to ID, so only assert order when timestamps differ.
		for i := 1; i < len(first.Tasks); i++ {
			a, b := first.Tasks[i-1], first.Tasks[i]
			if b.CreatedAt.Before(a.CreatedAt) || (b.CreatedAt.Equal(a.CreatedAt) && b.ID < a.ID) {
				t.Errorf("tiebreak violated between %s and %s", a.Title, b.Title)
			}
		}
	}
}

func TestListOverdueTasks_Pagination(t *testing.T) {
	s := newEmptyService(t)
	for i := 0; i < 5; i++ {
		addTask(t, s, fmt.Sprintf("t%d", i), at(-time.Duration(10-i)*time.Hour), "")
	}

	p1, _ := s.ListOverdueTasks(2, "")
	if !equal(titles(p1.Tasks), []string{"t0", "t1"}) || p1.NextPageToken != "2" || p1.TotalCount != 5 {
		t.Fatalf("page 1 unexpected: %v token=%q total=%d", titles(p1.Tasks), p1.NextPageToken, p1.TotalCount)
	}
	p2, _ := s.ListOverdueTasks(2, p1.NextPageToken)
	if !equal(titles(p2.Tasks), []string{"t2", "t3"}) || p2.NextPageToken != "4" {
		t.Fatalf("page 2 unexpected: %v token=%q", titles(p2.Tasks), p2.NextPageToken)
	}
	p3, _ := s.ListOverdueTasks(2, p2.NextPageToken)
	if !equal(titles(p3.Tasks), []string{"t4"}) || p3.NextPageToken != "" {
		t.Fatalf("page 3 unexpected: %v token=%q", titles(p3.Tasks), p3.NextPageToken)
	}
	beyond, _ := s.ListOverdueTasks(2, "99")
	if len(beyond.Tasks) != 0 || beyond.NextPageToken != "" {
		t.Fatalf("beyond range unexpected: %+v", beyond)
	}
}

func TestListTasks_PaginationRegression(t *testing.T) {
	s := newEmptyService(t)
	for i := 0; i < 5; i++ {
		addTask(t, s, fmt.Sprintf("t%d", i), nil, "")
	}
	q := models.ListTasksQuery{PageSize: 2, SortOrder: models.SortOrderCreatedAtAsc}
	p1, _ := s.ListTasks(q)
	if !equal(titles(p1.Tasks), []string{"t0", "t1"}) || p1.NextPageToken != "2" || p1.TotalCount != 5 {
		t.Fatalf("page 1: %v %q %d", titles(p1.Tasks), p1.NextPageToken, p1.TotalCount)
	}
	q.PageToken = p1.NextPageToken
	p2, _ := s.ListTasks(q)
	if !equal(titles(p2.Tasks), []string{"t2", "t3"}) || p2.NextPageToken != "4" {
		t.Fatalf("page 2: %v %q", titles(p2.Tasks), p2.NextPageToken)
	}
	q.PageToken = p2.NextPageToken
	p3, _ := s.ListTasks(q)
	if !equal(titles(p3.Tasks), []string{"t4"}) || p3.NextPageToken != "" {
		t.Fatalf("page 3: %v %q", titles(p3.Tasks), p3.NextPageToken)
	}
	q.PageSize = 1000
	q.PageToken = ""
	all, _ := s.ListTasks(q)
	if len(all.Tasks) != 5 {
		t.Fatalf("clamped page size: got %d tasks", len(all.Tasks))
	}
	q.PageSize = 0
	q.PageToken = "bogus"
	def, _ := s.ListTasks(q)
	if len(def.Tasks) != 5 {
		t.Fatalf("default page size / invalid token: got %d tasks", len(def.Tasks))
	}
}

func TestAssignTask(t *testing.T) {
	tests := []struct {
		name      string
		email     string
		unknownID bool
		notifyErr error
		wantErr   error
		wantCalls int
	}{
		{name: "success", email: "alice@example.com", wantCalls: 1},
		{name: "notifier failure does not fail", email: "alice@example.com", notifyErr: errors.New("smtp down"), wantCalls: 1},
		{name: "unknown task", email: "alice@example.com", unknownID: true, wantErr: ErrTaskNotFound},
		{name: "empty email", email: "", wantErr: ErrInvalidEmail},
		{name: "not an email", email: "alice", wantErr: ErrInvalidEmail},
		{name: "display name rejected", email: "Alice <alice@example.com>", wantErr: ErrInvalidEmail},
		{name: "invalid email on unknown task validates first", email: "nope", unknownID: true, wantErr: ErrInvalidEmail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEmptyService(t)
			n := &fakeNotifier{err: tt.notifyErr}
			s.SetNotifier(n)
			task := addTask(t, s, "work", nil, "")
			id := task.ID
			if tt.unknownID {
				id = "missing"
			}
			got, err := s.AssignTask(id, tt.email)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if len(n.calls) != 0 {
					t.Errorf("notifier called on failure: %v", n.calls)
				}
				if stored, _ := s.GetTask(task.ID); stored.AssignedTo != nil {
					t.Errorf("task modified on failure")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.AssignedTo == nil || *got.AssignedTo != tt.email {
				t.Errorf("AssignedTo = %v", got.AssignedTo)
			}
			if !got.UpdatedAt.Equal(fixedNow) {
				t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, fixedNow)
			}
			if stored, _ := s.GetTask(id); stored.AssignedTo == nil || *stored.AssignedTo != tt.email {
				t.Errorf("assignment not persisted")
			}
			if len(n.calls) != tt.wantCalls || n.calls[0] != id+"|"+tt.email {
				t.Errorf("notifier calls = %v", n.calls)
			}
		})
	}
}

func TestLogNotifier(t *testing.T) {
	if err := (LogNotifier{}).NotifyAssignment(models.Task{ID: "x", Title: "y"}, "a@b.co"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentOverdueAndAssign(t *testing.T) {
	s := newEmptyService(t)
	task := addTask(t, s, "work", at(-time.Hour), "")
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				s.ListOverdueTasks(20, "")
				s.AssignTask(task.ID, "a@example.com")
			}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}
