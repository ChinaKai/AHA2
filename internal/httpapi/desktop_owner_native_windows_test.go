//go:build windows

package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/desktop"
	"github.com/ChinaKai/AHA2/internal/store"
	"github.com/coder/websocket"
	"golang.org/x/sys/windows"
)

type nativeOwnerFixture struct {
	input   io.WriteCloser
	output  *bufio.Scanner
	window  desktop.Window
	initial string
}

func startNativeOwnerFixture(t *testing.T, ctx context.Context) *nativeOwnerFixture {
	t.Helper()
	var source strings.Builder
	for _, name := range []string{"native.cs", "foreground_native.cs", "native_targets.cs"} {
		data, err := os.ReadFile(filepath.Join("..", "desktop", name))
		if err != nil {
			t.Fatal(err)
		}
		source.Write(data)
		source.WriteByte('\n')
	}
	script, err := os.ReadFile(filepath.Join("..", "desktop", "foreground_native_fixture.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := `[Console]::InputEncoding = New-Object System.Text.UTF8Encoding($false); $fixture = [Console]::In.ReadLine() | ConvertFrom-Json; & ([ScriptBlock]::Create($fixture.script))`
	units := utf16.Encode([]rune(bootstrap))
	raw := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(raw[i*2:], unit)
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"),
		"-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden",
		"-EncodedCommand", base64.StdEncoding.EncodeToString(raw))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.WaitDelay = time.Second
	cmd.Env = []string{"PATH=" + system}
	for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "USERPROFILE", "LOCALAPPDATA"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	encoder := json.NewEncoder(input)
	if err := encoder.Encode(map[string]string{"script": string(script)}); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(map[string]string{"source": source.String()}); err != nil {
		t.Fatal(err)
	}
	fixture := &nativeOwnerFixture{input: input, output: bufio.NewScanner(output)}
	var ready struct {
		Ready   bool           `json:"ready"`
		Window  desktop.Window `json:"window"`
		Initial string         `json:"initial_desktop"`
	}
	if !fixture.output.Scan() || json.Unmarshal(fixture.output.Bytes(), &ready) != nil || !ready.Ready {
		t.Fatal("owned native fixture did not become ready")
	}
	fixture.window, fixture.initial = ready.Window, ready.Initial
	return fixture
}

func (f *nativeOwnerFixture) command(operation string, fields map[string]any) bool {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["operation"] = operation
	if json.NewEncoder(f.input).Encode(fields) != nil || !f.output.Scan() {
		return false
	}
	var reply struct {
		OK bool `json:"ok"`
	}
	return json.Unmarshal(f.output.Bytes(), &reply) == nil && reply.OK
}

func TestOwnerNativeTemporaryServiceDesktopPreview(t *testing.T) {
	if os.Getenv("AHA_OWNER_NATIVE_PREVIEW_TEST") != "1" {
		t.Skip("opt-in isolated Windows Owner server with owned desktops and real capture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	// Windows SQLite file locking on the WSL UNC mount is not reliable.
	// A new in-memory store keeps this instance isolated without copying live data.
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task := createHardwareAPITask(t, db)
	provider := desktop.NativeProvider()
	manager := desktop.New(provider)
	defer manager.Close()
	api := New(Config{Store: db, Auth: auth.NewService(db, "setup-test", time.Hour),
		App: app.NewService(db, nil, app.StubExecutor{}), Desktop: manager})
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	t.Logf("isolated real Windows HTTP server %s; new test Owner/database; production 8766 untouched", server.URL)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	csrf := registerOwner(t, client, server.URL)
	base := server.URL + "/api/v1/tasks/" + task.ID + "/desktop"
	fixture := startNativeOwnerFixture(t, ctx)
	var active *desktop.Session
	stop := func() {
		if active == nil {
			return
		}
		response := requestJSON(t, client, http.MethodPost, base+"/stop", map[string]any{"session_id": active.ID}, csrf)
		response.Body.Close()
		active = nil
	}
	defer stop()
	share := func(target desktop.TargetSelection) error {
		endpoint := "/session"
		payload := map[string]any{"target": target, "mode": "foreground", "confirm_foreground": true}
		if active != nil {
			endpoint = "/switch"
			payload["session_id"], payload["revision"] = active.ID, active.Revision
		}
		response := requestJSON(t, client, http.MethodPost, base+endpoint, payload, csrf)
		defer response.Body.Close()
		var body struct {
			Status desktop.Status `json:"status"`
			Error  string         `json:"error"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			return err
		}
		if response.StatusCode >= 300 || body.Status.Session == nil {
			t.Logf("Owner %s rejected: HTTP%d code=%s", endpoint, response.StatusCode, body.Error)
			return &desktop.Error{Code: body.Error}
		}
		active = body.Status.Session
		if active.Controller != "owner" || active.Claimed {
			t.Fatal("test changed Owner control role")
		}
		return nil
	}
	readHTTP := func(label string) {
		t.Helper()
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/observation?session_id="+active.ID+
			"&view_id=owner-native&max_width=800&max_height=600&quality=65", nil)
		response, err := client.Do(request)
		if err != nil {
			t.Errorf("%s HTTP transport: %v", label, err)
			return
		}
		defer response.Body.Close()
		var body struct {
			Observation desktop.Observation `json:"observation"`
			Error       string              `json:"error"`
		}
		if json.NewDecoder(response.Body).Decode(&body) != nil {
			t.Errorf("%s invalid response", label)
			return
		}
		o := body.Observation
		t.Logf("%s Owner HTTP%d error=%s has_image=%t capture_error=%s control_error=%s actions=%d",
			label, response.StatusCode, body.Error, o.Image != "", o.CaptureError, o.ControlError, len(o.InputActions))
		if response.StatusCode != 200 || o.Image == "" {
			t.Errorf("%s Owner HTTP did not yield a frame", label)
			return
		}
		if session := manager.Status(task.ID).Session; session == nil || session.Controller != "owner" || session.Claimed {
			t.Fatal("Owner observation changed role")
		}
		data, err := base64.StdEncoding.DecodeString(o.Image)
		if err != nil {
			t.Error(err)
			return
		}
		if config, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil ||
			config.Width != o.PreviewWidth || config.Height != o.PreviewHeight {
			t.Errorf("%s invalid real image", label)
		}
	}
	readWS := func(label string) {
		t.Helper()
		wctx, done := context.WithTimeout(ctx, 12*time.Second)
		defer done()
		c, _, err := websocket.Dial(wctx, base+"/stream?session_id="+active.ID,
			&websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Origin": []string{server.URL}}})
		if err != nil {
			t.Errorf("%s websocket connect: %v", label, err)
			return
		}
		defer c.CloseNow()
		start, _ := json.Marshal(desktopStreamMessage{Type: "start", CSRFToken: csrf, ViewID: "native-ws",
			MaxWidth: 800, MaxHeight: 600, Quality: 65, IntervalMS: 100})
		if err := c.Write(wctx, websocket.MessageText, start); err != nil {
			t.Error(err)
			return
		}
		frames := 0
		var totalBytes int
		for i := 0; i < 20; i++ {
			kind, data, err := c.Read(wctx)
			if err != nil {
				t.Errorf("%s websocket read: %v", label, err)
				return
			}
			if kind == websocket.MessageText {
				var message struct{ Type, Error string }
				_ = json.Unmarshal(data, &message)
				if message.Type == "error" {
					t.Errorf("%s Owner WS error=%s", label, message.Error)
					return
				}
				continue
			}
			if len(data) < 4 {
				t.Error("invalid binary envelope")
				return
			}
			size := int(binary.BigEndian.Uint32(data[:4]))
			if size > len(data)-4 {
				t.Error("invalid metadata length")
				return
			}
			var frame struct{ Observation desktop.Observation }
			if json.Unmarshal(data[4:4+size], &frame) != nil {
				t.Error("invalid frame metadata")
				return
			}
			o := frame.Observation
			totalBytes += len(data) - 4 - size
			if config, _, err := image.DecodeConfig(bytes.NewReader(data[4+size:])); err != nil ||
				config.Width != o.PreviewWidth || config.Height != o.PreviewHeight {
				t.Errorf("%s WS frame has no decodable image", label)
				return
			}
			if o.ControlError != "" || o.CaptureError != "" {
				t.Logf("%s WS frame=%d capture_error=%s control_error=%s actions=%d",
					label, frames+1, o.CaptureError, o.ControlError, len(o.InputActions))
			}
			frames++
			ack, _ := json.Marshal(desktopStreamMessage{Type: "ack", FrameID: o.ID,
				MaxWidth: 800, MaxHeight: 600, Quality: 65, IntervalMS: 100})
			if err := c.Write(wctx, websocket.MessageText, ack); err != nil {
				t.Error("native stream ACK failed", err)
				return
			}
			if frames == 12 {
				t.Logf("%s Owner WS frames=%d decoded_bytes=%d; no role handoff", label, frames, totalBytes)
				if session := manager.Status(task.ID).Session; session == nil || session.Controller != "owner" || session.Claimed {
					t.Fatal("Owner stream changed role")
				}
				return
			}
		}
		t.Errorf("%s no binary frame", label)
	}
	if !fixture.command("own_activation", nil) {
		t.Fatal("could not activate owned fixture")
	}
	time.Sleep(500 * time.Millisecond)
	if err := share(desktop.TargetSelection{Kind: "window", WindowID: fixture.window.ID}); err != nil {
		t.Fatal("owned window share failed", err)
	}
	readHTTP("owned-window")
	readWS("owned-window")
	stop()
	catalog, err := provider.(desktop.TargetProvider).Targets(ctx)
	if err != nil {
		t.Fatal("native catalog failed", desktop.ErrorCode(err))
	}
	t.Logf("actual monitor count=%d; virtual desktops initially=%d; test creates two temporary desktops sequentially",
		len(catalog.Monitors), len(catalog.Desktops))
	for i := 0; i < 2; i++ {
		func() {
			if !fixture.command("own_activation", nil) {
				t.Fatal("could not focus fixture before desktop creation")
			}
			time.Sleep(350 * time.Millisecond)
			if err := share(desktop.TargetSelection{Kind: "new-desktop"}); err != nil {
				t.Fatal("owned desktop creation failed", err)
			}
			desktopID := active.Window.DesktopID
			if desktopID == "" || desktopID == fixture.initial || !fixture.command("record_desktop", map[string]any{"id": desktopID}) {
				t.Fatal("cannot register owned desktop for safe cleanup")
			}
			defer func() {
				stop()
				if !fixture.command("cleanup_desktop", map[string]any{"id": desktopID}) {
					t.Error("owned desktop cleanup failed")
				}
				if !fixture.command("current_matches_initial", nil) {
					t.Error("current desktop did not return to initial desktop")
				}
			}()
			for index, monitor := range catalog.Monitors {
				if err := share(desktop.TargetSelection{Kind: "desktop", DesktopID: desktopID, MonitorID: monitor.ID}); err != nil {
					t.Error("same owned desktop monitor selection failed", err)
					continue
				}
				label := "empty-owned-desktop-" + string(rune('1'+i)) + "-monitor-" + string(rune('1'+index))
				readHTTP(label)
				readWS(label)
			}
			if !fixture.command("move_to_created", nil) || !fixture.command("own_activation", nil) {
				t.Error("cannot place owned fixture in owned desktop")
				return
			}
			time.Sleep(400 * time.Millisecond)
			readHTTP("owned-desktop-with-fixture")
			readWS("owned-desktop-with-fixture")
		}()
	}
	after, err := provider.(desktop.TargetProvider).Targets(ctx)
	if err != nil || len(after.Desktops) != len(catalog.Desktops) {
		t.Error("virtual desktop count did not return to initial value")
	}
	if !fixture.command("close", nil) {
		t.Error("owned fixture close failed")
	}
	t.Log("temporary service stopped; only test-created desktops/windows cleaned; no Agent handoff or user application input")
}
