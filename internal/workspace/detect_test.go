package workspace

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type detectRunnerFunc func(Command) (Result, error)

func (runner detectRunnerFunc) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	return runner(command)
}

type windowsDetectRunner struct {
	commands []string
}

func (runner *windowsDetectRunner) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	runner.commands = append(runner.commands, command.Executable)
	switch command.Executable {
	case "pwd":
		return Result{Stdout: `C:\workspace`}, nil
	case "git":
		return Result{ExitCode: 128, Stderr: "fatal: not a git repository"}, nil
	case "codex", "claude":
		return Result{Stdout: command.Executable + " 1.0"}, nil
	default:
		return Result{}, errors.New("unexpected command")
	}
}

func (runner *windowsDetectRunner) DetectedPlatform() string { return "windows/amd64" }

func TestDetectUsesSSHRunnerTargetPlatformWithoutUnixProbe(t *testing.T) {
	runner := &windowsDetectRunner{}
	item := domain.Workspace{ID: "workspace-windows", Locality: "remote", Transport: "ssh", RootPath: `C:\workspace`}
	detected, err := detectWithRunner(context.Background(), item, runner)
	if err != nil {
		t.Fatal(err)
	}
	if detected.Platform != "windows/amd64" || detected.Health != "ready" {
		t.Fatalf("unexpected Windows detection: %#v", detected)
	}
	if strings.Contains(strings.Join(runner.commands, ","), "uname") {
		t.Fatalf("Windows target ran Unix platform probe: %#v", runner.commands)
	}
}

func TestDetectClearsStaleStateBeforeWorkspaceAccessFailure(t *testing.T) {
	item := domain.Workspace{
		ID: "workspace-stale", Locality: "remote", Transport: "ssh", RootPath: "/missing",
		SSHPassword: "do-not-display", Platform: "old-platform", Health: "ready",
		Capabilities: map[string]any{"codex": map[string]any{"status": "ready"}},
		Repository:   map[string]any{"is_git": true, "branch": "old"},
	}
	detected, err := detectWithRunner(context.Background(), item, detectRunnerFunc(func(command Command) (Result, error) {
		if command.Executable != "pwd" {
			t.Fatalf("probe %q ran after workspace access failed", command.Executable)
		}
		return Result{ExitCode: 2, Stderr: "cannot cd using do-not-display"}, nil
	}))
	if err == nil || DetectionErrorCode(err) != "workspace_unavailable" {
		t.Fatalf("unexpected error: %v (%s)", err, DetectionErrorCode(err))
	}
	if detected.Platform != "" || detected.Health != "error" || len(detected.Repository) != 0 {
		t.Fatalf("stale detection state survived: %#v", detected)
	}
	if _, exists := detected.Capabilities["codex"]; exists {
		t.Fatalf("stale backend capability survived: %#v", detected.Capabilities)
	}
	workspaceProbe := detected.Capabilities["workspace"].(map[string]any)
	if workspaceProbe["status"] != probeUnavailable || !strings.Contains(workspaceProbe["error"].(string), "[已隐藏]") || strings.Contains(workspaceProbe["error"].(string), item.SSHPassword) {
		t.Fatalf("workspace error was not safely preserved: %#v", workspaceProbe)
	}
}

