package workspace

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestFindWriterPIDsScanWritesNothingToStderr pins the invariant that makes an
// empty result readable.
//
// "No writer found" is expressed as a non-zero shell exit with empty stderr, and
// the caller reads a non-zero exit as a failure only when stderr says something.
// The scan walks /proc/<pid>/environ, and a plain `[ -r ... ]` test is not enough
// there: it uses access(2), which does not apply the ptrace check open(2) applies
// to procfs. A process run by another user — on a normal Linux host that is every
// root-owned daemon — passes the test, then fails the read, and that redirection
// error was written to stderr.
//
// Whether the stray message also flips the exit code depends on where the loop
// happens to end, so the symptom is intermittent; the message itself is not. The
// caller cannot tell a real failure from a stray note, so the scan must be silent.
func TestFindWriterPIDsScanWritesNothingToStderr(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("/proc/self/environ"); err != nil {
		t.Skip("/proc is unavailable")
	}
	if unreadableProcessCount(t) == 0 {
		t.Skip("no uninspectable processes on this host, so this cannot trigger here")
	}

	// Capture the exact command the production path builds, then run it, so this
	// asserts the real script rather than a copy that could drift from it.
	spy := &capturingRunner{inner: LocalRunner{}}
	if _, err := FindWriterPIDs(context.Background(), spy, WriterQuery{
		EnvName: "CODEX_HOME", EnvValue: "/nonexistent/aha2-writer-scan-test",
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if spy.command.Executable == "" {
		t.Fatal("the scan did not run a command")
	}

	result, err := LocalRunner{}.Run(context.Background(), spy.command, nil)
	if err != nil {
		t.Fatalf("run captured scan: %v", err)
	}
	if detail := result.Stderr; detail != "" {
		t.Fatalf("an uninspectable process made the scan noisy: %q", detail)
	}
}

// capturingRunner records the command it is asked to run without running it.
type capturingRunner struct {
	inner   Runner
	command Command
}

func (runner *capturingRunner) Run(ctx context.Context, command Command, onLine LineHandler) (Result, error) {
	runner.command = command
	return Result{}, nil
}

// unreadableProcessCount counts processes whose environment this user cannot read,
// which is the condition that used to put text on stderr.
func unreadableProcessCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() || !isDecimal(entry.Name()) {
			continue
		}
		if _, err := os.ReadFile("/proc/" + entry.Name() + "/environ"); err != nil {
			count++
		}
	}
	return count
}

func isDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// TestFindWriterPIDsScanIsPosixShell pins the scan to POSIX shell.
//
// The runner executes the scan with "sh". On Debian and Ubuntu that is dash,
// which has no "read -d"; the scan originally used it to split
// /proc/<pid>/environ on NUL. Under dash the loop aborted immediately, so every
// query matched nothing and AHA silently concluded no backend writer existed —
// CI caught it only because the runner there is Ubuntu while a WSL shell links
// /bin/sh to bash. The scan runs in the same shell on real Ubuntu hosts, so this
// is production behaviour, not a test-environment quirk.
func TestFindWriterPIDsScanIsPosixShell(t *testing.T) {
	t.Parallel()
	spy := &capturingRunner{inner: LocalRunner{}}
	if _, err := FindWriterPIDs(context.Background(), spy, WriterQuery{
		EnvName: "CODEX_HOME", EnvValue: "/nonexistent/aha2-writer-posix-test",
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if spy.command.Executable != "sh" {
		t.Fatalf("the scan must run under sh, got %q", spy.command.Executable)
	}
	// Only the executable lines matter: the explanatory comments name the
	// constructs they warn against, and a comment cannot break dash.
	var code []string
	for _, line := range strings.Split(strings.Join(spy.command.Args, "\n"), "\n") {
		if trimmed := strings.TrimSpace(line); !strings.HasPrefix(trimmed, "#") {
			code = append(code, line)
		}
	}
	script := strings.Join(code, "\n")
	for _, bashism := range []string{"read -d", "-d ''", "read -r -d", "[[", "<<<"} {
		if strings.Contains(script, bashism) {
			t.Errorf("scan uses a non-POSIX construct %q that dash rejects", bashism)
		}
	}
}
