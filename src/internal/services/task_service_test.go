package services

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/task-management/task-management-api-nb/internal/models"
)

// newCleanService creates a TaskService without sample data for isolated tests.
func newCleanService() *TaskService {
	return &TaskService{
		tasks: make(map[string]*models.Task),
	}
}

// stringPtr is a helper to get a pointer to a string.
func stringPtr(s string) *string {
	return &s
}

// timePtr is a helper to get a pointer to a time.Time.
func timePtr(t time.Time) *time.Time {
	return &t
}

// statusPtr is a helper to get a pointer to a TaskStatus.
func statusPtr(s models.TaskStatus) *models.TaskStatus {
	return &s
}

// priorityPtr is a helper to get a pointer to a TaskPriority.
func priorityPtr(p models.TaskPriority) *models.TaskPriority {
	return &p
}

// ---------------------------------------------------------------------------
// 1. CRUD Operations
// ---------------------------------------------------------------------------

func TestCreateTask(t *testing.T) {
	svc := newCleanService()
	assignee := "alice"
	dueDate := time.Now().Add(24 * time.Hour)

	req := models.CreateTaskRequest{
		Title:       "Test Task",
		Description: "A task for testing",
		Priority:    models.TaskPriorityHigh,
		Tags:        []string{"test", "unit"},
		AssignedTo:  &assignee,
		DueDate:     &dueDate,
	}

	task, err := svc.CreateTask(req)
	assert.NoError(t, err)
	assert.NotNil(t, task)

	// ID must be set (UUID format)
	assert.NotEmpty(t, task.ID)

	// Fields match request
	assert.Equal(t, "Test Task", task.Title)
	assert.Equal(t, "A task for testing", task.Description)
	assert.Equal(t, models.TaskPriorityHigh, task.Priority)
	assert.Equal(t, []string{"test", "unit"}, task.Tags)
	assert.Equal(t, &assignee, task.AssignedTo)
	assert.WithinDuration(t, dueDate, *task.DueDate, time.Second)

	// Default status must be pending
	assert.Equal(t, models.TaskStatusPending, task.Status)

	// Timestamps must be set and close to now
	assert.WithinDuration(t, time.Now(), task.CreatedAt, 2*time.Second)
	assert.WithinDuration(t, time.Now(), task.UpdatedAt, 2*time.Second)

	// CompletedAt must be nil for a new task
	assert.Nil(t, task.CompletedAt)

	// CreatedBy defaults to "system"
	assert.Equal(t, "system", task.CreatedBy)
}

func TestGetTask(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{
		Title: "Fetchable Task",
	})
	assert.NoError(t, err)

	// Get existing task
	fetched, err := svc.GetTask(created.ID)
	assert.NoError(t, err)
	assert.NotNil(t, fetched)
	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, "Fetchable Task", fetched.Title)
}

func TestGetTask_NotFound(t *testing.T) {
	svc := newCleanService()
	task, err := svc.GetTask("nonexistent-id")
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.Contains(t, err.Error(), "not found")
}

func TestUpdateTask(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{
		Title:       "Original Title",
		Description: "Original Description",
		Priority:    models.TaskPriorityLow,
		Tags:        []string{"old"},
	})
	assert.NoError(t, err)
	originalUpdatedAt := created.UpdatedAt

	// Small delay to ensure UpdatedAt changes
	time.Sleep(2 * time.Millisecond)

	// Update title
	updated, err := svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Title: stringPtr("Updated Title"),
	})
	assert.NoError(t, err)
	assert.Equal(t, "Updated Title", updated.Title)
	assert.True(t, updated.UpdatedAt.After(originalUpdatedAt))

	// Update description
	time.Sleep(time.Millisecond)
	prev := updated.UpdatedAt
	updated, err = svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Description: stringPtr("Updated Description"),
	})
	assert.NoError(t, err)
	assert.Equal(t, "Updated Description", updated.Description)
	assert.True(t, updated.UpdatedAt.After(prev) || updated.UpdatedAt.Equal(prev))

	// Update status
	updated, err = svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Status: statusPtr(models.TaskStatusInProgress),
	})
	assert.NoError(t, err)
	assert.Equal(t, models.TaskStatusInProgress, updated.Status)

	// Update priority
	updated, err = svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Priority: priorityPtr(models.TaskPriorityCritical),
	})
	assert.NoError(t, err)
	assert.Equal(t, models.TaskPriorityCritical, updated.Priority)

	// Update tags
	updated, err = svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Tags: []string{"new", "tags"},
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{"new", "tags"}, updated.Tags)

	// Update assigned_to
	updated, err = svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		AssignedTo: stringPtr("bob"),
	})
	assert.NoError(t, err)
	assert.Equal(t, "bob", *updated.AssignedTo)

	// Update due_date
	newDue := time.Now().Add(48 * time.Hour)
	updated, err = svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		DueDate: &newDue,
	})
	assert.NoError(t, err)
	assert.NotNil(t, updated.DueDate)
	assert.WithinDuration(t, newDue, *updated.DueDate, time.Second)
}

