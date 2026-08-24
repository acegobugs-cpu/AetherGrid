package middleware

import (
	"net/http"
	"strings"
	"time"

	"AetherGrid/controlPlane/internal/audit"
	"AetherGrid/controlPlane/internal/auth"
)

// Authenticate resolves the caller's identity from the Authorization header
// and attaches the resulting principal to the request context.
//
// Resolution order:
//  1. Static human API keys (role: admin/operator/viewer).
//  2. Issued node credentials (agent principals, plus single-use bootstrap
//     principals during registration).
//
// Requests without an Authorization header proceed anonymously; route-level
// authorization decides whether that is acceptable. Invalid credentials are
// rejected immediately with 401 and audited.
func Authenticate(credentials *auth.Service, staticKeys *auth.StaticKeyStore, auditor *audit.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := BearerToken(r)
			if token == "" {
				next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), nil)))
				return
			}

			requestID := RequestIDFrom(r.Context())
			source := SourceAddress(r)
			deny := func(reason string) {
				if auditor != nil {
					auditor.Record(r.Context(), audit.Event{
						Operation: audit.OpAuthenticationFailed,
						Actor:     "unknown",
						ActorType: string(auth.PrincipalAnonymous),
						Result:    audit.ResultFailure,
						RequestID: requestID,
						Source:    source,
						Reason:    reason,
					})
				}
				writeAuthError(w, http.StatusUnauthorized, "authentication failed")
			}

			if principal := staticKeys.Authenticate(token); principal != nil {
				if auditor != nil {
					credentials.RecordUse(r.Context(), principal.TokenHash)
				}
				next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
				return
			}

			credential, err := credentials.Verify(r.Context(), token)
			if err != nil {
				deny("invalid credential")
				return
			}

			principalType := auth.PrincipalAgent
			if credential.Kind == auth.KindBootstrap {
				principalType = auth.PrincipalBootstrap
			}
			principal := &auth.Principal{
				Type:      principalType,
				NodeID:    credential.NodeID,
				TokenHash: credential.TokenHash,
			}
			credentials.RecordUse(r.Context(), credential.TokenHash)
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}
}

// Authorize enforces a route access policy against the authenticated
// principal. It is applied per-route by the router.
type Authorize struct {
	auditor *audit.Logger
}

// NewAuthorize constructs the authorization helper.
func NewAuthorize(auditor *audit.Logger) *Authorize {
	return &Authorize{auditor: auditor}
}

// Require wraps a handler so it only executes when the request satisfies the
// policy. Unauthenticated callers receive 401 (identity unknown);
// authenticated but unauthorized callers receive 403. Both are audited.
func (a *Authorize) Require(policy Policy, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal := auth.PrincipalFrom(r.Context())
		if reason := policy.Allows(principal, r); reason != "" {
			status := http.StatusForbidden
			if reason == "authentication required" {
				status = http.StatusUnauthorized
			}
			requestID := RequestIDFrom(r.Context())
			if a.auditor != nil {
				a.auditor.Record(r.Context(), audit.Event{
					Operation: audit.OpAuthorizationDenied,
					Actor:     principal.ID(),
					ActorType: principal.ActorType(),
					Resource:  r.Method + " " + r.URL.Path,
					Result:    audit.ResultDenied,
					RequestID: requestID,
					Source:    SourceAddress(r),
					Reason:    reason,
				})
			}
			message := "forbidden"
			if status == http.StatusUnauthorized {
				message = "authentication required"
			}
			writeAuthError(w, status, message)
			return
		}
		handler(w, r)
	}
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + message + `"}`))
}

// BearerToken extracts the bearer token from the Authorization header.
func BearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	const scheme = "Bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(header[len(scheme):])
}

// SourceAddress returns the remote peer address for audit records.
func SourceAddress(r *http.Request) string {
	host := r.RemoteAddr
	if host == "" {
		return "unknown"
	}
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}
	return host
}

// clientKey identifies the rate-limit bucket for a request: authenticated
// principals get their own bucket; anonymous callers share one per source IP.
func ClientBucket(r *http.Request) string {
	if principal := auth.PrincipalFrom(r.Context()); principal != nil && principal.Type != auth.PrincipalAnonymous {
		return "id:" + principal.ID()
	}
	return "ip:" + SourceAddress(r)
}

// timeNow is split out to keep the rate limiter deterministic in tests.
var timeNow = time.Now
