package httpapi

import (
	"bytes"
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
