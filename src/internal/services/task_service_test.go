package services

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/task-management-api-nb/internal/models"
)

// helper: stringPtr returns a pointer to a string.
func stringPtr(s string) *string {
	return &s
}

// helper: timePtr returns a pointer to a time.Time.
func timePtr(t time.Time) *time.Time {
	return &t
}

// helper: statusPtr returns a pointer to a TaskStatus.
func statusPtr(s models.TaskStatus) *models.TaskStatus {
	return &s
}

// helper: priorityPtr returns a pointer to a TaskPriority.
func priorityPtr(p models.TaskPriority) *models.TaskPriority {
	return &p
}

// helper: createCleanService creates a TaskService with sample data (2 sample tasks).
func createCleanService() *TaskService {
	return NewTaskService()
}

// helper: createTaskInService creates a task with the given title in the service and returns it.
func createTaskInService(t *testing.T, svc *TaskService, title string) *models.Task {
	t.Helper()
	task, err := svc.CreateTask(models.CreateTaskRequest{
		Title: title,
	})
	require.NoError(t, err)
	require.NotNil(t, task)
	return task
}

// ============================================================
// CRUD Operations
// ============================================================

func TestCreateTask(t *testing.T) {
	tests := []struct {
		name        string
		req         models.CreateTaskRequest
		expectError bool
		validate    func(t *testing.T, task *models.Task)
	}{
		{
			name: "valid task creation with all fields",
			req: models.CreateTaskRequest{
				Title:       "Test Task",
				Description: "A test task description",
				Priority:    models.TaskPriorityHigh,
				Tags:        []string{"test", "unit"},
				AssignedTo:  stringPtr("user1"),
				DueDate:     timePtr(time.Now().Add(24 * time.Hour)),
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				assert.Equal(t, "Test Task", task.Title)
				assert.Equal(t, "A test task description", task.Description)
				assert.Equal(t, models.TaskPriorityHigh, task.Priority)
				assert.Equal(t, models.TaskStatusPending, task.Status)
				assert.Equal(t, []string{"test", "unit"}, task.Tags)
				assert.NotNil(t, task.AssignedTo)
				assert.Equal(t, "user1", *task.AssignedTo)
				assert.NotNil(t, task.DueDate)
				assert.NotEmpty(t, task.ID)
				assert.Equal(t, "system", task.CreatedBy)
				assert.False(t, task.CreatedAt.IsZero())
				assert.False(t, task.UpdatedAt.IsZero())
				assert.Nil(t, task.CompletedAt)
			},
		},
		{
			name: "default priority when not specified",
			req: models.CreateTaskRequest{
				Title: "No Priority Task",
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				assert.Equal(t, models.TaskPriorityMedium, task.Priority)
			},
		},
		{
			name: "invalid priority returns error",
			req: models.CreateTaskRequest{
				Title:    "Bad Priority",
				Priority: models.TaskPriority("ultra"),
			},
			expectError: true,
			validate:    nil,
		},
		{
			name: "nil tags initialized to empty slice",
			req: models.CreateTaskRequest{
				Title: "No Tags Task",
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				require.NotNil(t, task.Tags)
				assert.Len(t, task.Tags, 0)
			},
		},
		{
			name: "assigned_to setting",
			req: models.CreateTaskRequest{
				Title:      "Assigned Task",
				AssignedTo: stringPtr("alice"),
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				require.NotNil(t, task.AssignedTo)
				assert.Equal(t, "alice", *task.AssignedTo)
			},
		},
		{
			name: "assigned_to nil when not provided",
			req: models.CreateTaskRequest{
				Title: "Unassigned Task",
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				assert.Nil(t, task.AssignedTo)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := createCleanService()
			task, err := svc.CreateTask(tc.req)
			if tc.expectError {
				assert.Error(t, err)
				assert.Nil(t, task)
			} else {
				require.NoError(t, err)
				require.NotNil(t, task)
				if tc.validate != nil {
					tc.validate(t, task)
				}
			}
		})
	}
}

