package services

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// newEmptyService returns a service with no tasks (sample data removed).
func newEmptyService() *TaskService {
	s := NewTaskService()
	s.mu.Lock()
	s.tasks = make(map[string]*models.Task)
	s.mu.Unlock()
	return s
}

func seedN(t *testing.T, s *TaskService, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		task, err := s.CreateTask(models.CreateTaskRequest{Title: fmt.Sprintf("task-%03d", i)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, task.ID)
	}
	return ids
}

func TestListTasksPagination(t *testing.T) {
	s := newEmptyService()
	seedN(t, s, 25)
	base := models.ListTasksQuery{SortOrder: models.SortOrderCreatedAtAsc}

	tests := []struct {
		name      string
		pageSize  int
		token     string
		wantLen   int
		wantNext  string
		wantTotal int
	}{
		{"default page size when zero", 0, "", 20, "20", 25},
		{"negative page size uses default", -5, "", 20, "20", 25},
		{"explicit page size", 10, "", 10, "10", 25},
		{"clamped to 100", 500, "", 25, "", 25},
		{"second page", 10, "10", 10, "20", 25},
		{"last partial page", 10, "20", 5, "", 25},
		{"token at total", 10, "25", 0, "", 25},
		{"token beyond total", 10, "1000", 0, "", 25},
		{"invalid token starts at 0", 10, "abc", 10, "10", 25},
		{"negative token starts at 0", 10, "-3", 10, "10", 25},
		{"exact fit has no next token", 25, "", 25, "", 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := base
			q.PageSize = tt.pageSize
			q.PageToken = tt.token
			resp, err := s.ListTasks(q)
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Tasks) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(resp.Tasks), tt.wantLen)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("next = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
			if resp.TotalCount != tt.wantTotal {
				t.Errorf("total = %d, want %d", resp.TotalCount, tt.wantTotal)
			}
		})
	}
}

func TestListTasksPagesAreContiguous(t *testing.T) {
	s := newEmptyService()
	seedN(t, s, 7)
	seen := map[string]bool{}
	token := ""
	for pages := 0; pages < 10; pages++ {
		resp, err := s.ListTasks(models.ListTasksQuery{PageSize: 3, PageToken: token, SortOrder: models.SortOrderCreatedAtAsc})
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range resp.Tasks {
			if seen[task.ID] {
				t.Fatalf("duplicate task %s", task.ID)
			}
			seen[task.ID] = true
		}
		token = resp.NextPageToken
		if token == "" {
			break
		}
	}
	if len(seen) != 7 {
		t.Errorf("saw %d tasks, want 7", len(seen))
	}
}

// fakeNotifier records calls and optionally fails.
type fakeNotifier struct {
	calls    []string
	err      error
	service  *TaskService
	lockFree bool
}

func (f *fakeNotifier) Notify(task models.Task, assignee string) error {
	f.calls = append(f.calls, assignee)
	if f.service != nil {
		// If the lock were still held this would deadlock; TryLock proves it is free.
		if f.service.mu.TryLock() {
			f.lockFree = true
			f.service.mu.Unlock()
		}
	}
	return f.err
}

var fixedNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

func tp(t time.Time) *time.Time { return &t }

func putTask(s *TaskService, id string, status models.TaskStatus, due *time.Time, created time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[id] = &models.Task{ID: id, Title: id, Status: status, Priority: models.TaskPriorityMedium, DueDate: due, CreatedAt: created, Tags: []string{}}
}

