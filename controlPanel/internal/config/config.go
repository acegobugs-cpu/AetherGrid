package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds the runtime configuration for the AETHER-GRID control plane.
type Config struct {
	ServerHost   string
	ServerPort   string
	DatabasePath string

	// Reconciliation engine tuning.
	ReconciliationInterval        time.Duration
	ReconciliationWorkers         int
	NodeHeartbeatTimeout          time.Duration
	ReconciliationMaxRetries      int
	ReconciliationMaxBackoff      time.Duration
	ReconciliationRecoveryTimeout time.Duration
}

// Defaults used when the corresponding environment variable is not set.
const (
	defaultHost   = "0.0.0.0"
	defaultPort   = "8080"
	defaultDBPath = "./data/aether-grid.db"

	defaultReconciliationInterval        = 10 * time.Second
	defaultReconciliationWorkers         = 4
	defaultNodeHeartbeatTimeout          = 30 * time.Second
	defaultReconciliationMaxRetries      = 3
	defaultReconciliationMaxBackoff      = 10 * time.Second
	defaultReconciliationRecoveryTimeout = 60 * time.Second
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
		DatabasePath: envOr("DATABASE_PATH", defaultDBPath),

		ReconciliationInterval:        envDurationOr("RECONCILIATION_INTERVAL", defaultReconciliationInterval),
		ReconciliationWorkers:         envIntOr("RECONCILIATION_WORKERS", defaultReconciliationWorkers),
		NodeHeartbeatTimeout:          envDurationOr("NODE_HEARTBEAT_TIMEOUT", defaultNodeHeartbeatTimeout),
		ReconciliationMaxRetries:      envIntOr("RECONCILIATION_MAX_RETRIES", defaultReconciliationMaxRetries),
		ReconciliationMaxBackoff:      envDurationOr("RECONCILIATION_MAX_BACKOFF", defaultReconciliationMaxBackoff),
		ReconciliationRecoveryTimeout: envDurationOr("RECONCILIATION_RECOVERY_TIMEOUT", defaultReconciliationRecoveryTimeout),
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func envDurationOr(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return fallback
}
