package services

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

var testNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

type notifyCall struct {
	task     models.Task
	assignee string
}

type fakeNotifier struct {
	mu    sync.Mutex
	calls []notifyCall
	err   error
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, notifyCall{task: task, assignee: assignee})
	return f.err
}

func (f *fakeNotifier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newTestService(n Notifier) *TaskService {
	return NewTaskService(WithClock(func() time.Time { return testNow }), WithNotifier(n))
}

func timePtr(t time.Time) *time.Time { return &t }

func mustCreate(t *testing.T, s *TaskService, title string, due *time.Time) *models.Task {
	t.Helper()
	task, err := s.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatalf("CreateTask(%q): %v", title, err)
	}
	return task
}

func mustSetStatus(t *testing.T, s *TaskService, id string, st models.TaskStatus) {
	t.Helper()
	if _, err := s.UpdateTaskStatus(id, st); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
}

func titles(tasks []models.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.Title)
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

// ---- ListTasks characterization tests ----

func TestListTasksPaginationCharacterization(t *testing.T) {
	// 2 sample tasks are created by the constructor.
	const extra = 128
	const total = extra + 2

	tests := []struct {
		name      string
		pageSize  int
		token     string
		wantLen   int
		wantToken string
	}{
		{"default page size when zero", 0, "", 20, "20"},
		{"negative page size uses default", -5, "", 20, "20"},
		{"explicit page size", 10, "", 10, "10"},
		{"page size capped at 100", 500, "", 100, "100"},
		{"second page via token", 20, "20", 20, "40"},
		{"last page has no next token", 50, "100", 30, ""},
		{"invalid token starts at zero", 10, "abc", 10, "10"},
		{"empty token starts at zero", 10, "", 10, "10"},
		{"token at total yields empty page", 10, strconv.Itoa(total), 0, ""},
		{"token beyond total yields empty page", 10, "100000", 0, ""},
		{"page size at cap has next token", 100, "", 100, "100"},
	}

	s := NewTaskService()
	for i := 0; i < extra; i++ {
		mustCreate(t, s, fmt.Sprintf("task %03d", i), nil)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := s.ListTasks(models.ListTasksQuery{PageSize: tc.pageSize, PageToken: tc.token})
			if err != nil {
				t.Fatalf("ListTasks: %v", err)
			}
			if len(resp.Tasks) != tc.wantLen {
				t.Errorf("len(tasks) = %d, want %d", len(resp.Tasks), tc.wantLen)
			}
			if resp.NextPageToken != tc.wantToken {
				t.Errorf("next token = %q, want %q", resp.NextPageToken, tc.wantToken)
			}
			if resp.TotalCount != total {
				t.Errorf("total = %d, want %d", resp.TotalCount, total)
			}
		})
	}
}

func TestListTasksWalksAllPages(t *testing.T) {
	s := NewTaskService()
	for i := 0; i < 45; i++ {
		mustCreate(t, s, fmt.Sprintf("task %02d", i), nil)
	}

	token := ""
	pages := 0
	for {
		resp, err := s.ListTasks(models.ListTasksQuery{PageSize: 10, PageToken: token, SortOrder: models.SortOrderPriorityAsc})
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		pages++
		if resp.TotalCount != 47 {
			t.Fatalf("total = %d, want 47", resp.TotalCount)
		}
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
		if pages > 10 {
			t.Fatal("too many pages")
		}
	}
	if pages != 5 {
		t.Errorf("pages = %d, want 5", pages)
	}
}

