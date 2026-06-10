package services

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/task-management-api-nb/task-management-api-nb/internal/models"
)

// newCleanService creates a TaskService with no pre-populated data.
// Since the test file is in the same package, it can access unexported fields.
func newCleanService() *TaskService {
	return &TaskService{tasks: make(map[string]*models.Task)}
}

// helper to create a string pointer
func strPtr(s string) *string {
	return &s
}

// helper to create a TaskStatus pointer
func statusPtr(s models.TaskStatus) *models.TaskStatus {
	return &s
}

// helper to create a TaskPriority pointer
func priorityPtr(p models.TaskPriority) *models.TaskPriority {
	return &p
}

// =============================================================================
// Task Creation Tests
// =============================================================================

func TestCreateTask_ValidInput_DefaultValues(t *testing.T) {
	svc := newCleanService()

	task, err := svc.CreateTask(models.CreateTaskRequest{
		Title: "Test Task",
	})

	require.NoError(t, err)
	require.NotNil(t, task)

	assert.NotEmpty(t, task.ID, "ID should be generated")
	assert.Equal(t, "Test Task", task.Title)
	assert.Equal(t, models.TaskStatusPending, task.Status, "default status should be pending")
	assert.Equal(t, models.TaskPriorityMedium, task.Priority, "default priority should be medium")
	assert.NotNil(t, task.Tags, "tags should not be nil")
	assert.Empty(t, task.Tags, "tags should default to empty slice")
	assert.False(t, task.CreatedAt.IsZero(), "CreatedAt should be set")
	assert.False(t, task.UpdatedAt.IsZero(), "UpdatedAt should be set")
}

func TestCreateTask_AllFieldsPopulated(t *testing.T) {
	svc := newCleanService()

	dueDate := time.Now().Add(72 * time.Hour)
	assignee := "alice"

	task, err := svc.CreateTask(models.CreateTaskRequest{
		Title:       "Full Task",
		Description: "A task with all fields",
		Priority:    models.TaskPriorityHigh,
		Tags:        []string{"backend", "urgent"},
		AssignedTo:  &assignee,
		DueDate:     &dueDate,
	})

	require.NoError(t, err)
	require.NotNil(t, task)

	assert.Equal(t, "Full Task", task.Title)
	assert.Equal(t, "A task with all fields", task.Description)
	assert.Equal(t, models.TaskPriorityHigh, task.Priority)
	assert.Equal(t, []string{"backend", "urgent"}, task.Tags)
	assert.NotNil(t, task.AssignedTo)
	assert.Equal(t, "alice", *task.AssignedTo)
	assert.NotNil(t, task.DueDate)
	assert.Equal(t, dueDate.Unix(), task.DueDate.Unix())
}

func TestCreateTask_InvalidPriority(t *testing.T) {
	svc := newCleanService()

	task, err := svc.CreateTask(models.CreateTaskRequest{
		Title:    "Bad Priority",
		Priority: models.TaskPriority("ultra"),
	})

	assert.Nil(t, task)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidPriority), "should return ErrInvalidPriority")
}

// =============================================================================
// Task Retrieval Tests
// =============================================================================

func TestGetTask_Existing(t *testing.T) {
	svc := newCleanService()

	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "Findable"})
	require.NoError(t, err)

	found, err := svc.GetTask(created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, "Findable", found.Title)
}

func TestGetTask_NonExisting(t *testing.T) {
	svc := newCleanService()

	found, err := svc.GetTask("does-not-exist")
	assert.Nil(t, found)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTaskNotFound), "should return ErrTaskNotFound")
}

// =============================================================================
// Task Update Tests
// =============================================================================

func TestUpdateTask_TitleOnly(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "Original"})
	require.NoError(t, err)

	updated, err := svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Title: strPtr("Renamed"),
	})
	require.NoError(t, err)
	assert.Equal(t, "Renamed", updated.Title)
	// Other fields should remain unchanged
	assert.Equal(t, created.Description, updated.Description)
	assert.Equal(t, created.Priority, updated.Priority)
}

func TestUpdateTask_DescriptionOnly(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	require.NoError(t, err)

	updated, err := svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Description: strPtr("New description"),
	})
	require.NoError(t, err)
	assert.Equal(t, "New description", updated.Description)
	assert.Equal(t, "T", updated.Title)
}

func TestUpdateTask_StatusToValidValue(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	require.NoError(t, err)

	updated, err := svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Status: statusPtr(models.TaskStatusInProgress),
	})
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusInProgress, updated.Status)
}

