package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
	"github.com/ChinaKai/AHA2/internal/workspaceterminal"
	"github.com/coder/websocket"
)

func TestWorkspaceTerminalOrigin(t *testing.T) {
	fixture := newTaskDetailFixture(t, false)
	externalOrigin := "https://proxy.example"
	if _, err := fixture.database.UpdateWorkspaceTerminalSettings(t.Context(), nil, &externalOrigin); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	api := &Server{store: fixture.database, logger: slog.New(slog.NewTextHandler(&logs, nil))}
	for _, test := range []struct {
		name, host, method, proto string
		origins                   []string
		tls, loopback, required   bool
		wantRejection             string
	}{
		{name: "direct http", host: "internal.example:8766", origins: []string{"http://internal.example:8766"}},
		{name: "direct TLS", host: "internal.example", tls: true, origins: []string{"https://internal.example"}},
		{name: "loopback forwarded TLS", host: "localhost", loopback: true, proto: "HTTPS", origins: []string{"https://localhost"}},
		{name: "remote forwarded TLS", host: "internal.example", proto: "https", origins: []string{"https://internal.example"}, wantRejection: "origin_mismatch"},
		{name: "external", host: "internal.example", origins: []string{"https://proxy.example"}},
		{name: "normalized external", host: "internal.example", origins: []string{"HTTPS://PROXY.Example:443/"}},
		{name: "external host mismatch", host: "internal.example", origins: []string{"https://proxy.example.evil"}, wantRejection: "origin_mismatch"},
		{name: "external scheme mismatch", host: "internal.example", origins: []string{"http://proxy.example"}, wantRejection: "origin_mismatch"},
		{name: "external port mismatch", host: "internal.example", origins: []string{"https://proxy.example:8443"}, wantRejection: "origin_mismatch"},
		{name: "path and query", host: "internal.example", origins: []string{"https://proxy.example/secret?token=secret"}, wantRejection: "invalid_origin"},
		{name: "empty query", host: "internal.example", origins: []string{"https://proxy.example?"}, wantRejection: "invalid_origin"},
		{name: "encoded slash", host: "internal.example", origins: []string{"https://proxy.example/%2F"}, wantRejection: "invalid_origin"},
		{name: "userinfo", host: "internal.example", origins: []string{"https://user:secret@proxy.example"}, wantRejection: "invalid_origin"},
		{name: "missing optional", host: "internal.example"},
		{name: "missing websocket", host: "internal.example", method: http.MethodGet, required: true, wantRejection: "missing_origin"},
		{name: "multiple websocket origins", host: "internal.example", method: http.MethodGet, required: true, origins: []string{"https://proxy.example", "https://proxy.example"}, wantRejection: "multiple_origins"},
		{name: "multiple delete origins with empty first", host: "internal.example", method: http.MethodDelete, origins: []string{"", "https://proxy.example"}, wantRejection: "multiple_origins"},
		{name: "combined origins", host: "internal.example", origins: []string{"https://proxy.example, https://proxy.example"}, wantRejection: "multiple_origins"},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs.Reset()
			req := httptest.NewRequest(test.method, "http://"+test.host, nil)
			if test.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if test.loopback {
				req.RemoteAddr = "127.0.0.1:12345"
			}
			req.Header.Set("X-Forwarded-Proto", test.proto)
			req.Header["Origin"] = test.origins
			req.Header.Set("Cookie", "session=secret")
			req.Header.Set("X-CSRF-Token", "secret")
			res := httptest.NewRecorder()
			if got := api.checkWorkspaceTerminalOrigin(res, req, test.required); got != (test.wantRejection == "") {
				t.Fatalf("allowed = %v, response = %s", got, res.Body)
			}
			if test.wantRejection == "" {
				return
			}
			if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "origin_rejected") {
				t.Fatalf("rejected response = %d %s", res.Code, res.Body)
			}
			for _, field := range []string{"level=WARN", "stage=origin_rejected", "peer=" + req.RemoteAddr,
				"origin=", "host=" + test.host, "reason=" + test.wantRejection} {
				if !strings.Contains(logs.String(), field) {
					t.Fatalf("missing origin diagnostic %q", field)
				}
			}
			if strings.Contains(logs.String(), "secret") {
				t.Fatal("origin diagnostic logged a credential")
			}
		})
	}
	nonDefaultPort := "https://proxy.example:8443"
	if _, err := fixture.database.UpdateWorkspaceTerminalSettings(t.Context(), nil, &nonDefaultPort); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"https://PROXY.EXAMPLE:8443/", "https://proxy.example"} {
		req := httptest.NewRequest(http.MethodDelete, "http://internal.example/terminal", nil)
		req.Header.Set("Origin", origin)
		if allowed := api.checkWorkspaceTerminalOrigin(httptest.NewRecorder(), req, false); allowed != strings.Contains(origin, ":8443") {
			t.Fatalf("allowed = %v for %s with configured port 8443", allowed, origin)
		}
	}
}

