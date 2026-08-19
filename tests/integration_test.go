// Package integration_test contains the end-to-end test for the Phase 1
// control plane. It exercises the complete system through the HTTP API
// against a real SQLite database, including a simulated restart.
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

	apihandler "github.com/acegobugs-cpu/AetherGrid/internal/http"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository/sqlite"
	"github.com/acegobugs-cpu/AetherGrid/internal/service"
	"github.com/acegobugs-cpu/AetherGrid/migrations"
)

// appInstance represents one running control plane instance backed by a
// specific database file.
type appInstance struct {
	server *httptest.Server
	repo   *sqlite.NodeRepository
	dbPath string
}

// startApp boots a control plane instance on the given database path.
func startApp(t *testing.T, dbPath string) *appInstance {
	t.Helper()

	repo, err := sqlite.NewNodeRepository(dbPath)
	if err != nil {
		t.Fatalf("opening repository: %v", err)
	}
	if err := migrations.Apply(context.Background(), repo.DB()); err != nil {
		repo.Close()
		t.Fatalf("applying migrations: %v", err)
	}

	logger := log.New(io.Discard, "", 0)
	router := apihandler.NewRouter(
		service.NewNodeService(repo),
		service.NewHeartbeatService(repo),
		service.NewReconciliationService(repo),
		logger,
	)
	server := httptest.NewServer(router)

	return &appInstance{server: server, repo: repo, dbPath: dbPath}
}

// stop simulates a process shutdown: the HTTP server stops listening and the
// database connection is closed.
func (a *appInstance) stop() {
	a.server.Close()
	a.repo.Close()
}

// request performs an HTTP request against the running instance.
func (a *appInstance) request(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
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

	resp, err := a.server.Client().Do(req)
	if err != nil {
		t.Fatalf("executing request: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	var decoded map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decoding response %q: %v", raw, err)
		}
	}
	return resp, decoded
}

func TestControlPlaneLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aether-grid.db")

	// 1. Start the application.
	app := startApp(t, dbPath)
	t.Cleanup(app.stop)

	// 2. Create a node.
	createResp, createBody := app.request(t, http.MethodPost, "/nodes", map[string]any{
		"name":               "edge-01",
		"location":           "addis-01",
		"ip_address":         "10.0.0.10",
		"kubernetes_enabled": true,
		"wireguard_enabled":  true,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("step 2: expected 201, got %d (%v)", createResp.StatusCode, createBody)
	}
	nodeID := createBody["id"].(string)
	if nodeID == "" {
		t.Fatal("step 2: expected node id in response")
	}

	// 3. Verify the node was persisted by reading it back through the
	// repository directly.
	persisted, err := app.repo.GetByID(context.Background(), nodeID)
	if err != nil {
		t.Fatalf("step 3: node not persisted: %v", err)
	}
	if persisted.Name != "edge-01" {
		t.Fatalf("step 3: expected name edge-01, got %q", persisted.Name)
	}

	// 4. Retrieve the node through the API.
	getResp, getBody := app.request(t, http.MethodGet, "/nodes/"+nodeID, nil)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("step 4: expected 200, got %d (%v)", getResp.StatusCode, getBody)
	}

	// 5. Send a heartbeat.
	firstBeat, heartbeatBody := app.request(t, http.MethodPost, "/nodes/"+nodeID+"/heartbeat", nil)
	if firstBeat.StatusCode != http.StatusOK {
		t.Fatalf("step 5: expected 200, got %d (%v)", firstBeat.StatusCode, heartbeatBody)
	}
	firstBeatTime := heartbeatBody["last_heartbeat"]
	if firstBeatTime == nil {
		t.Fatal("step 5: expected last_heartbeat to be set")
	}

	// 6. Verify the heartbeat timestamp changed after another heartbeat.
	time.Sleep(5 * time.Millisecond)
	secondBeat, heartbeatBody := app.request(t, http.MethodPost, "/nodes/"+nodeID+"/heartbeat", nil)
	if secondBeat.StatusCode != http.StatusOK {
		t.Fatalf("step 6: expected 200, got %d (%v)", secondBeat.StatusCode, heartbeatBody)
	}
	if heartbeatBody["last_heartbeat"] == firstBeatTime {
		t.Fatal("step 6: expected heartbeat timestamp to change")
	}

	// 7. Read the desired state, then set it.
	desiredResp, desiredBody := app.request(t, http.MethodGet, "/nodes/"+nodeID+"/desired-state", nil)
	if desiredResp.StatusCode != http.StatusOK {
		t.Fatalf("step 7a: expected 200, got %d (%v)", desiredResp.StatusCode, desiredBody)
	}
	if desiredBody["desired_status"] != "READY" {
		t.Fatalf("step 7a: expected desired_status READY, got %v", desiredBody["desired_status"])
	}

	setDesiredResp, setDesiredBody := app.request(t, http.MethodPut, "/nodes/"+nodeID+"/desired-state",
		map[string]any{"status": "READY"})
	if setDesiredResp.StatusCode != http.StatusOK {
		t.Fatalf("step 7b: setting desired state failed: %d (%v)", setDesiredResp.StatusCode, setDesiredBody)
	}

	// 8. Compare desired vs actual state.
	// The node is still PROVISIONING while desired is READY.
	stateResp, stateBody := app.request(t, http.MethodGet, "/nodes/"+nodeID+"/state", nil)
	if stateResp.StatusCode != http.StatusOK {
		t.Fatalf("step 8: expected 200, got %d (%v)", stateResp.StatusCode, stateBody)
	}
	if stateBody["status"] != "PROVISIONING" {
		t.Fatalf("step 8: expected actual status PROVISIONING, got %v", stateBody["status"])
	}

	// 9. Detect drift.
	reconcileResp, reconcileBody := app.request(t, http.MethodPost, "/nodes/"+nodeID+"/reconcile", nil)
	if reconcileResp.StatusCode != http.StatusOK {
		t.Fatalf("step 9: expected 200, got %d (%v)", reconcileResp.StatusCode, reconcileBody)
	}
	if reconcileBody["result"] != "DRIFT_DETECTED" {
		t.Fatalf("step 9: expected DRIFT_DETECTED, got %v", reconcileBody["result"])
	}
	if reconcileBody["desired_state"] != "READY" || reconcileBody["actual_state"] != "PROVISIONING" {
		t.Fatalf("step 9: unexpected comparison: %v", reconcileBody)
	}

	// Also verify an in-sync result by matching desired to actual.
	if _, setBody := app.request(t, http.MethodPut, "/nodes/"+nodeID+"/desired-state",
		map[string]any{"status": "PROVISIONING"}); setBody["desired_status"] != "PROVISIONING" {
		t.Fatalf("step 9b: failed to set matching desired state: %v", setBody)
	}
	_, syncBody := app.request(t, http.MethodPost, "/nodes/"+nodeID+"/reconcile", nil)
	if syncBody["result"] != "IN_SYNC" {
		t.Fatalf("step 9b: expected IN_SYNC, got %v", syncBody["result"])
	}

	// 10. Restart the application against the same database.
	app.stop()
	app = startApp(t, dbPath)
	t.Cleanup(app.stop)

	// 11. Verify the node still exists after restart.
	afterRestart, afterRestartBody := app.request(t, http.MethodGet, "/nodes/"+nodeID, nil)
	if afterRestart.StatusCode != http.StatusOK {
		t.Fatalf("step 11: expected 200 after restart, got %d (%v)", afterRestart.StatusCode, afterRestartBody)
	}
	if afterRestartBody["name"] != "edge-01" {
		t.Fatalf("step 11: expected name edge-01 after restart, got %v", afterRestartBody["name"])
	}

	// 12. Delete the node.
	deleteResp, deleteBody := app.request(t, http.MethodDelete, "/nodes/"+nodeID, nil)
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("step 12: expected 204, got %d (%v)", deleteResp.StatusCode, deleteBody)
	}

	// 13. Verify the node is gone.
	missingResp, missingBody := app.request(t, http.MethodGet, "/nodes/"+nodeID, nil)
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("step 13: expected 404, got %d (%v)", missingResp.StatusCode, missingBody)
	}
	if missingBody["error"] == "" {
		t.Fatal("step 13: expected an error message")
	}
}