func TestUpdateTask_StatusToCompleted_SetsCompletedAt(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	require.NoError(t, err)
	assert.Nil(t, created.CompletedAt, "CompletedAt should be nil initially")

	before := time.Now()
	updated, err := svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Status: statusPtr(models.TaskStatusCompleted),
	})
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusCompleted, updated.Status)
	require.NotNil(t, updated.CompletedAt, "CompletedAt should be set when completed")
	assert.False(t, updated.CompletedAt.Before(before), "CompletedAt should be >= time before update")
}

func TestUpdateTask_PriorityToValidValue(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	require.NoError(t, err)

	updated, err := svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Priority: priorityPtr(models.TaskPriorityCritical),
	})
	require.NoError(t, err)
	assert.Equal(t, models.TaskPriorityCritical, updated.Priority)
}

func TestUpdateTask_Tags(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "T", Tags: []string{"old"}})
	require.NoError(t, err)

	updated, err := svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Tags: []string{"new", "tags"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"new", "tags"}, updated.Tags)
}

func TestUpdateTask_AssignedTo(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	require.NoError(t, err)

	updated, err := svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		AssignedTo: strPtr("bob"),
	})
	require.NoError(t, err)
	require.NotNil(t, updated.AssignedTo)
	assert.Equal(t, "bob", *updated.AssignedTo)
}

func TestUpdateTask_DueDate(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	require.NoError(t, err)

	due := time.Now().Add(48 * time.Hour)
	updated, err := svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		DueDate: &due,
	})
	require.NoError(t, err)
	require.NotNil(t, updated.DueDate)
	assert.Equal(t, due.Unix(), updated.DueDate.Unix())
}

func TestUpdateTask_InvalidStatus(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	require.NoError(t, err)

	_, err = svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Status: statusPtr(models.TaskStatus("bogus")),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidStatus), "should return ErrInvalidStatus")
}

func TestUpdateTask_InvalidPriority(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	require.NoError(t, err)

	_, err = svc.UpdateTask(created.ID, models.UpdateTaskRequest{
		Priority: priorityPtr(models.TaskPriority("bogus")),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidPriority), "should return ErrInvalidPriority")
}

func TestUpdateTask_NonExisting(t *testing.T) {
	svc := newCleanService()

	_, err := svc.UpdateTask("nonexistent", models.UpdateTaskRequest{
		Title: strPtr("X"),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTaskNotFound), "should return ErrTaskNotFound")
}

// =============================================================================
// Task Status Update Tests
// =============================================================================

func TestUpdateTaskStatus_Valid(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	require.NoError(t, err)

	updated, err := svc.UpdateTaskStatus(created.ID, models.TaskStatusInProgress)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusInProgress, updated.Status)
}

func TestUpdateTaskStatus_NonExisting(t *testing.T) {
	svc := newCleanService()

	_, err := svc.UpdateTaskStatus("nonexistent", models.TaskStatusInProgress)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTaskNotFound), "should return ErrTaskNotFound")
}

// =============================================================================
// Task Deletion Tests
// =============================================================================

func TestDeleteTask_Existing(t *testing.T) {
	svc := newCleanService()
	created, err := svc.CreateTask(models.CreateTaskRequest{Title: "Doomed"})
	require.NoError(t, err)

	err = svc.DeleteTask(created.ID)
	require.NoError(t, err)

	// Verify it is gone
	_, err = svc.GetTask(created.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTaskNotFound))
}

func TestDeleteTask_NonExisting(t *testing.T) {
	svc := newCleanService()

	err := svc.DeleteTask("nonexistent")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTaskNotFound), "should return ErrTaskNotFound")
}

// =============================================================================
// List Tasks - Filtering Tests
// =============================================================================

func TestListTasks_NoFilters(t *testing.T) {
	svc := newCleanService()
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "A"})
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "B"})

	resp, err := svc.ListTasks(models.ListTasksQuery{})
	require.NoError(t, err)
	assert.Len(t, resp.Tasks, 2)
	assert.Equal(t, 2, resp.TotalCount)
}

func TestListTasks_FilterByStatus(t *testing.T) {
	svc := newCleanService()
	task1, _ := svc.CreateTask(models.CreateTaskRequest{Title: "A"})
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "B"})
	_, _ = svc.UpdateTaskStatus(task1.ID, models.TaskStatusInProgress)

	resp, err := svc.ListTasks(models.ListTasksQuery{
		Status: models.TaskStatusInProgress,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Tasks, 1)
	assert.Equal(t, "A", resp.Tasks[0].Title)
}

func TestListTasks_FilterByAssignedTo(t *testing.T) {
	svc := newCleanService()
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "A", AssignedTo: strPtr("alice")})
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "B", AssignedTo: strPtr("bob")})
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "C"}) // unassigned

	resp, err := svc.ListTasks(models.ListTasksQuery{
		AssignedTo: "alice",
	})
	require.NoError(t, err)
	assert.Len(t, resp.Tasks, 1)
	assert.Equal(t, "A", resp.Tasks[0].Title)
}

