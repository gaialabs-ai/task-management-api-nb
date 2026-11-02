package services

import (
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Notifier delivers notifications about task events.
type Notifier interface {
	// NotifyAssignment informs assignee (an email address) that task was
	// assigned to them.
	NotifyAssignment(task models.Task, assignee string) error
}

// LogNotifier is the default Notifier; it only writes a log line.
type LogNotifier struct{}

// NotifyAssignment logs the assignment.
func (LogNotifier) NotifyAssignment(task models.Task, assignee string) error {
	log.Printf("notification: task %s (%q) assigned to %s", task.ID, task.Title, assignee)
	return nil
}
