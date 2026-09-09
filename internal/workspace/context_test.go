package workspace

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type contextRunnerFunc func(Command) (Result, error)

func (runner contextRunnerFunc) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	return runner(command)
}

type windowsContextRunner struct {
	commands []Command
}

func (runner *windowsContextRunner) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	runner.commands = append(runner.commands, command)
	return Result{}, nil
}

func (runner *windowsContextRunner) DetectedPlatform() string { return "windows/amd64" }

func TestMaterializeContextWritesInsideNativeWorkspace(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".aha2-context", "task-1", "main")
	target := filepath.Join(root, "task.md")
	stale := filepath.Join(root, "knowledge", "old-path.md")
	if err := os.MkdirAll(filepath.Dir(stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := MaterializeContext(context.Background(), domain.Workspace{Transport: "native"}, workspace, root, map[string]string{
		target: `{"version":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != `{"version":1}` {
		t.Fatalf("materialized=%q err=%v", data, err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale context survived reset: %v", err)
	}
	if err := materializeLocalContext(root, map[string]string{filepath.Join(filepath.Dir(root), "escape"): "bad"}); err == nil {
		t.Fatal("workspace escape was accepted")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != `{"version":1}` {
		t.Fatalf("failed validation changed existing context: %q %v", data, err)
	}
	if err := materializeLocalContext(workspace, map[string]string{filepath.Join(workspace, "unsafe"): "bad"}); err == nil {
		t.Fatal("unsafe materialization root was accepted")
	}
}

func TestPruneSharedContextsKeepsOnlyExplicitTaskSnapshots(t *testing.T) {
	t.Parallel()
	workspaceRoot := t.TempDir()
	taskRoot := filepath.Join(workspaceRoot, ".aha2-context", "task-1")
	current := filepath.Join(taskRoot, "shared-"+strings.Repeat("a", sha256HexLength))
	active := filepath.Join(taskRoot, "shared-"+strings.Repeat("b", sha256HexLength))
	stale := filepath.Join(taskRoot, "shared-"+strings.Repeat("c", sha256HexLength))
	malformed := filepath.Join(taskRoot, "shared-not-a-snapshot")
	agentRoot := filepath.Join(taskRoot, "main")
	otherTask := filepath.Join(workspaceRoot, ".aha2-context", "task-2", "shared-"+strings.Repeat("d", sha256HexLength))
	for _, root := range []string{current, active, stale, malformed, agentRoot, otherTask} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if err := PruneSharedContexts(context.Background(), domain.Workspace{Transport: "native"}, workspaceRoot, current, []string{active, otherTask}); err != nil {
		t.Fatal(err)
	}
	for _, retained := range []string{current, active, malformed, agentRoot, otherTask} {
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("retained path %s: %v", retained, err)
		}
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale shared snapshot survived: %v", err)
	}
}

func TestPruneSharedContextsRejectsBroadOrMalformedRoots(t *testing.T) {
	t.Parallel()
	workspaceRoot := t.TempDir()
	taskRoot := filepath.Join(workspaceRoot, ".aha2-context", "task-1")
	stale := filepath.Join(taskRoot, "shared-"+strings.Repeat("c", sha256HexLength))
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), ".aha2-context", "task-outside", "shared-"+strings.Repeat("e", sha256HexLength))
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{taskRoot, filepath.Join(taskRoot, "shared-invalid"), workspaceRoot, outside} {
		if err := PruneSharedContexts(context.Background(), domain.Workspace{Transport: "native"}, workspaceRoot, root, nil); err == nil {
			t.Fatalf("unsafe prune root was accepted: %s", root)
		}
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("failed validation changed shared snapshot: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside snapshot was changed: %v", err)
	}
}

func TestPruneRemoteSharedContextsUsesDirectChildBoundary(t *testing.T) {
	t.Parallel()
	taskRoot := "/srv/repo/.aha2-context/task-1"
	keep := map[string]struct{}{"shared-" + strings.Repeat("a", sha256HexLength): {}}
	for _, item := range []domain.Workspace{
		{Transport: "ssh", Platform: "linux/amd64"},
		{Transport: "ssh", Platform: "windows/amd64"},
	} {
		var command Command
		runner := contextRunnerFunc(func(value Command) (Result, error) {
			command = value
			return Result{}, nil
		})
		if err := pruneRemoteSharedContexts(context.Background(), item, "/srv/repo", runner, taskRoot, keep); err != nil {
			t.Fatal(err)
		}
		if command.Dir != "/srv/repo" || !strings.Contains(strings.Join(command.Args, " "), taskRoot) {
			t.Fatalf("remote prune escaped Task boundary: %#v", command)
		}
		joined := strings.Join(command.Args, " ")
		strictFilter := strings.Contains(joined, "shared-[0-9a-f]") ||
			strings.Contains(joined, "[!0-9a-f]") && strings.Contains(joined, "-eq 64")
		if !strictFilter || !strings.Contains(joined, "shared-"+strings.Repeat("a", sha256HexLength)) {
			t.Fatalf("remote prune lacks strict snapshot filter or keep set: %#v", command)
		}
	}
}

func TestSharedContextLocationPreservesWindowsUNC(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{Transport: "ssh", Platform: "windows/amd64"}
	root := `\\server\share\repo\.aha2-context\task-1\shared-` + strings.Repeat("a", sha256HexLength)
	taskRoot, name, err := sharedContextLocation(item, cleanContextPath(item, root))
	if err != nil {
		t.Fatal(err)
	}
	if taskRoot != "//server/share/repo/.aha2-context/task-1" || name != "shared-"+strings.Repeat("a", sha256HexLength) {
		t.Fatalf("UNC shared Context location = %q %q", taskRoot, name)
	}
}

func TestMaterializeRemoteContextResetsBeforeWriting(t *testing.T) {
	t.Parallel()
	root := "/srv/repo/.aha2-context/task-1/main"
	calls := 0
	writes := map[string]string{}
	runner := contextRunnerFunc(func(command Command) (Result, error) {
		calls++
		if command.Executable != "sh" || len(command.Args) != 4 {
			t.Fatalf("unexpected command: %#v", command)
		}
		if command.Args[2] != "aha-context-reset-or-write" || command.Args[3] != root ||
			!strings.Contains(command.Args[1], "rm -rf") || !strings.Contains(command.Args[1], "tar -xf") {
			t.Fatalf("invalid batch command: %#v", command)
		}
		reader := tar.NewReader(strings.NewReader(command.Stdin))
		for {
			header, err := reader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if header.Typeflag != tar.TypeReg {
				continue
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			writes[root+"/"+header.Name] = string(data)
		}
		return Result{}, nil
	})
	files := map[string]string{
		root + "/task.md":             "task",
		root + "/knowledge/global.md": "current",
	}
	if err := materializeRemoteContext(context.Background(), domain.Workspace{Transport: "ssh"}, runner, root, files); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(writes) != len(files) {
		t.Fatalf("calls=%d writes=%#v", calls, writes)
	}
	for target, content := range files {
		if writes[target] != content {
			t.Fatalf("remote file %s=%q", target, writes[target])
		}
	}
}

func TestMaterializeRemoteWindowsContextDoesNotRequireUnixTools(t *testing.T) {
	t.Parallel()
	root := `C:/repo/.aha2-context/task-1/main`
	runner := &windowsContextRunner{}
	files := map[string]string{
		root + "/task.md":        "task",
		root + "/nested/info.md": "info",
	}
	item := domain.Workspace{Transport: "ssh", Locality: "remote", Platform: "windows/amd64"}
	if err := materializeRemoteContext(context.Background(), item, runner, root, files); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("Windows context commands=%d: %#v", len(runner.commands), runner.commands)
	}
	for _, command := range runner.commands {
		if command.Executable != "powershell.exe" {
			t.Fatalf("Windows context used Unix command: %#v", command)
		}
	}
	if runner.commands[1].Stdin != "info" || runner.commands[2].Stdin != "task" {
		t.Fatalf("Windows context contents were not preserved: %#v", runner.commands)
	}
}

func TestRemoteContextMaterializationRetriesDeadlineOnceForWSLOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		transport string
		wantCalls int
		wantErr   bool
	}{
		{name: "wsl", transport: "wsl", wantCalls: 2},
		{name: "ssh", transport: "ssh", wantCalls: 1, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			runner := contextRunnerFunc(func(command Command) (Result, error) {
				calls++
				if calls == 1 {
					return Result{}, context.DeadlineExceeded
				}
				return Result{}, nil
			})
			err := materializeRemoteContext(context.Background(), domain.Workspace{Transport: test.transport}, runner,
				"/srv/repo/.aha2-context/task-1/main", map[string]string{
					"/srv/repo/.aha2-context/task-1/main/task.md": "task",
				})
			if calls != test.wantCalls || (err != nil) != test.wantErr {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if err != nil && !strings.Contains(err.Error(), "reset-or-write") {
				t.Fatalf("missing stage in error: %v", err)
			}
		})
	}
}

func TestRemoteContextMaterializationDoesNotRetryExpiredParent(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := materializeRemoteContext(ctx, domain.Workspace{Transport: "wsl"}, contextRunnerFunc(func(Command) (Result, error) {
		calls++
		return Result{}, context.DeadlineExceeded
	}), "/srv/repo/.aha2-context/task-1/main", map[string]string{
		"/srv/repo/.aha2-context/task-1/main/task.md": "task",
	})
	if calls != 1 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestRemoteContextRejectsEscapesBeforeReset(t *testing.T) {
	t.Parallel()
	root := "/srv/repo/.aha2-context/task-1/main"
	for _, target := range []string{
		root,
		root + "/../other/task.md",
		root + "-other/task.md",
	} {
		t.Run(target, func(t *testing.T) {
			calls := 0
			err := materializeRemoteContext(context.Background(), domain.Workspace{Transport: "ssh"}, contextRunnerFunc(func(Command) (Result, error) {
				calls++
				return Result{}, nil
			}), root, map[string]string{target: "bad"})
			if calls != 0 || err == nil || !strings.Contains(err.Error(), "reset-or-write") {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestWSLContextDeadlineRetryBudgetIsShared(t *testing.T) {
	t.Parallel()
	calls := 0
	runner := retryRemoteContextRunner(domain.Workspace{Transport: "wsl"}, contextRunnerFunc(func(Command) (Result, error) {
		calls++
		return Result{}, context.DeadlineExceeded
	}))
	for range 2 {
		if _, err := runner.Run(context.Background(), Command{Executable: "sh"}, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if calls != 3 {
		t.Fatalf("underlying calls=%d, want one retry across both commands", calls)
	}
}

func TestRemoteContextIgnoreSkipsNonGitWorkspace(t *testing.T) {
	calls := 0
	err := ensureRemoteContextIgnored(context.Background(), domain.Workspace{Transport: "wsl"}, "/srv/plain", contextRunnerFunc(func(command Command) (Result, error) {
		calls++
		if command.Executable != "git" || command.Dir != "/srv/plain" {
			t.Fatalf("unexpected probe: %#v", command)
		}
		return Result{ExitCode: 128, Stderr: "not a git repository"}, nil
	}))
	if err != nil || calls != 1 {
		t.Fatalf("non-Git ignore err=%v calls=%d", err, calls)
	}
}

func TestRemoteContextIgnoreAnchorsRelativeGitPathToWorkspace(t *testing.T) {
	calls := 0
	err := ensureRemoteContextIgnored(context.Background(), domain.Workspace{Transport: "ssh"}, "/srv/repo", contextRunnerFunc(func(command Command) (Result, error) {
		calls++
		switch calls {
		case 1:
			if command.Executable != "git" || command.Dir != "/srv/repo" {
				t.Fatalf("unexpected Git probe: %#v", command)
			}
			return Result{Stdout: ".git/info/exclude\n"}, nil
		case 2:
			if command.Executable != "sh" || command.Dir != "/srv/repo" {
				t.Fatalf("unexpected ignore write: %#v", command)
			}
			if got := command.Args[len(command.Args)-1]; got != "/srv/repo/.git/info/exclude" {
				t.Fatalf("exclude path=%q", got)
			}
			return Result{}, nil
		default:
			return Result{}, errors.New("unexpected extra command")
		}
	}))
	if err != nil || calls != 2 {
		t.Fatalf("relative Git ignore err=%v calls=%d", err, calls)
	}
}

func TestRemoteContextIgnoreReportsWriteFailure(t *testing.T) {
	calls := 0
	err := ensureRemoteContextIgnored(context.Background(), domain.Workspace{Transport: "ssh"}, "/srv/repo", contextRunnerFunc(func(command Command) (Result, error) {
		calls++
		if calls == 1 {
			return Result{Stdout: "/srv/repo/.git/info/exclude"}, nil
		}
		return Result{ExitCode: 1, Stderr: "permission denied"}, nil
	}))
	if err == nil || err.Error() != "context ignore stage: configure context ignore: permission denied" {
		t.Fatalf("write failure=%v", err)
	}
}

func TestRemoteContextIgnoreRetriesDeadlineForWSLOnly(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		transport string
		wantCalls int
		wantErr   bool
	}{
		{transport: "wsl", wantCalls: 2},
		{transport: "ssh", wantCalls: 1, wantErr: true},
	} {
		t.Run(test.transport, func(t *testing.T) {
			calls := 0
			err := ensureRemoteContextIgnored(context.Background(), domain.Workspace{Transport: test.transport}, "/srv/plain", contextRunnerFunc(func(Command) (Result, error) {
				calls++
				if calls == 1 {
					return Result{}, context.DeadlineExceeded
				}
				return Result{ExitCode: 128}, nil
			}))
			if calls != test.wantCalls || (err != nil) != test.wantErr {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if err != nil && !strings.Contains(err.Error(), "ignore stage") {
				t.Fatalf("missing stage in error: %v", err)
			}
		})
	}
}