func TestDetectDistinguishesRepositoryAndBackendResults(t *testing.T) {
	item := domain.Workspace{ID: "workspace-probes", Locality: "remote", Transport: "ssh", RootPath: "/srv/project"}
	detected, err := detectWithRunner(context.Background(), item, detectRunnerFunc(func(command Command) (Result, error) {
		switch command.Executable {
		case "pwd":
			return Result{Stdout: "/srv/project\n"}, nil
		case "uname":
			return Result{Stdout: "Linux 6.8 x86_64\n"}, nil
		case "git":
			return Result{ExitCode: 128, Stderr: "fatal: not a git repository"}, nil
		case "codex":
			return Result{}, exec.ErrNotFound
		case "claude":
			return Result{ExitCode: 2, Stderr: "configuration invalid"}, nil
		default:
			return Result{}, errors.New("unexpected command")
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if detected.Platform != "Linux 6.8 x86_64" || detected.Health != "degraded" {
		t.Fatalf("unexpected workspace result: %#v", detected)
	}
	if detected.Repository["status"] != probeNotRepository || detected.Repository["is_git"] != false {
		t.Fatalf("Git non-repository was misclassified: %#v", detected.Repository)
	}
	codex := detected.Capabilities["codex"].(map[string]any)
	claude := detected.Capabilities["claude"].(map[string]any)
	if codex["status"] != probeNotInstalled || codex["error"] != nil {
		t.Fatalf("missing Codex was misclassified: %#v", codex)
	}
	if claude["status"] != probeExecutionFailed || !strings.Contains(claude["error"].(string), "configuration invalid") {
		t.Fatalf("failed Claude probe was misclassified: %#v", claude)
	}
}

func TestDetectDistinguishesGitExecutionFailure(t *testing.T) {
	item := domain.Workspace{ID: "workspace-git-error", Locality: "remote", Transport: "ssh", RootPath: "/srv/project"}
	detected, err := detectWithRunner(context.Background(), item, detectRunnerFunc(func(command Command) (Result, error) {
		switch command.Executable {
		case "pwd":
			return Result{Stdout: item.RootPath}, nil
		case "uname":
			return Result{Stdout: "Linux"}, nil
		case "git":
			return Result{ExitCode: 2, Stderr: "permission denied while running git"}, nil
		case "codex", "claude":
			return Result{Stdout: command.Executable + " 1.0"}, nil
		default:
			return Result{}, errors.New("unexpected command")
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if detected.Health != "degraded" || detected.Repository["status"] != probeExecutionFailed || !strings.Contains(detected.Repository["error"].(string), "permission denied") {
		t.Fatalf("Git execution failure was not retained: %#v", detected)
	}
}

func TestDetectRetriesOneTransientWSLDeadline(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{ID: "workspace-wsl", Locality: "local", Transport: "wsl", RootPath: "/srv/project"}
	pwdCalls := 0
	detected, err := detectWithRunner(context.Background(), item, detectRunnerFunc(func(command Command) (Result, error) {
		switch command.Executable {
		case "pwd":
			pwdCalls++
			if pwdCalls == 1 {
				return Result{}, context.DeadlineExceeded
			}
			return Result{Stdout: item.RootPath}, nil
		case "uname":
			return Result{Stdout: "Linux"}, nil
		case "git":
			return Result{ExitCode: 128, Stderr: "not a git repository"}, nil
		case "codex", "claude":
			return Result{ExitCode: 127, Stderr: "command not found"}, nil
		default:
			return Result{}, errors.New("unexpected command")
		}
	}))
	if err != nil || detected.Health != "ready" || pwdCalls != 2 {
		t.Fatalf("detected=%#v err=%v pwd_calls=%d", detected, err, pwdCalls)
	}
}

func TestDetectionRetryDoesNotRepeatSSHOrCancelledWSL(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		item domain.Workspace
		ctx  context.Context
	}{
		{name: "ssh", item: domain.Workspace{Transport: "ssh"}, ctx: context.Background()},
		{name: "cancelled-wsl", item: domain.Workspace{Transport: "wsl"}, ctx: cancelledContext()},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			_, err := runDetectionCommand(test.ctx, test.item, detectRunnerFunc(func(Command) (Result, error) {
				calls++
				return Result{}, context.DeadlineExceeded
			}), Command{Executable: "pwd"})
			if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestSameWorkspaceRootRejectsNestedDirectory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		item := domain.Workspace{RootPath: `E:\repo\nested`, Locality: "local", Transport: "native"}
		if sameWorkspaceRoot(item, `E:/repo`) {
			t.Fatal("nested directory was treated as repository root")
		}
		if !sameWorkspaceRoot(domain.Workspace{RootPath: `E:\repo`, Locality: "local"}, `E:/repo`) {
			t.Fatal("equivalent Windows roots did not match")
		}
		return
	}
	item := domain.Workspace{RootPath: "/repo/nested", Locality: "local", Transport: "native"}
	if sameWorkspaceRoot(item, "/repo") {
		t.Fatal("nested directory was treated as repository root")
	}
}

func TestSameWorkspaceRootRemote(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{RootPath: "/srv/project", Locality: "remote", Transport: "ssh"}
	if !sameWorkspaceRoot(item, "/srv/project/") {
		t.Fatal("equivalent remote roots did not match")
	}
	if sameWorkspaceRoot(item, "/srv") {
		t.Fatal("remote parent repository was accepted")
	}
}

func TestSameWorkspaceRootRemoteWindowsIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{RootPath: `C:\Work\Project`, Locality: "remote", Transport: "ssh", Platform: "windows/amd64"}
	if !sameWorkspaceRoot(item, `c:/work/project/`) {
		t.Fatal("equivalent remote Windows roots did not match")
	}
	if sameWorkspaceRoot(item, `C:\Work`) {
		t.Fatal("remote Windows parent was accepted")
	}
	unc := domain.Workspace{RootPath: `\\server\share\Project`, Locality: "remote", Transport: "ssh", Platform: "windows/amd64"}
	if !sameWorkspaceRoot(unc, `//SERVER/share/project/`) || !remotePathIsAbs(unc, unc.RootPath) {
		t.Fatal("equivalent remote Windows UNC roots did not match")
	}
}
