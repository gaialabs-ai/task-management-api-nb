package services

import (
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

var fixedNow = time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

func fixedClock() time.Time { return fixedNow }

type notifyCall struct {
	task       models.Task
	assignedTo string
}

type fakeNotifier struct {
	calls []notifyCall
	err   error
}

func (f *fakeNotifier) NotifyAssignment(task models.Task, assignedTo string) error {
	f.calls = append(f.calls, notifyCall{task: task, assignedTo: assignedTo})
	return f.err
}

func newSvc(opts ...Option) *TaskService {
	return NewTaskService(append([]Option{WithClock(fixedClock)}, opts...)...)
}

func addTask(t *testing.T, s *TaskService, title string, due *time.Time, status models.TaskStatus) *models.Task {
	t.Helper()
	task, err := s.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if status != "" && status != models.TaskStatusPending {
		if _, err := s.UpdateTaskStatus(task.ID, status); err != nil {
			t.Fatalf("update status: %v", err)
		}
	}
	return task
}

func at(d time.Duration) *time.Time {
	t := fixedNow.Add(d)
	return &t
}

func titles(tasks []models.Task) []string {
	out := make([]string, len(tasks))
	for i, tk := range tasks {
		out[i] = tk.Title
	}
	return out
}

func TestNewTaskService_SeedsTwoSampleTasksWithoutDueDate(t *testing.T) {
	s := NewTaskService()
	resp, err := s.ListTasks(models.ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 {
		t.Fatalf("expected 2 sample tasks, got total=%d len=%d", resp.TotalCount, len(resp.Tasks))
	}
	for _, tk := range resp.Tasks {
		if tk.DueDate != nil {
			t.Errorf("sample task %q should have no due date", tk.Title)
		}
	}
}

func TestNewTaskService_NilOptionsKeepDefaults(t *testing.T) {
	s := NewTaskService(WithClock(nil), WithNotifier(nil))
	if s.now == nil || s.notifier == nil {
		t.Fatal("nil options must not clear defaults")
	}
	resp, _ := s.ListTasks(models.ListTasksQuery{})
	if _, err := s.AssignTask(resp.Tasks[0].ID, "a@example.com"); err != nil {
		t.Fatalf("assign with default notifier: %v", err)
	}
}

func TestListOverdueTasks_SampleDataNotOverdue(t *testing.T) {
	s := newSvc()
	resp, err := s.ListOverdueTasks(models.ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 0 || len(resp.Tasks) != 0 || resp.NextPageToken != "" {
		t.Fatalf("expected empty overdue list, got %+v", resp)
	}
}

func TestListOverdueTasks_DueDateFiltering(t *testing.T) {
	s := newSvc()
	addTask(t, s, "past", at(-time.Hour), "")
	addTask(t, s, "future", at(time.Hour), "")
	addTask(t, s, "nodue", nil, "")
	addTask(t, s, "exactly-now", at(0), "")

	resp, err := s.ListOverdueTasks(models.ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(resp.Tasks); len(got) != 1 || got[0] != "past" {
		t.Fatalf("expected only [past], got %v", got)
	}
	if resp.TotalCount != 1 {
		t.Errorf("total_count = %d", resp.TotalCount)
	}
}

func TestListOverdueTasks_StatusFiltering(t *testing.T) {
	s := newSvc()
	past := at(-24 * time.Hour)
	for _, st := range []models.TaskStatus{
		models.TaskStatusPending, models.TaskStatusInProgress, models.TaskStatusOnHold,
		models.TaskStatusCompleted, models.TaskStatusCancelled,
	} {
		addTask(t, s, string(st), past, st)
	}
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{})
	got := map[string]bool{}
	for _, tk := range resp.Tasks {
		got[tk.Title] = true
	}
	for _, want := range []string{"pending", "in_progress", "on_hold"} {
		if !got[want] {
			t.Errorf("%s should be overdue", want)
		}
	}
	for _, no := range []string{"completed", "cancelled"} {
		if got[no] {
			t.Errorf("%s must be excluded", no)
		}
	}
	if resp.TotalCount != 3 {
		t.Errorf("total_count = %d, want 3", resp.TotalCount)
	}
}

func TestListOverdueTasks_UsesInjectedClock(t *testing.T) {
	now := fixedNow
	s := NewTaskService(WithClock(func() time.Time { return now }))
	addTask(t, s, "t", at(time.Hour), "")
	if r, _ := s.ListOverdueTasks(models.ListTasksQuery{}); r.TotalCount != 0 {
		t.Fatalf("should not be overdue yet")
	}
	now = fixedNow.Add(2 * time.Hour)
	if r, _ := s.ListOverdueTasks(models.ListTasksQuery{}); r.TotalCount != 1 {
		t.Fatalf("should be overdue after clock advance")
	}
}

func TestListOverdueTasks_SortedByDueDateAscending(t *testing.T) {
	s := newSvc()
	addTask(t, s, "c", at(-1*time.Hour), "")
	addTask(t, s, "a", at(-72*time.Hour), "")
	addTask(t, s, "b", at(-24*time.Hour), "")
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{})
	got := titles(resp.Tasks)
	want := []string{"a", "b", "c"}
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestListOverdueTasks_Pagination(t *testing.T) {
	s := newSvc()
	for i := 0; i < 5; i++ {
		addTask(t, s, fmt.Sprintf("t%d", i), at(-time.Duration(10-i)*time.Hour), "")
	}
	var all []string
	token := ""
	pages := 0
	for {
		resp, err := s.ListOverdueTasks(models.ListTasksQuery{PageSize: 2, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if resp.TotalCount != 5 {
			t.Errorf("total_count = %d", resp.TotalCount)
		}
		all = append(all, titles(resp.Tasks)...)
		if resp.NextPageToken == "" {
			if len(resp.Tasks) != 1 {
				t.Errorf("last page size = %d, want 1", len(resp.Tasks))
			}
			break
		}
		if len(resp.Tasks) != 2 {
			t.Errorf("page size = %d, want 2", len(resp.Tasks))
		}
		token = resp.NextPageToken
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if pages != 3 {
		t.Errorf("pages = %d, want 3", pages)
	}
	want := []string{"t0", "t1", "t2", "t3", "t4"}
	if len(all) != 5 {
		t.Fatalf("collected %v", all)
	}
	for i := range want {
		if all[i] != want[i] {
			t.Fatalf("collected %v, want %v", all, want)
		}
	}
}

func TestListOverdueTasks_ExactPageBoundaryHasNoToken(t *testing.T) {
	s := newSvc()
	addTask(t, s, "a", at(-2*time.Hour), "")
	addTask(t, s, "b", at(-time.Hour), "")
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{PageSize: 2})
	if resp.NextPageToken != "" || len(resp.Tasks) != 2 {
		t.Fatalf("unexpected %+v", resp)
	}
}

func TestListOverdueTasks_TokenPastEndAndInvalid(t *testing.T) {
	s := newSvc()
	addTask(t, s, "a", at(-time.Hour), "")
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{PageSize: 5, PageToken: "50"})
	if len(resp.Tasks) != 0 || resp.NextPageToken != "" || resp.TotalCount != 1 {
		t.Fatalf("unexpected %+v", resp)
	}
	resp, _ = s.ListOverdueTasks(models.ListTasksQuery{PageSize: 5, PageToken: "garbage"})
	if len(resp.Tasks) != 1 {
		t.Fatalf("invalid token should start from beginning, got %+v", resp)
	}
}

func TestListOverdueTasks_PageSizeDefaultAndClamp(t *testing.T) {
	s := newSvc()
	for i := 0; i < 120; i++ {
		addTask(t, s, "t"+strconv.Itoa(i), at(-time.Duration(i+1)*time.Minute), "")
	}
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{})
	if len(resp.Tasks) != 20 || resp.NextPageToken != "20" || resp.TotalCount != 120 {
		t.Errorf("default: len=%d token=%q total=%d", len(resp.Tasks), resp.NextPageToken, resp.TotalCount)
	}
	resp, _ = s.ListOverdueTasks(models.ListTasksQuery{PageSize: 1000})
	if len(resp.Tasks) != 100 || resp.NextPageToken != "100" {
		t.Errorf("clamp: len=%d token=%q", len(resp.Tasks), resp.NextPageToken)
	}
}

func TestAssignTask_Success(t *testing.T) {
	n := &fakeNotifier{}
	clock := fixedNow.Add(3 * time.Hour)
	s := NewTaskService(WithClock(func() time.Time { return clock }), WithNotifier(n))
	task := addTask(t, s, "x", nil, "")

	got, err := s.AssignTask(task.ID, "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
		t.Fatalf("AssignedTo = %v", got.AssignedTo)
	}
	if !got.UpdatedAt.Equal(clock) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, clock)
	}
	stored, _ := s.GetTask(task.ID)
	if stored.AssignedTo == nil || *stored.AssignedTo != "bob@example.com" || !stored.UpdatedAt.Equal(clock) {
		t.Errorf("stored task not updated: %+v", stored)
	}
}

func TestAssignTask_Reassign(t *testing.T) {
	n := &fakeNotifier{}
	s := newSvc(WithNotifier(n))
	task := addTask(t, s, "x", nil, "")
	if _, err := s.AssignTask(task.ID, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	got, err := s.AssignTask(task.ID, "b@example.com")
	if err != nil || *got.AssignedTo != "b@example.com" {
		t.Fatalf("reassign failed: %v %+v", err, got)
	}
	if len(n.calls) != 2 {
		t.Errorf("notifier calls = %d, want 2", len(n.calls))
	}
}

func TestAssignTask_NotFound(t *testing.T) {
	n := &fakeNotifier{}
	s := newSvc(WithNotifier(n))
	got, err := s.AssignTask("missing-id", "a@example.com")
	if err == nil || got != nil {
		t.Fatalf("expected error, got %v %v", got, err)
	}
	if err.Error() != "task with ID missing-id not found" {
		t.Errorf("message = %q", err.Error())
	}
	if len(n.calls) != 0 {
		t.Errorf("notifier must not be called, got %d", len(n.calls))
	}
}

func TestAssignTask_InvalidEmail(t *testing.T) {
	for _, email := range []string{"", "not-an-email", "a@", "@b.com", "Bob <bob@example.com>", " a@example.com"} {
		email := email
		t.Run(email, func(t *testing.T) {
			n := &fakeNotifier{}
			s := newSvc(WithNotifier(n))
			task := addTask(t, s, "x", nil, "")
			before, _ := s.GetTask(task.ID)
			beforeUpdated := before.UpdatedAt
			got, err := s.AssignTask(task.ID, email)
			if err == nil || got != nil {
				t.Fatalf("expected error for %q", email)
			}
			if len(n.calls) != 0 {
				t.Errorf("notifier must not be called")
			}
			after, _ := s.GetTask(task.ID)
			if after.AssignedTo != nil || !after.UpdatedAt.Equal(beforeUpdated) {
				t.Errorf("task mutated on failure: %+v", after)
			}
		})
	}
}

func TestAssignTask_InvalidEmailOnMissingTask(t *testing.T) {
	s := newSvc()
	if _, err := s.AssignTask("nope", "bad"); err == nil {
		t.Fatal("expected error")
	}
}

func TestAssignTask_NotifierCalledOnceWithArgs(t *testing.T) {
	n := &fakeNotifier{}
	s := newSvc(WithNotifier(n))
	task := addTask(t, s, "x", nil, "")
	if _, err := s.AssignTask(task.ID, "carol@example.com"); err != nil {
		t.Fatal(err)
	}
	if len(n.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(n.calls))
	}
	c := n.calls[0]
	if c.assignedTo != "carol@example.com" {
		t.Errorf("assignedTo = %q", c.assignedTo)
	}
	if c.task.ID != task.ID || c.task.Title != "x" {
		t.Errorf("task = %+v", c.task)
	}
	if c.task.AssignedTo == nil || *c.task.AssignedTo != "carol@example.com" {
		t.Errorf("notified task should carry the new assignee: %+v", c.task.AssignedTo)
	}
}

func TestAssignTask_NotifierFailureTolerated(t *testing.T) {
	n := &fakeNotifier{err: errors.New("smtp down")}
	s := newSvc(WithNotifier(n))
	task := addTask(t, s, "x", nil, "")
	got, err := s.AssignTask(task.ID, "dave@example.com")
	if err != nil {
		t.Fatalf("notifier failure must not fail assignment: %v", err)
	}
	if got == nil || got.AssignedTo == nil || *got.AssignedTo != "dave@example.com" {
		t.Fatalf("unexpected result %+v", got)
	}
	stored, _ := s.GetTask(task.ID)
	if stored.AssignedTo == nil || *stored.AssignedTo != "dave@example.com" {
		t.Error("task not persisted as assigned")
	}
	if len(n.calls) != 1 {
		t.Errorf("calls = %d", len(n.calls))
	}
}

func TestAssignTask_ReturnsCopy(t *testing.T) {
	s := newSvc()
	task := addTask(t, s, "x", nil, "")
	got, _ := s.AssignTask(task.ID, "a@example.com")
	got.Title = "mutated"
	stored, _ := s.GetTask(task.ID)
	if stored.Title != "x" {
		t.Error("AssignTask should return a copy")
	}
}

// ---- ListTasks regression ----

func TestListTasks_DefaultsAndSampleData(t *testing.T) {
	s := newSvc()
	resp, err := s.ListTasks(models.ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 || resp.NextPageToken != "" {
		t.Fatalf("unexpected %+v", resp)
	}
}

// Pagination with a deterministic sort order (distinct due dates).
func TestListTasks_PaginationWithSortOrder(t *testing.T) {
	s := newSvc()
	for i := 0; i < 3; i++ {
		addTask(t, s, fmt.Sprintf("t%d", i), at(time.Duration(i+1)*time.Hour), "")
	}
	var order []string
	token := ""
	for i := 0; i < 5; i++ {
		resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 2, PageToken: token, SortOrder: models.SortOrderDueDateAsc})
		if resp.TotalCount != 5 {
			t.Errorf("total = %d", resp.TotalCount)
		}
		order = append(order, titles(resp.Tasks)...)
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	if len(order) != 5 || order[0] != "t0" || order[1] != "t1" || order[2] != "t2" {
		t.Errorf("order = %v", order)
	}
}

// With no sort order, pages must still be stable and non-overlapping.
// NOTE: currently fails because ListTasks iterates a map without a default
// ordering (production bug: unstable pagination when sort_order is empty).
func TestListTasks_Pagination(t *testing.T) {
	s := newSvc()
	for i := 0; i < 3; i++ {
		addTask(t, s, fmt.Sprintf("t%d", i), nil, "")
	}
	seen := map[string]bool{}
	token := ""
	for i := 0; i < 5; i++ {
		resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 2, PageToken: token})
		if resp.TotalCount != 5 {
			t.Errorf("total = %d", resp.TotalCount)
		}
		for _, tk := range resp.Tasks {
			if seen[tk.ID] {
				t.Errorf("duplicate task across pages")
			}
			seen[tk.ID] = true
		}
		if i == 0 && resp.NextPageToken != "2" {
			t.Errorf("first token = %q", resp.NextPageToken)
		}
		if resp.NextPageToken == "" {
			if i != 2 || len(resp.Tasks) != 1 {
				t.Errorf("last page at i=%d len=%d", i, len(resp.Tasks))
			}
			break
		}
		token = resp.NextPageToken
	}
	if len(seen) != 5 {
		t.Errorf("saw %d tasks, want 5", len(seen))
	}
}