func TestListTasksFiltersAndSortCharacterization(t *testing.T) {
	s := NewTaskService()
	alice := "alice@example.com"
	_, _ = s.CreateTask(models.CreateTaskRequest{Title: "a", Tags: []string{"x", "y"}, AssignedTo: &alice, DueDate: timePtr(testNow.Add(2 * time.Hour))})
	b, _ := s.CreateTask(models.CreateTaskRequest{Title: "b", Tags: []string{"x"}, DueDate: timePtr(testNow.Add(time.Hour))})
	mustSetStatus(t, s, b.ID, models.TaskStatusInProgress)

	tests := []struct {
		name  string
		query models.ListTasksQuery
		want  []string // titles that must be present (and only these) among non-sample tasks
	}{
		{"status filter", models.ListTasksQuery{Status: models.TaskStatusInProgress}, []string{"b"}},
		{"assignee filter", models.ListTasksQuery{AssignedTo: alice}, []string{"a"}},
		{"tags filter all", models.ListTasksQuery{Tags: "x,y"}, []string{"a"}},
		{"tags filter single", models.ListTasksQuery{Tags: "x"}, []string{"a", "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := s.ListTasks(tc.query)
			got := titles(resp.Tasks)
			sort.Strings(got)
			if !equalStrings(got, tc.want) {
				t.Errorf("titles = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("due date ascending puts nil due dates last", func(t *testing.T) {
		resp, _ := s.ListTasks(models.ListTasksQuery{SortOrder: models.SortOrderDueDateAsc})
		got := titles(resp.Tasks)
		if len(got) != 4 || got[0] != "b" || got[1] != "a" {
			t.Errorf("titles = %v, want b, a first", got)
		}
	})
}

// ---- ListOverdueTasks ----

func TestListOverdueTasksFiltering(t *testing.T) {
	tests := []struct {
		name   string
		due    *time.Time
		status models.TaskStatus
		want   bool
	}{
		{"nil due date excluded", nil, models.TaskStatusPending, false},
		{"past due pending included", timePtr(testNow.Add(-time.Hour)), models.TaskStatusPending, true},
		{"past due in progress included", timePtr(testNow.Add(-time.Hour)), models.TaskStatusInProgress, true},
		{"past due on hold included", timePtr(testNow.Add(-time.Hour)), models.TaskStatusOnHold, true},
		{"past due completed excluded", timePtr(testNow.Add(-time.Hour)), models.TaskStatusCompleted, false},
		{"past due cancelled excluded", timePtr(testNow.Add(-time.Hour)), models.TaskStatusCancelled, false},
		{"due exactly now excluded", timePtr(testNow), models.TaskStatusPending, false},
		{"due one nanosecond ago included", timePtr(testNow.Add(-time.Nanosecond)), models.TaskStatusPending, true},
		{"future due excluded", timePtr(testNow.Add(time.Hour)), models.TaskStatusPending, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestService(&fakeNotifier{})
			task := mustCreate(t, s, "t", tc.due)
			if tc.status != models.TaskStatusPending {
				mustSetStatus(t, s, task.ID, tc.status)
			}

			resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
			if err != nil {
				t.Fatalf("ListOverdueTasks: %v", err)
			}
			got := len(resp.Tasks) == 1 && resp.Tasks[0].ID == task.ID
			if got != tc.want {
				t.Errorf("overdue = %v, want %v (tasks=%v)", got, tc.want, titles(resp.Tasks))
			}
			wantTotal := 0
			if tc.want {
				wantTotal = 1
			}
			if resp.TotalCount != wantTotal {
				t.Errorf("total = %d, want %d", resp.TotalCount, wantTotal)
			}
		})
	}
}

func TestListOverdueTasksUsesInjectedClock(t *testing.T) {
	current := testNow
	s := NewTaskService(WithClock(func() time.Time { return current }))
	mustCreate(t, s, "t", timePtr(testNow.Add(time.Hour)))

	resp, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
	if resp.TotalCount != 0 {
		t.Fatalf("total = %d, want 0 before clock advances", resp.TotalCount)
	}
	current = testNow.Add(2 * time.Hour)
	resp, _ = s.ListOverdueTasks(models.ListOverdueTasksQuery{})
	if resp.TotalCount != 1 {
		t.Fatalf("total = %d, want 1 after clock advances", resp.TotalCount)
	}
}

func TestListOverdueTasksEmptyReturnsEmptySlice(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Tasks == nil || len(resp.Tasks) != 0 || resp.NextPageToken != "" || resp.TotalCount != 0 {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestListOverdueTasksOrdering(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	mustCreate(t, s, "c", timePtr(testNow.Add(-1*time.Hour)))
	mustCreate(t, s, "a", timePtr(testNow.Add(-3*time.Hour)))
	mustCreate(t, s, "b", timePtr(testNow.Add(-2*time.Hour)))

	resp, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
	if got := titles(resp.Tasks); !equalStrings(got, []string{"a", "b", "c"}) {
		t.Errorf("order = %v, want [a b c]", got)
	}
}

func TestListOverdueTasksTieBreakByID(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	due := testNow.Add(-time.Hour)
	var ids []string
	for i := 0; i < 8; i++ {
		ids = append(ids, mustCreate(t, s, fmt.Sprintf("t%d", i), timePtr(due)).ID)
	}
	earlier := mustCreate(t, s, "earlier", timePtr(due.Add(-time.Hour)))
	sort.Strings(ids)
	want := append([]string{earlier.ID}, ids...)

	resp, _ := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
	if len(resp.Tasks) != len(want) {
		t.Fatalf("len = %d, want %d", len(resp.Tasks), len(want))
	}
	for i, task := range resp.Tasks {
		if task.ID != want[i] {
			t.Errorf("position %d: got %s, want %s", i, task.ID, want[i])
		}
	}
}

func TestListOverdueTasksPagination(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	for i := 0; i < 25; i++ {
		// Increasing due dates: t00 is the oldest.
		mustCreate(t, s, fmt.Sprintf("t%02d", i), timePtr(testNow.Add(-time.Duration(100-i)*time.Hour)))
	}

	tests := []struct {
		name      string
		query     models.ListOverdueTasksQuery
		wantFirst string
		wantLen   int
		wantToken string
	}{
		{"default page size", models.ListOverdueTasksQuery{}, "t00", 20, "20"},
		{"explicit page size", models.ListOverdueTasksQuery{PageSize: 10}, "t00", 10, "10"},
		{"second page", models.ListOverdueTasksQuery{PageSize: 10, PageToken: "10"}, "t10", 10, "20"},
		{"last page", models.ListOverdueTasksQuery{PageSize: 10, PageToken: "20"}, "t20", 5, ""},
		{"invalid token starts at zero", models.ListOverdueTasksQuery{PageSize: 5, PageToken: "zzz"}, "t00", 5, "5"},
		{"token beyond total", models.ListOverdueTasksQuery{PageSize: 5, PageToken: "99"}, "", 0, ""},
		{"page size capped at 100", models.ListOverdueTasksQuery{PageSize: 1000}, "t00", 25, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := s.ListOverdueTasks(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Tasks) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(resp.Tasks), tc.wantLen)
			}
			if tc.wantLen > 0 && resp.Tasks[0].Title != tc.wantFirst {
				t.Errorf("first = %s, want %s", resp.Tasks[0].Title, tc.wantFirst)
			}
			if resp.NextPageToken != tc.wantToken {
				t.Errorf("token = %q, want %q", resp.NextPageToken, tc.wantToken)
			}
			if resp.TotalCount != 25 {
				t.Errorf("total = %d, want 25", resp.TotalCount)
			}
		})
	}
}

// ---- AssignTask ----

func TestAssignTask(t *testing.T) {
	t.Run("assigns, updates timestamp and notifies", func(t *testing.T) {
		n := &fakeNotifier{}
		s := newTestService(n)
		task := mustCreate(t, s, "t", nil)

		got, err := s.AssignTask(task.ID, "bob@example.com")
		if err != nil {
			t.Fatalf("AssignTask: %v", err)
		}
		if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
			t.Errorf("AssignedTo = %v", got.AssignedTo)
		}
		if !got.UpdatedAt.Equal(testNow) {
			t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, testNow)
		}
		stored, _ := s.GetTask(task.ID)
		if stored.AssignedTo == nil || *stored.AssignedTo != "bob@example.com" {
			t.Errorf("stored AssignedTo = %v", stored.AssignedTo)
		}
		if n.callCount() != 1 {
			t.Fatalf("notifier calls = %d, want 1", n.callCount())
		}
		call := n.calls[0]
		if call.assignee != "bob@example.com" || call.task.ID != task.ID {
			t.Errorf("unexpected notify call: %+v", call)
		}
		if call.task.AssignedTo == nil || *call.task.AssignedTo != "bob@example.com" {
			t.Errorf("notified task not updated: %+v", call.task)
		}
	})

	t.Run("reassign replaces assignee", func(t *testing.T) {
		n := &fakeNotifier{}
		s := newTestService(n)
		task := mustCreate(t, s, "t", nil)
		_, _ = s.AssignTask(task.ID, "a@example.com")
		got, err := s.AssignTask(task.ID, "b@example.com")
		if err != nil || *got.AssignedTo != "b@example.com" {
			t.Fatalf("got %v, err %v", got, err)
		}
		if n.callCount() != 2 {
			t.Errorf("notifier calls = %d, want 2", n.callCount())
		}
	})

	t.Run("failing notifier does not roll back", func(t *testing.T) {
		n := &fakeNotifier{err: errors.New("smtp down")}
		s := newTestService(n)
		task := mustCreate(t, s, "t", nil)

		got, err := s.AssignTask(task.ID, "bob@example.com")
		if err != nil {
			t.Fatalf("AssignTask returned error: %v", err)
		}
		if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
			t.Errorf("AssignedTo = %v", got.AssignedTo)
		}
		stored, _ := s.GetTask(task.ID)
		if stored.AssignedTo == nil || *stored.AssignedTo != "bob@example.com" {
			t.Errorf("assignment rolled back: %v", stored.AssignedTo)
		}
		if n.callCount() != 1 {
			t.Errorf("notifier calls = %d, want 1", n.callCount())
		}
	})

	t.Run("notifier may call back into the service", func(t *testing.T) {
		s := newTestService(&fakeNotifier{})
		task := mustCreate(t, s, "t", nil)
		s.notifier = notifierFunc(func(models.Task, string) error {
			_, err := s.GetTask(task.ID)
			if err == nil {
				// Write lock must not be held during notification.
				_, err = s.UpdateTaskStatus(task.ID, models.TaskStatusInProgress)
			}
			return err
		})
		if _, err := s.AssignTask(task.ID, "bob@example.com"); err != nil {
			t.Fatalf("AssignTask: %v", err)
		}
	})
}

