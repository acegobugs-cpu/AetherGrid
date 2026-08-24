package agent

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"sync"
	"testing"
	"time"

	"AetherGrid/nodeAgent/internal/client"
	"AetherGrid/nodeAgent/internal/config"
	"AetherGrid/nodeAgent/internal/kubernetes"
	"AetherGrid/nodeAgent/internal/state"
)

// fakeClient is a configurable in-memory ControlPlaneClient for tests.
type fakeClient struct {
	mu sync.Mutex

	registerCalls   int
	registerInput   client.RegisterInput
	registerResult  client.RegisterResult
	registerErr     error
	exchangeCalls   int
	exchangeToken   string
	exchangeErr     error
	credentials     []string // tokens installed via SetToken
	heartbeats      int
	heartbeatErr    error
	getNodeCalls    int
	getNodeErr      error
	getNodeResult   client.NodeInfo
	stateReports    []client.StateReport
	reportStateErr  error
	desiredCalls    int
	desiredResult   client.DesiredState
	desiredErr      error
	pendingCommands []client.Command
	commandResults  []commandReport
}

type commandReport struct {
	commandID string
	result    client.CommandResult
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		registerResult: client.RegisterResult{NodeID: "node-1", Status: "PROVISIONING", Credential: "agr_bootstrap_node1"},
		getNodeResult:  client.NodeInfo{ID: "node-1", Name: "edge-01", Status: "PROVISIONING", DesiredStatus: "READY"},
		desiredResult:  client.DesiredState{NodeID: "node-1", DesiredStatus: "READY"},
	}
}

func (f *fakeClient) Register(_ context.Context, input client.RegisterInput) (client.RegisterResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registerCalls++
	f.registerInput = input
	return f.registerResult, f.registerErr
}

func (f *fakeClient) ExchangeBootstrap(_ context.Context, nodeID, bootstrapToken string, _ client.RegisterInput) (client.RegisterResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchangeCalls++
	f.exchangeToken = bootstrapToken
	if f.exchangeErr != nil {
		return client.RegisterResult{}, f.exchangeErr
	}
	return client.RegisterResult{NodeID: nodeID, Status: "PROVISIONING", Credential: "agr_agent_" + nodeID}, nil
}

// SetToken records credential installation, mirroring the real HTTP client.
func (f *fakeClient) SetToken(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.credentials = append(f.credentials, token)
}

func (f *fakeClient) Heartbeat(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeats++
	return f.heartbeatErr
}

func (f *fakeClient) GetNode(_ context.Context, _ string) (client.NodeInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getNodeCalls++
	return f.getNodeResult, f.getNodeErr
}

func (f *fakeClient) ReportState(_ context.Context, _ string, report client.StateReport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stateReports = append(f.stateReports, report)
	return f.reportStateErr
}

func (f *fakeClient) GetDesiredState(_ context.Context, _ string) (client.DesiredState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.desiredCalls++
	return f.desiredResult, f.desiredErr
}

func (f *fakeClient) GetPendingCommands(_ context.Context, _ string) ([]client.Command, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]client.Command(nil), f.pendingCommands...), nil
}

func (f *fakeClient) ReportCommandResult(_ context.Context, _ string, commandID string, result client.CommandResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commandResults = append(f.commandResults, commandReport{commandID: commandID, result: result})
	return nil
}

func (f *fakeClient) getNodeCallsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getNodeCalls
}

func (f *fakeClient) registerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registerCalls
}

func (f *fakeClient) heartbeatCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.heartbeats
}

func (f *fakeClient) setHeartbeatErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeatErr = err
}

func (f *fakeClient) reportCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stateReports)
}

func (f *fakeClient) commandResultCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.commandResults)
}

func (f *fakeClient) latestCommandResult() (commandReport, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.commandResults) == 0 {
		return commandReport{}, false
	}
	return f.commandResults[len(f.commandResults)-1], true
}

