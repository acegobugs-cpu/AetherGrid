// Command aether-grid runs the AETHER-GRID control plane.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"AetherGrid/controlPlane/internal/audit"
	"AetherGrid/controlPlane/internal/auth"
	"AetherGrid/controlPlane/internal/config"
	apihandler "AetherGrid/controlPlane/internal/http"
	"AetherGrid/controlPlane/internal/provisioning"
	"AetherGrid/controlPlane/internal/provisioning/terraform"
	"AetherGrid/controlPlane/internal/reconcile"
	"AetherGrid/controlPlane/internal/repository/sqlite"
	"AetherGrid/controlPlane/internal/service"
	"AetherGrid/controlPlane/migrations"
)

func main() {
	logger := log.New(os.Stdout, "[aether-grid] ", log.LstdFlags)

	cfg := config.FromEnv()

	// Phase 10 secure startup: fail fast on unsafe configuration instead of
	// silently running an insecure production deployment.
	if err := cfg.Validate(); err != nil {
		logger.Fatalf("invalid security configuration: %v", err)
	}

	logger.Printf("starting aether-grid control plane (host=%s port=%s db=%s env=%s tls=%s)",
		cfg.ServerHost, cfg.ServerPort, cfg.DatabasePath, cfg.Environment,
		map[bool]string{true: "enabled", false: "disabled"}[cfg.TLSEnabled()])
	if !cfg.Production() {
		logger.Printf("DEVELOPMENT environment: explicit overrides are active; production requires AETHERGRID_ENV=production")
	}
	if cfg.OpenRegistration {
		logger.Printf("WARNING: anonymous self-registration is ENABLED (development only)")
	}

	if err := ensureParentDir(cfg.DatabasePath); err != nil {
		logger.Fatalf("preparing database directory: %v", err)
	}

	nodeRepo, err := sqlite.NewNodeRepository(cfg.DatabasePath)
	if err != nil {
		logger.Fatalf("opening database: %v", err)
	}
	defer nodeRepo.Close()
	restrictDBPermissions(cfg.DatabasePath, logger)
	logger.Printf("database initialized: %s", cfg.DatabasePath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := migrations.Apply(ctx, nodeRepo.DB()); err != nil {
		logger.Fatalf("applying migrations: %v", err)
	}
	logger.Printf("database migrations applied")

	// Phase 10: credential lifecycle and audit trail.
	credentialRepo := sqlite.NewCredentialRepository(nodeRepo.DB())
	credentials := auth.NewService(credentialRepo)
	credentials.BootstrapTokenTTL = cfg.BootstrapTokenTTL
	credentials.AgentCredentialTTL = cfg.AgentCredentialTTL

	staticKeys, err := auth.NewStaticKeyStore(cfg.StaticAuthKeys)
	if err != nil {
		logger.Fatalf("invalid static API keys: %v", err)
	}
	if cfg.Production() && !hasRole(staticKeys, auth.RoleAdmin) {
		logger.Fatalf("production requires at least one admin static key")
	}

	auditRepo := sqlite.NewAuditRepository(nodeRepo.DB())
	auditor := audit.NewLogger(logger, auditRepo)

	nodeService := service.NewNodeService(nodeRepo)
	heartbeatService := service.NewHeartbeatService(nodeRepo)
	commandService := service.NewCommandService(sqlite.NewCommandRepository(nodeRepo.DB()), nodeRepo)
	clusterRepo := sqlite.NewClusterRepository(nodeRepo.DB())
	clusterOpRepo := sqlite.NewClusterOperationRepository(nodeRepo.DB())
	reconcilerSvc := service.NewReconciliationService(
		reconcileConfig(cfg),
		nodeRepo,
		sqlite.NewReconciliationRepository(nodeRepo.DB()),
		commandService,
		logger,
		clusterRepo,
	)
	// Cluster-aware recovery preconditions (Phase 9 #70): recovery only runs
	// against AETHER-GRID-managed clusters with no conflicting operation.
	reconcilerSvc.SetClusterInspector(
		service.NewClusterInspectorAdapter(clusterRepo, clusterOpRepo, nodeRepo, logger))

	infraRepo := sqlite.NewInfrastructureRepository(nodeRepo.DB())
	provisioner := terraform.NewProvisioner(
		cfg.TerraformBin,
		cfg.TerraformWorkDir,
		cfg.TerraformModuleDir,
		cfg.TerraformTimeout,
		logger,
	)
	infrastructureService := service.NewInfrastructureService(
		infraRepo,
		infraRepo,
		provisioner,
		&provisioning.Metrics{},
		logger,
	)

	clusterService := service.NewClusterService(
		clusterRepo,
		clusterOpRepo,
		nodeRepo,
		nil, // K3sBootstrapper - will be initialized later
		logger,
	)

	nodeService.SetReconcileNotifier(reconcilerSvc.Notify)
	heartbeatService.SetReconcileNotifier(reconcilerSvc.Notify)

	router := apihandler.NewRouter(
		apihandler.Services{
			Nodes:           nodeService,
			Heartbeats:      heartbeatService,
			Reconciler:      reconcilerSvc,
			Commands:        commandService,
			Infrastructures: infrastructureService,
			Clusters:        clusterService,
		},
		apihandler.Security{
			Credentials:      credentials,
			StaticKeys:       staticKeys,
			Auditor:          auditor,
			OpenRegistration: cfg.OpenRegistration,
			TLSEnabled:       cfg.TLSEnabled(),
		},
		logger,
	)

	if err := infrastructureService.Recover(ctx); err != nil {
		logger.Fatalf("recovering infrastructure state: %v", err)
	}
	logger.Printf("infrastructure provisioning ready (bin=%s workdir=%s module=%s)",
		cfg.TerraformBin, cfg.TerraformWorkDir, cfg.TerraformModuleDir)

	server := &http.Server{
		Addr:              cfg.ListenAddress(),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	reconcilerSvc.Start()
	defer reconcilerSvc.Stop()

	serverErrors := make(chan error, 1)
	go func() {
		if cfg.TLSEnabled() {
			logger.Printf("listening on https://%s", cfg.ListenAddress())
			serverErrors <- server.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
			return
		}
		logger.Printf("listening on http://%s", cfg.ListenAddress())
		serverErrors <- server.ListenAndServe()
	}()

	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("server failed: %v", err)
		}
	case sig := <-shutdownSignals:
		logger.Printf("received signal %s, shutting down", sig)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Printf("graceful shutdown failed: %v", err)
	}
	logger.Printf("aether-grid control plane stopped")
}

