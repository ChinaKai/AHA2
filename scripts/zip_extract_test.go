package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestExtractZipMemberFindsBsdtarBehindGnuTar reproduces the Release packaging
// failure mode found in review: Git for Windows has no unzip and may put GNU
// tar (which cannot read zip) ahead of bsdtar on PATH. The extractor must probe
// past the first tar instead of failing with "unzip is required".
func TestExtractZipMemberFindsBsdtarBehindGnuTar(t *testing.T) {
	bash := requireZipExtractorTools(t)
	root := repoRoot(t)
	dir := t.TempDir()

	base := makeZipToolBase(t, dir)
	gnu := filepath.Join(dir, "gnu")
	if err := os.MkdirAll(gnu, 0o755); err != nil {
		t.Fatal(err)
	}
	// GNU tar cannot read zip: both the list and extract probes fail.
	writeExecutableScript(t, filepath.Join(gnu, "tar"), "#!/bin/sh\nexit 1\n")

	bsd := filepath.Join(dir, "bsd")
	if err := os.MkdirAll(bsd, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutableScript(t, filepath.Join(bsd, "bsdtar"), `#!/bin/sh
if [ "$1" = "-tf" ]; then exit 0; fi
if [ "$1" = "-xOf" ]; then printf '%s' 'member-payload'; exit 0; fi
exit 1
`)

	archive := filepath.Join(dir, "node.zip")
	if err := os.WriteFile(archive, []byte("PK\x03\x04 fixture"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bash, filepath.Join(root, "scripts", "extract-zip-member.sh"), archive, "node-v22.23.5-win-x64/node.exe")
	cmd.Env = zipExtractorEnv(dir, gnu, bsd, base)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("extractor failed although bsdtar was on PATH behind GNU tar: %v\nstderr: %s", err, stderr.String())
	}
	if got := stdout.String(); got != "member-payload" {
		t.Fatalf("extracted member = %q, want %q", got, "member-payload")
	}
}

// TestExtractZipMemberFailsClearlyWithoutZipTool pins the other half: when no
// unzip and no zip-capable tar/bsdtar exists, extraction exits nonzero with a
// message naming the tools to install, rather than a misleading "unzip is
// required" that hides an available bsdtar.
func TestExtractZipMemberFailsClearlyWithoutZipTool(t *testing.T) {
	bash := requireZipExtractorTools(t)
	root := repoRoot(t)
	dir := t.TempDir()

	base := makeZipToolBase(t, dir)
	gnu := filepath.Join(dir, "gnu")
	if err := os.MkdirAll(gnu, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutableScript(t, filepath.Join(gnu, "tar"), "#!/bin/sh\nexit 1\n")

	archive := filepath.Join(dir, "node.zip")
	if err := os.WriteFile(archive, []byte("PK\x03\x04 fixture"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bash, filepath.Join(root, "scripts", "extract-zip-member.sh"), archive, "node-v22.23.5-win-x64/node.exe")
	cmd.Env = zipExtractorEnv(dir, gnu, base)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("extractor succeeded without any ZIP-capable tool\nstdout: %s", stdout.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("failed extraction wrote %d bytes to stdout", stdout.Len())
	}
	if !strings.Contains(stderr.String(), "No ZIP extractor") || !strings.Contains(stderr.String(), "install unzip or bsdtar") {
		t.Fatalf("failure message does not name both alternatives:\n%s", stderr.String())
	}
}

func requireZipExtractorTools(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake GNU tar/bsdtar harness runs on the Linux build host")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	return bash
}

// makeZipToolBase links the few non-builtin commands the extractor needs into
// a directory that the test can put on a tightly controlled PATH.
func makeZipToolBase(t *testing.T, dir string) string {
	t.Helper()
	base := filepath.Join(dir, "base")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cat", "mktemp", "rm"} {
		bin, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s is not available: %v", name, err)
		}
		if err := os.Symlink(bin, filepath.Join(base, name)); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

func zipExtractorEnv(tmpDir string, pathDirs ...string) []string {
	return []string{
		"PATH=" + strings.Join(pathDirs, string(os.PathListSeparator)),
		"TMPDIR=" + tmpDir,
	}
}

func writeExecutableScript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