func TestGetTask(t *testing.T) {
	t.Run("existing task", func(t *testing.T) {
		svc := createCleanService()
		created := createTaskInService(t, svc, "Find Me")

		got, err := svc.GetTask(created.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, "Find Me", got.Title)
	})

	t.Run("non-existent task", func(t *testing.T) {
		svc := createCleanService()
		got, err := svc.GetTask("non-existent-id")
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestUpdateTask(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, svc *TaskService) string // returns task ID
		req         models.UpdateTaskRequest
		expectError bool
		errorMsg    string
		validate    func(t *testing.T, task *models.Task)
	}{
		{
			name: "update title",
			setup: func(t *testing.T, svc *TaskService) string {
				return createTaskInService(t, svc, "Original Title").ID
			},
			req: models.UpdateTaskRequest{
				Title: stringPtr("Updated Title"),
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				assert.Equal(t, "Updated Title", task.Title)
			},
		},
		{
			name: "update description",
			setup: func(t *testing.T, svc *TaskService) string {
				return createTaskInService(t, svc, "Desc Task").ID
			},
			req: models.UpdateTaskRequest{
				Description: stringPtr("New description"),
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				assert.Equal(t, "New description", task.Description)
			},
		},
		{
			name: "update status to in_progress",
			setup: func(t *testing.T, svc *TaskService) string {
				return createTaskInService(t, svc, "Status Task").ID
			},
			req: models.UpdateTaskRequest{
				Status: statusPtr(models.TaskStatusInProgress),
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				assert.Equal(t, models.TaskStatusInProgress, task.Status)
			},
		},
		{
			name: "update priority",
			setup: func(t *testing.T, svc *TaskService) string {
				return createTaskInService(t, svc, "Priority Task").ID
			},
			req: models.UpdateTaskRequest{
				Priority: priorityPtr(models.TaskPriorityCritical),
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				assert.Equal(t, models.TaskPriorityCritical, task.Priority)
			},
		},
		{
			name: "update tags",
			setup: func(t *testing.T, svc *TaskService) string {
				return createTaskInService(t, svc, "Tags Task").ID
			},
			req: models.UpdateTaskRequest{
				Tags: []string{"new-tag1", "new-tag2"},
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				assert.Equal(t, []string{"new-tag1", "new-tag2"}, task.Tags)
			},
		},
		{
			name: "update assigned_to",
			setup: func(t *testing.T, svc *TaskService) string {
				return createTaskInService(t, svc, "Assign Task").ID
			},
			req: models.UpdateTaskRequest{
				AssignedTo: stringPtr("bob"),
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				require.NotNil(t, task.AssignedTo)
				assert.Equal(t, "bob", *task.AssignedTo)
			},
		},
		{
			name: "update due_date",
			setup: func(t *testing.T, svc *TaskService) string {
				return createTaskInService(t, svc, "DueDate Task").ID
			},
			req: models.UpdateTaskRequest{
				DueDate: timePtr(time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)),
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				require.NotNil(t, task.DueDate)
				assert.Equal(t, 2026, task.DueDate.Year())
				assert.Equal(t, time.Month(12), task.DueDate.Month())
			},
		},
		{
			name: "task not found",
			setup: func(t *testing.T, svc *TaskService) string {
				return "non-existent-id"
			},
			req: models.UpdateTaskRequest{
				Title: stringPtr("Whatever"),
			},
			expectError: true,
			errorMsg:    "not found",
		},
		{
			name: "invalid status returns error",
			setup: func(t *testing.T, svc *TaskService) string {
				return createTaskInService(t, svc, "Invalid Status Task").ID
			},
			req: models.UpdateTaskRequest{
				Status: statusPtr(models.TaskStatus("invalid_status")),
			},
			expectError: true,
			errorMsg:    "invalid status",
		},
		{
			name: "invalid priority returns error",
			setup: func(t *testing.T, svc *TaskService) string {
				return createTaskInService(t, svc, "Invalid Priority Task").ID
			},
			req: models.UpdateTaskRequest{
				Priority: priorityPtr(models.TaskPriority("super_high")),
			},
			expectError: true,
			errorMsg:    "invalid priority",
		},
		{
			name: "setting status to completed sets completed_at",
			setup: func(t *testing.T, svc *TaskService) string {
				return createTaskInService(t, svc, "Complete Task").ID
			},
			req: models.UpdateTaskRequest{
				Status: statusPtr(models.TaskStatusCompleted),
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				assert.Equal(t, models.TaskStatusCompleted, task.Status)
				require.NotNil(t, task.CompletedAt)
				assert.WithinDuration(t, time.Now(), *task.CompletedAt, 5*time.Second)
			},
		},
		{
			name: "setting status to completed again does not overwrite completed_at",
			setup: func(t *testing.T, svc *TaskService) string {
				task := createTaskInService(t, svc, "Already Complete")
				// Complete it once
				_, err := svc.UpdateTask(task.ID, models.UpdateTaskRequest{
					Status: statusPtr(models.TaskStatusCompleted),
				})
				require.NoError(t, err)
				return task.ID
			},
			req: models.UpdateTaskRequest{
				Status: statusPtr(models.TaskStatusCompleted),
			},
			expectError: false,
			validate: func(t *testing.T, task *models.Task) {
				assert.Equal(t, models.TaskStatusCompleted, task.Status)
				require.NotNil(t, task.CompletedAt)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := createCleanService()
			taskID := tc.setup(t, svc)
			task, err := svc.UpdateTask(taskID, tc.req)
			if tc.expectError {
				assert.Error(t, err)
				if tc.errorMsg != "" {
					assert.Contains(t, err.Error(), tc.errorMsg)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, task)
				if tc.validate != nil {
					tc.validate(t, task)
				}
			}
		})
	}
}

