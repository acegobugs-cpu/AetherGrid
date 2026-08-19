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

	"github.com/acegobugs-cpu/AetherGrid/internal/config"
	apihandler "github.com/acegobugs-cpu/AetherGrid/internal/http"
	"github.com/acegobugs-cpu/AetherGrid/internal/provisioning"
	"github.com/acegobugs-cpu/AetherGrid/internal/provisioning/terraform"
	"github.com/acegobugs-cpu/AetherGrid/internal/reconcile"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository/sqlite"
	"github.com/acegobugs-cpu/AetherGrid/internal/service"
	"github.com/acegobugs-cpu/AetherGrid/migrations"
)

func main() {
	logger := log.New(os.Stdout, "[aether-grid] ", log.LstdFlags)

	cfg := config.FromEnv()
	logger.Printf("starting aether-grid control plane (host=%s port=%s db=%s)",
		cfg.ServerHost, cfg.ServerPort, cfg.DatabasePath)

	if err := ensureParentDir(cfg.DatabasePath); err != nil {
		logger.Fatalf("preparing database directory: %v", err)
	}

	nodeRepo, err := sqlite.NewNodeRepository(cfg.DatabasePath)
	if err != nil {
		logger.Fatalf("opening database: %v", err)
	}
	defer nodeRepo.Close()
	logger.Printf("database initialized: %s", cfg.DatabasePath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := migrations.Apply(ctx, nodeRepo.DB()); err != nil {
		logger.Fatalf("applying migrations: %v", err)
	}
	logger.Printf("database migrations applied")

	nodeService := service.NewNodeService(nodeRepo)
	heartbeatService := service.NewHeartbeatService(nodeRepo)
	commandService := service.NewCommandService(sqlite.NewCommandRepository(nodeRepo.DB()), nodeRepo)
	reconciler := service.NewReconciliationService(
		reconcileConfig(cfg),
		nodeRepo,
		sqlite.NewReconciliationRepository(nodeRepo.DB()),
		commandService,
		logger,
	)

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

	nodeService.SetReconcileNotifier(reconciler.Notify)
	heartbeatService.SetReconcileNotifier(reconciler.Notify)

	router := apihandler.NewRouter(nodeService, heartbeatService, reconciler, commandService, infrastructureService, logger)

	if err := infrastructureService.Recover(ctx); err != nil {
		logger.Fatalf("recovering infrastructure state: %v", err)
	}
	logger.Printf("infrastructure provisioning ready (bin=%s workdir=%s module=%s)",
		cfg.TerraformBin, cfg.TerraformWorkDir, cfg.TerraformModuleDir)

	server := &http.Server{
		Addr:              cfg.ListenAddress(),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	reconciler.Start()
	defer reconciler.Stop()

	serverErrors := make(chan error, 1)
	go func() {
		logger.Printf("listening on %s", cfg.ListenAddress())
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

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// reconcileConfig maps the application configuration onto the engine config.
func reconcileConfig(cfg config.Config) reconcile.Config {
	return reconcile.Config{
		Interval:         cfg.ReconciliationInterval,
		Workers:          cfg.ReconciliationWorkers,
		HeartbeatTimeout: cfg.NodeHeartbeatTimeout,
		MaxRetries:       cfg.ReconciliationMaxRetries,
		MaxBackoff:       cfg.ReconciliationMaxBackoff,
		RecoveryTimeout:  cfg.ReconciliationRecoveryTimeout,
	}
}
