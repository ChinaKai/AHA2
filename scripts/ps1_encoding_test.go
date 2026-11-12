package scripts

import (
	"bytes"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
}

// windowsPowerShellScripts returns the checked-in scripts that Windows
// PowerShell 5.1 may read from disk. internal/desktop/*.ps1 is excluded: those
// are embedded and passed as -EncodedCommand strings, so the file encoding
// never reaches the parser.
func windowsPowerShellScripts(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	for _, pattern := range []string{
		filepath.Join(root, "scripts", "*.ps1"),
		filepath.Join(root, "installer", "windows", "*.ps1"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) == 0 {
		t.Fatal("no Windows PowerShell scripts found")
	}
	return paths
}

// TestWindowsPowerShellScriptsHaveUTF8BOM guards the 0.5.471 failure: Windows
// PowerShell 5.1 decodes a BOM-less script with the ANSI code page, so the
// Chinese diagnostics introduced by task-308 turned into mojibake and the
// scheduled worker exited with a parser error before writing a result.
func TestWindowsPowerShellScriptsHaveUTF8BOM(t *testing.T) {
	root := repoRoot(t)
	for _, path := range windowsPowerShellScripts(t, root) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s must start with a UTF-8 BOM (EF BB BF); Windows PowerShell 5.1 reads a BOM-less file as ANSI", rel)
		}
	}
}

// TestTarPackagingPreservesUTF8BOM is the packaging side of the same guard:
// tar must round-trip the script bytes, BOM included.
func TestTarPackagingPreservesUTF8BOM(t *testing.T) {
	tarBin, err := exec.LookPath("tar")
	if err != nil {
		t.Skip("tar is not available")
	}
	root := repoRoot(t)
	rel, err := filepath.Rel(root, filepath.Join(root, "scripts", "deploy-windows-user.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(original, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("%s is missing its UTF-8 BOM", rel)
	}

	archive := filepath.Join(t.TempDir(), "scripts.tar")
	if out, err := exec.Command(tarBin, "-cf", archive, "-C", root, rel).CombinedOutput(); err != nil {
		t.Fatalf("tar create: %v\n%s", err, out)
	}
	extract := t.TempDir()
	if out, err := exec.Command(tarBin, "-xf", archive, "-C", extract).CombinedOutput(); err != nil {
		t.Fatalf("tar extract: %v\n%s", err, out)
	}
	roundTripped, err := os.ReadFile(filepath.Join(extract, rel))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(roundTripped, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("tar round-trip dropped the UTF-8 BOM")
	}
	if sha256.Sum256(original) != sha256.Sum256(roundTripped) {
		t.Fatal("tar round-trip changed the script bytes")
	}
}

// TestDeployScriptBackupAndHandoffContract keeps the Linux test suite able to
// catch a regression that removes the shm exclusion, the hash error handling,
// or the fail-loud handoff. The PowerShell self-test exercises the behaviour on
// Windows; these markers prove the production path still wires it.
func TestDeployScriptBackupAndHandoffContract(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "scripts", "deploy-windows-user.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`$databaseBackupFileNames = @("aha2.db", "aha2.db-wal")`,
		`Copy-AHA2DatabaseBackup -DataDirectory $data -BackupDirectory $backup`,
		`Get-FileHash -LiteralPath $Source -Algorithm SHA256 -ErrorAction Stop`,
		`$databaseBackupFileNames | Where-Object`,
		`Resolve-AHA2DetachedHandoff`,
		`Assert-AHA2DetachedWorkerAlive`,
		`Assert-AHA2WindowsPowerShellScriptEncoding`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("deploy-windows-user.ps1 no longer contains %q", want)
		}
	}
	if strings.Contains(text, `$databaseFileNames + @("secrets.json"`) {
		t.Error("backup loop still iterates the full database trio, including aha2.db-shm")
	}

	handoffStart := strings.Index(text, `if (-not $DetachedWorker -and (Test-AHA2Ancestor))`)
	if handoffStart < 0 {
		t.Fatal("deploy-windows-user.ps1 no longer has a detached handoff block")
	}
	handoffEnd := strings.Index(text[handoffStart:], `$stamp = [DateTime]::UtcNow.ToString`)
	if handoffEnd < 0 {
		t.Fatal("could not find the end of the detached handoff block")
	}
	handoff := text[handoffStart : handoffStart+handoffEnd]
	for _, want := range []string{
		`$decision -eq "failed"`,
		`Assert-AHA2DetachedWorkerAlive -TaskInfo $taskInfo -LogPath $workerLogPath`,
		`Assert-AHA2WindowsPowerShellScriptEncoding -Path $PSCommandPath`,
	} {
		if !strings.Contains(handoff, want) {
			t.Errorf("detached handoff no longer fails loudly: missing %q", want)
		}
	}
}
