package notifications

import (
	"bytes"
	"log"
	"testing"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Compile-time checks that LogNotifier satisfies Notifier.
var (
	_ Notifier = (*LogNotifier)(nil)
	_ Notifier = NewLogNotifier()
)

var notifyCases = []struct {
	name     string
	task     models.Task
	assignee string
	want     string
}{
	{
		name:     "simple title",
		task:     models.Task{ID: "task-123", Title: "Write report"},
		assignee: "alice@example.com",
		want:     `assignment notification: task task-123 "Write report" assigned to alice@example.com`,
	},
	{
		name:     "title with quotes is escaped",
		task:     models.Task{ID: "abc", Title: `Fix "login" bug`},
		assignee: "bob@example.org",
		want:     `assignment notification: task abc "Fix \"login\" bug" assigned to bob@example.org`,
	},
	{
		name:     "empty title",
		task:     models.Task{ID: "empty-title", Title: ""},
		assignee: "carol@example.com",
		want:     `assignment notification: task empty-title "" assigned to carol@example.com`,
	},
}

func TestLogNotifierWritesToLogger(t *testing.T) {
	for _, tc := range notifyCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			n := &LogNotifier{Logger: log.New(&buf, "", 0)}

			if err := n.NotifyAssignment(tc.task, tc.assignee); err != nil {
				t.Fatalf("NotifyAssignment returned error: %v", err)
			}
			if got := buf.String(); got != tc.want+"\n" {
				t.Errorf("logged %q, want %q", got, tc.want+"\n")
			}
		})
	}
}

func TestLogNotifierNilLoggerUsesStandardLog(t *testing.T) {
	var buf bytes.Buffer
	prevOut, prevFlags, prevPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	})

	notifiers := []struct {
		name string
		n    *LogNotifier
	}{
		{name: "NewLogNotifier", n: NewLogNotifier()},
		{name: "zero value", n: &LogNotifier{}},
		{name: "nil receiver", n: nil},
	}

	for _, nc := range notifiers {
		for _, tc := range notifyCases {
			t.Run(nc.name+"/"+tc.name, func(t *testing.T) {
				buf.Reset()
				if err := nc.n.NotifyAssignment(tc.task, tc.assignee); err != nil {
					t.Fatalf("NotifyAssignment returned error: %v", err)
				}
				if got := buf.String(); got != tc.want+"\n" {
					t.Errorf("logged %q, want %q", got, tc.want+"\n")
				}
			})
		}
	}
}
