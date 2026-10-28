package services

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/notifications"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/testutil"
)

// testNow is the frozen clock value used by the service tests
var testNow = testutil.Now

// newTestService creates an empty service with a frozen clock and the given notifier
func newTestService(n *testutil.RecordingNotifier) *TaskService {
	return NewTaskServiceWithOptions(
		WithClock(func() time.Time { return testNow }),
		WithNotifier(n),
		WithoutSampleData(),
	)
}

// seedOrderedTasks creates n pending tasks with strictly increasing CreatedAt
// values and returns their IDs in creation order
func seedOrderedTasks(t *testing.T, s *TaskService, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := testutil.MustCreateTask(t, s, fmt.Sprintf("task %d", i), models.TaskStatusPending, nil)
		// CreateTask uses time.Now, so pin CreatedAt for a deterministic order
		s.tasks[id].CreatedAt = testNow.Add(time.Duration(i) * time.Second)
		ids = append(ids, id)
	}
	return ids
}

func TestListOverdueTasks_Inclusion(t *testing.T) {
	tests := []struct {
		name   string
		status models.TaskStatus
		due    *time.Time
		want   bool
	}{
		{name: "pending past due", status: models.TaskStatusPending, due: testutil.PtrTime(testNow.Add(-time.Hour)), want: true},
		{name: "in_progress past due", status: models.TaskStatusInProgress, due: testutil.PtrTime(testNow.Add(-time.Hour)), want: true},
		{name: "on_hold past due", status: models.TaskStatusOnHold, due: testutil.PtrTime(testNow.Add(-time.Hour)), want: true},
		{name: "pending due one nanosecond before now", status: models.TaskStatusPending, due: testutil.PtrTime(testNow.Add(-time.Nanosecond)), want: true},
		{name: "completed past due", status: models.TaskStatusCompleted, due: testutil.PtrTime(testNow.Add(-time.Hour)), want: false},
		{name: "cancelled past due", status: models.TaskStatusCancelled, due: testutil.PtrTime(testNow.Add(-time.Hour)), want: false},
		{name: "pending due in the future", status: models.TaskStatusPending, due: testutil.PtrTime(testNow.Add(time.Hour)), want: false},
		{name: "pending without due date", status: models.TaskStatusPending, due: nil, want: false},
		{name: "pending due exactly now", status: models.TaskStatusPending, due: testutil.PtrTime(testNow), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService(&testutil.RecordingNotifier{})
			id := testutil.MustCreateTask(t, s, tt.name, tt.status, tt.due)

			resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
			if err != nil {
				t.Fatalf("ListOverdueTasks returned error: %v", err)
			}

			wantCount := 0
			if tt.want {
				wantCount = 1
			}
			if resp.TotalCount != wantCount {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, wantCount)
			}
			found := false
			for _, task := range resp.Tasks {
				if task.ID == id {
					found = true
				}
			}
			if found != tt.want {
				t.Errorf("task included = %v, want %v", found, tt.want)
			}
			if resp.NextPageToken != "" {
				t.Errorf("NextPageToken = %q, want empty", resp.NextPageToken)
			}
		})
	}
}

