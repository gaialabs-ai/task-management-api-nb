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

type fakeNotifier struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, task.ID+"|"+assignee)
	return f.err
}

// newEmptyService returns a service with no sample data and a fixed clock.
func newEmptyService() (*TaskService, *fakeNotifier) {
	s := NewTaskService()
	s.tasks = map[string]*models.Task{}
	s.SetClock(func() time.Time { return fixedNow })
	n := &fakeNotifier{}
	s.SetNotifier(n)
	return s, n
}

func addTask(t *testing.T, s *TaskService, title string, due *time.Time, status models.TaskStatus) *models.Task {
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

func at(d time.Duration) *time.Time {
	t := fixedNow.Add(d)
	return &t
}

func titles(r *models.ListTasksResponse) []string {
	var out []string
	for _, t := range r.Tasks {
		out = append(out, t.Title)
	}
	return out
}

func TestListOverdueTasks(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, s *TaskService)
		want []string
	}{
		{"empty", func(t *testing.T, s *TaskService) {}, nil},
		{"nil due date excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "nodue", nil, "")
		}, nil},
		{"future and exactly-now excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "future", at(time.Hour), "")
			addTask(t, s, "now", at(0), "")
		}, nil},
		{"completed and cancelled excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "done", at(-time.Hour), models.TaskStatusCompleted)
			addTask(t, s, "cancelled", at(-time.Hour), models.TaskStatusCancelled)
		}, nil},
		{"other statuses included", func(t *testing.T, s *TaskService) {
			addTask(t, s, "pending", at(-4*time.Hour), "")
			addTask(t, s, "progress", at(-3*time.Hour), models.TaskStatusInProgress)
			addTask(t, s, "hold", at(-2*time.Hour), models.TaskStatusOnHold)
		}, []string{"pending", "progress", "hold"}},
		{"sorted by due date ascending", func(t *testing.T, s *TaskService) {
			addTask(t, s, "c", at(-time.Hour), "")
			addTask(t, s, "a", at(-72*time.Hour), "")
			addTask(t, s, "b", at(-24*time.Hour), "")
		}, []string{"a", "b", "c"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newEmptyService()
			tc.seed(t, s)
			resp, err := s.ListOverdueTasks(models.ListTasksQuery{PageSize: 20})
			if err != nil {
				t.Fatal(err)
			}
			got := titles(resp)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			if resp.TotalCount != len(tc.want) {
				t.Fatalf("total %d want %d", resp.TotalCount, len(tc.want))
			}
		})
	}
}

func TestListOverdueTasksTiebreakByID(t *testing.T) {
	s, _ := newEmptyService()
	due := at(-time.Hour)
	for i := 0; i < 10; i++ {
		addTask(t, s, fmt.Sprint(i), due, "")
	}
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{PageSize: 100})
	for i := 1; i < len(resp.Tasks); i++ {
		if resp.Tasks[i-1].ID >= resp.Tasks[i].ID {
			t.Fatalf("ties not ordered by ID: %s >= %s", resp.Tasks[i-1].ID, resp.Tasks[i].ID)
		}
	}
}

