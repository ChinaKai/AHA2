package scripts

import (
	"bytes"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	scripts := windowsPowerShellScripts(t, root)
	original := make(map[string][]byte, len(scripts))
	var rels []string
	for _, path := range scripts {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
			t.Fatalf("%s is missing its UTF-8 BOM", rel)
		}
		original[rel] = data
		rels = append(rels, rel)
	}

	archive := filepath.Join(t.TempDir(), "scripts.tar")
	tarArgs := append([]string{"-cf", archive, "-C", root}, rels...)
	if out, err := exec.Command(tarBin, tarArgs...).CombinedOutput(); err != nil {
		t.Fatalf("tar create: %v\n%s", err, out)
	}
	extract := t.TempDir()
	if out, err := exec.Command(tarBin, "-xf", archive, "-C", extract).CombinedOutput(); err != nil {
		t.Fatalf("tar extract: %v\n%s", err, out)
	}
	for _, rel := range rels {
		roundTripped, err := os.ReadFile(filepath.Join(extract, rel))
		if err != nil {
			t.Fatalf("%s after tar extract: %v", rel, err)
		}
		if !bytes.HasPrefix(roundTripped, []byte{0xEF, 0xBB, 0xBF}) {
			t.Errorf("tar round-trip dropped the UTF-8 BOM from %s", rel)
		}
		if sha256.Sum256(original[rel]) != sha256.Sum256(roundTripped) {
			t.Errorf("tar round-trip changed the script bytes of %s", rel)
		}
	}
}
