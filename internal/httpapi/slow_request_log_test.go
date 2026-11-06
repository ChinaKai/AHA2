package httpapi

import (
	"bytes"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSlowRequestLogOnlyWarnsForSlowRequests(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))

	fast := slowRequestLogHandler(logger, 50*time.Millisecond, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	fast.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if got := logged.String(); got != "" {
		t.Fatalf("a fast request must not be logged, got %q", got)
	}

	slow := slowRequestLogHandler(logger, 25*time.Millisecond, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte("ok"))
		time.Sleep(60 * time.Millisecond)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?token=super-secret&filename=private.pdf", nil)
	request.Header.Set("Authorization", "Bearer top-secret")
	recorder := httptest.NewRecorder()
	slow.ServeHTTP(recorder, request)
	// Observing the request must not change the response the client receives.
	if recorder.Code != http.StatusAccepted || recorder.Body.String() != "ok" {
		t.Fatalf("response changed by middleware: status %d body %q", recorder.Code, recorder.Body.String())
	}

	output := logged.String()
	for _, want := range []string{"level=WARN", "slow http request", "method=GET", "path=/api/v1/tasks", "status=202", "duration="} {
		if !strings.Contains(output, want) {
			t.Errorf("slow request log missing %q in %q", want, output)
		}
	}
	// The query string, body and headers may carry credentials or content; only
	// the path may appear in the log line.
	for _, leaked := range []string{"super-secret", "private.pdf", "top-secret", "Authorization", "token="} {
		if strings.Contains(output, leaked) {
			t.Errorf("slow request log leaked %q in %q", leaked, output)
		}
	}
}

func TestSlowRequestLogReportsConnectionWait(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	var calls int
	stats := func() sql.DBStats {
		calls++
		if calls == 1 {
			return sql.DBStats{WaitCount: 2, WaitDuration: 100 * time.Millisecond}
		}
		return sql.DBStats{WaitCount: 3, WaitDuration: 450 * time.Millisecond}
	}
	handler := slowRequestLogHandlerWithStats(logger, time.Nanosecond, stats, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		time.Sleep(time.Millisecond)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil))
	output := logged.String()
	for _, want := range []string{"db_wait=350ms", "db_wait_count=1"} {
		if !strings.Contains(output, want) {
			t.Fatalf("slow request log missing %q in %q", want, output)
		}
	}
}

func TestSlowRequestLogRecordsImplicitOKStatus(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	handler := slowRequestLogHandler(logger, time.Nanosecond, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
		time.Sleep(time.Millisecond)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil))
	if output := logged.String(); !strings.Contains(output, "status=200") {
		t.Fatalf("an implicit status must be logged as 200, got %q", output)
	}
}

func TestSlowRequestLogSkipsLongPollingRoutes(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	const threshold = 20 * time.Millisecond

	mux := http.NewServeMux()
	slow := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte("ok"))
		time.Sleep(2 * threshold)
	})
	for _, pattern := range []string{
		waitAgentInboxRoute,
		waitAgentCollaborationRoute,
		agentADBExecRoute,
		hardwareADBPairRoute,
	} {
		mux.Handle(pattern, slow)
	}

	handler := slowRequestLogHandler(logger, threshold, mux)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/agent/turn/inbox/wait"},
		{http.MethodPost, "/api/v1/agent/collaboration/batches/batch-1/wait"},
		{http.MethodPost, "/api/v1/agent/hardware/hw-1/adb/exec"},
		{http.MethodPost, "/api/v1/hardware-resources/res-1/adb/pair"},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
		// The middleware must not change the response of skipped requests either.
		if recorder.Code != http.StatusAccepted || recorder.Body.String() != "ok" {
			t.Fatalf("response changed by middleware for %s: status %d body %q", tc.path, recorder.Code, recorder.Body.String())
		}
	}
	if got := logged.String(); got != "" {
		t.Fatalf("requests that are long by design must not be reported as slow, got %q", got)
	}
}

func TestSlowRequestLogWarnsForLongPollingLookalikes(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	const threshold = 20 * time.Millisecond

	mux := http.NewServeMux()
	slow := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
		time.Sleep(2 * threshold)
	})
	// Only the exact registered pattern is skipped: the sibling route, the same
	// path under another method, and longer or similarly named paths must stay
	// visible in the slow-request log.
	mux.Handle(waitAgentInboxRoute, slow)
	mux.Handle("GET /api/v1/agent/turn/inbox/wait", slow)
	mux.Handle("POST /api/v1/agent/turn/inbox/claim", slow)
	mux.Handle("POST /api/v1/agent/turn/inbox/wait/extra", slow)
	mux.Handle("POST /api/v1/hardware-resources/{id}/adb/pairing", slow)
	mux.Handle("/", slow)

	handler := slowRequestLogHandler(logger, threshold, mux)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/agent/turn/inbox/wait"},
		{http.MethodPost, "/api/v1/agent/turn/inbox/claim"},
		{http.MethodPost, "/api/v1/agent/turn/inbox/wait/extra"},
		{http.MethodPost, "/api/v1/hardware-resources/res-1/adb/pairing"},
		{http.MethodPost, "/api/v1/agent/collaboration/batches/batch-1/wait/extra"},
	} {
		logged.Reset()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, nil))
		output := logged.String()
		for _, want := range []string{
			"level=WARN", "slow http request", "method=" + tc.method,
			"path=" + tc.path, "status=202", "duration=",
		} {
			if !strings.Contains(output, want) {
				t.Errorf("lookalike %s %s must still be logged with %q, got %q", tc.method, tc.path, want, output)
			}
		}
	}
}

func TestSlowRequestLogSkipsEventStreams(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	handler := slowRequestLogHandler(logger, time.Nanosecond, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		time.Sleep(time.Millisecond)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/tasks/events", nil))
	if got := logged.String(); got != "" {
		t.Fatalf("a long-lived event stream must not be reported as a slow request, got %q", got)
	}
}
