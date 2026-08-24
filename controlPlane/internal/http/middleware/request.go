package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type requestIDKey struct{}

// maxRequestIDLength bounds the size of a client-supplied correlation ID so
// hostile headers cannot bloat logs.
const maxRequestIDLength = 128

// RequestID assigns a correlation ID to every request: an existing,
// well-formed X-Request-ID header is honored, otherwise a UUID is minted.
// The ID is echoed back to the client, attached to the request context (for
// handlers and audit events) and stored for the access log.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestIDFrom returns the correlation ID assigned by the RequestID
// middleware, or an empty string.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// sanitizeRequestID accepts printable ASCII request IDs without whitespace
// or control characters.
func sanitizeRequestID(value string) string {
	if len(value) == 0 || len(value) > maxRequestIDLength {
		return ""
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7E {
			return ""
		}
	}
	return value
}
