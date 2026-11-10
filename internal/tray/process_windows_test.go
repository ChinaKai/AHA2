//go:build windows

package tray

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTrayHelperProcess is not a real test: the launcher tests re-execute the
// test binary with this name so a genuine child process writes to both streams
// and exits with a known nonzero status.
func TestTrayHelperProcess(t *testing.T) {
	switch os.Getenv("AHA2_TRAY_HELPER") {
	case "1":
		fmt.Fprintln(os.Stdout, "helper stdout line")
		fmt.Fprintln(os.Stderr, "helper stderr line")
		os.Exit(7)
	case "hold":
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	case "spawn":
		env := make([]string, 0, len(os.Environ())+1)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "AHA2_TRAY_HELPER=") {
				env = append(env, entry)
			}
		}
		env = append(env, "AHA2_TRAY_HELPER=hold")
		grandchild := exec.Command(os.Args[0], "-test.run=^TestTrayHelperProcess$")
		grandchild.Env = env
		grandchild.Stdout = os.Stdout
		grandchild.Stderr = os.Stderr
		if err := grandchild.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(9)
		}
		// Exit without waiting: the grandchild keeps the inherited stdout pipe
		// open after the direct child is gone.
		os.Exit(0)
	}
}

func TestOSLauncherCapturesStdoutStderrAndExitCode(t *testing.T) {
	t.Setenv("AHA2_TRAY_HELPER", "1")
	path := filepath.Join(t.TempDir(), "data", "logs", "aha2-server.log")
	launcher := OSLauncher{LogPath: path}
	command := Command{
		Path: os.Args[0],
		Args: []string{"-test.run=^TestTrayHelperProcess$"},
		Dir:  t.TempDir(),
	}
	child, err := launcher.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	waitErr := child.Wait()
	if waitErr == nil {
		t.Fatal("helper process should have exited nonzero")
	}

	content := readLogFile(t, path)
	for _, want := range []string{
		"AHA2 tray starting server:",
		"TestTrayHelperProcess",
		"helper stdout line",
		"helper stderr line",
		"AHA2 tray server exited: exit code 7 (0x00000007)",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("server log missing %q:\n%s", want, content)
		}
	}
}

func TestOSLauncherRecordsStartFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "logs", "aha2-server.log")
	launcher := OSLauncher{LogPath: path}
	missing := filepath.Join(t.TempDir(), "missing-aha2.exe")
	if _, err := launcher.Start(Command{Path: missing}); err == nil {
		t.Fatal("starting a missing executable should fail")
	}
	content := readLogFile(t, path)
	if !strings.Contains(content, "AHA2 tray server start failed:") {
		t.Fatalf("start failure was not persisted:\n%s", content)
	}
}

func TestOSLauncherWaitDelayBoundsInheritedPipeHang(t *testing.T) {
	t.Setenv("AHA2_TRAY_HELPER", "spawn")
	path := filepath.Join(t.TempDir(), "data", "logs", "aha2-server.log")
	launcher := OSLauncher{LogPath: path, waitDelay: 150 * time.Millisecond}
	started := time.Now()
	child, err := launcher.Start(Command{
		Path: os.Args[0],
		Args: []string{"-test.run=^TestTrayHelperProcess$"},
		Dir:  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitErr := child.Wait()
	if !errors.Is(waitErr, exec.ErrWaitDelay) {
		t.Fatalf("Wait error = %v, want exec.ErrWaitDelay once inherited pipes outlive the child", waitErr)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("Wait blocked for %s despite waitDelay", elapsed)
	}
	content := readLogFile(t, path)
	if !strings.Contains(content, "AHA2 tray server exited: exit code 0") {
		t.Fatalf("exit was not recorded for a successful child whose pipes stayed open:\n%s", content)
	}
}

func TestLogTrayFailurePersistsPreflightError(t *testing.T) {
	dataDir := t.TempDir()
	logTrayFailure(dataDir, errors.New("missing server executable"))
	content := readLogFile(t, ServerLogPath(dataDir))
	if !strings.Contains(content, "AHA2 tray failed to start server: missing server executable") {
		t.Fatalf("preflight failure was not persisted:\n%s", content)
	}
}

func TestOSLauncherWithoutLogPathKeepsWaitingForChild(t *testing.T) {
	t.Setenv("AHA2_TRAY_HELPER", "1")
	launcher := OSLauncher{}
	child, err := launcher.Start(Command{
		Path: os.Args[0],
		Args: []string{"-test.run=^TestTrayHelperProcess$"},
		Dir:  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("helper process should have exited nonzero")
	}
}
