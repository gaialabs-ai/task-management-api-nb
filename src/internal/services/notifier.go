package services

import (
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Notifier sends notifications about task events.
type Notifier interface {
	// NotifyAssignment is called after a task has been assigned to a user.
	NotifyAssignment(task models.Task, assignedTo string) error
}

// LogNotifier is the default Notifier; it only writes to the standard logger.
type LogNotifier struct{}

// NotifyAssignment logs the assignment.
func (LogNotifier) NotifyAssignment(task models.Task, assignedTo string) error {
	log.Printf("notification: task %s (%q) assigned to %s", task.ID, task.Title, assignedTo)
	return nil
}
