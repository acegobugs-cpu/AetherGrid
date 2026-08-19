package config

import (
	"os"
)

// Config holds the runtime configuration for the AETHER-GRID control plane.
type Config struct {
	ServerHost   string
	ServerPort   string
	DatabasePath string
}

// Defaults used when the corresponding environment variable is not set.
const (
	defaultHost         = "0.0.0.0"
	defaultPort         = "8080"
	defaultDatabasePath = "./data/aether-grid.db"
)

// ListenAddress returns the host:port address the HTTP server should bind to.
func (c Config) ListenAddress() string {
	return c.ServerHost + ":" + c.ServerPort
}

// FromEnv builds a Config from environment variables, falling back to
// sensible local-development defaults when variables are not set.
func FromEnv() Config {
	return Config{
		ServerHost:   envOr("SERVER_HOST", defaultHost),
		ServerPort:   envOr("SERVER_PORT", defaultPort),
		DatabasePath: envOr("DATABASE_PATH", defaultDatabasePath),
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
