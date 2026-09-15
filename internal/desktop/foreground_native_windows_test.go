//go:build windows

package desktop

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:embed foreground_native_fixture.ps1
var foregroundFixtureScript string

type foregroundNativeOwnedFixture struct {
	t       *testing.T
	input   io.WriteCloser
	output  *bufio.Scanner
	window  Window
	initial string
}

type fixturePoint struct{ X, Y float64 }
type foregroundFixtureStatus struct {
	OK                             bool
	Text                           string
	Clicks, Doubles, Wheels, Drags int
	Minimized, Foreground          bool
	Modal, Enabled                 bool
	Edit, Button, Canvas           fixturePoint
	EnterKeys                      int          `json:"enter_keys"`
	TabKeys                        int          `json:"tab_keys"`
	DesktopEdit                    fixturePoint `json:"desktop_edit"`
	DesktopButton                  fixturePoint `json:"desktop_button"`
	DesktopCanvas                  fixturePoint `json:"desktop_canvas"`
}

func startForegroundFixture(t *testing.T, ctx context.Context) *foregroundNativeOwnedFixture {
	t.Helper()
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe"),
		"-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden",
		"-EncodedCommand", encodeNativeCommand(`[Console]::InputEncoding = New-Object System.Text.UTF8Encoding($false); $fixture = [Console]::In.ReadLine() | ConvertFrom-Json; & ([ScriptBlock]::Create($fixture.script))`))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.WaitDelay = time.Second
	cmd.Env = []string{"PATH=" + dir}
	for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "USERPROFILE", "LOCALAPPDATA"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
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
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if err := json.NewEncoder(input).Encode(map[string]string{"script": foregroundFixtureScript}); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(input).Encode(map[string]string{"source": nativeSource + "\n" + foregroundNativeSource}); err != nil {
		t.Fatal(err)
	}
	fixture := &foregroundNativeOwnedFixture{t: t, input: input, output: bufio.NewScanner(output)}
	var ready struct {
		Ready   bool
		Window  Window
		Initial string `json:"initial_desktop"`
	}
	if !fixture.output.Scan() || json.Unmarshal(fixture.output.Bytes(), &ready) != nil || !ready.Ready {
		t.Fatalf("owned foreground fixture did not start: %s", fixture.output.Text())
	}
	fixture.window, fixture.initial = ready.Window, ready.Initial
	fixture.command("own_activation", nil)
	time.Sleep(500 * time.Millisecond)
	if !fixture.command("status", nil).Foreground {
		t.Fatal("owned activation did not receive foreground")
	}
	return fixture
}

func (f *foregroundNativeOwnedFixture) command(operation string, extra map[string]any) foregroundFixtureStatus {
	f.t.Helper()
	if extra == nil {
		extra = map[string]any{}
	}
	extra["operation"] = operation
	if err := json.NewEncoder(f.input).Encode(extra); err != nil {
		f.t.Fatal(err)
	}
	var status foregroundFixtureStatus
	if !f.output.Scan() || json.Unmarshal(f.output.Bytes(), &status) != nil || !status.OK {
		f.t.Fatalf("fixture command %s failed: %s", operation, f.output.Text())
	}
	return status
}

