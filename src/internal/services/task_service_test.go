package services

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// seededTaskCount is the number of sample tasks (without due dates) created by NewTaskService.
const seededTaskCount = 2

var testNow = time.Date(2024, time.June, 15, 12, 0, 0, 0, time.UTC)

// newTestService returns a service whose clock is fixed at testNow.
func newTestService(t *testing.T) *TaskService {
	t.Helper()
	svc := NewTaskService()
	svc.now = func() time.Time { return testNow }
	return svc
}

// addTask creates a task through the service with an optional due date and status.
func addTask(t *testing.T, svc *TaskService, title string, due *time.Time, status models.TaskStatus) *models.Task {
	t.Helper()
	task, err := svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due, Tags: []string{"t1", "t2"}})
	if err != nil {
		t.Fatalf("CreateTask(%q): %v", title, err)
	}
	if status != "" && status != models.TaskStatusPending {
		if _, err := svc.UpdateTaskStatus(task.ID, status); err != nil {
			t.Fatalf("UpdateTaskStatus(%q): %v", title, err)
		}
	}
	return task
}

// at returns a pointer to testNow shifted by d.
func at(d time.Duration) *time.Time {
	v := testNow.Add(d)
	return &v
}

func titlesOf(tasks []models.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.Title)
	}
	return out
}

func idsOf(tasks []models.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.ID)
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

func TestNewTaskServiceDefaultsClock(t *testing.T) {
	svc := NewTaskService()
	if svc.now == nil {
		t.Fatal("now must default to a non-nil clock")
	}
	before := time.Now()
	got := svc.now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatalf("default clock returned %v, want between %v and %v", got, before, after)
	}
}

func TestListOverdueTasks_Filtering(t *testing.T) {
	tests := []struct {
		name    string
		due     *time.Time
		status  models.TaskStatus
		overdue bool
	}{
		{"pending past due", at(-time.Hour), models.TaskStatusPending, true},
		{"in_progress past due", at(-time.Hour), models.TaskStatusInProgress, true},
		{"on_hold past due", at(-time.Hour), models.TaskStatusOnHold, true},
		{"completed past due", at(-time.Hour), models.TaskStatusCompleted, false},
		{"cancelled past due", at(-time.Hour), models.TaskStatusCancelled, false},
		{"nil due date pending", nil, models.TaskStatusPending, false},
		{"nil due date in_progress", nil, models.TaskStatusInProgress, false},
		{"future due date pending", at(time.Hour), models.TaskStatusPending, false},
		{"future due date on_hold", at(time.Hour), models.TaskStatusOnHold, false},
		{"exactly now is not overdue", at(0), models.TaskStatusPending, false},
		{"one nanosecond before now is overdue", at(-time.Nanosecond), models.TaskStatusPending, true},
		{"one nanosecond after now is not overdue", at(time.Nanosecond), models.TaskStatusPending, false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			task := addTask(t, svc, tt.name, tt.due, tt.status)

			resp, err := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 100})
			if err != nil {
				t.Fatalf("ListOverdueTasks: %v", err)
			}

			found := false
			for _, got := range resp.Tasks {
				if got.ID == task.ID {
					found = true
				}
			}
			if found != tt.overdue {
				t.Errorf("task present in overdue list = %v, want %v", found, tt.overdue)
			}

			// The two seeded tasks have no due date, so they never count.
			wantTotal := 0
			if tt.overdue {
				wantTotal = 1
			}
			if resp.TotalCount != wantTotal {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, wantTotal)
			}
			if len(resp.Tasks) != wantTotal {
				t.Errorf("len(Tasks) = %d, want %d", len(resp.Tasks), wantTotal)
			}
		})
	}
}

func TestListOverdueTasks_EmptyStore(t *testing.T) {
	svc := newTestService(t)
	resp, err := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 20})
	if err != nil {
		t.Fatalf("ListOverdueTasks: %v", err)
	}
	if resp.TotalCount != 0 || len(resp.Tasks) != 0 || resp.NextPageToken != "" {
		t.Errorf("got total=%d tasks=%d next=%q, want empty response", resp.TotalCount, len(resp.Tasks), resp.NextPageToken)
	}
}

