package http_test

// Phase 10 security test suite. It covers the required security matrix:
// authentication (valid/invalid/expired/revoked/missing/malformed),
// authorization roles, node identity scoping, secure registration, credential
// lifecycle, rate limiting, input validation, secret leakage, request IDs and
// audit events.

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"AetherGrid/controlPlane/internal/audit"
	"AetherGrid/controlPlane/internal/auth"
	apihandler "AetherGrid/controlPlane/internal/http"
	"AetherGrid/controlPlane/internal/provisioning"
	"AetherGrid/controlPlane/internal/repository/sqlite"
	"AetherGrid/controlPlane/internal/service"
	"AetherGrid/controlPlane/migrations"
)

const bootstrapTokenPrefix = "agr_"

const validUUID = "00000000-0000-0000-0000-000000000001"

// createNodeAsAdmin registers a node through the API and returns its id.
func createNodeAsAdmin(t *testing.T, app *testApp, name string) string {
	t.Helper()
	resp, body := app.request(t, http.MethodPost, "/nodes", map[string]any{
		"name": name,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating node %s: expected 201, got %d (%v)", name, resp.StatusCode, body)
	}
	return asMap(t, body)["id"].(string)
}

// registerAgent exchanges a bootstrap token for an agent credential and
// returns both the raw response and decoded body for custom assertions.
func registerAgent(t *testing.T, app *testApp, nodeID, bootstrapToken string) (*http.Response, map[string]any) {
	t.Helper()
	resp, decoded := app.requestAs(t, bootstrapToken, http.MethodPost, "/nodes/"+nodeID+"/register", nil)
	if m, ok := decoded.(map[string]any); ok {
		return resp, m
	}
	return resp, map[string]any{}
}

// provisionAndRegister drives the full provisioning flow: admin creates a
// node, then the "agent" exchanges its bootstrap token. Returns node id +
// agent credential.
func provisionAndRegister(t *testing.T, app *testApp, name string) (string, string) {
	t.Helper()
	createResp, createBody := app.request(t, http.MethodPost, "/nodes", map[string]any{"name": name})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("provisioning: expected 201, got %d (%v)", createResp.StatusCode, createBody)
	}
	body := asMap(t, createBody)
	nodeID, _ := body["id"].(string)
	token, _ := body["bootstrap_token"].(string)
	if nodeID == "" || !strings.HasPrefix(token, bootstrapTokenPrefix) {
		t.Fatalf("provisioning response missing id/bootstrap_token: %v", body)
	}

	resp, registered := registerAgent(t, app, nodeID, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("registration exchange failed: %d (%v)", resp.StatusCode, registered)
	}
	credential, _ := registered["credential"].(string)
	if !strings.HasPrefix(credential, bootstrapTokenPrefix) {
		t.Fatalf("missing agent credential in exchange response: %v", registered)
	}
	return nodeID, credential
}

var registrationCounter int

