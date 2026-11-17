package browser_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/browser"
)

func TestHTTPHostMapsErrorsAndSessionPayloads(t *testing.T) {
	ctx := context.Background()
	const token = "host-token-test"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			writer.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{
				"code": "host_unauthorized", "message": "invalid host token",
			}})
			return
		}
		switch request.URL.Path {
		case "/v1/health":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"host_id": "local", "version": "0.1.0", "platform": "win32", "edge_available": true,
			})
		case "/v1/profiles/profile_1/session":
			if request.Method == http.MethodPost {
				_ = json.NewEncoder(writer).Encode(map[string]any{"session": map[string]any{
					"id": "host-session-1", "profile_id": "profile_1", "state": "ready",
				}})
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"session": map[string]any{
				"id": "host-session-1", "profile_id": "profile_1", "state": "closed",
			}})
		case "/v1/profiles/profile_1/bookmarks":
			if request.URL.Query().Get("session_id") != "host-session-1" {
				writer.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{
					"code": "browser_session_changed", "message": "session mismatch",
				}})
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"bookmarks": []map[string]any{
				{"title": "Mail", "url": "https://mail.example/", "folder": "Bar"},
			}})
		case "/v1/profiles/profile_1/tabs/tab_1/activate":
			if request.Method != http.MethodPost {
				writer.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"tab": map[string]any{
				"id": "tab_1", "url": "https://example.com/login", "title": "Login",
			}})
		case "/v1/profiles/profile_1/tabs":
			if request.URL.Query().Get("session_id") != "host-session-1" {
				writer.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{
					"code": "browser_session_changed", "message": "session mismatch",
				}})
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"tabs": []map[string]any{
				{"id": "host-tab-1", "url": "https://example.com/login", "title": "Login", "active": true},
			}})
		default:
			writer.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{
				"code": "browser_edge_unavailable", "message": "Edge was not found",
			}})
		}
	}))
	defer server.Close()

	host := browser.NewHTTPHost(server.URL, token)
	health, err := host.Health(ctx)
	if err != nil || !health.EdgeAvailable || health.HostID != "local" {
		t.Fatalf("health=%#v err=%v", health, err)
	}
	session, err := host.Open(ctx, "profile_1", "edge")
	if err != nil || session.ID != "host-session-1" || session.State != "ready" {
		t.Fatalf("open=%#v err=%v", session, err)
	}
	closed, err := host.Session(ctx, "profile_1")
	if err != nil || closed.State != "closed" {
		t.Fatalf("session=%#v err=%v", closed, err)
	}
	tabs, err := host.ListTabs(ctx, "profile_1", "host-session-1")
	if err != nil || len(tabs) != 1 || tabs[0].ID != "host-tab-1" || !tabs[0].Active {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	activated, err := host.Activate(ctx, "profile_1", "host-session-1", "tab_1", "fence-1")
	if err != nil || activated.ID != "tab_1" {
		t.Fatalf("activate=%#v err=%v", activated, err)
	}
	bookmarks, err := host.Bookmarks(ctx, "profile_1", "host-session-1")
	if err != nil || len(bookmarks) != 1 || bookmarks[0].URL != "https://mail.example/" {
		t.Fatalf("bookmarks=%#v err=%v", bookmarks, err)
	}
	_, err = host.Open(ctx, "profile_2", "edge")
	if browser.Code(err) != "browser_edge_unavailable" {
		t.Fatalf("edge error mapping: %v", err)
	}
	if browser.Status(err) != http.StatusServiceUnavailable {
		t.Fatalf("edge error status=%d", browser.Status(err))
	}

	badToken := browser.NewHTTPHost(server.URL, "wrong")
	_, err = badToken.Health(ctx)
	if browser.Code(err) != "browser_host_unauthorized" {
		t.Fatalf("unauthorized mapping: %v", err)
	}
}

func TestHTTPHostUnreachableIsTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()
	host := browser.NewHTTPHost(address, "token")
	_, err := host.Health(context.Background())
	if browser.Code(err) != "browser_host_unavailable" {
		t.Fatalf("unreachable mapping: %v", err)
	}
	if browser.Status(err) != http.StatusServiceUnavailable {
		t.Fatalf("unreachable status=%d", browser.Status(err))
	}
}

func TestHTTPHostDistinguishesTimeoutFromUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_ = json.NewEncoder(writer).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()
	host := browser.NewHTTPHostWithTimeout(server.URL, "token", 30*time.Millisecond)
	_, err := host.Health(context.Background())
	if browser.Code(err) != "browser_host_timeout" {
		t.Fatalf("timeout mapping: %v", err)
	}
	if browser.Status(err) != http.StatusGatewayTimeout {
		t.Fatalf("timeout status=%d", browser.Status(err))
	}
}

func TestHTTPHostNeverForwardsDiagnosticText(t *testing.T) {
	const diagnostic = `C:\Users\owner\AppData\Local\AHA2\browser-profiles\browser_profile_1`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{
			"code": "browser_launch_failed", "message": diagnostic,
		}})
	}))
	defer server.Close()
	host := browser.NewHTTPHost(server.URL, "token")
	_, err := host.Health(context.Background())
	if browser.Code(err) != "browser_launch_failed" {
		t.Fatalf("error code=%v", err)
	}
	if strings.Contains(err.Error(), "browser-profiles") || strings.Contains(err.Error(), `C:\`) {
		t.Fatalf("diagnostic path leaked: %v", err)
	}
	if message := browser.PublicMessage(err); strings.Contains(message, "browser-profiles") || strings.Contains(message, `C:\`) {
		t.Fatalf("public message leaked: %q", message)
	}
}

func TestHostClientTimeoutExceedsNavigationDeadline(t *testing.T) {
	if browser.DefaultHostTimeout <= 30*time.Second {
		t.Fatalf("client timeout %v must exceed the host's 30s navigation deadline", browser.DefaultHostTimeout)
	}
}

func TestHTTPHostFrameReturnsImageAndMapsHostFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/profiles/profile_1/tabs/tab_1/frame":
			writer.Header().Set("Content-Type", "image/jpeg")
			_, _ = writer.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
		default:
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{
				"code": "browser_frame_failed", "message": "C:\\Users\\owner\\screenshot failed",
			}})
		}
	}))
	defer server.Close()
	host := browser.NewHTTPHost(server.URL, "token")
	frame, err := host.Frame(context.Background(), "profile_1", "session_1", "tab_1")
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	if frame.ContentType != "image/jpeg" || len(frame.Data) != 4 || frame.Data[0] != 0xff {
		t.Fatalf("frame=%#v", frame)
	}
	_, err = host.Frame(context.Background(), "profile_1", "session_1", "tab_2")
	if browser.Code(err) != "browser_frame_failed" {
		t.Fatalf("frame error mapping: %v", err)
	}
	if strings.Contains(err.Error(), "screenshot failed") || strings.Contains(err.Error(), `C:\`) {
		t.Fatalf("frame diagnostics leaked: %v", err)
	}
}
