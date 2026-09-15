//go:build windows

package desktop

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ownEdgeWindowFinder() func(map[uint32]bool, string) (Window, bool) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	var found Window
	var owned map[uint32]bool
	var titlePart string
	callback := windows.NewCallback(func(hwnd, _ uintptr) uintptr {
		var owner uint32
		user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if !owned[owner] {
			return 1
		}
		visible, _, _ := user32.NewProc("IsWindowVisible").Call(hwnd)
		if visible == 0 {
			return 1
		}
		text := make([]uint16, 2048)
		user32.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
		title := windows.UTF16ToString(text)
		if !strings.Contains(title, titlePart) {
			return 1
		}
		process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, owner)
		if err != nil {
			return 1
		}
		defer windows.CloseHandle(process)
		var created, exited, kernel, user windows.Filetime
		if windows.GetProcessTimes(process, &created, &exited, &kernel, &user) != nil {
			return 1
		}
		birth := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
		found = Window{ID: fmt.Sprintf("%x.%x.%x", owner, birth, hwnd), Title: title, Process: "msedge", Kind: "window"}
		return 0
	})
	return func(pids map[uint32]bool, title string) (Window, bool) {
		owned, titlePart, found = pids, title, Window{}
		user32.NewProc("EnumWindows").Call(callback, 0)
		return found, found.ID != ""
	}
}

