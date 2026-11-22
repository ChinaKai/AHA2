package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

// This helper speaks JSON-RPC over real process pipes without invoking Codex.
func TestCodexAppServerProcessHelper(t *testing.T) {
	mode := os.Getenv("AHA2_APP_SERVER_PROCESS_TEST")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var message struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			os.Exit(90)
		}
		if mode == "handshake-failure" {
			os.Exit(0)
		}
		result := map[string]any{}
		switch message.Method {
		case "initialize":
		case "initialized":
			continue
		case "thread/resume":
			size := 5 * 1024 * 1024
			if mode == "overflow" {
				size = 17 * 1024 * 1024
			}
			result = map[string]any{"thread": map[string]any{"id": "synthetic-thread"}, "padding": strings.Repeat("x", size)}
		case "turn/start":
			result = map[string]any{"turn": map[string]any{"id": "synthetic-turn"}}
		default:
			os.Exit(91)
		}
		if encoder.Encode(map[string]any{"id": message.ID, "result": result}) != nil {
			os.Exit(92)
		}
		if mode == "overflow" && message.Method == "thread/resume" {
			time.Sleep(30 * time.Second)
		}
		if message.Method == "turn/start" {
			if strings.HasPrefix(mode, "provider-failed") {
				_, _ = io.WriteString(os.Stdout, `{"method":"turn/completed","params":{"threadId":"synthetic-thread","turn":{"id":"synthetic-turn","status":"failed","error":{"message":"synthetic-provider-quota-exhausted","codexErrorInfo":"usageLimitExceeded"}}}}`+"\n")
				if mode == "provider-failed-scan" {
					_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 17*1024*1024))
					time.Sleep(30 * time.Second)
				}
				continue
			}
			_, _ = io.WriteString(os.Stdout, `{"method":"item/completed","params":{"threadId":"synthetic-thread","turnId":"synthetic-turn","item":{"type":"agentMessage","id":"reply","text":"synthetic-result","phase":"final_answer"}}}`+"\n")
			_, _ = io.WriteString(os.Stdout, `{"method":"turn/completed","params":{"threadId":"synthetic-thread","turn":{"id":"synthetic-turn","status":"completed"}}}`+"\n")
		}
	}
	os.Exit(0)
}

type appServerProcessTestRunner struct {
	mode          string
	command       workspace.Command
	afterRunError error
}

func (*appServerProcessTestRunner) StreamsInput() bool { return true }
func (runner *appServerProcessTestRunner) Run(ctx context.Context, command workspace.Command, handler workspace.LineHandler) (workspace.Result, error) {
	runner.command = command
	// Replace only the executable/argv with the test binary. The adapter's input,
	// limits, context and line handler still cross LocalRunner and scanOutput.
	command.Args = []string{"-test.run=^TestCodexAppServerProcessHelper$"}
	command.Env = map[string]string{"AHA2_APP_SERVER_PROCESS_TEST": runner.mode}
	result, err := (workspace.LocalRunner{}).Run(ctx, command, handler)
	if err != nil {
		return result, err
	}
	return result, runner.afterRunError
}

func TestCodexAppServerRealProcessLargeResumeAndLimitFailure(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"large", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			runner := &appServerProcessTestRunner{mode: mode}
			request := Request{Runner: runner, WorkDir: t.TempDir(), Prompt: "synthetic", ProviderSessionID: "synthetic-thread"}
			start := time.Now()
			result, err, started := (Codex{}).executeLiveInbox(context.Background(), request, nil, executable, 8*time.Second)
			if mode == "large" {
				if err != nil || !started || result.Reply != "synthetic-result" || result.ProviderSessionID != "synthetic-thread" {
					t.Fatalf("started=%v result=%+v err=%v", started, result, err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "line exceeds 16777216 byte scan limit") || strings.Contains(err.Error(), "timed out") {
					t.Fatalf("explicit frame error lost: %v", err)
				}
				if started {
					t.Fatal("turn started after failed resume frame")
				}
				if time.Since(start) > 6*time.Second {
					t.Fatalf("frame failure waited too long: %s", time.Since(start))
				}
			}
			if runner.command.StdoutLineLimit != 16*1024*1024 || runner.command.OutputLimit != 64*1024 {
				t.Fatal("adapter did not configure bounded protocol output")
			}
		})
	}
}

// Provider failure comes from real JSON-RPC process output; only the independent
// cleanup failure is injected after LocalRunner has finished successfully.
func TestCodexAppServerRealProcessErrorPrecedence(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cleanupError := errors.New("synthetic-runner-cleanup-failure")
	for _, tc := range []struct {
		mode                                string
		wantProvider, wantScan, wantStarted bool
	}{
		{mode: "provider-failed", wantProvider: true, wantStarted: true},
		{mode: "provider-failed-scan", wantScan: true, wantStarted: true},
		{mode: "large", wantStarted: true},
		{mode: "handshake-failure"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			runner := &appServerProcessTestRunner{mode: tc.mode, afterRunError: cleanupError}
			request := Request{Runner: runner, WorkDir: t.TempDir(), Prompt: "synthetic", ProviderSessionID: "synthetic-thread"}
			begin := time.Now()
			_, err, started := (Codex{}).executeLiveInbox(context.Background(), request, nil, executable, 8*time.Second)
			if started != tc.wantStarted || err == nil {
				t.Fatalf("started=%v err=%v", started, err)
			}
			if tc.wantProvider {
				if !strings.Contains(err.Error(), "synthetic-provider-quota-exhausted") || strings.Contains(err.Error(), cleanupError.Error()) {
					t.Fatalf("provider failure was replaced by cleanup error: %v", err)
				}
			} else if tc.wantScan {
				if !strings.Contains(err.Error(), "line exceeds 16777216 byte scan limit") || strings.Contains(err.Error(), "exited") || strings.Contains(err.Error(), "timed out") {
					t.Fatalf("scan failure was lost: %v", err)
				}
			} else if !errors.Is(err, cleanupError) {
				t.Fatalf("runner failure was lost: %v", err)
			}
			if time.Since(begin) > 6*time.Second {
				t.Fatalf("error settlement took too long: %s", time.Since(begin))
			}
		})
	}
}