func TestUpdateTaskStatus(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{
		Title: "Status Test",
	})
	assert.NoError(t, err)

	// Update to in_progress
	updated, err := svc.UpdateTaskStatus(created.ID, models.TaskStatusInProgress)
	assert.NoError(t, err)
	assert.Equal(t, models.TaskStatusInProgress, updated.Status)
	assert.Nil(t, updated.CompletedAt)

	// Update to completed -- should set CompletedAt
	updated, err = svc.UpdateTaskStatus(created.ID, models.TaskStatusCompleted)
	assert.NoError(t, err)
	assert.Equal(t, models.TaskStatusCompleted, updated.Status)
	assert.NotNil(t, updated.CompletedAt)
	assert.WithinDuration(t, time.Now(), *updated.CompletedAt, 2*time.Second)
}

func TestDeleteTask(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{
		Title: "To Delete",
	})
	assert.NoError(t, err)

	// Delete existing task
	err = svc.DeleteTask(created.ID)
	assert.NoError(t, err)

	// Verify task is gone
	task, err := svc.GetTask(created.ID)
	assert.Error(t, err)
	assert.Nil(t, task)
}

func TestDeleteTask_NotFound(t *testing.T) {
	svc := newCleanService()
	err := svc.DeleteTask("nonexistent-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// 2. Filtering
// ---------------------------------------------------------------------------

func TestListTasks_FilterByStatus(t *testing.T) {
	svc := newCleanService()

	_, err := svc.CreateTask(models.CreateTaskRequest{Title: "Pending One"})
	assert.NoError(t, err)
	task2, err := svc.CreateTask(models.CreateTaskRequest{Title: "In Progress One"})
	assert.NoError(t, err)
	_, err = svc.UpdateTaskStatus(task2.ID, models.TaskStatusInProgress)
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Pending Two"})
	assert.NoError(t, err)

	resp, err := svc.ListTasks(models.ListTasksQuery{
		Status: models.TaskStatusPending,
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, resp.TotalCount)
	for _, task := range resp.Tasks {
		assert.Equal(t, models.TaskStatusPending, task.Status)
	}
}

func TestListTasks_FilterByAssignedTo(t *testing.T) {
	svc := newCleanService()

	_, err := svc.CreateTask(models.CreateTaskRequest{
		Title:      "Alice Task",
		AssignedTo: stringPtr("alice"),
	})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{
		Title:      "Bob Task",
		AssignedTo: stringPtr("bob"),
	})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{
		Title: "Unassigned Task",
	})
	assert.NoError(t, err)

	resp, err := svc.ListTasks(models.ListTasksQuery{
		AssignedTo: "alice",
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, resp.TotalCount)
	assert.Equal(t, "Alice Task", resp.Tasks[0].Title)
}

func TestListTasks_FilterByTags(t *testing.T) {
	svc := newCleanService()

	_, err := svc.CreateTask(models.CreateTaskRequest{
		Title: "Go API",
		Tags:  []string{"go", "api"},
	})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{
		Title: "Go CLI",
		Tags:  []string{"go", "cli"},
	})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{
		Title: "Python API",
		Tags:  []string{"python", "api"},
	})
	assert.NoError(t, err)

	// Filter by single tag
	resp, err := svc.ListTasks(models.ListTasksQuery{
		Tags: "go",
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, resp.TotalCount)

	// Filter by multiple tags (intersection) -- task must have ALL tags
	resp, err = svc.ListTasks(models.ListTasksQuery{
		Tags: "go,api",
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, resp.TotalCount)
	assert.Equal(t, "Go API", resp.Tasks[0].Title)
}

func TestListTasks_NoFilter(t *testing.T) {
	svc := NewTaskService() // includes 2 sample tasks

	resp, err := svc.ListTasks(models.ListTasksQuery{})
	assert.NoError(t, err)
	assert.Equal(t, 2, resp.TotalCount)
	assert.Len(t, resp.Tasks, 2)
}

// ---------------------------------------------------------------------------
// 3. Sorting
// ---------------------------------------------------------------------------

func TestListTasks_SortCreatedAtAsc(t *testing.T) {
	svc := newCleanService()

	_, err := svc.CreateTask(models.CreateTaskRequest{Title: "First"})
	assert.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Second"})
	assert.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Third"})
	assert.NoError(t, err)

	resp, err := svc.ListTasks(models.ListTasksQuery{
		SortOrder: models.SortOrderCreatedAtAsc,
	})
	assert.NoError(t, err)
	assert.Equal(t, 3, resp.TotalCount)
	assert.Equal(t, "First", resp.Tasks[0].Title)
	assert.Equal(t, "Second", resp.Tasks[1].Title)
	assert.Equal(t, "Third", resp.Tasks[2].Title)
}

func TestListTasks_SortCreatedAtDesc(t *testing.T) {
	svc := newCleanService()

	_, err := svc.CreateTask(models.CreateTaskRequest{Title: "First"})
	assert.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Second"})
	assert.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Third"})
	assert.NoError(t, err)

	resp, err := svc.ListTasks(models.ListTasksQuery{
		SortOrder: models.SortOrderCreatedAtDesc,
	})
	assert.NoError(t, err)
	assert.Equal(t, 3, resp.TotalCount)
	assert.Equal(t, "Third", resp.Tasks[0].Title)
	assert.Equal(t, "Second", resp.Tasks[1].Title)
	assert.Equal(t, "First", resp.Tasks[2].Title)
}

func TestListTasks_SortDueDateAsc(t *testing.T) {
	svc := newCleanService()

	now := time.Now()
	_, err := svc.CreateTask(models.CreateTaskRequest{
		Title:   "Later",
		DueDate: timePtr(now.Add(48 * time.Hour)),
	})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{
		Title:   "Sooner",
		DueDate: timePtr(now.Add(24 * time.Hour)),
	})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{
		Title: "No Due Date",
	})
	assert.NoError(t, err)

	resp, err := svc.ListTasks(models.ListTasksQuery{
		SortOrder: models.SortOrderDueDateAsc,
	})
	assert.NoError(t, err)
	assert.Equal(t, 3, resp.TotalCount)
	// Nil due dates sort last in ascending order
	assert.Equal(t, "Sooner", resp.Tasks[0].Title)
	assert.Equal(t, "Later", resp.Tasks[1].Title)
	assert.Equal(t, "No Due Date", resp.Tasks[2].Title)
}

