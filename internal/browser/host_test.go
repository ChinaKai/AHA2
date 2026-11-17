package browser_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
