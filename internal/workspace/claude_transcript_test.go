package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeProjectSlugMatchesClaudeLayout(t *testing.T) {
	if got, want := ClaudeProjectSlug("/home/kaikai/kk-workspace/my_project/AHA2"),
		"-home-kaikai-kk-workspace-my-project-AHA2"; got != want {
		t.Fatalf("slug = %q, want %q", got, want)
	}
	if got, want := ClaudeProjectSlug("/tmp/a_b.c"), "-tmp-a-b-c"; got != want {
		t.Fatalf("slug = %q, want %q", got, want)
	}
}

func TestClaudeDefaultConfigDirOnAsksTheWorkspaceHost(t *testing.T) {
	t.Parallel()
	// A backend transcript lives in the workspace host's home. When the control
	// plane's own home differs from that host's, resolving it locally loses the
	// session, so the host must be asked.
	if got, want := ClaudeDefaultConfigDirOn(context.Background(), recordingRunner{t: t, home: "/host/home"}), "/host/home/.claude"; got != want {
		t.Fatalf("host config dir = %q, want %q", got, want)
	}
	// A host that cannot answer falls back to the control plane's own home.
	if got := ClaudeDefaultConfigDirOn(context.Background(), failingRunner{t: t}); got != DefaultClaudeConfigDir() {
		t.Fatalf("fallback config dir = %q, want %q", got, DefaultClaudeConfigDir())
	}
}

func TestClaudeDefaultConfigDirOnAsksAWindowsHostForItsProfile(t *testing.T) {
	t.Parallel()
	// A Windows host has no POSIX shell. Asking it for $HOME would fail and send
	// the resume fallback back to the control plane's own home, which is not where
	// the Windows target keeps its transcripts.
	runner := &windowsHostRunner{t: t, platform: "windows/amd64", profile: `C:\Users\target`}
	if got, want := ClaudeDefaultConfigDirOn(context.Background(), runner), `C:\Users\target\.claude`; got != want {
		t.Fatalf("Windows host config dir = %q, want %q", got, want)
	}
	if runner.shellQueries != 0 {
		t.Fatalf("a Windows host must not be asked with a POSIX shell, saw %d sh queries", runner.shellQueries)
	}
	// Failing to reach the host must not silently claim a directory.
	failing := &windowsHostRunner{t: t, platform: "windows/amd64", fail: true}
	if got := ClaudeDefaultConfigDirOn(context.Background(), failing); got != "" {
		t.Fatalf("unreachable Windows host resolved %q, want an empty result", got)
	}
}

type windowsHostRunner struct {
	t            *testing.T
	platform     string
	profile      string
	fail         bool
	shellQueries int
}

func (runner *windowsHostRunner) DetectedPlatform() string { return runner.platform }

func (runner *windowsHostRunner) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	if command.Executable == "sh" {
		runner.shellQueries++
		return Result{ExitCode: 127, Stderr: "'sh' is not recognized"}, nil
	}
	if command.Executable != "powershell.exe" {
		runner.t.Fatalf("unexpected probe executable %q", command.Executable)
	}
	if runner.fail {
		return Result{ExitCode: 1}, errors.New("host unreachable")
	}
	return Result{ExitCode: 0, Stdout: runner.profile}, nil
}

type recordingRunner struct {
	t    *testing.T
	home string
}

func (runner recordingRunner) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	assertShellHomeQuery(runner.t, command)
	return Result{ExitCode: 0, Stdout: runner.home + "/.claude"}, nil
}

type failingRunner struct{ t *testing.T }

func (runner failingRunner) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	assertShellHomeQuery(runner.t, command)
	return Result{ExitCode: 1}, errors.New("shell unavailable")
}

func assertShellHomeQuery(t *testing.T, command Command) {
	t.Helper()
	if command.Executable != "sh" {
		t.Fatalf("config dir must be resolved by asking the host, got %q", command.Executable)
	}
}

func TestClaudeTranscriptExistsRejectsEmptyConfigDir(t *testing.T) {
	t.Parallel()
	if ClaudeTranscriptExists(context.Background(), recordingRunner{t: t}, "", "/work", "session") {
		t.Fatal("an empty config directory must not be reported as resumable")
	}
}

func TestClaudeTranscriptExistsLocatesRealSession(t *testing.T) {
	def := DefaultClaudeConfigDir()
	if def == "" {
		t.Skip("no home directory")
	}
	workDir := "/home/kaikai/kk-workspace/my_project/AHA2"
	target := filepath.Join(def, "projects", ClaudeProjectSlug(workDir), "b81e3c4b-3d99-4963-ad4b-7755e831bcd4.jsonl")
	if _, err := os.Stat(target); err != nil {
		t.Skipf("fixture session not present: %v", err)
	}
	if !ClaudeTranscriptExists(context.Background(), LocalRunner{}, def, workDir, "b81e3c4b-3d99-4963-ad4b-7755e831bcd4") {
		t.Fatalf("fallback failed to locate the live session under %s", def)
	}
	if ClaudeTranscriptExists(context.Background(), LocalRunner{}, def, workDir, "00000000-0000-0000-0000-000000000000") {
		t.Fatal("a nonexistent session must not be reported as resumable")
	}
}
