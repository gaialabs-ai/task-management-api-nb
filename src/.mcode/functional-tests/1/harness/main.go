// TEST-ONLY functional-test harness. It builds the router the same way the unit/HTTP
// tests do (server.NewHandler + RegisterRoutes) with an in-memory TaskService, and is
// NOT a substitute for cmd/rest-server: the workstation license check there is
// unchanged and remains the only supported way to run the server. To keep this from
// being used as a license-free server, it refuses to start unless FT_HARNESS_TEST_ONLY=1
// is set by the functional-test runner, and it listens on 127.0.0.1 only.
package main

import (
	"errors"
	"log"
	"os"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/rest/server"
	"github.com/gin-gonic/gin"
)

type failNotifier struct{}

func (failNotifier) NotifyAssigned(models.Task, string) error { return errors.New("smtp down") }

func main() {
	if os.Getenv("FT_HARNESS_TEST_ONLY") != "1" {
		log.Fatal("functional-test harness is test-only; run the licensed server via cmd/rest-server instead")
	}
	gin.SetMode(gin.ReleaseMode)
	svc := services.NewTaskService()
	if os.Getenv("FAIL_NOTIFIER") == "1" {
		svc.SetNotifier(failNotifier{})
	}
	r := gin.New()
	r.Use(gin.Recovery())
	server.NewHandler(svc).RegisterRoutes(r)
	port := os.Getenv("PORT")
	if port == "" {
		port = "18080"
	}
	log.Fatal(r.Run("127.0.0.1:" + port))
}
