package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// baseTime is the fixed instant the frozen test clock starts at. Tests never
// depend on wall-clock time.
var baseTime = time.Date(2024, time.March, 15, 12, 0, 0, 0, time.UTC)

// testClock is a goroutine-safe, manually advanced clock.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(t time.Time) *testClock {
	return &testClock{now: t}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d and returns the new time.
func (c *testClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// assignmentCall records one NotifyAssignment invocation.
type assignmentCall struct {
	Task     models.Task
	Assignee string
}

// recordingNotifier is a goroutine-safe fake notifications.Notifier.
type recordingNotifier struct {
	mu    sync.Mutex
	calls []assignmentCall
	err   error
}

func (n *recordingNotifier) NotifyAssignment(task models.Task, assignee string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, assignmentCall{Task: task, Assignee: assignee})
	return n.err
}

func (n *recordingNotifier) Calls() []assignmentCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]assignmentCall, len(n.calls))
	copy(out, n.calls)
	return out
}

// newTestService returns an empty service with a frozen clock and a fake notifier.
func newTestService(t *testing.T) (*TaskService, *testClock, *recordingNotifier) {
	t.Helper()
	clock := newTestClock(baseTime)
	notifier := &recordingNotifier{}
	svc := NewTaskServiceWithOptions(WithClock(clock.Now), WithNotifier(notifier), WithoutSampleData())
	return svc, clock, notifier
}

func timePtr(t time.Time) *time.Time { return &t }

func stringPtr(s string) *string { return &s }

// mustCreateTask creates a task through the public API and optionally moves it
// to the given status.
func mustCreateTask(t *testing.T, svc *TaskService, title string, due *time.Time, status models.TaskStatus) string {
	t.Helper()
	task, err := svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatalf("CreateTask(%q) returned error: %v", title, err)
	}
	id := task.ID
	if status != "" && status != models.TaskStatusPending {
		if _, err := svc.UpdateTaskStatus(id, status); err != nil {
			t.Fatalf("UpdateTaskStatus(%q, %q) returned error: %v", id, status, err)
		}
	}
	return id
}

// putTask inserts a task with a caller-chosen ID directly into the store.
func putTask(svc *TaskService, task *models.Task) {
	if task.Tags == nil {
		task.Tags = []string{}
	}
	if task.Status == "" {
		task.Status = models.TaskStatusPending
	}
	if task.Priority == "" {
		task.Priority = models.TaskPriorityMedium
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	svc.tasks[task.ID] = task
}

func taskIDs(tasks []models.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func sortedCopy(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

// makeTasks builds n tasks with IDs task-000, task-001, ...
func makeTasks(n int) []*models.Task {
	tasks := make([]*models.Task, 0, n)
	for i := 0; i < n; i++ {
		tasks = append(tasks, &models.Task{
			ID:    fmt.Sprintf("task-%03d", i),
			Title: fmt.Sprintf("Task %d", i),
			Tags:  []string{},
		})
	}
	return tasks
}

func idsOf(tasks []*models.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

// captureStdLog redirects the standard logger into a buffer for the duration
// of the test.
func captureStdLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags, prevPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	})
	return &buf
}

// ---------------------------------------------------------------------------
// paginate helper
// ---------------------------------------------------------------------------

func TestPaginate(t *testing.T) {
	tests := []struct {
		name          string
		total         int
		pageSize      int
		pageToken     string
		wantStart     int
		wantLen       int
		wantNextToken string
	}{
		{name: "default page size when zero", total: 25, pageSize: 0, wantStart: 0, wantLen: 20, wantNextToken: "20"},
		{name: "default page size when negative", total: 25, pageSize: -3, wantStart: 0, wantLen: 20, wantNextToken: "20"},
		{name: "page size exactly max", total: 120, pageSize: 100, wantStart: 0, wantLen: 100, wantNextToken: "100"},
		{name: "page size clamped from 101", total: 120, pageSize: 101, wantStart: 0, wantLen: 100, wantNextToken: "100"},
		{name: "page size clamped from 1000", total: 120, pageSize: 1000, wantStart: 0, wantLen: 100, wantNextToken: "100"},
		{name: "clamped second page", total: 120, pageSize: 500, pageToken: "100", wantStart: 100, wantLen: 20, wantNextToken: ""},
		{name: "first page of five", total: 5, pageSize: 2, wantStart: 0, wantLen: 2, wantNextToken: "2"},
		{name: "second page of five", total: 5, pageSize: 2, pageToken: "2", wantStart: 2, wantLen: 2, wantNextToken: "4"},
		{name: "last page of five has no token", total: 5, pageSize: 2, pageToken: "4", wantStart: 4, wantLen: 1, wantNextToken: ""},
		{name: "page size larger than total", total: 5, pageSize: 10, wantStart: 0, wantLen: 5, wantNextToken: ""},
		{name: "page size equal to total", total: 5, pageSize: 5, wantStart: 0, wantLen: 5, wantNextToken: ""},
		{name: "explicit zero token", total: 5, pageSize: 2, pageToken: "0", wantStart: 0, wantLen: 2, wantNextToken: "2"},
		{name: "non-numeric token treated as zero", total: 5, pageSize: 2, pageToken: "abc", wantStart: 0, wantLen: 2, wantNextToken: "2"},
		{name: "token equal to total", total: 5, pageSize: 2, pageToken: "5", wantLen: 0, wantNextToken: ""},
		{name: "token past end", total: 5, pageSize: 2, pageToken: "50", wantLen: 0, wantNextToken: ""},
		{name: "empty input", total: 0, pageSize: 20, wantLen: 0, wantNextToken: ""},
		{name: "empty input default size", total: 0, pageSize: 0, wantLen: 0, wantNextToken: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tasks := makeTasks(tc.total)
			resp := paginate(tasks, tc.pageSize, tc.pageToken)
			if resp == nil {
				t.Fatal("paginate returned nil response")
			}
			if resp.TotalCount != tc.total {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, tc.total)
			}
			if resp.NextPageToken != tc.wantNextToken {
				t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, tc.wantNextToken)
			}
			if len(resp.Tasks) != tc.wantLen {
				t.Fatalf("len(Tasks) = %d, want %d", len(resp.Tasks), tc.wantLen)
			}
			wantIDs := idsOf(tasks[tc.wantStart : tc.wantStart+tc.wantLen])
			if got := taskIDs(resp.Tasks); !reflect.DeepEqual(got, wantIDs) {
				t.Errorf("task IDs = %v, want %v", got, wantIDs)
			}
		})
	}
}