// restrictDBPermissions tightens permissions on the SQLite database files so
// only the control plane's user can read them.
func restrictDBPermissions(dbPath string, logger *log.Logger) {
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(path); err == nil {
			if err := os.Chmod(path, 0o600); err != nil {
				logger.Printf("restricting database permissions for %s: %v", path, err)
			}
		}
	}
}

// hasRole reports whether the store contains at least one key of the role.
func hasRole(store *auth.StaticKeyStore, role auth.Role) bool {
	for _, candidate := range store.Roles() {
		if candidate == role {
			return true
		}
	}
	return false
}

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o700)
}

// reconcileConfig maps the application configuration onto the engine config.
func reconcileConfig(cfg config.Config) reconcile.Config {
	return reconcile.Config{
		Interval:                 cfg.ReconciliationInterval,
		Workers:                  cfg.ReconciliationWorkers,
		HeartbeatTimeout:         cfg.NodeHeartbeatTimeout,
		MaxRetries:               cfg.ReconciliationMaxRetries,
		MaxBackoff:               cfg.ReconciliationMaxBackoff,
		RecoveryTimeout:          cfg.ReconciliationRecoveryTimeout,
		FailureConfirmMultiplier: cfg.FailureConfirmMultiplier,
		RecoveryPolicy: reconcile.RecoveryPolicy{
			WorkerAutomaticRecovery:       reconcile.BoolPtr(cfg.WorkerRecoveryEnabled),
			ControlPlaneAutomaticRecovery: reconcile.BoolPtr(cfg.ControlPlaneRecoveryEnabled),
			MaxRecoveryAttempts:           cfg.MaxRecoveryAttempts,
			RecoveryBackoff: reconcile.RecoveryBackoff{
				BaseDelay:     cfg.RecoveryBackoffBase,
				MaxDelay:      cfg.RecoveryBackoffMax,
				JitterEnabled: cfg.RecoveryBackoffJitterEnabled,
			},
			MaxConcurrentRecoveries:   cfg.MaxConcurrentRecoveries,
			RecoveryCooldown:          cfg.RecoveryCooldown,
			MaxReplacementsPerCluster: cfg.MaxReplacementsPerCluster,
		},
	}
}