func TestListTasks_FilterByTagsSingle(t *testing.T) {
	svc := newCleanService()
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "A", Tags: []string{"go", "api"}})
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "B", Tags: []string{"python"}})

	resp, err := svc.ListTasks(models.ListTasksQuery{
		Tags: "go",
	})
	require.NoError(t, err)
	assert.Len(t, resp.Tasks, 1)
	assert.Equal(t, "A", resp.Tasks[0].Title)
}

func TestListTasks_FilterByTagsMultiple(t *testing.T) {
	svc := newCleanService()
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "A", Tags: []string{"go", "api", "rest"}})
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "B", Tags: []string{"go", "grpc"}})

	resp, err := svc.ListTasks(models.ListTasksQuery{
		Tags: "go,api",
	})
	require.NoError(t, err)
	assert.Len(t, resp.Tasks, 1)
	assert.Equal(t, "A", resp.Tasks[0].Title)
}

func TestListTasks_FilterNoMatchingResults(t *testing.T) {
	svc := newCleanService()
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "A"})

	resp, err := svc.ListTasks(models.ListTasksQuery{
		Status: models.TaskStatusCompleted,
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Tasks)
	assert.Equal(t, 0, resp.TotalCount)
}

// =============================================================================
// List Tasks - Sorting Tests (all 6 sort orders)
// =============================================================================

func TestListTasks_Sorting(t *testing.T) {
	// Helper that creates three tasks with staggered times and different priorities/due dates.
	setupSortService := func(t *testing.T) (*TaskService, string, string, string) {
		t.Helper()
		svc := newCleanService()

		due1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		due3 := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

		task1, err := svc.CreateTask(models.CreateTaskRequest{
			Title:    "Task1",
			Priority: models.TaskPriorityLow,
			DueDate:  &due1,
		})
		require.NoError(t, err)

		// Small sleep to ensure distinct CreatedAt timestamps
		time.Sleep(2 * time.Millisecond)

		task2, err := svc.CreateTask(models.CreateTaskRequest{
			Title:    "Task2",
			Priority: models.TaskPriorityHigh,
			// nil DueDate
		})
		require.NoError(t, err)

		time.Sleep(2 * time.Millisecond)

		task3, err := svc.CreateTask(models.CreateTaskRequest{
			Title:    "Task3",
			Priority: models.TaskPriorityCritical,
			DueDate:  &due3,
		})
		require.NoError(t, err)

		return svc, task1.ID, task2.ID, task3.ID
	}

	tests := []struct {
		name           string
		sortOrder      models.SortOrder
		expectedTitles []string
	}{
		{
			name:           "created_at ascending",
			sortOrder:      models.SortOrderCreatedAtAsc,
			expectedTitles: []string{"Task1", "Task2", "Task3"},
		},
		{
			name:           "created_at descending",
			sortOrder:      models.SortOrderCreatedAtDesc,
			expectedTitles: []string{"Task3", "Task2", "Task1"},
		},
		{
			name:           "due_date ascending (nil due dates sort last)",
			sortOrder:      models.SortOrderDueDateAsc,
			expectedTitles: []string{"Task1", "Task3", "Task2"},
		},
		{
			name:           "due_date descending (nil due dates sort last)",
			sortOrder:      models.SortOrderDueDateDesc,
			expectedTitles: []string{"Task3", "Task1", "Task2"},
		},
		{
			name:           "priority ascending",
			sortOrder:      models.SortOrderPriorityAsc,
			expectedTitles: []string{"Task1", "Task2", "Task3"},
		},
		{
			name:           "priority descending",
			sortOrder:      models.SortOrderPriorityDesc,
			expectedTitles: []string{"Task3", "Task2", "Task1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _, _ := setupSortService(t)

			resp, err := svc.ListTasks(models.ListTasksQuery{
				SortOrder: tc.sortOrder,
			})
			require.NoError(t, err)
			require.Len(t, resp.Tasks, 3)

			var titles []string
			for _, task := range resp.Tasks {
				titles = append(titles, task.Title)
			}
			assert.Equal(t, tc.expectedTitles, titles, "sort order: %s", tc.sortOrder)
		})
	}
}

// =============================================================================
// List Tasks - Pagination Tests
// =============================================================================

