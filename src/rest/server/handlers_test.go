package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

func TestHealthCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	taskService := services.NewTaskService()
	router := gin.New()
	NewHandler(taskService).RegisterRoutes(router)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Existing fields are unchanged.
	if body["status"] != "healthy" {
		t.Errorf("status = %v, want healthy", body["status"])
	}
	if body["service"] != "task-api-go" {
		t.Errorf("service = %v, want task-api-go", body["service"])
	}
	for _, key := range []string{"timestamp", "version", "process"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing existing field %q", key)
		}
	}

	// New additive checks field.
	checks, ok := body["checks"].(map[string]any)
	if !ok {
		t.Fatalf("checks missing or not an object: %v", body["checks"])
	}
	svc, ok := checks["task_service"].(map[string]any)
	if !ok {
		t.Fatalf("checks.task_service missing or not an object: %v", checks["task_service"])
	}
	if svc["status"] != "ok" {
		t.Errorf("checks.task_service.status = %v, want ok", svc["status"])
	}
	want := float64(taskService.GetStats().Total)
	if svc["task_count"] != want {
		t.Errorf("checks.task_service.task_count = %v, want %v", svc["task_count"], want)
	}

	// The response must never leak the admin secret or its hash.
	if secret := os.Getenv("TASK_API_ADMIN_SECRET"); secret != "" && strings.Contains(w.Body.String(), secret) {
		t.Error("health response contains the admin secret")
	}
}

func TestHealthChecksNilTaskService(t *testing.T) {
	h := &Handler{} // taskService intentionally nil
	checks := h.healthChecks()

	svc, ok := checks["task_service"].(gin.H)
	if !ok {
		t.Fatalf("checks.task_service missing or not an object: %v", checks["task_service"])
	}
	if svc["status"] != "unavailable" {
		t.Errorf("checks.task_service.status = %v, want unavailable", svc["status"])
	}
	if _, ok := svc["task_count"]; ok {
		t.Error("task_count must not be reported when the task service is unavailable")
	}
}
