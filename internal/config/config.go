package config

import (
	"os"

	"github.com/gin-gonic/gin"
)

// Config holds the application configuration.
type Config struct {
	Port    string
	GinMode string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Port:    getEnvOrDefault("PORT", "8080"),
		GinMode: getEnvOrDefault("GIN_MODE", gin.DebugMode),
	}
}

// getEnvOrDefault returns the value of an environment variable or a default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
