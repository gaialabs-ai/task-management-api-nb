// Package notifications provides the assignment notification extension point
// used by the task service.
package notifications

import (
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Notifier sends a notification when a task is assigned to a user.
type Notifier interface {
	NotifyAssignment(task models.Task, assignee string) error
}

// LogNotifier is the default Notifier. It writes assignment notifications to
// a logger instead of delivering them to an external system.
type LogNotifier struct {
	// Logger is the destination for notifications. When nil, the standard
	// log package is used.
	Logger *log.Logger
}

// NewLogNotifier creates a LogNotifier that writes through the standard log package.
func NewLogNotifier() *LogNotifier {
	return &LogNotifier{}
}

// NotifyAssignment logs the assignment and always returns nil.
func (n *LogNotifier) NotifyAssignment(task models.Task, assignee string) error {
	const format = "assignment notification: task %s %q assigned to %s"
	if n != nil && n.Logger != nil {
		n.Logger.Printf(format, task.ID, task.Title, assignee)
		return nil
	}
	log.Printf(format, task.ID, task.Title, assignee)
	return nil
}