func TestPaginateReturnsValueCopies(t *testing.T) {
	tasks := makeTasks(3)
	resp := paginate(tasks, 10, "")
	if len(resp.Tasks) != 3 {
		t.Fatalf("len(Tasks) = %d, want 3", len(resp.Tasks))
	}
	resp.Tasks[0].Title = "mutated"
	if tasks[0].Title != "Task 0" {
		t.Errorf("source task title = %q, want %q (paginate must return copies)", tasks[0].Title, "Task 0")
	}
}

// ---------------------------------------------------------------------------
// ListOverdueTasks
// ---------------------------------------------------------------------------

func TestListOverdueTasksFiltering(t *testing.T) {
	svc, clock, _ := newTestService(t)
	now := clock.Now()

	tests := []struct {
		name        string
		status      models.TaskStatus
		due         *time.Time
		wantOverdue bool
	}{
		{name: "pending past due", status: models.TaskStatusPending, due: timePtr(now.Add(-time.Hour)), wantOverdue: true},
		{name: "in_progress past due", status: models.TaskStatusInProgress, due: timePtr(now.Add(-48 * time.Hour)), wantOverdue: true},
		{name: "on_hold past due", status: models.TaskStatusOnHold, due: timePtr(now.Add(-time.Minute)), wantOverdue: true},
		{name: "pending one nanosecond before now", status: models.TaskStatusPending, due: timePtr(now.Add(-time.Nanosecond)), wantOverdue: true},
		{name: "completed past due", status: models.TaskStatusCompleted, due: timePtr(now.Add(-time.Hour)), wantOverdue: false},
		{name: "cancelled past due", status: models.TaskStatusCancelled, due: timePtr(now.Add(-time.Hour)), wantOverdue: false},
		{name: "pending future due", status: models.TaskStatusPending, due: timePtr(now.Add(time.Hour)), wantOverdue: false},
		{name: "in_progress future due", status: models.TaskStatusInProgress, due: timePtr(now.Add(time.Nanosecond)), wantOverdue: false},
		{name: "pending nil due", status: models.TaskStatusPending, due: nil, wantOverdue: false},
		{name: "on_hold nil due", status: models.TaskStatusOnHold, due: nil, wantOverdue: false},
		{name: "pending due exactly now", status: models.TaskStatusPending, due: timePtr(now), wantOverdue: false},
		{name: "on_hold due exactly now", status: models.TaskStatusOnHold, due: timePtr(now), wantOverdue: false},
	}

	ids := make(map[string]string, len(tests))
	wantCount := 0
	for _, tc := range tests {
		ids[tc.name] = mustCreateTask(t, svc, tc.name, tc.due, tc.status)
		if tc.wantOverdue {
			wantCount++
		}
	}

	resp, err := svc.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 100})
	if err != nil {
		t.Fatalf("ListOverdueTasks returned error: %v", err)
	}
	if resp.TotalCount != wantCount {
		t.Errorf("TotalCount = %d, want %d", resp.TotalCount, wantCount)
	}
	if len(resp.Tasks) != wantCount {
		t.Errorf("len(Tasks) = %d, want %d", len(resp.Tasks), wantCount)
	}
	if resp.NextPageToken != "" {
		t.Errorf("NextPageToken = %q, want empty", resp.NextPageToken)
	}

	returned := make(map[string]models.Task, len(resp.Tasks))
	for _, task := range resp.Tasks {
		returned[task.ID] = task
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task, got := returned[ids[tc.name]]
			if got != tc.wantOverdue {
				t.Fatalf("task included = %v, want %v", got, tc.wantOverdue)
			}
			if got && task.Status != tc.status {
				t.Errorf("returned status = %q, want %q", task.Status, tc.status)
			}
		})
	}
}

func TestListOverdueTasksUsesInjectedClock(t *testing.T) {
	tests := []struct {
		name        string
		dueOffset   time.Duration
		advance     time.Duration
		wantOverdue bool
	}{
		{name: "not yet due before advancing", dueOffset: time.Hour, advance: 0, wantOverdue: false},
		{name: "becomes overdue after clock passes due date", dueOffset: time.Hour, advance: 2 * time.Hour, wantOverdue: true},
		{name: "clock lands exactly on due date", dueOffset: time.Hour, advance: time.Hour, wantOverdue: false},
		{name: "clock one nanosecond past due date", dueOffset: time.Hour, advance: time.Hour + time.Nanosecond, wantOverdue: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, clock, _ := newTestService(t)
			id := mustCreateTask(t, svc, "clock task", timePtr(baseTime.Add(tc.dueOffset)), "")
			clock.Advance(tc.advance)

			resp, err := svc.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 20})
			if err != nil {
				t.Fatalf("ListOverdueTasks returned error: %v", err)
			}
			wantCount := 0
			if tc.wantOverdue {
				wantCount = 1
			}
			if resp.TotalCount != wantCount {
				t.Fatalf("TotalCount = %d, want %d", resp.TotalCount, wantCount)
			}
			if tc.wantOverdue && resp.Tasks[0].ID != id {
				t.Errorf("returned ID = %q, want %q", resp.Tasks[0].ID, id)
			}
		})
	}
}

