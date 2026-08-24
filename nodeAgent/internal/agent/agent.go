// Package agent implements the runtime that coordinates registration,
// heartbeat and state-reporting loops, and command handling for the
// AETHER-GRID edge node agent.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"AetherGrid/nodeAgent/internal/backoff"
	"AetherGrid/nodeAgent/internal/client"
	"AetherGrid/nodeAgent/internal/command"
	"AetherGrid/nodeAgent/internal/config"
	"AetherGrid/nodeAgent/internal/identity"
	"AetherGrid/nodeAgent/internal/kubernetes"
	"AetherGrid/nodeAgent/internal/state"
)

// Agent coordinates the edge node agent's lifecycle.
type Agent struct {
	cfg           config.Config
	logger        *log.Logger
	cpClient      client.ControlPlaneClient
	collector     state.Collector
	commands      *command.Registry
	identityStore *identity.Store
	kubernetes    *kubernetes.Service
	k8sCollector  kubernetes.Collector

	mu          sync.RWMutex
	status      state.AgentStatus
	nodeID      string
	lastSync    time.Time
	lastState   state.NodeState
	lastDesired *client.DesiredState
	lastK8s     kubernetes.KubernetesState

	nodeIP string

	processedMu sync.Mutex
	processed   map[string]bool

	// hasCredential records whether a node credential is installed. Without
	// one the agent cannot authenticate and must not attempt privileged
	// loops.
	hasCredential bool

	cancel        context.CancelFunc
	wg            sync.WaitGroup
	localListener net.Listener
	localServer   *http.Server
}

// New constructs an agent from the given configuration and logger.
func New(cfg config.Config, logger *log.Logger) *Agent {
	return newAgent(cfg, logger, client.New(cfg.ControlPlaneURL), state.NewLocalCollector(cfg.Version))
}

// newAgent is the shared constructor used by New and by tests.
func newAgent(cfg config.Config, logger *log.Logger, cpClient client.ControlPlaneClient, collector state.Collector) *Agent {
	k8sService := kubernetes.NewService(kubernetes.ServiceConfig{
		Enabled:        cfg.KubernetesEnabled,
		RequestTimeout: cfg.KubernetesRequestTimeout,
	}, func() (kubernetes.KubernetesClient, error) {
		return kubernetes.NewClient(cfg.Kubeconfig)
	}, logger)

	agent := &Agent{
		cfg:           cfg,
		logger:        logger,
		cpClient:      cpClient,
		collector:     collector,
		commands:      command.NewRegistry(),
		identityStore: identity.NewStore(cfg.DataDir),
		status:        state.StatusStarting,
		processed:     make(map[string]bool),
		nodeIP:        detectIP(cfg.ControlPlaneURL),
		kubernetes:    k8sService,
		k8sCollector:  kubernetes.NewStateCollector(k8sService),
	}

	// Phase 10: if the agent already holds a persisted credential it is
	// attached to every control-plane request. The HTTP client supports
	// SetToken; static fakes in tests ignore it via the interface.
	if setter, ok := cpClient.(interface{ SetToken(string) }); ok {
		if token, err := agent.identityStore.LoadCredential(); err == nil && token != "" {
			setter.SetToken(token)
			agent.hasCredential = true
		}
	}

	agent.commands.Register("GET_STATUS", command.NewGetStatusHandler(agent.stateSnapshot))
	agent.commands.Register("RESTART_AGENT", command.NewRestartHandler())
	agent.commands.Register(command.CommandGetKubernetesStatus, command.NewGetKubernetesStatusHandler(k8sService))
	agent.commands.Register(command.CommandListKubernetesNodes, command.NewListKubernetesNodesHandler(k8sService))
	agent.commands.Register(command.CommandListKubernetesPods, command.NewListKubernetesPodsHandler(k8sService))
	agent.commands.Register(command.CommandCreateTestNamespace, command.NewCreateTestNamespaceHandler(k8sService))
	agent.commands.Register(command.CommandDeleteTestNamespace, command.NewDeleteTestNamespaceHandler(k8sService))
	return agent
}

