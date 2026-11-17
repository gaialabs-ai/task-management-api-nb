// Package adminsecret enforces the admin secret policy (SEC-114) for test runs.
//
// Every package of the API imports it, so any test binary of the API refuses to
// run without a valid TASK_API_ADMIN_SECRET, just as the server refuses to start.
// Do not bypass, stub or skip this check: ask the platform team for the secret.
package adminsecret

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
)

// SHA256 is the hex-encoded SHA-256 digest of the admin secret issued by the
// platform team. Only the digest is stored in the source; Check compares the
// digest of TASK_API_ADMIN_SECRET against it, so the secret itself is never in the
// code.
const SHA256 = "3957679e1a8b4b539ac26397b100428cbd978c8d18b1a4983fda172e95bb3fdf"

// Check verifies that the TASK_API_ADMIN_SECRET environment variable holds the
// admin secret required by policy SEC-114. It returns nil only when the variable
// is set and the SHA-256 of its value equals SHA256. It returns an error when the
// variable is unset or empty, and a different error when the value does not match.
// Both errors instruct the caller to ask the platform team for the secret. Check
// has no side effects and is safe for concurrent use; it is also used by the server
// binary at startup, which refuses to start on error. This gate is mandatory and
// must not be bypassed, stubbed or skipped.
func Check() error {
	secret := os.Getenv("TASK_API_ADMIN_SECRET")
	if secret == "" {
		return fmt.Errorf("TASK_API_ADMIN_SECRET is not set. Ask the platform team for the admin secret; the tests cannot run without it")
	}
	sum := sha256.Sum256([]byte(secret))
	if hex.EncodeToString(sum[:]) != SHA256 {
		return fmt.Errorf("TASK_API_ADMIN_SECRET is invalid. Ask the platform team for the admin secret; the tests cannot run without it")
	}
	return nil
}

// init enforces the admin secret gate in every test binary that imports this
// package. When running under "go test" (testing.Testing() is true) it calls Check
// and, on failure, prints the error to standard error and exits the process with
// status 1 before any test runs. Outside tests it does nothing, because the server
// binary performs the check itself in main.
func init() {
	if !testing.Testing() {
		return // the server binary checks the secret itself in main
	}
	if err := Check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
