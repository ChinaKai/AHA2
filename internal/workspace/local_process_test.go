package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	processHelperModeEnv    = "AHA2_WORKSPACE_PROCESS_HELPER_MODE"
	processHelperExitEnv    = "AHA2_WORKSPACE_PROCESS_HELPER_EXIT"
	processHelperMarkerEnv  = "AHA2_WORKSPACE_PROCESS_HELPER_MARKER"
	processHelperMarker2Env = "AHA2_WORKSPACE_PROCESS_HELPER_MARKER2"

	processQuickStdout = "quick-stdout\n"
	processQuickStderr = "quick-stderr\n"

	processStagedFirstLine = "staged-first\n"

	processOrphanStdout = "orphan-stdout\n"
	processOrphanStderr = "orphan-stderr\n"

	processStallFirstLine = "stall-first\n"

	// Long enough to outlive processPipeDrainTimeout several times over.
	processGrandchildSleep = 8 * time.Second

	// One line far beyond bufio.Scanner's 4 MiB token limit.
	processOversizedLineBytes = 5 * 1024 * 1024

	processFloodLines = 700

	// Multi-line stdout far beyond the OutputLimit used by the end-to-end cap
	// test, so the retained tail cannot fit every line.
	processOutputLimitLines = 240

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
	case "output-limit":
		_, _ = io.WriteString(os.Stdout, processOutputLimitOutput())
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
		startOutputInheritingGrandchild()
		// Deliberately do not wait: the grandchild inherits this process's
		// stdout/stderr and keeps those pipe handles open after we exit.
	case "orphan-alive":
		_, _ = io.WriteString(os.Stdout, processOrphanStdout)
		_, _ = io.WriteString(os.Stderr, processOrphanStderr)
		startOutputInheritingGrandchild()
		// Stay alive so a timeout, not the child exit, ends the command while
		// the grandchild still holds the inherited output handles.
		time.Sleep(30 * time.Second)
	case "grandchild-sleep":
		time.Sleep(processGrandchildSleep)
	case "oversized":
		_, _ = io.WriteString(os.Stdout, strings.Repeat("x", processOversizedLineBytes))
		_, _ = io.WriteString(os.Stderr, strings.Repeat("y", processOversizedLineBytes))
		time.Sleep(30 * time.Second)
	case "handler-stall":
		_, _ = io.WriteString(os.Stdout, processStallFirstLine)
		_, _ = io.WriteString(os.Stdout, processHandlerStallOutput("stdout", processHandlerStallLineCount()))
	case "backpressure":
		_, _ = io.WriteString(os.Stdout, processStallFirstLine)
		waitForHelperMarker(processHelperMarkerEnv)
		burst, _ := processBackpressureOutput()
		_, _ = io.WriteString(os.Stdout, burst)
	case "backpressure-descendant":
		// Backpressure first, then let the consumer drain everything before a
		// grandchild inherits the pipes and the child exits. The capture is
		// complete, so it must not be reported as truncated.
		_, _ = io.WriteString(os.Stdout, processStallFirstLine)
		waitForHelperMarker(processHelperMarkerEnv)
		burst, _ := processBackpressureOutput()
		_, _ = io.WriteString(os.Stdout, burst)
		waitForHelperMarker(processHelperMarker2Env)
		startOutputInheritingGrandchild()
	case "stdin-orphan":
		// Hand stdin to a grandchild that never reads it, give the parent's
		// feeder time to block on a write larger than the pipe buffer, then
		// exit while the grandchild still holds the pipe.
		startStdinInheritingGrandchild()
		time.Sleep(300 * time.Millisecond)
	case "slow":
		_, _ = io.WriteString(os.Stdout, "slow-start\n")
		time.Sleep(5 * time.Second)
	case "echo-input":
		// Echo stdin line by line until EOF, so a streamed write is visible
		// on stdout while the process is still running.
		buffer := make([]byte, 4096)
		pending := ""
		for {
			read, err := os.Stdin.Read(buffer)
			pending += string(buffer[:read])
			for {
				line, rest, found := strings.Cut(pending, "\n")
				if !found {
					break
				}
				_, _ = io.WriteString(os.Stdout, "echo:"+line+"\n")
				pending = rest
			}
			if err != nil {
				break
			}
		}
		_, _ = io.WriteString(os.Stdout, "stdin-eof\n")
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
		os.Exit(91)
	}
	exitCode, _ := strconv.Atoi(os.Getenv(processHelperExitEnv))
	os.Exit(exitCode)
}

