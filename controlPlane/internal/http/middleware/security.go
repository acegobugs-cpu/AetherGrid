package middleware

import "net/http"

// DefaultMaxBodyBytes caps every request body (1 MiB). JSON payloads in this
// API are small; a generous fixed ceiling protects the control plane from
// resource exhaustion.
const DefaultMaxBodyBytes int64 = 1 << 20

// SecureHeaders sets conservative security headers appropriate for a
// machine-only JSON API. Browser-oriented headers such as CSP are
// deliberately omitted: there is no browser content to protect.
//
// Strict-Transport-Security is only set when the control plane serves TLS,
// otherwise it would instruct clients to require HTTPS the server cannot
// provide.
func SecureHeaders(tlsEnabled bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if tlsEnabled {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// LimitBody rejects requests that declare a body larger than maxBytes and
// wraps every remaining body with http.MaxBytesReader so oversized chunked
// payloads also fail fast with 413 instead of consuming memory.
func LimitBody(maxBytes int64, next http.Handler) http.Handler {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBytes {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"error":"request body too large"}`))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}
