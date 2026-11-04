package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	processHelperModeEnv = "AHA2_WORKSPACE_PROCESS_HELPER_MODE"
	processHelperExitEnv = "AHA2_WORKSPACE_PROCESS_HELPER_EXIT"

	processQuickStdout = "quick-stdout\n"
	processQuickStderr = "quick-stderr\n"

	processStagedFirstLine = "staged-first\n"

	processOrphanStdout = "orphan-stdout\n"
	processOrphanStderr = "orphan-stderr\n"

	// Long enough to outlive processPipeDrainTimeout several times over.
	processGrandchildSleep = 8 * time.Second

	processFloodLines = 700

	// The burst child must finish writing stdout while the parent's stdout
	// scanner is stalled in the line handler. Keep stdout below the smallest
	// platform pipe buffer (4 KiB on Windows) so those writes never block.
	processBurstStdoutLines = 24
	processBurstStderrLines = 300
)

// TestRunProcessHelperProcess is not an ordinary test: the stability tests
// below re-execute this test binary and select a child behavior through the
// environment, so the child drives the same stdout/stderr pipes os/exec gives
// any command.
func TestRunProcessHelperProcess(t *testing.T) {
	mode := os.Getenv(processHelperModeEnv)
	if mode == "" {
		t.Skip("runProcess helper process")
	}
	switch mode {
	case "quick":
		_, _ = io.WriteString(os.Stdout, processQuickStdout)
		_, _ = io.WriteString(os.Stderr, processQuickStderr)
	case "staged":
		// The first line wakes the parent's line handler, which pauses. Only
		// once the reader is known to be busy does the bulk output arrive, so
		// it cannot have been absorbed by the scanner's read-ahead buffer.
		_, _ = io.WriteString(os.Stdout, processStagedFirstLine)
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(os.Stdout, processBurstOutput("stdout", processBurstStdoutLines))
		_, _ = io.WriteString(os.Stderr, processBurstOutput("stderr", processBurstStderrLines))
	case "flood":
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			_, _ = io.WriteString(os.Stdout, processFloodOutput("stdout"))
		}()
		go func() {
			defer wait.Done()
			_, _ = io.WriteString(os.Stderr, processFloodOutput("stderr"))
		}()
		wait.Wait()
	case "orphan":
		_, _ = io.WriteString(os.Stdout, processOrphanStdout)
		_, _ = io.WriteString(os.Stderr, processOrphanStderr)
		grandchild, err := os.Executable()
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "locate grandchild executable: %v\n", err)
			os.Exit(92)
		}
		command := exec.Command(grandchild, "-test.run=^TestRunProcessHelperProcess$")
		command.Env = mergeEnvironment(os.Environ(), map[string]string{
			processHelperModeEnv: "grandchild-sleep",
			processHelperExitEnv: "0",
		})
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "start grandchild: %v\n", err)
			os.Exit(93)
		}
		// Deliberately do not wait: the grandchild inherits this process's
		// stdout/stderr and keeps those pipe handles open after we exit.
	case "grandchild-sleep":
		time.Sleep(processGrandchildSleep)
	case "slow":
		_, _ = io.WriteString(os.Stdout, "slow-start\n")
		time.Sleep(5 * time.Second)
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
		os.Exit(91)
	}
	exitCode, _ := strconv.Atoi(os.Getenv(processHelperExitEnv))
	os.Exit(exitCode)
}

func processFloodOutput(prefix string) string {
	var builder strings.Builder
	line := strings.Repeat("0123456789abcdef", 48)
	for index := 0; index < processFloodLines; index++ {
		fmt.Fprintf(&builder, "%s-%04d-%s\n", prefix, index, line)
	}
	return builder.String()
}

func processBurstOutput(prefix string, lines int) string {
	var builder strings.Builder
	line := strings.Repeat("0123456789", 10)
	for index := 0; index < lines; index++ {
		fmt.Fprintf(&builder, "%s-%04d-%s\n", prefix, index, line)
	}
	return builder.String()
}

// executeProcessHelper runs this test binary as a child process through the
// production LocalRunner path. configure may adjust timeout, output limit or
// working directory before the command starts.
func executeProcessHelper(t *testing.T, mode string, exitCode int, configure func(*Command), handler LineHandler) (Result, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	command := Command{
		Executable: executable,
		Args:       []string{"-test.run=^TestRunProcessHelperProcess$"},
		Env: map[string]string{
			processHelperModeEnv: mode,
			processHelperExitEnv: strconv.Itoa(exitCode),
		},
		Timeout: 60 * time.Second,
	}
	if configure != nil {
		configure(&command)
	}
	return (LocalRunner{}).Run(context.Background(), command, handler)
}

// Wait used to close the pipes as soon as the child exited, even while a scan
// goroutine was still occupied. Here the child emits its bulk only after the
// line handler is already paused, so that output is still in the pipe when the
// child exits; Wait then discarded it.
func TestRunProcessDrainsPipesWhileLineHandlerIsBusy(t *testing.T) {
	var once sync.Once
	handler := func(line string) {
		if line != strings.TrimSuffix(processStagedFirstLine, "\n") {
			return
		}
		once.Do(func() { time.Sleep(500 * time.Millisecond) })
	}
	result, err := executeProcessHelper(t, "staged", 0, nil, handler)
	if err != nil {
		t.Fatalf("run helper: %v", err)
	}
	wantStdout := processStagedFirstLine + processBurstOutput("stdout", processBurstStdoutLines)
	if result.Stdout != wantStdout {
		t.Fatalf("stdout truncated: got %d bytes, want %d", len(result.Stdout), len(wantStdout))
	}
	wantStderr := processBurstOutput("stderr", processBurstStderrLines)
	if result.Stderr != wantStderr {
		t.Fatalf("stderr truncated: got %d bytes, want %d", len(result.Stderr), len(wantStderr))
	}
}