// Run starts the agent and blocks until ctx is cancelled or a restart command
// requests shutdown. It returns nil after a clean shutdown.
func (a *Agent) Run(ctx context.Context) error {
	a.setStatus(state.StatusStarting)
	a.logger.Printf("agent starting (version=%s)", a.cfg.Version)
	a.logger.Printf("configuration loaded: control_plane=%s node_name=%s node_location=%s data_dir=%s heartbeat=%s state=%s commands=%s timeout=%s kubernetes=%s",
		a.cfg.ControlPlaneURL, a.cfg.NodeName, a.cfg.NodeLocation, a.cfg.DataDir,
		a.cfg.HeartbeatInterval, a.cfg.StateReportInterval, a.cfg.CommandPollInterval, a.cfg.CommandTimeout,
		map[bool]string{true: "enabled", false: "disabled"}[a.cfg.KubernetesEnabled])

	if err := a.setupIdentity(ctx); err != nil {
		return err
	}
	a.setStatus(state.StatusReady)
	a.logger.Printf("identity ready: node_id=%s ip=%s", a.currentNodeID(), a.nodeIP)

	if err := a.startLocalServer(); err != nil {
		// The debug API is optional; a bind failure must not stop the agent.
		a.logger.Printf("local debug API unavailable: %v", err)
	}
	defer a.stopLocalServer()

	runCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	defer cancel()

	a.wg.Add(3)
	go a.heartbeatLoop(runCtx)
	go a.stateLoop(runCtx)
	go a.commandLoop(runCtx)

	a.logger.Printf("agent running: node_id=%s", a.currentNodeID())
	<-runCtx.Done()

	a.setStatus(state.StatusStopping)
	a.logger.Printf("agent shutting down: node_id=%s", a.currentNodeID())
	cancel()
	a.wg.Wait()
	a.logger.Printf("agent stopped: node_id=%s", a.currentNodeID())
	return nil
}

// LocalAddr returns the bound address of the local debug API, or an empty
// string when the API is not running. Used by tests to reach the endpoint.
func (a *Agent) LocalAddr() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.localListener == nil {
		return ""
	}
	return a.localListener.Addr().String()
}

// setupIdentity establishes the agent's authenticated identity (Phase 10).
//
// Resolution order:
//  1. A persisted node credential + node ID: verified against the control
//     plane. A rejected credential (401) fails startup - revoked agents must
//     not silently acquire new identities.
//  2. A bootstrap token from configuration: exchanged for a long-lived node
//     credential via the secure registration endpoint.
//  3. Explicitly enabled self-registration against a development control
//     plane with open registration. Never attempted otherwise.
func (a *Agent) setupIdentity(ctx context.Context) error {
	storedID := a.cfg.NodeID
	if storedID == "" {
		if id, err := a.identityStore.Load(); err == nil {
			storedID = id
		}
	}

	switch {
	case a.hasCredential && storedID != "":
		a.setNodeID(storedID)
		a.logger.Printf("identity loaded: node_id=%s", storedID)

		info, err := a.cpClient.GetNode(ctx, storedID)
		switch {
		case err == nil:
			a.logger.Printf("authenticated to existing node: node_id=%s name=%s status=%s desired=%s",
				storedID, info.Name, info.Status, info.DesiredStatus)
			return nil
		case client.IsUnauthorized(err):
			// Fail closed: the credential exists but is no longer trusted.
			return fmt.Errorf("node credential rejected by control plane (node_id=%s); the credential may have been rotated or revoked", storedID)
		case client.IsNotFound(err):
			// The node record was deleted; its credentials were revoked with
			// it. Re-adopting an identity requires new provisioning.
			return fmt.Errorf("node %s no longer exists on the control plane; provision this agent again", storedID)
		default:
			a.logger.Printf("control plane unreachable at startup (node_id=%s): %v; will authenticate on next heartbeat", storedID, err)
			return nil
		}

	case a.cfg.BootstrapToken != "" && storedID != "":
		a.setNodeID(storedID)
		return a.exchangeBootstrapCredential(ctx, storedID, a.cfg.BootstrapToken)

	case a.cfg.AllowSelfRegistration:
		a.logger.Printf("self-registration enabled by configuration; registering with control plane")
		return a.register(ctx)

	default:
		return fmt.Errorf(
			"no node credential available; provide AETHER_BOOTSTRAP_TOKEN and NODE_ID from provisioning, or restore %s",
			filepath.Join(a.cfg.DataDir, identity.CredentialFileName))
	}
}

