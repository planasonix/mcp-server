// Package middleware provides HTTP middleware for the MCP server.
package middleware

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"runtime/debug"
	"time"
)

type contextKey string

const RequestIDKey contextKey = "request_id"

// Chain wraps a handler with multiple middleware in order (outermost first).
func Chain(h http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// RequestID injects a unique request ID into the context and response headers.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := fmt.Sprintf("%d-%06d", time.Now().UnixMilli(), rand.Intn(1000000))
		ctx := context.WithValue(r.Context(), RequestIDKey, id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Logger logs method, path, status code, and latency.
// NOTE: We deliberately do NOT log the Authorization header or any
// org_id/pipeline content to avoid leaking tenant data to shared log sinks.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rw, r)

		reqID, _ := r.Context().Value(RequestIDKey).(string)
		log.Printf("[mcp] %s %s %d %s req=%s",
			r.Method, r.URL.Path, rw.status,
			time.Since(start).Round(time.Millisecond),
			reqID,
		)
	})
}

// Recover catches panics and returns a 500 rather than crashing the server.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[mcp] PANIC: %v\n%s", rec, debug.Stack())
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
