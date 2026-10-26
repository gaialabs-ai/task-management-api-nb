package services

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// baseNow is the fixed "current time" used by most tests.
var baseNow = time.Date(2024, time.June, 15, 12, 0, 0, 0, time.UTC)

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

type notifyCall struct {
	task     models.Task
	assignee string
}

// fakeNotifier records NotifyAssignment calls and can return a configured error.
type fakeNotifier struct {
	mu    sync.Mutex
	calls []notifyCall
	err   error
	// hook, when non-nil, runs inside NotifyAssignment (before returning).
	hook func()
}

func (f *fakeNotifier) NotifyAssignment(task models.Task, assignee string) error {
	f.mu.Lock()
	f.calls = append(f.calls, notifyCall{task: task, assignee: assignee})
	err := f.err
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return err
}

func (f *fakeNotifier) snapshot() []notifyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]notifyCall(nil), f.calls...)
}

// newEmptyService returns a service with a fixed clock, a fake notifier and no
// tasks (the sample data seeded by NewTaskService is removed).
func newEmptyService(t *testing.T, now time.Time) (*TaskService, *fakeNotifier) {
	t.Helper()
	svc := NewTaskService()
	svc.mu.Lock()
	svc.tasks = make(map[string]*models.Task)
	svc.mu.Unlock()
	svc.SetClock(fixedClock(now))
	n := &fakeNotifier{}
	svc.SetNotifier(n)
	return svc, n
}

// createAt creates a task with CreatedAt == at, using the service clock.
func createAt(t *testing.T, svc *TaskService, at time.Time, req models.CreateTaskRequest) *models.Task {
	t.Helper()
	svc.SetClock(fixedClock(at))
	task, err := svc.CreateTask(req)
	if err != nil {
		t.Fatalf("CreateTask(%q): %v", req.Title, err)
	}
	return task
}

// setID re-keys a stored task so tests get deterministic ID ordering.
func setID(svc *TaskService, task *models.Task, id string) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	delete(svc.tasks, task.ID)
	task.ID = id
	svc.tasks[id] = task
}

func timePtr(t time.Time) *time.Time { return &t }

func titles(tasks []models.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.Title)
	}
	return out
}

func ids(tasks []models.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.ID)
	}
	return out
}

// storedCopy returns a deep copy of the stored task with the given ID.
func storedCopy(t *testing.T, svc *TaskService, id string) models.Task {
	t.Helper()
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	task, ok := svc.tasks[id]
	if !ok {
		t.Fatalf("task %s not stored", id)
	}
	return cloneTask(*task)
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevW := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prevW) })
	return &buf
}

// ---------------------------------------------------------------------------
// ListOverdueTasks
// ---------------------------------------------------------------------------

type overdueSpec struct {
	title  string
	due    *time.Duration // offset from baseNow; nil means no due date
	status models.TaskStatus
}

func off(d time.Duration) *time.Duration { return &d }

func TestListOverdueTasks_Selection(t *testing.T) {
	hour := time.Hour
	tests := []struct {
		name  string
		tasks []overdueSpec
		want  []string
	}{
		{
			name: "no tasks",
			want: []string{},
		},
		{
			name:  "nil due date excluded",
			tasks: []overdueSpec{{"nodue", nil, models.TaskStatusPending}},
			want:  []string{},
		},
		{
			name:  "future due date excluded",
			tasks: []overdueSpec{{"future", off(hour), models.TaskStatusPending}},
			want:  []string{},
		},
		{
			name:  "due one nanosecond in the future excluded",
			tasks: []overdueSpec{{"future-ns", off(time.Nanosecond), models.TaskStatusPending}},
			want:  []string{},
		},
		{
			name:  "due date equal to now excluded (strictly before)",
			tasks: []overdueSpec{{"now", off(0), models.TaskStatusPending}},
			want:  []string{},
		},
		{
			name:  "due one nanosecond before now included",
			tasks: []overdueSpec{{"past-ns", off(-time.Nanosecond), models.TaskStatusPending}},
			want:  []string{"past-ns"},
		},
		{
			name:  "completed excluded",
			tasks: []overdueSpec{{"done", off(-hour), models.TaskStatusCompleted}},
			want:  []string{},
		},
		{
			name:  "cancelled excluded",
			tasks: []overdueSpec{{"cancelled", off(-hour), models.TaskStatusCancelled}},
			want:  []string{},
		},
		{
			name:  "pending included",
			tasks: []overdueSpec{{"pending", off(-hour), models.TaskStatusPending}},
			want:  []string{"pending"},
		},
		{
			name:  "in_progress included",
			tasks: []overdueSpec{{"in_progress", off(-hour), models.TaskStatusInProgress}},
			want:  []string{"in_progress"},
		},
		{
			name:  "on_hold included",
			tasks: []overdueSpec{{"on_hold", off(-hour), models.TaskStatusOnHold}},
			want:  []string{"on_hold"},
		},
		{
			name: "mixed set sorted by due date ascending",
			tasks: []overdueSpec{
				{"c-1h", off(-1 * hour), models.TaskStatusPending},
				{"a-72h", off(-72 * hour), models.TaskStatusOnHold},
				{"nodue", nil, models.TaskStatusPending},
				{"future", off(2 * hour), models.TaskStatusInProgress},
				{"now", off(0), models.TaskStatusPending},
				{"done", off(-100 * hour), models.TaskStatusCompleted},
				{"cancelled", off(-200 * hour), models.TaskStatusCancelled},
				{"b-24h", off(-24 * hour), models.TaskStatusInProgress},
			},
			want: []string{"a-72h", "b-24h", "c-1h"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newEmptyService(t, baseNow)
			for i, spec := range tc.tasks {
				req := models.CreateTaskRequest{Title: spec.title}
				if spec.due != nil {
					req.DueDate = timePtr(baseNow.Add(*spec.due))
				}
				created := createAt(t, svc, baseNow.Add(-time.Duration(len(tc.tasks)-i)*time.Minute), req)
				if spec.status != models.TaskStatusPending {
					if _, err := svc.UpdateTaskStatus(created.ID, spec.status); err != nil {
						t.Fatalf("UpdateTaskStatus: %v", err)
					}
				}
			}
			svc.SetClock(fixedClock(baseNow))

			resp, err := svc.ListOverdueTasks(models.ListTasksQuery{})
			if err != nil {
				t.Fatalf("ListOverdueTasks: %v", err)
			}
			if resp.Tasks == nil {
				t.Fatal("Tasks must be non-nil")
			}
			if got := titles(resp.Tasks); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("overdue titles = %v, want %v", got, tc.want)
			}
			if resp.TotalCount != len(tc.want) {
				t.Fatalf("TotalCount = %d, want %d", resp.TotalCount, len(tc.want))
			}
			if resp.NextPageToken != "" {
				t.Fatalf("NextPageToken = %q, want empty", resp.NextPageToken)
			}
		})
	}
}

