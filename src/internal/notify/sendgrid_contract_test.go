package notify

import (
	"context"
	"os"
	"testing"
)

// TestSendGridContract checks the real SendGrid API accepts our payload. It runs in
// SendGrid's sandbox mode, so no email is delivered. Required before merging (QA-27).
func TestSendGridContract(t *testing.T) {
	if os.Getenv("SENDGRID_API_KEY") == "" {
		t.Fatal("SENDGRID_API_KEY is required for the SendGrid contract test (QA-27). Ask the platform team for the sandbox key; do not skip this test.")
	}
	n := FromEnv()
	n.sandbox = true
	if err := n.TaskAssigned(context.Background(), "contract-test@gaialabs.ai", "Contract test"); err != nil {
		t.Fatalf("SendGrid rejected the notification: %v", err)
	}
}