func ownBrowserPIDs(root uint32) map[uint32]bool {
	pids := map[uint32]bool{root: true}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return pids
	}
	defer windows.CloseHandle(snapshot)
	var entries []windows.ProcessEntry32
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err := windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		entries = append(entries, entry)
	}
	for depth := 0; depth < 16; depth++ {
		changed := false
		for _, entry := range entries {
			if pids[entry.ParentProcessID] && !pids[entry.ProcessID] {
				pids[entry.ProcessID] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return pids
}

func TestForegroundOwnedEdgeNavigationAndUnicode(t *testing.T) {
	if os.Getenv("AHA_FOREGROUND_EDGE_TEST") != "1" {
		t.Skip("opt-in; real Edge with a private temporary profile and local fixture only")
	}
	var executable string
	var roots []string
	for _, folder := range []*windows.KNOWNFOLDERID{
		windows.FOLDERID_ProgramFilesX86, windows.FOLDERID_ProgramFiles, windows.FOLDERID_LocalAppData,
	} {
		if root, err := windows.KnownFolderPath(folder, 0); err == nil {
			roots = append(roots, root)
		}
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		candidate := filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe")
		if _, err := os.Stat(candidate); err == nil {
			executable = candidate
			break
		}
	}
	if executable == "" {
		t.Fatal("Edge executable not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	title := "AHA-owned-edge-" + token()
	submitted := make(chan string, 1)
	var startRequested atomic.Bool
	var rendererReady atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/":
			startRequested.Store(true)
			fmt.Fprintf(w, "<!doctype html><title>%s start</title><h1>Owned Edge fixture</h1><script>fetch('/ready')</script>", title)
		case "/ready":
			rendererReady.Store(true)
			w.WriteHeader(http.StatusNoContent)
		case "/input":
			fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s input</title>
<form action="/done"><label>Fixture value<input name="value" autofocus></label>
<button type="submit">Submit fixture</button></form>`, title)
		case "/done":
			select {
			case submitted <- r.URL.Query().Get("value"):
			default:
			}
			fmt.Fprintf(w, "<!doctype html><title>%s done</title><h1>Submitted</h1>", title)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	profile := t.TempDir()
	cmd := exec.CommandContext(ctx, executable, "--user-data-dir="+profile, "--no-first-run",
		"--no-default-browser-check", "--disable-background-mode", "--disable-extensions",
		"--force-renderer-accessibility", "--disable-gpu", "--guest", "--window-size=900,700", "--new-window", server.URL)
	cmd.WaitDelay = time.Second
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = []string{"PATH=" + systemDir}
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
		// Retain handles before terminating the root, avoiding PID reuse during cleanup.
		var handles []windows.Handle
		for pid := range ownBrowserPIDs(rootPID) {
			if handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid); err == nil {
				handles = append(handles, handle)
			}
		}
		_ = cmd.Process.Kill()
		for _, handle := range handles {
			_ = windows.TerminateProcess(handle, 1)
			windows.WaitForSingleObject(handle, 2000)
			windows.CloseHandle(handle)
		}
		_ = cmd.Wait()
	}()
	findWindow := ownEdgeWindowFinder()
	var debugWindow Window
	waitWindow := func(suffix string) Window {
		t.Helper()
		deadline := time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) && ctx.Err() == nil {
			if window, ok := findWindow(ownBrowserPIDs(rootPID), title+" "+suffix); ok {
				return window
			}
			time.Sleep(100 * time.Millisecond)
		}
		if debugWindow.ID != "" {
			if snapshot, err := NativeProvider().(ForegroundProvider).ObserveForeground(ctx, debugWindow); err == nil && snapshot.Image != "" {
				if image, err := base64.StdEncoding.DecodeString(snapshot.Image); err == nil {
					_ = os.WriteFile(filepath.Join(".data", "shared-control-test", "edge-focus-failure.png"), image, 0600)
				}
			}
		}
		t.Fatalf("owned Edge did not reach the %s page (fixture_requested=%t renderer_ready=%t owned_processes=%d)",
			suffix, startRequested.Load(), rendererReady.Load(), len(ownBrowserPIDs(rootPID)))
		return Window{}
	}
	window := waitWindow("start")
	debugWindow = window
	provider := NativeProvider().(ForegroundProvider)
	act := func(action Action) {
		t.Helper()
		observation, err := provider.ObserveForeground(ctx, window)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Image == "" && action.Kind != "focus" {
			t.Fatalf("owned Edge screenshot unavailable: %s", observation.CaptureError)
		}
		action.ElementID = "$surface"
		if err := provider.ActForeground(ctx, window, action, observation); err != nil {
			if image, decodeErr := base64.StdEncoding.DecodeString(observation.Image); decodeErr == nil {
				_ = os.WriteFile(filepath.Join(".data", "shared-control-test", "edge-focus-failure.png"), image, 0600)
			}
			parts := strings.Split(window.ID, ".")
			handle, _ := strconv.ParseUint(parts[len(parts)-1], 16, 64)
			user32 := windows.NewLazySystemDLL("user32.dll")
			enabled, _, _ := user32.NewProc("IsWindowEnabled").Call(uintptr(handle))
			front, _, _ := user32.NewProc("GetForegroundWindow").Call()
			var frontPID uint32
			user32.NewProc("GetWindowThreadProcessId").Call(front, uintptr(unsafe.Pointer(&frontPID)))
			t.Fatalf("owned Edge %s: %v (target_enabled=%t foreground_in_owned_tree=%t)",
				action.Kind, err, enabled != 0, ownBrowserPIDs(rootPID)[frontPID])
		}
		time.Sleep(80 * time.Millisecond)
	}
	rootParts := strings.Split(window.ID, ".")
	rootHandle, _ := strconv.ParseUint(rootParts[len(rootParts)-1], 16, 64)
	user32 := windows.NewLazySystemDLL("user32.dll")
	for attempt, stable := 0, 0; attempt < 8 && stable < 4; attempt++ {
		enabled, _, _ := user32.NewProc("IsWindowEnabled").Call(uintptr(rootHandle))
		if enabled != 0 {
			stable++
			time.Sleep(300 * time.Millisecond)
			continue
		}
		stable = 0
		// Only dismiss a modal owned by this test's temporary-profile browser.
		// The provider validates process identity and the owner chain.
		act(Action{Kind: "focus"})
		act(Action{Kind: "key", Keys: []string{"ESC"}})
		time.Sleep(200 * time.Millisecond)
		enabled, _, _ = user32.NewProc("IsWindowEnabled").Call(uintptr(rootHandle))
		if enabled == 0 {
			act(Action{Kind: "key", Keys: []string{"ALT", "F4"}})
			time.Sleep(200 * time.Millisecond)
			enabled, _, _ = user32.NewProc("IsWindowEnabled").Call(uintptr(rootHandle))
		}
		if enabled == 0 {
			t.Fatal("owned temporary-profile startup modal could not be canceled")
		}
		t.Log("owned temporary-profile startup dialog closed; parent enabled")
	}
	act(Action{Kind: "focus"})
	if controls, err := NativeProvider().Observe(ctx, window); err == nil {
		for _, element := range controls.Elements {
			if (strings.Contains(element.Name, "地址") || strings.Contains(strings.ToLower(element.Name), "address")) &&
				element.Width > 0 && element.Height > 0 {
				act(Action{Kind: "click", X: element.X + element.Width/2, Y: element.Y + element.Height/2})
				break
			}
		}
	}
	act(Action{Kind: "key", Keys: []string{"CTRL", "L"}})
	act(Action{Kind: "text", Value: server.URL + "/input"})
	if address, err := NativeProvider().Observe(ctx, window); err == nil {
		for _, element := range address.Elements {
			if strings.Contains(element.Name, "地址") || strings.Contains(strings.ToLower(element.Name), "address") {
				state := "other"
				if strings.TrimSuffix(element.Value, "/") == strings.TrimPrefix(server.URL, "http://") ||
					strings.TrimSuffix(element.Value, "/") == server.URL {
					state = "start"
				}
				if strings.Contains(element.Value, strings.TrimPrefix(server.URL, "http://")+"/input") {
					state = "input"
				}
				t.Logf("owned address bar after text: state=%s length=%d", state, len(element.Value))
			}
		}
	}
	act(Action{Kind: "key", Keys: []string{"ENTER"}})
	waitWindow("input")
	value := "AHA foreground \u4e2d\u6587"
	act(Action{Kind: "text", Value: value})
	act(Action{Kind: "key", Keys: []string{"ENTER"}})
	select {
	case got := <-submitted:
		if got != value {
			t.Fatalf("owned Edge input mismatch: got %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("owned Edge form submission did not arrive")
	}
	waitWindow("done")
	t.Log("real owned Edge: Ctrl+L navigation, Unicode input and Enter submission verified without CDP/UIA mutation")
}