func TestListTasks_SortDueDateDesc(t *testing.T) {
	svc := newCleanService()

	now := time.Now()
	_, err := svc.CreateTask(models.CreateTaskRequest{
		Title:   "Later",
		DueDate: timePtr(now.Add(48 * time.Hour)),
	})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{
		Title:   "Sooner",
		DueDate: timePtr(now.Add(24 * time.Hour)),
	})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{
		Title: "No Due Date",
	})
	assert.NoError(t, err)

	resp, err := svc.ListTasks(models.ListTasksQuery{
		SortOrder: models.SortOrderDueDateDesc,
	})
	assert.NoError(t, err)
	assert.Equal(t, 3, resp.TotalCount)
	// Nil due dates sort last in descending order too (per implementation)
	assert.Equal(t, "Later", resp.Tasks[0].Title)
	assert.Equal(t, "Sooner", resp.Tasks[1].Title)
	assert.Equal(t, "No Due Date", resp.Tasks[2].Title)
}

func TestListTasks_SortPriorityAsc(t *testing.T) {
	svc := newCleanService()

	_, err := svc.CreateTask(models.CreateTaskRequest{Title: "High", Priority: models.TaskPriorityHigh})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Low", Priority: models.TaskPriorityLow})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Critical", Priority: models.TaskPriorityCritical})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Medium", Priority: models.TaskPriorityMedium})
	assert.NoError(t, err)

	resp, err := svc.ListTasks(models.ListTasksQuery{
		SortOrder: models.SortOrderPriorityAsc,
	})
	assert.NoError(t, err)
	assert.Equal(t, 4, resp.TotalCount)
	assert.Equal(t, "Low", resp.Tasks[0].Title)
	assert.Equal(t, "Medium", resp.Tasks[1].Title)
	assert.Equal(t, "High", resp.Tasks[2].Title)
	assert.Equal(t, "Critical", resp.Tasks[3].Title)
}