func TestListOverdueTasks_UsesServiceClock(t *testing.T) {
	svc, _ := newEmptyService(t, baseNow)
	due := baseNow.Add(-time.Hour)
	createAt(t, svc, baseNow.Add(-48*time.Hour), models.CreateTaskRequest{Title: "t", DueDate: &due})

	tests := []struct {
		name string
		now  time.Time
		want int
	}{
		{"clock before due date", due.Add(-time.Second), 0},
		{"clock equal to due date", due, 0},
		{"clock just after due date", due.Add(time.Nanosecond), 1},
		{"clock far in the future", due.Add(1000 * time.Hour), 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc.SetClock(fixedClock(tc.now))
			resp, err := svc.ListOverdueTasks(models.ListTasksQuery{})
			if err != nil {
				t.Fatal(err)
			}
			if resp.TotalCount != tc.want || len(resp.Tasks) != tc.want {
				t.Fatalf("total=%d len=%d, want %d", resp.TotalCount, len(resp.Tasks), tc.want)
			}
		})
	}
}

func TestListOverdueTasks_SeededSampleTasksNotOverdue(t *testing.T) {
	svc := NewTaskService()
	if got := len(svc.tasks); got != 2 {
		t.Fatalf("expected 2 seeded tasks, got %d", got)
	}
	svc.SetClock(fixedClock(baseNow.Add(1000 * time.Hour)))
	resp, err := svc.ListOverdueTasks(models.ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Tasks == nil || len(resp.Tasks) != 0 || resp.TotalCount != 0 || resp.NextPageToken != "" {
		t.Fatalf("seeded tasks have no due date and must not be overdue: %+v", resp)
	}

	// Adding one overdue task alongside the seeds yields exactly that task.
	createAt(t, svc, baseNow, models.CreateTaskRequest{Title: "late", DueDate: timePtr(baseNow.Add(time.Hour))})
	svc.SetClock(fixedClock(baseNow.Add(1000 * time.Hour)))
	resp, err = svc.ListOverdueTasks(models.ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(resp.Tasks); !reflect.DeepEqual(got, []string{"late"}) || resp.TotalCount != 1 {
		t.Fatalf("got %v total %d", got, resp.TotalCount)
	}
}

func TestListOverdueTasks_Ordering(t *testing.T) {
	t0 := baseNow.Add(-10 * time.Hour)
	t1 := baseNow.Add(-9 * time.Hour)
	t2 := baseNow.Add(-8 * time.Hour)
	d1 := baseNow.Add(-5 * time.Hour)
	d2 := baseNow.Add(-4 * time.Hour)

	type spec struct {
		id      string
		created time.Time
		due     time.Time
	}

	tests := []struct {
		name  string
		specs []spec
		want  []string // IDs in expected order
	}{
		{
			name: "due date ascending regardless of creation order",
			specs: []spec{
				{"id-a", t0, d2},
				{"id-b", t1, d1},
			},
			want: []string{"id-b", "id-a"},
		},
		{
			name: "equal due dates tie broken by CreatedAt (ID order opposite)",
			specs: []spec{
				{"id-a", t2, d1},
				{"id-b", t0, d1},
				{"id-c", t1, d1},
			},
			want: []string{"id-b", "id-c", "id-a"},
		},
		{
			name: "equal due and CreatedAt tie broken by ID",
			specs: []spec{
				{"id-c", t0, d1},
				{"id-a", t0, d1},
				{"id-b", t0, d1},
			},
			want: []string{"id-a", "id-b", "id-c"},
		},
		{
			name: "due date dominates CreatedAt and ID",
			specs: []spec{
				{"id-a", t2, d2},
				{"id-b", t0, d2},
				{"id-c", t1, d1},
				{"id-d", t2, d1},
			},
			want: []string{"id-c", "id-d", "id-b", "id-a"},
		},
		{
			name: "same instant in a different time zone counts as equal due date",
			specs: []spec{
				{"id-a", t1, d1.In(time.FixedZone("UTC+9", 9*3600))},
				{"id-b", t0, d1},
			},
			want: []string{"id-b", "id-a"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newEmptyService(t, baseNow)
			for _, s := range tc.specs {
				due := s.due
				task := createAt(t, svc, s.created, models.CreateTaskRequest{Title: s.id, DueDate: &due})
				setID(svc, task, s.id)
			}
			svc.SetClock(fixedClock(baseNow))
			// Repeat to guard against map-iteration-order flakiness.
			for i := 0; i < 20; i++ {
				resp, err := svc.ListOverdueTasks(models.ListTasksQuery{})
				if err != nil {
					t.Fatal(err)
				}
				if got := ids(resp.Tasks); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("iteration %d: order = %v, want %v", i, got, tc.want)
				}
			}
		})
	}
}

func TestListOverdueTasks_RandomIDTieBreakIsDeterministic(t *testing.T) {
	svc, _ := newEmptyService(t, baseNow)
	due := baseNow.Add(-time.Hour)
	created := baseNow.Add(-2 * time.Hour)
	var wantIDs []string
	for i := 0; i < 8; i++ {
		task := createAt(t, svc, created, models.CreateTaskRequest{Title: fmt.Sprintf("t%d", i), DueDate: timePtr(due)})
		wantIDs = append(wantIDs, task.ID)
	}
	sort.Strings(wantIDs)
	svc.SetClock(fixedClock(baseNow))
	for i := 0; i < 10; i++ {
		resp, err := svc.ListOverdueTasks(models.ListTasksQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(resp.Tasks); !reflect.DeepEqual(got, wantIDs) {
			t.Fatalf("order = %v, want %v", got, wantIDs)
		}
	}
}

func TestListOverdueTasks_Pagination(t *testing.T) {
	// seed creates n overdue tasks with strictly increasing due dates, so that
	// task i is the i-th overdue task in the result order.
	seed := func(t *testing.T, n int) *TaskService {
		svc, _ := newEmptyService(t, baseNow)
		for i := 0; i < n; i++ {
			due := baseNow.Add(-time.Duration(n-i) * time.Hour)
			createAt(t, svc, baseNow.Add(-1000*time.Hour), models.CreateTaskRequest{
				Title:   fmt.Sprintf("task-%03d", i),
				DueDate: &due,
			})
		}
		svc.SetClock(fixedClock(baseNow))
		return svc
	}
	wantTitles := func(from, to int) []string {
		out := []string{}
		for i := from; i < to; i++ {
			out = append(out, fmt.Sprintf("task-%03d", i))
		}
		return out
	}

	tests := []struct {
		name      string
		total     int
		query     models.ListTasksQuery
		wantRange [2]int
		wantNext  string
	}{
		{"default page size is 20", 25, models.ListTasksQuery{}, [2]int{0, 20}, "20"},
		{"default page size when negative", 25, models.ListTasksQuery{PageSize: -3}, [2]int{0, 20}, "20"},
		{"explicit page size", 25, models.ListTasksQuery{PageSize: 10}, [2]int{0, 10}, "10"},
		{"second page via token", 25, models.ListTasksQuery{PageSize: 10, PageToken: "10"}, [2]int{10, 20}, "20"},
		{"last page has empty next token", 25, models.ListTasksQuery{PageSize: 10, PageToken: "20"}, [2]int{20, 25}, ""},
		{"exact fit has empty next token", 10, models.ListTasksQuery{PageSize: 10}, [2]int{0, 10}, ""},
		{"page size larger than total", 5, models.ListTasksQuery{PageSize: 50}, [2]int{0, 5}, ""},
		{"page size capped at 100", 120, models.ListTasksQuery{PageSize: 500}, [2]int{0, 100}, "100"},
		{"capped page 2", 120, models.ListTasksQuery{PageSize: 500, PageToken: "100"}, [2]int{100, 120}, ""},
		{"non-numeric token treated as 0", 25, models.ListTasksQuery{PageSize: 5, PageToken: "abc"}, [2]int{0, 5}, "5"},
		{"negative token treated as 0", 25, models.ListTasksQuery{PageSize: 5, PageToken: "-7"}, [2]int{0, 5}, "5"},
		{"float token treated as 0", 25, models.ListTasksQuery{PageSize: 5, PageToken: "1.5"}, [2]int{0, 5}, "5"},
		{"zero token", 25, models.ListTasksQuery{PageSize: 5, PageToken: "0"}, [2]int{0, 5}, "5"},
		{"token past the end yields empty page", 25, models.ListTasksQuery{PageSize: 5, PageToken: "1000"}, [2]int{0, 0}, ""},
		{"token equal to total yields empty page", 25, models.ListTasksQuery{PageSize: 5, PageToken: "25"}, [2]int{0, 0}, ""},
		{"empty result", 0, models.ListTasksQuery{PageSize: 5}, [2]int{0, 0}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := seed(t, tc.total)
			resp, err := svc.ListOverdueTasks(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if resp.Tasks == nil {
				t.Fatal("Tasks must be non-nil")
			}
			want := wantTitles(tc.wantRange[0], tc.wantRange[1])
			if got := titles(resp.Tasks); !reflect.DeepEqual(got, want) {
				t.Fatalf("titles = %v, want %v", got, want)
			}
			if resp.NextPageToken != tc.wantNext {
				t.Fatalf("NextPageToken = %q, want %q", resp.NextPageToken, tc.wantNext)
			}
			if resp.TotalCount != tc.total {
				t.Fatalf("TotalCount = %d, want %d (full overdue count)", resp.TotalCount, tc.total)
			}
		})
	}

	t.Run("walking all pages visits every task exactly once", func(t *testing.T) {
		svc := seed(t, 23)
		var got []string
		token := ""
		for pages := 0; pages < 10; pages++ {
			resp, err := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 7, PageToken: token})
			if err != nil {
				t.Fatal(err)
			}
			if resp.TotalCount != 23 {
				t.Fatalf("TotalCount = %d", resp.TotalCount)
			}
			got = append(got, titles(resp.Tasks)...)
			token = resp.NextPageToken
			if token == "" {
				break
			}
		}
		if token != "" {
			t.Fatal("pagination did not terminate")
		}
		if want := wantTitles(0, 23); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("total count ignores non-overdue tasks", func(t *testing.T) {
		svc := seed(t, 6)
		createAt(t, svc, baseNow, models.CreateTaskRequest{Title: "future", DueDate: timePtr(baseNow.Add(time.Hour))})
		createAt(t, svc, baseNow, models.CreateTaskRequest{Title: "nodue"})
		svc.SetClock(fixedClock(baseNow))
		resp, err := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 4})
		if err != nil {
			t.Fatal(err)
		}
		if resp.TotalCount != 6 || len(resp.Tasks) != 4 || resp.NextPageToken != "4" {
			t.Fatalf("total=%d len=%d next=%q", resp.TotalCount, len(resp.Tasks), resp.NextPageToken)
		}
	})
}

