package browser_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/browser"
)

// The host client must hand the download body to the caller as soon as the
// response headers arrive. Buffering the whole file would block here until the
// server closes the stream.
func TestHTTPHostDownloadStreamsBeforeEOF(t *testing.T) {
	release := make(chan struct{})
	firstChunk := bytes.Repeat([]byte("a"), 32<<10)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/profiles/profile-1/downloads/download-1" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("X-AHA2-Download-Filename", "big.bin")
		writer.WriteHeader(http.StatusOK)
		flusher, _ := writer.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = writer.Write(firstChunk)
		if flusher != nil {
			flusher.Flush()
		}
		<-release
	}))
	defer server.Close()

	host := browser.NewHTTPHost(server.URL, "token")
	type downloadResult struct {
		file browser.HostDownloadFile
		err  error
	}
	done := make(chan downloadResult, 1)
	go func() {
		file, err := host.Download(context.Background(), "profile-1", "session-1", "download-1")
		done <- downloadResult{file: file, err: err}
	}()

	var result downloadResult
	select {
	case result = <-done:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("Download did not return before the host stream ended; the body was buffered")
	}
	if result.err != nil {
		close(release)
		t.Fatalf("download error: %v", result.err)
	}
	if result.file.Download.Filename != "big.bin" || result.file.Download.Size != -1 {
		close(release)
		t.Fatalf("download metadata=%#v, want filename big.bin and unknown size", result.file.Download)
	}
	if result.file.Body == nil {
		close(release)
		t.Fatal("download body is nil")
	}

	readDone := make(chan error, 1)
	got := make([]byte, len(firstChunk))
	go func() {
		_, err := io.ReadFull(result.file.Body, got)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err != nil {
			close(release)
			t.Fatalf("read first chunk: %v", err)
		}
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("first chunk was not readable before EOF")
	}
	if !bytes.Equal(got, firstChunk) {
		close(release)
		t.Fatalf("first chunk mismatch: got %d bytes", len(got))
	}
	close(release)
	rest, err := io.ReadAll(result.file.Body)
	if err != nil || len(rest) != 0 {
		t.Fatalf("rest=%d err=%v, want EOF immediately", len(rest), err)
	}
	if err := result.file.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}
}

// A host error body is diagnostic data. Only a small bounded prefix may be
// read before the status is decided; an endless error body must not stall the
// request.
func TestHTTPHostDownloadErrorBodyIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte(`{"error":{"code":"browser_session_changed","message":"retry"}}`))
		flusher, _ := writer.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		padding := bytes.Repeat([]byte(" "), 4096)
		for {
			if _, err := writer.Write(padding); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	host := browser.NewHTTPHost(server.URL, "token")
	errCh := make(chan error, 1)
	go func() {
		_, err := host.Download(context.Background(), "profile-1", "session-1", "download-1")
		errCh <- err
	}()
	select {
	case err := <-errCh:
		if browser.Code(err) != "browser_session_changed" {
			t.Fatalf("error code=%q want browser_session_changed (%v)", browser.Code(err), err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Download copied an unbounded error body")
	}
}

// A non-2xx response maps to a typed error and never exposes a usable body.
func TestHTTPHostDownloadNotFoundIsTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"error":{"code":"browser_download_not_found","message":"gone"}}`))
	}))
	defer server.Close()

	host := browser.NewHTTPHost(server.URL, "token")
	file, err := host.Download(context.Background(), "profile-1", "session-1", "download-1")
	if browser.Code(err) != "browser_download_not_found" || browser.Status(err) != http.StatusNotFound {
		t.Fatalf("code=%q status=%d err=%v", browser.Code(err), browser.Status(err), err)
	}
	if file.Body != nil {
		_ = file.Body.Close()
		t.Fatalf("failed download returned a body")
	}
}
