//go:build linux || darwin || freebsd

package workspaceterminal

import (
	"bufio"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceTerminalNativePTYEchoAndResize(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("AHA2_TEST_UNSAFE_ENV", "test-sentinel")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	process, err := StartLocal(root, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Close() })
	if err := process.Resize(103, 37); err != nil {
		t.Fatal(err)
	}
	const command = "printf 'PTY''-MARKER:%s:%s\\n' \"$(pwd -P)\" \"${AHA2_TEST_UNSAFE_ENV:-clean}\"; stty size; exit\n"
	if _, err := process.Write([]byte(command)); err != nil {
		t.Fatal(err)
	}
	output := make(chan string, 1)
	go func() {
		var transcript strings.Builder
		scanner := bufio.NewScanner(process)
		for scanner.Scan() {
			transcript.WriteString(scanner.Text() + "\n")
		}
		output <- transcript.String()
	}()
	select {
	case text := <-output:
		for _, want := range []string{"PTY''-MARKER", "PTY-MARKER:" + root + ":clean", "37 103"} {
			if !strings.Contains(text, want) {
				t.Fatalf("PTY output missing %q: %q", want, text)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PTY did not output and exit")
	}
}
