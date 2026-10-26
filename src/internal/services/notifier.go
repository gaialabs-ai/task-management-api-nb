package services

import (
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Notifier delivers notifications about task events. It is the extension point
// for real delivery (email, webhook, ...). Implementations must be safe for
// concurrent use.
type Notifier interface {
	// NotifyAssignment tells assignedTo that the task was assigned to them.
	// The task is a snapshot taken after the assignment.
	NotifyAssignment(task models.Task, assignedTo string) error
}

// LogNotifier is the default Notifier; it only writes a log line.
type LogNotifier struct{}

// NotifyAssignment logs the assignment.
func (LogNotifier) NotifyAssignment(task models.Task, assignedTo string) error {
	log.Printf("notification: task %q (%s) assigned to %s", task.Title, task.ID, assignedTo)
	return nil
}
