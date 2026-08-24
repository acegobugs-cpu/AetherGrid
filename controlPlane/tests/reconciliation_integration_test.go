// Package integration_test contains the Phase 3 end-to-end reconciliation
// scenario. It drives the full system through the HTTP API against a real
// SQLite database with the reconciliation engine running, simulating a node
// going offline and recovering.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"AetherGrid/controlPlane/internal/audit"
	"AetherGrid/controlPlane/internal/auth"
	apihandler "AetherGrid/controlPlane/internal/http"
	"AetherGrid/controlPlane/internal/provisioning"
	"AetherGrid/controlPlane/internal/reconcile"
	"AetherGrid/controlPlane/internal/repository/sqlite"
	"AetherGrid/controlPlane/internal/service"
	"AetherGrid/controlPlane/migrations"
)

// Test-only static API key (fake development credential). Declared in
// integration_test.go and shared across this package's test files.
const testReconcileAdminKey = "reconcile-admin-key-001"

// reconciliationApp is a control-plane instance with the reconciliation engine
// running.
type reconciliationApp struct {
	server     *httptest.Server
	repo       *sqlite.NodeRepository
	reconciler *service.ReconciliationService
}

func startReconciliationApp(t *testing.T, cfg reconcile.Config) *reconciliationApp {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "aether-grid-reconcile.db")
	repo, err := sqlite.NewNodeRepository(dbPath)
	if err != nil {
		t.Fatalf("opening repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	if err := migrations.Apply(context.Background(), repo.DB()); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}

	logger := log.New(io.Discard, "", 0)
	commandService := service.NewCommandService(sqlite.NewCommandRepository(repo.DB()), repo)
	reconciler := service.NewReconciliationService(cfg, repo,
		sqlite.NewReconciliationRepository(repo.DB()), commandService, logger, nil)

	nodeService := service.NewNodeService(repo)
	heartbeatService := service.NewHeartbeatService(repo)
	nodeService.SetReconcileNotifier(reconciler.Notify)
	heartbeatService.SetReconcileNotifier(reconciler.Notify)

	infraRepo := sqlite.NewInfrastructureRepository(repo.DB())
	infrastructureService := service.NewInfrastructureService(
		infraRepo,
		infraRepo,
		&stubProvisioner{},
		&provisioning.Metrics{},
		logger,
	)
	clusterService := service.NewClusterService(
		sqlite.NewClusterRepository(repo.DB()),
		sqlite.NewClusterOperationRepository(repo.DB()),
		repo,
		nil,
		logger,
	)

	staticKeys, err := auth.NewStaticKeyStore([]string{testReconcileAdminKey + ":admin"})
	if err != nil {
		t.Fatalf("building static key store: %v", err)
	}
	router := apihandler.NewRouter(
		apihandler.Services{
			Nodes:           nodeService,
			Heartbeats:      heartbeatService,
			Reconciler:      reconciler,
			Commands:        commandService,
			Infrastructures: infrastructureService,
			Clusters:        clusterService,
		},
		apihandler.Security{
			Credentials: auth.NewService(sqlite.NewCredentialRepository(repo.DB())),
			StaticKeys:  staticKeys,
			Auditor:     audit.NewLogger(logger, sqlite.NewAuditRepository(repo.DB())),
		},
		logger,
	)
	server := httptest.NewServer(router)

	reconciler.Start()
	t.Cleanup(reconciler.Stop)
	t.Cleanup(server.Close)

	return &reconciliationApp{server: server, repo: repo, reconciler: reconciler}
}

func (a *reconciliationApp) request(t *testing.T, method, path string, body any) (*http.Response, any) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshalling request body: %v", err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequest(method, a.server.URL+path, reader)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+testReconcileAdminKey)

	resp, err := a.server.Client().Do(req)
	if err != nil {
		t.Fatalf("executing request: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	var decoded any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decoding response %q: %v", raw, err)
		}
	}
	return resp, decoded
}

