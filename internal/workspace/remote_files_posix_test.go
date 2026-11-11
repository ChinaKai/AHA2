//go:build !windows

package workspace

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runPOSIXScript(t *testing.T, script, target, stdin string) (int, string, string) {
	t.Helper()
	command := exec.Command("sh", "-c", script, "aha-script", target)
	command.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			t.Fatalf("run POSIX script: %v", runErr)
		}
		exitCode = exitErr.ExitCode()
	}
	return exitCode, stdout.String(), stderr.String()
}

func TestPOSIXReadFileIfExistsScriptSemantics(t *testing.T) {
	dir := t.TempDir()
	readable := filepath.Join(dir, "readable.txt")
	if err := os.WriteFile(readable, []byte("content-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.txt")
	if code, out, _ := runPOSIXScript(t, posixReadFileIfExistsScript, readable, ""); code != 0 || out != "content-value" {
		t.Fatalf("readable file: code=%d out=%q", code, out)
	}
	if code, _, _ := runPOSIXScript(t, posixReadFileIfExistsScript, missing, ""); code != 3 {
		t.Fatalf("missing file: code=%d, want 3", code)
	}
	if code, _, stderr := runPOSIXScript(t, posixReadFileIfExistsScript, dir, ""); code != 1 || strings.TrimSpace(stderr) == "" {
		t.Fatalf("directory read: code=%d stderr=%q, want 1", code, stderr)
	}
	if os.Geteuid() != 0 {
		unreadable := filepath.Join(dir, "unreadable.txt")
		if err := os.WriteFile(unreadable, []byte("secret"), 0o000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(unreadable, 0o600)
		if code, _, stderr := runPOSIXScript(t, posixReadFileIfExistsScript, unreadable, ""); code != 1 || strings.TrimSpace(stderr) == "" {
			t.Fatalf("unreadable file: code=%d stderr=%q, want 1", code, stderr)
		}
	}
}

func TestPOSIXAtomicWriteScriptRemovesStaleTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(target+".tmp", []byte("stale-fake-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runPOSIXScript(t, posixWriteFileAtomicScript, target, "new-content")
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
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("target mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("stale temp file still exists: %v", err)
	}
}