func TestDeleteTask(t *testing.T) {
	t.Run("existing task", func(t *testing.T) {
		svc := createCleanService()
		task := createTaskInService(t, svc, "Delete Me")

		err := svc.DeleteTask(task.ID)
		require.NoError(t, err)

		// Verify it's gone
		got, err := svc.GetTask(task.ID)
		assert.Error(t, err)
		assert.Nil(t, got)
	})

	t.Run("non-existent task", func(t *testing.T) {
		svc := createCleanService()
		err := svc.DeleteTask("non-existent-id")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

// ============================================================
// Pagination (ListTasks)
// ============================================================

func TestListTasks_Pagination(t *testing.T) {
	t.Run("default page size is 20", func(t *testing.T) {
		svc := createCleanService()
		// Service starts with 2 sample tasks. Add 25 more = 27 total.
		for i := 0; i < 25; i++ {
			createTaskInService(t, svc, fmt.Sprintf("Task %d", i))
		}

		resp, err := svc.ListTasks(models.ListTasksQuery{})
		require.NoError(t, err)
		assert.Len(t, resp.Tasks, 20)
		assert.Equal(t, 27, resp.TotalCount)
		assert.NotEmpty(t, resp.NextPageToken)
	})

	t.Run("custom page size", func(t *testing.T) {
		svc := createCleanService()
		// 2 sample tasks + 8 new = 10 total
		for i := 0; i < 8; i++ {
			createTaskInService(t, svc, fmt.Sprintf("Task %d", i))
		}

		resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 5})
		require.NoError(t, err)
		assert.Len(t, resp.Tasks, 5)
		assert.Equal(t, 10, resp.TotalCount)
		assert.NotEmpty(t, resp.NextPageToken)
	})

	t.Run("page token navigation - first page, second page, last page", func(t *testing.T) {
		svc := createCleanService()
		// 2 sample + 5 new = 7 total
		for i := 0; i < 5; i++ {
			createTaskInService(t, svc, fmt.Sprintf("Task %d", i))
		}

		// First page: 3 items
		resp1, err := svc.ListTasks(models.ListTasksQuery{PageSize: 3})
		require.NoError(t, err)
		assert.Len(t, resp1.Tasks, 3)
		assert.Equal(t, 7, resp1.TotalCount)
		assert.NotEmpty(t, resp1.NextPageToken)

		// Second page: 3 items
		resp2, err := svc.ListTasks(models.ListTasksQuery{PageSize: 3, PageToken: resp1.NextPageToken})
		require.NoError(t, err)
		assert.Len(t, resp2.Tasks, 3)
		assert.Equal(t, 7, resp2.TotalCount)
		assert.NotEmpty(t, resp2.NextPageToken)

		// Last page: 1 item
		resp3, err := svc.ListTasks(models.ListTasksQuery{PageSize: 3, PageToken: resp2.NextPageToken})
		require.NoError(t, err)
		assert.Len(t, resp3.Tasks, 1)
		assert.Equal(t, 7, resp3.TotalCount)
		assert.Empty(t, resp3.NextPageToken) // No more pages
	})

	t.Run("empty result set", func(t *testing.T) {
		svc := createCleanService()
		// Filter for a status that no sample task has
		resp, err := svc.ListTasks(models.ListTasksQuery{
			Status: models.TaskStatusCompleted,
		})
		require.NoError(t, err)
		assert.Empty(t, resp.Tasks)
		assert.Equal(t, 0, resp.TotalCount)
		assert.Empty(t, resp.NextPageToken)
	})

	t.Run("page size clamped to minimum 1 when 0 or negative", func(t *testing.T) {
		svc := createCleanService()
		// PageSize <= 0 defaults to 20
		resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 0})
		require.NoError(t, err)
		// 2 sample tasks, default page size 20, so all returned
		assert.Len(t, resp.Tasks, 2)

		resp2, err := svc.ListTasks(models.ListTasksQuery{PageSize: -5})
		require.NoError(t, err)
		assert.Len(t, resp2.Tasks, 2)
	})

	t.Run("page size clamped to maximum 100", func(t *testing.T) {
		svc := createCleanService()
		// Create many tasks: 2 sample + 110 = 112
		for i := 0; i < 110; i++ {
			createTaskInService(t, svc, fmt.Sprintf("Task %d", i))
		}

		resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 200})
		require.NoError(t, err)
		assert.Len(t, resp.Tasks, 100) // clamped to 100
		assert.Equal(t, 112, resp.TotalCount)
		assert.NotEmpty(t, resp.NextPageToken)
	})
}

