package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestPasswordChangeInvalidatesOtherSessionsAndOldPassword(t *testing.T) {
	server := newAuthTestServer(t)
	currentClient := newCookieClient(t)
	currentCSRF := registerOwner(t, currentClient, server.URL)
	otherClient := newCookieClient(t)
	loginOwner(t, otherClient, server.URL, "correct-horse-battery")

	response := requestJSON(t, currentClient, http.MethodPost, server.URL+"/api/v1/auth/password", map[string]any{
		"current_password": "wrong-password", "new_password": "new-correct-password",
	}, currentCSRF)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong current password status = %d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, currentClient, http.MethodPost, server.URL+"/api/v1/auth/password", map[string]any{
		"current_password": "correct-horse-battery", "new_password": "new-correct-password",
	}, currentCSRF)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("password change status = %d", response.StatusCode)
	}
	response.Body.Close()
	assertAuthenticated(t, currentClient, server.URL, true)
	assertAuthenticated(t, otherClient, server.URL, false)

	oldPasswordClient := newCookieClient(t)
	response = requestJSON(t, oldPasswordClient, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{
		"username": "owner", "password": "correct-horse-battery",
	}, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password login status = %d", response.StatusCode)
	}
	response.Body.Close()
	loginOwner(t, newCookieClient(t), server.URL, "new-correct-password")
}

func TestPasswordRecoveryRotatesSessionsAndCredentials(t *testing.T) {
	server := newAuthTestServer(t)
	firstClient := newCookieClient(t)
	registerOwner(t, firstClient, server.URL)
	secondClient := newCookieClient(t)
	loginOwner(t, secondClient, server.URL, "correct-horse-battery")
	recoveryClient := newCookieClient(t)

	response := requestJSON(t, recoveryClient, http.MethodPost, server.URL+"/api/v1/auth/recover", map[string]any{
		"setup_token": "wrong-token", "username": "owner", "new_password": "recovered-password",
	}, "")
	var failure map[string]any
	decodeResponse(t, response, &failure)
	if response.StatusCode != http.StatusUnauthorized || failure["error"] != "recovery_failed" {
		t.Fatalf("wrong recovery token response = %d %#v", response.StatusCode, failure)
	}
	assertAuthenticated(t, firstClient, server.URL, true)
	response = requestJSON(t, recoveryClient, http.MethodPost, server.URL+"/api/v1/auth/recover", map[string]any{
		"setup_token": "setup-test", "username": "unknown-owner", "new_password": "recovered-password",
	}, "")
	var unknownOwner map[string]any
	decodeResponse(t, response, &unknownOwner)
	if response.StatusCode != http.StatusUnauthorized || unknownOwner["error"] != failure["error"] {
		t.Fatalf("unknown owner disclosed a distinct response: %d %#v", response.StatusCode, unknownOwner)
	}

	response = requestJSON(t, recoveryClient, http.MethodPost, server.URL+"/api/v1/auth/recover", map[string]any{
		"setup_token": "setup-test", "username": "owner", "new_password": "recovered-password",
	}, "")
	var recovered map[string]any
	decodeResponse(t, response, &recovered)
	if response.StatusCode != http.StatusOK || recovered["csrf_token"] == "" {
		t.Fatalf("recovery response = %d %#v", response.StatusCode, recovered)
	}
	assertAuthenticated(t, firstClient, server.URL, false)
	assertAuthenticated(t, secondClient, server.URL, false)
	assertAuthenticated(t, recoveryClient, server.URL, true)

	oldPasswordClient := newCookieClient(t)
	response = requestJSON(t, oldPasswordClient, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{
		"username": "owner", "password": "correct-horse-battery",
	}, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password login status = %d", response.StatusCode)
	}
	response.Body.Close()
	loginOwner(t, newCookieClient(t), server.URL, "recovered-password")
}

func TestPasswordEndpointsRejectCrossOriginRequests(t *testing.T) {
	server := newAuthTestServer(t)
	client := newCookieClient(t)
	csrf := registerOwner(t, client, server.URL)

	for _, test := range []struct {
		path    string
		payload map[string]any
		csrf    string
	}{
		{
			path: "/api/v1/auth/password",
			payload: map[string]any{
				"current_password": "correct-horse-battery", "new_password": "new-correct-password",
			},
			csrf: csrf,
		},
		{
			path: "/api/v1/auth/recover",
			payload: map[string]any{
				"setup_token": "setup-test", "username": "owner", "new_password": "recovered-password",
			},
		},
	} {
		body, err := json.Marshal(test.payload)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, server.URL+test.path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://attacker.example")
		request.Header.Set("X-CSRF-Token", test.csrf)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s cross-origin status = %d", test.path, response.StatusCode)
		}
		response.Body.Close()
	}
}

func newAuthTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}),
	}).Handler())
	t.Cleanup(server.Close)
	return server
}

func newCookieClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func loginOwner(t *testing.T, client *http.Client, baseURL, password string) string {
	t.Helper()
	response := requestJSON(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", map[string]any{
		"username": "owner", "password": password,
	}, "")
	var result map[string]any
	decodeResponse(t, response, &result)
	csrf, _ := result["csrf_token"].(string)
	if response.StatusCode != http.StatusOK || csrf == "" {
		t.Fatalf("login failed: status=%d body=%#v", response.StatusCode, result)
	}
	return csrf
}

func assertAuthenticated(t *testing.T, client *http.Client, baseURL string, expected bool) {
	t.Helper()
	response := requestJSON(t, client, http.MethodGet, baseURL+"/api/v1/auth/status", nil, "")
	var result map[string]any
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if authenticated, _ := result["authenticated"].(bool); authenticated != expected {
		t.Fatalf("authenticated = %v, want %v (body=%#v)", authenticated, expected, result)
	}
}
