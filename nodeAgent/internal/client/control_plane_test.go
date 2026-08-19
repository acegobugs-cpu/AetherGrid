package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// requestRecorder records the last request seen by the mock control plane.
type requestRecorder struct {
	method string
	path   string
	query  string
	body   string
	header http.Header
}

// mockServer is a test control plane that returns a canned response and
// records the requests it receives.
type mockServer struct {
	server  *httptest.Server
	status  int
	payload string
	rec     *requestRecorder
}

func newMockServer(t *testing.T, status int, payload string) *mockServer {
	t.Helper()
	rec := &requestRecorder{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.query = r.URL.RawQuery
		rec.body = string(body)
		rec.header = r.Header.Clone()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if payload != "" {
			_, _ = w.Write([]byte(payload))
		}
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &mockServer{server: server, status: status, payload: payload, rec: rec}
}

func newTestClient(t *testing.T, server *mockServer) *Client {
	t.Helper()
	return New(server.server.URL, WithHTTPClient(server.server.Client()))
}

func TestClientRegister(t *testing.T) {
	mock := newMockServer(t, http.StatusCreated,
		`{"id":"node-1","name":"edge-01","status":"PROVISIONING","desired_status":"READY"}`)
	client := newTestClient(t, mock)

	result, err := client.Register(context.Background(), RegisterInput{
		Name:              "edge-01",
		Location:          "addis-01",
		IPAddress:         "10.0.0.10",
		KubernetesEnabled: false,
		WireGuardEnabled:  false,
	})
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	if mock.rec.method != http.MethodPost {
		t.Errorf("expected POST, got %s", mock.rec.method)
	}
	if mock.rec.path != "/nodes" {
		t.Errorf("expected /nodes, got %s", mock.rec.path)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(mock.rec.body), &body); err != nil {
		t.Fatalf("unmarshalling request body %q: %v", mock.rec.body, err)
	}
	if body["name"] != "edge-01" {
		t.Errorf("expected name edge-01 in body, got %v", body["name"])
	}
	if body["kubernetes_enabled"] != false || body["wireguard_enabled"] != false {
		t.Errorf("expected kubernetes/wireguard disabled in Phase 2, got %v", body)
	}

	if result.NodeID != "node-1" {
		t.Errorf("expected node id node-1, got %q", result.NodeID)
	}
	if result.Status != "PROVISIONING" {
		t.Errorf("expected status PROVISIONING, got %q", result.Status)
	}
}

func TestClientHeartbeat(t *testing.T) {
	mock := newMockServer(t, http.StatusOK,
		`{"id":"node-1","name":"edge-01","status":"READY","last_heartbeat":"2026-08-19T00:00:00Z"}`)
	client := newTestClient(t, mock)

	if err := client.Heartbeat(context.Background(), "node-1"); err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	if mock.rec.method != http.MethodPost {
		t.Errorf("expected POST, got %s", mock.rec.method)
	}
	if mock.rec.path != "/nodes/node-1/heartbeat" {
		t.Errorf("expected heartbeat path, got %s", mock.rec.path)
	}
}

func TestClientGetNode(t *testing.T) {
	mock := newMockServer(t, http.StatusOK,
		`{"id":"node-1","name":"edge-01","status":"READY","desired_status":"READY","location":"addis-01","ip_address":"10.0.0.10","kubernetes_enabled":false,"wireguard_enabled":false}`)
	client := newTestClient(t, mock)

	info, err := client.GetNode(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("get node failed: %v", err)
	}
	if mock.rec.method != http.MethodGet {
		t.Errorf("expected GET, got %s", mock.rec.method)
	}
	if mock.rec.path != "/nodes/node-1" {
		t.Errorf("expected /nodes/node-1, got %s", mock.rec.path)
	}
	if info.ID != "node-1" || info.Name != "edge-01" || info.Status != "READY" {
		t.Errorf("unexpected node info: %+v", info)
	}
}

func TestClientGetNodeNotFound(t *testing.T) {
	mock := newMockServer(t, http.StatusNotFound, `{"error":"node not found"}`)
	client := newTestClient(t, mock)

	_, err := client.GetNode(context.Background(), "unknown")
	if !IsNotFound(err) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestClientReportState(t *testing.T) {
	mock := newMockServer(t, http.StatusOK,
		`{"node_id":"node-1","status":"READY","last_heartbeat":"2026-08-19T00:00:00Z"}`)
	client := newTestClient(t, mock)

	report := StateReport{Status: "READY", IPAddress: "10.0.0.10"}
	if err := client.ReportState(context.Background(), "node-1", report); err != nil {
		t.Fatalf("report state failed: %v", err)
	}

	if mock.rec.method != http.MethodPut {
		t.Errorf("expected PUT, got %s", mock.rec.method)
	}
	if mock.rec.path != "/nodes/node-1/state" {
		t.Errorf("expected /nodes/node-1/state, got %s", mock.rec.path)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(mock.rec.body), &body); err != nil {
		t.Fatalf("unmarshalling request body %q: %v", mock.rec.body, err)
	}
	if body["status"] != "READY" || body["ip_address"] != "10.0.0.10" {
		t.Errorf("unexpected state payload: %v", body)
	}
}

func TestClientGetDesiredState(t *testing.T) {
	mock := newMockServer(t, http.StatusOK, `{"node_id":"node-1","desired_status":"READY"}`)
	client := newTestClient(t, mock)

	desired, err := client.GetDesiredState(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("get desired state failed: %v", err)
	}
	if mock.rec.method != http.MethodGet {
		t.Errorf("expected GET, got %s", mock.rec.method)
	}
	if mock.rec.path != "/nodes/node-1/desired-state" {
		t.Errorf("expected desired-state path, got %s", mock.rec.path)
	}
	if desired.DesiredStatus != "READY" {
		t.Errorf("expected desired READY, got %q", desired.DesiredStatus)
	}
}

func TestClientGetPendingCommands(t *testing.T) {
	mock := newMockServer(t, http.StatusOK,
		`[{"id":"cmd-1","node_id":"node-1","type":"GET_STATUS","parameters":{"detail":"full"},"status":"PENDING"}]`)
	client := newTestClient(t, mock)

	commands, err := client.GetPendingCommands(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("get pending commands failed: %v", err)
	}

	if mock.rec.method != http.MethodGet {
		t.Errorf("expected GET, got %s", mock.rec.method)
	}
	if mock.rec.path != "/nodes/node-1/commands" {
		t.Errorf("expected commands path, got %s", mock.rec.path)
	}
	if mock.rec.query != "status=pending" {
		t.Errorf("expected status=pending filter, got %q", mock.rec.query)
	}
	if len(commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(commands))
	}
	if commands[0].ID != "cmd-1" || commands[0].Type != "GET_STATUS" {
		t.Errorf("unexpected command: %+v", commands[0])
	}
	if commands[0].Parameters["detail"] != "full" {
		t.Errorf("expected parameters parsed, got %v", commands[0].Parameters)
	}
}

func TestClientReportCommandResult(t *testing.T) {
	mock := newMockServer(t, http.StatusOK,
		`{"id":"cmd-1","node_id":"node-1","type":"GET_STATUS","status":"SUCCEEDED"}`)
	client := newTestClient(t, mock)

	result := CommandResult{
		Status: "SUCCEEDED",
		Result: map[string]any{"status": "READY"},
	}
	if err := client.ReportCommandResult(context.Background(), "node-1", "cmd-1", result); err != nil {
		t.Fatalf("report command result failed: %v", err)
	}

	if mock.rec.method != http.MethodPost {
		t.Errorf("expected POST, got %s", mock.rec.method)
	}
	if mock.rec.path != "/nodes/node-1/commands/cmd-1/result" {
		t.Errorf("expected result path, got %s", mock.rec.path)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(mock.rec.body), &body); err != nil {
		t.Fatalf("unmarshalling request body %q: %v", mock.rec.body, err)
	}
	if body["status"] != "SUCCEEDED" {
		t.Errorf("expected status SUCCEEDED, got %v", body["status"])
	}
	if result, ok := body["result"].(map[string]any); !ok || result["status"] != "READY" {
		t.Errorf("expected result payload, got %v", body["result"])
	}
}

func TestClientServerError(t *testing.T) {
	mock := newMockServer(t, http.StatusInternalServerError, `{"error":"internal server error"}`)
	client := newTestClient(t, mock)

	err := client.Heartbeat(context.Background(), "node-1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if apiErr.Status != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", apiErr.Status)
	}
	if apiErr.Message != "internal server error" {
		t.Errorf("expected error message, got %q", apiErr.Message)
	}
}

func TestClientMalformedSuccessResponse(t *testing.T) {
	mock := newMockServer(t, http.StatusOK, `not-json`)
	client := newTestClient(t, mock)

	_, err := client.GetDesiredState(context.Background(), "node-1")
	if err == nil {
		t.Fatal("expected error for malformed response")
	}
	if !strings.Contains(err.Error(), "decoding response") {
		t.Errorf("expected decoding error, got %v", err)
	}
}

func TestClientAuthorizationHeader(t *testing.T) {
	mock := newMockServer(t, http.StatusOK,
		`{"id":"node-1","name":"edge-01","status":"READY","desired_status":"READY"}`)
	client := New(mock.server.URL, WithHTTPClient(mock.server.Client()), WithToken("secret-token"))

	if _, err := client.GetNode(context.Background(), "node-1"); err != nil {
		t.Fatalf("get node failed: %v", err)
	}
	if got := mock.rec.header.Get("Authorization"); got != "Bearer secret-token" {
		t.Errorf("expected Authorization bearer token, got %q", got)
	}
}
