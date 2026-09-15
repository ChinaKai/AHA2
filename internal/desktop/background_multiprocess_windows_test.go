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
)

func TestBackgroundMultiProcessOwnedWindow(t *testing.T) {
	if os.Getenv("AHA_BACKGROUND_MULTIPROCESS_TEST") != "1" {
		t.Skip("opt-in; two owned processes/windows only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	root := startForegroundFixture(t, ctx)
	child := startForegroundFixture(t, ctx)
	child.command("cross_process_children", map[string]any{"parent": strings.Split(root.window.ID, ".")[2]})
	defer child.command("close_cross_process_children", nil)
	p := NativeProvider().(*nativeProvider)
	defer p.Close()
	window, err := p.SelectBackgroundWindow(ctx, root.window.ID)
	if err != nil {
		t.Fatal(err)
	}
	check := func(window Window) {
		t.Helper()
		before := backgroundFixtureInputState(t)
		o, err := p.Observe(ctx, window)
		if err != nil {
			t.Fatalf("live owned root with another process child refused: %v", err)
		}
		if o.Image == "" {
			t.Fatalf("missing multiprocess bitmap: %s", o.CaptureError)
		}
		data, err := base64.StdEncoding.DecodeString(o.Image)
		if err != nil {
			t.Fatal(err)
		}
		image, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		password := false
		for _, e := range o.Elements {
			if strings.Contains(e.Value, "cross-process-") || strings.Contains(e.Name, "cross-process-") {
				t.Fatal("foreign process child text exported")
			}
			if e.Role == "password" {
				password = true
				if e.Name != "" || e.Value != "" || len(e.Actions) != 0 {
					t.Fatal("foreign process password not redacted")
				}
				r, g, b, _ := image.At(int(e.X+e.Width/2), int(e.Y+e.Height/2)).RGBA()
				if r != 0 || g != 0 || b != 0 {
					t.Fatal("foreign process password bitmap not masked")
				}
			}
			if e.Y > 260 && len(e.Actions) > 0 {
				t.Fatal("cross-process native child advertises an action")
			}
		}
		if !password || before != backgroundFixtureInputState(t) {
			t.Fatal("password missing or background operation changed input state")
		}
	}
	check(window)
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
	root.command("record_desktop", map[string]any{"id": created.DesktopID})
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if _, err := p.SelectTarget(cleanup, TargetSelection{Kind: "desktop", DesktopID: created.DesktopID, MonitorID: monitor}); err != nil {
			t.Errorf("select own cleanup desktop: %v", err)
			return
		}
		root.command("cleanup_desktop", map[string]any{"id": created.DesktopID})
		if _, err := p.SelectTarget(cleanup, TargetSelection{Kind: "desktop", DesktopID: root.initial, MonitorID: monitor}); err != nil {
			t.Errorf("restore initial desktop: %v", err)
		}
		root.command("current_matches_initial", nil)
	}()
	root.command("move_to_created", nil)
	if _, err := p.SelectTarget(ctx, TargetSelection{Kind: "desktop", DesktopID: root.initial, MonitorID: monitor}); err != nil {
		t.Fatal(err)
	}
	child.command("other", nil)
	time.Sleep(500 * time.Millisecond)
	window, err = p.SelectBackgroundWindow(ctx, root.window.ID)
	if err != nil || !window.OtherDesktop {
		t.Fatalf("select owned multiprocess window on other desktop: %v", err)
	}
	check(window)
	root.command("current_matches_initial", nil)
	for _, denial := range []string{"target_foreign_user", "target_foreign_session", "elevated_target", "target_token_unavailable"} {
		// Fault injection affects only trust inspection of the owned child,
		// not the selected root and never another application.
		source := strings.Replace(nativeSource,
			"static Target InspectWindowProcessIdentity(IntPtr hwnd, bool describeWindow) {",
			"static Target InspectWindowProcessIdentity(IntPtr hwnd, bool describeWindow) {\n"+
				`if (!describeWindow) Fail("`+denial+`");`, 1)
		if source == nativeSource {
			t.Fatal("child trust instrumentation missing")
		}
		result, err := p.callSource(ctx, nativeRequest{Operation: "background_observe",
			WindowID: window.ID, DesktopID: window.DesktopID}, source+"\n"+foregroundNativeSource)
		if err == nil || ErrorCode(err) != "desktop_background_child_unverifiable" ||
			result.Observation.Image != "" || len(result.Observation.Elements) != 0 {
			t.Fatalf("child trust %s was not withheld: %v", denial, err)
		}
	}
	t.Log("owned multiprocess window remains observable; cross-process children have no text/actions and password pixels masked; foreground/input unchanged")
}