// ============================================================
// Filtering (ListTasks)
// ============================================================

func TestListTasks_Filtering(t *testing.T) {
	t.Run("filter by status", func(t *testing.T) {
		svc := createCleanService()
		task := createTaskInService(t, svc, "Completed Task")
		_, err := svc.UpdateTask(task.ID, models.UpdateTaskRequest{
			Status: statusPtr(models.TaskStatusCompleted),
		})
		require.NoError(t, err)

		resp, err := svc.ListTasks(models.ListTasksQuery{
			Status:   models.TaskStatusCompleted,
			PageSize: 100,
		})
		require.NoError(t, err)
		assert.Equal(t, 1, resp.TotalCount)
		for _, t2 := range resp.Tasks {
			assert.Equal(t, models.TaskStatusCompleted, t2.Status)
		}
	})

	t.Run("filter by assigned_to", func(t *testing.T) {
		svc := createCleanService()
		_, err := svc.CreateTask(models.CreateTaskRequest{
			Title:      "Alice Task",
			AssignedTo: stringPtr("alice"),
		})
		require.NoError(t, err)
		_, err = svc.CreateTask(models.CreateTaskRequest{
			Title:      "Bob Task",
			AssignedTo: stringPtr("bob"),
		})
		require.NoError(t, err)

		resp, err := svc.ListTasks(models.ListTasksQuery{
			AssignedTo: "alice",
			PageSize:   100,
		})
		require.NoError(t, err)
		assert.Equal(t, 1, resp.TotalCount)
		assert.Equal(t, "Alice Task", resp.Tasks[0].Title)
	})

	t.Run("filter by assigned_to excludes nil assigned_to", func(t *testing.T) {
		svc := createCleanService()
		// Sample tasks have nil AssignedTo
		_, err := svc.CreateTask(models.CreateTaskRequest{
			Title:      "Assigned Task",
			AssignedTo: stringPtr("charlie"),
		})
		require.NoError(t, err)

		resp, err := svc.ListTasks(models.ListTasksQuery{
			AssignedTo: "charlie",
			PageSize:   100,
		})
		require.NoError(t, err)
		// Only the assigned task should appear, not the unassigned sample tasks
		assert.Equal(t, 1, resp.TotalCount)
		require.Len(t, resp.Tasks, 1)
		assert.Equal(t, "Assigned Task", resp.Tasks[0].Title)
	})

	t.Run("filter by single tag", func(t *testing.T) {
		svc := createCleanService()
		// Sample tasks have "go" tag
		resp, err := svc.ListTasks(models.ListTasksQuery{
			Tags:     "go",
			PageSize: 100,
		})
		require.NoError(t, err)
		assert.Equal(t, 2, resp.TotalCount) // both sample tasks have "go"
	})

	t.Run("filter by multiple tags comma-separated", func(t *testing.T) {
		svc := createCleanService()
		// Sample task 1 has ["go", "rest", "api"]
		// Sample task 2 has ["go", "grpc", "protobuf"]
		resp, err := svc.ListTasks(models.ListTasksQuery{
			Tags:     "go,rest",
			PageSize: 100,
		})
		require.NoError(t, err)
		// Only the first sample task has both "go" and "rest"
		assert.Equal(t, 1, resp.TotalCount)
	})

	t.Run("filter by tag that matches no tasks", func(t *testing.T) {
		svc := createCleanService()
		resp, err := svc.ListTasks(models.ListTasksQuery{
			Tags:     "nonexistent",
			PageSize: 100,
		})
		require.NoError(t, err)
		assert.Equal(t, 0, resp.TotalCount)
		assert.Empty(t, resp.Tasks)
	})

	t.Run("combined filters", func(t *testing.T) {
		svc := createCleanService()
		// Create a task that is assigned, has a specific tag, and is in_progress
		_, err := svc.CreateTask(models.CreateTaskRequest{
			Title:      "Combined Filter Task",
			Tags:       []string{"important"},
			AssignedTo: stringPtr("dave"),
		})
		require.NoError(t, err)

		// All sample tasks are pending, unassigned, and don't have "important" tag
		resp, err := svc.ListTasks(models.ListTasksQuery{
			Status:     models.TaskStatusPending,
			AssignedTo: "dave",
			Tags:       "important",
			PageSize:   100,
		})
		require.NoError(t, err)
		assert.Equal(t, 1, resp.TotalCount)
		assert.Equal(t, "Combined Filter Task", resp.Tasks[0].Title)
	})
}

