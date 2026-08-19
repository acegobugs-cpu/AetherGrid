package http_test

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
	"github.com/acegobugs-cpu/AetherGrid/internal/reconcile"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository/sqlite"
	"github.com/acegobugs-cpu/AetherGrid/internal/service"
	"github.com/acegobugs-cpu/AetherGrid/migrations"
)

// testReconcileConfig is the engine configuration used by the HTTP tests. The
// engine is never started here; manual reconciliation runs synchronously.
var testReconcileConfig = reconcile.Config{
	Interval:         time.Minute,
	Workers:          2,
	HeartbeatTimeout: 30 * time.Second,
	MaxRetries:       3,
	MaxBackoff:       time.Second,
	RecoveryTimeout:  time.Minute,
}

// testApp wires the full HTTP stack against a real SQLite database in a
// temporary directory.
type testApp struct {
	server *httptest.Server
	dbPath string
	repo   *sqlite.NodeRepository
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "aether-grid-test.db")
	repo, err := sqlite.NewNodeRepository(dbPath)
	if err != nil {
		t.Fatalf("opening repository: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	if err := migrations.Apply(context.Background(), repo.DB()); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}

	logger := log.New(io.Discard, "", 0)
	commandService := service.NewCommandService(sqlite.NewCommandRepository(repo.DB()), repo)
	reconciler := service.NewReconciliationService(testReconcileConfig, repo,
		sqlite.NewReconciliationRepository(repo.DB()), commandService, logger)
	router := apihandler.NewRouter(
		service.NewNodeService(repo),
		service.NewHeartbeatService(repo),
		reconciler,
		commandService,
		logger,
	)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return &testApp{server: server, dbPath: dbPath, repo: repo}
}

// request performs an HTTP request against the test server and returns the
// response plus the decoded JSON body (any shape).
func (a *testApp) request(t *testing.T, method, path string, body any) (*http.Response, any) {
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

	var decoded any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			// Some responses (e.g. net/http's built-in 405) are plain text.
			// Leave decoded nil in that case; tests assert status codes.
			decoded = nil
		}
	}

	return resp, decoded
}

// asMap asserts the decoded body is a JSON object and returns it.
func asMap(t *testing.T, body any) map[string]any {
	t.Helper()
	object, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("expected object response, got %T", body)
	}
	return object
}

const validNodePayload = `{"name":"edge-01","location":"addis-01","ip_address":"10.0.0.10","kubernetes_enabled":false,"wireguard_enabled":true}`

func createNode(t *testing.T, app *testApp) string {
	t.Helper()
	return createNodeNamed(t, app, "edge-01")
}

func createNodeNamed(t *testing.T, app *testApp, name string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"name":               name,
		"location":           "addis-01",
		"ip_address":         "10.0.0.10",
		"kubernetes_enabled": false,
		"wireguard_enabled":  true,
	})
	if err != nil {
		t.Fatalf("marshalling payload: %v", err)
	}
	resp, body := app.request(t, http.MethodPost, "/nodes", json.RawMessage(payload))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%v)", resp.StatusCode, body)
	}
	object := asMap(t, body)
	id, _ := object["id"].(string)
	if id == "" {
		t.Fatal("expected node id in response")
	}
	return id
}