// exchangeBootstrapCredential swaps a single-use bootstrap token for the
// long-lived node credential and persists both identities.
func (a *Agent) exchangeBootstrapCredential(ctx context.Context, nodeID, bootstrapToken string) error {
	result, err := a.cpClient.ExchangeBootstrap(ctx, nodeID, bootstrapToken, a.registerInput())
	if err != nil {
		if client.IsUnauthorized(err) {
			return fmt.Errorf("bootstrap credential rejected during registration (node_id=%s): %w", nodeID, err)
		}
		return fmt.Errorf("registration exchange failed: %w", err)
	}
	if result.Credential == "" {
		return fmt.Errorf("control plane did not issue a node credential")
	}
	if result.NodeID != "" && result.NodeID != nodeID {
		return fmt.Errorf("control plane returned mismatched node id %q", result.NodeID)
	}

	if setter, ok := a.cpClient.(interface{ SetToken(string) }); ok {
		setter.SetToken(result.Credential)
	}
	a.hasCredential = true

	if err := a.identityStore.SaveCredential(result.Credential); err != nil {
		return fmt.Errorf("persisting node credential: %w", err)
	}
	if err := a.identityStore.Save(nodeID); err != nil {
		return fmt.Errorf("persisting identity: %w", err)
	}
	a.logger.Printf("secure registration complete: node_id=%s status=%s", nodeID, result.Status)
	return nil
}

