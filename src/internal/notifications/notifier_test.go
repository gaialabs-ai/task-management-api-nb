package notifications

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// LogNotifier must satisfy the Notifier interface
var _ Notifier = (*LogNotifier)(nil)

func TestNewLogNotifier(t *testing.T) {
	n := NewLogNotifier()
	if n == nil {
		t.Fatal("NewLogNotifier returned nil")
	}
	if n.Logger != nil {
		t.Errorf("NewLogNotifier Logger = %v, want nil (standard log package)", n.Logger)
	}
}

func TestLogNotifier_CustomLogger(t *testing.T) {
	tests := []struct {
		name     string
		task     models.Task
		assignee string
		want     string
	}{
		{
			name:     "simple title",
			task:     models.Task{ID: "task-1", Title: "Write docs"},
			assignee: "alice@example.com",
			want:     `assignment notification: task task-1 "Write docs" assigned to alice@example.com`,
		},
		{
			name:     "title with quotes is escaped",
			task:     models.Task{ID: "task-2", Title: `Say "hi"`},
			assignee: "bob@example.com",
			want:     `assignment notification: task task-2 "Say \"hi\"" assigned to bob@example.com`,
		},
		{
			name:     "empty title",
			task:     models.Task{ID: "task-3"},
			assignee: "carol@example.com",
			want:     `assignment notification: task task-3 "" assigned to carol@example.com`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			n := &LogNotifier{Logger: log.New(&buf, "", 0)}

			if err := n.NotifyAssignment(tt.task, tt.assignee); err != nil {
				t.Fatalf("NotifyAssignment returned error: %v", err)
			}
			if got := buf.String(); !strings.Contains(got, tt.want) {
				t.Errorf("log output = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

func TestLogNotifier_DefaultLogger(t *testing.T) {
	tests := []struct {
		name     string
		notifier *LogNotifier
	}{
		{name: "NewLogNotifier", notifier: NewLogNotifier()},
		{name: "zero value", notifier: &LogNotifier{}},
		{name: "nil receiver", notifier: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			prevWriter := log.Writer()
			prevFlags := log.Flags()
			prevPrefix := log.Prefix()
			log.SetOutput(&buf)
			log.SetFlags(0)
			log.SetPrefix("")
			defer func() {
				log.SetOutput(prevWriter)
				log.SetFlags(prevFlags)
				log.SetPrefix(prevPrefix)
			}()

			task := models.Task{ID: "task-42", Title: "Ship it"}
			if err := tt.notifier.NotifyAssignment(task, "dave@example.com"); err != nil {
				t.Fatalf("NotifyAssignment returned error: %v", err)
			}

			want := `assignment notification: task task-42 "Ship it" assigned to dave@example.com`
			if got := buf.String(); !strings.Contains(got, want) {
				t.Errorf("log output = %q, want it to contain %q", got, want)
			}
		})
	}
}
