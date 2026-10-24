package services

import (
	"fmt"
	"log"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// SetClock overrides the clock used for overdue checks and update timestamps.
// A nil clock restores time.Now.
func (s *TaskService) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clock == nil {
		clock = time.Now
	}
	s.clock = clock
}

// SetNotifier overrides the assignment notifier. A nil notifier restores the logging default.
func (s *TaskService) SetNotifier(n Notifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n == nil {
		n = LogNotifier{}
	}
	s.notifier = n
}

// ListOverdueTasks returns tasks whose due date is in the past and whose status is
// neither completed nor cancelled, sorted by due date ascending (ties broken by ID).
// Pagination uses the same page_size/page_token logic as ListTasks.
func (s *TaskService) ListOverdueTasks(query models.ListOverdueTasksQuery) (*models.ListTasksResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.clock()
	var overdue []*models.Task
	for _, task := range s.tasks {
		if task.DueDate == nil || !task.DueDate.Before(now) {
			continue
		}
		if task.Status == models.TaskStatusCompleted || task.Status == models.TaskStatusCancelled {
			continue
		}
		overdue = append(overdue, task)
	}

	sort.Slice(overdue, func(i, j int) bool {
		if !overdue[i].DueDate.Equal(*overdue[j].DueDate) {
			return overdue[i].DueDate.Before(*overdue[j].DueDate)
		}
		return overdue[i].ID < overdue[j].ID
	})

	page, next := paginateTasks(overdue, query.PageSize, query.PageToken)
	return &models.ListTasksResponse{
		Tasks:         page,
		NextPageToken: next,
		TotalCount:    len(overdue),
	}, nil
}

// validateEmail checks that value is a bare email address (no display name).
func validateEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	addr, err := mail.ParseAddress(value)
	if err != nil || addr.Address != value {
		return "", fmt.Errorf("%w: %q", ErrInvalidEmail, value)
	}
	return value, nil
}

// AssignTask assigns a task to the given email address and notifies the assignee.
// A notification failure is logged and does not fail the assignment.
func (s *TaskService) AssignTask(id, email string) (*models.Task, error) {
	email, err := validateEmail(email)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	task, exists := s.tasks[id]
	if !exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: task with ID %s not found", ErrTaskNotFound, id)
	}
	assignee := email
	task.AssignedTo = &assignee
	task.UpdatedAt = s.clock()
	snapshot := *task
	notifier := s.notifier
	s.mu.Unlock()

	if err := notifier.NotifyAssigned(snapshot, email); err != nil {
		log.Printf("failed to send assignment notification for task %s: %v", snapshot.ID, err)
	}
	return &snapshot, nil
}
