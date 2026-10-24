// Test harness: serves the real router (server.NewHandler + RegisterRoutes)
// without the workstation license check in cmd/rest-server. Does not modify app code.
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
	log.Fatal(r.Run(":" + port))
}