func TestCreateNode(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, body := app.request(t, http.MethodGet, "/nodes/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	object := asMap(t, body)
	if object["name"] != "edge-01" {
		t.Errorf("expected name edge-01, got %v", object["name"])
	}
	if object["status"] != "PROVISIONING" {
		t.Errorf("expected initial status PROVISIONING, got %v", object["status"])
	}
	if object["desired_status"] != "READY" {
		t.Errorf("expected desired status READY, got %v", object["desired_status"])
	}
	if object["location"] != "addis-01" {
		t.Errorf("expected location addis-01, got %v", object["location"])
	}
}

func TestCreateNodeInvalidBody(t *testing.T) {
	app := newTestApp(t)

	invalid := []struct {
		name string
		body any
	}{
		{"malformed json", "{"},
		{"empty name", map[string]any{"name": ""}},
		{"missing name", map[string]any{"location": "addis-01"}},
		{"invalid ip", map[string]any{"name": "edge-01", "ip_address": "not-an-ip"}},
		{"bad boolean", map[string]any{"name": "edge-01", "kubernetes_enabled": "yes"}},
		{"unknown field", map[string]any{"name": "edge-01", "bogus": true}},
	}

	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			resp, body := app.request(t, http.MethodPost, "/nodes", test.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (%v)", resp.StatusCode, body)
			}
			if _, ok := asMap(t, body)["error"]; !ok {
				t.Errorf("expected error key in response, got %v", body)
			}
		})
	}
}

func TestCreateNodeDuplicateNameConflict(t *testing.T) {
	app := newTestApp(t)

	createNode(t, app)

	resp, body := app.request(t, http.MethodPost, "/nodes", json.RawMessage(validNodePayload))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d (%v)", resp.StatusCode, body)
	}
}

