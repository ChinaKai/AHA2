package workspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPrependPathDirectoriesDeduplicatesAndPreservesOrder(t *testing.T) {
	got := prependPathDirectories("/usr/bin:/bin", []string{"/home/u/.local/bin", "/usr/bin", " /home/u/.local/bin ", ""})
	want := strings.Join([]string{"/home/u/.local/bin", "/usr/bin", "/bin"}, string(os.PathListSeparator))
	if got != want {
		t.Fatalf("unexpected PATH: got %q want %q", got, want)
	}
}

func TestEnvironmentValueIsCaseInsensitive(t *testing.T) {
	if got := environmentValue([]string{"Path=/custom/bin"}, "PATH"); got != "/custom/bin" {
		t.Fatalf("unexpected value: %q", got)
	}
	if got := environmentValue([]string{"HOME=/home/u"}, "PATH"); got != "" {
		t.Fatalf("expected empty value, got %q", got)
	}
}

func TestLocalPathDirectoriesIncludesUserBinAndNVM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("user bin directories differ on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.0.0", "bin")
	if err := os.MkdirAll(nvmBin, 0o755); err != nil {
		t.Fatalf("create nvm dir: %v", err)
	}

	dirs := localPathDirectories()
	if len(dirs) < 3 {
		t.Fatalf("expected user bin, home bin and nvm dirs, got %#v", dirs)
	}
	if dirs[0] != filepath.Join(home, ".local", "bin") {
		t.Fatalf("first dir should be ~/.local/bin, got %q", dirs[0])
	}
	if dirs[1] != filepath.Join(home, "bin") {
		t.Fatalf("second dir should be ~/bin, got %q", dirs[1])
	}
	if dirs[len(dirs)-1] != nvmBin {
		t.Fatalf("expected nvm bin %q, got %#v", nvmBin, dirs)
	}
}

func TestLocalEnvironmentKeepsExistingPathAndHonorsOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH separator and home lookup differ on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	env := localEnvironment([]string{"PATH=/usr/bin:/bin"}, nil)
	path := environmentValue(env, "PATH")
	if !strings.HasPrefix(path, filepath.Join(home, ".local", "bin")) {
		t.Fatalf("expected ~/.local/bin prepended, got %q", path)
	}
	if !strings.HasSuffix(path, "/usr/bin:/bin") {
		t.Fatalf("expected original PATH preserved, got %q", path)
	}

	explicit := localEnvironment([]string{"PATH=/usr/bin:/bin"}, map[string]string{"PATH": "/opt/toolchain/bin"})
	if got := environmentValue(explicit, "PATH"); got != "/opt/toolchain/bin" {
		t.Fatalf("explicit command PATH should win, got %q", got)
	}
}

// A backend CLI installed under ~/.local/bin must be runnable through the local
// runner even when the parent process PATH does not contain that directory.
func TestLocalRunnerFindsUserLocalBinExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is POSIX only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	script := filepath.Join(binDir, "aha-fake-dsh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho fake-dsh 0.1.0\n"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	result, err := LocalRunner{}.Run(context.Background(), Command{
		Executable: "aha-fake-dsh", Args: []string{"--version"}, Timeout: 15 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("run fixture: %v", err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, "fake-dsh 0.1.0") {
		t.Fatalf("unexpected result: %#v", result)
	}
}
