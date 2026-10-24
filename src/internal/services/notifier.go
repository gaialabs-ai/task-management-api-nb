package services

import (
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Notifier delivers notifications about task events.
type Notifier interface {
	// NotifyTaskAssigned is called after a task has been assigned to a user.
	// The task is a snapshot of the task after the assignment.
	NotifyTaskAssigned(task models.Task, assignedTo string) error
}

// LoggingNotifier is the default Notifier; it only writes a log line.
type LoggingNotifier struct{}

// NotifyTaskAssigned logs the assignment.
func (LoggingNotifier) NotifyTaskAssigned(task models.Task, assignedTo string) error {
	log.Printf("notification: task %s (%q) assigned to %s", task.ID, task.Title, assignedTo)
	return nil
}