func TestListTasks_PageSizeClamp(t *testing.T) {
	s := newSvc()
	for i := 0; i < 110; i++ {
		addTask(t, s, "t", nil, "")
	}
	resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 500})
	if len(resp.Tasks) != 100 || resp.NextPageToken != "100" || resp.TotalCount != 112 {
		t.Errorf("len=%d token=%q total=%d", len(resp.Tasks), resp.NextPageToken, resp.TotalCount)
	}
	resp, _ = s.ListTasks(models.ListTasksQuery{PageSize: -3})
	if len(resp.Tasks) != 20 {
		t.Errorf("negative page size should default to 20, got %d", len(resp.Tasks))
	}
}

func TestListTasks_Filters(t *testing.T) {
	s := newSvc()
	a := addTask(t, s, "a", nil, models.TaskStatusInProgress)
	if _, err := s.AssignTask(a.ID, "x@example.com"); err != nil {
		t.Fatal(err)
	}

	resp, _ := s.ListTasks(models.ListTasksQuery{Status: models.TaskStatusInProgress})
	if resp.TotalCount != 1 || resp.Tasks[0].ID != a.ID {
		t.Errorf("status filter: %+v", resp)
	}
	resp, _ = s.ListTasks(models.ListTasksQuery{AssignedTo: "x@example.com"})
	if resp.TotalCount != 1 || resp.Tasks[0].ID != a.ID {
		t.Errorf("assigned filter: %+v", resp)
	}
	resp, _ = s.ListTasks(models.ListTasksQuery{Tags: "go,grpc"})
	if resp.TotalCount != 1 || resp.Tasks[0].Title != "Add gRPC support" {
		t.Errorf("tags filter: %+v", titles(resp.Tasks))
	}
	resp, _ = s.ListTasks(models.ListTasksQuery{Tags: "go"})
	if resp.TotalCount != 2 {
		t.Errorf("tag go: %d", resp.TotalCount)
	}
	resp, _ = s.ListTasks(models.ListTasksQuery{Tags: "nonexistent"})
	if resp.TotalCount != 0 || len(resp.Tasks) != 0 {
		t.Errorf("no match: %+v", resp)
	}
}

