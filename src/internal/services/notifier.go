package services

import (
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Notifier is told about task events that users may want to be alerted about.
type Notifier interface {
	// NotifyAssigned is called after a task has been assigned to assignee.
	NotifyAssigned(task models.Task, assignee string) error
}

// LogNotifier is the default Notifier; it only writes a log line.
type LogNotifier struct{}

// NotifyAssigned logs the assignment.
func (LogNotifier) NotifyAssigned(task models.Task, assignee string) error {
	log.Printf("notifier: task %s (%q) assigned to %s", task.ID, task.Title, assignee)
	return nil
}
