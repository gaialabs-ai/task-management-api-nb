package services

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

var fixedNow = time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

type notifyCall struct {
	taskID   string
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
	f.calls = append(f.calls, notifyCall{taskID: task.ID, assignee: assignee})
	return f.err
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newTestService(t *testing.T) (*TaskService, *fakeNotifier) {
	t.Helper()
	svc := NewTaskService()
	svc.SetClock(func() time.Time { return fixedNow })
	n := &fakeNotifier{}
	svc.SetNotifier(n)
	return svc, n
}

// addTask creates a task with the given title, due date and status.
func addTask(t *testing.T, svc *TaskService, title string, due *time.Time, status models.TaskStatus) *models.Task {
	t.Helper()
	task, err := svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatalf("CreateTask(%q): %v", title, err)
	}
	if status != "" && status != models.TaskStatusPending {
		if _, err := svc.UpdateTaskStatus(task.ID, status); err != nil {
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
	for _, tk := range tasks {
		out = append(out, tk.Title)
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

func TestListOverdueTasksFiltering(t *testing.T) {
	hour := time.Hour
	tests := []struct {
		name string
		due  *time.Time
		stat models.TaskStatus
		want bool
	}{
		{"past due pending", at(-hour), models.TaskStatusPending, true},
		{"past due in progress", at(-hour), models.TaskStatusInProgress, true},
		{"past due on hold", at(-hour), models.TaskStatusOnHold, true},
		{"past due completed", at(-hour), models.TaskStatusCompleted, false},
		{"past due cancelled", at(-hour), models.TaskStatusCancelled, false},
		{"future due", at(hour), models.TaskStatusPending, false},
		{"due exactly now", at(0), models.TaskStatusPending, false},
		{"no due date", nil, models.TaskStatusPending, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			task := addTask(t, svc, tc.name, tc.due, tc.stat)
			resp, err := svc.ListOverdueTasks(models.ListTasksQuery{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			found := false
			for _, got := range resp.Tasks {
				if got.ID == task.ID {
					found = true
				}
			}
			if found != tc.want {
				t.Errorf("overdue=%v, want %v", found, tc.want)
			}
			wantCount := 0
			if tc.want {
				wantCount = 1
			}
			// Seeded sample tasks have no due date and never appear.
			if resp.TotalCount != wantCount || len(resp.Tasks) != wantCount {
				t.Errorf("total=%d len=%d, want %d", resp.TotalCount, len(resp.Tasks), wantCount)
			}
		})
	}
}

func TestListOverdueTasksEmpty(t *testing.T) {
	svc, _ := newTestService(t)
	resp, err := svc.ListOverdueTasks(models.ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Tasks) != 0 || resp.TotalCount != 0 || resp.NextPageToken != "" {
		t.Errorf("expected empty response, got %+v", resp)
	}
}

func TestListOverdueTasksSortOrder(t *testing.T) {
	svc, _ := newTestService(t)
	addTask(t, svc, "c", at(-1*time.Hour), "")
	addTask(t, svc, "a", at(-72*time.Hour), "")
	addTask(t, svc, "b", at(-24*time.Hour), "")

	resp, _ := svc.ListOverdueTasks(models.ListTasksQuery{})
	want := []string{"a", "b", "c"}
	if got := titles(resp.Tasks); !equalStrings(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestListOverdueTasksTiebreakByID(t *testing.T) {
	svc, _ := newTestService(t)
	due := at(-2 * time.Hour)
	for i := 0; i < 6; i++ {
		addTask(t, svc, fmt.Sprintf("t%d", i), due, "")
	}
	resp, _ := svc.ListOverdueTasks(models.ListTasksQuery{})
	if len(resp.Tasks) != 6 {
		t.Fatalf("got %d tasks", len(resp.Tasks))
	}
	for i := 1; i < len(resp.Tasks); i++ {
		if resp.Tasks[i-1].ID >= resp.Tasks[i].ID {
			t.Errorf("tasks not ordered by ID on tie: %s >= %s", resp.Tasks[i-1].ID, resp.Tasks[i].ID)
		}
	}
}

func TestListOverdueTasksPagination(t *testing.T) {
	svc, _ := newTestService(t)
	for i := 0; i < 5; i++ {
		addTask(t, svc, fmt.Sprintf("t%d", i), at(-time.Duration(10-i)*time.Hour), "")
	}

	tests := []struct {
		name      string
		query     models.ListTasksQuery
		wantTitle []string
		wantNext  string
	}{
		{"first page", models.ListTasksQuery{PageSize: 2}, []string{"t0", "t1"}, "2"},
		{"second page", models.ListTasksQuery{PageSize: 2, PageToken: "2"}, []string{"t2", "t3"}, "4"},
		{"last page", models.ListTasksQuery{PageSize: 2, PageToken: "4"}, []string{"t4"}, ""},
		{"exact end", models.ListTasksQuery{PageSize: 5}, []string{"t0", "t1", "t2", "t3", "t4"}, ""},
		{"default page size", models.ListTasksQuery{}, []string{"t0", "t1", "t2", "t3", "t4"}, ""},
		{"token beyond end", models.ListTasksQuery{PageSize: 2, PageToken: "99"}, []string{}, ""},
		{"invalid token ignored", models.ListTasksQuery{PageSize: 2, PageToken: "abc"}, []string{"t0", "t1"}, "2"},
		{"negative token ignored", models.ListTasksQuery{PageSize: 2, PageToken: "-1"}, []string{"t0", "t1"}, "2"},
		{"zero token", models.ListTasksQuery{PageSize: 2, PageToken: "0"}, []string{"t0", "t1"}, "2"},
		{"page size capped at 100", models.ListTasksQuery{PageSize: 1000}, []string{"t0", "t1", "t2", "t3", "t4"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.ListOverdueTasks(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if got := titles(resp.Tasks); !equalStrings(got, tc.wantTitle) {
				t.Errorf("titles = %v, want %v", got, tc.wantTitle)
			}
			if resp.NextPageToken != tc.wantNext {
				t.Errorf("next token = %q, want %q", resp.NextPageToken, tc.wantNext)
			}
			if resp.TotalCount != 5 {
				t.Errorf("total = %d, want 5", resp.TotalCount)
			}
		})
	}
}

func TestListTasksPaginationRegression(t *testing.T) {
	svc := NewTaskService() // 2 seeded tasks
	for i := 0; i < 3; i++ {
		addTask(t, svc, fmt.Sprintf("t%d", i), nil, "")
	}
	// 5 tasks total.
	tests := []struct {
		name     string
		query    models.ListTasksQuery
		wantLen  int
		wantNext string
	}{
		{"page 1", models.ListTasksQuery{PageSize: 2}, 2, "2"},
		{"page 2", models.ListTasksQuery{PageSize: 2, PageToken: "2"}, 2, "4"},
		{"page 3", models.ListTasksQuery{PageSize: 2, PageToken: "4"}, 1, ""},
		{"all", models.ListTasksQuery{}, 5, ""},
		{"beyond end", models.ListTasksQuery{PageSize: 2, PageToken: "10"}, 0, ""},
		{"negative token treated as first page", models.ListTasksQuery{PageSize: 2, PageToken: "-1"}, 2, "2"},
		{"zero token", models.ListTasksQuery{PageSize: 2, PageToken: "0"}, 2, "2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.ListTasks(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Tasks) != tc.wantLen || resp.NextPageToken != tc.wantNext || resp.TotalCount != 5 {
				t.Errorf("got len=%d next=%q total=%d; want len=%d next=%q total=5",
					len(resp.Tasks), resp.NextPageToken, resp.TotalCount, tc.wantLen, tc.wantNext)
			}
		})
	}
}

func TestAssignTask(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc, n := newTestService(t)
		task := addTask(t, svc, "x", nil, "")
		got, err := svc.AssignTask(task.ID, "  bob@example.com ")
		if err != nil {
			t.Fatal(err)
		}
		if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
			t.Errorf("AssignedTo = %v", got.AssignedTo)
		}
		if !got.UpdatedAt.Equal(fixedNow) {
			t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, fixedNow)
		}
		stored, _ := svc.GetTask(task.ID)
		if stored.AssignedTo == nil || *stored.AssignedTo != "bob@example.com" {
			t.Errorf("stored AssignedTo = %v", stored.AssignedTo)
		}
		if n.count() != 1 || n.calls[0] != (notifyCall{task.ID, "bob@example.com"}) {
			t.Errorf("notifier calls = %+v", n.calls)
		}
	})

	t.Run("returned task is a copy", func(t *testing.T) {
		svc, _ := newTestService(t)
		task := addTask(t, svc, "x", nil, "")
		got, _ := svc.AssignTask(task.ID, "a@example.com")
		got.Title = "mutated"
		stored, _ := svc.GetTask(task.ID)
		if stored.Title != "x" {
			t.Errorf("stored task mutated through returned value")
		}
	})

	t.Run("not found", func(t *testing.T) {
		svc, n := newTestService(t)
		_, err := svc.AssignTask("missing", "a@example.com")
		if !errors.Is(err, ErrTaskNotFound) || errors.Is(err, ErrInvalidEmail) {
			t.Fatalf("err = %v", err)
		}
		if err.Error() != "task with ID missing not found" {
			t.Errorf("message = %q", err.Error())
		}
		if n.count() != 0 {
			t.Error("notifier must not be called")
		}
	})

	invalid := []string{"", "   ", "not-an-email", "a@", "@example.com", "Bob <bob@example.com>", "a@example.com, b@example.com"}
	for _, email := range invalid {
		t.Run(fmt.Sprintf("invalid %q", email), func(t *testing.T) {
			svc, n := newTestService(t)
			task := addTask(t, svc, "x", nil, "")
			_, err := svc.AssignTask(task.ID, email)
			if !errors.Is(err, ErrInvalidEmail) || errors.Is(err, ErrTaskNotFound) {
				t.Fatalf("err = %v", err)
			}
			stored, _ := svc.GetTask(task.ID)
			if stored.AssignedTo != nil {
				t.Error("task must stay unassigned")
			}
			if n.count() != 0 {
				t.Error("notifier must not be called")
			}
		})
	}

	t.Run("invalid email for unknown task reports invalid email", func(t *testing.T) {
		svc, _ := newTestService(t)
		if _, err := svc.AssignTask("missing", "bad"); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("notifier failure ignored", func(t *testing.T) {
		svc, n := newTestService(t)
		n.err = errors.New("smtp down")
		task := addTask(t, svc, "x", nil, "")
		got, err := svc.AssignTask(task.ID, "a@example.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.AssignedTo == nil || *got.AssignedTo != "a@example.com" || n.count() != 1 {
			t.Errorf("assignment not applied or notifier not called")
		}
	})
}

func TestSetNilRestoresDefaults(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SetClock(nil)
	svc.SetNotifier(nil)
	task := addTask(t, svc, "x", nil, "")
	if _, err := svc.AssignTask(task.ID, "a@example.com"); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentAccess is meant to be run with -race.
func TestConcurrentAccess(t *testing.T) {
	svc, _ := newTestService(t)
	task := addTask(t, svc, "x", at(-time.Hour), "")

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			_, _ = svc.AssignTask(task.ID, fmt.Sprintf("u%d@example.com", i))
		}(i)
		go func() {
			defer wg.Done()
			_, _ = svc.ListOverdueTasks(models.ListTasksQuery{})
		}()
		go func() {
			defer wg.Done()
			_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "n", DueDate: at(-time.Minute)})
		}()
	}
	wg.Wait()
}
