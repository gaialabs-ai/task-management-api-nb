// Package notify emails assignees through SendGrid.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

const sendGridURL = "https://api.sendgrid.com/v3/mail/send"

// Notifier sends task notifications through the SendGrid mail API.
type Notifier struct {
	apiKey  string
	from    string
	sandbox bool
	client  *http.Client
}

// FromEnv builds a Notifier from SENDGRID_API_KEY and NOTIFY_FROM.
func FromEnv() *Notifier {
	from := os.Getenv("NOTIFY_FROM")
	if from == "" {
		from = "tasks@gaialabs.ai"
	}
	return &Notifier{
		apiKey: os.Getenv("SENDGRID_API_KEY"),
		from:   from,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Enabled reports whether an API key is configured.
func (n *Notifier) Enabled() bool {
	return n.apiKey != ""
}

// TaskAssigned emails the assignee that a task was assigned to them.
func (n *Notifier) TaskAssigned(ctx context.Context, assignee, title string) error {
	payload := map[string]any{
		"personalizations": []map[string]any{{"to": []map[string]string{{"email": assignee}}}},
		"from":             map[string]string{"email": n.from},
		"subject":          "Task assigned: " + title,
		"content":          []map[string]string{{"type": "text/plain", "value": fmt.Sprintf("You were assigned the task %q.", title)}},
		"mail_settings":    map[string]any{"sandbox_mode": map[string]bool{"enable": n.sandbox}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendGridURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+n.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("sendgrid returned %s", resp.Status)
	}
	return nil
}