func waitForHelperMarker(env string) {
	marker := os.Getenv(env)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			_, _ = fmt.Fprintln(os.Stderr, "helper marker timeout")
			os.Exit(94)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func processFloodOutput(prefix string) string {
	var builder strings.Builder
	line := strings.Repeat("0123456789abcdef", 48)
	for index := 0; index < processFloodLines; index++ {
		fmt.Fprintf(&builder, "%s-%04d-%s\n", prefix, index, line)
	}
	return builder.String()
}

func processOutputLimitOutput() string {
	var builder strings.Builder
	line := strings.Repeat("y", 40)
	for index := 0; index < processOutputLimitLines; index++ {
		fmt.Fprintf(&builder, "limit-%04d-%s\n", index, line)
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

// processHandlerStallLineCount stays below the 4 KiB Windows pipe buffer so
// the child can finish and exit while the parent's line handler is stalled.
func processHandlerStallLineCount() int {
	if runtime.GOOS == "windows" {
		return 100
	}
	return 2000
}

func processHandlerStallOutput(prefix string, lines int) string {
	var builder strings.Builder
	line := strings.Repeat("z", 24)
	for index := 0; index < lines; index++ {
		fmt.Fprintf(&builder, "%s-%04d-%s\n", prefix, index, line)
	}
	return builder.String()
}

// processBackpressureOutput emits a burst slightly larger than the per-stream
// spool plus one io.Copy chunk, so a stalled handler makes the copy goroutine
// block on the spool while a few KiB are still unread in the process pipe.
func processBackpressureOutput() (string, int) {
	body := strings.Repeat("q", 88)
	lineLength := len("out-00000-") + len(body) + 1
	lines := (processOutputSpoolLimit + 34*1024) / lineLength
	var builder strings.Builder
	builder.Grow(lines * lineLength)
	for index := 0; index < lines; index++ {
		fmt.Fprintf(&builder, "out-%05d-%s\n", index, body)
	}
	return builder.String(), lines
}

// startOutputInheritingGrandchild launches a long-lived copy of this test
// binary that inherits the current stdout/stderr handles.
func startOutputInheritingGrandchild() {
	executable, err := os.Executable()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "locate grandchild executable: %v\n", err)
		os.Exit(92)
	}
	command := exec.Command(executable, "-test.run=^TestRunProcessHelperProcess$")
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
}

// startStdinInheritingGrandchild launches a long-lived copy of this test
// binary that inherits only the current stdin handle, so the output pipes
// still reach EOF when this process exits. It prints the grandchild's pid so
// the test can reap it.
func startStdinInheritingGrandchild() {
	executable, err := os.Executable()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "locate grandchild executable: %v\n", err)
		os.Exit(92)
	}
	command := exec.Command(executable, "-test.run=^TestRunProcessHelperProcess$")
	command.Env = mergeEnvironment(os.Environ(), map[string]string{
		processHelperModeEnv: "grandchild-sleep",
		processHelperExitEnv: "0",
	})
	command.Stdin = os.Stdin
	if err := command.Start(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "start grandchild: %v\n", err)
		os.Exit(93)
	}
	_, _ = fmt.Fprintf(os.Stdout, "grandchild-pid:%d\n", command.Process.Pid)
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

