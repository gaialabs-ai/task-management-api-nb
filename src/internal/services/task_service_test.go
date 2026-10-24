package services

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

var testNow = time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	calls []notifyCall
	err   error
}

type notifyCall struct {
	task       models.Task
	assignedTo string
}

func (f *fakeNotifier) NotifyTaskAssigned(task models.Task, assignedTo string) error {
	f.calls = append(f.calls, notifyCall{task: task, assignedTo: assignedTo})
	return f.err
}

func newTestService(n Notifier) *TaskService {
	return NewTaskService(WithClock(func() time.Time { return testNow }), WithNotifier(n))
}

func mustCreate(t *testing.T, s *TaskService, title string, due *time.Time) *models.Task {
	t.Helper()
	task, err := s.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task
}

func at(d time.Duration) *time.Time {
	v := testNow.Add(d)
	return &v
}

func titles(tasks []models.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.Title)
	}
	return out
}

func TestListOverdueTasks_FilteringAndSorting(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	// Sample tasks created by NewTaskService have no due date and must be excluded.
	mustCreate(t, s, "future", at(time.Hour))
	mustCreate(t, s, "no-due", nil)
	mustCreate(t, s, "old", at(-48*time.Hour))
	mustCreate(t, s, "recent", at(-time.Hour))
	mustCreate(t, s, "older", at(-72*time.Hour))
	done := mustCreate(t, s, "completed", at(-24*time.Hour))
	canc := mustCreate(t, s, "cancelled", at(-24*time.Hour))
	hold := mustCreate(t, s, "on-hold", at(-30*time.Hour))
	if _, err := s.UpdateTaskStatus(done.ID, models.TaskStatusCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTaskStatus(canc.ID, models.TaskStatusCancelled); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTaskStatus(hold.ID, models.TaskStatusOnHold); err != nil {
		t.Fatal(err)
	}

	resp := s.ListOverdueTasks(models.OverdueTasksQuery{})
	want := []string{"older", "old", "on-hold", "recent"}
	got := titles(resp.Tasks)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if resp.TotalCount != 4 || resp.NextPageToken != "" {
		t.Fatalf("total=%d next=%q", resp.TotalCount, resp.NextPageToken)
	}
}

func TestListOverdueTasks_DueExactlyNowIsNotOverdue(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	mustCreate(t, s, "now", at(0))
	if resp := s.ListOverdueTasks(models.OverdueTasksQuery{}); resp.TotalCount != 0 {
		t.Fatalf("expected none, got %v", titles(resp.Tasks))
	}
}

func TestListOverdueTasks_IDTiebreak(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	for i := 0; i < 5; i++ {
		mustCreate(t, s, fmt.Sprintf("t%d", i), at(-time.Hour))
	}
	resp := s.ListOverdueTasks(models.OverdueTasksQuery{})
	if len(resp.Tasks) != 5 {
		t.Fatalf("got %d tasks", len(resp.Tasks))
	}
	for i := 1; i < len(resp.Tasks); i++ {
		if resp.Tasks[i-1].ID >= resp.Tasks[i].ID {
			t.Fatalf("not sorted by ID tiebreak at %d", i)
		}
	}
}

func TestListOverdueTasks_Pagination(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	for i := 0; i < 5; i++ {
		mustCreate(t, s, fmt.Sprintf("t%d", i), at(-time.Duration(10-i)*time.Hour))
	}

	tests := []struct {
		name      string
		query     models.OverdueTasksQuery
		wantTitle []string
		wantNext  string
	}{
		{"first page", models.OverdueTasksQuery{PageSize: 2}, []string{"t0", "t1"}, "2"},
		{"second page", models.OverdueTasksQuery{PageSize: 2, PageToken: "2"}, []string{"t2", "t3"}, "4"},
		{"last page", models.OverdueTasksQuery{PageSize: 2, PageToken: "4"}, []string{"t4"}, ""},
		{"past end", models.OverdueTasksQuery{PageSize: 2, PageToken: "10"}, []string{}, ""},
		{"invalid token", models.OverdueTasksQuery{PageSize: 2, PageToken: "abc"}, []string{"t0", "t1"}, "2"},
		{"default size", models.OverdueTasksQuery{}, []string{"t0", "t1", "t2", "t3", "t4"}, ""},
		{"oversize clamped", models.OverdueTasksQuery{PageSize: 1000}, []string{"t0", "t1", "t2", "t3", "t4"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := s.ListOverdueTasks(tc.query)
			if fmt.Sprint(titles(resp.Tasks)) != fmt.Sprint(tc.wantTitle) {
				t.Errorf("tasks = %v, want %v", titles(resp.Tasks), tc.wantTitle)
			}
			if resp.NextPageToken != tc.wantNext {
				t.Errorf("next = %q, want %q", resp.NextPageToken, tc.wantNext)
			}
			if resp.TotalCount != 5 {
				t.Errorf("total = %d, want 5", resp.TotalCount)
			}
		})
	}
}

func TestListTasks_PaginationUnchanged(t *testing.T) {
	s := newTestService(&fakeNotifier{}) // 2 sample tasks
	for i := 0; i < 3; i++ {
		mustCreate(t, s, fmt.Sprintf("t%d", i), nil)
	}
	tests := []struct {
		name     string
		query    models.ListTasksQuery
		wantLen  int
		wantNext string
	}{
		{"default page size", models.ListTasksQuery{}, 5, ""},
		{"page size 2", models.ListTasksQuery{PageSize: 2}, 2, "2"},
		{"second page", models.ListTasksQuery{PageSize: 2, PageToken: "2"}, 2, "4"},
		{"last page", models.ListTasksQuery{PageSize: 2, PageToken: "4"}, 1, ""},
		{"beyond end", models.ListTasksQuery{PageSize: 2, PageToken: "99"}, 0, ""},
		{"bad token", models.ListTasksQuery{PageSize: 2, PageToken: "x"}, 2, "2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := s.ListTasks(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Tasks) != tc.wantLen || resp.NextPageToken != tc.wantNext || resp.TotalCount != 5 {
				t.Errorf("len=%d next=%q total=%d", len(resp.Tasks), resp.NextPageToken, resp.TotalCount)
			}
		})
	}
}