func TestWorkspaceTerminalAccessRecheckDistinguishesQueryFailure(t *testing.T) {
	fixture := newTaskDetailFixture(t, false)
	loginURL, err := url.Parse(fixture.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var login domain.Session
	for _, cookie := range fixture.client.Jar.Cookies(loginURL) {
		if cookie.Name == sessionCookieName {
			login, err = authServiceForTest(fixture.database).Authenticate(t.Context(), cookie.Value)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if login.ID == "" {
		t.Fatal("fixture login missing")
	}

	api := &Server{store: fixture.database}
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/tasks/test/workspace-terminal", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	if err := api.workspaceTerminalRecheck(t.Context(), request, login); err != nil {
		t.Fatalf("valid login rejected: %v", err)
	}

	checkResponse := func(err error, status int, code string) {
		t.Helper()
		if err == nil {
			t.Fatalf("access check unexpectedly succeeded, want %s", code)
		}
		recorder := httptest.NewRecorder()
		workspaceTerminalError(recorder, err)
		if recorder.Code != status || !strings.Contains(recorder.Body.String(), `"error":"`+code+`"`) {
			t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body)
		}
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	err = api.workspaceTerminalRecheck(ctx, request, login)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errWorkspaceTerminalAccessRevoked) {
		t.Fatalf("timeout misclassified: %v", err)
	}
	checkResponse(err, 503, "workspace_terminal_check_failed")

	request.RemoteAddr = "192.0.2.10:12345"
	checkResponse(api.workspaceTerminalRecheck(t.Context(), request, login), 403, "workspace_terminal_access_revoked")
	if err := fixture.database.RevokeSession(t.Context(), login.ID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	request.RemoteAddr = "127.0.0.1:12345"
	checkResponse(api.workspaceTerminalRecheck(t.Context(), request, login), 403, "workspace_terminal_access_revoked")
}

func TestWorkspaceTerminalWatchAccess(t *testing.T) {
	busy := workspaceTerminalCheckError{errors.New("database is locked (5) (SQLITE_BUSY)")}
	for _, test := range []struct {
		name           string
		recheck        func(ctx context.Context, attempt int) error
		wantCloseAfter time.Duration
	}{
		{
			name: "timeout and busy survive; a fresh check renews the grace period",
			recheck: func(ctx context.Context, attempt int) error {
				switch attempt {
				case 1:
					<-ctx.Done()
					return workspaceTerminalCheckError{ctx.Err()}
				case 3:
					time.Sleep(time.Second)
					return nil
				default:
					return busy
				}
			},
			wantCloseAfter: 3*workspaceTerminalRecheckInterval + workspaceTerminalAccessGrace,
		},
		{
			name: "a definitive denial closes after a transient failure",
			recheck: func(_ context.Context, attempt int) error {
				if attempt == 1 {
					return busy
				}
				return errWorkspaceTerminalAccessRevoked
			},
			wantCloseAfter: 2 * workspaceTerminalRecheckInterval,
		},
		{
			name: "the grace period ends during a blocked database call",
			recheck: func(context.Context, int) error {
				time.Sleep(workspaceTerminalAccessGrace)
				return nil
			},
			wantCloseAfter: workspaceTerminalAccessGrace,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				logger := slog.New(slog.DiscardHandler)
				attempt := 0
				finished := make(chan struct{})
				go func() {
					defer close(finished)
					watchWorkspaceTerminalAccess(ctx, cancel, logger, func(ctx context.Context) error {
						attempt++
						return test.recheck(ctx, attempt)
					}, func(context.Context) error { return nil })
				}()

				time.Sleep(test.wantCloseAfter - time.Nanosecond)
				synctest.Wait()
				if ctx.Err() != nil {
					t.Fatalf("socket canceled before %v: %v", test.wantCloseAfter, ctx.Err())
				}
				time.Sleep(time.Nanosecond)
				synctest.Wait()
				if ctx.Err() == nil {
					t.Fatalf("socket still active at %v", test.wantCloseAfter)
				}
				<-finished
			})
		})
	}
}

