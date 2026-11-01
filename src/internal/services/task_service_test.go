package services

import (
	"errors"
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

func (f *fakeNotifier) NotifyAssigned(task models.Task, assignedTo string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, task.ID+"|"+assignedTo)
	return f.err
}

func newEmptyService() *TaskService {
	s := NewTaskService()
	s.tasks = map[string]*models.Task{}
	s.SetClock(func() time.Time { return fixedNow })
	return s
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
	v := fixedNow.Add(d)
	return &v
}

func titles(r *models.ListTasksResponse) []string {
	var out []string
	for _, tk := range r.Tasks {
		out = append(out, tk.Title)
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
		setup func(t *testing.T, s *TaskService)
		want  []string
	}{
		{"empty", func(t *testing.T, s *TaskService) {}, nil},
		{"nil due date excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "nodue", nil, "")
		}, nil},
		{"future excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "future", at(time.Hour), "")
		}, nil},
		{"due exactly now excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "now", at(0), "")
		}, nil},
		{"completed and cancelled excluded", func(t *testing.T, s *TaskService) {
			addTask(t, s, "done", at(-time.Hour), models.TaskStatusCompleted)
			addTask(t, s, "cancelled", at(-time.Hour), models.TaskStatusCancelled)
		}, nil},
		{"other statuses included, ascending", func(t *testing.T, s *TaskService) {
			addTask(t, s, "c", at(-time.Hour), models.TaskStatusOnHold)
			addTask(t, s, "a", at(-72*time.Hour), models.TaskStatusInProgress)
			addTask(t, s, "b", at(-24*time.Hour), "")
		}, []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEmptyService()
			tt.setup(t, s)
			got, err := s.ListOverdueTasks(0, "")
			if err != nil {
				t.Fatal(err)
			}
			if !equal(titles(got), tt.want) || got.TotalCount != len(tt.want) {
				t.Errorf("got %v (total %d), want %v", titles(got), got.TotalCount, tt.want)
			}
		})
	}
}

func TestListOverdueTasks_TiebreakByID(t *testing.T) {
	s := newEmptyService()
	due := at(-time.Hour)
	for i := 0; i < 5; i++ {
		addTask(t, s, "t", due, "")
	}
	got, _ := s.ListOverdueTasks(0, "")
	for i := 1; i < len(got.Tasks); i++ {
		if got.Tasks[i-1].ID >= got.Tasks[i].ID {
			t.Fatalf("not ordered by ID: %s >= %s", got.Tasks[i-1].ID, got.Tasks[i].ID)
		}
	}
}

func TestListOverdueTasks_Pagination(t *testing.T) {
	s := newEmptyService()
	for i, n := range []string{"t1", "t2", "t3", "t4", "t5"} {
		addTask(t, s, n, at(-time.Duration(10-i)*time.Hour), "")
	}
	p1, _ := s.ListOverdueTasks(2, "")
	if !equal(titles(p1), []string{"t1", "t2"}) || p1.NextPageToken != "2" || p1.TotalCount != 5 {
		t.Fatalf("page1: %v token=%q total=%d", titles(p1), p1.NextPageToken, p1.TotalCount)
	}
	p2, _ := s.ListOverdueTasks(2, p1.NextPageToken)
	if !equal(titles(p2), []string{"t3", "t4"}) || p2.NextPageToken != "4" {
		t.Fatalf("page2: %v token=%q", titles(p2), p2.NextPageToken)
	}
	p3, _ := s.ListOverdueTasks(2, p2.NextPageToken)
	if !equal(titles(p3), []string{"t5"}) || p3.NextPageToken != "" {
		t.Fatalf("page3: %v token=%q", titles(p3), p3.NextPageToken)
	}
	past, _ := s.ListOverdueTasks(2, "99")
	if len(past.Tasks) != 0 || past.NextPageToken != "" {
		t.Fatalf("past end: %v", past)
	}
}