func TestListTasks_SortPriorityDesc(t *testing.T) {
	svc := newCleanService()

	_, err := svc.CreateTask(models.CreateTaskRequest{Title: "High", Priority: models.TaskPriorityHigh})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Low", Priority: models.TaskPriorityLow})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Critical", Priority: models.TaskPriorityCritical})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Medium", Priority: models.TaskPriorityMedium})
	assert.NoError(t, err)

	resp, err := svc.ListTasks(models.ListTasksQuery{
		SortOrder: models.SortOrderPriorityDesc,
	})
	assert.NoError(t, err)
	assert.Equal(t, 4, resp.TotalCount)
	assert.Equal(t, "Critical", resp.Tasks[0].Title)
	assert.Equal(t, "High", resp.Tasks[1].Title)
	assert.Equal(t, "Medium", resp.Tasks[2].Title)
	assert.Equal(t, "Low", resp.Tasks[3].Title)
}

// ---------------------------------------------------------------------------
// 4. Pagination
// ---------------------------------------------------------------------------

func TestListTasks_Pagination_DefaultPageSize(t *testing.T) {
	svc := newCleanService()

	// Create 25 tasks
	for i := 0; i < 25; i++ {
		_, err := svc.CreateTask(models.CreateTaskRequest{
			Title: "Task",
		})
		assert.NoError(t, err)
	}

	resp, err := svc.ListTasks(models.ListTasksQuery{})
	assert.NoError(t, err)
	assert.Len(t, resp.Tasks, 20)
	assert.Equal(t, 25, resp.TotalCount)
	assert.NotEmpty(t, resp.NextPageToken)
}

func TestListTasks_Pagination_CustomPageSize(t *testing.T) {
	svc := newCleanService()

	_, err := svc.CreateTask(models.CreateTaskRequest{Title: "Task A"})
	assert.NoError(t, err)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Task B"})
	assert.NoError(t, err)

	resp, err := svc.ListTasks(models.ListTasksQuery{
		PageSize: 1,
	})
	assert.NoError(t, err)
	assert.Len(t, resp.Tasks, 1)
	assert.Equal(t, 2, resp.TotalCount)
	assert.NotEmpty(t, resp.NextPageToken)
}

func TestListTasks_Pagination_PageToken(t *testing.T) {
	svc := newCleanService()

	// Create tasks with known order (use created_at_asc for deterministic results)
	time.Sleep(time.Millisecond)
	_, err := svc.CreateTask(models.CreateTaskRequest{Title: "Task A"})
	assert.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Task B"})
	assert.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	_, err = svc.CreateTask(models.CreateTaskRequest{Title: "Task C"})
	assert.NoError(t, err)

	// Get first page
	resp, err := svc.ListTasks(models.ListTasksQuery{
		PageSize:  2,
		SortOrder: models.SortOrderCreatedAtAsc,
	})
	assert.NoError(t, err)
	assert.Len(t, resp.Tasks, 2)
	assert.Equal(t, 3, resp.TotalCount)
	assert.NotEmpty(t, resp.NextPageToken)

	// Get second page
	resp2, err := svc.ListTasks(models.ListTasksQuery{
		PageSize:  2,
		PageToken: resp.NextPageToken,
		SortOrder: models.SortOrderCreatedAtAsc,
	})
	assert.NoError(t, err)
	assert.Len(t, resp2.Tasks, 1)
	assert.Equal(t, 3, resp2.TotalCount)
	assert.Empty(t, resp2.NextPageToken)
	assert.Equal(t, "Task C", resp2.Tasks[0].Title)
}

func TestListTasks_Pagination_EmptyPage(t *testing.T) {
	svc := newCleanService()

	_, err := svc.CreateTask(models.CreateTaskRequest{Title: "Only Task"})
	assert.NoError(t, err)

	// Request page beyond results
	resp, err := svc.ListTasks(models.ListTasksQuery{
		PageToken: "999",
	})
	assert.NoError(t, err)
	assert.Len(t, resp.Tasks, 0)
	assert.Equal(t, 1, resp.TotalCount)
	assert.Empty(t, resp.NextPageToken)
}

func TestListTasks_Pagination_MaxPageSize(t *testing.T) {
	svc := newCleanService()

	// Create 5 tasks
	for i := 0; i < 5; i++ {
		_, err := svc.CreateTask(models.CreateTaskRequest{Title: "Task"})
		assert.NoError(t, err)
	}

	// Request page_size > 100 should be clamped to 100
	resp, err := svc.ListTasks(models.ListTasksQuery{
		PageSize: 200,
	})
	assert.NoError(t, err)
	// With only 5 tasks and clamped page size 100, all tasks returned
	assert.Len(t, resp.Tasks, 5)
	assert.Equal(t, 5, resp.TotalCount)
	assert.Empty(t, resp.NextPageToken)
}

