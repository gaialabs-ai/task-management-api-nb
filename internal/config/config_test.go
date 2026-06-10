package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad_DefaultValues(t *testing.T) {
	t.Run("returns default port 8080 when PORT is not set", func(t *testing.T) {
		t.Setenv("PORT", "")

		cfg := Load()

		assert.Equal(t, "8080", cfg.Port)
	})

	t.Run("returns default gin mode debug when GIN_MODE is not set", func(t *testing.T) {
		t.Setenv("GIN_MODE", "")

		cfg := Load()

		assert.Equal(t, "debug", cfg.GinMode)
	})
}

func TestLoad_EnvironmentVariableOverrides(t *testing.T) {
	t.Run("uses custom port when PORT env var is set", func(t *testing.T) {
		t.Setenv("PORT", "3000")

		cfg := Load()

		assert.Equal(t, "3000", cfg.Port)
	})

	t.Run("uses custom gin mode when GIN_MODE env var is set", func(t *testing.T) {
		t.Setenv("GIN_MODE", "release")

		cfg := Load()

		assert.Equal(t, "release", cfg.GinMode)
	})

	t.Run("uses both custom values when PORT and GIN_MODE are set", func(t *testing.T) {
		t.Setenv("PORT", "9090")
		t.Setenv("GIN_MODE", "test")

		cfg := Load()

		assert.Equal(t, "9090", cfg.Port)
		assert.Equal(t, "test", cfg.GinMode)
	})
}