func TestListOverdueTasks_MixedStoreOrdering(t *testing.T) {
	s := newTestService(&testutil.RecordingNotifier{})

	dueLate := testutil.MustCreateTask(t, s, "due 1h ago", models.TaskStatusPending, testutil.PtrTime(testNow.Add(-1*time.Hour)))
	dueEarly := testutil.MustCreateTask(t, s, "due 3h ago", models.TaskStatusOnHold, testutil.PtrTime(testNow.Add(-3*time.Hour)))
	dueMid := testutil.MustCreateTask(t, s, "due 2h ago", models.TaskStatusInProgress, testutil.PtrTime(testNow.Add(-2*time.Hour)))
	tieA := testutil.MustCreateTask(t, s, "tie a", models.TaskStatusPending, testutil.PtrTime(testNow.Add(-4*time.Hour)))
	tieB := testutil.MustCreateTask(t, s, "tie b", models.TaskStatusPending, testutil.PtrTime(testNow.Add(-4*time.Hour)))

	// Tasks that must never be listed
	testutil.MustCreateTask(t, s, "completed", models.TaskStatusCompleted, testutil.PtrTime(testNow.Add(-5*time.Hour)))
	testutil.MustCreateTask(t, s, "cancelled", models.TaskStatusCancelled, testutil.PtrTime(testNow.Add(-5*time.Hour)))
	testutil.MustCreateTask(t, s, "future", models.TaskStatusPending, testutil.PtrTime(testNow.Add(time.Hour)))
	testutil.MustCreateTask(t, s, "no due date", models.TaskStatusPending, nil)
	testutil.MustCreateTask(t, s, "due now", models.TaskStatusPending, testutil.PtrTime(testNow))

	// Equal due dates are ordered by ID
	first, second := tieA, tieB
	if second < first {
		first, second = second, first
	}
	want := []string{first, second, dueEarly, dueMid, dueLate}

	resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 20})
	if err != nil {
		t.Fatalf("ListOverdueTasks returned error: %v", err)
	}
	if resp.TotalCount != len(want) {
		t.Errorf("TotalCount = %d, want %d", resp.TotalCount, len(want))
	}
	if got := testutil.TaskIDs(resp.Tasks); !testutil.EqualStrings(got, want) {
		t.Errorf("task order = %v, want %v", got, want)
	}
	for i := 1; i < len(resp.Tasks); i++ {
		prev, cur := resp.Tasks[i-1], resp.Tasks[i]
		if cur.DueDate.Before(*prev.DueDate) {
			t.Errorf("tasks[%d] due %v is before tasks[%d] due %v", i, cur.DueDate, i-1, prev.DueDate)
		}
		if cur.DueDate.Equal(*prev.DueDate) && cur.ID < prev.ID {
			t.Errorf("tasks[%d] ID %q sorts before tasks[%d] ID %q despite equal due dates", i, cur.ID, i-1, prev.ID)
		}
	}
	if resp.NextPageToken != "" {
		t.Errorf("NextPageToken = %q, want empty", resp.NextPageToken)
	}
}

func TestListOverdueTasks_Pagination(t *testing.T) {
	s := newTestService(&testutil.RecordingNotifier{})
	ids := testutil.SeedOverdueTasks(t, s, testNow, 5)

	tests := []struct {
		name     string
		token    string
		from     int
		to       int
		wantNext string
	}{
		{name: "first page", token: "", from: 0, to: 2, wantNext: "2"},
		{name: "second page", token: "2", from: 2, to: 4, wantNext: "4"},
		{name: "last page", token: "4", from: 4, to: 5, wantNext: ""},
		{name: "token at total count", token: "5", from: 0, to: 0, wantNext: ""},
		{name: "out of range token", token: "50", from: 0, to: 0, wantNext: ""},
		{name: "non-numeric token starts at zero", token: "abc", from: 0, to: 2, wantNext: "2"},
		{name: "negative token starts at zero", token: "-3", from: 0, to: 2, wantNext: "2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 2, PageToken: tt.token})
			if err != nil {
				t.Fatalf("ListOverdueTasks returned error: %v", err)
			}
			if resp.TotalCount != 5 {
				t.Errorf("TotalCount = %d, want 5", resp.TotalCount)
			}
			if got, want := testutil.TaskIDs(resp.Tasks), ids[tt.from:tt.to]; !testutil.EqualStrings(got, want) {
				t.Errorf("task IDs = %v, want %v", got, want)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
		})
	}

	t.Run("walking all pages yields every task once in order", func(t *testing.T) {
		var got []string
		var tokens []string
		token := ""
		for i := 0; i < 10; i++ {
			resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 2, PageToken: token})
			if err != nil {
				t.Fatalf("ListOverdueTasks returned error: %v", err)
			}
			got = append(got, testutil.TaskIDs(resp.Tasks)...)
			if resp.NextPageToken == "" {
				break
			}
			tokens = append(tokens, resp.NextPageToken)
			token = resp.NextPageToken
		}
		if !testutil.EqualStrings(got, ids) {
			t.Errorf("walked IDs = %v, want %v", got, ids)
		}
		if want := []string{"2", "4"}; !testutil.EqualStrings(tokens, want) {
			t.Errorf("page tokens = %v, want %v", tokens, want)
		}
	})
}

