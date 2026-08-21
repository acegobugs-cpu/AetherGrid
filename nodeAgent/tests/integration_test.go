// Package agent_test contains end-to-end integration tests that run the real
// agent runtime (real HTTP client, real state collector) against an in-memory
// mock of the control plane HTTP API.
package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"AetherGrid/nodeAgent/internal/agent"
	"AetherGrid/nodeAgent/internal/config"
)

// commandReport records a command result reported to the mock control plane.
type commandReport struct {
	NodeID    string
	CommandID string
	Status    string
	Result    any
	Error     string
}

// mockControlPlane is an in-memory implementation of the control plane HTTP
// API. It can be stopped and restarted on the same address to simulate a
// control plane restart.
type mockControlPlane struct {
	mu           sync.Mutex
	nodes        map[string]mockNode
	nodeCounter  int
	heartbeats   map[string]int
	stateReports []stateReport
	desired      map[string]string
	pending      map[string][]pendingCommand
	results      []commandReport
	addr         string
	server       *httptest.Server
}

type mockNode struct {
	ID        string
	Name      string
	Status    string
	Location  string
	IPAddress string
}

type stateReport struct {
	NodeID string
	Status string
	IP     string
	// Kubernetes is the observed Kubernetes summary sent by the agent.
	Kubernetes map[string]any
}

type pendingCommand struct {
	ID         string
	Type       string
	Parameters map[string]any
}

type nodeResponse struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Status            string  `json:"status"`
	DesiredStatus     string  `json:"desired_status"`
	Location          string  `json:"location"`
	IPAddress         string  `json:"ip_address"`
	KubernetesEnabled bool    `json:"kubernetes_enabled"`
	WireGuardEnabled  bool    `json:"wireguard_enabled"`
	LastHeartbeat     *string `json:"last_heartbeat"`
}

type desiredStateResponse struct {
	NodeID        string `json:"node_id"`
	DesiredStatus string `json:"desired_status"`
}