// A grandchild that inherits stdout/stderr keeps those pipes open after the
// direct child exits. Run must still return in bounded time (WaitDelay), keep
// the child's own output intact and report success, with and without KillTree.
func TestRunProcessReturnsWhenDescendantsHoldOutputPipes(t *testing.T) {
	for _, killTree := range []bool{false, true} {
		t.Run(fmt.Sprintf("kill_tree=%t", killTree), func(t *testing.T) {
			start := time.Now()
			result, err := executeProcessHelper(t, "orphan", 0, func(command *Command) {
				command.KillTree = killTree
				command.Timeout = 30 * time.Second
			}, nil)
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("run helper: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("exit code = %d, want 0", result.ExitCode)
			}
			if elapsed > processPipeDrainTimeout+4*time.Second {
				t.Fatalf("Run took %s; descendant-held pipes were not bounded", elapsed)
			}
			if result.Stdout != processOrphanStdout {
				t.Fatalf("stdout = %q, want %q", result.Stdout, processOrphanStdout)
			}
			if result.Stderr != processOrphanStderr {
				t.Fatalf("stderr = %q, want %q", result.Stderr, processOrphanStderr)
			}
		})
	}
}

// A process that writes a few bytes and exits immediately used to lose its
// buffered output when Wait closed the pipes before the scan goroutines had
// drained them.
func TestRunProcessKeepsOutputWhenProcessExitsImmediately(t *testing.T) {
	iterations := 80
	if runtime.GOOS == "windows" {
		iterations = 40
	}
	for index := 0; index < iterations; index++ {
		result, err := executeProcessHelper(t, "quick", 0, nil, nil)
		if err != nil {
			t.Fatalf("iteration %d: run helper: %v (stdout=%q stderr=%q)", index, err, result.Stdout, result.Stderr)
		}
		if result.ExitCode != 0 {
			t.Fatalf("iteration %d: unexpected exit code %d", index, result.ExitCode)
		}
		if result.Stdout != processQuickStdout || result.Stderr != processQuickStderr {
			t.Fatalf("iteration %d: truncated output: stdout=%q stderr=%q", index, result.Stdout, result.Stderr)
		}
	}
}

// Both streams flood concurrently and the process exits while data is still in
// flight. The full output must survive, in order, and every stdout line must
// still reach the line handler.
func TestRunProcessKeepsLargeConcurrentOutput(t *testing.T) {
	wantStdout := processFloodOutput("stdout")
	wantStderr := processFloodOutput("stderr")
	for index := 0; index < 3; index++ {
		var lines []string
		result, err := executeProcessHelper(t, "flood", 0, nil, func(line string) {
			lines = append(lines, line)
		})
		if err != nil {
			t.Fatalf("iteration %d: run helper: %v", index, err)
		}
		if result.Stdout != wantStdout {
			t.Fatalf("iteration %d: stdout truncated: got %d bytes, want %d", index, len(result.Stdout), len(wantStdout))
		}
		if result.Stderr != wantStderr {
			t.Fatalf("iteration %d: stderr truncated: got %d bytes, want %d", index, len(result.Stderr), len(wantStderr))
		}
		if len(lines) != processFloodLines {
			t.Fatalf("iteration %d: line handler saw %d lines, want %d", index, len(lines), processFloodLines)
		}
		wantLast := strings.TrimSuffix(strings.SplitN(wantStdout, "\n", processFloodLines)[processFloodLines-1], "\n")
		if lines[len(lines)-1] != wantLast {
			t.Fatalf("iteration %d: last streamed line = %q, want %q", index, lines[len(lines)-1], wantLast)
		}
	}
}

// A non-zero exit is reported through ExitCode and does not suppress output.
func TestRunProcessKeepsOutputOnNonZeroExit(t *testing.T) {
	result, err := executeProcessHelper(t, "flood", 3, nil, nil)
	if err != nil {
		t.Fatalf("run helper: %v", err)
	}
	if result.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", result.ExitCode)
	}
	if result.Stdout != processFloodOutput("stdout") {
		t.Fatalf("stdout truncated: got %d bytes", len(result.Stdout))
	}
	if result.Stderr != processFloodOutput("stderr") {
		t.Fatalf("stderr truncated: got %d bytes", len(result.Stderr))
	}
}

// The timeout still cancels the command, returns context.DeadlineExceeded and
// preserves output written before the kill.
func TestRunProcessTimeoutStillReturnsPartialOutput(t *testing.T) {
	start := time.Now()
	result, err := executeProcessHelper(t, "slow", 0, func(command *Command) {
		command.Timeout = 300 * time.Millisecond
	}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if result.Stdout != "slow-start\n" {
		t.Fatalf("stdout = %q, want %q", result.Stdout, "slow-start\n")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout took %s, command was not cancelled promptly", elapsed)
	}
}