func TestListOverdueTasks_PageSizeDefaults(t *testing.T) {
	tests := []struct {
		name     string
		count    int
		pageSize int
		wantLen  int
		wantNext string
	}{
		{name: "zero page size defaults to 20", count: 25, pageSize: 0, wantLen: 20, wantNext: "20"},
		{name: "negative page size defaults to 20", count: 25, pageSize: -1, wantLen: 20, wantNext: "20"},
		{name: "page size above 100 is clamped", count: 105, pageSize: 500, wantLen: 100, wantNext: "100"},
		{name: "page size of 100 is allowed", count: 105, pageSize: 100, wantLen: 100, wantNext: "100"},
		{name: "page size larger than result", count: 3, pageSize: 10, wantLen: 3, wantNext: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService(&testutil.RecordingNotifier{})
			ids := testutil.SeedOverdueTasks(t, s, testNow, tt.count)

			resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: tt.pageSize})
			if err != nil {
				t.Fatalf("ListOverdueTasks returned error: %v", err)
			}
			if resp.TotalCount != tt.count {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, tt.count)
			}
			if got, want := testutil.TaskIDs(resp.Tasks), ids[:tt.wantLen]; !testutil.EqualStrings(got, want) {
				t.Errorf("got %d tasks %v, want %d tasks %v", len(got), got, len(want), want)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
		})
	}
}

func TestListOverdueTasks_Empty(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, s *TaskService)
	}{
		{name: "empty store", setup: func(t *testing.T, s *TaskService) {}},
		{name: "no task is overdue", setup: func(t *testing.T, s *TaskService) {
			testutil.MustCreateTask(t, s, "completed", models.TaskStatusCompleted, testutil.PtrTime(testNow.Add(-time.Hour)))
			testutil.MustCreateTask(t, s, "cancelled", models.TaskStatusCancelled, testutil.PtrTime(testNow.Add(-time.Hour)))
			testutil.MustCreateTask(t, s, "future", models.TaskStatusPending, testutil.PtrTime(testNow.Add(time.Hour)))
			testutil.MustCreateTask(t, s, "no due date", models.TaskStatusInProgress, nil)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService(&testutil.RecordingNotifier{})
			tt.setup(t, s)

			resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 20})
			if err != nil {
				t.Fatalf("ListOverdueTasks returned error: %v", err)
			}
			if resp.TotalCount != 0 {
				t.Errorf("TotalCount = %d, want 0", resp.TotalCount)
			}
			if resp.Tasks == nil || len(resp.Tasks) != 0 {
				t.Errorf("Tasks = %#v, want a non-nil empty slice", resp.Tasks)
			}
			if resp.NextPageToken != "" {
				t.Errorf("NextPageToken = %q, want empty", resp.NextPageToken)
			}
		})
	}
}

func TestAssignTask_Success(t *testing.T) {
	tests := []struct {
		name      string
		email     string
		wantEmail string
	}{
		{name: "plain email", email: "alice@example.com", wantEmail: "alice@example.com"},
		{name: "email with surrounding whitespace is trimmed", email: "  bob@example.com\t", wantEmail: "bob@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &testutil.RecordingNotifier{}
			s := newTestService(n)
			id := testutil.MustCreateTask(t, s, "Write docs", models.TaskStatusPending, nil)

			got, err := s.AssignTask(id, tt.email)
			if err != nil {
				t.Fatalf("AssignTask returned error: %v", err)
			}
			if got == nil {
				t.Fatal("AssignTask returned nil task")
			}
			if got.ID != id {
				t.Errorf("returned ID = %q, want %q", got.ID, id)
			}
			if got.AssignedTo == nil || *got.AssignedTo != tt.wantEmail {
				t.Errorf("returned AssignedTo = %v, want %q", got.AssignedTo, tt.wantEmail)
			}
			if !got.UpdatedAt.Equal(testNow) {
				t.Errorf("returned UpdatedAt = %v, want %v", got.UpdatedAt, testNow)
			}

			stored, err := s.GetTask(id)
			if err != nil {
				t.Fatalf("GetTask returned error: %v", err)
			}
			if stored.AssignedTo == nil || *stored.AssignedTo != tt.wantEmail {
				t.Errorf("stored AssignedTo = %v, want %q", stored.AssignedTo, tt.wantEmail)
			}
			if !stored.UpdatedAt.Equal(testNow) {
				t.Errorf("stored UpdatedAt = %v, want %v", stored.UpdatedAt, testNow)
			}

			calls := n.Recorded()
			if len(calls) != 1 {
				t.Fatalf("notifier called %d times, want 1", len(calls))
			}
			call := calls[0]
			if call.Assignee != tt.wantEmail {
				t.Errorf("notified assignee = %q, want %q", call.Assignee, tt.wantEmail)
			}
			if call.Task.ID != id {
				t.Errorf("notified task ID = %q, want %q", call.Task.ID, id)
			}
			if call.Task.Title != "Write docs" {
				t.Errorf("notified task Title = %q, want %q", call.Task.Title, "Write docs")
			}
			if call.Task.AssignedTo == nil || *call.Task.AssignedTo != tt.wantEmail {
				t.Errorf("notified task AssignedTo = %v, want %q", call.Task.AssignedTo, tt.wantEmail)
			}
			if !call.Task.UpdatedAt.Equal(testNow) {
				t.Errorf("notified task UpdatedAt = %v, want %v", call.Task.UpdatedAt, testNow)
			}
		})
	}
}

