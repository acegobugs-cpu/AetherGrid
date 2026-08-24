package http_test

import (
	"net/http"
	"testing"
)

// TestClusterRecoveryEndpoints exercises the Phase 9 API surface: cluster
// health, reconciliation view, recovery view and manual reconcile must answer
// 404 for an unknown cluster, and recovery reset requires a node_id.
func TestClusterRecoveryEndpoints(t *testing.T) {
	app := newTestApp(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/clusters/missing/health"},
		{http.MethodGet, "/clusters/missing/reconciliation"},
		{http.MethodGet, "/clusters/missing/recovery"},
		{http.MethodPost, "/clusters/missing/reconcile"},
	} {
		resp, _ := app.request(t, tc.method, tc.path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: expected 404 for unknown cluster, got %d", tc.method, tc.path, resp.StatusCode)
		}
	}

	resp, body := app.request(t, http.MethodPost, "/clusters/x/recovery/reset", map[string]string{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("reset without node_id: expected 400, got %d (%v)", resp.StatusCode, body)
	}
}
