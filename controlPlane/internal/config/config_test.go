package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func validProductionConfig() Config {
	return Config{
		Environment:           EnvProduction,
		TLSCertFile:           "testdata/cert.pem",
		TLSKeyFile:            "testdata/key.pem",
		StaticAuthKeys:        []string{"production-admin-key-01:admin"},
		BootstrapTokenTTL:     15 * time.Minute,
		AgentCredentialTTL:    90 * 24 * time.Hour,
		MaxBodyBytes:          1 << 20,
		RegisterRatePerMinute: 30,
		MutationRatePerMinute: 120,
	}
}

func TestValidateAcceptsDevelopmentDefaults(t *testing.T) {
	cfg := Config{
		Environment:           EnvDevelopment,
		BootstrapTokenTTL:     15 * time.Minute,
		AgentCredentialTTL:    90 * 24 * time.Hour,
		MaxBodyBytes:          1 << 20,
		RegisterRatePerMinute: 30,
		MutationRatePerMinute: 120,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("development defaults must validate, got %v", err)
	}
}

func TestValidateRejectsUnknownEnvironment(t *testing.T) {
	cfg := Config{Environment: "staging"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("unknown environment must be rejected")
	}
}

func TestValidateProductionRequiresTLS(t *testing.T) {
	cfg := validProductionConfig()
	cfg.TLSCertFile = ""
	cfg.TLSKeyFile = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("production without TLS must be refused")
	}
}

func TestValidateProductionRequiresStaticKeys(t *testing.T) {
	cfg := validProductionConfig()
	cfg.StaticAuthKeys = nil
	if err := cfg.Validate(); err == nil {
		t.Fatal("production without human API keys must be refused")
	}
}

func TestValidateRefusesOpenRegistrationInProduction(t *testing.T) {
	cfg := validProductionConfig()
	cfg.OpenRegistration = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("anonymous registration must never be allowed in production")
	}
}

func TestValidateChecksTLSFilesExist(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")

	cfg := validProductionConfig()
	cfg.TLSCertFile = cert
	cfg.TLSKeyFile = filepath.Join(dir, "missing.pem")
	if err := cfg.Validate(); err == nil {
		t.Fatal("missing TLS key file must be refused")
	}

	if err := os.WriteFile(cert, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.TLSKeyFile = cert
	if err := cfg.Validate(); err != nil {
		t.Fatalf("existing TLS files must pass, got %v", err)
	}
}

func TestValidateRequiresCertAndKeyTogether(t *testing.T) {
	cfg := Config{
		Environment: EnvDevelopment,
		TLSCertFile: "cert.pem",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("certificate without key must be rejected")
	}
}

func TestValidateRejectsWeakStaticKeys(t *testing.T) {
	cfg := Config{
		Environment:    EnvDevelopment,
		StaticAuthKeys: []string{"short:key"},
	}
	// Validation of static keys happens before TTL checks; a short token
	// must fail regardless.
	if err := cfg.Validate(); err == nil {
		t.Fatal("short static key tokens must be rejected")
	}
}

func TestValidateRejectsBadCredentialTTLs(t *testing.T) {
	cfg := Config{
		Environment:           EnvDevelopment,
		BootstrapTokenTTL:     -time.Minute,
		AgentCredentialTTL:    90 * 24 * time.Hour,
		MaxBodyBytes:          1 << 20,
		RegisterRatePerMinute: 30,
		MutationRatePerMinute: 120,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("non-positive bootstrap TTL must be rejected")
	}

	cfg.BootstrapTokenTTL = 2 * time.Hour
	cfg.AgentCredentialTTL = time.Hour
	if err := cfg.Validate(); err == nil {
		t.Fatal("agent credential TTL below bootstrap TTL must be rejected")
	}
}