func TestListTasks_PaginationRegression(t *testing.T) {
	s := newEmptyService()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		addTask(t, s, n, nil, "")
		time.Sleep(time.Millisecond)
	}
	q := models.ListTasksQuery{PageSize: 2, SortOrder: models.SortOrderCreatedAtAsc}
	p1, _ := s.ListTasks(q)
	if !equal(titles(p1), []string{"a", "b"}) || p1.NextPageToken != "2" || p1.TotalCount != 5 {
		t.Fatalf("page1: %v %q %d", titles(p1), p1.NextPageToken, p1.TotalCount)
	}
	q.PageToken = p1.NextPageToken
	p2, _ := s.ListTasks(q)
	q.PageToken = p2.NextPageToken
	p3, _ := s.ListTasks(q)
	if !equal(titles(p2), []string{"c", "d"}) || !equal(titles(p3), []string{"e"}) || p3.NextPageToken != "" {
		t.Fatalf("p2=%v p3=%v token=%q", titles(p2), titles(p3), p3.NextPageToken)
	}
	big, _ := s.ListTasks(models.ListTasksQuery{PageSize: 1000})
	if len(big.Tasks) != 5 {
		t.Fatalf("page size cap: %d", len(big.Tasks))
	}
	def, _ := s.ListTasks(models.ListTasksQuery{})
	if len(def.Tasks) != 5 {
		t.Fatalf("default page size: %d", len(def.Tasks))
	}
}

func TestAssignTask(t *testing.T) {
	tests := []struct {
		name    string
		id      func(*models.Task) string
		email   string
		wantErr error // nil = success
		notFind bool
	}{
		{"success", func(tk *models.Task) string { return tk.ID }, "alice@example.com", nil, false},
		{"not found", func(*models.Task) string { return "missing" }, "alice@example.com", nil, true},
		{"empty email", func(tk *models.Task) string { return tk.ID }, "", ErrInvalidEmail, false},
		{"no at sign", func(tk *models.Task) string { return tk.ID }, "alice", ErrInvalidEmail, false},
		{"display name rejected", func(tk *models.Task) string { return tk.ID }, "Alice <alice@example.com>", ErrInvalidEmail, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEmptyService()
			n := &fakeNotifier{}
			s.SetNotifier(n)
			task := addTask(t, s, "x", nil, "")
			got, err := s.AssignTask(tt.id(task), tt.email)
			switch {
			case tt.notFind:
				if err == nil || errors.Is(err, ErrInvalidEmail) {
					t.Fatalf("want not-found error, got %v", err)
				}
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want %v, got %v", tt.wantErr, err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if got.AssignedTo == nil || *got.AssignedTo != tt.email {
					t.Fatalf("assigned_to = %v", got.AssignedTo)
				}
				stored, _ := s.GetTask(task.ID)
				if *stored.AssignedTo != tt.email {
					t.Fatal("not persisted")
				}
			}
			wantCalls := 0
			if tt.wantErr == nil && !tt.notFind {
				wantCalls = 1
			}
			if len(n.calls) != wantCalls {
				t.Fatalf("notifier calls = %v", n.calls)
			}
		})
	}
}

func TestAssignTask_NotifierFailureIgnored(t *testing.T) {
	s := newEmptyService()
	s.SetNotifier(&fakeNotifier{err: errors.New("boom")})
	task := addTask(t, s, "x", nil, "")
	if _, err := s.AssignTask(task.ID, "bob@example.com"); err != nil {
		t.Fatalf("notifier failure must not fail assignment: %v", err)
	}
}

func TestAssignTask_Concurrent(t *testing.T) {
	s := newEmptyService()
	task := addTask(t, s, "x", nil, "")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); s.AssignTask(task.ID, "a@example.com") }()
		go func() { defer wg.Done(); s.ListOverdueTasks(0, "") }()
	}
	wg.Wait()
}
