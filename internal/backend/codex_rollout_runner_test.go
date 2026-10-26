package backend

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

// wslLikeRunner reports a Linux platform, which is how a WSL or SSH workspace
// looks to the adapter: the rollout file must be read through the runner, not
// opened by the AHA process.
type wslLikeRunner struct {
	path string
	log  []string
}

func (*wslLikeRunner) DetectedPlatform() string { return "Linux 6.6.0-generic x86_64" }

func (runner *wslLikeRunner) Run(_ context.Context, command workspace.Command, _ workspace.LineHandler) (workspace.Result, error) {
	runner.log = append(runner.log, command.Executable)
	switch command.Executable {
	case "sh":
		return workspace.Result{ExitCode: 0, Stdout: runner.path + "\n"}, nil
	case "tail":
		data, err := os.ReadFile(runner.path)
		if err != nil {
			return workspace.Result{ExitCode: 1}, nil
		}
		return workspace.Result{ExitCode: 0, Stdout: string(data)}, nil
	}
	return workspace.Result{ExitCode: 0}, nil
}

func TestCodexRolloutUsesRunnerForRemotePlatform(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-x-th.jsonl")
	line := `{"type":"response_item","payload":{"type":"reasoning","id":"r1","summary":[],"content":[{"type":"reasoning_text","text":"真实形态"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &wslLikeRunner{path: path}
	rollout := &codexRollout{home: "/home/u/.codex-env", thread: "th", runner: runner}

	events := rollout.drain(context.Background(), true)
	if len(events) != 1 || events[0].Type != "agent_reasoning" || events[0].Data["text"] != "真实形态" {
		t.Fatalf("runner path produced no reasoning event: %#v", events)
	}
	if len(runner.log) != 2 || runner.log[0] != "sh" || runner.log[1] != "tail" {
		t.Fatalf("expected discovery then tail through the runner, got %#v", runner.log)
	}
}

// The direct read is only correct when the file is local to the AHA host.
func TestCodexRolloutReadsFileForLocalWindowsRunner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions", "2026", "10", "26", "rollout-x-th.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"response_item","payload":{"type":"reasoning","id":"r9","summary":["摘要"],"content":[]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rollout := &codexRollout{home: dir, thread: "th"}
	events := rollout.drain(context.Background(), true)
	if len(events) != 1 || events[0].Data["text"] != "摘要" {
		t.Fatalf("direct read failed: %#v", events)
	}
}
