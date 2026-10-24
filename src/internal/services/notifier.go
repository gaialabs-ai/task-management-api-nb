package services

import (
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Notifier delivers a notification when a task is assigned to someone.
type Notifier interface {
	// NotifyAssigned is called after task has been assigned to assignee (an email address).
	NotifyAssigned(task models.Task, assignee string) error
}

// LogNotifier is the default Notifier; it only writes a log line.
type LogNotifier struct{}

// NotifyAssigned logs the assignment.
func (LogNotifier) NotifyAssigned(task models.Task, assignee string) error {
	log.Printf("notification: task %s (%q) assigned to %s", task.ID, task.Title, assignee)
	return nil
}
