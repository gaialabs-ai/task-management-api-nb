package notifications

import (
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// assignmentFormat is the log line format for assignment notifications
const assignmentFormat = "assignment notification: task %s %q assigned to %s"

// Notifier sends notifications about task events
type Notifier interface {
	// NotifyAssignment notifies that a task has been assigned to the given assignee
	NotifyAssignment(task models.Task, assignee string) error
}

// LogNotifier is the default Notifier that writes assignment notifications to a log
type LogNotifier struct {
	// Logger is the destination for notifications; nil uses the standard log package
	Logger *log.Logger
}

// NewLogNotifier creates a new log-based notifier using the standard log package
func NewLogNotifier() *LogNotifier {
	return &LogNotifier{}
}

// NotifyAssignment logs the assignment of a task
func (n *LogNotifier) NotifyAssignment(task models.Task, assignee string) error {
	if n != nil && n.Logger != nil {
		n.Logger.Printf(assignmentFormat, task.ID, task.Title, assignee)
		return nil
	}
	log.Printf(assignmentFormat, task.ID, task.Title, assignee)
	return nil
}