func TestListOverdueTasks_ReturnsCopies(t *testing.T) {
	svc, _ := newEmptyService(t, baseNow)
	assignee := "owner@example.com"
	due := baseNow.Add(-time.Hour)
	completed := baseNow.Add(-30 * time.Minute)
	task := createAt(t, svc, baseNow.Add(-2*time.Hour), models.CreateTaskRequest{
		Title:      "orig",
		Tags:       []string{"x", "y"},
		AssignedTo: &assignee,
		DueDate:    &due,
	})
	svc.mu.Lock()
	task.CompletedAt = &completed
	svc.mu.Unlock()
	svc.SetClock(fixedClock(baseNow))

	before := storedCopy(t, svc, task.ID)

	resp, err := svc.ListOverdueTasks(models.ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Tasks) != 1 {
		t.Fatalf("len = %d", len(resp.Tasks))
	}
	got := &resp.Tasks[0]
	got.Title = "mutated"
	got.Status = models.TaskStatusCompleted
	got.Tags[0] = "mutated"
	*got.AssignedTo = "mutated@example.com"
	*got.DueDate = baseNow.Add(1000 * time.Hour)
	*got.CompletedAt = baseNow.Add(2000 * time.Hour)
	resp.Tasks[0] = models.Task{}

	after := storedCopy(t, svc, task.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("stored task changed by mutating returned copy:\nbefore=%+v\nafter=%+v", before, after)
	}

	// A second listing still sees the original data.
	resp2, err := svc.ListOverdueTasks(models.ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp2.Tasks) != 1 || resp2.Tasks[0].Title != "orig" || resp2.Tasks[0].Tags[0] != "x" ||
		*resp2.Tasks[0].AssignedTo != assignee {
		t.Fatalf("unexpected second listing: %+v", resp2.Tasks)
	}
}

