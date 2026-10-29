package services

import (
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Notifier is told when a task is assigned to someone.
type Notifier interface {
	NotifyAssigned(task models.Task, assignee string) error
}

// LogNotifier is the default Notifier; it only logs the assignment.
type LogNotifier struct{}

// NotifyAssigned logs the assignment.
func (LogNotifier) NotifyAssigned(task models.Task, assignee string) error {
	log.Printf("task %q (%s) assigned to %s", task.Title, task.ID, assignee)
	return nil
}