// uniqueName builds a fresh valid node name for rate-limit hammering.
func uniqueName(base int) string {
	registrationCounter++
	return "ratelimit-node-" + itoa(base) + "-" + itoa(registrationCounter)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('a' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

func TestAuthenticationRejectsMissingCredential(t *testing.T) {
	app := newTestApp(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/nodes"},
		{http.MethodPost, "/nodes/" + validUUID + "/heartbeat"},
		{http.MethodPut, "/nodes/" + validUUID + "/state"},
		{http.MethodDelete, "/nodes/" + validUUID},
		{http.MethodGet, "/infrastructure"},
		{http.MethodPost, "/clusters/x/reconcile"},
		{http.MethodPost, "/clusters/x/recovery/reset"},
	} {
		resp, _ := app.requestAs(t, "", tc.method, tc.path, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without credential: expected 401, got %d", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestAuthenticationRejectsInvalidCredential(t *testing.T) {
	app := newTestApp(t)

	for _, token := range []string{
		"totally-bogus-token",
		"agr_expired_or_never_issued",
		testAdminKey[:8], // malformed prefix of a real key
	} {
		resp, _ := app.requestAs(t, token, http.MethodGet, "/nodes", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("invalid credential %q: expected 401, got %d", token, resp.StatusCode)
		}
	}

	// Malformed Authorization schemes are treated as missing credentials and
	// rejected by policy (no route is public).
	req, _ := http.NewRequest(http.MethodGet, app.server.URL+"/nodes", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	resp, err := app.server.Client().Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("non-bearer authorization: expected 401, got %d", resp.StatusCode)
	}
}

func TestAuthenticationRejectsExpiredBootstrapToken(t *testing.T) {
	app := newTestApp(t)
	nodeID := createNodeAsAdmin(t, app, "expired-node")

	// Issue a bootstrap credential whose lifetime has already elapsed.
	app.credentials.BootstrapTokenTTL = -time.Minute
	token, _, err := app.credentials.IssueBootstrap(context.Background(), nodeID)
	app.credentials.BootstrapTokenTTL = 15 * time.Minute
	if err != nil {
		t.Fatalf("issuing expired bootstrap: %v", err)
	}

	resp, _ := app.requestAs(t, token, http.MethodPost, "/nodes/"+nodeID+"/register", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expired bootstrap token: expected 401, got %d", resp.StatusCode)
	}
}

func TestAuthenticationRejectsRevokedCredential(t *testing.T) {
	app := newTestApp(t)
	nodeID, agentToken := provisionAndRegister(t, app, "revoked-node")

	// Admin revokes all credentials for the node.
	resp, _ := app.request(t, http.MethodDelete, "/nodes/"+nodeID+"/credentials", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revocation: expected 200, got %d", resp.StatusCode)
	}

	// The revoked agent credential no longer authenticates.
	resp, _ = app.requestAs(t, agentToken, http.MethodPost, "/nodes/"+nodeID+"/heartbeat", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoked credential heartbeat: expected 401, got %d", resp.StatusCode)
	}
}

func TestAuthorizationRoleMatrix(t *testing.T) {
	app := newTestApp(t)
	nodeID, agentToken := provisionAndRegister(t, app, "matrix-node")

	cases := []struct {
		name     string
		token    string
		method   string
		path     string
		body     any
		expected int
	}{
		// Viewer may read...
		{"viewer reads nodes", testViewerKey, http.MethodGet, "/nodes", nil, http.StatusOK},
		// ...but not reconcile (operator+).
		{"viewer reconciles", testViewerKey, http.MethodPost, "/nodes/" + nodeID + "/reconcile", nil, http.StatusForbidden},
		// Operator may reconcile...
		{"operator reconciles", testOperatorKey, http.MethodPost, "/nodes/" + nodeID + "/reconcile", nil, http.StatusOK},
		// ...but not provision infrastructure (admin).
		{"operator creates infrastructure", testOperatorKey, http.MethodPost, "/infrastructure",
			map[string]any{"name": "op-infra"}, http.StatusForbidden},
		// Agent cannot access human endpoints or other nodes' resources.
		{"agent lists nodes", agentToken, http.MethodGet, "/nodes", nil, http.StatusForbidden},
		{"agent deletes node", agentToken, http.MethodDelete, "/nodes/" + nodeID, nil, http.StatusForbidden},
		{"agent resets recovery", agentToken, http.MethodPost, "/clusters/x/recovery/reset",
			map[string]string{"node_id": nodeID}, http.StatusForbidden},
		{"agent destroys infrastructure", agentToken, http.MethodPost, "/infrastructure/00000000-0000-0000-0000-000000000009/destroy", nil, http.StatusForbidden},
		// Admin may destroy (404 here because the resource does not exist,
		// proving the request passed authorization).
		{"admin deletes infrastructure", testAdminKey, http.MethodDelete, "/infrastructure/00000000-0000-0000-0000-000000000010", nil, http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := app.requestAs(t, tc.token, tc.method, tc.path, tc.body)
			if resp.StatusCode != tc.expected {
				t.Fatalf("expected %d, got %d", tc.expected, resp.StatusCode)
			}
		})
	}
}

func TestAgentIdentityScoping(t *testing.T) {
	app := newTestApp(t)
	nodeA, credA := provisionAndRegister(t, app, "worker-01")
	nodeB, credB := provisionAndRegister(t, app, "worker-02")

	// worker-01's credential works on worker-01...
	resp, _ := app.requestAs(t, credA, http.MethodPost, "/nodes/"+nodeA+"/heartbeat", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("own-node heartbeat: expected 200, got %d", resp.StatusCode)
	}

	// ...but must NOT act on worker-02 (identity spoofing prevention).
	resp, _ = app.requestAs(t, credA, http.MethodPost, "/nodes/"+nodeB+"/heartbeat", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-node heartbeat: expected 403, got %d", resp.StatusCode)
	}

	resp, _ = app.requestAs(t, credA, http.MethodPut, "/nodes/"+nodeB+"/state",
		map[string]string{"status": "READY"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-node state report: expected 403, got %d", resp.StatusCode)
	}

	resp, _ = app.requestAs(t, credA, http.MethodGet, "/nodes/"+nodeB+"/commands", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-node command list: expected 403, got %d", resp.StatusCode)
	}

	// worker-02 still works fine with its own credential.
	resp, _ = app.requestAs(t, credB, http.MethodPost, "/nodes/"+nodeB+"/heartbeat", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("worker-02 own heartbeat: expected 200, got %d", resp.StatusCode)
	}
}

func TestSecureRegistrationFlow(t *testing.T) {
	app := newTestApp(t)

	// Provisioning returns exactly one usable bootstrap credential.
	createResp, createBody := app.request(t, http.MethodPost, "/nodes", map[string]any{"name": "reg-node"})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	body := asMap(t, createBody)
	nodeID := body["id"].(string)
	bootstrapToken := body["bootstrap_token"].(string)

	// The exchange yields an agent credential.
	resp, registered := registerAgent(t, app, nodeID, bootstrapToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("exchange failed: %d (%v)", resp.StatusCode, registered)
	}
	if !strings.HasPrefix(registered["credential"].(string), bootstrapTokenPrefix) {
		t.Fatalf("missing agent credential in exchange response: %v", registered)
	}

	// Bootstrap tokens are single-use: replay is rejected.
	replayResp, _ := app.requestAs(t, bootstrapToken, http.MethodPost, "/nodes/"+nodeID+"/register", nil)
	if replayResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bootstrap replay: expected 401, got %d", replayResp.StatusCode)
	}

	// A consumed bootstrap token cannot register a different node either.
	otherNode := createNodeAsAdmin(t, app, "reg-other")
	mismatchResp, _ := app.requestAs(t, bootstrapToken, http.MethodPost, "/nodes/"+otherNode+"/register", nil)
	if mismatchResp.StatusCode != http.StatusUnauthorized && mismatchResp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-node bootstrap: expected 401/403, got %d", mismatchResp.StatusCode)
	}
}

func TestCredentialRotation(t *testing.T) {
	app := newTestApp(t)
	nodeID, oldCred := provisionAndRegister(t, app, "rotate-node")

	resp, body := app.requestAs(t, oldCred, http.MethodPost, "/nodes/"+nodeID+"/credentials/rotate", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotation: expected 200, got %d (%v)", resp.StatusCode, body)
	}
	newCred := asMap(t, body)["credential"].(string)
	if newCred == oldCred {
		t.Fatal("rotation must issue a distinct credential")
	}

	// Old credential revoked by rotation; new one authenticates.
	oldResp, _ := app.requestAs(t, oldCred, http.MethodPost, "/nodes/"+nodeID+"/heartbeat", nil)
	if oldResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("old credential after rotation: expected 401, got %d", oldResp.StatusCode)
	}
	newResp, _ := app.requestAs(t, newCred, http.MethodPost, "/nodes/"+nodeID+"/heartbeat", nil)
	if newResp.StatusCode != http.StatusOK {
		t.Errorf("new credential after rotation: expected 200, got %d", newResp.StatusCode)
	}
}

func TestDeletedNodeCannotReauthenticate(t *testing.T) {
	app := newTestApp(t)
	nodeID, agentToken := provisionAndRegister(t, app, "doomed-node")

	resp, _ := app.request(t, http.MethodDelete, "/nodes/"+nodeID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", resp.StatusCode)
	}

	resp, _ = app.requestAs(t, agentToken, http.MethodPost, "/nodes/"+nodeID+"/heartbeat", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("credential after node deletion: expected 401, got %d", resp.StatusCode)
	}
}

func TestRateLimitingProtectsRegistration(t *testing.T) {
	app := newSecurityTestApp(t, func(s *apihandler.Security) {
		s.OpenRegistration = true
		s.RegisterRatePerMinute = 5
	}, io.Discard)

	var limited int
	var created int
	for i := 0; i < 20; i++ {
		resp, _ := app.requestAs(t, "", http.MethodPost, "/nodes", map[string]any{"name": uniqueName(i)})
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			limited++
		case http.StatusCreated:
			created++
		default:
			t.Fatalf("open registration attempt %d: unexpected status %d", i, resp.StatusCode)
		}
	}
	if limited == 0 || created == 0 {
		t.Fatalf("expected a mix of accepted and limited requests, got created=%d limited=%d", created, limited)
	}
}

