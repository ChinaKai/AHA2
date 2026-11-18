package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestServeWebServesBrowserNewTabAsHTTPPage(t *testing.T) {
	server := &Server{web: fstest.MapFS{
		"index.html":          &fstest.MapFile{Data: []byte("<!doctype html><title>app</title>")},
		"browser-newtab.html": &fstest.MapFile{Data: []byte("<!doctype html><title>AHA 新标签页</title>")},
	}}
	for _, target := range []string{"/browser-newtab", "/browser-newtab/"} {
		recorder := httptest.NewRecorder()
		server.serveWeb(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", target, recorder.Code)
		}
		if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
			t.Fatalf("%s: content type = %q, want text/html", target, contentType)
		}
		if !strings.Contains(recorder.Body.String(), "AHA 新标签页") {
			t.Fatalf("%s: the SPA fallback hid the static start page: %q", target, recorder.Body.String())
		}
	}
}

func TestServeWebUnknownRouteStillFallsBackToSPA(t *testing.T) {
	server := &Server{web: fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>app</title>")},
	}}
	recorder := httptest.NewRecorder()
	server.serveWeb(recorder, httptest.NewRequest(http.MethodGet, "/tasks/123", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "<title>app</title>") {
		t.Fatalf("SPA fallback changed: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}