func TestAssignTask_Reassign(t *testing.T) {
	n := &testutil.RecordingNotifier{}
	s := newTestService(n)
	id := testutil.MustCreateTask(t, s, "Reassign me", models.TaskStatusInProgress, nil)

	if _, err := s.AssignTask(id, "first@example.com"); err != nil {
		t.Fatalf("first AssignTask returned error: %v", err)
	}
	if _, err := s.AssignTask(id, "second@example.com"); err != nil {
		t.Fatalf("second AssignTask returned error: %v", err)
	}

	stored, err := s.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if stored.AssignedTo == nil || *stored.AssignedTo != "second@example.com" {
		t.Errorf("stored AssignedTo = %v, want %q", stored.AssignedTo, "second@example.com")
	}
	if stored.Status != models.TaskStatusInProgress {
		t.Errorf("stored Status = %q, want %q", stored.Status, models.TaskStatusInProgress)
	}

	calls := n.Recorded()
	if len(calls) != 2 {
		t.Fatalf("notifier called %d times, want 2", len(calls))
	}
	if calls[1].Assignee != "second@example.com" {
		t.Errorf("second notified assignee = %q, want %q", calls[1].Assignee, "second@example.com")
	}

	// The new assignee is visible through the existing assigned_to filter
	resp, err := s.ListTasks(models.ListTasksQuery{AssignedTo: "second@example.com"})
	if err != nil {
		t.Fatalf("ListTasks returned error: %v", err)
	}
	if got := testutil.TaskIDs(resp.Tasks); !testutil.EqualStrings(got, []string{id}) {
		t.Errorf("ListTasks(assigned_to) IDs = %v, want [%s]", got, id)
	}
}

func TestAssignTask_ReturnsCopy(t *testing.T) {
	s := newTestService(&testutil.RecordingNotifier{})
	task, err := s.CreateTask(models.CreateTaskRequest{Title: "Original", Tags: []string{"go", "api"}})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	id := task.ID

	got, err := s.AssignTask(id, "alice@example.com")
	if err != nil {
		t.Fatalf("AssignTask returned error: %v", err)
	}

	// Mutating the returned task must not change the stored task
	got.Title = "Mutated"
	got.Status = models.TaskStatusCancelled
	got.Tags[0] = "mutated"
	other := "mallory@example.com"
	got.AssignedTo = &other

	stored, err := s.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if stored.Title != "Original" {
		t.Errorf("stored Title = %q, want %q", stored.Title, "Original")
	}
	if stored.Status != models.TaskStatusPending {
		t.Errorf("stored Status = %q, want %q", stored.Status, models.TaskStatusPending)
	}
	if want := []string{"go", "api"}; !testutil.EqualStrings(stored.Tags, want) {
		t.Errorf("stored Tags = %v, want %v", stored.Tags, want)
	}
	if stored.AssignedTo == nil || *stored.AssignedTo != "alice@example.com" {
		t.Errorf("stored AssignedTo = %v, want %q", stored.AssignedTo, "alice@example.com")
	}
}

func TestAssignTask_UnknownID(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{name: "random id", id: "does-not-exist"},
		{name: "empty id", id: ""},
		{name: "overdue literal", id: "overdue"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &testutil.RecordingNotifier{}
			s := newTestService(n)
			testutil.MustCreateTask(t, s, "existing", models.TaskStatusPending, nil)

			got, err := s.AssignTask(tt.id, "alice@example.com")
			if err == nil {
				t.Fatal("AssignTask returned nil error, want not found error")
			}
			if want := fmt.Sprintf("task with ID %s not found", tt.id); err.Error() != want {
				t.Errorf("error = %q, want %q", err.Error(), want)
			}
			if got != nil {
				t.Errorf("returned task = %+v, want nil", got)
			}
			if calls := n.Recorded(); len(calls) != 0 {
				t.Errorf("notifier called %d times, want 0", len(calls))
			}
		})
	}
}

