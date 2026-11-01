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

// ErrTaskNotFound is wrapped by errors returned when a task ID is unknown.
var ErrTaskNotFound = fmt.Errorf("task not found")

// ErrInvalidEmail is wrapped by errors returned for a malformed assignee email.
var ErrInvalidEmail = fmt.Errorf("invalid email")

// Notifier is told when a task is assigned.
type Notifier interface {
	NotifyAssigned(task models.Task, assignee string) error
}

// LogNotifier is the default Notifier; it only logs the assignment.
type LogNotifier struct{}

// NotifyAssigned logs the assignment.
func (LogNotifier) NotifyAssigned(task models.Task, assignee string) error {
	log.Printf("task %s (%q) assigned to %s", task.ID, task.Title, assignee)
	return nil
}

// SetClock overrides the clock used to decide whether a task is overdue.
func (s *TaskService) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now == nil {
		now = time.Now
	}
	s.now = now
}

// SetNotifier overrides the Notifier used on assignment.
func (s *TaskService) SetNotifier(n Notifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n == nil {
		n = LogNotifier{}
	}
	s.notifier = n
}

// ListOverdueTasks returns tasks whose due date is in the past and whose status
// is neither completed nor cancelled, sorted by due date ascending (ties broken
// by task ID), paginated with the same logic as ListTasks.
func (s *TaskService) ListOverdueTasks(pageSize int, pageToken string) (*models.ListTasksResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.now()
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

	page, next := paginate(overdue, pageSize, pageToken)
	return &models.ListTasksResponse{
		Tasks:         page,
		NextPageToken: next,
		TotalCount:    len(overdue),
	}, nil
}

// AssignTask assigns a task to the given email address and notifies the
// assignee. A notification failure is logged and does not fail the call.
func (s *TaskService) AssignTask(id, email string) (*models.Task, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil || addr.Name != "" || addr.Address != strings.TrimSpace(email) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidEmail, email)
	}

	s.mu.Lock()
	task, exists := s.tasks[id]
	if !exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("task with ID %s not found: %w", id, ErrTaskNotFound)
	}
	assignee := addr.Address
	task.AssignedTo = &assignee
	task.UpdatedAt = s.now()
	snapshot := *task
	notifier := s.notifier
	s.mu.Unlock()

	if err := notifier.NotifyAssigned(snapshot, assignee); err != nil {
		log.Printf("failed to notify %s of assignment of task %s: %v", assignee, id, err)
	}
	return &snapshot, nil
}