func TestListOverdueTasks_UsesInjectedClock(t *testing.T) {
	svc := newTestService(t)
	due := at(-time.Hour)
	addTask(t, svc, "clock", due, models.TaskStatusPending)

	// Move the clock back before the due date: the task is no longer overdue.
	svc.now = func() time.Time { return due.Add(-time.Minute) }
	resp, _ := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 20})
	if resp.TotalCount != 0 {
		t.Errorf("TotalCount = %d with clock before due date, want 0", resp.TotalCount)
	}

	// Advance the clock past the due date: it becomes overdue.
	svc.now = func() time.Time { return due.Add(time.Minute) }
	resp, _ = svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 20})
	if resp.TotalCount != 1 {
		t.Errorf("TotalCount = %d with clock after due date, want 1", resp.TotalCount)
	}
}

func TestListOverdueTasks_AscendingOrder(t *testing.T) {
	svc := newTestService(t)
	// Created deliberately out of order.
	addTask(t, svc, "c", at(-1*time.Hour), models.TaskStatusPending)
	addTask(t, svc, "a", at(-72*time.Hour), models.TaskStatusInProgress)
	addTask(t, svc, "d", at(-time.Minute), models.TaskStatusOnHold)
	addTask(t, svc, "b", at(-24*time.Hour), models.TaskStatusPending)

	resp, err := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 100})
	if err != nil {
		t.Fatalf("ListOverdueTasks: %v", err)
	}
	want := []string{"a", "b", "c", "d"}
	if got := titlesOf(resp.Tasks); !equalStrings(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if resp.TotalCount != len(want) {
		t.Errorf("TotalCount = %d, want %d", resp.TotalCount, len(want))
	}
}

func TestListOverdueTasks_TieBreakByID(t *testing.T) {
	svc := newTestService(t)
	due := at(-2 * time.Hour)
	const n = 12
	var wantIDs []string
	for i := 0; i < n; i++ {
		// Equal instants expressed in different locations must still tie.
		d := due.In(time.FixedZone("z", (i%5)*3600))
		task := addTask(t, svc, fmt.Sprintf("tie-%d", i), &d, models.TaskStatusPending)
		wantIDs = append(wantIDs, task.ID)
	}
	sort.Strings(wantIDs)

	// Repeated calls (map iteration order is random) must give the same order.
	for run := 0; run < 25; run++ {
		resp, err := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 100})
		if err != nil {
			t.Fatalf("ListOverdueTasks: %v", err)
		}
		if got := idsOf(resp.Tasks); !equalStrings(got, wantIDs) {
			t.Fatalf("run %d: ids = %v, want ID-ascending %v", run, got, wantIDs)
		}
	}
}

func TestListOverdueTasks_TieBreakStablePagination(t *testing.T) {
	svc := newTestService(t)
	due := at(-time.Hour)
	const n = 9
	want := map[string]bool{}
	for i := 0; i < n; i++ {
		task := addTask(t, svc, fmt.Sprintf("p-%d", i), due, models.TaskStatusPending)
		want[task.ID] = true
	}

	for run := 0; run < 10; run++ {
		seen := map[string]bool{}
		token := ""
		for pages := 0; pages < n+1; pages++ {
			resp, err := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 2, PageToken: token})
			if err != nil {
				t.Fatalf("ListOverdueTasks: %v", err)
			}
			for _, task := range resp.Tasks {
				if seen[task.ID] {
					t.Fatalf("run %d: task %s repeated across pages", run, task.ID)
				}
				seen[task.ID] = true
			}
			if resp.NextPageToken == "" {
				break
			}
			token = resp.NextPageToken
		}
		if len(seen) != n {
			t.Fatalf("run %d: saw %d tasks across pages, want %d", run, len(seen), n)
		}
		for id := range want {
			if !seen[id] {
				t.Fatalf("run %d: task %s skipped across pages", run, id)
			}
		}
	}
}