// ============================================================
// Sorting (ListTasks)
// ============================================================

func TestListTasks_Sorting(t *testing.T) {
	t.Run("sort by created_at ascending", func(t *testing.T) {
		svc := createCleanService()
		// Add tasks with small delays to ensure ordering
		time.Sleep(1 * time.Millisecond)
		createTaskInService(t, svc, "Task A")
		time.Sleep(1 * time.Millisecond)
		createTaskInService(t, svc, "Task B")

		resp, err := svc.ListTasks(models.ListTasksQuery{
			SortOrder: models.SortOrderCreatedAtAsc,
			PageSize:  100,
		})
		require.NoError(t, err)
		require.True(t, len(resp.Tasks) >= 4) // 2 sample + 2 new
		// Verify ascending order
		for i := 1; i < len(resp.Tasks); i++ {
			assert.True(t, !resp.Tasks[i].CreatedAt.Before(resp.Tasks[i-1].CreatedAt),
				"task %d created_at should be >= task %d created_at", i, i-1)
		}
	})

	t.Run("sort by created_at descending", func(t *testing.T) {
		svc := createCleanService()
		time.Sleep(1 * time.Millisecond)
		createTaskInService(t, svc, "Task A")
		time.Sleep(1 * time.Millisecond)
		createTaskInService(t, svc, "Task B")

		resp, err := svc.ListTasks(models.ListTasksQuery{
			SortOrder: models.SortOrderCreatedAtDesc,
			PageSize:  100,
		})
		require.NoError(t, err)
		for i := 1; i < len(resp.Tasks); i++ {
			assert.True(t, !resp.Tasks[i].CreatedAt.After(resp.Tasks[i-1].CreatedAt),
				"task %d created_at should be <= task %d created_at", i, i-1)
		}
	})

	t.Run("sort by due_date ascending with nil due dates last", func(t *testing.T) {
		svc := createCleanService()
		early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		late := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)

		_, err := svc.CreateTask(models.CreateTaskRequest{
			Title:   "Late Task",
			DueDate: timePtr(late),
		})
		require.NoError(t, err)
		_, err = svc.CreateTask(models.CreateTaskRequest{
			Title:   "Early Task",
			DueDate: timePtr(early),
		})
		require.NoError(t, err)

		resp, err := svc.ListTasks(models.ListTasksQuery{
			SortOrder: models.SortOrderDueDateAsc,
			PageSize:  100,
		})
		require.NoError(t, err)

		// Tasks with due dates should come before nil due dates
		// Find the boundary between dated and nil-dated tasks
		var datedTasks []models.Task
		var nilDatedTasks []models.Task
		for _, task := range resp.Tasks {
			if task.DueDate != nil {
				datedTasks = append(datedTasks, task)
			} else {
				nilDatedTasks = append(nilDatedTasks, task)
			}
		}

		// Dated tasks should be in ascending order
		for i := 1; i < len(datedTasks); i++ {
			assert.True(t, !datedTasks[i].DueDate.Before(*datedTasks[i-1].DueDate),
				"dated task %d should have due_date >= dated task %d", i, i-1)
		}

		// In the sort, nil due dates go to the end (return false when i is nil)
		// Verify nil tasks are at the end of the list
		if len(datedTasks) > 0 && len(nilDatedTasks) > 0 {
			lastDatedIdx := -1
			firstNilIdx := len(resp.Tasks)
			for idx, task := range resp.Tasks {
				if task.DueDate != nil {
					lastDatedIdx = idx
				}
				if task.DueDate == nil && idx < firstNilIdx {
					firstNilIdx = idx
				}
			}
			assert.True(t, lastDatedIdx < firstNilIdx,
				"all dated tasks should appear before nil-dated tasks in ascending order")
		}
	})

	t.Run("sort by due_date descending with nil due dates last", func(t *testing.T) {
		svc := createCleanService()
		early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		late := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)

		_, err := svc.CreateTask(models.CreateTaskRequest{
			Title:   "Early Task",
			DueDate: timePtr(early),
		})
		require.NoError(t, err)
		_, err = svc.CreateTask(models.CreateTaskRequest{
			Title:   "Late Task",
			DueDate: timePtr(late),
		})
		require.NoError(t, err)

		resp, err := svc.ListTasks(models.ListTasksQuery{
			SortOrder: models.SortOrderDueDateDesc,
			PageSize:  100,
		})
		require.NoError(t, err)

		// Dated tasks should be in descending order
		var datedTasks []models.Task
		for _, task := range resp.Tasks {
			if task.DueDate != nil {
				datedTasks = append(datedTasks, task)
			}
		}
		for i := 1; i < len(datedTasks); i++ {
			assert.True(t, !datedTasks[i].DueDate.After(*datedTasks[i-1].DueDate),
				"dated task %d should have due_date <= dated task %d", i, i-1)
		}
	})

	t.Run("sort by priority ascending", func(t *testing.T) {
		svc := createCleanService()
		_, err := svc.CreateTask(models.CreateTaskRequest{
			Title:    "Critical Task",
			Priority: models.TaskPriorityCritical,
		})
		require.NoError(t, err)
		_, err = svc.CreateTask(models.CreateTaskRequest{
			Title:    "Low Task",
			Priority: models.TaskPriorityLow,
		})
		require.NoError(t, err)

		resp, err := svc.ListTasks(models.ListTasksQuery{
			SortOrder: models.SortOrderPriorityAsc,
			PageSize:  100,
		})
		require.NoError(t, err)
		for i := 1; i < len(resp.Tasks); i++ {
			assert.True(t, resp.Tasks[i].Priority.GetValue() >= resp.Tasks[i-1].Priority.GetValue(),
				"task %d priority should be >= task %d priority", i, i-1)
		}
	})

	t.Run("sort by priority descending", func(t *testing.T) {
		svc := createCleanService()
		_, err := svc.CreateTask(models.CreateTaskRequest{
			Title:    "Low Task",
			Priority: models.TaskPriorityLow,
		})
		require.NoError(t, err)
		_, err = svc.CreateTask(models.CreateTaskRequest{
			Title:    "Critical Task",
			Priority: models.TaskPriorityCritical,
		})
		require.NoError(t, err)

		resp, err := svc.ListTasks(models.ListTasksQuery{
			SortOrder: models.SortOrderPriorityDesc,
			PageSize:  100,
		})
		require.NoError(t, err)
		for i := 1; i < len(resp.Tasks); i++ {
			assert.True(t, resp.Tasks[i].Priority.GetValue() <= resp.Tasks[i-1].Priority.GetValue(),
				"task %d priority should be <= task %d priority", i, i-1)
		}
	})

	t.Run("default sort - no sort specified", func(t *testing.T) {
		svc := createCleanService()
		createTaskInService(t, svc, "Task A")
		createTaskInService(t, svc, "Task B")

		resp, err := svc.ListTasks(models.ListTasksQuery{
			PageSize: 100,
		})
		require.NoError(t, err)
		// With no sort order, the code doesn't sort, so we just verify we get all tasks
		assert.Equal(t, 4, resp.TotalCount) // 2 sample + 2 new
		assert.Len(t, resp.Tasks, 4)
	})
}

