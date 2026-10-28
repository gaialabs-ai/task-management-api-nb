// Package testutil holds test helpers shared by the service and HTTP tests.
// It must not import the services package, whose internal tests import it.
package testutil

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Now is the frozen clock value used by the tests
var Now = time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

// NotifyCall records a single NotifyAssignment invocation
type NotifyCall struct {
	Task     models.Task
	Assignee string
}

// RecordingNotifier is a fake Notifier that records every call and returns Err
type RecordingNotifier struct {
	mu    sync.Mutex
	calls []NotifyCall
	Err   error
}

// NotifyAssignment records the call and returns the configured error
func (n *RecordingNotifier) NotifyAssignment(task models.Task, assignee string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, NotifyCall{Task: task, Assignee: assignee})
	return n.Err
}

// Recorded returns a snapshot of the recorded calls
func (n *RecordingNotifier) Recorded() []NotifyCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]NotifyCall(nil), n.calls...)
}

// TaskStore is the subset of the task service the helpers need
type TaskStore interface {
	CreateTask(req models.CreateTaskRequest) (*models.Task, error)
	UpdateTask(id string, req models.UpdateTaskRequest) (*models.Task, error)
}

// PtrTime returns a pointer to the given time
func PtrTime(t time.Time) *time.Time {
	return &t
}

// MustCreateTask creates a task with the given status and due date and returns its ID
func MustCreateTask(t *testing.T, s TaskStore, title string, status models.TaskStatus, due *time.Time) string {
	t.Helper()
	task, err := s.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatalf("CreateTask(%q) returned error: %v", title, err)
	}
	if status != "" && status != models.TaskStatusPending {
		if _, err := s.UpdateTask(task.ID, models.UpdateTaskRequest{Status: &status}); err != nil {
			t.Fatalf("UpdateTask(%q) status %q returned error: %v", task.ID, status, err)
		}
	}
	return task.ID
}

// SeedOverdueTasks creates n pending tasks that are overdue relative to now,
// with strictly increasing due dates, and returns their IDs in due date order
func SeedOverdueTasks(t *testing.T, s TaskStore, now time.Time, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		due := now.Add(-time.Duration(n-i) * time.Hour)
		ids = append(ids, MustCreateTask(t, s, fmt.Sprintf("overdue %d", i), models.TaskStatusPending, PtrTime(due)))
	}
	return ids
}

// TaskIDs returns the IDs of the given tasks in order
func TaskIDs(tasks []models.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

// EqualStrings reports whether two string slices have the same elements in order
func EqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