func TestListOverdueTasksPagination(t *testing.T) {
	s, _ := newEmptyService()
	for i := 0; i < 5; i++ {
		addTask(t, s, fmt.Sprint(i), at(-time.Duration(10-i)*time.Hour), "")
	}
	var got []string
	token := ""
	pages := 0
	for {
		resp, err := s.ListOverdueTasks(models.ListTasksQuery{PageSize: 2, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		if resp.TotalCount != 5 {
			t.Fatalf("total %d", resp.TotalCount)
		}
		got = append(got, titles(resp)...)
		pages++
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	if pages != 3 || fmt.Sprint(got) != "[0 1 2 3 4]" {
		t.Fatalf("pages=%d got=%v", pages, got)
	}
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{PageSize: 2, PageToken: "99"})
	if len(resp.Tasks) != 0 || resp.NextPageToken != "" {
		t.Fatalf("out-of-range token: %+v", resp)
	}
}

func TestAssignTask(t *testing.T) {
	tests := []struct {
		name    string
		id      string // "" = use a real task
		email   string
		wantErr error
	}{
		{"success", "", "alice@example.com", nil},
		{"not found", "missing", "alice@example.com", ErrTaskNotFound},
		{"empty email", "", "", ErrInvalidEmail},
		{"no at sign", "", "alice", ErrInvalidEmail},
		{"display name rejected", "", "Alice <alice@example.com>", ErrInvalidEmail},
		{"trailing garbage", "", "alice@example.com, bob@example.com", ErrInvalidEmail},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, n := newEmptyService()
			task := addTask(t, s, "t", nil, "")
			id := tc.id
			if id == "" {
				id = task.ID
			}
			got, err := s.AssignTask(id, tc.email)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err %v want %v", err, tc.wantErr)
				}
				if len(n.calls) != 0 {
					t.Fatal("notifier must not be called on error")
				}
				stored, _ := s.GetTask(task.ID)
				if stored.AssignedTo != nil {
					t.Fatal("task must not be modified on error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.AssignedTo == nil || *got.AssignedTo != tc.email {
				t.Fatalf("assigned_to %v", got.AssignedTo)
			}
			if !got.UpdatedAt.Equal(fixedNow) {
				t.Fatalf("updated_at %v", got.UpdatedAt)
			}
			if len(n.calls) != 1 || n.calls[0] != task.ID+"|"+tc.email {
				t.Fatalf("notifier calls %v", n.calls)
			}
		})
	}
}

func TestAssignTaskNotifierFailureDoesNotFail(t *testing.T) {
	s, n := newEmptyService()
	n.err = errors.New("smtp down")
	task := addTask(t, s, "t", nil, "")
	got, err := s.AssignTask(task.ID, "bob@example.com")
	if err != nil || got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
		t.Fatalf("got %+v err %v", got, err)
	}
	if len(n.calls) != 1 {
		t.Fatalf("calls %v", n.calls)
	}
}

func TestListTasksPaginationRegression(t *testing.T) {
	s, _ := newEmptyService()
	for i := 0; i < 5; i++ {
		addTask(t, s, fmt.Sprint(i), nil, "")
		time.Sleep(time.Millisecond)
	}
	q := models.ListTasksQuery{PageSize: 2, SortOrder: models.SortOrderCreatedAtAsc}
	r1, _ := s.ListTasks(q)
	if fmt.Sprint(titles(r1)) != "[0 1]" || r1.NextPageToken != "2" || r1.TotalCount != 5 {
		t.Fatalf("page1 %v %q %d", titles(r1), r1.NextPageToken, r1.TotalCount)
	}
	q.PageToken = r1.NextPageToken
	r2, _ := s.ListTasks(q)
	if fmt.Sprint(titles(r2)) != "[2 3]" || r2.NextPageToken != "4" {
		t.Fatalf("page2 %v %q", titles(r2), r2.NextPageToken)
	}
	q.PageToken = r2.NextPageToken
	r3, _ := s.ListTasks(q)
	if fmt.Sprint(titles(r3)) != "[4]" || r3.NextPageToken != "" {
		t.Fatalf("page3 %v %q", titles(r3), r3.NextPageToken)
	}
	// page size defaults to 20 and is capped at 100; invalid token falls back to start
	r4, _ := s.ListTasks(models.ListTasksQuery{PageToken: "abc"})
	if len(r4.Tasks) != 5 || r4.NextPageToken != "" {
		t.Fatalf("default page %d", len(r4.Tasks))
	}
	r5, _ := s.ListTasks(models.ListTasksQuery{PageSize: 500})
	if len(r5.Tasks) != 5 {
		t.Fatalf("capped page %d", len(r5.Tasks))
	}
}

func TestOverdueAndAssignConcurrent(t *testing.T) {
	s, _ := newEmptyService()
	task := addTask(t, s, "t", at(-time.Hour), "")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = s.ListOverdueTasks(models.ListTasksQuery{}) }()
		go func() { defer wg.Done(); _, _ = s.AssignTask(task.ID, "a@example.com") }()
	}
	wg.Wait()
}