func TestAssignTask_NotifierError(t *testing.T) {
	n := &testutil.RecordingNotifier{Err: errors.New("notification backend unavailable")}
	s := newTestService(n)
	id := testutil.MustCreateTask(t, s, "Notify fails", models.TaskStatusPending, nil)

	got, err := s.AssignTask(id, "alice@example.com")
	if err != nil {
		t.Fatalf("AssignTask returned error %v, want nil when notifier fails", err)
	}
	if got == nil || got.AssignedTo == nil || *got.AssignedTo != "alice@example.com" {
		t.Errorf("returned task = %+v, want AssignedTo %q", got, "alice@example.com")
	}

	stored, err := s.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if stored.AssignedTo == nil || *stored.AssignedTo != "alice@example.com" {
		t.Errorf("stored AssignedTo = %v, want %q", stored.AssignedTo, "alice@example.com")
	}
	if !stored.UpdatedAt.Equal(testNow) {
		t.Errorf("stored UpdatedAt = %v, want %v", stored.UpdatedAt, testNow)
	}
	if calls := n.Recorded(); len(calls) != 1 {
		t.Errorf("notifier called %d times, want 1", len(calls))
	}
}

func TestSetNotifier(t *testing.T) {
	original := &testutil.RecordingNotifier{}
	replacement := &testutil.RecordingNotifier{}
	s := newTestService(original)
	id := testutil.MustCreateTask(t, s, "Swap notifier", models.TaskStatusPending, nil)

	s.SetNotifier(replacement)
	if _, err := s.AssignTask(id, "alice@example.com"); err != nil {
		t.Fatalf("AssignTask returned error: %v", err)
	}

	// A nil notifier is ignored and keeps the replacement
	s.SetNotifier(nil)
	if _, err := s.AssignTask(id, "bob@example.com"); err != nil {
		t.Fatalf("AssignTask returned error: %v", err)
	}

	if calls := original.Recorded(); len(calls) != 0 {
		t.Errorf("original notifier called %d times, want 0", len(calls))
	}
	calls := replacement.Recorded()
	if len(calls) != 2 {
		t.Fatalf("replacement notifier called %d times, want 2", len(calls))
	}
	if calls[0].Assignee != "alice@example.com" || calls[1].Assignee != "bob@example.com" {
		t.Errorf("replacement assignees = [%q %q], want [alice@example.com bob@example.com]", calls[0].Assignee, calls[1].Assignee)
	}
}

func TestNewTaskService_Defaults(t *testing.T) {
	t.Run("NewTaskService seeds sample data", func(t *testing.T) {
		s := NewTaskService()
		if got := s.GetStats().Total; got != 2 {
			t.Errorf("sample task count = %d, want 2", got)
		}
		resp, err := s.ListTasks(models.ListTasksQuery{PageSize: 20})
		if err != nil {
			t.Fatalf("ListTasks returned error: %v", err)
		}
		if resp.TotalCount != 2 || len(resp.Tasks) != 2 {
			t.Errorf("ListTasks TotalCount = %d, len = %d, want 2 and 2", resp.TotalCount, len(resp.Tasks))
		}
		if _, ok := s.notifier.(*notifications.LogNotifier); !ok {
			t.Errorf("default notifier is %T, want *notifications.LogNotifier", s.notifier)
		}
		if s.now == nil {
			t.Error("default clock is nil")
		}
		// Sample tasks have no due date, so nothing is overdue
		overdue, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
		if err != nil {
			t.Fatalf("ListOverdueTasks returned error: %v", err)
		}
		if overdue.TotalCount != 0 {
			t.Errorf("overdue TotalCount = %d, want 0", overdue.TotalCount)
		}
	})

	t.Run("nil options are ignored", func(t *testing.T) {
		s := NewTaskServiceWithOptions(WithClock(nil), WithNotifier(nil), nil, WithoutSampleData())
		if got := s.GetStats().Total; got != 0 {
			t.Errorf("task count = %d, want 0", got)
		}
		if s.now == nil {
			t.Error("clock is nil, want default")
		}
		if _, ok := s.notifier.(*notifications.LogNotifier); !ok {
			t.Errorf("notifier is %T, want *notifications.LogNotifier", s.notifier)
		}
	})

	t.Run("WithClock drives the overdue cutoff", func(t *testing.T) {
		s := newTestService(&testutil.RecordingNotifier{})
		// Due one hour after the frozen clock, so not overdue even though it is in the real past
		testutil.MustCreateTask(t, s, "not yet due", models.TaskStatusPending, testutil.PtrTime(testNow.Add(time.Hour)))
		resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
		if err != nil {
			t.Fatalf("ListOverdueTasks returned error: %v", err)
		}
		if resp.TotalCount != 0 {
			t.Errorf("TotalCount = %d, want 0", resp.TotalCount)
		}
	})
}

