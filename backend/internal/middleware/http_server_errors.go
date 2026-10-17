package middleware

import (
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/reqctx"
)

const (
	// rpcPathPrefix starts every Connect procedure's path. LogOutcome logs a
	// failed RPC with its cause, so this leaves those requests to it.
	rpcPathPrefix = "/paladin."
	// firstServerErrorStatus is the lowest status that reports the server
	// failing rather than the request.
	firstServerErrorStatus = http.StatusInternalServerError
	// httpFailedMessage is the log line of a plain HTTP request the server
	// failed.
	httpFailedMessage = "http request failed"
)

// LogHTTPServerErrors gives every plain HTTP request — the OAuth endpoints,
// the audit stream, the MCP edge, a webhook — a request id, and logs one the
// server answered with a 5xx: method, path, status and duration, under that
// id. The RPC interceptors do this for Connect calls; a plain handler that
// failed left no line, and the client's request id named nothing.
func LogHTTPServerErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, rpcPathPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		id := ensureRequestID(r.Header.Get(HeaderRequestID), r.Header.Set)
		w.Header().Set(HeaderRequestID, id)
		ctx := reqctx.WithRequestID(r.Context(), id)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r.WithContext(ctx))
		if rec.status >= firstServerErrorStatus {
			logger.FromContext(ctx).Error(httpFailedMessage,
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", rec.status),
				zap.Int64("took_ms", time.Since(start).Milliseconds()))
		}
	})
}

// statusRecorder remembers the status a handler wrote. It flushes and
// unwraps to the writer it wraps, so a streamed response — the audit feed,
// an MCP session — still streams through it.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status, s.wroteHeader = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