// TestReconciliationScenario is the Phase 3 integration scenario:
//
//	register -> READY -> IN_SYNC -> heartbeats stop -> OFFLINE detected
//	-> recovery dispatched -> node reaches READY again -> heartbeat resumes
//	-> IN_SYNC.
func TestReconciliationScenario(t *testing.T) {
	app := startReconciliationApp(t, reconcile.Config{
		Interval:         50 * time.Millisecond,
		Workers:          2,
		HeartbeatTimeout: 500 * time.Millisecond,
		MaxRetries:       3,
		MaxBackoff:       20 * time.Millisecond,
		RecoveryTimeout:  5 * time.Second,
	})

	// 1. Register a node.
	createResp, createBody := app.request(t, http.MethodPost, "/nodes", map[string]any{
		"name": "edge-01", "location": "addis-01", "ip_address": "10.0.0.10",
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("step 1: expected 201, got %d (%v)", createResp.StatusCode, createBody)
	}
	nodeID := asStringMap(t, createBody)["id"].(string)

	// 2. Send a heartbeat so the node is considered healthy.
	if resp, body := app.request(t, http.MethodPost, "/nodes/"+nodeID+"/heartbeat", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("step 2: expected 200, got %d (%v)", resp.StatusCode, body)
	}

	// 3. Node reports READY.
	if resp, body := app.request(t, http.MethodPut, "/nodes/"+nodeID+"/state",
		map[string]any{"status": "READY"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("step 3: expected 200, got %d (%v)", resp.StatusCode, body)
	}

	// 4. Desired state is READY by default, so manual reconcile is IN_SYNC.
	resp, body := app.request(t, http.MethodPost, "/nodes/"+nodeID+"/reconcile", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("step 4: expected 200, got %d (%v)", resp.StatusCode, body)
	}
	if asStringMap(t, body)["result"] != "IN_SYNC" {
		t.Fatalf("step 4: expected IN_SYNC, got %v", body)
	}

	// 5. Stop heartbeats. The periodic sweep must flag the node OFFLINE and
	// dispatch a recovery (RESTART_AGENT command).
	deadline := time.Now().Add(5 * time.Second)
	var result string
	for time.Now().Before(deadline) {
		if resp, body := app.request(t, http.MethodGet, "/nodes/"+nodeID+"/reconciliation", nil); resp.StatusCode == http.StatusOK {
			result = asStringMap(t, body)["result"].(string)
			if result == "RECONCILING" || result == "RECONCILED" {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if result == "" {
		t.Fatal("step 5: never observed a reconciliation attempt")
	}

	// 6. A recovery command must have been dispatched.
	cmdResp, cmdBody := app.request(t, http.MethodGet, "/nodes/"+nodeID+"/commands", nil)
	if cmdResp.StatusCode != http.StatusOK {
		t.Fatalf("step 6: expected 200, got %d (%v)", cmdResp.StatusCode, cmdBody)
	}
	commands, ok := cmdBody.([]any)
	if !ok || len(commands) == 0 {
		t.Fatalf("step 6: expected at least one command, got %v", cmdBody)
	}
	if asStringMap(t, commands[0])["type"] != "RESTART_AGENT" {
		t.Fatalf("step 6: expected a RESTART_AGENT recovery command, got %v", commands[0])
	}

	// 7. Simulate the agent recovering: the node reports READY and heartbeats
	// resume.
	if resp, body := app.request(t, http.MethodPut, "/nodes/"+nodeID+"/state",
		map[string]any{"status": "READY"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("step 7: expected 200, got %d (%v)", resp.StatusCode, body)
	}
	for i := 0; i < 5; i++ {
		if resp, body := app.request(t, http.MethodPost, "/nodes/"+nodeID+"/heartbeat", nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("step 7: heartbeat failed: %d (%v)", resp.StatusCode, body)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 8. The engine converges the node back to IN_SYNC.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, body := app.request(t, http.MethodPost, "/nodes/"+nodeID+"/reconcile", nil)
		if resp.StatusCode == http.StatusOK && asStringMap(t, body)["result"] == "IN_SYNC" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("step 8: node never converged back to IN_SYNC")
}

// asStringMap asserts the decoded value is a JSON object.
func asStringMap(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected object response, got %T", value)
	}
	return object
}