// ---------------------------------------------------------------------------
// paginateTasks
// ---------------------------------------------------------------------------

func TestPaginateTasks(t *testing.T) {
	makeTasks := func(n int) []*models.Task {
		out := make([]*models.Task, n)
		for i := range out {
			out[i] = &models.Task{ID: strconv.Itoa(i), Title: fmt.Sprintf("t%d", i)}
		}
		return out
	}

	tests := []struct {
		name      string
		n         int
		pageSize  int
		pageToken string
		wantFirst int // index of first expected ID; ignored when wantLen == 0
		wantLen   int
		wantNext  string
	}{
		{"empty input", 0, 10, "", 0, 0, ""},
		{"empty input with token", 0, 10, "5", 0, 0, ""},
		{"zero page size defaults to 20", 30, 0, "", 0, 20, "20"},
		{"negative page size defaults to 20", 30, -1, "", 0, 20, "20"},
		{"page size 1", 3, 1, "", 0, 1, "1"},
		{"page size 1 last", 3, 1, "2", 2, 1, ""},
		{"page size exactly 100", 150, 100, "", 0, 100, "100"},
		{"page size 101 capped to 100", 150, 101, "", 0, 100, "100"},
		{"page size huge capped to 100", 150, 1 << 30, "", 0, 100, "100"},
		{"page size equals remaining", 10, 5, "5", 5, 5, ""},
		{"partial last page", 10, 4, "8", 8, 2, ""},
		{"middle page", 10, 3, "3", 3, 3, "6"},
		{"empty token starts at 0", 10, 3, "", 0, 3, "3"},
		{"invalid token treated as 0", 10, 3, "garbage", 0, 3, "3"},
		{"negative token treated as 0", 10, 3, "-2", 0, 3, "3"},
		{"zero token", 10, 3, "0", 0, 3, "3"},
		{"token with whitespace is invalid -> 0", 10, 3, " 3", 0, 3, "3"},
		{"token at end", 10, 3, "10", 0, 0, ""},
		{"token beyond end", 10, 3, "999", 0, 0, ""},
		{"overflowing token treated as 0", 10, 3, "99999999999999999999999", 0, 3, "3"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := makeTasks(tc.n)
			page, next := paginateTasks(in, tc.pageSize, tc.pageToken)
			if len(page) != tc.wantLen {
				t.Fatalf("len(page) = %d, want %d", len(page), tc.wantLen)
			}
			if tc.wantLen == 0 && page != nil {
				t.Fatalf("empty page should be nil, got %#v", page)
			}
			if next != tc.wantNext {
				t.Fatalf("next = %q, want %q", next, tc.wantNext)
			}
			for i, task := range page {
				if want := strconv.Itoa(tc.wantFirst + i); task.ID != want {
					t.Fatalf("page[%d].ID = %s, want %s", i, task.ID, want)
				}
			}
		})
	}

	t.Run("returns value copies of the tasks", func(t *testing.T) {
		in := makeTasks(3)
		page, _ := paginateTasks(in, 3, "")
		page[0].Title = "changed"
		if in[0].Title != "t0" {
			t.Fatalf("input task mutated through page: %q", in[0].Title)
		}
	})
}

// ---------------------------------------------------------------------------
// AssignTask
// ---------------------------------------------------------------------------

