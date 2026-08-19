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

	apihandler "github.com/acegobugs-cpu/AetherGrid/internal/http"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository/sqlite"
	"github.com/acegobugs-cpu/AetherGrid/internal/service"
	"github.com/acegobugs-cpu/AetherGrid/migrations"
)

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
	router := apihandler.NewRouter(
		service.NewNodeService(repo),
		service.NewHeartbeatService(repo),
		service.NewReconciliationService(repo),
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

const validNodePayload = `{"name":"edge-01","location":"addis-01","ip_address":"10.0.0.10","kubernetes_enabled":true,"wireguard_enabled":true}`

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
		"kubernetes_enabled": true,
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