// fakeCollector returns a fixed state for tests.
type fakeCollector struct {
	state state.NodeState
}

func (f *fakeCollector) Collect(_ context.Context) (state.NodeState, error) {
	return f.state, nil
}

func testConfig(t *testing.T, dataDir string) config.Config {
	t.Helper()
	return config.Config{
		ControlPlaneURL:       "http://localhost:8080",
		NodeName:              "edge-01",
		NodeLocation:          "addis-01",
		DataDir:               dataDir,
		HeartbeatInterval:     15 * time.Millisecond,
		StateReportInterval:   25 * time.Millisecond,
		CommandPollInterval:   15 * time.Millisecond,
		CommandTimeout:        time.Second,
		InitialBackoff:        5 * time.Millisecond,
		MaxBackoff:            40 * time.Millisecond,
		ListenAddr:            "127.0.0.1:0",
		Version:               "test",
		AllowSelfRegistration: true, // tests exercise the development open-registration flow
	}
}

func newTestAgent(t *testing.T, cfg config.Config, fake *fakeClient) *Agent {
	t.Helper()
	logger := log.New(io.Discard, "", 0)
	collector := &fakeCollector{state: state.NodeState{
		Hostname:      "edge-01",
		OS:            "linux",
		Architecture:  "amd64",
		CPUCount:      2,
		MemoryBytes:   1024,
		UptimeSeconds: 10,
		AgentVersion:  "test",
	}}
	return newAgent(cfg, logger, fake, collector)
}

// runAgent starts the agent and returns a cancel function plus a channel that
// closes when Run returns.
func runAgent(t *testing.T, a *Agent) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()
	return cancel, done
}

// getLocalJSON performs a GET against the local debug API.
func getLocalJSON(url string) (*http.Response, error) {
	return http.Get(url) //nolint:gosec -- URL comes from the test harness
}