// register obtains a node identity from the control plane and persists it,
// including the issued credential. It retries with exponential backoff so a
// temporarily unavailable control plane does not kill a first-time agent.
// This path only runs when self-registration was explicitly enabled.
func (a *Agent) register(ctx context.Context) error {
	attempt := 0
	for {
		result, err := a.cpClient.Register(ctx, a.registerInput())
		if err == nil {
			if result.Credential != "" {
				// Open-registration control planes hand back a bootstrap
				// token that must be exchanged immediately.
				a.setNodeID(result.NodeID)
				if err := a.identityStore.Save(result.NodeID); err != nil {
					return fmt.Errorf("persisting identity: %w", err)
				}
				if err := a.exchangeBootstrapCredential(ctx, result.NodeID, result.Credential); err != nil {
					return err
				}
				return nil
			}
			// Legacy response without credential support (should not happen
			// with Phase 10 control planes).
			a.setNodeID(result.NodeID)
			if err := a.identityStore.Save(result.NodeID); err != nil {
				return fmt.Errorf("persisting identity: %w", err)
			}
			a.logger.Printf("registration successful: node_id=%s status=%s", result.NodeID, result.Status)
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
		attempt++
		delay := backoff.NextDelay(attempt, a.cfg.InitialBackoff, a.cfg.MaxBackoff)
		a.logger.Printf("registration failed (attempt %d): %v; retrying in %s", attempt, err, delay)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (a *Agent) registerInput() client.RegisterInput {
	return client.RegisterInput{
		Name:              a.cfg.NodeName,
		Location:          a.cfg.NodeLocation,
		IPAddress:         a.nodeIP,
		KubernetesEnabled: a.cfg.KubernetesEnabled,
		WireGuardEnabled:  false,
	}
}

// handleIdentityLoss fails closed when the control plane reports the agent's
// credential as untrusted or the node as unknown. A compromised or revoked
// identity must never be silently replaced by a new one; re-joining requires
// fresh provisioning by an operator.
func (a *Agent) handleIdentityLoss(ctx context.Context, reason string) {
	a.logger.Printf("CRITICAL: identity no longer trusted (%s); stopping agent loops (node_id=%s). Re-provision this node to resume.",
		reason, a.currentNodeID())
	a.setStatus(state.StatusDegraded)
	if a.cancel != nil {
		a.cancel()
	}
}

// heartbeatLoop periodically sends heartbeats. On transient failure it enters
// an exponential-backoff retry cycle that returns to the normal schedule as
// soon as the control plane is reachable again. Authentication and existence
// failures are not retryable: they stop the agent.
func (a *Agent) heartbeatLoop(ctx context.Context) {
	defer a.wg.Done()

	timer := time.NewTimer(0)
	defer timer.Stop()

	connected := false
	failures := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		err := a.cpClient.Heartbeat(ctx, a.currentNodeID())
		switch {
		case err == nil:
			if !connected {
				a.logger.Printf("heartbeat established: node_id=%s", a.currentNodeID())
			}
			failures = 0
			connected = true
			a.setStatus(state.StatusReady)
			a.setLastSync(time.Now().UTC())
			timer.Reset(a.cfg.HeartbeatInterval)
		case client.IsUnauthorized(err):
			a.handleIdentityLoss(ctx, "credential rejected")
			return
		case client.IsNotFound(err):
			a.handleIdentityLoss(ctx, "node unknown to control plane")
			return
		default:
			failures++
			connected = false
			a.setStatus(state.StatusDegraded)
			delay := backoff.NextDelay(failures, a.cfg.InitialBackoff, a.cfg.MaxBackoff)
			a.logger.Printf("heartbeat failed: node_id=%s error=%v retrying in %s", a.currentNodeID(), err, delay)
			timer.Reset(delay)
		}
	}
}

// stateLoop periodically collects local state and reports it to the control
// plane, then retrieves the authoritative desired state.
func (a *Agent) stateLoop(ctx context.Context) {
	defer a.wg.Done()

	a.collectAndReport(ctx)
	ticker := time.NewTicker(a.cfg.StateReportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.collectAndReport(ctx)
		}
	}
}

func (a *Agent) collectAndReport(ctx context.Context) {
	collected, err := a.collector.Collect(ctx)
	if err != nil {
		a.logger.Printf("state collection failed: node_id=%s error=%v", a.currentNodeID(), err)
		return
	}
	collected.IPAddress = a.nodeIP
	collected.Status = a.currentStatus()
	a.setLastState(collected)

	a.logger.Printf("state collected: node_id=%s status=%s hostname=%s os=%s/%s cpu=%d mem=%d uptime=%ds version=%s",
		a.currentNodeID(), collected.Status, collected.Hostname, collected.OS, collected.Architecture,
		collected.CPUCount, collected.MemoryBytes, collected.UptimeSeconds, collected.AgentVersion)

	k8sState := a.k8sCollector.Collect(ctx)
	if k8sState.Error != "" {
		a.logger.Printf("kubernetes state unavailable: node_id=%s error=%s", a.currentNodeID(), k8sState.Error)
	}
	a.setLastKubernetes(k8sState)

	if err := a.cpClient.ReportState(ctx, a.currentNodeID(), client.StateReport{
		Status:     string(collected.Status),
		IPAddress:  a.nodeIP,
		Kubernetes: &k8sState,
	}); err != nil {
		a.logger.Printf("state report failed: node_id=%s error=%v", a.currentNodeID(), err)
		return
	}

	desired, err := a.cpClient.GetDesiredState(ctx, a.currentNodeID())
	if err != nil {
		a.logger.Printf("desired state retrieval failed: node_id=%s error=%v", a.currentNodeID(), err)
		return
	}
	a.setLastDesired(&desired)
	a.logger.Printf("desired state retrieved: node_id=%s desired_status=%s", a.currentNodeID(), desired.DesiredStatus)
}

// commandLoop polls the control plane for pending commands.
func (a *Agent) commandLoop(ctx context.Context) {
	defer a.wg.Done()

	ticker := time.NewTicker(a.cfg.CommandPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pollCommands(ctx)
		}
	}
}

func (a *Agent) pollCommands(ctx context.Context) {
	commands, err := a.cpClient.GetPendingCommands(ctx, a.currentNodeID())
	if err != nil {
		a.logger.Printf("command poll failed: node_id=%s error=%v", a.currentNodeID(), err)
		return
	}
	for _, pending := range commands {
		if a.wasProcessed(pending.ID) {
			continue
		}
		a.executeCommand(ctx, pending)
	}
}