func TestListOverdueTasksOrdering(t *testing.T) {
	type seed struct {
		id     string
		offset time.Duration // relative to baseTime; negative means past due
	}
	tests := []struct {
		name    string
		seeds   []seed
		wantIDs []string
	}{
		{
			name: "ascending due date",
			seeds: []seed{
				{id: "task-a", offset: -1 * time.Hour},
				{id: "task-b", offset: -3 * time.Hour},
				{id: "task-c", offset: -2 * time.Hour},
			},
			wantIDs: []string{"task-b", "task-c", "task-a"},
		},
		{
			name: "same due date ordered by id",
			seeds: []seed{
				{id: "task-z", offset: -time.Hour},
				{id: "task-m", offset: -time.Hour},
				{id: "task-a", offset: -time.Hour},
			},
			wantIDs: []string{"task-a", "task-m", "task-z"},
		},
		{
			name: "mixed due dates with id tiebreak",
			seeds: []seed{
				{id: "task-d", offset: -1 * time.Hour},
				{id: "task-c", offset: -3 * time.Hour},
				{id: "task-b", offset: -2 * time.Hour},
				{id: "task-a", offset: -3 * time.Hour},
			},
			wantIDs: []string{"task-a", "task-c", "task-b", "task-d"},
		},
		{
			name: "non-overdue tasks are not interleaved",
			seeds: []seed{
				{id: "task-future", offset: time.Hour},
				{id: "task-2", offset: -2 * time.Minute},
				{id: "task-now", offset: 0},
				{id: "task-1", offset: -2 * time.Minute},
			},
			wantIDs: []string{"task-1", "task-2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newTestService(t)
			for _, s := range tc.seeds {
				putTask(svc, &models.Task{
					ID:        s.id,
					Title:     s.id,
					CreatedAt: baseTime,
					UpdatedAt: baseTime,
					DueDate:   timePtr(baseTime.Add(s.offset)),
				})
			}

			resp, err := svc.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 20})
			if err != nil {
				t.Fatalf("ListOverdueTasks returned error: %v", err)
			}
			if got := taskIDs(resp.Tasks); !reflect.DeepEqual(got, tc.wantIDs) {
				t.Errorf("order = %v, want %v", got, tc.wantIDs)
			}
			if resp.TotalCount != len(tc.wantIDs) {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, len(tc.wantIDs))
			}
		})
	}
}

func TestListOverdueTasksTiebreakWithGeneratedIDs(t *testing.T) {
	svc, clock, _ := newTestService(t)
	due := clock.Now().Add(-time.Hour)
	earlier := clock.Now().Add(-2 * time.Hour)

	var sameDue []string
	for i := 0; i < 4; i++ {
		sameDue = append(sameDue, mustCreateTask(t, svc, fmt.Sprintf("same due %d", i), timePtr(due), ""))
	}
	first := mustCreateTask(t, svc, "earliest", timePtr(earlier), models.TaskStatusInProgress)

	resp, err := svc.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 20})
	if err != nil {
		t.Fatalf("ListOverdueTasks returned error: %v", err)
	}
	want := append([]string{first}, sortedCopy(sameDue)...)
	if got := taskIDs(resp.Tasks); !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestListOverdueTasksPagination(t *testing.T) {
	svc, clock, _ := newTestService(t)
	now := clock.Now()

	// Five overdue tasks with distinct due dates, created out of order.
	offsets := []time.Duration{-3 * time.Hour, -5 * time.Hour, -1 * time.Hour, -4 * time.Hour, -2 * time.Hour}
	byOffset := make(map[time.Duration]string, len(offsets))
	for i, off := range offsets {
		byOffset[off] = mustCreateTask(t, svc, fmt.Sprintf("overdue %d", i), timePtr(now.Add(off)), "")
	}
	ordered := []string{
		byOffset[-5*time.Hour],
		byOffset[-4*time.Hour],
		byOffset[-3*time.Hour],
		byOffset[-2*time.Hour],
		byOffset[-1*time.Hour],
	}
	// Noise that must never appear or be counted.
	mustCreateTask(t, svc, "future", timePtr(now.Add(time.Hour)), "")
	mustCreateTask(t, svc, "completed", timePtr(now.Add(-time.Hour)), models.TaskStatusCompleted)
	mustCreateTask(t, svc, "no due date", nil, "")

	tests := []struct {
		name          string
		pageSize      int
		pageToken     string
		wantIDs       []string
		wantNextToken string
	}{
		{name: "first page", pageSize: 2, pageToken: "", wantIDs: ordered[0:2], wantNextToken: "2"},
		{name: "second page", pageSize: 2, pageToken: "2", wantIDs: ordered[2:4], wantNextToken: "4"},
		{name: "last page has no token", pageSize: 2, pageToken: "4", wantIDs: ordered[4:5], wantNextToken: ""},
		{name: "single large page", pageSize: 10, pageToken: "", wantIDs: ordered, wantNextToken: ""},
		{name: "default page size", pageSize: 0, pageToken: "", wantIDs: ordered, wantNextToken: ""},
		{name: "non-numeric token treated as zero", pageSize: 2, pageToken: "abc", wantIDs: ordered[0:2], wantNextToken: "2"},
		{name: "token past end", pageSize: 2, pageToken: "10", wantIDs: []string{}, wantNextToken: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: tc.pageSize, PageToken: tc.pageToken})
			if err != nil {
				t.Fatalf("ListOverdueTasks returned error: %v", err)
			}
			if resp.TotalCount != 5 {
				t.Errorf("TotalCount = %d, want 5", resp.TotalCount)
			}
			if resp.NextPageToken != tc.wantNextToken {
				t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, tc.wantNextToken)
			}
			if got := taskIDs(resp.Tasks); !reflect.DeepEqual(got, tc.wantIDs) {
				t.Errorf("task IDs = %v, want %v", got, tc.wantIDs)
			}
		})
	}

	t.Run("walking all pages yields every task once in order", func(t *testing.T) {
		var collected []string
		token := ""
		for page := 0; page < 10; page++ {
			resp, err := svc.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 2, PageToken: token})
			if err != nil {
				t.Fatalf("ListOverdueTasks returned error: %v", err)
			}
			if resp.TotalCount != 5 {
				t.Errorf("page %d: TotalCount = %d, want 5", page, resp.TotalCount)
			}
			collected = append(collected, taskIDs(resp.Tasks)...)
			if resp.NextPageToken == "" {
				break
			}
			token = resp.NextPageToken
		}
		if !reflect.DeepEqual(collected, ordered) {
			t.Errorf("collected = %v, want %v", collected, ordered)
		}
	})
}

