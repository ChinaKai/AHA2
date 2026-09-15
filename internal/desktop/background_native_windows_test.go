//go:build windows

package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestBackgroundWorkerLaneAndNoPhysicalCleanup(t *testing.T) {
	pool := newNativeWorkerPool("", "", time.Second)
	defer pool.close()
	pool.background.mu.Lock()
	defer pool.background.mu.Unlock()
	for _, operation := range []string{"background_select", "background_observe", "background_act"} {
		packet, err := json.Marshal(struct {
			Source  string        `json:"source"`
			Request nativeRequest `json:"request"`
		}{nativeSource, nativeRequest{Operation: operation}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.run(context.Background(), packet); err == nil || ErrorCode(err) != "desktop_busy" {
			t.Fatalf("%s did not use the serial background worker: %v", operation, err)
		}
		if events := cleanupForegroundEvents(nativeRequest{Operation: operation, Action: Action{Kind: "key", Keys: []string{"CTRL"}}}); len(events) != 0 {
			t.Fatal("background worker cancellation attempted physical key cleanup")
		}
	}
}

func backgroundFixtureInputState(t *testing.T) [5]uintptr {
	t.Helper()
	focus := fixtureInputState()
	user32 := windows.NewLazySystemDLL("user32.dll")
	var point struct{ X, Y int32 }
	ok, _, _ := user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
	if ok == 0 {
		t.Fatal("cannot inspect test cursor position")
	}
	var down uintptr
	for i, key := range []uintptr{1, 2, 4, 16, 17, 18, 91, 92} {
		state, _, _ := user32.NewProc("GetAsyncKeyState").Call(key)
		if state&0x8000 != 0 {
			down |= 1 << i
		}
	}
	return [5]uintptr{focus[0], focus[1], uintptr(point.X), uintptr(point.Y), down}
}

func TestBackgroundCrossDesktopOwnedWindow(t *testing.T) {
	if os.Getenv("AHA_BACKGROUND_DESKTOP_TEST") != "1" {
		t.Skip("opt-in; setup/cleanup only own fixture and temporary desktop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	fixture := startForegroundFixture(t, ctx)
	fixture.command("background_controls", nil)
	fixture.command("background_disabled_parent", nil)
	p := NativeProvider().(*nativeProvider)
	defer p.Close()
	local, err := p.SelectBackgroundWindow(ctx, fixture.window.ID)
	if err != nil {
		t.Fatal(err)
	}
	localFrame, err := p.Observe(ctx, local)
	if err != nil || localFrame.Image == "" {
		t.Fatalf("new current-desktop grant: %v", err)
	}
	targets, err := p.Targets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var monitor string
	for _, m := range targets.Monitors {
		if m.Primary {
			monitor = m.ID
		}
	}
	created, err := p.SelectTarget(ctx, TargetSelection{Kind: "new-desktop", MonitorID: monitor})
	if err != nil {
		t.Fatal(err)
	}
	fixture.command("record_desktop", map[string]any{"id": created.DesktopID})
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if _, err := p.SelectTarget(cleanup, TargetSelection{Kind: "desktop", DesktopID: created.DesktopID, MonitorID: monitor}); err != nil {
			t.Errorf("cannot select own cleanup desktop: %v", err)
			return
		}
		fixture.command("cleanup_desktop", map[string]any{"id": created.DesktopID})
		if _, err := p.SelectTarget(cleanup, TargetSelection{Kind: "desktop", DesktopID: fixture.initial, MonitorID: monitor}); err != nil {
			t.Errorf("cannot restore initial desktop: %v", err)
		}
		fixture.command("current_matches_initial", nil)
		fixture.command("close", nil)
	}()
	fixture.command("move_to_created", nil)
	if _, err := p.SelectTarget(ctx, TargetSelection{Kind: "desktop", DesktopID: fixture.initial, MonitorID: monitor}); err != nil {
		t.Fatal(err)
	}
	fixture.command("other", nil)
	time.Sleep(500 * time.Millisecond)
	before := backgroundFixtureInputState(t)
	check := func() {
		t.Helper()
		fixture.command("current_matches_initial", nil)
		if after := backgroundFixtureInputState(t); after != before {
			t.Fatal("background request changed physical input/focus state")
		}
	}
	window, err := p.SelectBackgroundWindow(ctx, fixture.window.ID)
	if err != nil || window.DesktopID != created.DesktopID || !window.OtherDesktop {
		t.Fatalf("background selection: %v desktop=%s other=%t", err, window.DesktopID, window.OtherDesktop)
	}
	check()
	observation, err := p.Observe(ctx, window)
	if err != nil || observation.Image == "" {
		t.Fatalf("cross-desktop capture: %v capture=%s elements=%d", err, observation.CaptureError, len(observation.Elements))
	}
	check()
	raw, err := base64.StdEncoding.DecodeString(observation.Image)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	status := fixture.command("status", nil)
	r, g, b, _ := img.At(int(status.Canvas.X), int(status.Canvas.Y)).RGBA()
	if r>>8 != 144 || g>>8 != 238 || b>>8 != 144 {
		t.Fatal("background bitmap does not contain the owned fixture canvas")
	}
	var passwordFound, disabledFound, disabledParentFound bool
	for _, e := range observation.Elements {
		if e.Value == "fixture-password-never-export" || e.Value == "fixture-hidden" {
			t.Fatal("private/hidden control text was exported")
		}
		if e.Role == "password" {
			passwordFound = true
			if e.Name != "" || e.Value != "" || len(e.Actions) != 0 {
				t.Fatal("password metadata is not redacted")
			}
			r, g, b, _ := img.At(int(e.X+e.Width/2), int(e.Y+e.Height/2)).RGBA()
			if r != 0 || g != 0 || b != 0 {
				t.Fatal("password bitmap was not masked")
			}
		}
		if e.Value == "fixture-disabled" {
			disabledFound = true
			if len(e.Actions) != 0 {
				t.Fatal("disabled control advertises actions")
			}
		}
		if e.Value == "fixture-parent-disabled" {
			disabledParentFound = true
			if len(e.Actions) != 0 {
				t.Fatal("control with native-disabled ancestor advertises actions")
			}
		}
	}
	if !passwordFound || !disabledFound || !disabledParentFound {
		t.Fatal("native password/disabled controls not discovered")
	}
	for _, e := range observation.Elements {
		if e.Role == "password" || e.Value == "fixture-disabled" {
			err := p.Act(ctx, window, Action{Kind: "set_value", ElementID: e.ID, Value: "must-not-write"})
			if err == nil {
				t.Fatal("private/disabled element accepted an action")
			}
			check()
		}
	}
	element := func(kind string) string {
		t.Helper()
		for _, e := range observation.Elements {
			for _, action := range e.Actions {
				if action == kind {
					return e.ID
				}
			}
		}
		t.Fatalf("no %s adapter in %d cross-desktop elements", kind, len(observation.Elements))
		return ""
	}
	write := Action{Kind: "set_value", ElementID: element("set_value"), Value: "background-other-desktop"}
	if err := p.Act(ctx, window, write); err != nil {
		t.Fatal(err)
	}
	check()
	if err := p.Act(ctx, window, write); err == nil {
		t.Fatal("consumed native receipt replayed")
	}
	observation, err = p.Observe(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Act(ctx, window, Action{Kind: "invoke", ElementID: element("invoke")}); err != nil {
		t.Fatal(err)
	}
	check()
	status = fixture.command("status", nil)
	if status.Text != "background-other-desktop" || status.Clicks != 1 || status.Foreground {
		t.Fatalf("background control state text=%q clicks=%d foreground=%t", status.Text, status.Clicks, status.Foreground)
	}
	observation, err = p.Observe(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	stableHandleReceipt := element("set_value")
	fixture.command("background_aba_enabled", nil)
	time.Sleep(100 * time.Millisecond)
	if err := p.Act(ctx, window, Action{Kind: "set_value", ElementID: stableHandleReceipt, Value: "must-not-write"}); err == nil {
		t.Fatal("same-HWND restored metadata accepted an old event generation")
	}
	check()
	observation, err = p.Observe(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NativeProvider().(*nativeProvider)
	if err := restarted.Act(ctx, window, Action{Kind: "set_value", ElementID: element("set_value"), Value: "must-not-write"}); err == nil {
		t.Fatal("new native worker reconstructed a receipt from untrusted input")
	}
	restarted.Close()
	check()
	for _, operation := range []string{"password_on_read", "password_after_print"} {
		fixture.command(operation, nil)
		result, err := p.Observe(ctx, window)
		if err == nil || result.Image != "" || len(result.Elements) != 0 {
			t.Fatalf("%s returned stale text or bitmap: error=%v image=%t elements=%d", operation, err, result.Image != "", len(result.Elements))
		}
		fixture.command("clear_password", nil)
		time.Sleep(100 * time.Millisecond)
		check()
	}
	observation, err = p.Observe(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	oldEdit := element("set_value")
	fixture.command("rebuild_edit", nil)
	time.Sleep(100 * time.Millisecond)
	if err := p.Act(ctx, window, Action{Kind: "set_value", ElementID: oldEdit, Value: "must-not-write"}); err == nil {
		t.Fatal("recreated child accepted old native receipt")
	}
	check()
	if got := fixture.command("status", nil).Text; got != "background-other-desktop" {
		t.Fatal("recreated child was mutated by stale action")
	}
	bad := window
	bad.DesktopID = fixture.initial
	if _, err := p.Observe(ctx, bad); err == nil || ErrorCode(err) != "desktop_background_desktop_changed" {
		t.Fatalf("incorrect desktop binding accepted: %v", err)
	}
	check()
	fixture.command("move_to_initial", nil)
	if _, err := p.Observe(ctx, window); err == nil || ErrorCode(err) != "desktop_background_desktop_changed" {
		t.Fatalf("moved real window accepted old binding: %v", err)
	}
	t.Log("current/other-desktop native capture/control passed without desktop/focus/input changes; password/layout races, disabled ancestor, native receipt replay/restart, same-HWND state ABA, child rebuild and desktop binding guards passed")
}
