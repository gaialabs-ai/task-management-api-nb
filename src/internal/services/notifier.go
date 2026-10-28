package services

import (
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Notifier delivers notifications about task events.
type Notifier interface {
	// NotifyAssigned is called after a task has been assigned to assignee.
	NotifyAssigned(task models.Task, assignee string) error
}

// LogNotifier is the default Notifier; it only writes to the standard logger.
type LogNotifier struct{}

// NotifyAssigned logs the assignment.
func (LogNotifier) NotifyAssigned(task models.Task, assignee string) error {
	log.Printf("notifier: task %s assigned to %s", task.ID, assignee)
	return nil
}
