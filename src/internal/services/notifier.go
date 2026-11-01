package services

import (
	"errors"
	"fmt"
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// ErrInvalidEmail is returned (wrapped) when an assignee is not a valid bare
// email address.
var ErrInvalidEmail = errors.New("invalid email address")

// TaskNotFoundError is returned when a task ID does not exist.
type TaskNotFoundError struct {
	ID string
}

func (e *TaskNotFoundError) Error() string {
	return fmt.Sprintf("task with ID %s not found", e.ID)
}

// Notifier is told when a task has been assigned.
type Notifier interface {
	Notify(task models.Task, assignee string) error
}

// LogNotifier is the minimal default Notifier: it only writes a log line.
type LogNotifier struct{}

// Notify logs the assignment.
func (LogNotifier) Notify(task models.Task, assignee string) error {
	log.Printf("notification: task %q (%s) assigned to %s", task.Title, task.ID, assignee)
	return nil
}