func TestListOverdueTasksPageSizeDefaultAndClamp(t *testing.T) {
	svc, clock, _ := newTestService(t)
	for i := 0; i < 120; i++ {
		putTask(svc, &models.Task{
			ID:      fmt.Sprintf("task-%03d", i),
			Title:   fmt.Sprintf("Task %d", i),
			DueDate: timePtr(clock.Now().Add(-time.Duration(120-i) * time.Minute)),
		})
	}

	tests := []struct {
		name          string
		pageSize      int
		wantLen       int
		wantNextToken string
	}{
		{name: "zero defaults to 20", pageSize: 0, wantLen: 20, wantNextToken: "20"},
		{name: "negative defaults to 20", pageSize: -1, wantLen: 20, wantNextToken: "20"},
		{name: "100 is allowed", pageSize: 100, wantLen: 100, wantNextToken: "100"},
		{name: "101 clamped to 100", pageSize: 101, wantLen: 100, wantNextToken: "100"},
		{name: "very large clamped to 100", pageSize: 5000, wantLen: 100, wantNextToken: "100"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: tc.pageSize})
			if err != nil {
				t.Fatalf("ListOverdueTasks returned error: %v", err)
			}
			if len(resp.Tasks) != tc.wantLen {
				t.Errorf("len(Tasks) = %d, want %d", len(resp.Tasks), tc.wantLen)
			}
			if resp.NextPageToken != tc.wantNextToken {
				t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, tc.wantNextToken)
			}
			if resp.TotalCount != 120 {
				t.Errorf("TotalCount = %d, want 120", resp.TotalCount)
			}
			if len(resp.Tasks) > 0 && resp.Tasks[0].ID != "task-000" {
				t.Errorf("first ID = %q, want task-000", resp.Tasks[0].ID)
			}
		})
	}
}