func TestListOverdueTasks_Pagination(t *testing.T) {
	svc := newTestService(t)
	// Five overdue tasks t0 (oldest) .. t4, plus noise that must not count.
	for i := 0; i < 5; i++ {
		addTask(t, svc, fmt.Sprintf("t%d", i), at(-time.Duration(10-i)*time.Hour), models.TaskStatusPending)
	}
	addTask(t, svc, "done", at(-time.Hour), models.TaskStatusCompleted)
	addTask(t, svc, "future", at(time.Hour), models.TaskStatusPending)

	all := []string{"t0", "t1", "t2", "t3", "t4"}
	tests := []struct {
		name      string
		pageSize  int
		token     string
		wantTitle []string
		wantNext  string
	}{
		{"first page", 2, "", []string{"t0", "t1"}, "2"},
		{"second page", 2, "2", []string{"t2", "t3"}, "4"},
		{"last partial page", 2, "4", []string{"t4"}, ""},
		{"exact fit has no next token", 5, "", all, ""},
		{"page larger than total", 50, "", all, ""},
		{"default page size when zero", 0, "", all, ""},
		{"page size clamped to 100", 1000, "", all, ""},
		{"token at total", 2, "5", nil, ""},
		{"token beyond range", 2, "999", nil, ""},
		{"negative token is offset 0", 2, "-1", []string{"t0", "t1"}, "2"},
		{"large negative token is offset 0", 2, "-9999", []string{"t0", "t1"}, "2"},
		{"non-numeric token is offset 0", 2, "abc", []string{"t0", "t1"}, "2"},
		{"float token is offset 0", 2, "1.5", []string{"t0", "t1"}, "2"},
		{"zero token", 2, "0", []string{"t0", "t1"}, "2"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: tt.pageSize, PageToken: tt.token})
			if err != nil {
				t.Fatalf("ListOverdueTasks: %v", err)
			}
			if got := titlesOf(resp.Tasks); !equalStrings(got, tt.wantTitle) {
				t.Errorf("tasks = %v, want %v", got, tt.wantTitle)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
			if resp.TotalCount != 5 {
				t.Errorf("TotalCount = %d, want 5 regardless of page", resp.TotalCount)
			}
		})
	}
}

func TestListOverdueTasks_WalkAllPages(t *testing.T) {
	svc := newTestService(t)
	const n = 7
	for i := 0; i < n; i++ {
		addTask(t, svc, strconv.Itoa(i), at(-time.Duration(n-i)*time.Hour), models.TaskStatusPending)
	}

	var got []string
	token := ""
	for i := 0; i < n+1; i++ {
		resp, err := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 3, PageToken: token})
		if err != nil {
			t.Fatalf("ListOverdueTasks: %v", err)
		}
		got = append(got, titlesOf(resp.Tasks)...)
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	want := []string{"0", "1", "2", "3", "4", "5", "6"}
	if !equalStrings(got, want) {
		t.Errorf("walked pages = %v, want %v", got, want)
	}
}

func TestListOverdueTasks_ReturnsCopies(t *testing.T) {
	svc := newTestService(t)
	task := addTask(t, svc, "orig", at(-time.Hour), models.TaskStatusPending)

	resp, _ := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 10})
	if len(resp.Tasks) != 1 {
		t.Fatalf("len(Tasks) = %d, want 1", len(resp.Tasks))
	}
	resp.Tasks[0].Title = "mutated"

	stored, err := svc.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if stored.Title != "orig" {
		t.Errorf("stored title = %q, want it unaffected by mutating the response", stored.Title)
	}
}

// ListTasks regression tests for the shared pagination helper.

func TestListTasks_Pagination(t *testing.T) {
	svc := newTestService(t)
	for i := 0; i < 3; i++ {
		addTask(t, svc, fmt.Sprintf("x%d", i), nil, models.TaskStatusPending)
	}
	total := seededTaskCount + 3

	tests := []struct {
		name     string
		pageSize int
		token    string
		wantLen  int
		wantNext string
	}{
		{"first page", 2, "", 2, "2"},
		{"middle page", 2, "2", 2, "4"},
		{"last page", 2, "4", 1, ""},
		{"all in one page", 100, "", total, ""},
		{"default page size", 0, "", total, ""},
		{"token beyond range", 2, "100", 0, ""},
		{"negative token does not panic and means offset 0", 2, "-1", 2, "2"},
		{"very negative token means offset 0", 2, "-100000", 2, "2"},
		{"non-numeric token means offset 0", 2, "garbage", 2, "2"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: tt.pageSize, PageToken: tt.token})
			if err != nil {
				t.Fatalf("ListTasks: %v", err)
			}
			if len(resp.Tasks) != tt.wantLen {
				t.Errorf("len(Tasks) = %d, want %d", len(resp.Tasks), tt.wantLen)
			}
			if resp.NextPageToken != tt.wantNext {
				t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, tt.wantNext)
			}
			if resp.TotalCount != total {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, total)
			}
		})
	}
}

