package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/rest/server"
	"github.com/gin-gonic/gin"
)

// adminSecretSHA256 is the hex-encoded SHA-256 digest of the admin secret issued
// by the platform team. Only the digest is stored here, never the secret itself;
// main compares the digest of TASK_API_ADMIN_SECRET against it at startup.
const adminSecretSHA256 = "3957679e1a8b4b539ac26397b100428cbd978c8d18b1a4983fda172e95bb3fdf"

// main is the entry point of the REST API server. It first enforces the admin
// secret gate (security policy SEC-114): TASK_API_ADMIN_SECRET must be set and
// its SHA-256 digest must equal adminSecretSHA256, otherwise the process exits
// with a fatal log and never listens. The secret is issued by the platform team
// and the check must not be removed or bypassed. After the gate passes it reads
// PORT (default 8080), sets Gin debug mode when GIN_MODE is unset, builds a Gin
// router with logger, recovery and CORS middleware, creates the task service and
// handler, registers routes, and serves HTTP in a goroutine. It then blocks until
// SIGINT or SIGTERM and shuts down gracefully with a 5-second timeout.
func main() {
	// The server refuses to start without the admin secret (security policy SEC-114)
	secret := os.Getenv("TASK_API_ADMIN_SECRET")
	if secret == "" {
		log.Fatal("TASK_API_ADMIN_SECRET is not set. Ask the platform team for the admin secret; the server cannot start without it.")
	}
	sum := sha256.Sum256([]byte(secret))
	if hex.EncodeToString(sum[:]) != adminSecretSHA256 {
		log.Fatal("TASK_API_ADMIN_SECRET is invalid. Ask the platform team for the admin secret; the server cannot start without it.")
	}

	// Get port from environment or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Set Gin mode based on environment
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.DebugMode)
	}

	// Create Gin router
	router := gin.New()
	
	// Add middleware
	router.Use(gin.Logger())
	router.Use(gin.Recovery())
	router.Use(corsMiddleware())

	// Initialize services
	taskService := services.NewTaskService()

	// Initialize handlers
	handler := server.NewHandler(taskService)
	handler.RegisterRoutes(router)

	// Create HTTP server
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		fmt.Printf("REST API server starting on http://localhost:%s\n", port)
		fmt.Printf("Health check: http://localhost:%s/health\n", port)
		fmt.Printf("API endpoint: http://localhost:%s/api/v1/tasks\n", port)
		
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	
	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server shutdown complete")
}

// corsMiddleware returns Gin middleware that adds permissive CORS headers to every
// response (any origin; GET, POST, PUT, PATCH, DELETE and OPTIONS; and the Origin,
// Content-Type, Accept and Authorization headers). OPTIONS preflight requests are
// answered with 204 No Content and aborted before reaching any handler; all other
// requests continue down the chain. It keeps no state and is safe for concurrent
// use.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
		
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		
		c.Next()
	}
}