// Command aether-agent runs the AETHER-GRID edge node agent.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"AetherGrid/nodeAgent/internal/agent"
	"AetherGrid/nodeAgent/internal/config"
)

func main() {
	logger := log.New(os.Stdout, "[aether-agent] ", log.LstdFlags)

	cfg := config.FromEnv()
	if err := cfg.Validate(); err != nil {
		logger.Fatalf("invalid configuration: %v", err)
	}

	a := agent.New(cfg, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := a.Run(ctx); err != nil {
		logger.Fatalf("agent failed: %v", err)
	}
	logger.Printf("aether-agent exited cleanly")
}