func TestListOverdueTasksEmpty(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, svc *TaskService, now time.Time)
	}{
		{name: "empty store", setup: func(t *testing.T, svc *TaskService, now time.Time) {}},
		{name: "only non-overdue tasks", setup: func(t *testing.T, svc *TaskService, now time.Time) {
			mustCreateTask(t, svc, "future", timePtr(now.Add(time.Hour)), "")
			mustCreateTask(t, svc, "due now", timePtr(now), "")
			mustCreateTask(t, svc, "no due", nil, "")
			mustCreateTask(t, svc, "completed", timePtr(now.Add(-time.Hour)), models.TaskStatusCompleted)
			mustCreateTask(t, svc, "cancelled", timePtr(now.Add(-time.Hour)), models.TaskStatusCancelled)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, clock, _ := newTestService(t)
			tc.setup(t, svc, clock.Now())

			resp, err := svc.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 20})
			if err != nil {
				t.Fatalf("ListOverdueTasks returned error: %v", err)
			}
			if resp.TotalCount != 0 {
				t.Errorf("TotalCount = %d, want 0", resp.TotalCount)
			}
			if len(resp.Tasks) != 0 {
				t.Errorf("len(Tasks) = %d, want 0", len(resp.Tasks))
			}
			if resp.NextPageToken != "" {
				t.Errorf("NextPageToken = %q, want empty", resp.NextPageToken)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// AssignTask
// ---------------------------------------------------------------------------

func TestAssignTaskSuccess(t *testing.T) {
	tests := []struct {
		name      string
		email     string
		wantEmail string
		tags      []string
	}{
		{name: "plain email", email: "alice@example.com", wantEmail: "alice@example.com", tags: []string{"backend", "urgent"}},
		{name: "email with surrounding whitespace is trimmed", email: "  bob@example.com\t\n", wantEmail: "bob@example.com", tags: []string{"ops"}},
		{name: "task without tags", email: "carol@example.org", wantEmail: "carol@example.org", tags: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, clock, notifier := newTestService(t)
			created, err := svc.CreateTask(models.CreateTaskRequest{
				Title:    "Write report",
				Priority: models.TaskPriorityHigh,
				Tags:     tc.tags,
				DueDate:  timePtr(baseTime.Add(24 * time.Hour)),
			})
			if err != nil {
				t.Fatalf("CreateTask returned error: %v", err)
			}
			id := created.ID

			wantUpdatedAt := clock.Advance(90 * time.Minute)

			got, err := svc.AssignTask(id, tc.email)
			if err != nil {
				t.Fatalf("AssignTask returned error: %v", err)
			}
			if got == nil {
				t.Fatal("AssignTask returned nil task")
			}

			// Returned snapshot.
			if got.ID != id {
				t.Errorf("returned ID = %q, want %q", got.ID, id)
			}
			if got.Title != "Write report" {
				t.Errorf("returned Title = %q, want %q", got.Title, "Write report")
			}
			if got.AssignedTo == nil || *got.AssignedTo != tc.wantEmail {
				t.Errorf("returned AssignedTo = %v, want %q", got.AssignedTo, tc.wantEmail)
			}
			if !got.UpdatedAt.Equal(wantUpdatedAt) {
				t.Errorf("returned UpdatedAt = %v, want %v", got.UpdatedAt, wantUpdatedAt)
			}
			if !got.CreatedAt.Equal(baseTime) {
				t.Errorf("returned CreatedAt = %v, want %v (must not change)", got.CreatedAt, baseTime)
			}

			// Stored task.
			stored, err := svc.GetTask(id)
			if err != nil {
				t.Fatalf("GetTask returned error: %v", err)
			}
			if stored.AssignedTo == nil || *stored.AssignedTo != tc.wantEmail {
				t.Errorf("stored AssignedTo = %v, want %q", stored.AssignedTo, tc.wantEmail)
			}
			if !stored.UpdatedAt.Equal(wantUpdatedAt) {
				t.Errorf("stored UpdatedAt = %v, want %v", stored.UpdatedAt, wantUpdatedAt)
			}
			if stored.Status != models.TaskStatusPending {
				t.Errorf("stored Status = %q, want %q (must not change)", stored.Status, models.TaskStatusPending)
			}

			// Notification.
			calls := notifier.Calls()
			if len(calls) != 1 {
				t.Fatalf("notifier called %d times, want 1", len(calls))
			}
			call := calls[0]
			if call.Assignee != tc.wantEmail {
				t.Errorf("notified assignee = %q, want %q", call.Assignee, tc.wantEmail)
			}
			if call.Task.ID != id {
				t.Errorf("notified task ID = %q, want %q", call.Task.ID, id)
			}
			if call.Task.Title != "Write report" {
				t.Errorf("notified task Title = %q, want %q", call.Task.Title, "Write report")
			}
			if call.Task.AssignedTo == nil || *call.Task.AssignedTo != tc.wantEmail {
				t.Errorf("notified task AssignedTo = %v, want %q", call.Task.AssignedTo, tc.wantEmail)
			}
			if !call.Task.UpdatedAt.Equal(wantUpdatedAt) {
				t.Errorf("notified task UpdatedAt = %v, want %v", call.Task.UpdatedAt, wantUpdatedAt)
			}
			wantTags := tc.tags
			if wantTags == nil {
				wantTags = []string{}
			}
			if !reflect.DeepEqual(call.Task.Tags, wantTags) {
				t.Errorf("notified task Tags = %#v, want %#v", call.Task.Tags, wantTags)
			}
		})
	}
}

func TestAssignTaskReassignment(t *testing.T) {
	svc, clock, notifier := newTestService(t)
	id := mustCreateTask(t, svc, "Reassign me", nil, "")

	steps := []struct {
		email   string
		advance time.Duration
	}{
		{email: "first@example.com", advance: time.Minute},
		{email: "second@example.com", advance: time.Hour},
	}

	for i, step := range steps {
		wantUpdatedAt := clock.Advance(step.advance)
		got, err := svc.AssignTask(id, step.email)
		if err != nil {
			t.Fatalf("step %d: AssignTask returned error: %v", i, err)
		}
		if got.AssignedTo == nil || *got.AssignedTo != step.email {
			t.Errorf("step %d: AssignedTo = %v, want %q", i, got.AssignedTo, step.email)
		}
		if !got.UpdatedAt.Equal(wantUpdatedAt) {
			t.Errorf("step %d: UpdatedAt = %v, want %v", i, got.UpdatedAt, wantUpdatedAt)
		}
	}

	calls := notifier.Calls()
	if len(calls) != len(steps) {
		t.Fatalf("notifier called %d times, want %d", len(calls), len(steps))
	}
	for i, step := range steps {
		if calls[i].Assignee != step.email {
			t.Errorf("call %d assignee = %q, want %q", i, calls[i].Assignee, step.email)
		}
	}
}

func TestAssignTaskUnknownID(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{name: "random id", id: "does-not-exist"},
		{name: "uuid-shaped id", id: "00000000-0000-0000-0000-000000000000"},
		{name: "route keyword", id: "overdue"},
		{name: "empty id", id: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, clock, notifier := newTestService(t)
			existing := mustCreateTask(t, svc, "Existing", nil, "")
			clock.Advance(time.Hour)

			got, err := svc.AssignTask(tc.id, "alice@example.com")
			if err == nil {
				t.Fatal("AssignTask returned nil error, want not-found error")
			}
			wantMsg := fmt.Sprintf("task with ID %s not found", tc.id)
			if err.Error() != wantMsg {
				t.Errorf("error = %q, want %q", err.Error(), wantMsg)
			}
			if got != nil {
				t.Errorf("returned task = %+v, want nil", got)
			}
			if calls := notifier.Calls(); len(calls) != 0 {
				t.Errorf("notifier called %d times, want 0", len(calls))
			}

			stored, err := svc.GetTask(existing)
			if err != nil {
				t.Fatalf("GetTask returned error: %v", err)
			}
			if stored.AssignedTo != nil {
				t.Errorf("unrelated task AssignedTo = %q, want nil", *stored.AssignedTo)
			}
			if !stored.UpdatedAt.Equal(baseTime) {
				t.Errorf("unrelated task UpdatedAt = %v, want %v", stored.UpdatedAt, baseTime)
			}
		})
	}
}

