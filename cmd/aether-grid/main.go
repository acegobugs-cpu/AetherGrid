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
	reconciler := service.NewReconciliationService(nodeRepo)

	router := apihandler.NewRouter(nodeService, heartbeatService, reconciler, logger)

	server := &http.Server{
		Addr:              cfg.ListenAddress(),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

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