func TestInputValidation(t *testing.T) {
	app := newTestApp(t)

	// Malformed node IDs are rejected before hitting services.
	resp, _ := app.request(t, http.MethodGet, "/nodes/not-a-uuid", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed node id: expected 400, got %d", resp.StatusCode)
	}

	// Command injection payloads in names are rejected.
	for _, hostile := range []string{
		"bad; rm -rf /",
		"$(whoami)",
		"back`tick`node",
		strings.Repeat("x", 64), // too long
	} {
		resp, _ := app.request(t, http.MethodPost, "/nodes", map[string]any{"name": hostile})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("hostile node name %q: expected 400, got %d", hostile, resp.StatusCode)
		}
	}

	// Invalid IPs rejected.
	resp, _ = app.request(t, http.MethodPost, "/nodes", map[string]any{
		"name": "ipcheck", "ip_address": "999.999.999.999",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid ip_address: expected 400, got %d", resp.StatusCode)
	}

	// Oversized bodies are rejected with 413.
	huge := bytes.Repeat([]byte("a"), int(2<<20)) // 2 MiB > 1 MiB limit
	req, _ := http.NewRequest(http.MethodPost, app.server.URL+"/nodes", bytes.NewReader(huge))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	oversize, err := app.server.Client().Do(req)
	if err != nil {
		t.Fatalf("oversize request failed: %v", err)
	}
	oversize.Body.Close()
	if oversize.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: expected 413, got %d", oversize.StatusCode)
	}

	// Unknown fields rejected (strict decoding).
	resp, _ = app.request(t, http.MethodPost, "/nodes", map[string]any{
		"name": "strict-node", "extra_field": true,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown field: expected 400, got %d", resp.StatusCode)
	}
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string { return b.buf.String() }

func TestSecretsNeverAppearInLogs(t *testing.T) {
	logBuf := &safeBuffer{}
	app := newTestAppWithLogger(t, logBuf)

	createResp, createBody := app.request(t, http.MethodPost, "/nodes", map[string]any{"name": "leak-check"})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	body := asMap(t, createBody)
	nodeID := body["id"].(string)
	bootstrap := body["bootstrap_token"].(string)

	resp, registered := registerAgent(t, app, nodeID, bootstrap)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("exchange failed: %d", resp.StatusCode)
	}
	agentCred := registered["credential"].(string)

	// Failed authentication attempts must log without credentials.
	for _, bad := range []string{"wrong-token", agentCred + "x"} {
		r, _ := app.requestAs(t, bad, http.MethodGet, "/nodes", nil)
		if r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 for invalid credential")
		}
	}

	logBuf.mu.Lock()
	logged := logBuf.String()
	logBuf.mu.Unlock()

	for _, secret := range []string{bootstrap, agentCred, testAdminKey} {
		if strings.Contains(logged, secret) {
			t.Fatalf("credential leaked into logs: %.12s...", secret)
		}
	}
}

