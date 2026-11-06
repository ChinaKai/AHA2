package httpapi

import (
	"bufio"
	"database/sql"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

const slowRequestThreshold = time.Second

// Routes registered on the ServeMux for endpoints that intentionally block for
// longer than slowRequestThreshold. Each value is the exact ServeMux pattern,
// including the HTTP method.
const (
	waitAgentInboxRoute         = "POST /api/v1/agent/turn/inbox/wait"
	waitAgentCollaborationRoute = "POST /api/v1/agent/collaboration/batches/{batch}/wait"
	agentADBExecRoute           = "POST /api/v1/agent/hardware/{hardware}/adb/exec"
	hardwareADBPairRoute        = "POST /api/v1/hardware-resources/{id}/adb/pair"
)

// longPollingRoutes are excluded from slow-request warnings: their duration is
// chosen by the caller's wait window or by a device that answers on human time,
// so they are long by design rather than slow by accident. Reporting them would
// bury the real request under investigation (such as a slow first screen).
//
// Matching uses http.Request.Pattern, the exact registered pattern (method plus
// path), so sibling routes (for example /api/v1/agent/turn/inbox/claim) and
// longer lookalike paths are still reported. Do not replace this with a prefix
// check. When a new long-polling or device-waiting endpoint is added, add its
// registered pattern here.
var longPollingRoutes = map[string]struct{}{
	waitAgentInboxRoute:         {},
	waitAgentCollaborationRoute: {},
	agentADBExecRoute:           {},
	hardwareADBPairRoute:        {},
}

// slowRequestLog reports requests that take longer than slowRequestThreshold.
// Only the method, path, status and duration are recorded: query strings,
// bodies and headers can carry credentials or user content and are never
// logged. The response writer is wrapped only to observe the status code.
func (s *Server) slowRequestLog(next http.Handler) http.Handler {
	return slowRequestLogHandlerWithStats(s.logger, slowRequestThreshold, s.store.PoolStats, next)
}

func slowRequestLogHandler(logger *slog.Logger, threshold time.Duration, next http.Handler) http.Handler {
	return slowRequestLogHandlerWithStats(logger, threshold, nil, next)
}

// slowRequestLogHandlerWithStats optionally samples pool counters around the
// request. The delta separates time spent waiting for a database connection
// (db_wait) from time spent executing the request and its queries.
func slowRequestLogHandlerWithStats(logger *slog.Logger, threshold time.Duration, stats func() sql.DBStats, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var before sql.DBStats
		if stats != nil {
			before = stats()
		}
		started := time.Now()
		recorder := &statusResponseWriter{ResponseWriter: writer}
		next.ServeHTTP(recorder, request)
		elapsed := time.Since(started)
		// The ServeMux fills in Pattern while serving the request. Skip the
		// endpoints that are long by design instead of reporting them as slow.
		if _, ok := longPollingRoutes[request.Pattern]; ok {
			return
		}
		// A hijacked connection (WebSocket) or an event stream stays open for its
		// own lifetime; that is streaming, not a slow request.
		if recorder.hijacked || strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/event-stream") {
			return
		}
		if elapsed <= threshold {
			return
		}
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		if stats == nil {
			logger.Warn("slow http request", "method", request.Method, "path", request.URL.Path, "status", status, "duration", elapsed)
			return
		}
		after := stats()
		logger.Warn(
			"slow http request",
			"method", request.Method,
			"path", request.URL.Path,
			"status", status,
			"duration", elapsed,
			"db_wait", (after.WaitDuration - before.WaitDuration).Round(time.Millisecond),
			"db_wait_count", after.WaitCount-before.WaitCount,
		)
	})
}

type statusResponseWriter struct {
	http.ResponseWriter
	status   int
	hijacked bool
}

func (w *statusResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *statusResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (w *statusResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	w.hijacked = true
	return hijacker.Hijack()
}
