//go:build windows

package desktop

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestBackgroundOwnedEdgeCoverageProbe measures what the production background
// adapter can actually see and drive on an ordinary page, using its own
// temporary-profile Edge so no Owner window is involved.
//
// It differs from TestBackgroundOwnedEdgeAccessibilityProbe in one deliberate
// way: it never touches the foreground. That probe activates the owned window
// first to prime the renderer's accessibility tree, which cannot happen when an
// elevated window owns the foreground (ObserveForeground refuses it). This probe
// therefore also records how much of the page is reachable with no priming at
// all, which is the situation a remote Owner-absent run actually faces.
func TestBackgroundOwnedEdgeCoverageProbe(t *testing.T) {
	if os.Getenv("AHA_BACKGROUND_COVERAGE_PROBE") != "1" {
		t.Skip("opt-in; owned temporary-profile Edge and a local fixture")
	}
	page := os.Getenv("AHA_BACKGROUND_COVERAGE_URL")
	if page == "" {
		t.Fatal("AHA_BACKGROUND_COVERAGE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	p := NativeProvider().(*nativeProvider)
	defer p.Close()

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

	title := "AHA-coverage-probe-" + token()
	args := []string{"--user-data-dir=" + t.TempDir(), "--no-first-run", "--no-default-browser-check",
		"--disable-background-mode", "--disable-extensions", "--guest", "--window-size=1100,800"}
	if os.Getenv("AHA_EDGE_FORCE_ACCESSIBILITY") == "1" {
		args = append(args, "--force-renderer-accessibility=complete")
	}
	args = append(args, page)
	cmd := exec.CommandContext(ctx, executable, args...)
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
	}()

	find := ownEdgeWindowFinder()
	var window Window
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if w, ok := find(ownBrowserPIDs(rootPID), title); ok {
			window = w
			break
		}
		// The page sets its own title once the DOM is parsed.
		if w, ok := find(ownBrowserPIDs(rootPID), ""); ok && window.ID == "" {
			window = w
		}
		time.Sleep(200 * time.Millisecond)
	}
	if window.ID == "" {
		t.Fatal("owned Edge window not found")
	}
	t.Logf("owned window id=%s title=%q", window.ID, window.Title)
	// Give the renderer time to build whatever accessibility tree it will build
	// without being activated.
	time.Sleep(4 * time.Second)

	selected, err := p.SelectBackgroundWindow(ctx, window.ID)
	if err != nil {
		t.Fatalf("background_select: %v", err)
	}
	t.Logf("selected window id=%s desktop=%s", selected.ID, selected.DesktopID)

	// The renderer builds its tree lazily, so retry briefly before reporting.
	var observation Observation
	var observeErr error
	elements := 0
	for attempt := 0; attempt < 5; attempt++ {
		observation, observeErr = p.Observe(ctx, selected)
		if observeErr != nil {
			break
		}
		elements = len(observation.Elements)
		if elements > 0 {
			break
		}
		time.Sleep(700 * time.Millisecond)
	}
	if observeErr != nil {
		t.Fatalf("background_observe: %v (elements seen earlier: %d)", observeErr, elements)
	}
	actionable := 0
	roles := map[string]int{}
	byAction := map[string]int{}
	for _, element := range observation.Elements {
		roles[element.Role]++
		if len(element.Actions) > 0 {
			actionable++
		}
		for _, action := range element.Actions {
			byAction[action]++
		}
	}
	if observation.Image != "" {
		if raw, decodeErr := base64.StdEncoding.DecodeString(observation.Image); decodeErr == nil {
			t.Logf("image=%d png bytes %dx%d", len(raw), observation.Width, observation.Height)
		}
	}
	t.Logf("elements=%d actionable=%d capture_error=%q control_error=%q",
		len(observation.Elements), actionable, observation.CaptureError, observation.ControlError)
	t.Logf("roles=%v", roles)
	t.Logf("actions=%v", byAction)
	// Named, actionable elements are what an Owner could actually press.
	named := 0
	for _, element := range observation.Elements {
		if len(element.Actions) > 0 && element.Name != "" {
			named++
			if named <= 15 {
				t.Logf("  actionable: %s | %q | %v", element.Role, element.Name, element.Actions)
			}
		}
	}
	t.Logf("named actionable=%d of %d elements", named, len(observation.Elements))
	if observation.ControlError != "" {
		t.Logf("NOTE: control_error set, so actions are suppressed: %s", observation.ControlError)
	}
	fmt.Println()
}
