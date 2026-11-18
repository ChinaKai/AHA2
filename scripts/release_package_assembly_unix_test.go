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

// os.walk skips a directory it cannot scan unless onerror is supplied, so an
// unreadable subdirectory used to yield a successful build whose archive was
// silently missing every file below that directory.
func TestWindowsPackageAssemblyRejectsUnreadableDirectory(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not on PATH")
	}
	if os.Geteuid() == 0 {
		// Root bypasses directory permission checks, so chmod cannot produce
		// a scan failure here. The handler contract is covered directly by
		// TestWindowsPackageAssemblyWalkError.
		t.Skip("running as root: chmod cannot make a directory unreadable")
	}

	packageDir := writeFixturePackage(t)
	installerDir := writeFixtureInstallers(t)
	locked := filepath.Join(packageDir, "browser-host", "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "hidden.mjs"), []byte("hidden\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
	out, err := runPackageAssembly(t, packageDir, installerDir, zipPath)
	if err == nil {
		t.Fatalf("assembler silently skipped an unreadable directory:\n%s", out)
	}
	if !strings.Contains(out, "package input directory cannot be listed: "+locked) {
		t.Errorf("assembler error does not name the unreadable directory; want %q in:\n%s", locked, out)
	}
}

// Direct test for the os.walk error handler. It stays meaningful when the
// end-to-end case is skipped as root, and it pins the full contract: the
// error must carry both the path and the underlying OS reason.
func TestWindowsPackageAssemblyWalkError(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH")
	}
	const snippet = `
import errno
import importlib.util
import sys

spec = importlib.util.spec_from_file_location("assemble_windows_package", sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

try:
    module.walk_error(PermissionError(errno.EACCES, "Permission denied", sys.argv[2]))
except ValueError as error:
    print(error)
else:
    raise SystemExit("walk_error did not raise ValueError")
`
	locked := filepath.Join(t.TempDir(), "dist", "locked")
	// -B keeps the import from leaving a __pycache__ directory in scripts/.
	cmd := exec.Command(python, "-B", "-c", snippet, "scripts/assemble-windows-package.py", locked)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("walk error handler did not reject a scan failure: %v\n%s", err, out)
	}
	for _, want := range []string{"package input directory cannot be listed: " + locked, "Permission denied"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("walk error does not contain %q:\n%s", want, out)
		}
	}
}

// The end-to-end unreadable-directory case cannot run as root, and the direct
// handler test above calls walk_error itself, so neither one notices when
// add_tree stops passing onerror=walk_error to os.walk. This test replaces the
// module's os.walk with a fake that records whether an onerror callback
// arrived. When the wiring is present the fake invokes onerror with a
// PermissionError, and the assembler must exit non-zero naming the path and
// OS reason; when it is missing the fake walks silently, the assembler exits
// 0, and the test fails. No chmod or uid is involved, so it runs as root too.
func TestWindowsPackageAssemblyWiresWalkError(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH")
	}
	packageDir := writeFixturePackage(t)
	installerDir := writeFixtureInstallers(t)
	const snippet = `
import errno
import importlib.util
import os
import sys

spec = importlib.util.spec_from_file_location("assemble_windows_package", sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

package_dir, installer_dir, output = sys.argv[2], sys.argv[3], sys.argv[4]
walk_roots = []

def fake_walk(root, followlinks=True, onerror=None):
    walk_roots.append(os.fspath(root))
    if onerror is None:
        # Mirror os.walk's silent skip when the assembler forgets to wire the
        # handler: yield nothing and let assembly finish successfully.
        return iter(())
    onerror(PermissionError(errno.EACCES, "Permission denied", os.fspath(root)))
    return iter(())

module.os.walk = fake_walk
code = module.main([
    "--package-dir", package_dir,
    "--installer-dir", installer_dir,
    "--output", output,
])
if not walk_roots:
    raise SystemExit("assembler never called os.walk")
print(f"os.walk roots: {walk_roots}")
raise SystemExit(code)
`
	zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
	// -B keeps the import from leaving a __pycache__ directory in scripts/.
	cmd := exec.Command(python, "-B", "-c", snippet, "scripts/assemble-windows-package.py", packageDir, installerDir, zipPath)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("assembler exited 0 after os.walk reported a scan failure; onerror is not wired:\n%s", out)
	}
	for _, want := range []string{"package input directory cannot be listed: " + packageDir, "Permission denied"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("assembler error does not contain %q:\n%s", want, out)
		}
	}
}