func TestForegroundNativeOwnedWindow(t *testing.T) {
	if os.Getenv("AHA_FOREGROUND_FIXTURE_TEST") != "1" {
		t.Skip("opt-in; real input only to this test's two owned windows")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	fixture := startForegroundFixture(t, ctx)
	p := NativeProvider().(ForegroundProvider)
	t.Cleanup(func() { _ = p.(*nativeProvider).Close() })
	observe := func() Observation {
		t.Helper()
		o, err := p.ObserveForeground(ctx, fixture.window)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	act := func(a Action) {
		t.Helper()
		a.ElementID = "$surface"
		if err := p.ActForeground(ctx, fixture.window, a, observe()); err != nil {
			t.Fatalf("%s: %v", a.Kind, err)
		}
		time.Sleep(60 * time.Millisecond)
	}
	first := observe()
	if first.Image == "" || first.Surface == "" || len(first.Elements) != 0 {
		t.Fatalf("foreground observation incomplete: capture=%s", first.CaptureError)
	}
	s := fixture.command("status", nil)
	act(Action{Kind: "click", X: s.Edit.X, Y: s.Edit.Y})
	act(Action{Kind: "key", Keys: []string{"CTRL", "A"}})
	act(Action{Kind: "text", Value: "foreground \u4e2d\u6587 \U0001f680"})
	if s = fixture.command("status", nil); s.Text != "foreground \u4e2d\u6587 \U0001f680" {
		t.Fatalf("Unicode actual text = %q", s.Text)
	}
	fixture.command("multiline", nil)
	act(Action{Kind: "key", Keys: []string{"CTRL", "A"}})
	act(Action{Kind: "text", Value: "first\r\n\u7b2c\u4e8c\tlast\nend\rfinal"})
	s = fixture.command("status", nil)
	if s.Text != "first\r\n\u7b2c\u4e8c\tlast\r\nend\r\nfinal" || s.EnterKeys != 3 || s.TabKeys != 1 {
		t.Fatalf("multiline input mismatch: text=%q ENTER=%d TAB=%d", s.Text, s.EnterKeys, s.TabKeys)
	}
	act(Action{Kind: "click", X: s.Button.X, Y: s.Button.Y})
	act(Action{Kind: "double_click", X: s.Canvas.X, Y: s.Canvas.Y})
	act(Action{Kind: "drag", X: s.Canvas.X - 50, Y: s.Canvas.Y, EndX: s.Canvas.X + 50, EndY: s.Canvas.Y + 15})
	act(Action{Kind: "scroll", X: s.Canvas.X, Y: s.Canvas.Y, DeltaY: 120})
	s = fixture.command("status", nil)
	if s.Clicks < 1 || s.Doubles < 1 || s.Drags < 1 || s.Wheels < 1 {
		t.Fatalf("real input counts: clicks=%d doubles=%d drags=%d wheels=%d", s.Clicks, s.Doubles, s.Drags, s.Wheels)
	}
	fixture.command("other", nil)
	if fixture.command("status", nil).Foreground {
		t.Fatal("fixture did not actually lose focus before activation regression")
	}
	before := fixtureInputState()
	observe()
	if after := fixtureInputState(); before != after {
		t.Fatal("foreground observation stole focus")
	}
	act(Action{Kind: "click", X: s.Button.X, Y: s.Button.Y})
	if !fixture.command("status", nil).Foreground {
		t.Fatal("foreground action did not repair focus")
	}
	fixture.command("minimize", nil)
	minimized := observe()
	if minimized.Image != "" || minimized.CaptureError != "window_minimized" {
		t.Fatalf("minimized capture unexpectedly available: %s", minimized.CaptureError)
	}
	if !fixture.command("status", nil).Minimized {
		t.Fatal("observation restored minimized fixture")
	}
	act(Action{Kind: "focus"})
	if fixture.command("status", nil).Minimized {
		t.Fatal("focus failed to restore minimized fixture")
	}
	stale := first
	stale.Surface += "-stale"
	if err := p.ActForeground(ctx, fixture.window, Action{Kind: "click", ElementID: "$surface", X: s.Button.X, Y: s.Button.Y}, stale); err == nil || !strings.Contains(err.Error(), "refresh_required") {
		t.Fatalf("stale geometry error = %v", err)
	}
	act(Action{Kind: "click", X: s.Button.X, Y: s.Button.Y})
	// Deterministically interrupt a test-only batch after CTRL down. The parent must release it.
	observation := observe()
	request := nativeRequest{Operation: "foreground_act", WindowID: fixture.window.ID,
		Action:  Action{Kind: "key", ElementID: "$surface", Keys: []string{"CTRL", "A"}},
		Surface: observation.Surface, Width: observation.Width, Height: observation.Height}
	source := strings.Replace(foregroundNativeSource,
		"uint sent = SendInput((uint)batch.Length, batch, Marshal.SizeOf(typeof(Input)));",
		"SendInput(1, new Input[] { batch[0] }, Marshal.SizeOf(typeof(Input))); Thread.Sleep(10000); uint sent = SendInput((uint)batch.Length, batch, Marshal.SizeOf(typeof(Input)));", 1)
	if source == foregroundNativeSource {
		t.Fatal("cancellation test instrumentation did not apply")
	}
	canceled, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := p.(*nativeProvider).callSource(canceled, request, nativeSource+"\n"+source)
		done <- err
	}()
	state := windows.NewLazySystemDLL("user32.dll").NewProc("GetAsyncKeyState")
	down := false
	for i := 0; i < 300; i++ {
		value, _, _ := state.Call(0x11)
		if value&0x8000 != 0 {
			down = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	if err := <-done; err == nil {
		t.Fatal("canceled native input unexpectedly succeeded")
	}
	time.Sleep(100 * time.Millisecond)
	value, _, _ := state.Call(0x11)
	if !down || value&0x8000 != 0 {
		t.Fatalf("real interrupted CTRL batch: observed_down=%t remains_down=%t", down, value&0x8000 != 0)
	}
	t.Logf("actual foreground PNG %dx%d; Unicode, CRLF/LF/CR and TAB, Ctrl+A, click/double/drag/wheel, ordinary lost-focus activation, minimized read-only polling/restore and stale geometry verified",
		first.Width, first.Height)
	t.Log("forced helper cancellation after actual CTRL down released the key in the parent")
	fixture.command("close", nil)
}

func TestForegroundNativeOwnNewDesktop(t *testing.T) {
	if os.Getenv("AHA_FOREGROUND_DESKTOP_TEST") != "1" {
		t.Skip("opt-in; creates and closes only the verified test-created virtual desktop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fixture := startForegroundFixture(t, ctx)
	p := NativeProvider().(ForegroundProvider)
	t.Cleanup(func() { _ = p.(*nativeProvider).Close() })
	window, err := p.NewDesktop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := window.DesktopID
	if window.Kind != "desktop" || id == fixture.initial {
		t.Fatal("new desktop identity was not verified")
	}
	fixture.command("record_desktop", map[string]any{"id": id})
	defer fixture.command("close", nil)
	cleaned := false
	defer func() {
		if !cleaned {
			fixture.command("cleanup_desktop", nil)
		}
	}()
	fixture.command("move_to_created", nil)
	time.Sleep(200 * time.Millisecond)
	fixture.command("own_activation", nil)
	time.Sleep(500 * time.Millisecond)
	if !fixture.command("status", nil).Foreground {
		t.Fatal("new-desktop owned fixture did not receive focus")
	}
	o, err := p.ObserveForeground(ctx, window)
	if err != nil || o.Image == "" || len(o.InputActions) < 2 {
		t.Fatalf("new desktop surface unavailable: %v capture_error=%s", err, o.CaptureError)
	}
	pixels, err := base64.StdEncoding.DecodeString(o.Image)
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(bytes.NewReader(pixels))
	if err != nil || image.Bounds().Dx() != o.Width || image.Bounds().Dy() != o.Height {
		t.Fatalf("desktop PNG invalid: %v", err)
	}
	s := fixture.command("status", nil)
	r, g, b, _ := image.At(int(s.DesktopCanvas.X), int(s.DesktopCanvas.Y)).RGBA()
	if r>>8 != 144 || g>>8 != 238 || b>>8 != 144 {
		t.Fatalf("desktop capture omitted owned fixture pixels: %d,%d,%d", r>>8, g>>8, b>>8)
	}
	act := func(action Action) {
		t.Helper()
		o, err := p.(*nativeProvider).ObservePreview(ctx, window, FrameOptions{MaxWidth: 640, MaxHeight: 360, Quality: 45})
		if err != nil {
			t.Fatal(err)
		}
		if o.Mime != "image/jpeg" || o.PreviewWidth > 640 || o.PreviewHeight > 360 || o.Width < o.PreviewWidth {
			t.Fatal("desktop preview lost its original input geometry")
		}
		action.ElementID = "$surface"
		if err := p.ActForeground(ctx, window, action, o); err != nil {
			t.Fatalf("desktop surface %s failed: %v", action.Kind, err)
		}
		time.Sleep(60 * time.Millisecond)
	}
	act(Action{Kind: "click", X: s.DesktopEdit.X, Y: s.DesktopEdit.Y})
	act(Action{Kind: "key", Keys: []string{"CTRL", "A"}})
	act(Action{Kind: "text", Value: "owned desktop \u4e2d\u6587"})
	act(Action{Kind: "click", X: s.DesktopButton.X, Y: s.DesktopButton.Y})
	s = fixture.command("status", nil)
	if s.Text != "owned desktop \u4e2d\u6587" || s.Clicks != 1 {
		t.Fatalf("desktop surface did not update owned fixture: text=%q clicks=%d", s.Text, s.Clicks)
	}
	wrong := Window{ID: "desktop:00000000-0000-0000-0000-000000000001", Kind: "desktop"}
	if _, err := p.ObserveForeground(ctx, wrong); err == nil || !strings.Contains(err.Error(), "desktop_changed") {
		t.Fatalf("mismatched desktop observation: %v", err)
	}
	o.Window = wrong
	if err := p.ActForeground(ctx, wrong, Action{Kind: "focus", ElementID: "$surface"}, o); err == nil ||
		!strings.Contains(err.Error(), "desktop_changed") {
		t.Fatalf("mismatched desktop input: %v", err)
	}
	fixture.command("cleanup_desktop", nil)
	cleaned = true
	if _, err := p.ObserveForeground(ctx, window); err == nil || !strings.Contains(err.Error(), "desktop_changed") {
		t.Fatalf("old grant observation after actual desktop change: %v", err)
	}
	o.Window = window
	if err := p.ActForeground(ctx, window, Action{Kind: "text", ElementID: "$surface", Value: "must-not-be-typed"}, o); err == nil ||
		!strings.Contains(err.Error(), "desktop_changed") {
		t.Fatalf("old grant input after actual desktop change: %v", err)
	}
	t.Logf("new GUID desktop PNG %dx%d includes owned fixture pixels; resized JPEG surface click/key/Unicode changed real state using original coordinates; mismatched and actual changed-desktop capture/input refused; only test-created desktop cleaned up",
		o.Width, o.Height)
}

func TestForegroundNativeCleanupEvents(t *testing.T) {
	if unsafe.Sizeof(foregroundCleanupInput{}) != 40 {
		t.Fatal("unexpected Windows amd64 INPUT layout")
	}
	if events := cleanupForegroundEvents(nativeRequest{Operation: "act", Action: Action{Kind: "invoke"}}); len(events) != 0 {
		t.Fatal("background operation received physical cleanup")
	}
	events := cleanupForegroundEvents(nativeRequest{Operation: "foreground_act", Action: Action{Kind: "key", Keys: []string{"CTRL", "A"}}})
	if len(events) != 2 {
		t.Fatal("missing release events")
	}
	for _, event := range events {
		if event.kind != 1 || *(*uint32)(unsafe.Add(unsafe.Pointer(&event.data), 4))&2 == 0 {
			t.Fatal("cleanup contains a non-release event")
		}
	}
	events = cleanupForegroundEvents(nativeRequest{Operation: "foreground_act", Action: Action{Kind: "drag", Button: "right"}})
	if len(events) != 1 || events[0].kind != 0 || events[0].data.flags != 16 {
		t.Fatal("mouse cleanup is not right-button release")
	}
	events = cleanupForegroundEvents(nativeRequest{Operation: "foreground_act", Action: Action{Kind: "text", Value: "\r\n\t"}})
	if len(events) != 2 {
		t.Fatal("text cleanup must deduplicate newline and include Tab")
	}
	for i, key := range []uint16{13, 9} {
		if *(*uint16)(unsafe.Pointer(&events[i].data)) != key ||
			*(*uint32)(unsafe.Add(unsafe.Pointer(&events[i].data), 4)) != 2 {
			t.Fatal("newline/Tab cleanup is not a virtual-key release")
		}
	}
}
