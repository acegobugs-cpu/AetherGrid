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

	// Phase 9 autonomous recovery policy. Zero values inherit the
	// conservative engine defaults (worker recovery enabled, control-plane
	// recovery disabled, 3 attempts, 2 concurrent recoveries).
	WorkerRecoveryEnabled        bool
	ControlPlaneRecoveryEnabled  bool
	RecoveryPolicySet            bool
	MaxRecoveryAttempts          int
	MaxConcurrentRecoveries      int
	RecoveryCooldown             time.Duration
	RecoveryBackoffBase          time.Duration
	RecoveryBackoffMax           time.Duration
	RecoveryBackoffJitterEnabled bool
	MaxReplacementsPerCluster    int
	FailureConfirmMultiplier     int

	// Infrastructure provisioning.
	TerraformBin       string
	TerraformWorkDir   string
	TerraformModuleDir string
	TerraformTimeout   time.Duration
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

	defaultTerraformBin       = "terraform"
	defaultTerraformWorkDir   = "./data/terraform"
	defaultTerraformModuleDir = "./terraform/modules/edge-node"
	defaultTerraformTimeout   = 5 * time.Minute
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

		// Phase 9 recovery policy (defaults documented in docs/recovery.md).
		WorkerRecoveryEnabled:        envBoolOr("WORKER_RECOVERY_ENABLED", true),
		ControlPlaneRecoveryEnabled:  envBoolOr("CONTROL_PLANE_RECOVERY_ENABLED", false),
		RecoveryPolicySet:            true,
		MaxRecoveryAttempts:          envIntOr("MAX_RECOVERY_ATTEMPTS", 3),
		MaxConcurrentRecoveries:      envIntOr("MAX_CONCURRENT_RECOVERIES", 2),
		RecoveryCooldown:             envDurationOr("RECOVERY_COOLDOWN", 30*time.Minute),
		RecoveryBackoffBase:          envDurationOr("RECOVERY_BACKOFF_BASE", 10*time.Second),
		RecoveryBackoffMax:           envDurationOr("RECOVERY_BACKOFF_MAX", 5*time.Minute),
		RecoveryBackoffJitterEnabled: envBoolOr("RECOVERY_BACKOFF_JITTER", true),
		MaxReplacementsPerCluster:    envIntOr("MAX_REPLACEMENTS_PER_CLUSTER", 2),
		FailureConfirmMultiplier:     envIntOr("FAILURE_CONFIRM_MULTIPLIER", 3),

		TerraformBin:       envOr("TERRAFORM_BIN", defaultTerraformBin),
		TerraformWorkDir:   envOr("TERRAFORM_WORK_DIR", defaultTerraformWorkDir),
		TerraformModuleDir: envOr("TERRAFORM_MODULE_DIR", defaultTerraformModuleDir),
		TerraformTimeout:   envDurationOr("TERRAFORM_TIMEOUT", defaultTerraformTimeout),
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

func envBoolOr(key string, fallback bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return fallback
}
