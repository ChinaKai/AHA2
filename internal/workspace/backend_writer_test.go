package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// startFakeWriter launches a long-lived process carrying the given CODEX_HOME,
// mirroring how a leftover Codex backend keeps holding its provider session.
func startFakeWriter(t *testing.T, codexHome string) *exec.Cmd {
	t.Helper()
	command := exec.Command("sleep", "120")
	command.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	if err := command.Start(); err != nil {
		t.Fatalf("start fake writer: %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})
	return command
}

func TestFindCodexWriterPIDsMatchesOnlyThatHome(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep is unavailable")
	}
	item := domain.Workspace{Transport: "native", RootPath: t.TempDir()}
	runner := LocalRunner{}
	wanted := filepath.Join(item.RootPath, "codex-env", "session_wanted")
	other := filepath.Join(item.RootPath, "codex-env", "session_other")

	writer := startFakeWriter(t, wanted)
	startFakeWriter(t, other)
	// Give both processes a moment to be observable through /proc.
	time.Sleep(300 * time.Millisecond)

	pids, err := FindWriterPIDs(context.Background(), runner, WriterQuery{EnvName: "CODEX_HOME", EnvValue: wanted})
	if err != nil {
		t.Fatalf("find writer: %v", err)
	}
	if len(pids) != 1 || pids[0] != writer.Process.Pid {
		t.Fatalf("matched pids = %v, want exactly [%d]", pids, writer.Process.Pid)
	}

	// A home nobody uses must not report unrelated writers.
	if pids, err := FindWriterPIDs(context.Background(), runner, WriterQuery{EnvName: "CODEX_HOME", EnvValue: filepath.Join(item.RootPath, "unused")}); err != nil || len(pids) != 0 {
		t.Fatalf("unused home matched pids=%v err=%v, want none", pids, err)
	}
}

func TestTerminateProcessesStopsWriterAndReportsGone(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep is unavailable")
	}
	item := domain.Workspace{Transport: "native", RootPath: t.TempDir()}
	runner := LocalRunner{}
	home := filepath.Join(item.RootPath, "codex-env", "session_stop")
	writer := startFakeWriter(t, home)
	time.Sleep(300 * time.Millisecond)

	pids, err := FindWriterPIDs(context.Background(), runner, WriterQuery{EnvName: "CODEX_HOME", EnvValue: home})
	if err != nil || len(pids) != 1 {
		t.Fatalf("find writer: pids=%v err=%v", pids, err)
	}
	if err := TerminateProcesses(context.Background(), runner, pids); err != nil {
		t.Fatalf("terminate writer: %v", err)
	}
	// The process must be gone, not merely signalled.
	if err := writer.Process.Signal(os.Signal(nil)); err == nil {
		t.Fatal("writer was still running after TerminateProcesses")
	}
	if pids, err := FindWriterPIDs(context.Background(), runner, WriterQuery{EnvName: "CODEX_HOME", EnvValue: home}); err != nil || len(pids) != 0 {
		t.Fatalf("writer still detected: pids=%v err=%v", pids, err)
	}
}

func TestTerminateProcessesWithNoPIDsIsNoop(t *testing.T) {
	t.Parallel()
	if err := TerminateProcesses(context.Background(), LocalRunner{}, nil); err != nil {
		t.Fatalf("empty terminate: %v", err)
	}
}

func TestFindCodexWriterPIDsRejectsEmptyHome(t *testing.T) {
	t.Parallel()
	if _, err := FindWriterPIDs(context.Background(), LocalRunner{}, WriterQuery{EnvName: "CODEX_HOME", EnvValue: "  "}); err == nil {
		t.Fatal("an empty env value must be rejected")
	}
	if _, err := FindWriterPIDs(context.Background(), LocalRunner{}, WriterQuery{}); err == nil {
		t.Fatal("an empty writer query must be rejected")
	}
}

func TestCodexSessionHomeDirFollowsAccountLayout(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{Transport: "native", RootPath: "/work"}
	envHome := CodexSessionHomeDir(item, "/work/task", "session_1", "")
	if want := CodexEnvHomeDir(item, "/work/task", "session_1"); envHome != want {
		t.Fatalf("env home = %q, want %q", envHome, want)
	}
	authHome := CodexSessionHomeDir(item, "/work/task", "session_1", "account_1")
	if want := CodexAuthProfileDir(item, "/work/task", "session_1"); authHome != want {
		t.Fatalf("auth home = %q, want %q", authHome, want)
	}
	if envHome == authHome {
		t.Fatal("env and official-account runs must use different Codex homes")
	}
}

// startFakeResumeWriter launches a process whose command line carries the same
// "--resume <id>" shape AHA uses for Claude, so the matcher is exercised against
// the real argument form rather than an assumed one.
func startFakeResumeWriter(t *testing.T, sessionID string) *exec.Cmd {
	t.Helper()
	// A script file carries the flag form in its command line while staying
	// alive. Running a file (rather than `sh -c`) keeps the extra arguments:
	// a single `-c` command is collapsed into an exec that discards them, and
	// sleep itself would reject the unknown flag.
	script := filepath.Join(t.TempDir(), "fake-backend.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 60\n"), 0o700); err != nil {
		t.Fatalf("write fake backend script: %v", err)
	}
	command := exec.Command("sh", script, "--resume", sessionID)
	if err := command.Start(); err != nil {
		t.Fatalf("start fake resume writer: %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})
	return command
}

func TestFindWriterPIDsMatchesResumeFlagForm(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep is unavailable")
	}
	runner := LocalRunner{}
	wanted := "b81e3c4b-3d99-4963-ad4b-7755e831bcd4"
	writer := startFakeResumeWriter(t, wanted)
	time.Sleep(300 * time.Millisecond)

	// The backend passes the flag as "--resume <id>"; a pattern expecting a
	// standalone "resume" argument silently matches nothing.
	pids, err := FindWriterPIDs(context.Background(), runner, WriterQuery{ResumeSessionID: wanted})
	if err != nil {
		t.Fatalf("find resume writer: %v", err)
	}
	found := false
	for _, pid := range pids {
		if pid == writer.Process.Pid {
			found = true
		}
	}
	if !found {
		t.Fatalf("matched %v, want it to include %d", pids, writer.Process.Pid)
	}
	if pids, err := FindWriterPIDs(context.Background(), runner, WriterQuery{ResumeSessionID: "00000000-0000-0000-0000-000000000000"}); err != nil || len(pids) != 0 {
		t.Fatalf("unrelated session matched %v err=%v", pids, err)
	}
}