func TestAssignTask(t *testing.T) {
	tests := []struct {
		name       string
		notifyErr  error
		unknownID  bool
		wantNotify bool
	}{
		{name: "success", wantNotify: true},
		{name: "notifier error is not fatal", notifyErr: errors.New("boom"), wantNotify: true},
		{name: "unknown task", unknownID: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := &fakeNotifier{err: tc.notifyErr}
			s := newTestService(n)
			task := mustCreate(t, s, "work", nil)
			id := task.ID
			if tc.unknownID {
				id = "missing"
			}
			// Move the clock forward so UpdatedAt visibly changes.
			s.clock = func() time.Time { return testNow.Add(time.Minute) }

			got, err := s.AssignTask(id, "bob@example.com")
			if tc.unknownID {
				if !errors.Is(err, ErrTaskNotFound) {
					t.Fatalf("err = %v, want ErrTaskNotFound", err)
				}
				if len(n.calls) != 0 {
					t.Fatalf("notifier called for unknown task")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
				t.Fatalf("AssignedTo = %v", got.AssignedTo)
			}
			if !got.UpdatedAt.Equal(testNow.Add(time.Minute)) {
				t.Fatalf("UpdatedAt = %v", got.UpdatedAt)
			}
			stored, _ := s.GetTask(id)
			if stored.AssignedTo == nil || *stored.AssignedTo != "bob@example.com" {
				t.Fatalf("stored AssignedTo = %v", stored.AssignedTo)
			}
			if tc.wantNotify {
				if len(n.calls) != 1 || n.calls[0].assignedTo != "bob@example.com" || n.calls[0].task.ID != id {
					t.Fatalf("notifier calls = %+v", n.calls)
				}
			}
		})
	}
}

func TestNewTaskService_DefaultsAndBackwardCompat(t *testing.T) {
	s := NewTaskService()
	if s.notifier == nil || s.clock == nil {
		t.Fatal("defaults not set")
	}
	task := mustCreate(t, s, "x", nil)
	if _, err := s.AssignTask(task.ID, "a@example.com"); err != nil {
		t.Fatal(err)
	}
}