func TestAssignTaskNotifierErrorStillAssigns(t *testing.T) {
	logs := captureStdLog(t)

	svc, clock, notifier := newTestService(t)
	notifier.err = errors.New("smtp unavailable")
	id := mustCreateTask(t, svc, "Notify failure", nil, "")
	wantUpdatedAt := clock.Advance(5 * time.Minute)

	got, err := svc.AssignTask(id, "dave@example.com")
	if err != nil {
		t.Fatalf("AssignTask returned error %v, want nil (notifier errors must not fail assignment)", err)
	}
	if got == nil || got.AssignedTo == nil || *got.AssignedTo != "dave@example.com" {
		t.Fatalf("returned task = %+v, want AssignedTo dave@example.com", got)
	}

	stored, err := svc.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if stored.AssignedTo == nil || *stored.AssignedTo != "dave@example.com" {
		t.Errorf("stored AssignedTo = %v, want dave@example.com (no rollback)", stored.AssignedTo)
	}
	if !stored.UpdatedAt.Equal(wantUpdatedAt) {
		t.Errorf("stored UpdatedAt = %v, want %v", stored.UpdatedAt, wantUpdatedAt)
	}

	if calls := notifier.Calls(); len(calls) != 1 {
		t.Errorf("notifier called %d times, want 1", len(calls))
	}

	wantLog := fmt.Sprintf("assignment notification for task %s failed: smtp unavailable", id)
	if !strings.Contains(logs.String(), wantLog) {
		t.Errorf("log output = %q, want it to contain %q", logs.String(), wantLog)
	}
}

func TestAssignTaskSnapshotTags(t *testing.T) {
	tests := []struct {
		name     string
		tags     []string
		wantTags []string
	}{
		{name: "nil tags become empty non-nil", tags: nil, wantTags: []string{}},
		{name: "explicit empty tags stay non-nil", tags: []string{}, wantTags: []string{}},
		{name: "populated tags are copied", tags: []string{"alpha", "beta"}, wantTags: []string{"alpha", "beta"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, notifier := newTestService(t)
			created, err := svc.CreateTask(models.CreateTaskRequest{Title: "Tagged", Tags: tc.tags})
			if err != nil {
				t.Fatalf("CreateTask returned error: %v", err)
			}
			id := created.ID

			got, err := svc.AssignTask(id, "erin@example.com")
			if err != nil {
				t.Fatalf("AssignTask returned error: %v", err)
			}

			if got.Tags == nil {
				t.Fatal("returned Tags is nil, want non-nil slice")
			}
			if !reflect.DeepEqual(got.Tags, tc.wantTags) {
				t.Errorf("returned Tags = %#v, want %#v", got.Tags, tc.wantTags)
			}
			calls := notifier.Calls()
			if len(calls) != 1 {
				t.Fatalf("notifier called %d times, want 1", len(calls))
			}
			if calls[0].Task.Tags == nil {
				t.Error("notified Tags is nil, want non-nil slice")
			}

			raw, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("json.Marshal returned error: %v", err)
			}
			if len(tc.wantTags) == 0 && !strings.Contains(string(raw), `"tags":[]`) {
				t.Errorf("JSON = %s, want it to contain \"tags\":[]", raw)
			}

			if len(got.Tags) > 0 {
				got.Tags[0] = "mutated"
				stored, err := svc.GetTask(id)
				if err != nil {
					t.Fatalf("GetTask returned error: %v", err)
				}
				if stored.Tags[0] != tc.wantTags[0] {
					t.Errorf("stored Tags[0] = %q after mutating snapshot, want %q (snapshot must not alias)", stored.Tags[0], tc.wantTags[0])
				}
			}
		})
	}
}

func TestAssignTaskConcurrent(t *testing.T) {
	svc, clock, notifier := newTestService(t)
	ids := make([]string, 5)
	for i := range ids {
		ids[i] = mustCreateTask(t, svc, fmt.Sprintf("Concurrent %d", i), timePtr(clock.Now().Add(-time.Hour)), "")
	}

	const workers = 20
	var wg sync.WaitGroup
	errCh := make(chan error, workers*3)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := ids[w%len(ids)]
			if _, err := svc.AssignTask(id, fmt.Sprintf("user%d@example.com", w)); err != nil {
				errCh <- err
			}
			if _, err := svc.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 2}); err != nil {
				errCh <- err
			}
			if _, err := svc.ListTasks(models.ListTasksQuery{PageSize: 2}); err != nil {
				errCh <- err
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent call returned error: %v", err)
	}

	if calls := notifier.Calls(); len(calls) != workers {
		t.Errorf("notifier called %d times, want %d", len(calls), workers)
	}
	for _, id := range ids {
		stored, err := svc.GetTask(id)
		if err != nil {
			t.Fatalf("GetTask returned error: %v", err)
		}
		if stored.AssignedTo == nil {
			t.Errorf("task %s not assigned after concurrent assignments", id)
		}
	}
}

// ---------------------------------------------------------------------------
// Construction and notifier wiring
// ---------------------------------------------------------------------------

