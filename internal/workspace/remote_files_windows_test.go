//go:build windows

package workspace

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// runPowerShellScript executes the exact production script with PowerShell and
// returns its exit code. The tests below assert observable script semantics
// (missing vs. read failure, stale temp removal), not a mocked exit code.
func runPowerShellScript(t *testing.T, script, stdin string) (int, string, string) {
	t.Helper()
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("powershell.exe is not available")
	}
	command := exec.Command(powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			t.Fatalf("run PowerShell script: %v", runErr)
		}
		exitCode = exitErr.ExitCode()
	}
	return exitCode, stdout.String(), stderr.String()
}

func TestWindowsReadFileIfExistsScriptSemantics(t *testing.T) {
	dir := t.TempDir()
	readable := filepath.Join(dir, "readable.txt")
	if err := os.WriteFile(readable, []byte("content-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(dir, "a-directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.txt")
	missingParent := filepath.Join(dir, "missing-dir", "config.toml")

	if code, out, stderr := runPowerShellScript(t, windowsReadFileIfExistsScript(readable), ""); code != 0 || out != "content-value" {
		t.Fatalf("readable file: code=%d out=%q stderr=%q", code, out, stderr)
	}
	if code, _, stderr := runPowerShellScript(t, windowsReadFileIfExistsScript(missing), ""); code != 3 {
		t.Fatalf("missing file: code=%d, want 3; stderr=%q", code, stderr)
	}
	if code, _, stderr := runPowerShellScript(t, windowsReadFileIfExistsScript(missingParent), ""); code != 3 {
		t.Fatalf("missing parent: code=%d, want 3; stderr=%q", code, stderr)
	}
	if code, _, stderr := runPowerShellScript(t, windowsReadFileIfExistsScript(directory), ""); code != 1 || strings.TrimSpace(stderr) == "" {
		t.Fatalf("directory read: code=%d stderr=%q, want 1 with an error message", code, stderr)
	}

	locked := filepath.Join(dir, "locked.txt")
	if err := os.WriteFile(locked, []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}
	pathPtr, err := syscall.UTF16PtrFromString(locked)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, // share mode 0: reject every other opener
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if code, _, stderr := runPowerShellScript(t, windowsReadFileIfExistsScript(locked), ""); code != 1 || strings.TrimSpace(stderr) == "" {
		t.Fatalf("locked file: code=%d stderr=%q, want 1 with an error message", code, stderr)
	}
}

func TestWindowsAtomicWriteScriptRemovesStaleTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(target+".tmp", []byte("stale-fake-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runPowerShellScript(t, windowsWriteFileAtomicScript(target), "new-content")
	if code != 0 {
		t.Fatalf("atomic write: code=%d stderr=%q", code, stderr)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new-content" {
		t.Fatalf("target content = %q", content)
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("stale temp file still exists: %v", err)
	}
}