type notifierFunc func(models.Task, string) error

func (f notifierFunc) NotifyAssigned(task models.Task, assignee string) error {
	return f(task, assignee)
}

func TestAssignTaskInvalidEmail(t *testing.T) {
	tests := []struct {
		name  string
		email string
	}{
		{"empty", ""},
		{"no at sign", "bob"},
		{"missing domain", "bob@"},
		{"missing local part", "@example.com"},
		{"display name", "Bob <bob@example.com>"},
		{"surrounding spaces", " bob@example.com "},
		{"two addresses", "a@example.com, b@example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := &fakeNotifier{}
			s := newTestService(n)
			task := mustCreate(t, s, "t", nil)

			got, err := s.AssignTask(task.ID, tc.email)
			if err == nil || got != nil {
				t.Fatalf("expected error, got task=%v err=%v", got, err)
			}
			if !IsValidation(err) || IsNotFound(err) {
				t.Errorf("error %v should be a validation error", err)
			}
			if n.callCount() != 0 {
				t.Errorf("notifier called for invalid email")
			}
			stored, _ := s.GetTask(task.ID)
			if stored.AssignedTo != nil {
				t.Errorf("task assigned despite invalid email")
			}
		})
	}
}

func TestAssignTaskUnknownTask(t *testing.T) {
	n := &fakeNotifier{}
	s := newTestService(n)

	got, err := s.AssignTask("missing-id", "bob@example.com")
	if err == nil || got != nil {
		t.Fatalf("expected error, got task=%v err=%v", got, err)
	}
	if !IsNotFound(err) || IsValidation(err) {
		t.Errorf("error %v should be a not-found error", err)
	}
	if want := "task with ID missing-id not found"; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if n.callCount() != 0 {
		t.Errorf("notifier called for unknown task")
	}
}

func TestNewTaskServiceDefaults(t *testing.T) {
	s := NewTaskService()
	if s.now == nil || s.notifier == nil {
		t.Fatal("defaults not set")
	}
	// Nil options are ignored.
	s = NewTaskService(WithClock(nil), WithNotifier(nil))
	if s.now == nil || s.notifier == nil {
		t.Fatal("nil options must keep defaults")
	}
	// Default (logging) notifier must work.
	task := mustCreate(t, s, "t", nil)
	if _, err := s.AssignTask(task.ID, "bob@example.com"); err != nil {
		t.Fatalf("AssignTask with default notifier: %v", err)
	}
}