// waitFor polls a predicate until it holds or times out.
func waitFor(t *testing.T, what string, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAgentRegistersAndHeartbeats(t *testing.T) {
	fake := newFakeClient()
	a := newTestAgent(t, testConfig(t, t.TempDir()), fake)

	cancel, done := runAgent(t, a)
	defer cancel()

	waitFor(t, "registration", func() bool { return fake.registerCount() >= 1 })
	waitFor(t, "heartbeats", func() bool { return fake.heartbeatCount() >= 2 })

	if a.currentNodeID() != "node-1" {
		t.Errorf("expected agent node id node-1, got %q", a.currentNodeID())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not shut down")
	}
}

func TestAgentReportsStateAndRetrievesDesired(t *testing.T) {
	fake := newFakeClient()
	a := newTestAgent(t, testConfig(t, t.TempDir()), fake)

	cancel, done := runAgent(t, a)
	defer cancel()

	waitFor(t, "state reports", func() bool { return fake.reportCount() >= 2 })
	waitFor(t, "desired state retrieval", func() bool { return fake.desiredCalls >= 2 })

	report := fake.stateReports[len(fake.stateReports)-1]
	if report.Status != "READY" {
		t.Errorf("expected reported status READY, got %q", report.Status)
	}

	cancel()
	<-done
}

// TestAgentKubernetesStateReporting verifies the Phase 4 integration between
// the agent runtime and the Kubernetes collector: the observed Kubernetes
// state is included in the actual-state report, and Kubernetes-disabled agents
// report DISABLED rather than failing.
func TestAgentKubernetesStateReporting(t *testing.T) {
	fake := newFakeClient()
	cfg := testConfig(t, t.TempDir())
	cfg.KubernetesEnabled = true
	cfg.KubernetesRequestTimeout = time.Second
	a := newTestAgent(t, cfg, fake)

	cancel, done := runAgent(t, a)
	defer cancel()

	waitFor(t, "registration", func() bool { return fake.registerCount() >= 1 })
	if !fake.registerInput.KubernetesEnabled {
		t.Error("expected registration to carry KubernetesEnabled=true")
	}
	waitFor(t, "state reports", func() bool { return fake.reportCount() >= 1 })

	report := fake.stateReports[len(fake.stateReports)-1]
	if report.Kubernetes == nil {
		t.Fatal("expected kubernetes state in the state report")
	}
	if report.Kubernetes.Status != kubernetes.KubernetesStatusUnavailable {
		t.Errorf("expected UNAVAILABLE (no cluster in test), got %s", report.Kubernetes.Status)
	}
	if report.Kubernetes.Available {
		t.Error("expected kubernetes to be unavailable")
	}

	cancel()
	<-done
}

// TestAgentKubernetesDisabled reports DISABLED without failing.
func TestAgentKubernetesDisabled(t *testing.T) {
	fake := newFakeClient()
	a := newTestAgent(t, testConfig(t, t.TempDir()), fake)

	cancel, done := runAgent(t, a)
	defer cancel()

	waitFor(t, "state reports", func() bool { return fake.reportCount() >= 1 })
	report := fake.stateReports[len(fake.stateReports)-1]
	if report.Kubernetes == nil {
		t.Fatal("expected kubernetes state in the state report")
	}
	if report.Kubernetes.Status != kubernetes.KubernetesStatusDisabled {
		t.Errorf("expected DISABLED, got %s", report.Kubernetes.Status)
	}

	cancel()
	<-done
}

func TestAgentReusesPersistedIdentity(t *testing.T) {
	// This is the Phase 2 persistence test: the second startup must not
	// create a duplicate node.
	dataDir := t.TempDir()
	fake := newFakeClient()

	first := newTestAgent(t, testConfig(t, dataDir), fake)
	cancel, done := runAgent(t, first)
	waitFor(t, "first registration", func() bool { return fake.registerCount() >= 1 })
	waitFor(t, "first heartbeat", func() bool { return fake.heartbeatCount() >= 1 })
	cancel()
	<-done

	if fake.registerCount() != 1 {
		t.Fatalf("expected exactly 1 registration on first run, got %d", fake.registerCount())
	}

	// Second startup against the same data dir must reuse the persisted ID.
	second := newTestAgent(t, testConfig(t, dataDir), fake)
	cancel2, done2 := runAgent(t, second)
	waitFor(t, "identity verification", func() bool { return fake.getNodeCallsCount() >= 1 })
	waitFor(t, "heartbeat after restart", func() bool { return fake.heartbeatCount() >= 2 })
	cancel2()
	<-done2

	if fake.registerCount() != 1 {
		t.Errorf("second startup must not re-register; got %d registrations", fake.registerCount())
	}
	if second.currentNodeID() != "node-1" {
		t.Errorf("expected persisted node id node-1, got %q", second.currentNodeID())
	}
}

func TestAgentSurvivesControlPlaneDowntime(t *testing.T) {
	fake := newFakeClient()
	a := newTestAgent(t, testConfig(t, t.TempDir()), fake)

	// Fail all heartbeats to simulate a control plane outage.
	fake.setHeartbeatErr(errors.New("connection refused"))

	cancel, done := runAgent(t, a)
	defer cancel()

	waitFor(t, "registration", func() bool { return fake.registerCount() >= 1 })
	waitFor(t, "failed heartbeats", func() bool { return fake.heartbeatCount() >= 3 })

	// The agent must still be alive while the control plane is down.
	select {
	case err := <-done:
		t.Fatalf("agent terminated during outage: %v", err)
	default:
	}

	// Control plane recovers; heartbeats must resume.
	fake.setHeartbeatErr(nil)
	before := fake.heartbeatCount()
	waitFor(t, "heartbeat recovery", func() bool { return fake.heartbeatCount() > before })

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not shut down")
	}
}

