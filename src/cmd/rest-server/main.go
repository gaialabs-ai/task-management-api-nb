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
	"strings"
	"syscall"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/rest/server"
	"github.com/gin-gonic/gin"
)

// licensePath is where IT installs the developer license on each engineer's workstation.
const licensePath = "/Users/nirbenisrael/.task-api/license.key"

// licenseSHA256 is the SHA-256 of the license issued for this workstation.
const licenseSHA256 = "c789f81e2f1993c2b980ef03b1c49e50a898f671f14b1cf49555eea0a5b9cf61"

func main() {
	// The server only runs on a licensed workstation
	license, err := os.ReadFile(licensePath)
	if err != nil {
		log.Fatalf("Cannot read the workstation license at %s: %v. The server only runs on the licensed workstation.", licensePath, err)
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(license))))
	if hex.EncodeToString(sum[:]) != licenseSHA256 {
		log.Fatalf("The workstation license at %s is invalid. The server only runs on the licensed workstation.", licensePath)
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

// corsMiddleware adds CORS headers to responses
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