func TestListTasks_StatusFilterStillWorks(t *testing.T) {
	svc := newTestService(t)
	addTask(t, svc, "late", at(-time.Hour), models.TaskStatusPending)
	addTask(t, svc, "done", at(-time.Hour), models.TaskStatusCompleted)

	resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 100, Status: models.TaskStatusCompleted})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if resp.TotalCount != 1 || len(resp.Tasks) != 1 || resp.Tasks[0].Title != "done" {
		t.Errorf("status filter result = %v (total %d), want only \"done\"", titlesOf(resp.Tasks), resp.TotalCount)
	}

	resp, _ = svc.ListTasks(models.ListTasksQuery{PageSize: 100})
	if resp.TotalCount != seededTaskCount+2 {
		t.Errorf("TotalCount = %d, want %d", resp.TotalCount, seededTaskCount+2)
	}
}

func TestPaginateTasks(t *testing.T) {
	mk := func(n int) []*models.Task {
		out := make([]*models.Task, n)
		for i := range out {
			out[i] = &models.Task{ID: strconv.Itoa(i)}
		}
		return out
	}

	tests := []struct {
		name     string
		n        int
		pageSize int
		token    string
		wantLen  int
		wantNext string
	}{
		{"empty input", 0, 10, "", 0, ""},
		{"zero page size defaults to 20", 25, 0, "", 20, "20"},
		{"negative page size defaults to 20", 25, -5, "", 20, "20"},
		{"page size capped at 100", 150, 500, "", 100, "100"},
		{"token past end", 3, 2, "3", 0, ""},
		{"negative token", 3, 2, "-3", 2, "2"},
		{"invalid token", 3, 2, "x", 2, "2"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			page, next := paginateTasks(mk(tt.n), tt.pageSize, tt.token)
			if len(page) != tt.wantLen {
				t.Errorf("len(page) = %d, want %d", len(page), tt.wantLen)
			}
			if next != tt.wantNext {
				t.Errorf("next = %q, want %q", next, tt.wantNext)
			}
			if tt.wantLen > 0 && page[0].ID != "0" {
				t.Errorf("first id = %q, want 0 (offset 0)", page[0].ID)
			}
		})
	}
}

func TestAssignTask_Success(t *testing.T) {
	svc := newTestService(t)
	task := addTask(t, svc, "assign me", nil, models.TaskStatusPending)

	// Push UpdatedAt into the past so the test needs no sleeping.
	stored, err := svc.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	old := stored.UpdatedAt.Add(-time.Hour)
	stored.UpdatedAt = old

	got, err := svc.AssignTask(task.ID, "alice@example.com")
	if err != nil {
		t.Fatalf("AssignTask: %v", err)
	}
	if got.ID != task.ID || got.Title != "assign me" {
		t.Errorf("returned task = %+v, want id %s title %q", got, task.ID, "assign me")
	}
	if got.AssignedTo == nil || *got.AssignedTo != "alice@example.com" {
		t.Fatalf("returned AssignedTo = %v, want alice@example.com", got.AssignedTo)
	}
	if !got.UpdatedAt.After(old) {
		t.Errorf("UpdatedAt %v not after previous %v", got.UpdatedAt, old)
	}

	stored, err = svc.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if stored.AssignedTo == nil || *stored.AssignedTo != "alice@example.com" {
		t.Errorf("stored AssignedTo = %v, want alice@example.com", stored.AssignedTo)
	}
	if !stored.UpdatedAt.Equal(got.UpdatedAt) {
		t.Errorf("stored UpdatedAt %v != returned %v", stored.UpdatedAt, got.UpdatedAt)
	}
}

func TestAssignTask_AnyStatus(t *testing.T) {
	statuses := []models.TaskStatus{
		models.TaskStatusPending, models.TaskStatusInProgress, models.TaskStatusCompleted,
		models.TaskStatusCancelled, models.TaskStatusOnHold,
	}
	for _, status := range statuses {
		status := status
		t.Run(string(status), func(t *testing.T) {
			svc := newTestService(t)
			task := addTask(t, svc, "s", nil, status)
			got, err := svc.AssignTask(task.ID, "bob@example.com")
			if err != nil {
				t.Fatalf("AssignTask on %s task: %v", status, err)
			}
			if got.Status != status {
				t.Errorf("status = %s, want it unchanged (%s)", got.Status, status)
			}
			if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
				t.Errorf("AssignedTo = %v", got.AssignedTo)
			}
		})
	}
}