func TestListTasks_PaginationRegression(t *testing.T) {
	s := newTestService(&testutil.RecordingNotifier{})
	ids := seedOrderedTasks(t, s, 105)

	tests := []struct {
		name     string
		pageSize int
		token    string
		from     int
		to       int
		wantNext string
	}{
		{name: "zero page size defaults to 20", pageSize: 0, token: "", from: 0, to: 20, wantNext: "20"},
		{name: "negative page size defaults to 20", pageSize: -1, token: "", from: 0, to: 20, wantNext: "20"},
		{name: "page size above 100 is clamped", pageSize: 150, token: "", from: 0, to: 100, wantNext: "100"},
		{name: "page size of 100", pageSize: 100, token: "", from: 0, to: 100, wantNext: "100"},
		{name: "middle page", pageSize: 10, token: "50", from: 50, to: 60, wantNext: "60"},
		{name: "last partial page has no token", pageSize: 10, token: "100", from: 100, to: 105, wantNext: ""},
		{name: "last exact page has no token", pageSize: 5, token: "100", from: 100, to: 105, wantNext: ""},
		{name: "token at total count is empty", pageSize: 10, token: "105", from: 0, to: 0, wantNext: ""},
		{name: "out of range token is empty", pageSize: 10, token: "500", from: 0, to: 0, wantNext: ""},
		{name: "non-numeric token starts at zero", pageSize: 10, token: "abc", from: 0, to: 10, wantNext: "10"},
		// Intentional deviation from origin, where a negative token panicked.
		{name: "negative token starts at zero", pageSize: 10, token: "-5", from: 0, to: 10, wantNext: "10"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.ListTasks(models.ListTasksQuery{
				PageSize:  tt.pageSize,
				PageToken: tt.token,
				SortOrder: models.SortOrderCreatedAtAsc,
			})
			if err != nil {
				t.Fatalf("ListTasks returned error: %v", err)
			}
			if resp.TotalCount != len(ids) {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, len(ids))
			}
			if got, want := testutil.TaskIDs(resp.Tasks), ids[tt.from:tt.to]; !testutil.EqualStrings(got, want) {
				t.Errorf("got %d tasks, want %d (IDs[%d:%d])", len(got), len(want), tt.from, tt.to)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
		})
	}

	t.Run("walking all pages yields every task once in order", func(t *testing.T) {
		const pageSize = 7
		var got []string
		token := ""
		for page := 0; page < 100; page++ {
			resp, err := s.ListTasks(models.ListTasksQuery{
				PageSize:  pageSize,
				PageToken: token,
				SortOrder: models.SortOrderCreatedAtAsc,
			})
			if err != nil {
				t.Fatalf("ListTasks returned error: %v", err)
			}
			got = append(got, testutil.TaskIDs(resp.Tasks)...)
			if resp.NextPageToken == "" {
				break
			}
			if want := strconv.Itoa(len(got)); resp.NextPageToken != want {
				t.Fatalf("NextPageToken = %q, want %q", resp.NextPageToken, want)
			}
			token = resp.NextPageToken
		}
		if !testutil.EqualStrings(got, ids) {
			t.Errorf("walked %d tasks, want %d in creation order", len(got), len(ids))
		}
	})

	t.Run("empty store", func(t *testing.T) {
		empty := newTestService(&testutil.RecordingNotifier{})
		resp, err := empty.ListTasks(models.ListTasksQuery{PageSize: 20})
		if err != nil {
			t.Fatalf("ListTasks returned error: %v", err)
		}
		if resp.TotalCount != 0 || len(resp.Tasks) != 0 || resp.NextPageToken != "" {
			t.Errorf("got TotalCount=%d len=%d next=%q, want 0, 0 and empty", resp.TotalCount, len(resp.Tasks), resp.NextPageToken)
		}
	})
}

