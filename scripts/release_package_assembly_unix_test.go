//go:build !windows

package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A FIFO is a regular directory entry on Unix but not a regular file. The
// assembler must reject it instead of trying to read it, which would block or
// package bytes that the manifest never described.
func TestWindowsPackageAssemblyRejectsNonRegularInputs(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not on PATH")
	}

	assemblerRejects := func(t *testing.T, packageDir, installerDir, want string) {
		t.Helper()
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		out, err := runPackageAssembly(t, packageDir, installerDir, zipPath)
		if err == nil {
			t.Fatalf("assembler accepted a non-regular input:\n%s", out)
		}
		if !strings.Contains(out, want) {
			t.Errorf("assembler error does not name the rejected input; want %q in:\n%s", want, out)
		}
	}

	t.Run("rejects a FIFO inside dist", func(t *testing.T) {
		packageDir := writeFixturePackage(t)
		installerDir := writeFixtureInstallers(t)
		fifo := filepath.Join(packageDir, "browser-host", "named-pipe")
		if err := syscall.Mkfifo(fifo, 0o644); err != nil {
			t.Skipf("cannot create a FIFO on this platform: %v", err)
		}
		assemblerRejects(t, packageDir, installerDir, "package input is not a regular file: "+fifo)
	})

	t.Run("rejects a FIFO installer", func(t *testing.T) {
		packageDir := writeFixturePackage(t)
		installerDir := writeFixtureInstallers(t)
		setup := filepath.Join(installerDir, "AHA2-Setup-x64.exe")
		if err := os.Remove(setup); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(setup, 0o644); err != nil {
			t.Skipf("cannot create a FIFO on this platform: %v", err)
		}
		assemblerRejects(t, packageDir, installerDir, "installer is not a regular file: "+setup)
	})
}