type commandResponse struct {
	ID         string          `json:"id"`
	NodeID     string          `json:"node_id"`
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters"`
	Status     string          `json:"status"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// startMockControlPlane creates a mock control plane and returns it.
func startMockControlPlane() *mockControlPlane {
	mock := &mockControlPlane{
		nodes:      make(map[string]mockNode),
		heartbeats: make(map[string]int),
		desired:    make(map[string]string),
		pending:    make(map[string][]pendingCommand),
	}
	mock.start()
	return mock
}

// start binds the mock server. It is called on initial creation and again
// after a simulated restart.
func (m *mockControlPlane) start() {
	handler := http.HandlerFunc(m.route)
	server := httptest.NewUnstartedServer(handler)
	if m.addr != "" {
		listener, err := net.Listen("tcp", m.addr)
		if err != nil {
			panic(fmt.Sprintf("re-binding mock control plane on %s: %v", m.addr, err))
		}
		server.Listener = listener
	}
	server.Start()
	m.server = server
	if m.addr == "" {
		m.addr = server.Listener.Addr().String()
	}
}

// stop shuts down the mock control plane but keeps its state, simulating a
// control plane going away while the agent keeps running.
func (m *mockControlPlane) stop() {
	m.server.Close()
	m.server = nil
}

// restart simulates a control plane restart on the same address with the same
// persisted state.
func (m *mockControlPlane) restart() {
	m.start()
}

// url returns the base URL of the mock control plane.
func (m *mockControlPlane) url() string {
	return "http://" + m.addr
}

// queueCommand adds a pending command for a node.
func (m *mockControlPlane) queueCommand(nodeID, commandType string, parameters map[string]any) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := fmt.Sprintf("cmd-%d", len(m.results)+len(m.pending[nodeID])+1)
	m.pending[nodeID] = append(m.pending[nodeID], pendingCommand{ID: id, Type: commandType, Parameters: parameters})
	return id
}

func (m *mockControlPlane) nodeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.nodes)
}

func (m *mockControlPlane) heartbeatCount(nodeID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.heartbeats[nodeID]
}

func (m *mockControlPlane) stateReportCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.stateReports)
}

func (m *mockControlPlane) resultCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.results)
}

func (m *mockControlPlane) latestResult() (commandReport, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.results) == 0 {
		return commandReport{}, false
	}
	return m.results[len(m.results)-1], true
}

func (m *mockControlPlane) nodeID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.nodes {
		return id
	}
	return ""
}

// route dispatches requests to per-endpoint handlers.
func (m *mockControlPlane) route(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/nodes":
		m.handleRegister(w, r)
	case r.Method == http.MethodGet && pathSegments(r.URL.Path) == 2 && pathOf(r.URL.Path, 1) == "nodes":
		m.handleGetNode(w, r)
	case r.Method == http.MethodPost && pathSegments(r.URL.Path) == 3 && pathOf(r.URL.Path, 1) == "nodes" && pathOf(r.URL.Path, 3) == "heartbeat":
		m.handleHeartbeat(w, r)
	case r.Method == http.MethodPut && pathSegments(r.URL.Path) == 3 && pathOf(r.URL.Path, 1) == "nodes" && pathOf(r.URL.Path, 3) == "state":
		m.handleReportState(w, r)
	case r.Method == http.MethodGet && pathSegments(r.URL.Path) == 3 && pathOf(r.URL.Path, 1) == "nodes" && pathOf(r.URL.Path, 3) == "desired-state":
		m.handleDesiredState(w, r)
	case r.Method == http.MethodGet && pathSegments(r.URL.Path) == 3 && pathOf(r.URL.Path, 1) == "nodes" && pathOf(r.URL.Path, 3) == "commands":
		m.handleListCommands(w, r)
	case r.Method == http.MethodPost && pathSegments(r.URL.Path) == 5 && pathOf(r.URL.Path, 1) == "nodes" && pathOf(r.URL.Path, 3) == "commands" && pathOf(r.URL.Path, 5) == "result":
		m.handleCommandResult(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (m *mockControlPlane) handleRegister(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name              string `json:"name"`
		Location          string `json:"location"`
		IPAddress         string `json:"ip_address"`
		KubernetesEnabled bool   `json:"kubernetes_enabled"`
		WireGuardEnabled  bool   `json:"wireguard_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid registration payload")
		return
	}

	m.mu.Lock()
	m.nodeCounter++
	id := fmt.Sprintf("node-%d", m.nodeCounter)
	m.nodes[id] = mockNode{ID: id, Name: input.Name, Status: "PROVISIONING", Location: input.Location, IPAddress: input.IPAddress}
	m.desired[id] = "READY"
	m.mu.Unlock()

	writeJSON(w, http.StatusCreated, nodeResponse{ID: id, Name: input.Name, Status: "PROVISIONING", Location: input.Location, IPAddress: input.IPAddress, DesiredStatus: "READY"})
}

func (m *mockControlPlane) handleGetNode(w http.ResponseWriter, r *http.Request) {
	nodeID := pathOf(r.URL.Path, 2)
	m.mu.Lock()
	node, ok := m.nodes[nodeID]
	desired := m.desired[nodeID]
	m.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	writeJSON(w, http.StatusOK, nodeResponse{ID: node.ID, Name: node.Name, Status: node.Status, Location: node.Location, IPAddress: node.IPAddress, DesiredStatus: desired})
}

