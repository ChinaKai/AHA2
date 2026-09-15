//go:build windows

package desktop

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestForegroundCreatedDesktopCaptureAndScope(t *testing.T) {
	if os.Getenv("AHA_FOREGROUND_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in; own new desktop only, verifies full surface and scope")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fixture := startForegroundFixture(t, ctx)
	provider := NativeProvider().(ForegroundProvider)
	window, err := provider.NewDesktop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fixture.command("record_desktop", map[string]any{"id": strings.TrimPrefix(window.ID, "desktop:")})
	defer fixture.command("close", nil)
	defer fixture.command("cleanup_desktop", nil)
	observation, err := provider.ObserveForeground(ctx, window)
	if err != nil || observation.Image == "" || observation.Width <= 0 || observation.Height <= 0 {
		t.Fatalf("new desktop capture failed: %v capture_error=%s", err, observation.CaptureError)
	}
	wrong := Window{ID: "desktop:00000000-0000-0000-0000-000000000001", Kind: "desktop"}
	if _, err := provider.ObserveForeground(ctx, wrong); err == nil {
		t.Fatal("provider captured a different desktop than the granted identity")
	}
	t.Logf("new desktop image verified %dx%d; mismatched desktop identity refused", observation.Width, observation.Height)
}

func TestForegroundOwnedModalRemainsInsideParentGrant(t *testing.T) {
	if os.Getenv("AHA_FOREGROUND_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in; owned fixture modal and parent only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	fixture := startForegroundFixture(t, ctx)
	defer fixture.command("close", nil)
	fixture.command("modal", nil)
	if s := fixture.command("status", nil); s.Enabled || !s.Modal {
		t.Fatal("fixture did not establish its disabled-parent modal state")
	}
	provider := NativeProvider().(ForegroundProvider)
	o, err := provider.ObserveForeground(ctx, fixture.window)
	if err != nil {
		t.Fatal(err)
	}
	if o.Window.ID != fixture.window.ID || o.Width >= 440 || o.Height >= 340 {
		t.Fatal("modal observation did not retain parent grant and use modal geometry")
	}
	if err := provider.ActForeground(ctx, fixture.window, Action{Kind: "focus", ElementID: "$surface"}, o); err != nil {
		t.Fatal(err)
	}
	o, err = provider.ObserveForeground(ctx, fixture.window)
	if err != nil || o.Image == "" {
		t.Fatalf("owned modal screenshot unavailable: %v", err)
	}
	if err := provider.ActForeground(ctx, fixture.window, Action{Kind: "key", ElementID: "$surface", Keys: []string{"ESC"}}, o); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if s := fixture.command("status", nil); !s.Enabled || s.Modal {
		t.Fatalf("owned modal state after generic input: parent_enabled=%t modal_visible=%t", s.Enabled, s.Modal)
	}
	t.Log("owned modal captured and dismissed within original parent grant; disabled parent recovered")
}
