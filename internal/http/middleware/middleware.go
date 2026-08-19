// Package middleware provides cross-cutting HTTP middleware for the control
// plane API.
package middleware

import (
	"log"
	"net/http"
	"runtime/debug"
	"time"
)

// statusRecorder captures the response status code written by a handler so it
// can be logged.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Log logs the method, path, response status and duration of every request.
func Log(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		logger.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, recorder.status, time.Since(start))
	})
}

// Recover converts panics raised inside handlers into a 500 response so a
// single faulty request cannot take down the process.
func Recover(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Printf("panic handling %s %s: %v\n%s", r.Method, r.URL.Path, recovered, debug.Stack())
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
