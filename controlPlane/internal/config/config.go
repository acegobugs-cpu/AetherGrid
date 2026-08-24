package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Deployment environments. Development permits explicitly configured
// insecure shortcuts; production enforces secure defaults and refuses to
// start with unsafe configuration.
const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
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

	// Phase 10 security posture.
	Environment string
	// TLSCertFile/TLSKeyFile enable HTTPS when both are set.
	TLSCertFile string
	TLSKeyFile  string
	// StaticAuthKeys holds "token:role" human API keys (AUTH_STATIC_KEYS).
	StaticAuthKeys []string
	// OpenRegistration allows anonymous node self-registration. Development
	// only: Validate refuses it in production.
	OpenRegistration bool
	// BootstrapTokenTTL bounds the single-use registration credential.
	BootstrapTokenTTL time.Duration
	// AgentCredentialTTL is the max age of a node's agent credential.
	AgentCredentialTTL time.Duration
	// MaxBodyBytes caps every request body.
	MaxBodyBytes int64
	// Rate limits per minute for sensitive route classes.
	RegisterRatePerMinute int
	MutationRatePerMinute int
}

// TLSEnabled reports whether the server should serve HTTPS.
func (c Config) TLSEnabled() bool {
	return c.TLSCertFile != "" && c.TLSKeyFile != ""
}

// Production reports whether production security enforcement applies.
func (c Config) Production() bool {
	return c.Environment == EnvProduction
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

	// Security defaults (Phase 10). The default environment is development
	// so the existing local workflow keeps working; production must be
	// opted into explicitly and then enforces TLS and authentication.
	defaultEnvironment       = EnvDevelopment
	defaultBootstrapTokenTTL = 15 * time.Minute
	defaultAgentCredTTL      = 90 * 24 * time.Hour
	defaultMaxBodyBytes      = int64(1 << 20) // 1 MiB
	defaultRegisterRate      = 30
	defaultMutationRate      = 120
)

// ListenAddress returns the host:port address the HTTP server should bind to.
func (c Config) ListenAddress() string {
	return c.ServerHost + ":" + c.ServerPort
}

// Validate enforces the Phase 10 secure-startup contract:
//
//   - production requires TLS and at least one admin static key;
//   - anonymous self-registration is refused in production;
//   - credential lifetimes are sane.
func (c Config) Validate() error {
	switch c.Environment {
	case EnvDevelopment, EnvProduction:
	default:
		return fmt.Errorf("AETHERGRID_ENV must be %q or %q, got %q", EnvDevelopment, EnvProduction, c.Environment)
	}

	if c.Production() {
		if !c.TLSEnabled() {
			return fmt.Errorf("refusing to start: production requires TLS (set TLS_CERT_FILE and TLS_KEY_FILE)")
		}
		if len(c.StaticAuthKeys) == 0 {
			return fmt.Errorf("refusing to start: production requires human API keys (set AUTH_STATIC_KEYS)")
		}
		if c.OpenRegistration {
			return fmt.Errorf("refusing to start: AUTH_OPEN_REGISTRATION is not permitted in production")
		}
	}

	if c.TLSEnabled() {
		if _, err := os.Stat(c.TLSCertFile); err != nil {
			return fmt.Errorf("TLS_CERT_FILE is not readable: %w", err)
		}
		if _, err := os.Stat(c.TLSKeyFile); err != nil {
			return fmt.Errorf("TLS_KEY_FILE is not readable: %w", err)
		}
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return fmt.Errorf("TLS_CERT_FILE and TLS_KEY_FILE must be set together")
	}

	for _, pair := range c.StaticAuthKeys {
		token, role, found := strings.Cut(pair, ":")
		if !found || strings.TrimSpace(token) == "" || strings.TrimSpace(role) == "" {
			return fmt.Errorf("AUTH_STATIC_KEYS entries must be token:role")
		}
		if len(strings.TrimSpace(token)) < 16 {
			return fmt.Errorf("AUTH_STATIC_KEYS tokens must be at least 16 characters")
		}
		switch strings.TrimSpace(strings.ToLower(role)) {
		case "admin", "operator", "viewer":
		default:
			return fmt.Errorf("AUTH_STATIC_KEYS role %q is invalid", role)
		}
	}

	if c.BootstrapTokenTTL <= 0 {
		return fmt.Errorf("BOOTSTRAP_TOKEN_TTL must be positive")
	}
	if c.AgentCredentialTTL <= 0 {
		return fmt.Errorf("AGENT_CREDENTIAL_TTL must be positive")
	}
	if c.AgentCredentialTTL < c.BootstrapTokenTTL {
		return fmt.Errorf("AGENT_CREDENTIAL_TTL must exceed BOOTSTRAP_TOKEN_TTL")
	}
	if c.MaxBodyBytes <= 0 {
		return fmt.Errorf("MAX_BODY_BYTES must be positive")
	}
	if c.RegisterRatePerMinute <= 0 || c.MutationRatePerMinute <= 0 {
		return fmt.Errorf("rate limits must be positive")
	}
	return nil
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

		// Phase 10 security posture.
		Environment:           strings.ToLower(envOr("AETHERGRID_ENV", defaultEnvironment)),
		TLSCertFile:           os.Getenv("TLS_CERT_FILE"),
		TLSKeyFile:            os.Getenv("TLS_KEY_FILE"),
		StaticAuthKeys:        splitNonEmpty(os.Getenv("AUTH_STATIC_KEYS")),
		OpenRegistration:      envBoolOr("AUTH_OPEN_REGISTRATION", false),
		BootstrapTokenTTL:     envDurationOr("BOOTSTRAP_TOKEN_TTL", defaultBootstrapTokenTTL),
		AgentCredentialTTL:    envDurationOr("AGENT_CREDENTIAL_TTL", defaultAgentCredTTL),
		MaxBodyBytes:          int64(envIntOr("MAX_BODY_BYTES", int(defaultMaxBodyBytes))),
		RegisterRatePerMinute: envIntOr("REGISTER_RATE_PER_MINUTE", defaultRegisterRate),
		MutationRatePerMinute: envIntOr("MUTATION_RATE_PER_MINUTE", defaultMutationRate),
	}
}

// splitNonEmpty splits a comma-separated environment value, dropping empty
// segments.
func splitNonEmpty(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			result = append(result, part)
		}
	}
	return result
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