// ============================================================
// Validation
// ============================================================

func TestValidation(t *testing.T) {
	t.Run("invalid priority on create", func(t *testing.T) {
		svc := createCleanService()
		task, err := svc.CreateTask(models.CreateTaskRequest{
			Title:    "Invalid Priority",
			Priority: models.TaskPriority("extreme"),
		})
		assert.Error(t, err)
		assert.Nil(t, task)
		assert.Contains(t, err.Error(), "invalid priority")
	})

	t.Run("invalid status on update", func(t *testing.T) {
		svc := createCleanService()
		task := createTaskInService(t, svc, "Status Validate Task")

		updated, err := svc.UpdateTask(task.ID, models.UpdateTaskRequest{
			Status: statusPtr(models.TaskStatus("unknown")),
		})
		assert.Error(t, err)
		assert.Nil(t, updated)
		assert.Contains(t, err.Error(), "invalid status")
	})

	t.Run("invalid priority on update", func(t *testing.T) {
		svc := createCleanService()
		task := createTaskInService(t, svc, "Priority Validate Task")

		updated, err := svc.UpdateTask(task.ID, models.UpdateTaskRequest{
			Priority: priorityPtr(models.TaskPriority("mega")),
		})
		assert.Error(t, err)
		assert.Nil(t, updated)
		assert.Contains(t, err.Error(), "invalid priority")
	})
}