func TestFindWriterPIDsCombinesEnvAndResumeQueries(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep is unavailable")
	}
	runner := LocalRunner{}
	home := "/tmp/aha2-writer-combined-home"
	writer := startFakeWriter(t, home)
	time.Sleep(300 * time.Millisecond)

	// A query carrying both must still find the env match: the env branch runs
	// first and a miss there must not skip the resume branch or vice versa.
	pids, err := FindWriterPIDs(context.Background(), runner, WriterQuery{
		EnvName: "CODEX_HOME", EnvValue: home, ResumeSessionID: "unused-session",
	})
	if err != nil {
		t.Fatalf("combined find: %v", err)
	}
	found := false
	for _, pid := range pids {
		if pid == writer.Process.Pid {
			found = true
		}
	}
	if !found {
		t.Fatalf("combined query matched %v, want it to include %d", pids, writer.Process.Pid)
	}
}

// windowsWriterRunner stands in for an SSH target running Windows: it has no
// POSIX shell, and it answers a process query through CIM.
type windowsWriterRunner struct {
	t        *testing.T
	platform string
	stdout   string
	fail     bool
	shCalls  int
	commands []string
}

func (runner *windowsWriterRunner) DetectedPlatform() string { return runner.platform }

func (runner *windowsWriterRunner) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	runner.commands = append(runner.commands, command.Executable)
	if command.Executable == "sh" {
		runner.shCalls++
		return Result{ExitCode: 127, Stderr: "'sh' is not recognized"}, nil
	}
	if command.Executable != "powershell.exe" {
		runner.t.Fatalf("unexpected executable on a Windows target: %q", command.Executable)
	}
	if runner.fail {
		return Result{ExitCode: 1}, errors.New("host unreachable")
	}
	return Result{ExitCode: 0, Stdout: runner.stdout}, nil
}

func TestFindWriterPIDsOnWindowsMatchesByResumeSessionID(t *testing.T) {
	t.Parallel()
	runner := &windowsWriterRunner{t: t, platform: "windows/amd64", stdout: "4120\r\n9981\r\n"}
	pids, err := FindWriterPIDs(context.Background(), runner, WriterQuery{ResumeSessionID: "b81e3c4b-3d99-4963-ad4b-7755e831bcd4"})
	if err != nil {
		t.Fatalf("windows writer lookup: %v", err)
	}
	if len(pids) != 2 || pids[0] != 4120 || pids[1] != 9981 {
		t.Fatalf("pids = %v, want [4120 9981]", pids)
	}
	if runner.shCalls != 0 {
		t.Fatalf("a Windows target must not be probed with a POSIX shell, saw %d sh calls", runner.shCalls)
	}
}

func TestFindWriterPIDsOnWindowsRejectsUnsafeSessionID(t *testing.T) {
	t.Parallel()
	runner := &windowsWriterRunner{t: t, platform: "windows/amd64"}
	// The session id reaches a PowerShell script, so anything outside the known
	// id shape must be refused rather than escaped.
	for _, value := range []string{`x'; Remove-Item -Recurse C:\;'`, "short", strings.Repeat("a", 200), "with space"} {
		if _, err := FindWriterPIDs(context.Background(), runner, WriterQuery{ResumeSessionID: value}); err == nil {
			t.Fatalf("session id %q was accepted for a Windows lookup", value)
		}
	}
	if len(runner.commands) != 0 {
		t.Fatalf("an unsafe session id must be rejected before any command runs, ran %v", runner.commands)
	}
}

func TestFindWriterPIDsOnWindowsNeedsAResumeSessionID(t *testing.T) {
	t.Parallel()
	runner := &windowsWriterRunner{t: t, platform: "windows/amd64"}
	// A Windows target cannot see another process's environment, so a per-session
	// home alone cannot identify a writer there. Failing loudly keeps the caller
	// from mistaking "nothing found" for "no writer", which would reuse a session
	// whose previous writer is still appending to it.
	if _, err := FindWriterPIDs(context.Background(), runner, WriterQuery{EnvName: "CLAUDE_CONFIG_DIR", EnvValue: "/home/x/.claude"}); err == nil {
		t.Fatal("a Windows lookup without a resume session id must fail")
	}
}

func TestTerminateProcessesUsesWindowsStopOnWindowsTargets(t *testing.T) {
	t.Parallel()
	runner := &windowsWriterRunner{t: t, platform: "windows/amd64"}
	if err := TerminateProcesses(context.Background(), runner, nil); err != nil {
		t.Fatalf("no pids must be a no-op: %v", err)
	}
	if runner.shCalls != 0 {
		t.Fatalf("an empty pid list must not run anything, saw %d sh calls", runner.shCalls)
	}
}
