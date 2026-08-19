// Package config loads and validates the runtime configuration for the
// AETHER-GRID edge node agent.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the runtime configuration for the edge node agent.
type Config struct {
	// ControlPlaneURL is the base URL of the AETHER-GRID control plane.
	ControlPlaneURL string
	// NodeName is the human-readable name of this edge node.
	NodeName string
	// NodeLocation is the deployment location of this edge node.
	NodeLocation string
	// NodeID is an optional pre-assigned identity. When empty the agent
	// registers with the control plane to obtain one.
	NodeID string
	// DataDir is where the agent persists its local identity.
	DataDir string
	// HeartbeatInterval controls how often the agent sends heartbeats.
	HeartbeatInterval time.Duration
	// StateReportInterval controls how often the agent reports its state.
	StateReportInterval time.Duration
	// CommandPollInterval controls how often the agent polls for commands.
	CommandPollInterval time.Duration
	// CommandTimeout bounds how long a single command may run.
	CommandTimeout time.Duration
	// InitialBackoff is the retry delay after the first connection failure.
	InitialBackoff time.Duration
	// MaxBackoff caps the exponential retry delay.
	MaxBackoff time.Duration
	// ListenAddr is the local-only address for the agent's debug API.
	ListenAddr string
	// Version is the build version reported in state and status.
	Version string
	// KubernetesEnabled turns Kubernetes integration on. When false the agent
	// operates normally and reports Kubernetes as DISABLED.
	KubernetesEnabled bool
	// Kubeconfig is an explicit path to a kubeconfig file. When empty the
	// standard kubeconfig loading rules (KUBECONFIG, ~/.kube/config) and
	// in-cluster configuration are used.
	Kubeconfig string
	// KubernetesRequestTimeout bounds every Kubernetes API call so an
	// unavailable cluster cannot block the agent.
	KubernetesRequestTimeout time.Duration
}

// Defaults used when the corresponding environment variable is not set.
const (
	defaultControlPlaneURL   = "http://localhost:8080"
	defaultNodeLocation      = "local"
	defaultDataDir           = "./data"
	defaultHeartbeatInterval = 10 * time.Second
	defaultStateInterval     = 30 * time.Second
	defaultCommandInterval   = 5 * time.Second
	defaultCommandTimeout    = 30 * time.Second
	defaultInitialBackoff    = 1 * time.Second
	defaultMaxBackoff        = 30 * time.Second
	defaultListenAddr        = "127.0.0.1:9090"
	defaultVersion           = "dev"
	defaultKubernetesTimeout = 10 * time.Second
)

// FromEnv builds a Config from environment variables, falling back to
// sensible local-development defaults when variables are not set.
func FromEnv() Config {
	return Config{
		ControlPlaneURL:          envOr("CONTROL_PLANE_URL", defaultControlPlaneURL),
		NodeName:                 envOr("NODE_NAME", defaultHostname()),
		NodeLocation:             envOr("NODE_LOCATION", defaultNodeLocation),
		NodeID:                   os.Getenv("NODE_ID"),
		DataDir:                  envOr("AGENT_DATA_DIR", defaultDataDir),
		HeartbeatInterval:        durationEnvOr("HEARTBEAT_INTERVAL", defaultHeartbeatInterval),
		StateReportInterval:      durationEnvOr("STATE_REPORT_INTERVAL", defaultStateInterval),
		CommandPollInterval:      durationEnvOr("COMMAND_POLL_INTERVAL", defaultCommandInterval),
		CommandTimeout:           durationEnvOr("COMMAND_TIMEOUT", defaultCommandTimeout),
		InitialBackoff:           durationEnvOr("RETRY_INITIAL_BACKOFF", defaultInitialBackoff),
		MaxBackoff:               durationEnvOr("RETRY_MAX_BACKOFF", defaultMaxBackoff),
		ListenAddr:               envOr("AGENT_LISTEN_ADDR", defaultListenAddr),
		Version:                  envOr("AGENT_VERSION", defaultVersion),
		KubernetesEnabled:        envBoolOr("KUBERNETES_ENABLED", false),
		Kubeconfig:               os.Getenv("KUBECONFIG"),
		KubernetesRequestTimeout: durationEnvOr("KUBERNETES_REQUEST_TIMEOUT", defaultKubernetesTimeout),
	}
}

// Validate checks the configuration for values that would prevent the agent
// from starting. Invalid configuration must fail startup.
func (c Config) Validate() error {
	if strings.TrimSpace(c.ControlPlaneURL) == "" {
		return fmt.Errorf("CONTROL_PLANE_URL is required")
	}
	parsed, err := url.Parse(c.ControlPlaneURL)
	if err != nil {
		return fmt.Errorf("CONTROL_PLANE_URL is invalid: %v", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("CONTROL_PLANE_URL must use http or https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("CONTROL_PLANE_URL must include a host")
	}
	if strings.TrimSpace(c.NodeName) == "" {
		return fmt.Errorf("NODE_NAME is required")
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return fmt.Errorf("AGENT_DATA_DIR is required")
	}
	if c.HeartbeatInterval <= 0 {
		return fmt.Errorf("HEARTBEAT_INTERVAL must be positive")
	}
	if c.StateReportInterval <= 0 {
		return fmt.Errorf("STATE_REPORT_INTERVAL must be positive")
	}
	if c.CommandPollInterval <= 0 {
		return fmt.Errorf("COMMAND_POLL_INTERVAL must be positive")
	}
	if c.CommandTimeout <= 0 {
		return fmt.Errorf("COMMAND_TIMEOUT must be positive")
	}
	if c.InitialBackoff <= 0 {
		return fmt.Errorf("RETRY_INITIAL_BACKOFF must be positive")
	}
	if c.MaxBackoff < c.InitialBackoff {
		return fmt.Errorf("RETRY_MAX_BACKOFF must be >= RETRY_INITIAL_BACKOFF")
	}
	if strings.TrimSpace(c.ListenAddr) == "" {
		return fmt.Errorf("AGENT_LISTEN_ADDR is required")
	}
	if c.KubernetesRequestTimeout <= 0 {
		return fmt.Errorf("KUBERNETES_REQUEST_TIMEOUT must be positive")
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationEnvOr(key string, fallback time.Duration) time.Duration {
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

func defaultHostname() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return "edge-node"
	}
	return hostname
}