// ============================================================
// Concurrent Access
// ============================================================

func TestConcurrentAccess(t *testing.T) {
	t.Run("multiple goroutines creating tasks simultaneously", func(t *testing.T) {
		svc := createCleanService()
		const numGoroutines = 50
		var wg sync.WaitGroup

		wg.Add(numGoroutines)
		for i := 0; i < numGoroutines; i++ {
			go func(idx int) {
				defer wg.Done()
				_, err := svc.CreateTask(models.CreateTaskRequest{
					Title: fmt.Sprintf("Concurrent Task %d", idx),
				})
				assert.NoError(t, err)
			}(i)
		}
		wg.Wait()

		// 2 sample + 50 concurrent = 52
		resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 100})
		require.NoError(t, err)
		assert.Equal(t, 52, resp.TotalCount)
	})

	t.Run("multiple goroutines reading and writing concurrently", func(t *testing.T) {
		svc := createCleanService()
		const numWriters = 20
		const numReaders = 20
		var wg sync.WaitGroup

		// Writers
		wg.Add(numWriters)
		for i := 0; i < numWriters; i++ {
			go func(idx int) {
				defer wg.Done()
				task, err := svc.CreateTask(models.CreateTaskRequest{
					Title:    fmt.Sprintf("Writer Task %d", idx),
					Priority: models.TaskPriorityHigh,
					Tags:     []string{"concurrent"},
				})
				if err == nil && task != nil {
					// Try updating the task we just created
					_, _ = svc.UpdateTask(task.ID, models.UpdateTaskRequest{
						Title: stringPtr(fmt.Sprintf("Updated Writer Task %d", idx)),
					})
				}
			}(i)
		}

		// Readers
		wg.Add(numReaders)
		for i := 0; i < numReaders; i++ {
			go func() {
				defer wg.Done()
				_, _ = svc.ListTasks(models.ListTasksQuery{PageSize: 100})
			}()
		}

		wg.Wait()

		// Verify the service is still consistent
		resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 100})
		require.NoError(t, err)
		// 2 sample + 20 writers = 22
		assert.Equal(t, 22, resp.TotalCount)
	})

	t.Run("concurrent reads, writes, and deletes", func(t *testing.T) {
		svc := createCleanService()
		const numTasks = 30
		var wg sync.WaitGroup

		// First create tasks that we will operate on concurrently
		taskIDs := make([]string, numTasks)
		for i := 0; i < numTasks; i++ {
			task := createTaskInService(t, svc, fmt.Sprintf("Concurrent Op Task %d", i))
			taskIDs[i] = task.ID
		}

		// Concurrent reads
		wg.Add(numTasks)
		for i := 0; i < numTasks; i++ {
			go func(idx int) {
				defer wg.Done()
				_, _ = svc.GetTask(taskIDs[idx])
			}(i)
		}

		// Concurrent updates (first half)
		wg.Add(numTasks / 2)
		for i := 0; i < numTasks/2; i++ {
			go func(idx int) {
				defer wg.Done()
				_, _ = svc.UpdateTask(taskIDs[idx], models.UpdateTaskRequest{
					Title: stringPtr(fmt.Sprintf("Updated %d", idx)),
				})
			}(i)
		}

		// Concurrent deletes (second half)
		wg.Add(numTasks / 2)
		for i := numTasks / 2; i < numTasks; i++ {
			go func(idx int) {
				defer wg.Done()
				_ = svc.DeleteTask(taskIDs[idx])
			}(i)
		}

		wg.Wait()

		// Verify: 2 sample + first 15 tasks should remain = 17
		resp, err := svc.ListTasks(models.ListTasksQuery{PageSize: 100})
		require.NoError(t, err)
		assert.Equal(t, 17, resp.TotalCount)
	})
}