func TestListTasks_Sort(t *testing.T) {
	s := newSvc()
	resp, _ := s.ListTasks(models.ListTasksQuery{SortOrder: models.SortOrderPriorityDesc})
	if got := titles(resp.Tasks); got[0] != "Implement Go REST API" {
		t.Errorf("priority_desc: %v", got)
	}
	resp, _ = s.ListTasks(models.ListTasksQuery{SortOrder: models.SortOrderPriorityAsc})
	if got := titles(resp.Tasks); got[0] != "Add gRPC support" {
		t.Errorf("priority_asc: %v", got)
	}

	early := addTask(t, s, "early", at(-48*time.Hour), "")
	late := addTask(t, s, "late", at(48*time.Hour), "")
	resp, _ = s.ListTasks(models.ListTasksQuery{SortOrder: models.SortOrderDueDateAsc})
	if resp.Tasks[0].ID != early.ID || resp.Tasks[1].ID != late.ID {
		t.Errorf("due_date_asc: %v", titles(resp.Tasks))
	}
	resp, _ = s.ListTasks(models.ListTasksQuery{SortOrder: models.SortOrderDueDateDesc})
	if resp.Tasks[0].ID != late.ID || resp.Tasks[1].ID != early.ID {
		t.Errorf("due_date_desc: %v", titles(resp.Tasks))
	}

	resp, _ = s.ListTasks(models.ListTasksQuery{SortOrder: models.SortOrderCreatedAtAsc})
	for i := 1; i < len(resp.Tasks); i++ {
		if resp.Tasks[i].CreatedAt.Before(resp.Tasks[i-1].CreatedAt) {
			t.Errorf("created_at_asc out of order")
		}
	}
	resp, _ = s.ListTasks(models.ListTasksQuery{SortOrder: models.SortOrderCreatedAtDesc})
	for i := 1; i < len(resp.Tasks); i++ {
		if resp.Tasks[i].CreatedAt.After(resp.Tasks[i-1].CreatedAt) {
			t.Errorf("created_at_desc out of order")
		}
	}
}

func TestListTasks_IncludesOverdueCompletedAndFuture(t *testing.T) {
	s := newSvc()
	addTask(t, s, "past", at(-time.Hour), "")
	addTask(t, s, "done", at(-time.Hour), models.TaskStatusCompleted)
	addTask(t, s, "future", at(time.Hour), "")
	resp, _ := s.ListTasks(models.ListTasksQuery{})
	if resp.TotalCount != 5 {
		t.Errorf("total = %d, want 5", resp.TotalCount)
	}
}