func TestWorkspaceTerminalOwnerLANWebSocketLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" {
		t.Skip("exercise the Unix PTY shell and stty")
	}
	shell := "/bin/sh"
	if bash, err := exec.LookPath("bash"); err == nil {
		shell = bash
	}
	t.Setenv("SHELL", shell)
	t.Setenv("HOME", t.TempDir())
	var watchQueries atomic.Bool
	var loginReads, settingsReads atomic.Int32
	blocked := make(chan time.Time, 1)
	retrying := make(chan struct{})
	unblock := make(chan struct{})
	releaseQuery := sync.OnceFunc(func() { close(unblock) })
	store.SetSQLQueryObserverForTest(func(query string) {
		if !watchQueries.Load() {
			return
		}
		switch {
		case strings.Contains(query, "FROM sessions WHERE token_hash"):
			switch loginReads.Add(1) {
			case 1:
				blocked <- time.Now()
				<-unblock
			case 2:
				close(retrying)
			}
		case strings.Contains(query, "FROM security_settings"):
			settingsReads.Add(1)
		}
	})
	t.Cleanup(func() { store.SetSQLQueryObserverForTest(nil) })
	fixture := newTaskDetailFixture(t, false)
	store.SetSQLQueryObserverForTest(nil)
	t.Cleanup(releaseQuery)
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
	var logs bytes.Buffer
	api := New(Config{Store: fixture.database, Auth: authServiceForTest(fixture.database), App: fixture.app,
		AgentCapabilities: capabilities, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	handler := api.Handler()
	const externalHost = "proxy.example"
	const externalOrigin = "https://" + externalHost
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-LAN") == "yes" {
			r.RemoteAddr = "192.0.2.10:12345"
		}
		if r.Header.Get("X-Test-Preserve-Host") == "yes" {
			r.Host = externalHost
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
	check := func(method, path, body string, headers http.Header, client *http.Client, code int, contains string) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header = headers.Clone()
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		if client == fixture.client && len(req.Header.Values("X-CSRF-Token")) == 0 {
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
		return string(data)
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
	const enableLAN = `{"workspace_terminal_lan":true}`
	const disableLAN = `{"workspace_terminal_lan":false}`
	const externalSetting = `{"workspace_terminal_external_origin":"HTTPS://PROXY.Example:443/"}`
	const normalizedExternalSetting = `"workspace_terminal_external_origin":"https://proxy.example"`
	const loopbackRequired = "workspace_terminal_lan_enable_requires_loopback"
	check(http.MethodPut, setting, enableLAN, lan, fixture.client, 403, loopbackRequired)
	check(http.MethodPut, setting, enableLAN, http.Header{"X-Forwarded-For": {"192.0.2.10"}}, fixture.client, 403, loopbackRequired)
	if stored, err := fixture.database.SecuritySettings(t.Context()); err != nil || stored.WorkspaceTerminalLAN {
		t.Fatalf("rejected enable changed the persisted setting: %+v %v", stored, err)
	}
	check(http.MethodPut, setting, enableLAN, nil, fixture.client, 200, `"workspace_terminal_lan":true`)
	if stored, err := fixture.database.SecuritySettings(t.Context()); err != nil || !stored.WorkspaceTerminalLAN || !stored.ValidateOrigin {
		t.Fatalf("terminal setting was not persisted independently: %+v %v", stored, err)
	}
	check(http.MethodPut, "/api/v1/settings/security", `{"validate_origin":true,"access_scope":"local"}`, nil, fixture.client, 200, `"workspace_terminal_lan":true`)
	check(http.MethodPost, base, size, lan, fixture.client, 403, "access_scope_local_only")
	check(http.MethodPut, "/api/v1/settings/security", `{"validate_origin":true,"access_scope":"lan"}`, nil, fixture.client, 200, `"workspace_terminal_lan":true`)

	direct := http.Header{"Origin": {server.URL}}
	t.Run("settings require a direct origin", func(t *testing.T) {
		proxy := http.Header{"Origin": {externalOrigin}, "X-Test-Lan": {"yes"}, "X-Test-Preserve-Host": {"yes"}}
		check(http.MethodPut, setting, externalSetting, proxy, http.DefaultClient, 401, "authentication_required")
		noCSRF := direct.Clone()
		noCSRF.Set("X-CSRF-Token", "")
		check(http.MethodPut, setting, externalSetting, noCSRF, fixture.client, 403, "csrf_rejected")
		check(http.MethodPut, setting, externalSetting, proxy, fixture.client, 403, "origin_rejected")
		check(http.MethodPut, setting, externalSetting, http.Header{"Origin": {"", server.URL}}, fixture.client, 403, "origin_rejected")
		check(http.MethodPut, setting, `{"workspace_terminal_external_origin":"https://proxy.example/path"}`, direct, fixture.client, 400, "workspace_terminal_invalid_external_origin")
		check(http.MethodGet, "/api/v1/settings/security", "", nil, fixture.client, 200, `"workspace_terminal_external_origin":""`)

		// A non-loopback peer making a direct, same-origin request can save
		// the address, while enabling LAN still requires a loopback request.
		directLAN := direct.Clone()
		directLAN.Set("X-Test-LAN", "yes")
		check(http.MethodPut, setting, enableLAN, directLAN, fixture.client, 403, loopbackRequired)
		check(http.MethodPut, setting, externalSetting, directLAN, fixture.client, 200, normalizedExternalSetting)
	})

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

	t.Run("local startup failure is logged", func(t *testing.T) {
		missingShell := filepath.Join(worktree, "missing-shell")
		t.Setenv("SHELL", missingShell)
		check(http.MethodPost, base, size, nil, fixture.client, 503, "workspace_terminal_start_failed")
		for _, field := range []string{"task=" + fixture.task.ID, "workspace=" + fixture.task.WorkspaceID,
			"transport=local", "stage=start_local", "error=", missingShell} {
			if !strings.Contains(logs.String(), field) {
				t.Fatalf("missing startup diagnostic %q", field)
			}
		}
	})

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
	createProxiedTerminal := func(t *testing.T, headers http.Header) string {
		t.Helper()
		body := check(http.MethodPost, base, size, headers, fixture.client, 201, `"id":`)
		var terminal workspaceterminal.Info
		if err := json.Unmarshal([]byte(body), &terminal); err != nil || terminal.ID == "" {
			t.Fatalf("could not decode created terminal: %v", err)
		}
		return base + "/" + terminal.ID
	}
	for _, validateOrigin := range []bool{false, true} {
		t.Run("same-host proxy validate_origin="+strconv.FormatBool(validateOrigin), func(t *testing.T) {
			api.setOriginValidation(validateOrigin)
			// The test server sees a loopback peer and Host, as it would
			// after proxying. Supply the external Origin without forwarding headers.
			proxy := http.Header{"Origin": {externalOrigin}}
			check(http.MethodPut, setting, disableLAN, direct, fixture.client, 200, `"workspace_terminal_lan":false`)
			check(http.MethodPut, setting, enableLAN, proxy, fixture.client, 403, "origin_rejected")
			check(http.MethodPut, setting, disableLAN, proxy, fixture.client, 403, "origin_rejected")
			check(http.MethodPut, setting, `{"workspace_terminal_external_origin":""}`, proxy, fixture.client, 403, "origin_rejected")
			check(http.MethodPut, setting, externalSetting, direct, fixture.client, 200, normalizedExternalSetting)

			check(http.MethodPost, base, size, proxy, fixture.client, 403, "workspace_terminal_lan_locked")
			dial(externalOrigin, proxy, "", 403)
			check(http.MethodDelete, item, "", proxy, fixture.client, 403, "workspace_terminal_lan_locked")

			check(http.MethodPut, setting, enableLAN, direct, fixture.client, 200, `"workspace_terminal_lan":true`)
			policy := strconv.FormatBool(validateOrigin)
			check(http.MethodPut, "/api/v1/settings/security", `{"validate_origin":`+policy+`,"access_scope":"local"}`, direct, fixture.client, 200, `"access_scope":"local"`)
			check(http.MethodPost, base, size, proxy, fixture.client, 403, "workspace_terminal_lan_locked")
			check(http.MethodPut, "/api/v1/settings/security", `{"validate_origin":`+policy+`,"access_scope":"lan"}`, direct, fixture.client, 200, `"access_scope":"lan"`)
			proxiedItem := createProxiedTerminal(t, proxy)
			directSocketURL := socketURL
			socketURL = "ws" + strings.TrimPrefix(server.URL, "http") + proxiedItem + "/ws"
			defer func() { socketURL = directSocketURL }()
			dial(externalOrigin, proxy, "", 101)
			check(http.MethodDelete, proxiedItem, "", proxy, fixture.client, 200, `"ok":true`)
		})
	}
	for _, name := range []string{"Host preserved", "Host rewritten"} {
		t.Run(name, func(t *testing.T) {
			proxy := lan.Clone()
			proxy.Set("Origin", externalOrigin)
			if name == "Host preserved" {
				proxy.Set("X-Test-Preserve-Host", "yes")
			}
			noCSRF := proxy.Clone()
			noCSRF.Set("X-CSRF-Token", "")
			check(http.MethodPost, base, size, noCSRF, fixture.client, 403, "csrf_rejected")
			proxiedItem := createProxiedTerminal(t, proxy)
			directSocketURL := socketURL
			socketURL = "ws" + strings.TrimPrefix(server.URL, "http") + proxiedItem + "/ws"
			defer func() { socketURL = directSocketURL }()
			dial("https://wrong.example", proxy, "", 403)
			crossSite := proxy.Clone()
			crossSite.Set("Sec-Fetch-Site", "cross-site")
			dial(externalOrigin, crossSite, "", 403)
			dial(externalOrigin, proxy, "", 101)
			check(http.MethodDelete, proxiedItem, "", noCSRF, fixture.client, 403, "csrf_rejected")
			check(http.MethodDelete, proxiedItem, "", proxy, fixture.client, 200, `"ok":true`)
		})
	}
	check(http.MethodPut, setting, `{"workspace_terminal_external_origin":""}`, nil, fixture.client, 200, `"workspace_terminal_external_origin":""`)
	for _, preserve := range []string{"yes", "no"} {
		proxy := http.Header{"Origin": {externalOrigin}, "X-Test-Lan": {"yes"}, "X-Test-Preserve-Host": {preserve}}
		check(http.MethodPost, base, size, proxy, fixture.client, 403, "origin_rejected")
		check(http.MethodDelete, item, "", proxy, fixture.client, 403, "origin_rejected")
		dial(externalOrigin, proxy, "", 403)
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
	watchQueries.Store(true)
	var blockedAt time.Time
	select {
	case blockedAt = <-blocked:
	case <-ctx.Done():
		t.Fatal("terminal did not recheck the login")
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"cols":106,"rows":39}`)); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("printf 'WS''-MARKER:%s\\n' \"$(pwd -P)\"; stty size\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(conn, "WS-MARKER:"+worktree, "39 106")
	if got := settingsReads.Load(); got != 0 {
		t.Fatalf("terminal input queried security settings %d times while the watcher was blocked", got)
	}
	// Let the first recheck context expire inside the driver. The same
	// socket must still handle input after the watcher retries.
	time.Sleep(time.Until(blockedAt.Add(workspaceTerminalRecheckTimeout)))
	releaseQuery()
	select {
	case <-retrying:
	case <-ctx.Done():
		t.Fatal("terminal did not retry after the login lookup timed out")
	}
	watchQueries.Store(false)
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("printf 'WS''-RESUMED\\n'\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(conn, "WS-RESUMED")
	stop()
	ctx, stop = context.WithTimeout(t.Context(), 15*time.Second)
	defer stop()

	check(http.MethodPut, setting, `{"workspace_terminal_lan":false}`, lan, fixture.client, 200, `"workspace_terminal_lan":false`)
	lockCtx, stopLock := context.WithTimeout(t.Context(), workspaceTerminalRecheckInterval+workspaceTerminalRecheckTimeout+time.Second)
	defer stopLock()
	for {
		_, _, err := conn.Read(lockCtx)
		if err != nil {
			if lockCtx.Err() != nil {
				t.Fatal("turning off LAN did not disconnect an active LAN socket")
			}
			break
		}
	}
	dial(server.URL, lan, "", 403)
	conn = dial(server.URL, nil, "", 101)
	readUntil(conn, "WS-MARKER:"+worktree, "39 106")
	// The background job ignores SIGHUP and has its own process group when
	// the interactive shell uses job control.
	jobCommand := "nohup sleep 120 </dev/null >/dev/null 2>&1 & printf 'PTY''-JOB:%s:JOB''-READY\\n' \"$!\"\n"
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(jobCommand)); err != nil {
		t.Fatal(err)
	}
	jobOutput := readUntil(conn, "PTY-JOB:", ":JOB-READY")
	jobMatch := regexp.MustCompile(`PTY-JOB:([0-9]+):JOB-READY`).FindStringSubmatch(jobOutput)
	if jobMatch == nil {
		t.Fatal("no background PTY job PID in shell output")
	}
	jobPID, err := strconv.Atoi(jobMatch[1])
	if err != nil {
		t.Fatal(err)
	}
	job, err := os.FindProcess(jobPID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = job.Kill() })
	jobRunning := func() bool {
		output, _ := exec.Command("ps", "-p", jobMatch[1], "-o", "stat=").Output()
		state := strings.TrimSpace(string(output))
		return state != "" && !strings.HasPrefix(state, "Z")
	}
	if !jobRunning() {
		t.Fatal("background PTY job was not running before DELETE")
	}
	// Explicit close ends the PTY, including the background job, and makes
	// its ID unusable.
	check(http.MethodDelete, item, "", nil, fixture.client, 200, `"ok":true`)
	deadline := time.Now().Add(3 * time.Second)
	for jobRunning() {
		if time.Now().After(deadline) {
			t.Fatal("nohup sleep still running after terminal DELETE")
		}
		time.Sleep(25 * time.Millisecond)
	}
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
