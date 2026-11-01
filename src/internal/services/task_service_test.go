package services

import (
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

var fixedNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	calls []string // assignee per call
	tasks []models.Task
	err   error
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.calls = append(f.calls, assignee)
	f.tasks = append(f.tasks, task)
	return f.err
}

func newTestService(t *testing.T) (*TaskService, *fakeNotifier) {
	t.Helper()
	s := NewTaskService()
	n := &fakeNotifier{}
	s.SetClock(func() time.Time { return fixedNow })
	s.SetNotifier(n)
	return s, n
}

func mkTask(t *testing.T, s *TaskService, title string, due *time.Time, status models.TaskStatus) *models.Task {
	t.Helper()
	task, err := s.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if status != "" && status != models.TaskStatusPending {
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
	out := make([]string, len(tasks))
	for i, task := range tasks {
		out[i] = task.Title
	}
	return out
}

func equalStrings(a, b []string) bool {
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

func TestListOverdueTasks_Filtering(t *testing.T) {
	h := time.Hour
	tests := []struct {
		name  string
		setup func(t *testing.T, s *TaskService)
		want  []string
	}{
		{"no tasks with due dates", func(t *testing.T, s *TaskService) {}, nil},
		{"past due pending is overdue", func(t *testing.T, s *TaskService) {
			mkTask(t, s, "a", at(-h), models.TaskStatusPending)
		}, []string{"a"}},
		{"in progress and on hold are overdue", func(t *testing.T, s *TaskService) {
			mkTask(t, s, "ip", at(-2*h), models.TaskStatusInProgress)
			mkTask(t, s, "oh", at(-h), models.TaskStatusOnHold)
		}, []string{"ip", "oh"}},
		{"completed excluded", func(t *testing.T, s *TaskService) {
			mkTask(t, s, "done", at(-h), models.TaskStatusCompleted)
		}, nil},
		{"cancelled excluded", func(t *testing.T, s *TaskService) {
			mkTask(t, s, "x", at(-h), models.TaskStatusCancelled)
		}, nil},
		{"nil due date excluded", func(t *testing.T, s *TaskService) {
			mkTask(t, s, "nodue", nil, models.TaskStatusPending)
		}, nil},
		{"future due date excluded", func(t *testing.T, s *TaskService) {
			mkTask(t, s, "future", at(h), models.TaskStatusPending)
		}, nil},
		{"due exactly now is not overdue", func(t *testing.T, s *TaskService) {
			mkTask(t, s, "now", at(0), models.TaskStatusPending)
		}, nil},
		{"one nanosecond ago is overdue", func(t *testing.T, s *TaskService) {
			mkTask(t, s, "ns", at(-time.Nanosecond), models.TaskStatusPending)
		}, []string{"ns"}},
		{"mixed", func(t *testing.T, s *TaskService) {
			mkTask(t, s, "late", at(-48*h), models.TaskStatusPending)
			mkTask(t, s, "done", at(-72*h), models.TaskStatusCompleted)
			mkTask(t, s, "future", at(24*h), models.TaskStatusPending)
			mkTask(t, s, "nodue", nil, models.TaskStatusPending)
			mkTask(t, s, "later", at(-1*h), models.TaskStatusInProgress)
		}, []string{"late", "later"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestService(t)
			tt.setup(t, s)
			resp, err := s.ListOverdueTasks(20, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := titles(resp.Tasks); !equalStrings(got, tt.want) {
				t.Errorf("tasks = %v, want %v", got, tt.want)
			}
			if resp.TotalCount != len(tt.want) {
				t.Errorf("total_count = %d, want %d", resp.TotalCount, len(tt.want))
			}
			if resp.NextPageToken != "" {
				t.Errorf("unexpected next token %q", resp.NextPageToken)
			}
		})
	}
}

func TestListOverdueTasks_ClockChangesResult(t *testing.T) {
	s, _ := newTestService(t)
	mkTask(t, s, "a", at(time.Hour), models.TaskStatusPending)
	resp, _ := s.ListOverdueTasks(20, "")
	if resp.TotalCount != 0 {
		t.Fatalf("expected none overdue, got %d", resp.TotalCount)
	}
	s.SetClock(func() time.Time { return fixedNow.Add(2 * time.Hour) })
	resp, _ = s.ListOverdueTasks(20, "")
	if resp.TotalCount != 1 {
		t.Fatalf("expected 1 overdue after clock advance, got %d", resp.TotalCount)
	}
	s.SetClock(nil) // falls back to real time; 2026 fixture is before... just must not panic
	if _, err := s.ListOverdueTasks(20, ""); err != nil {
		t.Fatal(err)
	}
}

func TestListOverdueTasks_SortOrder(t *testing.T) {
	h := time.Hour
	s, _ := newTestService(t)
	mkTask(t, s, "c", at(-1*h), models.TaskStatusPending)
	mkTask(t, s, "a", at(-30*h), models.TaskStatusPending)
	mkTask(t, s, "b", at(-10*h), models.TaskStatusPending)
	resp, _ := s.ListOverdueTasks(20, "")
	if got, want := titles(resp.Tasks), []string{"a", "b", "c"}; !equalStrings(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestListOverdueTasks_IDTiebreak(t *testing.T) {
	s, _ := newTestService(t)
	same := at(-5 * time.Hour)
	var ids []string
	for i := 0; i < 8; i++ {
		ids = append(ids, mkTask(t, s, fmt.Sprintf("t%d", i), same, models.TaskStatusPending).ID)
	}
	// An earlier task must still come first.
	first := mkTask(t, s, "earlier", at(-6*time.Hour), models.TaskStatusPending)
	sort.Strings(ids)
	want := append([]string{first.ID}, ids...)

	for i := 0; i < 5; i++ { // repeat: map iteration order is random
		resp, _ := s.ListOverdueTasks(20, "")
		var got []string
		for _, task := range resp.Tasks {
			got = append(got, task.ID)
		}
		if !equalStrings(got, want) {
			t.Fatalf("iteration %d: ids = %v, want %v", i, got, want)
		}
	}
}

func TestListOverdueTasks_Pagination(t *testing.T) {
	s, _ := newTestService(t)
	for i := 0; i < 5; i++ {
		mkTask(t, s, fmt.Sprintf("t%d", i), at(-time.Duration(10-i)*time.Hour), models.TaskStatusPending)
	}
	tests := []struct {
		name      string
		size      int
		token     string
		want      []string
		wantNext  string
		wantTotal int
	}{
		{"first page", 2, "", []string{"t0", "t1"}, "2", 5},
		{"second page", 2, "2", []string{"t2", "t3"}, "4", 5},
		{"last page", 2, "4", []string{"t4"}, "", 5},
		{"exact fit", 5, "", []string{"t0", "t1", "t2", "t3", "t4"}, "", 5},
		{"token at end", 2, "5", nil, "", 5},
		{"token out of range", 2, "99", nil, "", 5},
		{"non numeric token starts at 0", 2, "abc", []string{"t0", "t1"}, "2", 5},
		{"zero size defaults to 20", 0, "", []string{"t0", "t1", "t2", "t3", "t4"}, "", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.ListOverdueTasks(tt.size, tt.token)
			if err != nil {
				t.Fatal(err)
			}
			if got := titles(resp.Tasks); !equalStrings(got, tt.want) {
				t.Errorf("tasks = %v, want %v", got, tt.want)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("next = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
			if resp.TotalCount != tt.wantTotal {
				t.Errorf("total = %d, want %d", resp.TotalCount, tt.wantTotal)
			}
		})
	}

	t.Run("walk all pages", func(t *testing.T) {
		var got []string
		token := ""
		for i := 0; i < 10; i++ {
			resp, _ := s.ListOverdueTasks(2, token)
			got = append(got, titles(resp.Tasks)...)
			if resp.NextPageToken == "" {
				break
			}
			token = resp.NextPageToken
		}
		if want := []string{"t0", "t1", "t2", "t3", "t4"}; !equalStrings(got, want) {
			t.Errorf("walked = %v, want %v", got, want)
		}
	})
}

func TestAssignTask_Success(t *testing.T) {
	s, n := newTestService(t)
	task := mkTask(t, s, "a", nil, models.TaskStatusPending)

	got, err := s.AssignTask(task.ID, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedTo == nil || *got.AssignedTo != "alice@example.com" {
		t.Fatalf("AssignedTo = %v", got.AssignedTo)
	}
	if !got.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, fixedNow)
	}
	stored, _ := s.GetTask(task.ID)
	if stored.AssignedTo == nil || *stored.AssignedTo != "alice@example.com" {
		t.Errorf("stored AssignedTo = %v", stored.AssignedTo)
	}
	if len(n.calls) != 1 || n.calls[0] != "alice@example.com" {
		t.Fatalf("notifier calls = %v, want exactly one for alice", n.calls)
	}
	if n.tasks[0].ID != task.ID {
		t.Errorf("notifier got task %s, want %s", n.tasks[0].ID, task.ID)
	}
}

func TestAssignTask_Reassign(t *testing.T) {
	s, n := newTestService(t)
	task := mkTask(t, s, "a", nil, models.TaskStatusPending)
	s.AssignTask(task.ID, "a@b.co")
	got, err := s.AssignTask(task.ID, "c@d.co")
	if err != nil || *got.AssignedTo != "c@d.co" {
		t.Fatalf("got %v, %v", got, err)
	}
	if len(n.calls) != 2 {
		t.Errorf("calls = %d, want 2", len(n.calls))
	}
}

func TestAssignTask_TrimsWhitespace(t *testing.T) {
	s, _ := newTestService(t)
	task := mkTask(t, s, "a", nil, models.TaskStatusPending)
	got, err := s.AssignTask(task.ID, "  bob@example.com ")
	if err != nil {
		t.Fatal(err)
	}
	if *got.AssignedTo != "bob@example.com" {
		t.Errorf("AssignedTo = %q", *got.AssignedTo)
	}
}

func TestAssignTask_NotFound(t *testing.T) {
	s, n := newTestService(t)
	_, err := s.AssignTask("missing", "a@b.co")
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("err = %v, want ErrTaskNotFound", err)
	}
	if errors.Is(err, ErrInvalidEmail) {
		t.Error("not-found error must not match ErrInvalidEmail")
	}
	if len(n.calls) != 0 {
		t.Errorf("notifier must not be called, got %v", n.calls)
	}
}

func TestAssignTask_InvalidEmail(t *testing.T) {
	tests := []struct{ name, email string }{
		{"empty", ""},
		{"blank", "   "},
		{"display name", "Name <a@b.c>"},
		{"bad", "bad"},
		{"missing domain", "a@"},
		{"missing local", "@b.c"},
		{"two addresses", "a@b.c, d@e.f"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, n := newTestService(t)
			task := mkTask(t, s, "a", nil, models.TaskStatusPending)
			_, err := s.AssignTask(task.ID, tt.email)
			if !errors.Is(err, ErrInvalidEmail) {
				t.Fatalf("err = %v, want ErrInvalidEmail", err)
			}
			if len(n.calls) != 0 {
				t.Errorf("notifier called: %v", n.calls)
			}
			stored, _ := s.GetTask(task.ID)
			if stored.AssignedTo != nil {
				t.Errorf("AssignedTo changed to %q", *stored.AssignedTo)
			}
		})
	}
}

func TestAssignTask_InvalidEmailTakesPrecedenceOverNotFound(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.AssignTask("missing", "bad"); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("err = %v, want ErrInvalidEmail", err)
	}
}

func TestAssignTask_NotifierFailureStillSucceeds(t *testing.T) {
	s, n := newTestService(t)
	n.err = errors.New("smtp down")
	task := mkTask(t, s, "a", nil, models.TaskStatusPending)
	got, err := s.AssignTask(task.ID, "a@b.co")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got.AssignedTo == nil || *got.AssignedTo != "a@b.co" {
		t.Errorf("AssignedTo = %v", got.AssignedTo)
	}
	if len(n.calls) != 1 {
		t.Errorf("calls = %d, want 1", len(n.calls))
	}
	stored, _ := s.GetTask(task.ID)
	if stored.AssignedTo == nil {
		t.Error("assignment not persisted")
	}
}

func TestSetNotifierNilRestoresDefault(t *testing.T) {
	s, _ := newTestService(t)
	s.SetNotifier(nil)
	task := mkTask(t, s, "a", nil, models.TaskStatusPending)
	if _, err := s.AssignTask(task.ID, "a@b.co"); err != nil {
		t.Fatal(err)
	}
}

// Regression: ListTasks must paginate over a stable ordering.
func TestListTasks_PaginationRegression(t *testing.T) {
	s, _ := newTestService(t)
	for i := 0; i < 5; i++ {
		mkTask(t, s, fmt.Sprintf("t%d", i), nil, models.TaskStatusPending)
	}
	const total = 7 // 2 sample tasks + 5

	all, err := s.ListTasks(models.ListTasksQuery{PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if all.TotalCount != total || len(all.Tasks) != total || all.NextPageToken != "" {
		t.Fatalf("all: total=%d len=%d next=%q", all.TotalCount, len(all.Tasks), all.NextPageToken)
	}

	tests := []struct {
		name     string
		size     int
		token    string
		wantLen  int
		wantNext string
	}{
		{"first page", 3, "", 3, "3"},
		{"second page", 3, "3", 3, "6"},
		{"last page", 3, "6", 1, ""},
		{"exact", 7, "", 7, ""},
		{"token at end", 3, "7", 0, ""},
		{"token out of range", 3, "100", 0, ""},
		{"default size", 0, "", 7, ""},
		{"huge size capped", 1000, "", 7, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.ListTasks(models.ListTasksQuery{PageSize: tt.size, PageToken: tt.token})
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Tasks) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(resp.Tasks), tt.wantLen)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("next = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
			if resp.TotalCount != total {
				t.Errorf("total = %d, want %d", resp.TotalCount, total)
			}
		})
	}

	t.Run("pages are stable, disjoint and cover all tasks", func(t *testing.T) {
		for rep := 0; rep < 5; rep++ {
			seen := map[string]bool{}
			var got []string
			token := ""
			for i := 0; i < 10; i++ {
				resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 2, PageToken: token})
				for _, task := range resp.Tasks {
					if seen[task.ID] {
						t.Fatalf("duplicate task %s across pages", task.ID)
					}
					seen[task.ID] = true
					got = append(got, task.ID)
				}
				if resp.NextPageToken == "" {
					break
				}
				token = resp.NextPageToken
			}
			if len(got) != total {
				t.Fatalf("walked %d tasks, want %d", len(got), total)
			}
			for i, task := range all.Tasks {
				if got[i] != task.ID {
					t.Fatalf("rep %d: walked order differs from full listing at %d", rep, i)
				}
			}
		}
	})
}