func ids(r *models.ListTasksResponse) []string {
	out := []string{}
	for _, t := range r.Tasks {
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
	h := time.Hour
	type task struct {
		id      string
		status  models.TaskStatus
		due     *time.Time
		created time.Time
	}
	c0 := fixedNow.Add(-100 * h)
	tests := []struct {
		name  string
		tasks []task
		want  []string
	}{
		{"empty", nil, []string{}},
		{"past due included, future excluded", []task{
			{"past", models.TaskStatusPending, tp(fixedNow.Add(-h)), c0},
			{"future", models.TaskStatusPending, tp(fixedNow.Add(h)), c0},
		}, []string{"past"}},
		{"due exactly now is not overdue", []task{
			{"now", models.TaskStatusPending, tp(fixedNow), c0},
		}, []string{}},
		{"nil due date excluded", []task{
			{"nodue", models.TaskStatusPending, nil, c0},
			{"past", models.TaskStatusPending, tp(fixedNow.Add(-h)), c0},
		}, []string{"past"}},
		{"completed and cancelled excluded", []task{
			{"done", models.TaskStatusCompleted, tp(fixedNow.Add(-h)), c0},
			{"cancel", models.TaskStatusCancelled, tp(fixedNow.Add(-h)), c0},
			{"pending", models.TaskStatusPending, tp(fixedNow.Add(-h)), c0},
			{"prog", models.TaskStatusInProgress, tp(fixedNow.Add(-2 * h)), c0},
			{"hold", models.TaskStatusOnHold, tp(fixedNow.Add(-3 * h)), c0},
		}, []string{"hold", "prog", "pending"}},
		{"sorted by due date ascending", []task{
			{"c", models.TaskStatusPending, tp(fixedNow.Add(-1 * h)), c0},
			{"a", models.TaskStatusPending, tp(fixedNow.Add(-30 * h)), c0},
			{"b", models.TaskStatusPending, tp(fixedNow.Add(-10 * h)), c0},
		}, []string{"a", "b", "c"}},
		{"tiebreak by created_at then id", []task{
			{"z-early", models.TaskStatusPending, tp(fixedNow.Add(-h)), c0},
			{"a-late", models.TaskStatusPending, tp(fixedNow.Add(-h)), c0.Add(h)},
			{"b-late", models.TaskStatusPending, tp(fixedNow.Add(-h)), c0.Add(h)},
			{"a-late2", models.TaskStatusPending, tp(fixedNow.Add(-h)), c0.Add(h)},
		}, []string{"z-early", "a-late", "a-late2", "b-late"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEmptyService()
			s.SetClock(func() time.Time { return fixedNow })
			for _, tk := range tt.tasks {
				putTask(s, tk.id, tk.status, tk.due, tk.created)
			}
			resp, err := s.ListOverdueTasks(models.ListTasksQuery{PageSize: 20})
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(resp); !equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if resp.TotalCount != len(tt.want) {
				t.Errorf("total = %d, want %d", resp.TotalCount, len(tt.want))
			}
		})
	}
}

func TestListOverdueTasksClockChange(t *testing.T) {
	s := newEmptyService()
	putTask(s, "x", models.TaskStatusPending, tp(fixedNow.Add(time.Hour)), fixedNow)
	s.SetClock(func() time.Time { return fixedNow })
	if r, _ := s.ListOverdueTasks(models.ListTasksQuery{}); r.TotalCount != 0 {
		t.Fatalf("expected none overdue, got %d", r.TotalCount)
	}
	s.SetClock(func() time.Time { return fixedNow.Add(2 * time.Hour) })
	if r, _ := s.ListOverdueTasks(models.ListTasksQuery{}); r.TotalCount != 1 {
		t.Fatalf("expected one overdue, got %d", r.TotalCount)
	}
}

func TestListOverdueTasksPagination(t *testing.T) {
	s := newEmptyService()
	s.SetClock(func() time.Time { return fixedNow })
	for i := 0; i < 7; i++ {
		putTask(s, fmt.Sprintf("t%d", i), models.TaskStatusPending, tp(fixedNow.Add(-time.Duration(100-i)*time.Hour)), fixedNow)
	}
	tests := []struct {
		name     string
		size     int
		token    string
		want     []string
		wantNext string
	}{
		{"first page", 3, "", []string{"t0", "t1", "t2"}, "3"},
		{"second page", 3, "3", []string{"t3", "t4", "t5"}, "6"},
		{"last page", 3, "6", []string{"t6"}, ""},
		{"token beyond total", 3, "99", []string{}, ""},
		{"negative token", 3, "-1", []string{"t0", "t1", "t2"}, "3"},
		{"default size", 0, "", []string{"t0", "t1", "t2", "t3", "t4", "t5", "t6"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := s.ListOverdueTasks(models.ListTasksQuery{PageSize: tt.size, PageToken: tt.token})
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(r); !equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if r.NextPageToken != tt.wantNext {
				t.Errorf("next = %q, want %q", r.NextPageToken, tt.wantNext)
			}
			if r.TotalCount != 7 {
				t.Errorf("total = %d", r.TotalCount)
			}
		})
	}
}

func TestPaginateHelper(t *testing.T) {
	tests := []struct {
		total, size int
		token       string
		s, e        int
		next        string
	}{
		{0, 0, "", 0, 0, ""},
		{5, 2, "", 0, 2, "2"},
		{5, 2, "4", 4, 5, ""},
		{5, 2, "5", 5, 5, ""},
		{5, 2, "-9", 0, 2, "2"},
		{5, 2, "9999999999999999999999", 0, 2, "2"},
		{5, 2, "9223372036854775807", 5, 5, ""},
		{250, 1000, "", 0, 100, "100"},
	}
	for _, tt := range tests {
		s, e, n := paginate(tt.total, tt.size, tt.token)
		if s != tt.s || e != tt.e || n != tt.next {
			t.Errorf("paginate(%d,%d,%q) = %d,%d,%q want %d,%d,%q", tt.total, tt.size, tt.token, s, e, n, tt.s, tt.e, tt.next)
		}
	}
}

func TestAssignTask(t *testing.T) {
	boom := errors.New("smtp down")
	tests := []struct {
		name       string
		useRealID  bool
		email      string
		notifyErr  error
		wantErr    error
		wantNotFnd bool
		wantAssign string
		wantCalls  int
	}{
		{name: "success", useRealID: true, email: "bob@example.com", wantAssign: "bob@example.com", wantCalls: 1},
		{name: "trims whitespace", useRealID: true, email: "  bob@example.com  ", wantAssign: "bob@example.com", wantCalls: 1},
		{name: "notifier failure does not fail", useRealID: true, email: "bob@example.com", notifyErr: boom, wantAssign: "bob@example.com", wantCalls: 1},
		{name: "not found", useRealID: false, email: "bob@example.com", wantNotFnd: true},
		{name: "empty email", useRealID: true, email: "", wantErr: ErrInvalidEmail},
		{name: "blank email", useRealID: true, email: "   ", wantErr: ErrInvalidEmail},
		{name: "no at sign", useRealID: true, email: "bob", wantErr: ErrInvalidEmail},
		{name: "display name rejected", useRealID: true, email: "Bob <bob@example.com>", wantErr: ErrInvalidEmail},
		{name: "angle brackets rejected", useRealID: true, email: "<bob@example.com>", wantErr: ErrInvalidEmail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEmptyService()
			s.SetClock(func() time.Time { return fixedNow })
			n := &fakeNotifier{err: tt.notifyErr, service: s}
			s.SetNotifier(n)
			putTask(s, "task-1", models.TaskStatusPending, nil, fixedNow.Add(-time.Hour))
			id := "task-1"
			if !tt.useRealID {
				id = "missing"
			}
			got, err := s.AssignTask(id, tt.email)
			switch {
			case tt.wantNotFnd:
				if err == nil || err.Error() != "task with ID missing not found" || !IsNotFound(err) {
					t.Fatalf("expected not found, got %v", err)
				}
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected %v, got %v", tt.wantErr, err)
				}
				if IsNotFound(err) {
					t.Fatal("validation error must not be not-found")
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if got.AssignedTo == nil || *got.AssignedTo != tt.wantAssign {
					t.Errorf("assigned = %v", got.AssignedTo)
				}
				if !got.UpdatedAt.Equal(fixedNow) {
					t.Errorf("updated_at = %v, want %v", got.UpdatedAt, fixedNow)
				}
				stored, _ := s.GetTask("task-1")
				if stored.AssignedTo == nil || *stored.AssignedTo != tt.wantAssign {
					t.Errorf("stored assigned = %v", stored.AssignedTo)
				}
				if !n.lockFree {
					t.Error("notifier was called while lock held")
				}
			}
			if len(n.calls) != tt.wantCalls {
				t.Errorf("notifier calls = %d, want %d", len(n.calls), tt.wantCalls)
			}
			if tt.wantCalls == 1 && n.calls[0] != tt.wantAssign {
				t.Errorf("notified %q", n.calls[0])
			}
			if tt.wantAssign == "" {
				stored, _ := s.GetTask("task-1")
				if stored.AssignedTo != nil {
					t.Error("task must not be modified on failure")
				}
			}
		})
	}
}

func TestAssignTaskDefaultNotifierAndNil(t *testing.T) {
	s := newEmptyService()
	putTask(s, "a", models.TaskStatusPending, nil, fixedNow)
	s.SetNotifier(nil)
	s.SetClock(nil)
	if _, err := s.AssignTask("a", "x@example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentOverdueAndAssign(t *testing.T) {
	s := newEmptyService()
	s.SetClock(func() time.Time { return fixedNow })
	putTask(s, "a", models.TaskStatusPending, tp(fixedNow.Add(-time.Hour)), fixedNow)
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				s.ListOverdueTasks(models.ListTasksQuery{})
				s.AssignTask("a", "x@example.com")
			}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}