func TestListNodesEmpty(t *testing.T) {
	app := newTestApp(t)

	resp, body := app.request(t, http.MethodGet, "/nodes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	nodes, ok := body.([]any)
	if !ok {
		t.Fatalf("expected array response, got %T", body)
	}
	if len(nodes) != 0 {
		t.Errorf("expected empty list, got %d entries", len(nodes))
	}
}

func TestListNodesWithEntries(t *testing.T) {
	app := newTestApp(t)

	createNodeNamed(t, app, "edge-01")
	createNodeNamed(t, app, "edge-02")

	resp, body := app.request(t, http.MethodGet, "/nodes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	nodes, ok := body.([]any)
	if !ok {
		t.Fatalf("expected array response, got %T", body)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestGetNodeNotFound(t *testing.T) {
	app := newTestApp(t)

	id := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	resp, _ := app.request(t, http.MethodGet, "/nodes/"+id, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGetNodeInvalidID(t *testing.T) {
	app := newTestApp(t)

	resp, _ := app.request(t, http.MethodGet, "/nodes/not-a-uuid", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestDeleteNode(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, body := app.request(t, http.MethodDelete, "/nodes/"+id, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (%v)", resp.StatusCode, body)
	}

	resp, _ = app.request(t, http.MethodGet, "/nodes/"+id, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

func TestDeleteNodeNotFound(t *testing.T) {
	app := newTestApp(t)

	id := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	resp, _ := app.request(t, http.MethodDelete, "/nodes/"+id, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHeartbeat(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	first, body := app.request(t, http.MethodPost, "/nodes/"+id+"/heartbeat", nil)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", first.StatusCode, body)
	}
	firstObject := asMap(t, body)
	if firstObject["last_heartbeat"] == nil {
		t.Fatal("expected last_heartbeat to be set")
	}

	second, body := app.request(t, http.MethodPost, "/nodes/"+id+"/heartbeat", nil)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", second.StatusCode, body)
	}
	secondObject := asMap(t, body)
	if secondObject["last_heartbeat"] == firstObject["last_heartbeat"] {
		t.Error("expected heartbeat timestamp to change")
	}
	if secondObject["desired_status"] != "READY" {
		t.Errorf("heartbeat must not change desired state, got %v", secondObject["desired_status"])
	}
}

func TestHeartbeatNotFound(t *testing.T) {
	app := newTestApp(t)

	id := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	resp, _ := app.request(t, http.MethodPost, "/nodes/"+id+"/heartbeat", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGetState(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, body := app.request(t, http.MethodGet, "/nodes/"+id+"/state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	object := asMap(t, body)
	if object["status"] != "PROVISIONING" {
		t.Errorf("expected status PROVISIONING, got %v", object["status"])
	}
}

func TestGetStateNotFound(t *testing.T) {
	app := newTestApp(t)

	id := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	resp, _ := app.request(t, http.MethodGet, "/nodes/"+id+"/state", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGetDesiredState(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, body := app.request(t, http.MethodGet, "/nodes/"+id+"/desired-state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	object := asMap(t, body)
	if object["desired_status"] != "READY" {
		t.Errorf("expected desired_status READY, got %v", object["desired_status"])
	}
}

func TestSetDesiredState(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, body := app.request(t, http.MethodPut, "/nodes/"+id+"/desired-state",
		map[string]any{"status": "PROVISIONING"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	object := asMap(t, body)
	if object["desired_status"] != "PROVISIONING" {
		t.Errorf("expected desired_status PROVISIONING, got %v", object["desired_status"])
	}
}

func TestSetDesiredStateInvalidStatus(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, body := app.request(t, http.MethodPut, "/nodes/"+id+"/desired-state",
		map[string]any{"status": "BOGUS"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%v)", resp.StatusCode, body)
	}
}

func TestReconcileDriftDetected(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, body := app.request(t, http.MethodPost, "/nodes/"+id+"/reconcile", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	object := asMap(t, body)
	if object["result"] != "DRIFT_DETECTED" {
		t.Errorf("expected DRIFT_DETECTED, got %v", object["result"])
	}
}

func TestReconcileInSync(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	// Set desired state to match the actual state (PROVISIONING).
	resp, body := app.request(t, http.MethodPut, "/nodes/"+id+"/desired-state",
		map[string]any{"status": "PROVISIONING"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setting desired state failed: %d (%v)", resp.StatusCode, body)
	}

	resp, body = app.request(t, http.MethodPost, "/nodes/"+id+"/reconcile", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	object := asMap(t, body)
	if object["result"] != "IN_SYNC" {
		t.Errorf("expected IN_SYNC, got %v", object["result"])
	}
}

func TestReconcileNotFound(t *testing.T) {
	app := newTestApp(t)

	id := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	resp, _ := app.request(t, http.MethodPost, "/nodes/"+id+"/reconcile", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	app := newTestApp(t)

	resp, _ := app.request(t, http.MethodDelete, "/nodes", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestUnknownRoute(t *testing.T) {
	app := newTestApp(t)

	resp, _ := app.request(t, http.MethodGet, "/unknown", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestSetState(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, body := app.request(t, http.MethodPut, "/nodes/"+id+"/state",
		map[string]any{"status": "READY", "ip_address": "10.0.0.20"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	object := asMap(t, body)
	if object["status"] != "READY" {
		t.Errorf("expected status READY, got %v", object["status"])
	}
	if object["last_heartbeat"] == nil {
		t.Error("expected last_heartbeat to be refreshed by a state report")
	}

	// The node's IP address must be updated too.
	nodeResp, nodeBody := app.request(t, http.MethodGet, "/nodes/"+id, nil)
	if nodeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", nodeResp.StatusCode, nodeBody)
	}
	if asMap(t, nodeBody)["ip_address"] != "10.0.0.20" {
		t.Errorf("expected ip 10.0.0.20, got %v", asMap(t, nodeBody)["ip_address"])
	}
}

func TestSetStateInvalidStatus(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, body := app.request(t, http.MethodPut, "/nodes/"+id+"/state",
		map[string]any{"status": "BOGUS"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%v)", resp.StatusCode, body)
	}
}

func TestSetStateNotFound(t *testing.T) {
	app := newTestApp(t)

	id := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	resp, _ := app.request(t, http.MethodPut, "/nodes/"+id+"/state",
		map[string]any{"status": "READY"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestCreateCommand(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, body := app.request(t, http.MethodPost, "/nodes/"+id+"/commands",
		map[string]any{"type": "GET_STATUS", "parameters": map[string]any{"detail": "full"}})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%v)", resp.StatusCode, body)
	}
	object := asMap(t, body)
	commandID, _ := object["id"].(string)
	if commandID == "" {
		t.Fatal("expected command id in response")
	}
	if object["type"] != "GET_STATUS" {
		t.Errorf("expected type GET_STATUS, got %v", object["type"])
	}
	if object["status"] != "PENDING" {
		t.Errorf("expected status PENDING, got %v", object["status"])
	}
}

func TestCreateCommandMissingType(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, body := app.request(t, http.MethodPost, "/nodes/"+id+"/commands",
		map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%v)", resp.StatusCode, body)
	}
}

func TestCreateCommandUnknownNode(t *testing.T) {
	app := newTestApp(t)

	id := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	resp, _ := app.request(t, http.MethodPost, "/nodes/"+id+"/commands",
		map[string]any{"type": "GET_STATUS"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestListCommands(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	created, body := app.request(t, http.MethodPost, "/nodes/"+id+"/commands",
		map[string]any{"type": "GET_STATUS"})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("creating command failed: %d (%v)", created.StatusCode, body)
	}
	commandID := asMap(t, body)["id"].(string)

	// A PENDING filter returns the command.
	resp, body := app.request(t, http.MethodGet, "/nodes/"+id+"/commands?status=pending", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	commands, ok := body.([]any)
	if !ok {
		t.Fatalf("expected array response, got %T", body)
	}
	if len(commands) != 1 {
		t.Fatalf("expected 1 pending command, got %d", len(commands))
	}

	// Report a result, then verify it is no longer pending.
	resultResp, resultBody := app.request(t, http.MethodPost,
		"/nodes/"+id+"/commands/"+commandID+"/result",
		map[string]any{"status": "SUCCEEDED", "result": map[string]any{"hostname": "edge-01"}})
	if resultResp.StatusCode != http.StatusOK {
		t.Fatalf("reporting result failed: %d (%v)", resultResp.StatusCode, resultBody)
	}
	if asMap(t, resultBody)["status"] != "SUCCEEDED" {
		t.Errorf("expected status SUCCEEDED, got %v", asMap(t, resultBody)["status"])
	}

	resp, body = app.request(t, http.MethodGet, "/nodes/"+id+"/commands?status=pending", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	commands = body.([]any)
	if len(commands) != 0 {
		t.Errorf("expected 0 pending commands, got %d", len(commands))
	}
}

func TestListCommandsAllStatuses(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	if resp, body := app.request(t, http.MethodPost, "/nodes/"+id+"/commands",
		map[string]any{"type": "GET_STATUS"}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating command failed: %d (%v)", resp.StatusCode, body)
	}

	resp, body := app.request(t, http.MethodGet, "/nodes/"+id+"/commands", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	if commands, ok := body.([]any); !ok || len(commands) != 1 {
		t.Fatalf("expected 1 command without filter, got %v", body)
	}
}

func TestReportCommandResultCrossNodeRejected(t *testing.T) {
	app := newTestApp(t)

	firstID := createNodeNamed(t, app, "edge-01")
	secondID := createNodeNamed(t, app, "edge-02")

	created, body := app.request(t, http.MethodPost, "/nodes/"+firstID+"/commands",
		map[string]any{"type": "GET_STATUS"})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("creating command failed: %d (%v)", created.StatusCode, body)
	}
	commandID := asMap(t, body)["id"].(string)

	// Reporting a result for edge-02's node path must be rejected.
	resp, _ := app.request(t, http.MethodPost, "/nodes/"+secondID+"/commands/"+commandID+"/result",
		map[string]any{"status": "SUCCEEDED"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-node result, got %d", resp.StatusCode)
	}
}

func TestReportCommandResultInvalidCommandID(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, _ := app.request(t, http.MethodPost, "/nodes/"+id+"/commands/not-a-uuid/result",
		map[string]any{"status": "SUCCEEDED"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestReconcileStructuredResult(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	resp, body := app.request(t, http.MethodPost, "/nodes/"+id+"/reconcile", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	object := asMap(t, body)
	if object["result"] != "DRIFT_DETECTED" {
		t.Fatalf("expected DRIFT_DETECTED, got %v", object["result"])
	}
	if _, ok := object["desired_state"].(map[string]any); !ok {
		t.Errorf("expected structured desired_state, got %v", object["desired_state"])
	}
	if _, ok := object["actual_state"].(map[string]any); !ok {
		t.Errorf("expected structured actual_state, got %v", object["actual_state"])
	}
	if _, ok := object["differences"].([]any); !ok {
		t.Errorf("expected differences array, got %v", object["differences"])
	}
}

func TestReconciliationStateAndHistory(t *testing.T) {
	app := newTestApp(t)

	id := createNode(t, app)

	// Run a manual reconcile to persist metadata and history.
	if resp, body := app.request(t, http.MethodPost, "/nodes/"+id+"/reconcile", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("reconcile failed: %d (%v)", resp.StatusCode, body)
	}

	// GET /nodes/{id}/reconciliation reports the last result.
	resp, body := app.request(t, http.MethodGet, "/nodes/"+id+"/reconciliation", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	object := asMap(t, body)
	if object["result"] != "DRIFT_DETECTED" {
		t.Errorf("expected DRIFT_DETECTED state, got %v", object["result"])
	}
	if object["last_reconciliation"] == nil {
		t.Error("expected last_reconciliation timestamp")
	}

	// GET /nodes/{id}/reconciliation/history returns persisted events.
	resp, body = app.request(t, http.MethodGet, "/nodes/"+id+"/reconciliation/history", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	events, ok := body.([]any)
	if !ok {
		t.Fatalf("expected array response, got %T", body)
	}
	if len(events) < 1 {
		t.Fatalf("expected at least 1 history row, got %d", len(events))
	}
	if asMap(t, events[0])["result"] != "DRIFT_DETECTED" {
		t.Errorf("expected DRIFT_DETECTED history row, got %v", events[0])
	}
}

func TestReconciliationHistoryNotFound(t *testing.T) {
	app := newTestApp(t)

	id := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	resp, _ := app.request(t, http.MethodGet, "/nodes/"+id+"/reconciliation", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestReconciliationStatusEndpoint(t *testing.T) {
	app := newTestApp(t)

	resp, body := app.request(t, http.MethodGet, "/reconciliation/status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	object := asMap(t, body)
	if _, ok := object["pending_work"]; !ok {
		t.Errorf("expected pending_work counter, got %v", object)
	}
	if _, ok := object["nodes_reconciled"]; !ok {
		t.Errorf("expected nodes_reconciled counter, got %v", object)
	}
}

// TestKubernetesStateEndpoint verifies GET /nodes/{id}/kubernetes returns the
// declared desired Kubernetes state and the most recent agent-reported
// Kubernetes state.
func TestKubernetesStateEndpoint(t *testing.T) {
	app := newTestApp(t)
	id := createNode(t, app)

	// Declare the Kubernetes expectation via desired-state.
	resp, body := app.request(t, http.MethodPut, "/nodes/"+id+"/desired-state",
		map[string]any{"kubernetes": map[string]any{"enabled": true, "minimum_ready_nodes": 1}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setting desired state failed: %d (%v)", resp.StatusCode, body)
	}

	// Report observed Kubernetes state via the agent state endpoint.
	report, _ := app.request(t, http.MethodPut, "/nodes/"+id+"/state", map[string]any{
		"status": "READY",
		"kubernetes": map[string]any{
			"available":       true,
			"status":          "DEGRADED",
			"version":         "v1.31.0",
			"node_count":      2,
			"ready_nodes":     1,
			"not_ready_nodes": 1,
			"workload":        map[string]any{"total_pods": 5, "running_pods": 4, "failed_pods": 1},
		},
	})
	if report.StatusCode != http.StatusOK {
		t.Fatalf("reporting state failed: %d", report.StatusCode)
	}

	resp, body = app.request(t, http.MethodGet, "/nodes/"+id+"/kubernetes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	object := asMap(t, body)
	desired := asMap(t, object["desired"])
	if desired["enabled"] != true || desired["minimum_ready_nodes"] != float64(1) {
		t.Errorf("unexpected desired kubernetes: %v", desired)
	}
	state := asMap(t, object["state"])
	if state["status"] != "DEGRADED" || state["available"] != true {
		t.Errorf("unexpected observed kubernetes: %v", state)
	}
	if state["node_count"] != float64(2) || state["ready_nodes"] != float64(1) {
		t.Errorf("unexpected node counts: %v", state)
	}
	workload := asMap(t, state["workload"])
	if workload["total_pods"] != float64(5) {
		t.Errorf("unexpected workload: %v", workload)
	}
}

// TestKubernetesListNodesDispatchesCommand verifies GET /nodes/{id}/kubernetes/nodes
// answers 202 Accepted with a LIST_KUBERNETES_NODES command the caller can poll.
func TestKubernetesListNodesDispatchesCommand(t *testing.T) {
	app := newTestApp(t)
	id := createNode(t, app)

	resp, body := app.request(t, http.MethodGet, "/nodes/"+id+"/kubernetes/nodes", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%v)", resp.StatusCode, body)
	}
	command := asMap(t, body)
	if command["type"] != "LIST_KUBERNETES_NODES" {
		t.Errorf("expected LIST_KUBERNETES_NODES command, got %v", command["type"])
	}
	if command["status"] != "PENDING" {
		t.Errorf("expected PENDING command, got %v", command["status"])
	}
}

// TestKubernetesListPodsNamespace verifies the namespace query parameter is
// forwarded as a command parameter.
func TestKubernetesListPodsNamespace(t *testing.T) {
	app := newTestApp(t)
	id := createNode(t, app)

	resp, body := app.request(t, http.MethodGet, "/nodes/"+id+"/kubernetes/pods?namespace=default", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%v)", resp.StatusCode, body)
	}
	command := asMap(t, body)
	if command["type"] != "LIST_KUBERNETES_PODS" {
		t.Errorf("expected LIST_KUBERNETES_PODS command, got %v", command["type"])
	}
	parameters := asMap(t, command["parameters"])
	if parameters["namespace"] != "default" {
		t.Errorf("expected namespace parameter, got %v", parameters)
	}
}

// TestKubernetesEndpointsNotFound verifies the endpoints 404 for unknown nodes.
func TestKubernetesEndpointsNotFound(t *testing.T) {
	app := newTestApp(t)
	id := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	for _, path := range []string{
		"/nodes/" + id + "/kubernetes",
		"/nodes/" + id + "/kubernetes/nodes",
		"/nodes/" + id + "/kubernetes/pods",
	} {
		resp, _ := app.request(t, http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404 for %s, got %d", path, resp.StatusCode)
		}
	}
}

// TestReconcileKubernetesDrift implements the Phase 4 spec section 76 scenario:
// an unhealthy Kubernetes node produces AETHER-GRID DRIFT_DETECTED with a
// structured kubernetes difference, and restoring readiness returns IN_SYNC.
func TestReconcileKubernetesDrift(t *testing.T) {
	app := newTestApp(t)
	id := createNode(t, app)

	// Bring the node to READY and declare the Kubernetes expectation.
	readyResp, _ := app.request(t, http.MethodPut, "/nodes/"+id+"/state", map[string]any{"status": "READY"})
	if readyResp.StatusCode != http.StatusOK {
		t.Fatalf("bringing node to READY failed: %d", readyResp.StatusCode)
	}
	desiredResp, _ := app.request(t, http.MethodPut, "/nodes/"+id+"/desired-state",
		map[string]any{"status": "READY", "kubernetes": map[string]any{"enabled": true, "minimum_ready_nodes": 1}})
	if desiredResp.StatusCode != http.StatusOK {
		t.Fatalf("setting desired state failed: %d", desiredResp.StatusCode)
	}

	// Report a DEGRADED cluster: available but no Ready node.
	report, _ := app.request(t, http.MethodPut, "/nodes/"+id+"/state", map[string]any{
		"status": "READY",
		"kubernetes": map[string]any{
			"available":       true,
			"status":          "DEGRADED",
			"version":         "v1.31.0",
			"node_count":      2,
			"ready_nodes":     0,
			"not_ready_nodes": 2,
		},
	})
	if report.StatusCode != http.StatusOK {
		t.Fatalf("reporting degraded kubernetes failed: %d", report.StatusCode)
	}

	reconcileResp, reconcileBody := app.request(t, http.MethodPost, "/nodes/"+id+"/reconcile", nil)
	if reconcileResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", reconcileResp.StatusCode, reconcileBody)
	}
	object := asMap(t, reconcileBody)
	if object["result"] != "DRIFT_DETECTED" {
		t.Fatalf("expected DRIFT_DETECTED, got %v", object["result"])
	}
	differences, ok := object["differences"].([]any)
	if !ok || len(differences) != 1 {
		t.Fatalf("expected 1 difference, got %v", object["differences"])
	}
	diff := asMap(t, differences[0])
	if diff["field"] != "kubernetes.ready_nodes" {
		t.Fatalf("expected kubernetes.ready_nodes difference, got %v", diff)
	}
	if diff["desired"] != float64(1) || diff["actual"] != float64(0) {
		t.Errorf("unexpected difference values: %v", diff)
	}

	// Restore readiness: the cluster reports its Ready node again.
	report, _ = app.request(t, http.MethodPut, "/nodes/"+id+"/state", map[string]any{
		"status": "READY",
		"kubernetes": map[string]any{
			"available":       true,
			"status":          "READY",
			"version":         "v1.31.0",
			"node_count":      2,
			"ready_nodes":     2,
			"not_ready_nodes": 0,
		},
	})
	if report.StatusCode != http.StatusOK {
		t.Fatalf("reporting restored kubernetes failed: %d", report.StatusCode)
	}

	syncResp, syncBody := app.request(t, http.MethodPost, "/nodes/"+id+"/reconcile", nil)
	if syncResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", syncResp.StatusCode, syncBody)
	}
	if asMap(t, syncBody)["result"] != "IN_SYNC" {
		t.Errorf("expected IN_SYNC after restore, got %v", syncBody)
	}
}

// TestReconcileKubernetesUnavailable verifies the spec section 22 scenario:
// desired enabled while the cluster is unavailable is surfaced as a
// kubernetes.available drift.
func TestReconcileKubernetesUnavailable(t *testing.T) {
	app := newTestApp(t)
	id := createNode(t, app)

	_, _ = app.request(t, http.MethodPut, "/nodes/"+id+"/state", map[string]any{"status": "READY"})
	_, _ = app.request(t, http.MethodPut, "/nodes/"+id+"/desired-state",
		map[string]any{"status": "READY", "kubernetes": map[string]any{"enabled": true, "minimum_ready_nodes": 1}})

	report, _ := app.request(t, http.MethodPut, "/nodes/"+id+"/state", map[string]any{
		"status": "READY",
		"kubernetes": map[string]any{
			"available":       false,
			"status":          "UNAVAILABLE",
			"node_count":      0,
			"ready_nodes":     0,
			"not_ready_nodes": 0,
		},
	})
	if report.StatusCode != http.StatusOK {
		t.Fatalf("reporting unavailable kubernetes failed: %d", report.StatusCode)
	}

	_, reconcileBody := app.request(t, http.MethodPost, "/nodes/"+id+"/reconcile", nil)
	object := asMap(t, reconcileBody)
	if object["result"] != "DRIFT_DETECTED" {
		t.Fatalf("expected DRIFT_DETECTED, got %v", object["result"])
	}
	differences := object["differences"].([]any)
	diff := asMap(t, differences[0])
	if diff["field"] != "kubernetes.available" {
		t.Fatalf("expected kubernetes.available difference, got %v", diff)
	}
}