func TestNewTaskServiceSeedsSampleData(t *testing.T) {
	wantTitles := []string{"Add gRPC support", "Implement Go REST API"}

	tests := []struct {
		name       string
		build      func() *TaskService
		wantTitles []string
	}{
		{name: "NewTaskService", build: NewTaskService, wantTitles: wantTitles},
		{name: "NewTaskServiceWithOptions without options", build: func() *TaskService { return NewTaskServiceWithOptions() }, wantTitles: wantTitles},
		{name: "options without WithoutSampleData still seed", build: func() *TaskService {
			return NewTaskServiceWithOptions(WithClock(newTestClock(baseTime).Now), WithNotifier(&recordingNotifier{}))
		}, wantTitles: wantTitles},
		{name: "WithoutSampleData is empty", build: func() *TaskService { return NewTaskServiceWithOptions(WithoutSampleData()) }, wantTitles: []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := tc.build()
			resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 20})
			if err != nil {
				t.Fatalf("ListTasks returned error: %v", err)
			}
			if resp.TotalCount != len(tc.wantTitles) {
				t.Fatalf("TotalCount = %d, want %d", resp.TotalCount, len(tc.wantTitles))
			}
			titles := make([]string, 0, len(resp.Tasks))
			for _, task := range resp.Tasks {
				titles = append(titles, task.Title)
				if task.Status != models.TaskStatusPending {
					t.Errorf("sample task %q status = %q, want pending", task.Title, task.Status)
				}
				if task.DueDate != nil {
					t.Errorf("sample task %q has a due date, want nil", task.Title)
				}
			}
			sort.Strings(titles)
			if !reflect.DeepEqual(titles, tc.wantTitles) {
				t.Errorf("titles = %v, want %v", titles, tc.wantTitles)
			}
		})
	}

	t.Run("seeded tasks use the injected clock", func(t *testing.T) {
		svc := NewTaskServiceWithOptions(WithClock(newTestClock(baseTime).Now))
		resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 20})
		if err != nil {
			t.Fatalf("ListTasks returned error: %v", err)
		}
		for _, task := range resp.Tasks {
			if !task.CreatedAt.Equal(baseTime) {
				t.Errorf("sample task %q CreatedAt = %v, want %v", task.Title, task.CreatedAt, baseTime)
			}
		}
	})

	t.Run("sample tasks are never overdue", func(t *testing.T) {
		resp, err := NewTaskService().ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: 20})
		if err != nil {
			t.Fatalf("ListOverdueTasks returned error: %v", err)
		}
		if resp.TotalCount != 0 {
			t.Errorf("TotalCount = %d, want 0", resp.TotalCount)
		}
	})
}

func TestNilOptionsAreIgnored(t *testing.T) {
	logs := captureStdLog(t)

	svc := NewTaskServiceWithOptions(nil, WithClock(nil), WithNotifier(nil), WithoutSampleData())
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "Defaults"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if created.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero, want the default clock to remain in place")
	}

	// The default LogNotifier must still be installed and write through log.
	if _, err := svc.AssignTask(created.ID, "frank@example.com"); err != nil {
		t.Fatalf("AssignTask returned error: %v", err)
	}
	want := fmt.Sprintf("assignment notification: task %s \"Defaults\" assigned to frank@example.com", created.ID)
	if !strings.Contains(logs.String(), want) {
		t.Errorf("log output = %q, want it to contain %q", logs.String(), want)
	}
}

func TestSetNotifier(t *testing.T) {
	svc, _, original := newTestService(t)
	replacement := &recordingNotifier{}
	id := mustCreateTask(t, svc, "Swap notifier", nil, "")

	steps := []struct {
		name            string
		set             func()
		wantOriginal    int
		wantReplacement int
	}{
		{name: "replacement receives notifications", set: func() { svc.SetNotifier(replacement) }, wantOriginal: 0, wantReplacement: 1},
		{name: "nil notifier is ignored", set: func() { svc.SetNotifier(nil) }, wantOriginal: 0, wantReplacement: 2},
	}

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.set()
			if _, err := svc.AssignTask(id, "grace@example.com"); err != nil {
				t.Fatalf("AssignTask returned error: %v", err)
			}
			if got := len(original.Calls()); got != step.wantOriginal {
				t.Errorf("original notifier calls = %d, want %d", got, step.wantOriginal)
			}
			if got := len(replacement.Calls()); got != step.wantReplacement {
				t.Errorf("replacement notifier calls = %d, want %d", got, step.wantReplacement)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ListTasks regression after the paginate refactor
// ---------------------------------------------------------------------------

// seedOrderedTasks creates n tasks with strictly increasing CreatedAt and
// returns their IDs in creation order.
func seedOrderedTasks(t *testing.T, svc *TaskService, clock *testClock, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, mustCreateTask(t, svc, fmt.Sprintf("Task %03d", i), nil, ""))
		clock.Advance(time.Minute)
	}
	return ids
}

func TestListTasksPaginationRegression(t *testing.T) {
	svc, clock, _ := newTestService(t)
	ordered := seedOrderedTasks(t, svc, clock, 25)

	tests := []struct {
		name          string
		pageSize      int
		pageToken     string
		wantIDs       []string
		wantNextToken string
	}{
		{name: "default page size", pageSize: 0, wantIDs: ordered[0:20], wantNextToken: "20"},
		{name: "default page size second page", pageSize: 0, pageToken: "20", wantIDs: ordered[20:25], wantNextToken: ""},
		{name: "explicit page size", pageSize: 10, wantIDs: ordered[0:10], wantNextToken: "10"},
		{name: "page token", pageSize: 10, pageToken: "10", wantIDs: ordered[10:20], wantNextToken: "20"},
		{name: "last page has no token", pageSize: 10, pageToken: "20", wantIDs: ordered[20:25], wantNextToken: ""},
		{name: "page exactly fills remainder", pageSize: 5, pageToken: "20", wantIDs: ordered[20:25], wantNextToken: ""},
		{name: "non-numeric token treated as zero", pageSize: 10, pageToken: "not-a-number", wantIDs: ordered[0:10], wantNextToken: "10"},
		{name: "token equal to total", pageSize: 10, pageToken: "25", wantIDs: []string{}, wantNextToken: ""},
		{name: "token past end", pageSize: 10, pageToken: "500", wantIDs: []string{}, wantNextToken: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.ListTasks(models.ListTasksQuery{
				PageSize:  tc.pageSize,
				PageToken: tc.pageToken,
				SortOrder: models.SortOrderCreatedAtAsc,
			})
			if err != nil {
				t.Fatalf("ListTasks returned error: %v", err)
			}
			if resp.TotalCount != 25 {
				t.Errorf("TotalCount = %d, want 25", resp.TotalCount)
			}
			if resp.NextPageToken != tc.wantNextToken {
				t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, tc.wantNextToken)
			}
			if got := taskIDs(resp.Tasks); !reflect.DeepEqual(got, tc.wantIDs) {
				t.Errorf("task IDs = %v, want %v", got, tc.wantIDs)
			}
		})
	}

	t.Run("walking all pages yields every task once", func(t *testing.T) {
		var collected []string
		token := ""
		for page := 0; page < 20; page++ {
			resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 7, PageToken: token, SortOrder: models.SortOrderCreatedAtAsc})
			if err != nil {
				t.Fatalf("ListTasks returned error: %v", err)
			}
			collected = append(collected, taskIDs(resp.Tasks)...)
			if resp.NextPageToken == "" {
				break
			}
			token = resp.NextPageToken
		}
		if !reflect.DeepEqual(collected, ordered) {
			t.Errorf("collected = %v, want %v", collected, ordered)
		}
	})
}

