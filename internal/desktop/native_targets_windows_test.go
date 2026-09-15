//go:build windows

package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestNativeTargetMonitorMetadata(t *testing.T) {
	if os.Getenv("AHA_NATIVE_TARGET_TEST") != "1" {
		t.Skip("opt-in metadata only")
	}
	p := NativeProvider().(*nativeProvider)
	defer p.Close()
	targets, err := p.Targets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(targets.Monitors) == 0 || len(targets.Desktops) == 0 {
		t.Fatal("missing monitor/desktop metadata")
	}
	var current, primary int
	for _, d := range targets.Desktops {
		if d.Current {
			current++
		}
	}
	for _, m := range targets.Monitors {
		if m.Primary {
			primary++
		}
		if m.Width <= 0 || m.Height <= 0 || len(m.ID) != 32 {
			t.Fatal("invalid monitor identity/geometry")
		}
		t.Logf("actual monitor origin=%d,%d dimensions=%dx%d primary=%t", m.X, m.Y, m.Width, m.Height, m.Primary)
	}
	if current != 1 || primary != 1 {
		t.Fatalf("current=%d primary=%d", current, primary)
	}
	t.Logf("metadata: monitors=%d desktops=%d current=1 desktop_switch_supported=%t safe_reason=%s",
		len(targets.Monitors), len(targets.Desktops), targets.DesktopSwitchSupported, targets.DesktopReason)
}