func TestListTasks_FiltersRegression(t *testing.T) {
	s := newTestService(&testutil.RecordingNotifier{})
	testutil.MustCreateTask(t, s, "pending 1", models.TaskStatusPending, nil)
	testutil.MustCreateTask(t, s, "pending 2", models.TaskStatusPending, nil)
	testutil.MustCreateTask(t, s, "in progress 1", models.TaskStatusInProgress, nil)
	testutil.MustCreateTask(t, s, "in progress 2", models.TaskStatusInProgress, nil)
	testutil.MustCreateTask(t, s, "completed", models.TaskStatusCompleted, nil)

	tests := []struct {
		name      string
		status    models.TaskStatus
		pageSize  int
		wantTotal int
		wantLen   int
		wantNext  string
	}{
		{name: "no filter", status: "", pageSize: 20, wantTotal: 5, wantLen: 5, wantNext: ""},
		{name: "pending", status: models.TaskStatusPending, pageSize: 20, wantTotal: 2, wantLen: 2, wantNext: ""},
		{name: "in_progress", status: models.TaskStatusInProgress, pageSize: 20, wantTotal: 2, wantLen: 2, wantNext: ""},
		{name: "completed", status: models.TaskStatusCompleted, pageSize: 20, wantTotal: 1, wantLen: 1, wantNext: ""},
		{name: "on_hold has no match", status: models.TaskStatusOnHold, pageSize: 20, wantTotal: 0, wantLen: 0, wantNext: ""},
		{name: "filter with pagination", status: models.TaskStatusInProgress, pageSize: 1, wantTotal: 2, wantLen: 1, wantNext: "1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.ListTasks(models.ListTasksQuery{PageSize: tt.pageSize, Status: tt.status})
			if err != nil {
				t.Fatalf("ListTasks returned error: %v", err)
			}
			if resp.TotalCount != tt.wantTotal {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, tt.wantTotal)
			}
			if len(resp.Tasks) != tt.wantLen {
				t.Errorf("got %d tasks, want %d", len(resp.Tasks), tt.wantLen)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
			for _, task := range resp.Tasks {
				if tt.status != "" && task.Status != tt.status {
					t.Errorf("task %q has status %q, want %q", task.ID, task.Status, tt.status)
				}
			}
		})
	}
}

func TestTaskService_ConcurrentAccess(t *testing.T) {
	n := &testutil.RecordingNotifier{}
	s := newTestService(n)
	ids := testutil.SeedOverdueTasks(t, s, testNow, 10)

	const workers = 8
	const iterations = 50

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(3)

		go func(w int) {
			defer wg.Done()
			email := fmt.Sprintf("user%d@example.com", w)
			for i := 0; i < iterations; i++ {
				id := ids[(w+i)%len(ids)]
				task, err := s.AssignTask(id, email)
				if err != nil {
					t.Errorf("AssignTask(%q) returned error: %v", id, err)
					return
				}
				if task.AssignedTo == nil || *task.AssignedTo != email {
					t.Errorf("AssignTask(%q) AssignedTo = %v, want %q", id, task.AssignedTo, email)
					return
				}
			}
		}(w)

		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				resp, err := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 5, PageToken: strconv.Itoa(i % 10)})
				if err != nil {
					t.Errorf("ListOverdueTasks returned error: %v", err)
					return
				}
				if resp.TotalCount != len(ids) {
					t.Errorf("ListOverdueTasks TotalCount = %d, want %d", resp.TotalCount, len(ids))
					return
				}
				for _, task := range resp.Tasks {
					if task.AssignedTo != nil && *task.AssignedTo == "" {
						t.Errorf("task %q has empty assignee", task.ID)
						return
					}
				}
			}
		}()

		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				stats := s.GetStats()
				if stats.Total != len(ids) {
					t.Errorf("GetStats Total = %d, want %d", stats.Total, len(ids))
					return
				}
			}
		}()
	}
	wg.Wait()

	if got, want := len(n.Recorded()), workers*iterations; got != want {
		t.Errorf("notifier called %d times, want %d", got, want)
	}
}