func TestAssignTask_Reassign(t *testing.T) {
	svc := newTestService(t)
	task := addTask(t, svc, "re", nil, models.TaskStatusPending)

	first, err := svc.AssignTask(task.ID, "one@example.com")
	if err != nil {
		t.Fatalf("first AssignTask: %v", err)
	}
	second, err := svc.AssignTask(task.ID, "two@example.com")
	if err != nil {
		t.Fatalf("second AssignTask: %v", err)
	}
	if *second.AssignedTo != "two@example.com" {
		t.Errorf("second AssignedTo = %q, want two@example.com", *second.AssignedTo)
	}
	if *first.AssignedTo != "one@example.com" {
		t.Errorf("earlier returned value changed to %q after reassignment", *first.AssignedTo)
	}
}

func TestAssignTask_NotFound(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"unknown uuid", "00000000-0000-0000-0000-000000000000"},
		{"empty id", ""},
		{"word id", "overdue"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			got, err := svc.AssignTask(tt.id, "a@example.com")
			if err == nil {
				t.Fatal("expected an error for an unknown task ID")
			}
			if got != nil {
				t.Errorf("returned task = %+v, want nil", got)
			}
			want := fmt.Sprintf("task with ID %s not found", tt.id)
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err.Error(), want)
			}
		})
	}
}

func TestAssignTask_NotFoundDoesNotMutateStore(t *testing.T) {
	svc := newTestService(t)
	before, _ := svc.ListTasks(models.ListTasksQuery{PageSize: 100})
	if _, err := svc.AssignTask("missing", "a@example.com"); err == nil {
		t.Fatal("expected error")
	}
	after, _ := svc.ListTasks(models.ListTasksQuery{PageSize: 100})
	if before.TotalCount != after.TotalCount {
		t.Errorf("TotalCount changed from %d to %d", before.TotalCount, after.TotalCount)
	}
}

func TestAssignTask_ReturnsSafeCopy(t *testing.T) {
	svc := newTestService(t)
	task := addTask(t, svc, "copy", nil, models.TaskStatusPending)

	got, err := svc.AssignTask(task.ID, "c@example.com")
	if err != nil {
		t.Fatalf("AssignTask: %v", err)
	}

	stored, _ := svc.GetTask(task.ID)
	if got == stored {
		t.Fatal("AssignTask returned the shared stored pointer, want a copy")
	}

	// Mutating the returned value must not leak into the store.
	got.Title = "changed"
	got.Status = models.TaskStatusCancelled
	if len(got.Tags) == 0 {
		t.Fatal("expected tags on the returned task")
	}
	got.Tags[0] = "mutated"

	stored, _ = svc.GetTask(task.ID)
	if stored.Title != "copy" {
		t.Errorf("stored Title = %q, want unchanged", stored.Title)
	}
	if stored.Status != models.TaskStatusPending {
		t.Errorf("stored Status = %s, want unchanged", stored.Status)
	}
	if stored.Tags[0] != "t1" {
		t.Errorf("stored Tags[0] = %q, want t1 (Tags must be cloned)", stored.Tags[0])
	}
}

func TestAssignTask_ConcurrentWithReads(t *testing.T) {
	svc := newTestService(t)
	task := addTask(t, svc, "race", at(-time.Hour), models.TaskStatusPending)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			got, err := svc.AssignTask(task.ID, fmt.Sprintf("u%d@example.com", i))
			if err != nil {
				t.Errorf("AssignTask: %v", err)
				return
			}
			// Read the returned copy outside the lock; the race detector verifies safety.
			_ = got.Title
			_ = got.UpdatedAt
		}()
		go func() {
			defer wg.Done()
			if _, err := svc.ListOverdueTasks(models.ListTasksQuery{PageSize: 10}); err != nil {
				t.Errorf("ListOverdueTasks: %v", err)
			}
		}()
	}
	wg.Wait()
}
