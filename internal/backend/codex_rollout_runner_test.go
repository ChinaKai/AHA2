package backend

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		// Discovery returns the path; sizing returns the byte count.
		if len(command.Args) > 1 && strings.Contains(command.Args[1], "wc -c") {
			info, err := os.Stat(runner.path)
			if err != nil {
				return workspace.Result{ExitCode: 1}, nil
			}
			return workspace.Result{ExitCode: 0, Stdout: strconv.FormatInt(info.Size(), 10) + "\n"}, nil
		}
		return workspace.Result{ExitCode: 0, Stdout: runner.path + "\n"}, nil
	case "tail":
		// Emulate `tail -c +N`: read from byte N-1 so priming is observable.
		data, err := os.ReadFile(runner.path)
		if err != nil {
			return workspace.Result{ExitCode: 1}, nil
		}
		start := int64(0)
		if len(command.Args) > 1 {
			if offset, err := strconv.ParseInt(strings.TrimPrefix(command.Args[1], "+"), 10, 64); err == nil && offset > 1 {
				start = offset - 1
			}
		}
		if start >= int64(len(data)) {
			return workspace.Result{ExitCode: 0}, nil
		}
		return workspace.Result{ExitCode: 0, Stdout: string(data[start:])}, nil
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

// A resumed session keeps one rollout file and appends to it. Priming at turn
// start must move the reader past everything already on disk, so a follow-up
// turn only shows its own thinking.
func TestCodexRolloutPrimesPastExistingHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions", "2026", "10", "26", "rollout-x-th.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	history := `{"timestamp":"2026-10-26T01:00:00.000Z","type":"response_item","payload":{"type":"reasoning","id":"old","summary":[],"content":[{"type":"reasoning_text","text":"上一轮的思考"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	rollout := &codexRollout{home: dir, thread: "th"}
	rollout.prime(context.Background())

	if events := rollout.drain(context.Background(), true); len(events) != 0 {
		t.Fatalf("history must not be replayed: %#v", events)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"timestamp":"2026-10-26T02:00:00.000Z","type":"response_item","payload":{"type":"reasoning","id":"new","summary":[],"content":[{"type":"reasoning_text","text":"这一轮的思考"}]}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()

	events := rollout.drain(context.Background(), true)
	if len(events) != 1 || events[0].Data["text"] != "这一轮的思考" {
		t.Fatalf("this turn's reasoning missing: %#v", events)
	}
}

// A remote (WSL/SSH) workspace must size the rollout through the runner too.
func TestCodexRolloutPrimesThroughRunner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-x-th.jsonl")
	history := `{"timestamp":"2026-10-26T01:00:00.000Z","type":"response_item","payload":{"type":"reasoning","id":"old","summary":[],"content":[{"type":"reasoning_text","text":"上一轮的思考"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &wslLikeRunner{path: path}
	rollout := &codexRollout{home: "/home/u/.codex-env", thread: "th", runner: runner}
	rollout.prime(context.Background())

	if rollout.offset != int64(len(history)) {
		t.Fatalf("expected offset at end of existing rollout, got %d", rollout.offset)
	}
	if got := runner.log; len(got) < 2 || got[0] != "sh" {
		t.Fatalf("expected discovery then size through the runner, got %#v", got)
	}
	if events := rollout.drain(context.Background(), true); len(events) != 0 {
		t.Fatalf("history must not be replayed: %#v", events)
	}
}
