package services

import (
	"fmt"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

var baseTime = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

// newEmptyService returns a service with the sample data removed.
func newEmptyService() *TaskService {
	s := NewTaskService()
	s.mu.Lock()
	s.tasks = make(map[string]*models.Task)
	s.mu.Unlock()
	return s
}

// seedTask inserts a task with explicit fields (bypassing CreateTask so that
// timestamps are deterministic).
func seedTask(s *TaskService, id string, status models.TaskStatus, due *time.Time, createdOffset int) *models.Task {
	t := &models.Task{
		ID:        id,
		Title:     "task " + id,
		Status:    status,
		Priority:  models.TaskPriorityMedium,
		Tags:      []string{},
		CreatedAt: baseTime.Add(time.Duration(createdOffset) * time.Minute),
		UpdatedAt: baseTime,
		DueDate:   due,
	}
	s.mu.Lock()
	s.tasks[id] = t
	s.mu.Unlock()
	return t
}

// seedN seeds n tasks with ids t000..t(n-1), created in id order.
func seedN(s *TaskService, n int) {
	for i := 0; i < n; i++ {
		seedTask(s, fmt.Sprintf("t%03d", i), models.TaskStatusPending, nil, i)
	}
}

func timePtr(t time.Time) *time.Time { return &t }