func TestRequestIDEchoedAndSecurityHeadersPresent(t *testing.T) {
	app := newTestApp(t)

	req, _ := http.NewRequest(http.MethodGet, app.server.URL+"/reconciliation/status", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	req.Header.Set("X-Request-ID", "corr-id-42")
	resp, err := app.server.Client().Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if echoed := resp.Header.Get("X-Request-ID"); echoed != "corr-id-42" {
		t.Errorf("expected X-Request-ID echo, got %q", echoed)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("expected X-Content-Type-Options on API responses")
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("expected Cache-Control no-store on API responses")
	}
}

func TestAuditEventsRecorded(t *testing.T) {
	app := newTestApp(t)

	// Denied operations produce audit events.
	app.requestAs(t, testViewerKey, http.MethodDelete, "/nodes/"+validUUID, nil)

	// Registration flow events.
	nodeID, _ := provisionAndRegister(t, app, "audited-node")

	count := countAuditEvents(t, app)
	if count < 3 { // AuthorizationDenied + NodeRegistered + CredentialIssued(x2)
		t.Fatalf("expected audit events, found %d", count)
	}

	// Node deletion revokes credentials and audits.
	app.request(t, http.MethodDelete, "/nodes/"+nodeID, nil)
	if after := countAuditEvents(t, app); after <= count {
		t.Error("expected deletion to append audit events")
	}
}

func countAuditEvents(t *testing.T, app *testApp) int {
	t.Helper()
	row := app.repo.DB().QueryRow(`SELECT COUNT(*) FROM audit_events`)
	var count int
	if err := row.Scan(&count); err != nil {
		t.Fatalf("querying audit events: %v", err)
	}
	return count
}

func TestOpenRegistrationDisabledByDefault(t *testing.T) {
	app := newTestApp(t)

	resp, _ := app.requestAs(t, "", http.MethodPost, "/nodes", map[string]any{"name": "anon-node"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous node creation without open registration: expected 401, got %d", resp.StatusCode)
	}
}

// newTestAppWithLogger wires a standard app whose logs stream into the given
// writer so tests can assert on logged output.
func newTestAppWithLogger(t *testing.T, w io.Writer) *testApp {
	return newSecurityTestApp(t, nil, w)
}

// newSecurityTestApp allows tests to tweak security settings (rate limits,
// open registration) before wiring the router.
func newSecurityTestApp(t *testing.T, mutate func(*apihandler.Security), logWriter io.Writer) *testApp {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "security-test.db")
	repo, err := sqlite.NewNodeRepository(dbPath)
	if err != nil {
		t.Fatalf("opening repository: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	if err := migrations.Apply(context.Background(), repo.DB()); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}

	logger := log.New(logWriter, "", 0)
	staticKeys, err := auth.NewStaticKeyStore([]string{
		testAdminKey + ":admin",
		testOperatorKey + ":operator",
		testViewerKey + ":viewer",
	})
	if err != nil {
		t.Fatalf("building static key store: %v", err)
	}
	credentials := auth.NewService(sqlite.NewCredentialRepository(repo.DB()))
	auditor := audit.NewLogger(logger, sqlite.NewAuditRepository(repo.DB()))

	commandService := service.NewCommandService(sqlite.NewCommandRepository(repo.DB()), repo)
	reconciler := service.NewReconciliationService(testReconcileConfig, repo,
		sqlite.NewReconciliationRepository(repo.DB()), commandService, logger, nil)
	infraRepo := sqlite.NewInfrastructureRepository(repo.DB())
	infrastructureService := service.NewInfrastructureService(infraRepo, infraRepo, &stubProvisioner{}, &provisioning.Metrics{}, logger)

	security := apihandler.Security{
		Credentials:      credentials,
		StaticKeys:       staticKeys,
		Auditor:          auditor,
		OpenRegistration: false,
	}
	if mutate != nil {
		mutate(&security)
	}

	router := apihandler.NewRouter(
		apihandler.Services{
			Nodes:           service.NewNodeService(repo),
			Heartbeats:      service.NewHeartbeatService(repo),
			Reconciler:      reconciler,
			Commands:        commandService,
			Infrastructures: infrastructureService,
			Clusters:        nil,
		},
		security,
		logger,
	)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return &testApp{
		server:        server,
		dbPath:        dbPath,
		repo:          repo,
		credentials:   credentials,
		adminToken:    testAdminKey,
		operatorToken: testOperatorKey,
		viewerToken:   testViewerKey,
	}
}