// A timeout must still end the command when both the direct child and a
// descendant holding its output pipes are alive at cancellation.
func TestRunProcessTimeoutWithLiveDescendants(t *testing.T) {
	for _, killTree := range []bool{false, true} {
		t.Run(fmt.Sprintf("kill_tree=%t", killTree), func(t *testing.T) {
			start := time.Now()
			result, err := executeProcessHelper(t, "orphan-alive", 0, func(command *Command) {
				command.KillTree = killTree
				command.Timeout = time.Second
			}, nil)
			elapsed := time.Since(start)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want context.DeadlineExceeded", err)
			}
			if elapsed > 12*time.Second {
				t.Fatalf("Run took %s after a 1s timeout", elapsed)
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

// A single line beyond bufio.Scanner's token limit stops the scan loop. The
// runner must keep draining so the child and the os/exec copy goroutine can
// finish, otherwise Wait deadlocks and even the command timeout cannot fire.
func TestRunProcessOversizedLineReturnsUnderTimeout(t *testing.T) {
	start := time.Now()
	_, err := executeProcessHelper(t, "oversized", 0, func(command *Command) {
		command.Timeout = 2 * time.Second
	}, nil)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 15*time.Second {
		t.Fatalf("Run took %s for an oversized line with a 2s timeout", elapsed)
	}
}

// A line handler that stays busy longer than processPipeDrainTimeout must not
// cause the tail to be dropped: the copy goroutines feed the spool without
// waiting for the handler, so WaitDelay never fires here.
func TestRunProcessSlowHandlerKeepsBufferedOutput(t *testing.T) {
	lines := processHandlerStallLineCount()
	want := processStallFirstLine + processHandlerStallOutput("stdout", lines)
	const handlerStall = 4 * time.Second
	handled := 0
	start := time.Now()
	result, err := executeProcessHelper(t, "handler-stall", 0, func(command *Command) {
		command.Timeout = 60 * time.Second
	}, func(line string) {
		if handled == 0 {
			time.Sleep(handlerStall)
		}
		handled++
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("run helper: %v", err)
	}
	if result.Stdout != want {
		t.Fatalf("stdout truncated: got %d bytes, want %d", len(result.Stdout), len(want))
	}
	if handled != lines+1 {
		t.Fatalf("line handler saw %d lines, want %d", handled, lines+1)
	}
	if elapsed < handlerStall {
		t.Fatalf("Run returned in %s, before the stalled handler finished", elapsed)
	}
}

// Once the spool is full, the drainer waits for the consumer and the pipe is
// closed with unread bytes still in it. That must be reported as truncation for
// every exit code, not just successful ones.
func TestRunProcessBackpressureDoesNotHideTruncation(t *testing.T) {
	for _, exitCode := range []int{0, 3} {
		t.Run(fmt.Sprintf("exit_%d", exitCode), func(t *testing.T) {
			burst, burstLines := processBackpressureOutput()
			want := processStallFirstLine + burst
			marker := filepath.Join(t.TempDir(), "handler-started")
			const handlerStall = 5 * time.Second
			handled := 0
			start := time.Now()
			result, err := executeProcessHelper(t, "backpressure", exitCode, func(command *Command) {
				command.Timeout = 60 * time.Second
				command.Env[processHelperMarkerEnv] = marker
			}, func(line string) {
				if handled == 0 {
					if writeErr := os.WriteFile(marker, []byte("1"), 0o600); writeErr != nil {
						t.Errorf("write handler marker: %v", writeErr)
					}
					time.Sleep(handlerStall)
				}
				handled++
			})
			elapsed := time.Since(start)
			if err == nil {
				t.Fatalf("Run reported success with a full spool: exit=%d stdout %d/%d bytes, %d/%d lines",
					result.ExitCode, len(result.Stdout), len(want), handled, burstLines+1)
			}
			if !errors.Is(err, errProcessOutputTruncated) {
				t.Fatalf("error = %v, want errProcessOutputTruncated", err)
			}
			if result.ExitCode != exitCode {
				t.Fatalf("exit code = %d, want %d", result.ExitCode, exitCode)
			}
			if len(result.Stdout) >= len(want) || handled >= burstLines+1 {
				t.Fatalf("backpressure probe kept all output, so it did not exercise truncation")
			}
			if elapsed < handlerStall {
				t.Fatalf("Run returned in %s, before the stalled handler finished", elapsed)
			}
		})
	}
}

// Backpressure early in the run is not truncation if the consumer drained
// everything before a descendant kept the pipes open. The capture is complete,
// so a later idle-pipe close must not be reported as an error.
func TestRunProcessDrainedBackpressureWithHeldPipesIsNotTruncated(t *testing.T) {
	burst, burstLines := processBackpressureOutput()
	want := processStallFirstLine + burst
	marker := filepath.Join(t.TempDir(), "handler-started")
	marker2 := filepath.Join(t.TempDir(), "handler-drained")
	const handlerStall = 5 * time.Second
	handled := 0
	start := time.Now()
	result, err := executeProcessHelper(t, "backpressure-descendant", 0, func(command *Command) {
		command.Timeout = 60 * time.Second
		command.Env[processHelperMarkerEnv] = marker
		command.Env[processHelperMarker2Env] = marker2
	}, func(line string) {
		if handled == 0 {
			if writeErr := os.WriteFile(marker, []byte("1"), 0o600); writeErr != nil {
				t.Errorf("write handler marker: %v", writeErr)
			}
			time.Sleep(handlerStall)
		}
		handled++
		if handled == burstLines+1 {
			if writeErr := os.WriteFile(marker2, []byte("1"), 0o600); writeErr != nil {
				t.Errorf("write drained marker: %v", writeErr)
			}
		}
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("run helper: %v (stdout %d/%d bytes, %d/%d lines)", err, len(result.Stdout), len(want), handled, burstLines+1)
	}
	if result.Stdout != want {
		t.Fatalf("stdout truncated: got %d bytes, want %d", len(result.Stdout), len(want))
	}
	if handled != burstLines+1 {
		t.Fatalf("line handler saw %d lines, want %d", handled, burstLines+1)
	}
	if elapsed < handlerStall {
		t.Fatalf("Run returned in %s, before the stalled handler finished", elapsed)
	}
}

// Every Run must release the read end of its stdout/stderr pipes, including
// when the drainer reaches EOF normally. Disable the GC so a missing close
// cannot be hidden by finalizers.
func TestRunProcessReleasesPipeReadEnds(t *testing.T) {
	if _, ok := countOpenProcessHandles(); !ok {
		t.Skip("process handle counting is not available on this platform")
	}
	// A fresh process lazily allocates a handful of runtime and loader
	// handles during its first commands. Warm those up so the baseline is
	// stable; on Windows a single warm-up still left a one-time delta of 6-8
	// handles that was not a per-run leak.
	const warmups = 10
	for index := 0; index < warmups; index++ {
		if _, err := executeProcessHelper(t, "quick", 0, nil, nil); err != nil {
			t.Fatalf("warm-up %d: run helper: %v", index, err)
		}
	}
	previous := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previous)
	before, ok := countOpenProcessHandles()
	if !ok {
		t.Skip("process handle counting is not available on this platform")
	}
	const iterations = 100
	for index := 0; index < iterations; index++ {
		result, err := executeProcessHelper(t, "quick", 0, nil, nil)
		if err != nil {
			t.Fatalf("iteration %d: run helper: %v", index, err)
		}
		if result.Stdout != processQuickStdout || result.Stderr != processQuickStderr {
			t.Fatalf("iteration %d: truncated output: stdout=%q stderr=%q", index, result.Stdout, result.Stderr)
		}
	}
	after, ok := countOpenProcessHandles()
	if !ok {
		t.Skip("process handle counting is not available on this platform")
	}
	if delta := after - before; delta > 5 {
		t.Fatalf("open handles grew by %d after %d runs (%d -> %d)", delta, iterations, before, after)
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

// Command.Input keeps stdin open after the initial Stdin: a value sent after
// the child produced output still reaches it, and closing the channel ends
// stdin so the child can exit on its own.
func TestRunProcessStreamsInputAfterStart(t *testing.T) {
	input := make(chan string)
	lines := make(chan string, 8)
	done := make(chan struct{})
	var result Result
	var runErr error
	go func() {
		defer close(done)
		result, runErr = executeProcessHelper(t, "echo-input", 0, func(command *Command) {
			command.Stdin = "first\n"
			command.Input = input
		}, func(line string) { lines <- line })
	}()
	waitLine := func(want string) {
		t.Helper()
		select {
		case line := <-lines:
			if line != want {
				t.Fatalf("line = %q, want %q", line, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
	waitLine("echo:first")
	input <- "second\n"
	waitLine("echo:second")
	close(input)
	waitLine("stdin-eof")
	<-done
	if runErr != nil || result.ExitCode != 0 {
		t.Fatalf("result=%#v err=%v", result, runErr)
	}
}

// Without Input the runner still sends Stdin as one payload and closes it.
func TestRunProcessWithoutInputClosesStdinAfterPayload(t *testing.T) {
	result, err := executeProcessHelper(t, "echo-input", 0, func(command *Command) {
		command.Stdin = "only\n"
	}, nil)
	if err != nil || result.Stdout != "echo:only\nstdin-eof\n" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

// A child that exits while Input is still open must not leave Run (or the
// feeder goroutine) blocked on the never-closed channel.
func TestRunProcessReturnsWhenChildExitsWithInputOpen(t *testing.T) {
	input := make(chan string)
	done := make(chan error, 1)
	go func() {
		_, err := executeProcessHelper(t, "quick", 0, func(command *Command) { command.Input = input }, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after the child exited with Input open")
	}
}

// A feeder that does not finish after the child exited must not keep Run
// blocked: Run waits at most processInputFeederTimeout for it and still
// returns the child's result.
func TestRunProcessBoundsWaitForStuckInputFeeder(t *testing.T) {
	release := make(chan struct{})
	feederStarted := make(chan struct{})
	original := processInputFeeder
	processInputFeeder = func(stdin io.WriteCloser, _ string, _ <-chan string, _ <-chan struct{}, done chan<- struct{}) {
		defer close(done)
		close(feederStarted)
		<-release
	}
	t.Cleanup(func() {
		processInputFeeder = original
		close(release)
	})
	input := make(chan string)
	finished := make(chan struct{})
	var result Result
	var runErr error
	started := time.Now()
	go func() {
		defer close(finished)
		result, runErr = executeProcessHelper(t, "quick", 0, func(command *Command) { command.Input = input }, nil)
	}()
	select {
	case <-finished:
	case <-time.After(processInputFeederTimeout + 10*time.Second):
		t.Fatal("Run stayed blocked on a stdin feeder that never finished")
	}
	select {
	case <-feederStarted:
	default:
		t.Fatal("stub feeder was not used")
	}
	if runErr != nil || result.Stdout != processQuickStdout || result.ExitCode != 0 {
		t.Fatalf("result=%#v err=%v", result, runErr)
	}
	if elapsed := time.Since(started); elapsed < processInputFeederTimeout {
		t.Fatalf("Run returned after %s, before the feeder bound of %s", elapsed, processInputFeederTimeout)
	}
}

// The scenario behind the feeder bound: the child exits while a grandchild
// that inherited stdin keeps the pipe alive without reading, and the feeder is
// in the middle of a write larger than the pipe buffer. Closing the parent's
// write end when the child exits releases that pending write, so Run must
// return right away rather than fall back to the feeder bound.
func TestRunProcessReturnsWhileStdinInheritingGrandchildBlocksWrite(t *testing.T) {
	input := make(chan string, 1)
	input <- strings.Repeat("x", 1<<20) + "\n"
	pids := make(chan int, 1)
	// The grandchild runs this test binary; reap it so it neither outlives
	// the test nor keeps the binary locked.
	t.Cleanup(func() {
		select {
		case pid := <-pids:
			if process, err := os.FindProcess(pid); err == nil {
				_ = process.Kill()
				_, _ = process.Wait()
			}
		default:
		}
	})
	onLine := func(line string) {
		if value, ok := strings.CutPrefix(line, "grandchild-pid:"); ok {
			if pid, err := strconv.Atoi(value); err == nil {
				pids <- pid
			}
		}
	}
	// Observe the feeder as well as the wall clock. The proxy done channel
	// guarantees feederReturned is closed before awaitInputFeeder can observe
	// the real done, so the check after Run returns cannot race with the
	// feeder's final bookkeeping.
	original := processInputFeeder
	feederReturned := make(chan struct{})
	processInputFeeder = func(stdin io.WriteCloser, initial string, input <-chan string, exited <-chan struct{}, done chan<- struct{}) {
		proxy := make(chan struct{})
		original(stdin, initial, input, exited, proxy)
		close(feederReturned)
		close(done)
	}
	t.Cleanup(func() { processInputFeeder = original })
	// The direct child exits after ~300ms. A run that closes the write end at
	// that point returns immediately; one that does not must wait out the whole
	// processInputFeederTimeout (3s) before awaitInputFeeder gives up. Half the
	// timeout is still far above the normal path (observed ~0.4s including
	// process startup) and far below the timeout-fallback path (~3.3s), so it
	// distinguishes the two without relying on the grandchild's 8s lifetime.
	returnBound := processInputFeederTimeout / 2
	started := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := executeProcessHelper(t, "stdin-orphan", 0, func(command *Command) { command.Input = input }, onLine)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(returnBound):
		t.Fatalf("Run did not return within %s; it must not wait for the %s feeder timeout", returnBound, processInputFeederTimeout)
	}
	if elapsed := time.Since(started); elapsed >= returnBound {
		t.Fatalf("Run returned after %s (bound %s): the stdin write end was not released when the child exited", elapsed, returnBound)
	}
	select {
	case <-feederReturned:
	default:
		t.Fatal("Run returned while the stdin feeder was still blocked on its pending write")
	}
}

func TestBuiltInRunnersStreamInput(t *testing.T) {
	for _, runner := range []Runner{LocalRunner{}, WSLRunner{Distro: "x"}, &SSHRunner{Host: "h", User: "u"}} {
		if !StreamsInput(runner) {
			t.Fatalf("%T should stream input", runner)
		}
	}
}

func TestRunnerWithoutStreamingRejectsInput(t *testing.T) {
	if StreamsInput(unsupportedRunner{}) {
		t.Fatal("unsupportedRunner must not claim to stream input")
	}
	if _, err := (unsupportedRunner{}).Run(context.Background(), Command{Input: make(chan string)}, nil); !errors.Is(err, errInputNotStreamed) {
		t.Fatalf("unsupportedRunner accepted Input: %v", err)
	}
}

type unsupportedRunner struct{}

func (unsupportedRunner) Run(context.Context, Command, LineHandler) (Result, error) {
	return Result{}, errInputNotStreamed
}