func TestListTasksPageSizeClampRegression(t *testing.T) {
	svc, clock, _ := newTestService(t)
	ordered := seedOrderedTasks(t, svc, clock, 120)

	tests := []struct {
		name          string
		pageSize      int
		wantLen       int
		wantNextToken string
	}{
		{name: "zero defaults to 20", pageSize: 0, wantLen: 20, wantNextToken: "20"},
		{name: "negative defaults to 20", pageSize: -10, wantLen: 20, wantNextToken: "20"},
		{name: "100 allowed", pageSize: 100, wantLen: 100, wantNextToken: "100"},
		{name: "101 clamped to 100", pageSize: 101, wantLen: 100, wantNextToken: "100"},
		{name: "huge clamped to 100", pageSize: 1 << 20, wantLen: 100, wantNextToken: "100"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: tc.pageSize, SortOrder: models.SortOrderCreatedAtAsc})
			if err != nil {
				t.Fatalf("ListTasks returned error: %v", err)
			}
			if resp.TotalCount != 120 {
				t.Errorf("TotalCount = %d, want 120", resp.TotalCount)
			}
			if resp.NextPageToken != tc.wantNextToken {
				t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, tc.wantNextToken)
			}
			if got := taskIDs(resp.Tasks); !reflect.DeepEqual(got, ordered[:tc.wantLen]) {
				t.Errorf("got %d tasks not matching the first %d in creation order", len(got), tc.wantLen)
			}
		})
	}
}

func TestListTasksFiltersRegression(t *testing.T) {
	svc, _, _ := newTestService(t)

	create := func(title string, tags []string, assignee *string, status models.TaskStatus) string {
		t.Helper()
		task, err := svc.CreateTask(models.CreateTaskRequest{Title: title, Tags: tags, AssignedTo: assignee})
		if err != nil {
			t.Fatalf("CreateTask(%q) returned error: %v", title, err)
		}
		id := task.ID
		if status != models.TaskStatusPending {
			if _, err := svc.UpdateTaskStatus(id, status); err != nil {
				t.Fatalf("UpdateTaskStatus returned error: %v", err)
			}
		}
		return id
	}

	t1 := create("one", []string{"go", "api"}, stringPtr("alice@example.com"), models.TaskStatusPending)
	t2 := create("two", []string{"go"}, stringPtr("bob@example.com"), models.TaskStatusInProgress)
	t3 := create("three", []string{"api"}, nil, models.TaskStatusCompleted)
	t4 := create("four", []string{"go", "api", "rest"}, stringPtr("alice@example.com"), models.TaskStatusPending)

	tests := []struct {
		name    string
		query   models.ListTasksQuery
		wantIDs []string
	}{
		{name: "no filters", query: models.ListTasksQuery{}, wantIDs: []string{t1, t2, t3, t4}},
		{name: "status pending", query: models.ListTasksQuery{Status: models.TaskStatusPending}, wantIDs: []string{t1, t4}},
		{name: "status completed", query: models.ListTasksQuery{Status: models.TaskStatusCompleted}, wantIDs: []string{t3}},
		{name: "status with no matches", query: models.ListTasksQuery{Status: models.TaskStatusOnHold}, wantIDs: []string{}},
		{name: "assigned_to", query: models.ListTasksQuery{AssignedTo: "alice@example.com"}, wantIDs: []string{t1, t4}},
		{name: "single tag", query: models.ListTasksQuery{Tags: "go"}, wantIDs: []string{t1, t2, t4}},
		{name: "all tags must match (with spaces)", query: models.ListTasksQuery{Tags: "go, api"}, wantIDs: []string{t1, t4}},
		{name: "unknown tag", query: models.ListTasksQuery{Tags: "go,missing"}, wantIDs: []string{}},
		{name: "combined filters", query: models.ListTasksQuery{Status: models.TaskStatusPending, AssignedTo: "alice@example.com", Tags: "rest"}, wantIDs: []string{t4}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.ListTasks(tc.query)
			if err != nil {
				t.Fatalf("ListTasks returned error: %v", err)
			}
			if resp.TotalCount != len(tc.wantIDs) {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, len(tc.wantIDs))
			}
			if got, want := sortedCopy(taskIDs(resp.Tasks)), sortedCopy(tc.wantIDs); !reflect.DeepEqual(got, want) {
				t.Errorf("task IDs = %v, want %v", got, want)
			}
		})
	}

	t.Run("filter combined with pagination counts filtered tasks", func(t *testing.T) {
		resp, err := svc.ListTasks(models.ListTasksQuery{Tags: "go", PageSize: 2})
		if err != nil {
			t.Fatalf("ListTasks returned error: %v", err)
		}
		if resp.TotalCount != 3 {
			t.Errorf("TotalCount = %d, want 3", resp.TotalCount)
		}
		if len(resp.Tasks) != 2 {
			t.Errorf("len(Tasks) = %d, want 2", len(resp.Tasks))
		}
		if resp.NextPageToken != "2" {
			t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, "2")
		}
	})
}
