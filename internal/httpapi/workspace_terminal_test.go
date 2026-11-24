package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspaceterminal"
	"github.com/coder/websocket"
)

func TestWorkspaceTerminalOwnerLANWebSocketLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" {
		t.Skip("exercise the Unix PTY shell and stty")
	}
	t.Setenv("SHELL", "/bin/sh")
	fixture := newTaskDetailFixture(t, false)
	worktree, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateTaskWorkspace(t.Context(), fixture.task.ID, worktree, "", "", "", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	capabilities := agentapi.NewCapabilities()
	turn := createCoreTurn(t, fixture.database, fixture.task, "main", domain.NewID("inbox_batch"))
	token, err := capabilities.Issue(fixture.task.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	api := New(Config{Store: fixture.database, Auth: authServiceForTest(fixture.database), App: fixture.app,
		AgentCapabilities: capabilities, AllowCrossOrigin: true})
	handler := api.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-LAN") == "yes" {
			r.RemoteAddr = "192.0.2.10:12345"
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(api.CloseWorkspaceTerminals)
	base := "/api/v1/tasks/" + fixture.task.ID + "/workspace-terminal"
	status := requestJSON(t, fixture.client, http.MethodGet, server.URL+"/api/v1/auth/status", nil, "")
	var authStatus struct {
		CSRF string `json:"csrf_token"`
	}
	decodeResponse(t, status, &authStatus)
	check := func(method, path, body string, headers http.Header, client *http.Client, code int, contains string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header = headers.Clone()
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		if client == fixture.client {
			req.Header.Set("X-CSRF-Token", authStatus.CSRF)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != code || !strings.Contains(string(data), contains) {
			t.Fatalf("%s %s: %d %q, %v", method, path, resp.StatusCode, data, err)
		}
	}
	const size = `{"cols":80,"rows":24}`
	setting := "/api/v1/settings/security/workspace-terminal"
	bearer := http.Header{"Authorization": {"Bearer " + token}}
	lan := http.Header{"X-Test-Lan": {"yes"}}
	check(http.MethodPost, base, size, bearer, http.DefaultClient, 401, "authentication_required")
	check(http.MethodPost, base, size, bearer, fixture.client, 403, "workspace_terminal_owner_required")
	check(http.MethodPut, setting, `{"workspace_terminal_lan":true}`, bearer, http.DefaultClient, 401, "authentication_required")
	check(http.MethodGet, base, "", lan, fixture.client, 200, `"locked":true`)
	check(http.MethodGet, base, "", http.Header{"X-Forwarded-For": {"192.0.2.10"}}, fixture.client, 200, `"locked":true`)
	check(http.MethodGet, base, "", http.Header{"X-Forwarded-Host": {"lan.example"}}, fixture.client, 200, `"locked":true`)
	check(http.MethodGet, base, "", http.Header{"Sec-Fetch-Site": {"cross-site"}}, fixture.client, 403, "origin_rejected")
	check(http.MethodPost, base, size, lan, fixture.client, 403, "workspace_terminal_lan_locked")
	check(http.MethodPost, base, `{"cols":80,"rows":24,"cwd":"/"}`, nil, fixture.client, 400, "workspace_terminal_invalid_size")
	check(http.MethodPost, base+"?root=/", size, nil, fixture.client, 400, "workspace_terminal_invalid_target")
	check(http.MethodPut, setting, `{"workspace_terminal_lan":true}`, lan, fixture.client, 200, `"workspace_terminal_lan":true`)
	if stored, err := fixture.database.SecuritySettings(t.Context()); err != nil || !stored.WorkspaceTerminalLAN || !stored.ValidateOrigin {
		t.Fatalf("terminal setting was not persisted independently: %+v %v", stored, err)
	}
	check(http.MethodPut, "/api/v1/settings/security", `{"validate_origin":true,"access_scope":"local"}`, nil, fixture.client, 200, `"workspace_terminal_lan":true`)
	check(http.MethodPost, base, size, lan, fixture.client, 403, "access_scope_local_only")
	check(http.MethodPut, "/api/v1/settings/security", `{"validate_origin":true,"access_scope":"lan"}`, nil, fixture.client, 200, `"workspace_terminal_lan":true`)

	remote := domain.Workspace{ID: "other-workspace", ProjectID: fixture.task.ProjectID, Name: "Another target",
		Transport: "native", Locality: "local", RootPath: t.TempDir(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := fixture.database.CreateWorkspace(t.Context(), remote); err != nil {
		t.Fatal(err)
	}
	wsl := remote
	wsl.ID, wsl.Transport = "wsl", "wsl"
	if err := fixture.database.CreateWorkspace(t.Context(), wsl); err != nil {
		t.Fatal(err)
	}
	taskURL := "/api/v1/tasks/" + fixture.task.ID
	check(http.MethodGet, taskURL+"/workspace-targets", "", nil, fixture.client, 200, remote.ID)
	check(http.MethodGet, taskURL+"/workspace-target/"+remote.ID+"/state?availability=1", "", nil, fixture.client, 200, `"available":true`)
	check(http.MethodPost, base+"?workspace="+wsl.ID, size, nil, fixture.client, 422, "workspace_terminal_unsupported")

	response := requestJSON(t, fixture.client, http.MethodPost, server.URL+base, map[string]int{"cols": 80, "rows": 24}, authStatus.CSRF)
	var session workspaceterminal.Info
	decodeResponse(t, response, &session)
	if response.StatusCode != http.StatusCreated || session.ID == "" {
		t.Fatalf("create HTTP %d, empty id=%t", response.StatusCode, session.ID == "")
	}
	item := base + "/" + session.ID
	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + item + "/ws"
	dial := func(origin string, extra http.Header, suffix string, expected int) *websocket.Conn {
		t.Helper()
		ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
		defer stop()
		headers := extra.Clone()
		if headers == nil {
			headers = make(http.Header)
		}
		if origin != "" {
			headers.Set("Origin", origin)
		}
		conn, resp, err := websocket.Dial(ctx, socketURL+suffix, &websocket.DialOptions{HTTPClient: fixture.client, HTTPHeader: headers})
		if expected != http.StatusSwitchingProtocols {
			if err == nil || resp == nil || resp.StatusCode != expected {
				t.Fatalf("expected websocket denial %d: %v", expected, err)
			}
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		return conn
	}
	dial("", nil, "", 403)
	dial("http://wrong.example", nil, "", 403)
	dial(strings.Replace(server.URL, "http:", "https:", 1), nil, "", 403)
	dial(server.URL, bearer, "", 403)
	dial(server.URL, nil, "?workspace="+url.QueryEscape(remote.ID), 404)
	conn := dial(server.URL, lan, "", 101)
	ctx, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	readUntil := func(conn *websocket.Conn, needles ...string) string {
		t.Helper()
		var output strings.Builder
		for {
			kind, data, err := conn.Read(ctx)
			if err != nil || kind != websocket.MessageBinary {
				t.Fatalf("terminal output frame kind %v: %v", kind, err)
			}
			output.Write(data)
			found := true
			for _, needle := range needles {
				found = found && strings.Contains(output.String(), needle)
			}
			if found {
				return output.String()
			}
		}
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"cols":106,"rows":39}`)); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("printf 'WS''-MARKER:%s\\n' \"$(pwd -P)\"; stty size\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(conn, "WS-MARKER:"+worktree, "39 106")

	check(http.MethodPut, setting, `{"workspace_terminal_lan":false}`, lan, fixture.client, 200, `"workspace_terminal_lan":false`)
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatal("turning off LAN did not disconnect an active LAN socket")
			}
			break
		}
	}
	dial(server.URL, lan, "", 403)
	conn = dial(server.URL, nil, "", 101)
	readUntil(conn, "WS-MARKER:"+worktree, "39 106")
	// Explicit close ends the PTY and makes its ID unusable.
	check(http.MethodDelete, item, "", nil, fixture.client, 200, `"ok":true`)
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				t.Fatalf("expected normal close on DELETE: %v", err)
			}
			break
		}
	}
	dial(server.URL, nil, "", 404)

	second := requestJSON(t, fixture.client, http.MethodPost, server.URL+base, map[string]int{"cols": 80, "rows": 24}, authStatus.CSRF)
	decodeResponse(t, second, &session)
	socketURL = "ws" + strings.TrimPrefix(server.URL, "http") + base + "/" + session.ID + "/ws"
	conn = dial(server.URL, nil, "", 101)
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("printf 'WS''-LOGOUT\\n'\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(conn, "WS-LOGOUT")
	check(http.MethodPost, "/api/v1/auth/logout", "", nil, fixture.client, 200, `"ok":true`)
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				t.Fatalf("expected normal close on logout: %v", err)
			}
			break
		}
	}
	check(http.MethodGet, base, "", nil, fixture.client, 401, "authentication_required")
}
