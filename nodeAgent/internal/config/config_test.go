package config

import (
	"testing"
	"time"
)

func TestFromEnvDefaults(t *testing.T) {
	cfg := FromEnv()
	if cfg.ControlPlaneURL != "http://localhost:8080" {
		t.Errorf("expected default control plane URL, got %q", cfg.ControlPlaneURL)
	}
	if cfg.NodeName == "" {
		t.Error("expected default node name (hostname)")
	}
	if cfg.NodeLocation != "local" {
		t.Errorf("expected default location local, got %q", cfg.NodeLocation)
	}
	if cfg.DataDir != "./data" {
		t.Errorf("expected default data dir ./data, got %q", cfg.DataDir)
	}
	if cfg.HeartbeatInterval != 10*time.Second {
		t.Errorf("expected default heartbeat interval 10s, got %v", cfg.HeartbeatInterval)
	}
	if cfg.StateReportInterval != 30*time.Second {
		t.Errorf("expected default state interval 30s, got %v", cfg.StateReportInterval)
	}
	if cfg.CommandPollInterval != 5*time.Second {
		t.Errorf("expected default command poll interval 5s, got %v", cfg.CommandPollInterval)
	}
	if cfg.CommandTimeout != 30*time.Second {
		t.Errorf("expected default command timeout 30s, got %v", cfg.CommandTimeout)
	}
	if cfg.InitialBackoff != 1*time.Second {
		t.Errorf("expected default initial backoff 1s, got %v", cfg.InitialBackoff)
	}
	if cfg.MaxBackoff != 30*time.Second {
		t.Errorf("expected default max backoff 30s, got %v", cfg.MaxBackoff)
	}
	if cfg.ListenAddr != "127.0.0.1:9090" {
		t.Errorf("expected default listen address, got %q", cfg.ListenAddr)
	}
	if cfg.Version != "dev" {
		t.Errorf("expected default version dev, got %q", cfg.Version)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	t.Setenv("CONTROL_PLANE_URL", "http://127.0.0.1:9000")
	t.Setenv("NODE_NAME", "edge-07")
	t.Setenv("NODE_LOCATION", "addis-01")
	t.Setenv("NODE_ID", "abc-123")
	t.Setenv("AGENT_DATA_DIR", "/tmp/agent-data")
	t.Setenv("HEARTBEAT_INTERVAL", "3s")
	t.Setenv("STATE_REPORT_INTERVAL", "15s")
	t.Setenv("COMMAND_POLL_INTERVAL", "2s")
	t.Setenv("COMMAND_TIMEOUT", "5s")
	t.Setenv("RETRY_INITIAL_BACKOFF", "500ms")
	t.Setenv("RETRY_MAX_BACKOFF", "5s")
	t.Setenv("AGENT_LISTEN_ADDR", "127.0.0.1:9999")
	t.Setenv("AGENT_VERSION", "test")

	cfg := FromEnv()
	if cfg.ControlPlaneURL != "http://127.0.0.1:9000" {
		t.Errorf("expected overridden URL, got %q", cfg.ControlPlaneURL)
	}
	if cfg.NodeName != "edge-07" {
		t.Errorf("expected overridden name, got %q", cfg.NodeName)
	}
	if cfg.NodeID != "abc-123" {
		t.Errorf("expected overridden node id, got %q", cfg.NodeID)
	}
	if cfg.DataDir != "/tmp/agent-data" {
		t.Errorf("expected overridden data dir, got %q", cfg.DataDir)
	}
	if cfg.HeartbeatInterval != 3*time.Second {
		t.Errorf("expected heartbeat interval 3s, got %v", cfg.HeartbeatInterval)
	}
	if cfg.StateReportInterval != 15*time.Second {
		t.Errorf("expected state interval 15s, got %v", cfg.StateReportInterval)
	}
	if cfg.CommandPollInterval != 2*time.Second {
		t.Errorf("expected command poll interval 2s, got %v", cfg.CommandPollInterval)
	}
	if cfg.CommandTimeout != 5*time.Second {
		t.Errorf("expected command timeout 5s, got %v", cfg.CommandTimeout)
	}
	if cfg.InitialBackoff != 500*time.Millisecond {
		t.Errorf("expected initial backoff 500ms, got %v", cfg.InitialBackoff)
	}
	if cfg.MaxBackoff != 5*time.Second {
		t.Errorf("expected max backoff 5s, got %v", cfg.MaxBackoff)
	}
	if cfg.ListenAddr != "127.0.0.1:9999" {
		t.Errorf("expected overridden listen addr, got %q", cfg.ListenAddr)
	}
	if cfg.Version != "test" {
		t.Errorf("expected version test, got %q", cfg.Version)
	}
}

func TestValidate(t *testing.T) {
	valid := Config{
		ControlPlaneURL:     "http://localhost:8080",
		NodeName:            "edge-01",
		DataDir:             "./data",
		HeartbeatInterval:   10 * time.Second,
		StateReportInterval: 30 * time.Second,
		CommandPollInterval: 5 * time.Second,
		CommandTimeout:      30 * time.Second,
		InitialBackoff:      1 * time.Second,
		MaxBackoff:          30 * time.Second,
		ListenAddr:          "127.0.0.1:9090",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty url", func(c *Config) { c.ControlPlaneURL = "" }},
		{"bad url scheme", func(c *Config) { c.ControlPlaneURL = "ftp://localhost" }},
		{"url without host", func(c *Config) { c.ControlPlaneURL = "http://" }},
		{"empty name", func(c *Config) { c.NodeName = "" }},
		{"empty data dir", func(c *Config) { c.DataDir = "" }},
		{"zero heartbeat", func(c *Config) { c.HeartbeatInterval = 0 }},
		{"zero state interval", func(c *Config) { c.StateReportInterval = 0 }},
		{"zero command interval", func(c *Config) { c.CommandPollInterval = 0 }},
		{"zero command timeout", func(c *Config) { c.CommandTimeout = 0 }},
		{"zero initial backoff", func(c *Config) { c.InitialBackoff = 0 }},
		{"max below initial", func(c *Config) { c.MaxBackoff = 1; c.InitialBackoff = 2 }},
		{"empty listen addr", func(c *Config) { c.ListenAddr = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Errorf("expected validation error for %s", test.name)
			}
		})
	}
}
