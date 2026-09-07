package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type contextRunnerFunc func(Command) (Result, error)

func (runner contextRunnerFunc) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	return runner(command)
}

func TestMaterializeContextWritesInsideNativeWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "main", "manifest.json")
	err := MaterializeContext(context.Background(), domain.Workspace{Transport: "native"}, root, root, map[string]string{
		target: `{"version":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != `{"version":1}` {
		t.Fatalf("materialized=%q err=%v", data, err)
	}
	if err := materializeLocalContext(root, map[string]string{filepath.Join(filepath.Dir(root), "escape"): "bad"}); err == nil {
		t.Fatal("workspace escape was accepted")
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
	if err == nil || err.Error() != "configure context ignore: permission denied" {
		t.Fatalf("write failure=%v", err)
	}
}
