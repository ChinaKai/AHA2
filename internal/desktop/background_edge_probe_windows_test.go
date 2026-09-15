//go:build windows

package desktop

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

//go:embed background_browser_probe.cs
var backgroundBrowserProbe string

//go:embed background_msaa_probe.cs
var backgroundMsaaProbe string

func TestBackgroundOwnedEdgeAccessibilityProbe(t *testing.T) {
	if os.Getenv("AHA_BACKGROUND_EDGE_PROBE") != "1" {
		t.Skip("opt-in; only temporary-profile Edge and local fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	p := NativeProvider().(*nativeProvider)
	defer p.Close()
	var manager *Manager
	defer func() {
		if manager != nil {
			manager.Close()
		}
	}()
	var guardian *foregroundNativeOwnedFixture
	var monitor string
	if os.Getenv("AHA_EDGE_OTHER_DESKTOP") == "1" {
		guardian = startForegroundFixture(t, ctx)
		catalog, err := p.Targets(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range catalog.Monitors {
			if m.Primary {
				monitor = m.ID
			}
		}
		created, err := p.SelectTarget(ctx, TargetSelection{Kind: "new-desktop", MonitorID: monitor})
		if err != nil {
			t.Fatal(err)
		}
		guardian.command("record_desktop", map[string]any{"id": created.DesktopID})
		t.Logf("owned desktop created=%s restore=%s monitor=%s", created.DesktopID, guardian.initial, monitor)
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			if _, err := p.SelectTarget(cleanup, TargetSelection{Kind: "desktop", DesktopID: created.DesktopID, MonitorID: monitor}); err != nil {
				t.Errorf("owned desktop cleanup: %v", err)
				return
			}
			guardian.command("cleanup_desktop", map[string]any{"id": created.DesktopID})
			if _, err := p.SelectTarget(cleanup, TargetSelection{Kind: "desktop", DesktopID: guardian.initial, MonitorID: monitor}); err != nil {
				t.Errorf("restore original desktop: %v", err)
			}
			guardian.command("current_matches_initial", nil)
			guardian.command("close", nil)
		}()
	}
	var executable string
	for _, folder := range []*windows.KNOWNFOLDERID{windows.FOLDERID_ProgramFilesX86, windows.FOLDERID_ProgramFiles} {
		root, _ := windows.KnownFolderPath(folder, 0)
		candidate := filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe")
		if _, err := os.Stat(candidate); err == nil {
			executable = candidate
			break
		}
	}
	if executable == "" {
		t.Fatal("installed Edge unavailable")
	}
	title := "AHA-background-probe-" + token()
	submitted := make(chan string, 1)
	ready := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/":
			fmt.Fprintf(w, `<!doctype html><html><head><title>%s start</title></head><body>
<h1>Owned background browser test</h1><a href="/input">Fixture navigate</a>
<script>window.onload=()=>requestAnimationFrame(()=>requestAnimationFrame(()=>fetch('/ready?links='+document.querySelectorAll('a').length)))</script></body></html>`, title)
		case "/ready":
			select {
			case ready <- r.URL.Query().Get("links") == "1":
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		case "/input":
			fmt.Fprintf(w, `<!doctype html><title>%s input</title><form action="/done">
<label>Fixture value<input name="value"></label>
<input type="password" aria-label="Fixture password" value="fixture-password-secret">
<input aria-label="Fixture readonly" value="readonly" readonly>
<input aria-label="Fixture disabled" value="disabled" disabled>
<button>Fixture submit</button></form>`, title)
		case "/done":
			select {
			case submitted <- r.URL.Query().Get("value"):
			default:
			}
			fmt.Fprintf(w, "<!doctype html><title>%s done</title><h1>Fixture unread 7</h1>", title)
		}
	}))
	defer server.Close()
	cmd := exec.CommandContext(ctx, executable, "--user-data-dir="+t.TempDir(), "--no-first-run",
		"--no-default-browser-check", "--disable-background-mode", "--disable-extensions",
		"--guest", "--window-size=900,700", "--new-window", server.URL)
	if os.Getenv("AHA_EDGE_FORCE_ACCESSIBILITY") == "1" {
		cmd.Args = append(cmd.Args[:len(cmd.Args)-1], "--force-renderer-accessibility=complete", server.URL)
	}
	if os.Getenv("AHA_EDGE_NO_OCCLUSION") == "1" {
		cmd.Args = append(cmd.Args[:len(cmd.Args)-1], "--disable-backgrounding-occluded-windows",
			"--disable-renderer-backgrounding", "--disable-features=CalculateNativeWinOcclusion", server.URL)
	}
	cmd.WaitDelay = time.Second
	dir, _ := windows.GetSystemDirectory()
	cmd.Env = []string{"PATH=" + dir}
	for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "USERPROFILE", "LOCALAPPDATA"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	rootPID := uint32(cmd.Process.Pid)
	defer func() {
		var handles []windows.Handle
		for pid := range ownBrowserPIDs(rootPID) {
			if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid); err == nil {
				handles = append(handles, h)
			}
		}
		cmd.Process.Kill()
		for _, h := range handles {
			windows.TerminateProcess(h, 1)
			windows.WaitForSingleObject(h, 2000)
			windows.CloseHandle(h)
		}
		cmd.Wait()
	}()
	find := ownEdgeWindowFinder()
	var window Window
	for deadline := time.Now().Add(25 * time.Second); time.Now().Before(deadline); {
		if w, ok := find(ownBrowserPIDs(rootPID), title+" start"); ok {
			window = w
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if window.ID == "" {
		t.Fatal("owned Edge local fixture not ready")
	}
	setupFrame, err := p.ObserveForeground(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ActForeground(ctx, window, Action{Kind: "focus", ElementID: "$surface"}, setupFrame); err != nil {
		t.Fatalf("owned test browser setup activation: %v", err)
	}
	// Initialize only this owned browser's blank page area before background
	// assertions. No foreground operation is permitted after the anchor is set.
	setupFrame, err = p.ObserveForeground(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ActForeground(ctx, window, Action{Kind: "click", ElementID: "$surface", X: 800, Y: 550}, setupFrame); err != nil {
		t.Fatalf("owned browser setup click: %v", err)
	}
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("fixture DOM did not contain link")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("fixture JavaScript onload did not run")
	}
	time.Sleep(500 * time.Millisecond)
	if guardian != nil {
		if _, err := p.SelectTarget(ctx, TargetSelection{Kind: "desktop", DesktopID: guardian.initial, MonitorID: monitor}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(800 * time.Millisecond)
		guardian.command("own_activation", nil)
	} else {
		anchor := startForegroundFixture(t, ctx)
		defer anchor.command("close", nil)
	}
	time.Sleep(500 * time.Millisecond)
	before := backgroundFixtureInputState(t)
	selected, err := p.SelectBackgroundWindow(ctx, window.ID)
	if err != nil {
		t.Fatal(err)
	}
	probe := strings.Replace(foregroundNativeSource, "ObserveBackgroundNative(target)", "BrowserProbeObserve(target)", 1)
	probe = strings.Replace(probe, "ActBackgroundNative(target, action)", "BrowserProbeAct(target, action)", 1)
	source := nativeSource + "\n" + probe + "\n" + backgroundBrowserProbe
	if os.Getenv("AHA_EDGE_MSAA_PROBE") == "1" {
		original := nativeBootstrap
		nativeBootstrap = strings.Replace(nativeBootstrap, "'UIAutomationClient',", "'Accessibility', 'UIAutomationClient',", 1)
		defer func() { nativeBootstrap = original }()
		source = strings.ReplaceAll(source, "BrowserProbeObserve(target)", "BrowserMsaaObserve(target)")
		source = strings.ReplaceAll(source, "BrowserProbeAct(target, action)", "BrowserMsaaAct(target, action)")
		source += "\n" + backgroundMsaaProbe
	}
	var session Session
	if os.Getenv("AHA_EDGE_ADAPTER") == "1" {
		manager = New(p)
		state, err := manager.OpenSharedTarget(ctx, "owned-edge", TargetSelection{Kind: "window", WindowID: window.ID}, "background", false)
		if err != nil {
			t.Fatal(err)
		}
		session = *state.Session
	}
	if manager != nil && os.Getenv("AHA_EDGE_EXPECT_UNAVAILABLE") == "1" {
		o, err := manager.Observe(ctx, session.TaskID, session.ID, "owner")
		if err == nil || ErrorCode(err) != "desktop_browser_page_unavailable" || o.Image != "" || len(o.Elements) != 0 {
			t.Fatalf("unavailable browser page did not fail closed: %v", err)
		}
		if before != backgroundFixtureInputState(t) {
			t.Fatal("unavailable page probe changed input/focus")
		}
		t.Log("unmodified other-desktop browser did not expose a page; refused with explicit error and no image/actions")
		return
	}
	get := func() Observation {
		t.Helper()
		var result nativeResponse
		var err error
		for attempt := 0; attempt < 4; attempt++ {
			if manager != nil {
				result.Observation, err = manager.Observe(ctx, session.TaskID, session.ID, "owner")
			} else {
				result, err = p.callSource(ctx, nativeRequest{Operation: "background_observe", WindowID: selected.ID, DesktopID: selected.DesktopID}, source)
			}
			if err == nil || (!strings.Contains(err.Error(), "native_unavailable") &&
				!strings.Contains(err.Error(), "browser_accessibility_unavailable") && !strings.Contains(err.Error(), "refresh_required") &&
				!strings.Contains(err.Error(), "browser_page_unavailable")) {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("owned browser read: %v", err)
		}
		if before != backgroundFixtureInputState(t) {
			t.Fatal("owned browser observation changed foreground/input")
		}
		if manager != nil && (result.Observation.Image == "" || result.Observation.ControlError != "") {
			t.Fatalf("production browser observation image=%t capture=%s control=%s",
				result.Observation.Image != "", result.Observation.CaptureError, result.Observation.ControlError)
		}
		t.Logf("owned browser exposes %d accessibility elements", len(result.Observation.Elements))
		return result.Observation
	}
	act := func(name, kind, value string) {
		t.Helper()
		var o Observation
		for attempt := 0; attempt < 4; attempt++ {
			o = get()
			for _, e := range o.Elements {
				if e.Name != name {
					continue
				}
				allowed := false
				for _, action := range e.Actions {
					allowed = allowed || action == kind
				}
				if !allowed {
					continue
				}
				var err error
				if manager != nil {
					request := ActionRequest{SessionID: session.ID, Revision: o.Revision, ObservationID: o.ID,
						Action: Action{Kind: kind, ElementID: e.ID, Value: value}}
					_, err = manager.Act(ctx, session.TaskID, "owner", request)
					if err == nil {
						if _, replay := manager.Act(ctx, session.TaskID, "owner", request); replay == nil {
							t.Fatal("same browser observation authorized a replay")
						}
					}
				} else {
					_, err = p.callSource(ctx, nativeRequest{Operation: "background_act", WindowID: selected.ID, DesktopID: selected.DesktopID,
						Action: Action{Kind: kind, ElementID: e.ID, Value: value}}, source)
				}
				if err != nil {
					t.Fatalf("owned browser %s: %v", kind, err)
				}
				if before != backgroundFixtureInputState(t) {
					t.Fatal("browser action changed foreground/input")
				}
				time.Sleep(250 * time.Millisecond)
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		for _, e := range o.Elements {
			if strings.Contains(e.Name, "Fixture") || e.Role == "ControlType.Document" {
				t.Logf("owned accessibility role=%s name=%q actions=%v", e.Role, e.Name, e.Actions)
			}
		}
		if snapshot, err := p.ObserveForeground(ctx, selected); err == nil && snapshot.Image != "" {
			if data, err := base64.StdEncoding.DecodeString(snapshot.Image); err == nil {
				if err := os.WriteFile(filepath.Join(".data", "shared-control-test", "background-edge-probe.png"), data, 0600); err != nil {
					t.Log("owned fixture screenshot write failed")
				}
			}
		}
		t.Fatalf("fixture element %q not exposed", name)
	}
	act("Fixture navigate", "invoke", "")
	if manager != nil {
		o := get()
		data, err := base64.StdEncoding.DecodeString(o.Image)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		var protected, readonly, disabled bool
		for _, e := range o.Elements {
			if strings.Contains(e.Name, "fixture-password-secret") || strings.Contains(e.Value, "fixture-password-secret") {
				t.Fatal("password data escaped production browser adapter")
			}
			if e.Role == "password" {
				protected = true
				if e.Name != "" || e.Value != "" || len(e.Actions) != 0 {
					t.Fatal("password metadata exposed")
				}
				r, g, b, _ := img.At(int(e.X+e.Width/2), int(e.Y+e.Height/2)).RGBA()
				if r != 0 || g != 0 || b != 0 {
					t.Fatal("browser password bitmap unmasked")
				}
			}
			if e.Name == "Fixture readonly" {
				readonly = true
				if len(e.Actions) != 0 {
					t.Fatal("readonly browser control writable")
				}
			}
			if e.Name == "Fixture disabled" {
				disabled = true
				if len(e.Actions) != 0 {
					t.Fatal("disabled browser control writable")
				}
			}
		}
		if !protected || !readonly || !disabled {
			t.Fatal("protected browser fixtures missing")
		}
	}
	act("Fixture value", "set_value", "background \u4e2d\u6587")
	act("Fixture submit", "invoke", "")
	select {
	case got := <-submitted:
		if got != "background \u4e2d\u6587" {
			t.Fatalf("owned browser form state mismatch: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background form submission missing")
	}
	if manager != nil {
		o := get()
		found := false
		for _, e := range o.Elements {
			found = found || e.Name == "Fixture unread 7"
		}
		if !found {
			t.Fatal("browser result text not observable")
		}
		manager.Stop(session.TaskID, session.ID)
		if _, err := manager.Observe(ctx, session.TaskID, session.ID, "owner"); err == nil {
			t.Fatal("revoked browser grant still observable")
		}
	}
	t.Log("owned Edge navigation/input/submission without foreground/input changes")
}