func TestAssignTask_Success(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain address", "user@example.com", "user@example.com"},
		{"leading and trailing spaces", "  user@example.com  ", "user@example.com"},
		{"tabs and newlines", "\t user@example.com\n", "user@example.com"},
		{"plus tag and subdomain", "first.last+tag@sub.example.co.uk", "first.last+tag@sub.example.co.uk"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, notifier := newEmptyService(t, baseNow)
			created := createAt(t, svc, baseNow.Add(-time.Hour), models.CreateTaskRequest{
				Title: "work",
				Tags:  []string{"a", "b"},
			})
			createdAt := created.CreatedAt
			assignNow := baseNow.Add(5 * time.Minute)
			svc.SetClock(fixedClock(assignNow))

			got, err := svc.AssignTask(created.ID, tc.input)
			if err != nil {
				t.Fatalf("AssignTask: %v", err)
			}
			if got.AssignedTo == nil || *got.AssignedTo != tc.want {
				t.Fatalf("AssignedTo = %v, want %q", got.AssignedTo, tc.want)
			}
			if !got.UpdatedAt.Equal(assignNow) {
				t.Fatalf("UpdatedAt = %v, want clock now %v", got.UpdatedAt, assignNow)
			}
			if !got.CreatedAt.Equal(createdAt) {
				t.Fatalf("CreatedAt changed: %v", got.CreatedAt)
			}
			if got.ID != created.ID || got.Title != "work" {
				t.Fatalf("unexpected task: %+v", got)
			}

			stored := storedCopy(t, svc, created.ID)
			if stored.AssignedTo == nil || *stored.AssignedTo != tc.want || !stored.UpdatedAt.Equal(assignNow) {
				t.Fatalf("stored task not updated: %+v", stored)
			}

			calls := notifier.snapshot()
			if len(calls) != 1 {
				t.Fatalf("notifier called %d times, want 1", len(calls))
			}
			if calls[0].assignee != tc.want {
				t.Fatalf("notifier assignee = %q, want %q", calls[0].assignee, tc.want)
			}
			if calls[0].task.ID != created.ID || calls[0].task.AssignedTo == nil ||
				*calls[0].task.AssignedTo != tc.want || !calls[0].task.UpdatedAt.Equal(assignNow) {
				t.Fatalf("notifier received wrong task: %+v", calls[0].task)
			}
		})
	}
}

func TestAssignTask_ReassignOverwrites(t *testing.T) {
	svc, notifier := newEmptyService(t, baseNow)
	prev := "old@example.com"
	created := createAt(t, svc, baseNow, models.CreateTaskRequest{Title: "x", AssignedTo: &prev})

	if _, err := svc.AssignTask(created.ID, "new@example.com"); err != nil {
		t.Fatal(err)
	}
	stored := storedCopy(t, svc, created.ID)
	if stored.AssignedTo == nil || *stored.AssignedTo != "new@example.com" {
		t.Fatalf("AssignedTo = %v", stored.AssignedTo)
	}
	if len(notifier.snapshot()) != 1 {
		t.Fatalf("notifier calls = %d", len(notifier.snapshot()))
	}
}

func TestAssignTask_ResultAndNotifiedTaskAreCopies(t *testing.T) {
	svc, notifier := newEmptyService(t, baseNow)
	due := baseNow.Add(-time.Hour)
	created := createAt(t, svc, baseNow, models.CreateTaskRequest{
		Title:   "x",
		Tags:    []string{"keep"},
		DueDate: &due,
	})

	got, err := svc.AssignTask(created.ID, "user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	before := storedCopy(t, svc, created.ID)

	got.Title = "mutated"
	got.Tags[0] = "mutated"
	*got.AssignedTo = "mutated@example.com"
	*got.DueDate = baseNow.Add(500 * time.Hour)

	calls := notifier.snapshot()
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	calls[0].task.Title = "mutated"
	calls[0].task.Tags[0] = "mutated"
	*calls[0].task.AssignedTo = "mutated@example.com"

	after := storedCopy(t, svc, created.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("stored task changed through returned/notified copy:\nbefore=%+v\nafter=%+v", before, after)
	}
	// Result and notified task must not alias each other either.
	if got.AssignedTo == calls[0].task.AssignedTo {
		t.Fatal("result and notified task share AssignedTo pointer")
	}
}

func TestAssignTask_NotFound(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"unknown id", "does-not-exist"},
		{"empty id", ""},
		{"id with different case", "ABC"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, notifier := newEmptyService(t, baseNow)
			existing := createAt(t, svc, baseNow, models.CreateTaskRequest{Title: "existing"})
			setID(svc, existing, "abc")
			before := storedCopy(t, svc, "abc")

			got, err := svc.AssignTask(tc.id, "user@example.com")
			if err == nil {
				t.Fatal("expected error")
			}
			if got != nil {
				t.Fatalf("expected nil task, got %+v", got)
			}
			if !errors.Is(err, ErrTaskNotFound) {
				t.Fatalf("errors.Is(err, ErrTaskNotFound) = false; err = %v", err)
			}
			if errors.Is(err, ErrInvalidEmail) {
				t.Fatalf("not-found error must not match ErrInvalidEmail: %v", err)
			}
			if !strings.Contains(err.Error(), tc.id) {
				t.Fatalf("error %q should mention ID %q", err, tc.id)
			}
			if n := len(notifier.snapshot()); n != 0 {
				t.Fatalf("notifier called %d times, want 0", n)
			}
			if after := storedCopy(t, svc, "abc"); !reflect.DeepEqual(before, after) {
				t.Fatalf("unrelated task modified: %+v", after)
			}
		})
	}
}