func TestNativeTargetOwnedDesktopSwitchAndMonitorCapture(t *testing.T) {
	if os.Getenv("AHA_NATIVE_TARGET_PHYSICAL_TEST") != "1" {
		t.Skip("opt-in; only own desktops/fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	fixture := startForegroundFixture(t, ctx)
	p := NativeProvider().(*nativeProvider)
	defer p.Close()
	targets, err := p.Targets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !targets.DesktopSwitchSupported {
		t.Fatalf("desktop registry is not verified: %s", targets.DesktopReason)
	}
	var monitor Monitor
	for _, m := range targets.Monitors {
		if m.Primary {
			monitor = m
		}
	}
	existed := map[string]bool{}
	for _, d := range targets.Desktops {
		existed[d.ID] = true
	}
	create := func() Window {
		t.Helper()
		w, err := p.SelectTarget(ctx, TargetSelection{Kind: "new-desktop", MonitorID: monitor.ID})
		if err != nil {
			t.Fatal(err)
		}
		if existed[w.DesktopID] || w.MonitorID != monitor.ID || !strings.Contains(w.ID, "@"+monitor.ID) {
			t.Fatal("new desktop scope not pinned")
		}
		fixture.command("record_desktop", map[string]any{"id": w.DesktopID})
		return w
	}
	first := create()
	cleanedFirst := false
	defer func() {
		if !cleanedFirst {
			if _, err := p.SelectTarget(context.Background(), TargetSelection{Kind: "desktop", DesktopID: first.DesktopID, MonitorID: monitor.ID}); err != nil {
				t.Errorf("own desktop cleanup switch: %v", err)
				return
			}
			fixture.command("cleanup_desktop", map[string]any{"id": first.DesktopID})
		}
	}()
	fixture.command("move_to_created", nil)
	time.Sleep(200 * time.Millisecond)
	fixture.command("own_activation", nil)
	time.Sleep(500 * time.Millisecond)
	o, err := p.ObserveForeground(ctx, first)
	if err != nil || o.Image == "" || o.Width != monitor.Width || o.Height != monitor.Height {
		t.Fatalf("selected-monitor PNG: %v %s %dx%d", err, o.CaptureError, o.Width, o.Height)
	}
	raw, err := base64.StdEncoding.DecodeString(o.Image)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil || img.Bounds().Dx() != monitor.Width || img.Bounds().Dy() != monitor.Height {
		t.Fatalf("monitor PNG invalid: %v", err)
	}
	preview, err := p.ObservePreview(ctx, first, FrameOptions{MaxWidth: 640, MaxHeight: 360, Quality: 45})
	if err != nil || preview.Image == "" || preview.Width != monitor.Width || preview.Height != monitor.Height || preview.Surface != o.Surface {
		t.Fatalf("monitor preview scope: %v", err)
	}
	bad := first
	bad.MonitorID = strings.Repeat("f", 32)
	bad.ID = "desktop:" + bad.DesktopID + "@" + bad.MonitorID
	if _, err := p.ObserveForeground(ctx, bad); err == nil || !strings.Contains(err.Error(), "monitor_changed") {
		t.Fatalf("missing/reconfigured monitor capture: %v", err)
	}
	o.Window = bad
	if err := p.ActForeground(ctx, bad, Action{Kind: "focus", ElementID: "$surface"}, o); err == nil || !strings.Contains(err.Error(), "monitor_changed") {
		t.Fatalf("missing/reconfigured monitor input: %v", err)
	}
	for _, secondary := range targets.Monitors {
		if secondary.Primary {
			continue
		}
		selected, err := p.SelectTarget(ctx, TargetSelection{Kind: "desktop", DesktopID: first.DesktopID, MonitorID: secondary.ID})
		if err != nil {
			t.Fatal(err)
		}
		outside, err := p.ObserveForeground(ctx, selected)
		if err != nil || outside.Image == "" || outside.Width != secondary.Width || outside.Height != secondary.Height {
			t.Fatalf("secondary capture: %v %s", err, outside.CaptureError)
		}
		if err := p.ActForeground(ctx, selected, Action{Kind: "text", ElementID: "$surface", Value: "must-not-type"}, outside); err == nil ||
			!strings.Contains(err.Error(), "foreground_outside_monitor") {
			t.Fatalf("keyboard on other physical monitor not rejected: %v", err)
		}
		fixture.command("move_monitor", map[string]any{"x": secondary.X, "y": secondary.Y})
		fixture.command("own_activation", nil)
		time.Sleep(500 * time.Millisecond)
		actual, err := p.ObserveForeground(ctx, selected)
		if err != nil {
			t.Fatal(err)
		}
		data, err := base64.StdEncoding.DecodeString(actual.Image)
		if err != nil {
			t.Fatal(err)
		}
		picture, err := png.Decode(bytes.NewReader(data))
		if err != nil || picture.Bounds().Dx() != secondary.Width || picture.Bounds().Dy() != secondary.Height {
			t.Fatalf("secondary PNG geometry: %v", err)
		}
		status := fixture.command("status", nil)
		r, g, b, _ := picture.At(int(status.DesktopCanvas.X), int(status.DesktopCanvas.Y)).RGBA()
		if r>>8 != 144 || g>>8 != 238 || b>>8 != 144 {
			t.Fatal("secondary PNG does not contain own fixture at monitor-local coordinates")
		}
		act := func(action Action) {
			t.Helper()
			current, err := p.ObserveForeground(ctx, selected)
			if err != nil {
				t.Fatal(err)
			}
			action.ElementID = "$surface"
			if err := p.ActForeground(ctx, selected, action, current); err != nil {
				t.Fatalf("secondary input %s: %v", action.Kind, err)
			}
			time.Sleep(60 * time.Millisecond)
		}
		act(Action{Kind: "click", X: status.DesktopEdit.X, Y: status.DesktopEdit.Y})
		act(Action{Kind: "key", Keys: []string{"CTRL", "A"}})
		act(Action{Kind: "text", Value: "secondary \u4e2d\u6587"})
		status = fixture.command("status", nil)
		if status.Text != "secondary \u4e2d\u6587" {
			t.Fatalf("negative-origin physical input mismatch: %q", status.Text)
		}
		t.Logf("real secondary monitor %d,%d %dx%d: scoped PNG pixels, negative-origin click/Unicode, and outside-monitor keyboard rejection passed",
			secondary.X, secondary.Y, secondary.Width, secondary.Height)
		fixture.command("move_monitor", map[string]any{"x": monitor.X, "y": monitor.Y})
		fixture.command("own_activation", nil)
		time.Sleep(500 * time.Millisecond)
		break
	}
	second := create()
	cleanedSecond := false
	defer func() {
		if !cleanedSecond {
			if _, err := p.SelectTarget(context.Background(), TargetSelection{Kind: "desktop", DesktopID: second.DesktopID, MonitorID: monitor.ID}); err != nil {
				t.Errorf("own desktop cleanup switch: %v", err)
				return
			}
			fixture.command("cleanup_desktop", map[string]any{"id": second.DesktopID})
		}
	}()
	verifyCurrentDesktopWithoutRegistry(t, ctx, p, second, first)
	verifySelectionCancellationRelease(t, ctx, p, first)
	for _, w := range []Window{first, second, first, second} {
		selected, err := p.SelectTarget(ctx, TargetSelection{Kind: "desktop", DesktopID: w.DesktopID, MonitorID: monitor.ID})
		if err != nil || selected.ID != w.ID {
			t.Fatalf("switch/reuse own GUID failed: %v", err)
		}
		current, err := p.Targets(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ok := false
		for _, d := range current.Desktops {
			ok = ok || d.Current && d.ID == w.DesktopID
		}
		if !ok {
			t.Fatal("COM/registry current GUID did not match selection")
		}
	}
	fixture.command("cleanup_desktop", map[string]any{"id": second.DesktopID})
	cleanedSecond = true
	fixture.command("cleanup_desktop", map[string]any{"id": first.DesktopID})
	cleanedFirst = true
	fixture.command("close", nil)
	t.Logf("two newly-created desktops switched/reused in both directions with verified GUIDs and cleaned only owned IDs; selected-monitor PNG=%dx%d JPEG=%dx%d; bad monitor capture/input refused; physical monitor count=%d",
		monitor.Width, monitor.Height, preview.PreviewWidth, preview.PreviewHeight, len(targets.Monitors))
}

func verifyCurrentDesktopWithoutRegistry(t *testing.T, ctx context.Context, p *nativeProvider, current, other Window) {
	t.Helper()
	source := strings.Replace(foregroundNativeSource, "var before = ReadDesktopOrder();",
		`Fail("desktop_enumeration_unavailable"); var before = ReadDesktopOrder();`, 1)
	if source == foregroundNativeSource {
		t.Fatal("registry fault instrumentation did not apply")
	}
	request := nativeRequest{Operation: "target_select", Selection: &TargetSelection{
		Kind: "desktop", DesktopID: current.DesktopID, MonitorID: current.MonitorID,
	}}
	result, err := p.callSource(ctx, request, nativeSource+"\n"+source)
	if err != nil || result.Window.ID != current.ID {
		t.Fatalf("current desktop needs no registry order: %v", err)
	}
	request.Selection.DesktopID = other.DesktopID
	if _, err := p.callSource(ctx, request, nativeSource+"\n"+source); err == nil || ErrorCode(err) != "desktop_enumeration_unavailable" {
		t.Fatalf("unverified cross-desktop switch did not fail closed: %v", err)
	}
	t.Log("current monitor selection works without registry order; cross-desktop switching still refuses unverified order")
}

func verifySelectionCancellationRelease(t *testing.T, ctx context.Context, p *nativeProvider, target Window) {
	t.Helper()
	source := strings.Replace(foregroundNativeSource,
		"uint sent = SendInput((uint)batch.Length, batch, Marshal.SizeOf(typeof(Input)));",
		"SendInput(1, new Input[] { batch[0] }, Marshal.SizeOf(typeof(Input))); Thread.Sleep(10000); uint sent = SendInput((uint)batch.Length, batch, Marshal.SizeOf(typeof(Input)));", 1)
	if source == foregroundNativeSource {
		t.Fatal("selection cancellation instrumentation did not apply")
	}
	canceled, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() {
		_, err := p.callSource(canceled, nativeRequest{Operation: "target_select", Selection: &TargetSelection{
			Kind: "desktop", DesktopID: target.DesktopID, MonitorID: target.MonitorID,
		}}, nativeSource+"\n"+source)
		done <- err
	}()
	state := windows.NewLazySystemDLL("user32.dll").NewProc("GetAsyncKeyState")
	down := false
	for i := 0; i < 400; i++ {
		v, _, _ := state.Call(0x11)
		if v&0x8000 != 0 {
			down = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	err := <-done
	time.Sleep(100 * time.Millisecond)
	v, _, _ := state.Call(0x11)
	win, _, _ := state.Call(0x5B)
	if err == nil || !down || v&0x8000 != 0 || win&0x8000 != 0 {
		t.Fatalf("target_select cancellation: error=%v observed_CTRL_down=%t CTRL_down=%t WIN_down=%t", err, down, v&0x8000 != 0, win&0x8000 != 0)
	}
	t.Log("target_select interrupted after actual CTRL down; parent released the key without replaying the switch")
}

func TestNativeTargetSyntheticMonitorGeometry(t *testing.T) {
	if os.Getenv("AHA_NATIVE_TARGET_TEST") != "1" {
		t.Skip("opt-in synthetic C# geometry check, no window access")
	}
	p, pool := nativeTestPool(t, nativeWorkerIdle)
	defer p.Close()
	// Exercise the exact native arithmetic with synthetic negative-origin monitors,
	// without changing host display settings or pretending this is physical multi-screen evidence.
	extra := `namespace AHADesktop { public static partial class Native {
		public static void TargetGeometryCheck() {
			Rect a = new Rect { Left = -1920, Top = -200, Right = 0, Bottom = 880 };
			Rect b = new Rect { Left = 0, Top = 0, Right = 2560, Bottom = 1440 };
			if (!ContainsPoint(a, -1920, -200) || ContainsPoint(a, 0, 0) || ContainsPoint(a, -1921, 0)) throw new Exception();
			string x = MonitorIdentity("fixture-device", "fixture-hardware", new IntPtr(1), a);
			if (x == MonitorIdentity("fixture-device", "fixture-hardware", new IntPtr(1), b) ||
				x == MonitorIdentity("fixture-device", "other-hardware", new IntPtr(1), a)) throw new Exception();
			var s = new Surface { Bounds = a };
			Point origin = ScreenPoint(s, 0, 0), end = ScreenPoint(s, 1919, 1079);
			if (origin.X != -1920 || origin.Y != -200 || end.X != -1 || end.Y != 879) throw new Exception();
		} } }`
	source := nativeSource + "\n" + foregroundNativeSource + "\n" + nativePreviewSource + "\n" +
		strings.Replace(nativeWorkerSource, `Console.Out.WriteLine("{\"id\":0,\"result\":{\"ok\":true}}");`, `TargetGeometryCheck(); Console.Out.WriteLine("{\"id\":0,\"result\":{\"ok\":true}}");`, 1) + "\n" + extra
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	worker, err := pool.start(ctx, source, "capture")
	if err != nil {
		t.Fatal(err)
	}
	worker.stop()
	t.Log("native synthetic negative origins, differing dimensions, local-to-physical mapping and geometry/hardware-bound identity passed")
}

func TestNativeTargetSelectionCleanupKeys(t *testing.T) {
	events := cleanupForegroundEvents(nativeRequest{Operation: "target_select", Selection: &TargetSelection{Kind: "desktop", DesktopID: "fixture"}})
	if len(events) != 5 {
		t.Fatal("desktop transition cleanup must release both arrows, D, CTRL and WIN")
	}
}
