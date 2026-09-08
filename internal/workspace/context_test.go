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