func TestAssignTask_InvalidEmail(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"tabs and newlines only", "\t\n "},
		{"no at sign", "not-an-email"},
		{"display name form", "Name <a@b.com>"},
		{"quoted display name form", `"Name" <a@b.com>`},
		{"angle brackets only", "<a@b.com>"},
		{"trailing comment", "a@b.com (Name)"},
		{"missing domain", "a@"},
		{"missing local part", "@b.com"},
		{"double at", "a@@b.com"},
		{"space in local part", "a b@c.com"},
		{"multiple addresses", "a@b.com, c@d.com"},
		{"only at sign", "@"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, notifier := newEmptyService(t, baseNow)
			prev := "prev@example.com"
			created := createAt(t, svc, baseNow.Add(-time.Hour), models.CreateTaskRequest{
				Title:      "work",
				AssignedTo: &prev,
			})
			before := storedCopy(t, svc, created.ID)
			svc.SetClock(fixedClock(baseNow.Add(time.Hour)))

			got, err := svc.AssignTask(created.ID, tc.input)
			if err == nil {
				t.Fatalf("expected error for %q, got task %+v", tc.input, got)
			}
			if got != nil {
				t.Fatalf("expected nil task, got %+v", got)
			}
			if !errors.Is(err, ErrInvalidEmail) {
				t.Fatalf("errors.Is(err, ErrInvalidEmail) = false; err = %v", err)
			}
			if errors.Is(err, ErrTaskNotFound) {
				t.Fatalf("invalid-email error must not match ErrTaskNotFound: %v", err)
			}
			if after := storedCopy(t, svc, created.ID); !reflect.DeepEqual(before, after) {
				t.Fatalf("task changed by failed assignment:\nbefore=%+v\nafter=%+v", before, after)
			}
			if n := len(notifier.snapshot()); n != 0 {
				t.Fatalf("notifier called %d times, want 0", n)
			}
		})
	}
}

func TestAssignTask_NotifierErrorDoesNotFailAssignment(t *testing.T) {
	svc, notifier := newEmptyService(t, baseNow)
	notifier.err = errors.New("smtp down")
	created := createAt(t, svc, baseNow.Add(-time.Hour), models.CreateTaskRequest{Title: "work"})
	assignNow := baseNow.Add(time.Minute)
	svc.SetClock(fixedClock(assignNow))
	buf := captureLog(t)

	got, err := svc.AssignTask(created.ID, "user@example.com")
	if err != nil {
		t.Fatalf("AssignTask returned error despite notifier failure: %v", err)
	}
	if got == nil || got.AssignedTo == nil || *got.AssignedTo != "user@example.com" || !got.UpdatedAt.Equal(assignNow) {
		t.Fatalf("unexpected result: %+v", got)
	}
	stored := storedCopy(t, svc, created.ID)
	if stored.AssignedTo == nil || *stored.AssignedTo != "user@example.com" {
		t.Fatalf("assignment not persisted: %+v", stored)
	}
	if n := len(notifier.snapshot()); n != 1 {
		t.Fatalf("notifier called %d times, want 1", n)
	}
	logged := buf.String()
	if !strings.Contains(logged, "failed to notify") || !strings.Contains(logged, "smtp down") ||
		!strings.Contains(logged, created.ID) {
		t.Fatalf("notifier error not logged; log = %q", logged)
	}
}

func TestAssignTask_NilNotifier(t *testing.T) {
	svc, _ := newEmptyService(t, baseNow)
	svc.SetNotifier(nil)
	created := createAt(t, svc, baseNow, models.CreateTaskRequest{Title: "work"})

	got, err := svc.AssignTask(created.ID, " user@example.com ")
	if err != nil {
		t.Fatalf("AssignTask with nil notifier: %v", err)
	}
	if got.AssignedTo == nil || *got.AssignedTo != "user@example.com" {
		t.Fatalf("AssignedTo = %v", got.AssignedTo)
	}
}

func TestAssignTask_SetNotifierReplacesNotifier(t *testing.T) {
	svc, first := newEmptyService(t, baseNow)
	second := &fakeNotifier{}
	svc.SetNotifier(second)
	created := createAt(t, svc, baseNow, models.CreateTaskRequest{Title: "work"})

	if _, err := svc.AssignTask(created.ID, "user@example.com"); err != nil {
		t.Fatal(err)
	}
	if len(first.snapshot()) != 0 || len(second.snapshot()) != 1 {
		t.Fatalf("first=%d second=%d calls", len(first.snapshot()), len(second.snapshot()))
	}
}

func TestAssignTask_NotifierCalledOutsideLock(t *testing.T) {
	svc, notifier := newEmptyService(t, baseNow)
	created := createAt(t, svc, baseNow.Add(-2*time.Hour), models.CreateTaskRequest{
		Title:   "work",
		DueDate: timePtr(baseNow.Add(-time.Hour)),
	})
	svc.SetClock(fixedClock(baseNow))
	// If the service lock were still held, these re-entrant calls would deadlock
	// (write lock held) or the SetNotifier would block.
	notifier.hook = func() {
		_, _ = svc.ListOverdueTasks(models.ListTasksQuery{})
		_, _ = svc.GetTask(created.ID)
		svc.SetNotifier(notifier)
	}

	done := make(chan error, 1)
	go func() {
		_, err := svc.AssignTask(created.ID, "user@example.com")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AssignTask deadlocked: notifier appears to be called while holding the lock")
	}
}

// ---------------------------------------------------------------------------
// Clock
// ---------------------------------------------------------------------------