func TestListTasks_DefaultPageSize(t *testing.T) {
	svc := newCleanService()
	// Create 25 tasks
	for i := 0; i < 25; i++ {
		_, err := svc.CreateTask(models.CreateTaskRequest{Title: "T"})
		require.NoError(t, err)
	}

	resp, err := svc.ListTasks(models.ListTasksQuery{
		SortOrder: models.SortOrderCreatedAtAsc,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Tasks, 20, "default page size should be 20")
	assert.Equal(t, 25, resp.TotalCount)
	assert.NotEmpty(t, resp.NextPageToken, "should have next page token")
}

func TestListTasks_CustomPageSize(t *testing.T) {
	svc := newCleanService()
	for i := 0; i < 10; i++ {
		_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	}

	resp, err := svc.ListTasks(models.ListTasksQuery{
		PageSize:  5,
		SortOrder: models.SortOrderCreatedAtAsc,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Tasks, 5)
	assert.Equal(t, 10, resp.TotalCount)
}

func TestListTasks_PageTokenForSecondPage(t *testing.T) {
	svc := newCleanService()
	for i := 0; i < 10; i++ {
		_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	}

	// Get first page
	resp1, err := svc.ListTasks(models.ListTasksQuery{
		PageSize:  3,
		SortOrder: models.SortOrderCreatedAtAsc,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp1.NextPageToken)

	// Get second page
	resp2, err := svc.ListTasks(models.ListTasksQuery{
		PageSize:  3,
		PageToken: resp1.NextPageToken,
		SortOrder: models.SortOrderCreatedAtAsc,
	})
	require.NoError(t, err)
	assert.Len(t, resp2.Tasks, 3)
	assert.Equal(t, 10, resp2.TotalCount)

	// Ensure pages don't overlap
	for _, t1 := range resp1.Tasks {
		for _, t2 := range resp2.Tasks {
			assert.NotEqual(t, t1.ID, t2.ID, "pages should not overlap")
		}
	}
}

func TestListTasks_PageTokenBeyondAvailable(t *testing.T) {
	svc := newCleanService()
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "Only"})

	resp, err := svc.ListTasks(models.ListTasksQuery{
		PageToken: "999",
		SortOrder: models.SortOrderCreatedAtAsc,
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Tasks)
}

func TestListTasks_NextPageTokenPresentWhenMoreItems(t *testing.T) {
	svc := newCleanService()
	for i := 0; i < 5; i++ {
		_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	}

	resp, err := svc.ListTasks(models.ListTasksQuery{
		PageSize:  3,
		SortOrder: models.SortOrderCreatedAtAsc,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.NextPageToken, "should have next_page_token when more items exist")
}

func TestListTasks_NextPageTokenEmptyOnLastPage(t *testing.T) {
	svc := newCleanService()
	for i := 0; i < 3; i++ {
		_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "T"})
	}

	resp, err := svc.ListTasks(models.ListTasksQuery{
		PageSize:  5,
		SortOrder: models.SortOrderCreatedAtAsc,
	})
	require.NoError(t, err)
	assert.Empty(t, resp.NextPageToken, "should be empty when on last page")
}

func TestListTasks_TotalCountReflectsFilteredCount(t *testing.T) {
	svc := newCleanService()
	task1, _ := svc.CreateTask(models.CreateTaskRequest{Title: "A"})
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "B"})
	_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "C"})
	_, _ = svc.UpdateTaskStatus(task1.ID, models.TaskStatusInProgress)

	resp, err := svc.ListTasks(models.ListTasksQuery{
		Status: models.TaskStatusInProgress,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, resp.TotalCount, "total_count should reflect filtered count, not total")
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestConcurrentAccess_MultipleCreates(t *testing.T) {
	svc := newCleanService()
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	errCh := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := svc.CreateTask(models.CreateTaskRequest{
				Title: "Concurrent Task",
			})
			if err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err, "no errors expected during concurrent creates")
	}

	resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 100})
	require.NoError(t, err)
	assert.Equal(t, goroutines, resp.TotalCount, "all tasks should be created")
}

func TestConcurrentAccess_ReadsAndWrites(t *testing.T) {
	svc := newCleanService()
	const goroutines = 50

	// Pre-create some tasks
	var taskIDs []string
	for i := 0; i < 10; i++ {
		task, err := svc.CreateTask(models.CreateTaskRequest{Title: "Existing"})
		require.NoError(t, err)
		taskIDs = append(taskIDs, task.ID)
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			switch idx % 4 {
			case 0:
				// Create
				_, _ = svc.CreateTask(models.CreateTaskRequest{Title: "New"})
			case 1:
				// Read
				_, _ = svc.GetTask(taskIDs[idx%len(taskIDs)])
			case 2:
				// Update
				_, _ = svc.UpdateTask(taskIDs[idx%len(taskIDs)], models.UpdateTaskRequest{
					Title: strPtr("Updated"),
				})
			case 3:
				// List
				_, _ = svc.ListTasks(models.ListTasksQuery{})
			}
		}(i)
	}

	wg.Wait()
	// If we reach here without panic, the test passes.
	// Verify the service is still functional.
	resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 100})
	require.NoError(t, err)
	assert.True(t, resp.TotalCount >= 10, "should have at least the original tasks")
}