// ---------------------------------------------------------------------------
// 5. Validation
// ---------------------------------------------------------------------------

func TestCreateTask_InvalidPriority(t *testing.T) {
	svc := newCleanService()

	task, err := svc.CreateTask(models.CreateTaskRequest{
		Title:    "Bad Priority",
		Priority: models.TaskPriority("urgent"),
	})
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.Contains(t, err.Error(), "invalid priority")
}

func TestUpdateTask_InvalidStatus(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "Valid Task"})
	assert.NoError(t, err)

	badStatus := models.TaskStatus("invalid_status")
	updated, err := svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Status: &badStatus,
	})
	assert.Error(t, err)
	assert.Nil(t, updated)
	assert.Contains(t, err.Error(), "invalid status")
}

func TestUpdateTask_InvalidPriority(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "Valid Task"})
	assert.NoError(t, err)

	badPriority := models.TaskPriority("super_high")
	updated, err := svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Priority: &badPriority,
	})
	assert.Error(t, err)
	assert.Nil(t, updated)
	assert.Contains(t, err.Error(), "invalid priority")
}

// ---------------------------------------------------------------------------
// 6. Edge cases
// ---------------------------------------------------------------------------

func TestCreateTask_NilTags(t *testing.T) {
	svc := newCleanService()

	task, err := svc.CreateTask(models.CreateTaskRequest{
		Title: "No Tags",
		Tags:  nil,
	})
	assert.NoError(t, err)
	assert.NotNil(t, task.Tags)
	assert.Len(t, task.Tags, 0)
	assert.Equal(t, []string{}, task.Tags)
}

func TestCreateTask_DefaultPriority(t *testing.T) {
	svc := newCleanService()

	task, err := svc.CreateTask(models.CreateTaskRequest{
		Title: "Default Priority Task",
		// Priority left empty
	})
	assert.NoError(t, err)
	assert.Equal(t, models.TaskPriorityMedium, task.Priority)
}

func TestUpdateTask_CompletedAt(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "CompletedAt Test"})
	assert.NoError(t, err)

	// Move to in_progress -- CompletedAt should remain nil
	updated, err := svc.UpdateTaskStatus(created.ID, models.TaskStatusInProgress)
	assert.NoError(t, err)
	assert.Nil(t, updated.CompletedAt)

	// Move to on_hold -- CompletedAt should remain nil
	updated, err = svc.UpdateTaskStatus(created.ID, models.TaskStatusOnHold)
	assert.NoError(t, err)
	assert.Nil(t, updated.CompletedAt)

	// Move to completed -- CompletedAt should be set
	updated, err = svc.UpdateTaskStatus(created.ID, models.TaskStatusCompleted)
	assert.NoError(t, err)
	assert.NotNil(t, updated.CompletedAt)
	assert.WithinDuration(t, time.Now(), *updated.CompletedAt, 2*time.Second)

	// Move to cancelled after completed -- CompletedAt should still be set
	// (the service only sets CompletedAt, never clears it)
	completedAt := *updated.CompletedAt
	updated, err = svc.UpdateTaskStatus(created.ID, models.TaskStatusCancelled)
	assert.NoError(t, err)
	assert.NotNil(t, updated.CompletedAt)
	assert.Equal(t, completedAt, *updated.CompletedAt)
}

func TestListTasks_TotalCount(t *testing.T) {
	svc := newCleanService()

	// Create 3 pending and 2 in_progress tasks
	for i := 0; i < 3; i++ {
		_, err := svc.CreateTask(models.CreateTaskRequest{Title: "Pending"})
		assert.NoError(t, err)
	}
	for i := 0; i < 2; i++ {
		task, err := svc.CreateTask(models.CreateTaskRequest{Title: "InProgress"})
		assert.NoError(t, err)
		_, err = svc.UpdateTaskStatus(task.ID, models.TaskStatusInProgress)
		assert.NoError(t, err)
	}

	// Total without filter should be 5
	resp, err := svc.ListTasks(models.ListTasksQuery{})
	assert.NoError(t, err)
	assert.Equal(t, 5, resp.TotalCount)

	// TotalCount with status filter should reflect filtered count only
	resp, err = svc.ListTasks(models.ListTasksQuery{
		Status: models.TaskStatusPending,
	})
	assert.NoError(t, err)
	assert.Equal(t, 3, resp.TotalCount)

	resp, err = svc.ListTasks(models.ListTasksQuery{
		Status: models.TaskStatusInProgress,
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, resp.TotalCount)
}