func TestSetClock(t *testing.T) {
	t.Run("fixed clock drives CreatedAt UpdatedAt and AssignTask", func(t *testing.T) {
		svc, _ := newEmptyService(t, baseNow)
		task, err := svc.CreateTask(models.CreateTaskRequest{Title: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if !task.CreatedAt.Equal(baseNow) || !task.UpdatedAt.Equal(baseNow) {
			t.Fatalf("CreatedAt=%v UpdatedAt=%v, want %v", task.CreatedAt, task.UpdatedAt, baseNow)
		}
		later := baseNow.Add(3 * time.Hour)
		svc.SetClock(fixedClock(later))
		got, err := svc.AssignTask(task.ID, "u@example.com")
		if err != nil {
			t.Fatal(err)
		}
		if !got.UpdatedAt.Equal(later) {
			t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, later)
		}
	})

	t.Run("nil restores time.Now", func(t *testing.T) {
		svc, _ := newEmptyService(t, baseNow)
		svc.SetClock(nil)

		before := time.Now()
		task, err := svc.CreateTask(models.CreateTaskRequest{Title: "x"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := svc.AssignTask(task.ID, "u@example.com")
		if err != nil {
			t.Fatal(err)
		}
		after := time.Now()

		if task.CreatedAt.Before(before) || task.CreatedAt.After(after) {
			t.Fatalf("CreatedAt %v not within [%v, %v]", task.CreatedAt, before, after)
		}
		if got.UpdatedAt.Before(before) || got.UpdatedAt.After(after) {
			t.Fatalf("UpdatedAt %v not within [%v, %v]", got.UpdatedAt, before, after)
		}
		if got.UpdatedAt.Equal(baseNow) {
			t.Fatal("clock still fixed after SetClock(nil)")
		}
	})

	t.Run("nil restores time.Now for overdue evaluation", func(t *testing.T) {
		svc, _ := newEmptyService(t, baseNow)
		// Due date is after baseNow (2024) but long before the real now.
		createAt(t, svc, baseNow, models.CreateTaskRequest{Title: "late", DueDate: timePtr(baseNow.Add(time.Hour))})
		svc.SetClock(fixedClock(baseNow))
		resp, err := svc.ListOverdueTasks(models.ListTasksQuery{})
		if err != nil || resp.TotalCount != 0 {
			t.Fatalf("fixed clock: err=%v resp=%+v", err, resp)
		}
		svc.SetClock(nil)
		resp, err = svc.ListOverdueTasks(models.ListTasksQuery{})
		if err != nil || resp.TotalCount != 1 {
			t.Fatalf("real clock: err=%v resp=%+v", err, resp)
		}
	})
}

// ---------------------------------------------------------------------------
// Concurrency smoke test (meaningful under -race)
// ---------------------------------------------------------------------------

func TestConcurrentAssignListCreate(t *testing.T) {
	svc, notifier := newEmptyService(t, baseNow)
	laterNow := baseNow.Add(24 * time.Hour)
	due := baseNow.Add(-48 * time.Hour)

	const (
		targets      = 5
		assigners    = 4
		assignsEach  = 50
		listers      = 4
		listsEach    = 50
		creators     = 4
		createsEach  = 25
		clockToggles = 100
	)

	targetIDs := make([]string, targets)
	for i := range targetIDs {
		task := createAt(t, svc, baseNow.Add(-72*time.Hour), models.CreateTaskRequest{
			Title:   fmt.Sprintf("target-%d", i),
			Tags:    []string{"t"},
			DueDate: timePtr(due),
		})
		targetIDs[i] = task.ID
	}
	svc.SetClock(fixedClock(baseNow))

	var wg sync.WaitGroup
	start := make(chan struct{})

	for a := 0; a < assigners; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			<-start
			for i := 0; i < assignsEach; i++ {
				email := fmt.Sprintf("user%d-%d@example.com", a, i)
				got, err := svc.AssignTask(targetIDs[(a+i)%targets], email)
				if err != nil {
					t.Errorf("AssignTask: %v", err)
					return
				}
				if got.AssignedTo == nil || *got.AssignedTo != email {
					t.Errorf("AssignedTo = %v, want %s", got.AssignedTo, email)
					return
				}
				// Mutating our private copy must never race with the service.
				*got.AssignedTo = "scribble"
				got.Tags[0] = "scribble"
			}
		}(a)
	}

	for l := 0; l < listers; l++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < listsEach; i++ {
				resp, err := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 10})
				if err != nil {
					t.Errorf("ListOverdueTasks: %v", err)
					return
				}
				if resp.Tasks == nil || resp.TotalCount < targets {
					t.Errorf("bad response: nil=%v total=%d", resp.Tasks == nil, resp.TotalCount)
					return
				}
				for j := range resp.Tasks {
					resp.Tasks[j].Title = "scribble"
					if len(resp.Tasks[j].Tags) > 0 {
						resp.Tasks[j].Tags[0] = "scribble"
					}
					if resp.Tasks[j].AssignedTo != nil {
						*resp.Tasks[j].AssignedTo = "scribble"
					}
					if resp.Tasks[j].DueDate != nil {
						*resp.Tasks[j].DueDate = time.Time{}
					}
				}
			}
		}()
	}

	for c := 0; c < creators; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			<-start
			for i := 0; i < createsEach; i++ {
				d := due
				if _, err := svc.CreateTask(models.CreateTaskRequest{
					Title:   fmt.Sprintf("created-%d-%d", c, i),
					Tags:    []string{"c"},
					DueDate: &d,
				}); err != nil {
					t.Errorf("CreateTask: %v", err)
					return
				}
			}
		}(c)
	}

	// Concurrently swap clocks/notifier; both clocks are after every due date.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < clockToggles; i++ {
			if i%2 == 0 {
				svc.SetClock(fixedClock(laterNow))
			} else {
				svc.SetClock(fixedClock(baseNow))
			}
			svc.SetNotifier(notifier)
		}
	}()

	close(start)
	wg.Wait()

	if got, want := len(notifier.snapshot()), assigners*assignsEach; got != want {
		t.Errorf("notifier calls = %d, want %d", got, want)
	}

	resp, err := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if want := targets + creators*createsEach; resp.TotalCount != want {
		t.Fatalf("TotalCount = %d, want %d", resp.TotalCount, want)
	}
	// Mutations performed on returned copies must not have leaked into storage.
	for _, id := range targetIDs {
		stored := storedCopy(t, svc, id)
		if stored.Title == "scribble" || stored.Tags[0] != "t" || stored.DueDate == nil || stored.DueDate.IsZero() {
			t.Errorf("stored task %s corrupted via returned copy: %+v", id, stored)
		}
		if stored.AssignedTo == nil || *stored.AssignedTo == "scribble" {
			t.Errorf("stored task %s assignee corrupted: %v", id, stored.AssignedTo)
		}
	}
}