func (m *mockControlPlane) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	nodeID := pathOf(r.URL.Path, 2)
	m.mu.Lock()
	if _, ok := m.nodes[nodeID]; !ok {
		m.mu.Unlock()
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	m.heartbeats[nodeID]++
	m.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (m *mockControlPlane) handleReportState(w http.ResponseWriter, r *http.Request) {
	nodeID := pathOf(r.URL.Path, 2)
	var report struct {
		Status     string         `json:"status"`
		IPAddress  string         `json:"ip_address"`
		Kubernetes map[string]any `json:"kubernetes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		writeError(w, http.StatusBadRequest, "invalid state payload")
		return
	}
	m.mu.Lock()
	if _, ok := m.nodes[nodeID]; !ok {
		m.mu.Unlock()
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	m.stateReports = append(m.stateReports, stateReport{NodeID: nodeID, Status: report.Status, IP: report.IPAddress, Kubernetes: report.Kubernetes})
	m.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (m *mockControlPlane) handleDesiredState(w http.ResponseWriter, r *http.Request) {
	nodeID := pathOf(r.URL.Path, 2)
	m.mu.Lock()
	desired, ok := m.desired[nodeID]
	m.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	writeJSON(w, http.StatusOK, desiredStateResponse{NodeID: nodeID, DesiredStatus: desired})
}

func (m *mockControlPlane) handleListCommands(w http.ResponseWriter, r *http.Request) {
	nodeID := pathOf(r.URL.Path, 2)
	m.mu.Lock()
	pending := append([]pendingCommand(nil), m.pending[nodeID]...)
	m.mu.Unlock()

	responses := make([]commandResponse, 0, len(pending))
	for _, command := range pending {
		raw, err := json.Marshal(command.Parameters)
		if err != nil {
			raw = json.RawMessage("null")
		}
		responses = append(responses, commandResponse{
			ID:         command.ID,
			NodeID:     nodeID,
			Type:       command.Type,
			Parameters: raw,
			Status:     "PENDING",
		})
	}
	writeJSON(w, http.StatusOK, responses)
}

func (m *mockControlPlane) handleCommandResult(w http.ResponseWriter, r *http.Request) {
	nodeID := pathOf(r.URL.Path, 2)
	commandID := pathOf(r.URL.Path, 4)
	var payload struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result,omitempty"`
		Error  string          `json:"error,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid command result payload")
		return
	}

	m.mu.Lock()
	pending := m.pending[nodeID]
	filtered := pending[:0]
	for _, command := range pending {
		if command.ID != commandID {
			filtered = append(filtered, command)
		}
	}
	m.pending[nodeID] = filtered
	m.results = append(m.results, commandReport{
		NodeID:    nodeID,
		CommandID: commandID,
		Status:    payload.Status,
		Result:    decodeRaw(payload.Result),
		Error:     payload.Error,
	})
	m.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func decodeRaw(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func pathSegments(path string) int {
	count := 0
	for _, segment := range splitPath(path) {
		if segment != "" {
			count++
		}
	}
	return count
}

func pathOf(path string, index int) string {
	segments := splitPath(path)
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		index--
		if index == 0 {
			return segment
		}
	}
	return ""
}

func splitPath(path string) []string {
	var segments []string
	current := ""
	for _, char := range path {
		if char == '/' {
			segments = append(segments, current)
			current = ""
		} else {
			current += string(char)
		}
	}
	segments = append(segments, current)
	return segments
}

// integrationConfig builds a configuration pointing at the mock control plane.
func integrationConfig(t *testing.T, mock *mockControlPlane) config.Config {
	t.Helper()
	return config.Config{
		ControlPlaneURL:     mock.url(),
		NodeName:            "edge-01",
		NodeLocation:        "addis-01",
		DataDir:             t.TempDir(),
		HeartbeatInterval:   20 * time.Millisecond,
		StateReportInterval: 30 * time.Millisecond,
		CommandPollInterval: 20 * time.Millisecond,
		CommandTimeout:      2 * time.Second,
		InitialBackoff:      5 * time.Millisecond,
		MaxBackoff:          50 * time.Millisecond,
		ListenAddr:          "127.0.0.1:0",
		Version:             "integration-test",
	}
}

// runRealAgent starts a real agent runtime against the mock control plane.
func runRealAgent(t *testing.T, cfg config.Config) (*agent.Agent, context.CancelFunc, <-chan error) {
	t.Helper()
	logger := log.New(io.Discard, "", 0)
	a := agent.New(cfg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()
	return a, cancel, done
}

func waitFor(t *testing.T, what string, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func assertCleanExit(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent exited with error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not shut down cleanly")
	}
}

func TestIntegrationFullLifecycle(t *testing.T) {
	mock := startMockControlPlane()
	defer mock.stop()

	cfg := integrationConfig(t, mock)
	_, cancel, done := runRealAgent(t, cfg)

	waitFor(t, "registration", func() bool { return mock.nodeCount() == 1 })
	waitFor(t, "heartbeats", func() bool { return mock.heartbeatCount(mock.nodeID()) >= 2 })
	waitFor(t, "state reports", func() bool { return mock.stateReportCount() >= 1 })

	// Phase 4: the actual-state report carries the observed Kubernetes state.
	report := mock.stateReports[len(mock.stateReports)-1]
	if report.Kubernetes == nil {
		t.Fatal("expected kubernetes state in the state report")
	}
	if report.Kubernetes["status"] != "DISABLED" {
		t.Errorf("expected kubernetes DISABLED (default config), got %v", report.Kubernetes["status"])
	}

	nodeID := mock.nodeID()
	identityFile := filepath.Join(cfg.DataDir, "node-id")
	persisted, err := os.ReadFile(identityFile)
	if err != nil {
		t.Fatalf("identity not persisted: %v", err)
	}
	if strings.TrimSpace(string(persisted)) != nodeID {
		t.Errorf("persisted identity %q, expected %q", string(persisted), nodeID)
	}

	commandID := mock.queueCommand(nodeID, "GET_STATUS", map[string]any{})
	waitFor(t, "command result", func() bool { return mock.resultCount() >= 1 })

	result, _ := mock.latestResult()
	if result.CommandID != commandID {
		t.Errorf("expected result for %s, got %s", commandID, result.CommandID)
	}
	if result.Status != "SUCCEEDED" {
		t.Errorf("expected SUCCEEDED, got %s", result.Status)
	}
	if _, ok := result.Result.(map[string]any); !ok {
		t.Errorf("expected GET_STATUS to return a status object, got %v", result.Result)
	}

	cancel()
	assertCleanExit(t, done)
}

func TestIntegrationControlPlaneRestart(t *testing.T) {
	mock := startMockControlPlane()

	cfg := integrationConfig(t, mock)
	_, cancel, done := runRealAgent(t, cfg)

	waitFor(t, "registration", func() bool { return mock.nodeCount() == 1 })
	waitFor(t, "initial heartbeats", func() bool { return mock.heartbeatCount(mock.nodeID()) >= 2 })

	// The control plane goes away entirely; the agent must survive.
	mock.stop()
	beforeRestart := mock.heartbeatCount(mock.nodeID())
	time.Sleep(150 * time.Millisecond)

	// Verify the agent is still running during the outage.
	select {
	case err := <-done:
		t.Fatalf("agent terminated during control plane outage: %v", err)
	default:
	}

	// The control plane restarts with its persisted state.
	mock.restart()
	waitFor(t, "heartbeat recovery", func() bool {
		return mock.heartbeatCount(mock.nodeID()) > beforeRestart
	})

	// The agent must not have re-registered; it resumed using its identity.
	if mock.nodeCount() != 1 {
		t.Errorf("expected 1 node after control plane restart, got %d", mock.nodeCount())
	}

	cancel()
	assertCleanExit(t, done)
}

func TestIntegrationIdentityPersistedAcrossRestarts(t *testing.T) {
	mock := startMockControlPlane()
	defer mock.stop()

	cfg := integrationConfig(t, mock)

	first, cancelFirst, doneFirst := runRealAgent(t, cfg)
	waitFor(t, "first registration", func() bool { return mock.nodeCount() == 1 })
	waitFor(t, "first heartbeats", func() bool { return mock.heartbeatCount(mock.nodeID()) >= 2 })
	cancelFirst()
	assertCleanExit(t, doneFirst)

	if mock.nodeCount() != 1 {
		t.Fatalf("expected 1 node after first run, got %d", mock.nodeCount())
	}

	// A fresh agent process on the same data dir must reuse the identity.
	second, cancelSecond, doneSecond := runRealAgent(t, cfg)
	waitFor(t, "heartbeats after restart", func() bool { return mock.heartbeatCount(mock.nodeID()) >= 4 })
	cancelSecond()
	assertCleanExit(t, doneSecond)

	if mock.nodeCount() != 1 {
		t.Errorf("second run must not register a new node; got %d nodes", mock.nodeCount())
	}
	_ = first
	_ = second
}