// executeCommand runs a single command with a bounded timeout and reports the
// outcome back to the control plane.
func (a *Agent) executeCommand(ctx context.Context, pending client.Command) {
	a.logger.Printf("command received: node_id=%s command_id=%s type=%s", a.currentNodeID(), pending.ID, pending.Type)

	commandCtx, cancel := context.WithTimeout(ctx, a.cfg.CommandTimeout)
	defer cancel()

	result, err := a.commands.Handle(commandCtx, command.Request{
		ID:         pending.ID,
		Type:       pending.Type,
		Parameters: pending.Parameters,
	})
	if err != nil {
		if errors.Is(err, command.ErrRestartRequested) {
			a.logger.Printf("command completed (restart requested): node_id=%s command_id=%s type=%s", a.currentNodeID(), pending.ID, pending.Type)
			report := client.CommandResult{Status: "SUCCEEDED", Result: result}
			if reportErr := a.cpClient.ReportCommandResult(ctx, a.currentNodeID(), pending.ID, report); reportErr != nil {
				a.logger.Printf("command result report failed: node_id=%s command_id=%s error=%v", a.currentNodeID(), pending.ID, reportErr)
			}
			a.markProcessed(pending.ID)
			a.cancel()
			return
		}

		a.logger.Printf("command failed: node_id=%s command_id=%s type=%s error=%v", a.currentNodeID(), pending.ID, pending.Type, err)
		report := client.CommandResult{Status: "FAILED", Error: err.Error()}
		if reportErr := a.cpClient.ReportCommandResult(ctx, a.currentNodeID(), pending.ID, report); reportErr == nil {
			a.markProcessed(pending.ID)
		}
		return
	}

	a.logger.Printf("command completed: node_id=%s command_id=%s type=%s", a.currentNodeID(), pending.ID, pending.Type)
	report := client.CommandResult{Status: "SUCCEEDED", Result: result}
	if reportErr := a.cpClient.ReportCommandResult(ctx, a.currentNodeID(), pending.ID, report); reportErr == nil {
		a.markProcessed(pending.ID)
	}
}

// stateSnapshot builds the payload returned by the GET_STATUS command.
func (a *Agent) stateSnapshot() map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()

	lastDesired := ""
	if a.lastDesired != nil {
		lastDesired = a.lastDesired.DesiredStatus
	}

	kubernetesState := map[string]any{
		"available":       a.lastK8s.Available,
		"status":          a.lastK8s.Status,
		"version":         a.lastK8s.Version,
		"node_count":      a.lastK8s.NodeCount,
		"ready_nodes":     a.lastK8s.ReadyNodes,
		"not_ready_nodes": a.lastK8s.NotReadyNodes,
		"workload": map[string]any{
			"total_pods":   a.lastK8s.Workload.TotalPods,
			"running_pods": a.lastK8s.Workload.RunningPods,
			"failed_pods":  a.lastK8s.Workload.FailedPods,
		},
	}

	return map[string]any{
		"node_id":            a.nodeID,
		"status":             a.status,
		"name":               a.cfg.NodeName,
		"location":           a.cfg.NodeLocation,
		"ip_address":         a.nodeIP,
		"hostname":           a.lastState.Hostname,
		"os":                 a.lastState.OS,
		"architecture":       a.lastState.Architecture,
		"cpu_count":          a.lastState.CPUCount,
		"memory_bytes":       a.lastState.MemoryBytes,
		"uptime_seconds":     a.lastState.UptimeSeconds,
		"agent_version":      a.lastState.AgentVersion,
		"last_sync":          formatTime(a.lastSync),
		"last_desired":       lastDesired,
		"kubernetes":         kubernetesState,
		"kubernetes_enabled": a.cfg.KubernetesEnabled,
		"supported_commands": a.commands.Types(),
	}
}

func (a *Agent) setStatus(status state.AgentStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = status
}

func (a *Agent) currentStatus() state.AgentStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

func (a *Agent) setNodeID(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nodeID = id
}

func (a *Agent) currentNodeID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.nodeID
}

func (a *Agent) setLastSync(t time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastSync = t
}

func (a *Agent) setLastState(s state.NodeState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastState = s
}

func (a *Agent) setLastKubernetes(state kubernetes.KubernetesState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastK8s = state
}

func (a *Agent) setLastDesired(d *client.DesiredState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastDesired = d
}

func (a *Agent) wasProcessed(id string) bool {
	a.processedMu.Lock()
	defer a.processedMu.Unlock()
	return a.processed[id]
}

func (a *Agent) markProcessed(id string) {
	a.processedMu.Lock()
	defer a.processedMu.Unlock()
	a.processed[id] = true
}

// detectIP determines the local outbound IP address that would be used to
// reach the control plane, without sending any network traffic.
func detectIP(controlPlaneURL string) string {
	parsed, err := url.Parse(controlPlaneURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	conn, err := net.Dial("udp", parsed.Host)
	if err != nil {
		return ""
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return ""
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
