package notifications

import (
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Notifier delivers notifications about task events
type Notifier interface {
	// NotifyAssignment is called after a task has been assigned to assignee
	NotifyAssignment(task models.Task, assignee string) error
}

// LogNotifier is the default Notifier; it writes notifications to the standard logger
type LogNotifier struct{}

// NewLogNotifier creates a new log-based notifier
func NewLogNotifier() *LogNotifier {
	return &LogNotifier{}
}

// NotifyAssignment logs the assignment notification
func (n *LogNotifier) NotifyAssignment(task models.Task, assignee string) error {
	log.Printf("assignment notification: task %s %q assigned to %s", task.ID, task.Title, assignee)
	return nil
}