func TestListTasks_DefaultOrderIsStableAcrossPages(t *testing.T) {
	svc, _ := newEmptyService(t, baseNow)
	for i := 0; i < 25; i++ {
		// Equal CreatedAt for groups of tasks forces the ID tie-break.
		createAt(t, svc, baseNow.Add(time.Duration(i/5)*time.Minute), models.CreateTaskRequest{Title: strconv.Itoa(i)})
	}

	var first []string
	for run := 0; run < 5; run++ {
		var got []string
		token := ""
		for {
			resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 7, PageToken: token})
			if err != nil {
				t.Fatalf("ListTasks: %v", err)
			}
			got = append(got, ids(resp.Tasks)...)
			if resp.NextPageToken == "" {
				break
			}
			token = resp.NextPageToken
		}
		if len(got) != 25 {
			t.Fatalf("run %d: visited %d tasks, want 25", run, len(got))
		}
		seen := map[string]bool{}
		for _, id := range got {
			if seen[id] {
				t.Fatalf("run %d: task %s returned twice", run, id)
			}
			seen[id] = true
		}
		if run == 0 {
			first = got
		} else if !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d order differs from first run", run)
		}
	}
}

func TestTaskLookups_MissingIDReturnsErrTaskNotFound(t *testing.T) {
	const missing = "missing"
	wantMsg := "task with ID missing not found"

	calls := map[string]func(svc *TaskService) error{
		"GetTask": func(svc *TaskService) error { _, err := svc.GetTask(missing); return err },
		"UpdateTask": func(svc *TaskService) error {
			_, err := svc.UpdateTask(missing, models.UpdateTaskRequest{})
			return err
		},
		"UpdateTaskStatus": func(svc *TaskService) error {
			_, err := svc.UpdateTaskStatus(missing, models.TaskStatusCompleted)
			return err
		},
		"AssignTask": func(svc *TaskService) error { _, err := svc.AssignTask(missing, "a@example.com"); return err },
		"DeleteTask": func(svc *TaskService) error { return svc.DeleteTask(missing) },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			svc, _ := newEmptyService(t, baseNow)
			err := call(svc)
			if !errors.Is(err, ErrTaskNotFound) {
				t.Fatalf("errors.Is(%v, ErrTaskNotFound) = false", err)
			}
			if err.Error() != wantMsg {
				t.Errorf("message = %q, want %q", err.Error(), wantMsg)
			}
		})
	}
}

func TestListTasks_ReturnsDeepCopies(t *testing.T) {
	svc, _ := newEmptyService(t, baseNow)
	created := createAt(t, svc, baseNow, models.CreateTaskRequest{Title: "work", Tags: []string{"a", "b"}})

	res, err := svc.ListTasks(models.ListTasksQuery{PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	res.Tasks[0].Tags[0] = "mutated"

	if got := storedCopy(t, svc, created.ID).Tags[0]; got != "a" {
		t.Fatalf("stored tag = %q after mutating returned task, want %q", got, "a")
	}
}

func TestListTasks_SortOrdersKeepBaseOrderOnTies(t *testing.T) {
	c0 := baseNow
	c1 := baseNow.Add(time.Minute)
	c2 := baseNow.Add(2 * time.Minute)
	d1 := baseNow.Add(24 * time.Hour)
	d2 := baseNow.Add(48 * time.Hour)

	specs := map[string]struct {
		created  time.Time
		priority models.TaskPriority
		due      *time.Time
	}{
		"t1": {c0, models.TaskPriorityHigh, timePtr(d2)},
		"t2": {c0, models.TaskPriorityLow, nil},
		"t3": {c1, models.TaskPriorityHigh, timePtr(d1)},
		"t4": {c1, models.TaskPriorityLow, nil},
		"t5": {c2, models.TaskPriorityMedium, timePtr(d2)},
	}
	creationOrder := []string{"t5", "t3", "t2", "t4", "t1"}

	cases := []struct {
		name string
		sort models.SortOrder
		want []string
	}{
		{"default", "", []string{"t1", "t2", "t3", "t4", "t5"}},
		{"created_at_asc", models.SortOrderCreatedAtAsc, []string{"t1", "t2", "t3", "t4", "t5"}},
		{"created_at_desc", models.SortOrderCreatedAtDesc, []string{"t5", "t3", "t4", "t1", "t2"}},
		{"due_date_asc", models.SortOrderDueDateAsc, []string{"t3", "t1", "t5", "t2", "t4"}},
		{"due_date_desc", models.SortOrderDueDateDesc, []string{"t1", "t5", "t3", "t2", "t4"}},
		{"priority_asc", models.SortOrderPriorityAsc, []string{"t2", "t4", "t5", "t1", "t3"}},
		{"priority_desc", models.SortOrderPriorityDesc, []string{"t1", "t3", "t5", "t2", "t4"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newEmptyService(t, baseNow)
			for _, id := range creationOrder {
				sp := specs[id]
				task := createAt(t, svc, sp.created, models.CreateTaskRequest{Title: id, Priority: sp.priority, DueDate: sp.due})
				setID(svc, task, id)
			}

			for run := 0; run < 5; run++ {
				resp, err := svc.ListTasks(models.ListTasksQuery{SortOrder: tc.sort})
				if err != nil {
					t.Fatal(err)
				}
				if got := ids(resp.Tasks); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("run %d: order = %v, want %v", run, got, tc.want)
				}
			}

			var paged []string
			token := ""
			for pages := 0; ; pages++ {
				if pages > len(tc.want) {
					t.Fatal("pagination did not terminate")
				}
				resp, err := svc.ListTasks(models.ListTasksQuery{SortOrder: tc.sort, PageSize: 2, PageToken: token})
				if err != nil {
					t.Fatal(err)
				}
				paged = append(paged, ids(resp.Tasks)...)
				if token = resp.NextPageToken; token == "" {
					break
				}
			}
			if !reflect.DeepEqual(paged, tc.want) {
				t.Fatalf("paged order = %v, want %v", paged, tc.want)
			}
		})
	}
}
