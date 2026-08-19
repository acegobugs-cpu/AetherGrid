package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"
)

// startLocalServer binds the local-only debug API (health and status) and
// serves it in a background goroutine. It returns an error when the address
// cannot be bound.
func (a *Agent) startLocalServer() error {
	listener, err := net.Listen("tcp", a.cfg.ListenAddr)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.handleHealth)
	mux.HandleFunc("GET /status", a.handleStatus)

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	a.mu.Lock()
	a.localListener = listener
	a.localServer = server
	a.mu.Unlock()

	a.logger.Printf("local debug API listening on %s", listener.Addr().String())

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Printf("local debug API stopped: %v", err)
		}
	}()

	return nil
}

// stopLocalServer gracefully shuts down the debug API and waits for it to
// stop.
func (a *Agent) stopLocalServer() {
	a.mu.RLock()
	listener := a.localListener
	server := a.localServer
	a.mu.RUnlock()
	if listener == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if server != nil {
		if err := server.Shutdown(ctx); err != nil {
			a.logger.Printf("local debug API shutdown: %v", err)
		}
	}

	a.mu.Lock()
	a.localListener = nil
	a.localServer = nil
	a.mu.Unlock()
}

// handleHealth reports the agent's liveness. It returns 503 while the agent is
// not ready.
func (a *Agent) handleHealth(w http.ResponseWriter, _ *http.Request) {
	status := a.currentStatus()
	code := http.StatusOK
	if status != "READY" {
		code = http.StatusServiceUnavailable
	}
	writeLocalJSON(w, code, map[string]any{
		"status":  status,
		"node_id": a.currentNodeID(),
	})
}

// handleStatus reports the agent's full runtime status for local debugging.
func (a *Agent) handleStatus(w http.ResponseWriter, _ *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	lastDesired := ""
	if a.lastDesired != nil {
		lastDesired = a.lastDesired.DesiredStatus
	}

	writeLocalJSON(w, http.StatusOK, map[string]any{
		"node_id":      a.nodeID,
		"status":       a.status,
		"name":         a.cfg.NodeName,
		"location":     a.cfg.NodeLocation,
		"version":      a.cfg.Version,
		"ip_address":   a.nodeIP,
		"last_sync":    formatTime(a.lastSync),
		"last_state":   a.lastState,
		"last_desired": lastDesired,
		"kubernetes": map[string]any{
			"available":       a.lastK8s.Available,
			"status":          a.lastK8s.Status,
			"version":         a.lastK8s.Version,
			"node_count":      a.lastK8s.NodeCount,
			"ready_nodes":     a.lastK8s.ReadyNodes,
			"not_ready_nodes": a.lastK8s.NotReadyNodes,
		},
	})
}

func writeLocalJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
