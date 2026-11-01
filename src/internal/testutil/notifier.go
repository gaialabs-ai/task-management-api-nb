package testutil

import "github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"

// FakeNotifier records NotifyAssigned calls as "taskID|assignee" and returns Err.
type FakeNotifier struct {
	Calls []string
	Err   error
}

func (f *FakeNotifier) NotifyAssigned(task models.Task, assignee string) error {
	f.Calls = append(f.Calls, task.ID+"|"+assignee)
	return f.Err
}