func TestAgentExecutesCommand(t *testing.T) {
	fake := newFakeClient()
	fake.pendingCommands = []client.Command{
		{ID: "cmd-1", Type: "GET_STATUS", Parameters: map[string]any{}},
	}
	a := newTestAgent(t, testConfig(t, t.TempDir()), fake)

	cancel, done := runAgent(t, a)
	defer cancel()

	waitFor(t, "command result", func() bool { return fake.commandResultCount() >= 1 })

	report, ok := fake.latestCommandResult()
	if !ok {
		t.Fatal("expected a command result")
	}
	if report.commandID != "cmd-1" {
		t.Errorf("expected result for cmd-1, got %q", report.commandID)
	}
	if report.result.Status != "SUCCEEDED" {
		t.Errorf("expected SUCCEEDED, got %q", report.result.Status)
	}

	cancel()
	<-done
}

// TestAgentFailsClosedWhenNodeUnknown replaces the old re-registration
// behavior (Phase 10): a node deleted from the control plane had its
// credentials revoked with it, so the agent must refuse to start rather than
// silently minting a new identity.
func TestAgentFailsClosedWhenNodeUnknown(t *testing.T) {
	dataDir := t.TempDir()
	fake := newFakeClient()

	// First run registers normally.
	first := newTestAgent(t, testConfig(t, dataDir), fake)
	cancel, done := runAgent(t, first)
	waitFor(t, "first registration", func() bool { return fake.registerCount() >= 1 })
	waitFor(t, "credential installed", func() bool { return fake.exchangeCalls >= 1 })
	cancel()
	<-done

	// Second run: the control plane no longer knows the node.
	fake.getNodeErr = client.ErrNotFound

	second := newTestAgent(t, testConfig(t, dataDir), fake)
	_, done2 := runAgent(t, second)

	select {
	case err := <-done2:
		if err == nil {
			t.Fatal("expected agent to fail closed for an unknown node")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not exit after identity loss")
	}

	if fake.registerCount() != 1 {
		t.Errorf("agent must not re-register after identity loss; got %d registrations", fake.registerCount()-1+1)
	}
}

func TestAgentRestartCommandShutsDown(t *testing.T) {
	fake := newFakeClient()
	fake.pendingCommands = []client.Command{
		{ID: "cmd-restart", Type: "RESTART_AGENT", Parameters: map[string]any{}},
	}
	a := newTestAgent(t, testConfig(t, t.TempDir()), fake)

	_, done := runAgent(t, a)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restart command did not shut down the agent")
	}

	report, ok := fake.latestCommandResult()
	if !ok {
		t.Fatal("expected a command result")
	}
	if report.result.Status != "SUCCEEDED" {
		t.Errorf("expected SUCCEEDED restart result, got %q", report.result.Status)
	}
}

func TestAgentUnknownCommandReportedFailed(t *testing.T) {
	fake := newFakeClient()
	fake.pendingCommands = []client.Command{
		{ID: "cmd-bogus", Type: "MAKE_COFFEE", Parameters: map[string]any{}},
	}
	a := newTestAgent(t, testConfig(t, t.TempDir()), fake)

	cancel, done := runAgent(t, a)
	defer cancel()

	waitFor(t, "command result", func() bool { return fake.commandResultCount() >= 1 })

	report, _ := fake.latestCommandResult()
	if report.result.Status != "FAILED" {
		t.Errorf("expected FAILED for unknown command, got %q", report.result.Status)
	}

	cancel()
	<-done
}

func TestAgentLocalHealthEndpoint(t *testing.T) {
	fake := newFakeClient()
	a := newTestAgent(t, testConfig(t, t.TempDir()), fake)

	cancel, done := runAgent(t, a)
	defer cancel()

	waitFor(t, "local API", func() bool { return a.LocalAddr() != "" })

	status, err := getLocalJSON("http://" + a.LocalAddr() + "/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	if status.StatusCode != 200 {
		t.Errorf("expected 200 health, got %d", status.StatusCode)
	}

	cancel()
	<-done
}
