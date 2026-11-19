package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/browser/browsertest"
)

// stagedReadCloser emits one chunk, then blocks until released. It lets the
// test prove the HTTP handler forwards the first chunk before the host stream
// reaches EOF instead of buffering the whole file.
type stagedReadCloser struct {
	first   []byte
	offset  int
	release <-chan struct{}
	waited  bool
	closed  atomic.Bool
}

func (r *stagedReadCloser) Read(p []byte) (int, error) {
	if r.offset < len(r.first) {
		n := copy(p, r.first[r.offset:])
		r.offset += n
		return n, nil
	}
	if !r.waited {
		r.waited = true
		<-r.release
	}
	return 0, io.EOF
}

func (r *stagedReadCloser) Close() error {
	r.closed.Store(true)
	return nil
}

func TestAgentBrowserDownloadStreamsBeforeHostEOF(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	grantID, _ := agentGrantFixture(t, f, []string{"open_tab", "navigate", "snapshot", "files"})
	token := f.agentToken(t, f.task.ID)

	release := make(chan struct{})
	firstChunk := bytes.Repeat([]byte("a"), 32<<10)
	body := &stagedReadCloser{first: firstChunk, release: release}
	const downloadID = "download-0123456789abcdef01234567"
	host.DownloadFiles = map[string]browser.HostDownloadFile{
		downloadID: {
			Download: browser.HostDownload{Filename: "big.bin", Size: -1},
			Body:     body,
		},
	}

	request, err := http.NewRequest(http.MethodGet,
		f.server.URL+"/api/v1/agent/browser/grants/"+grantID+"/downloads/"+downloadID, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("download status=%d body=%s", response.StatusCode, payload)
	}
	if response.Header.Get("X-Content-Type-Options") != "nosniff" ||
		response.Header.Get("Cache-Control") != "no-store" ||
		response.Header.Get("Content-Length") != "" {
		t.Fatalf("download headers=%v, want nosniff/no-store and no length", response.Header)
	}

	if _, err := io.ReadFull(response.Body, firstChunk); err != nil {
		close(release)
		t.Fatalf("first chunk was not forwarded before EOF: %v", err)
	}
	close(release)
	rest, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read rest: %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("unexpected trailing bytes: %d", len(rest))
	}
	waitForClose(t, body)
}

// A host failure must produce a JSON error before any success header is
// written, and must never look like an attachment.
func TestAgentBrowserDownloadFailureWritesNoSuccessHeaders(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	grantID, _ := agentGrantFixture(t, f, []string{"open_tab", "navigate", "snapshot", "files"})
	token := f.agentToken(t, f.task.ID)
	host.FailDownloads = &browser.Error{
		Code: "browser_download_not_found", Message: "gone", Status: http.StatusNotFound,
	}

	request, err := http.NewRequest(http.MethodGet,
		f.server.URL+"/api/v1/agent/browser/grants/"+grantID+"/downloads/download-0123456789abcdef01234567", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("failure status=%d", response.StatusCode)
	}
	// Global JSON middleware adds its own no-store/nosniff headers; the point
	// is that this response is not an attachment and carries no file body.
	if disposition := response.Header.Get("Content-Disposition"); disposition != "" {
		t.Fatalf("failure wrote Content-Disposition=%q", disposition)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("failure Content-Type=%q, want JSON error", contentType)
	}
	payload, _ := io.ReadAll(response.Body)
	if !bytes.Contains(payload, []byte(`"browser_download_not_found"`)) {
		t.Fatalf("failure body=%s", payload)
	}
}

func waitForClose(t *testing.T, body *stagedReadCloser) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if body.closed.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("HTTP handler did not close the host download body")
}
