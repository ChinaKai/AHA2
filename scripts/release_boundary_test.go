package scripts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// removedOperatorDeployScripts are the operator deployment entry points the
// repository retired in task-315, stored as case-insensitive name stems so a
// reference without the .ps1 suffix or with different capitalisation is still
// caught. Deployment now belongs to the installer on the target machine, so
// these must stay gone and must not come back through a doc, build script or
// test.
var removedOperatorDeployScripts = []string{
	"build-and-deploy-windows-user",
	"deploy-windows-user",
	"deploy-windows",
}

// ignoredReferenceDirs keeps the reference sweep out of generated output,
// dependency trees and historical run logs. tmp/ holds tracked logs from
// deployments that already happened; they are records, not entry points.
var ignoredReferenceDirs = map[string]bool{
	".git":          true,
	".aha2-context": true,
	"artifacts":     true,
	"dist":          true,
	"node_modules":  true,
	"output":        true,
	"tmp":           true,
}

func referencesRemovedScript(content string) string {
	lower := strings.ToLower(content)
	match := ""
	for _, name := range removedOperatorDeployScripts {
		if strings.Contains(lower, name) && len(name) > len(match) {
			match = name
		}
	}
	return match
}

// TestRemovedScriptReferenceDetector protects the sweep below from going
// vacuous: an empty name list or a broken matcher would otherwise make the
// repository-wide assertion pass while references remain. It pins the two
// blind spots found in review: case differences and a bare name without the
// .ps1 suffix.
func TestRemovedScriptReferenceDetector(t *testing.T) {
	for _, name := range removedOperatorDeployScripts {
		if got := referencesRemovedScript("powershell.exe -File scripts\\" + name + ".ps1"); got != name {
			t.Fatalf("detector missed %s (got %q)", name, got)
		}
	}
	for _, sample := range []struct {
		content string
		want    string
	}{
		{"powershell -File scripts\\Deploy-Windows.ps1", "deploy-windows"},
		{"powershell -File scripts\\DEPLOY-WINDOWS-USER.PS1", "deploy-windows-user"},
		{"bash scripts/deploy-windows-user", "deploy-windows-user"},
		{"Build-And-Deploy-Windows-User.ps1", "build-and-deploy-windows-user"},
	} {
		if got := referencesRemovedScript(sample.content); got != sample.want {
			t.Errorf("detector on %q = %q, want %q", sample.content, got, sample.want)
		}
	}
	if got := referencesRemovedScript("bash scripts/build-windows-package.sh --version 1.2.3"); got != "" {
		t.Fatalf("detector reported a false positive: %q", got)
	}
}

func TestOperatorDeployScriptsRemoved(t *testing.T) {
	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("scripts directory is empty; the existence check would be vacuous")
	}
	for _, entry := range entries {
		lower := strings.ToLower(entry.Name())
		for _, name := range removedOperatorDeployScripts {
			if strings.Contains(lower, name) {
				t.Errorf("scripts/%s still exists; the repository must only build packages", entry.Name())
			}
		}
	}
}

// TestNoResidualOperatorDeployReferences is the repository-wide side of the
// same boundary. Adding a deleted script name back to a doc, build script or
// test turns this red; a "deprecated" section naming them would also fail, so
// docs must describe the replacement instead of keeping the old usage.
func TestNoResidualOperatorDeployReferences(t *testing.T) {
	root := repoRoot(t)
	self := "scripts/release_boundary_test.go"
	scanned := make([]string, 0, 512)
	required := map[string]bool{
		"README.md":                        false,
		"docs/deployment.md":               false,
		"local_build.sh":                   false,
		"scripts/build-windows-package.sh": false,
	}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if ignoredReferenceDirs[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == self {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > 4<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return nil // binary
		}
		scanned = append(scanned, rel)
		if _, ok := required[rel]; ok {
			required[rel] = true
		}
		if name := referencesRemovedScript(string(data)); name != "" {
			t.Errorf("%s still references the removed operator script %s", rel, name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}
	// A sweep that silently skips everything would pass no matter what the
	// repository contains. Require a realistic number of text files and the
	// documents/build entry points this task touches.
	if len(scanned) < 100 {
		t.Fatalf("reference sweep only scanned %d files; check the ignore list", len(scanned))
	}
	var missing []string
	for rel, seen := range required {
		if !seen {
			missing = append(missing, rel)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("reference sweep did not scan required files: %s", strings.Join(missing, ", "))
	}
}

type releaseManifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type releaseManifest struct {
	SchemaVersion int    `json:"schema_version"`
	Product       string `json:"product"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	BuiltAt       string `json:"built_at"`
	Generator     struct {
		Script      string `json:"script"`
		Command     string `json:"command"`
		WebVersion  string `json:"web_version"`
		GoVersion   string `json:"go_version"`
		NodeVersion string `json:"node_version"`
	} `json:"generator"`
	Files []releaseManifestFile `json:"files"`
}

// TestWindowsPackageBuildProducesAllArtifacts builds the release package into a
// directory outside the repository and checks the manifest against the files on
// disk. It also snapshots the source tree before and after the build: the
// script may only write --output-dir, so web/dist and internal/webassets/dist
// must stay untouched even though the Web build regenerates them in its
// temporary copy. It needs the Go and Node toolchains, so it is opt-in:
//
//	AHA2_BUILD_E2E=1 go test ./scripts/ -run TestWindowsPackageBuild -v
func TestWindowsPackageBuildProducesAllArtifacts(t *testing.T) {
	requirePackageBuildTools(t)
	root := repoRoot(t)
	// A space in the output path proves the manifest generator command quotes it.
	output := filepath.Join(t.TempDir(), "package dir")
	const version = "1.2.3"
	const commit = "0123456789abcdef0123456789abcdef01234567"
	before := snapshotSourceTree(t, root)
	recorder, goCallLog, realGo := installGoCallRecorder(t, t.TempDir())
	cmd := exec.Command(buildScriptPath(root),
		"--version", version,
		"--commit", commit,
		"--output-dir", output,
		"--skip-web-test",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "VERSION=",
		"GO_BIN="+recorder,
		"AHA2_REAL_GO="+realGo,
		"AHA2_GO_CALL_LOG="+goCallLog,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build-windows-package.sh failed: %v\n%s", err, out)
	}
	assertSourceTreeUnchanged(t, before, snapshotSourceTree(t, root))
	// The banner alone is not evidence: it could print before the test runs.
	// The recorder proves the build actually invoked `go test` and `go vet` in
	// the nested plugin module.
	if !strings.Contains(string(out), "==> Feishu plugin tests passed") {
		t.Error("package build did not report the Feishu plugin tests as passed")
	}
	assertGoCall(t, goCallLog, "test ./...", "plugins/feishu")
	assertGoCall(t, goCallLog, "vet ./...", "plugins/feishu")

	required := []string{
		"aha2-windows-amd64.exe",
		"aha2-tray-windows-amd64.exe",
		filepath.Join("plugins", "feishu", "windows-amd64", "aha2-channel-feishu.exe"),
		filepath.Join("plugins", "feishu", "windows-amd64", "plugin.json"),
	}
	for _, rel := range required {
		data, err := os.ReadFile(filepath.Join(output, rel))
		if err != nil {
			t.Fatalf("package artifact %s: %v", rel, err)
		}
		if rel != filepath.Join("plugins", "feishu", "windows-amd64", "plugin.json") {
			if len(data) < 2 || data[0] != 'M' || data[1] != 'Z' {
				t.Errorf("%s is not a Windows PE executable", rel)
			}
		}
	}

	manifestPath := filepath.Join(output, "release-manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("release manifest: %v", err)
	}
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(raw, &topLevel); err != nil {
		t.Fatalf("parse release manifest: %v", err)
	}
	allowedKeys := map[string]bool{
		"schema_version": true, "product": true, "version": true, "commit": true,
		"built_at": true, "generator": true, "files": true,
	}
	for key := range topLevel {
		if !allowedKeys[key] {
			t.Errorf("release manifest carries an unexpected top-level field %q; it must describe artifacts only", key)
		}
	}
	var manifest releaseManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse release manifest: %v", err)
	}
	if manifest.SchemaVersion != 1 {
		t.Errorf("manifest schema_version = %d, want 1", manifest.SchemaVersion)
	}
	if manifest.Product != "AHA2" || manifest.Version != version || manifest.Commit != commit {
		t.Errorf("manifest identity = %q/%q/%q, want AHA2/%s/%s", manifest.Product, manifest.Version, manifest.Commit, version, commit)
	}
	if _, err := time.Parse(time.RFC3339, manifest.BuiltAt); err != nil {
		t.Errorf("manifest built_at %q is not RFC3339: %v", manifest.BuiltAt, err)
	}
	if manifest.Generator.Script != "scripts/build-windows-package.sh" {
		t.Errorf("manifest generator.script = %q", manifest.Generator.Script)
	}
	if manifest.Generator.Command == "" || manifest.Generator.WebVersion == "" || manifest.Generator.GoVersion == "" || manifest.Generator.NodeVersion == "" {
		t.Errorf("manifest generator is incomplete: %+v", manifest.Generator)
	}
	// Replaying from a source snapshot needs the commit and test options in the
	// recorded command, not just the version.
	for _, want := range []string{"--version " + version, "--commit " + commit, "--output-dir", "--skip-web-test"} {
		if !strings.Contains(manifest.Generator.Command, want) {
			t.Errorf("manifest generator.command = %q, missing %q", manifest.Generator.Command, want)
		}
	}

	got := make(map[string]releaseManifestFile, len(manifest.Files))
	for _, file := range manifest.Files {
		got[filepath.FromSlash(file.Path)] = file
	}
	if len(got) != len(required) {
		t.Errorf("manifest lists %d files, want %d", len(got), len(required))
	}
	if _, ok := got["release-manifest.json"]; ok {
		t.Error("manifest must not list itself")
	}
	for _, rel := range required {
		entry, ok := got[rel]
		if !ok {
			t.Errorf("manifest is missing %s", rel)
			continue
		}
		data, err := os.ReadFile(filepath.Join(output, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		sum := sha256.Sum256(data)
		if entry.Size != int64(len(data)) {
			t.Errorf("%s size = %d, want %d", rel, entry.Size, len(data))
		}
		if entry.SHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("%s sha256 = %s, want %s", rel, entry.SHA256, hex.EncodeToString(sum[:]))
		}
	}

	// The tray must stay a GUI-subsystem binary: the login task starts it and a
	// console window would flash at every logon.
	trayHeader, err := os.ReadFile(filepath.Join(output, "aha2-tray-windows-amd64.exe"))
	if err != nil {
		t.Fatal(err)
	}
	subsystemOffset, err := peSubsystemOffset(trayHeader)
	if err != nil {
		t.Fatalf("parse tray PE header: %v", err)
	}
	if subsystem := uint16(trayHeader[subsystemOffset]) | uint16(trayHeader[subsystemOffset+1])<<8; subsystem != 2 {
		t.Errorf("tray subsystem = %d, want 2 (Windows GUI)", subsystem)
	}
}

// TestWindowsPackageBuildWorksWithoutGitCheckout builds from a source snapshot
// that has no .git directory, passing only --commit and --version. The Web
// version must come from that commit rather than from `git rev-parse`.
func TestWindowsPackageBuildWorksWithoutGitCheckout(t *testing.T) {
	bash := requirePackageBuildTools(t)
	root := repoRoot(t)
	source := filepath.Join(t.TempDir(), "source")
	copySourceTree(t, root, source)
	if _, err := os.Stat(filepath.Join(source, ".git")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source snapshot still has a .git entry: %v", err)
	}

	output := filepath.Join(t.TempDir(), "package")
	const commit = "0123456789abcdef0123456789abcdef01234567"
	cmd := exec.Command(bash, filepath.Join(source, "scripts", "build-windows-package.sh"),
		"--version", "1.2.3",
		"--commit", commit,
		"--output-dir", output,
		"--skip-web-test",
	)
	cmd.Dir = source
	cmd.Env = append(os.Environ(), "VERSION=", "AHA2_BUILD_DATE=20260101")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build without a git checkout failed: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(filepath.Join(output, "release-manifest.json"))
	if err != nil {
		t.Fatalf("release manifest: %v", err)
	}
	var manifest releaseManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse release manifest: %v", err)
	}
	if manifest.Commit != commit {
		t.Errorf("manifest commit = %q, want %q", manifest.Commit, commit)
	}
	const wantWebVersion = "v1.2.3.20260101.0123456789ab"
	if manifest.Generator.WebVersion != wantWebVersion {
		t.Errorf("manifest web_version = %q, want %q (derived from --commit)", manifest.Generator.WebVersion, wantWebVersion)
	}
	if _, err := os.Stat(filepath.Join(output, "plugins", "feishu", "windows-amd64", "plugin.json")); err != nil {
		t.Fatalf("Feishu plugin manifest missing from the no-git build: %v", err)
	}
}

// TestWindowsPackageBuildAcceptsBareGoCommand covers the release workflow,
// which passes GO_BIN=go rather than an absolute path.
func TestWindowsPackageBuildAcceptsBareGoCommand(t *testing.T) {
	bash := requirePackageBuildTools(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	root := repoRoot(t)
	output := t.TempDir()
	cmd := exec.Command(bash, buildScriptPath(root),
		"--version", "1.2.3",
		"--commit", "0123456789abcdef0123456789abcdef01234567",
		"--output-dir", output,
		"--skip-web-test",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "VERSION=", "GO_BIN=go")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build with GO_BIN=go failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(output, "release-manifest.json")); err != nil {
		t.Fatalf("release manifest missing from the GO_BIN=go build: %v", err)
	}
}

func requirePackageBuildTools(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the clean-directory package build runs on the Linux build server")
	}
	if os.Getenv("AHA2_BUILD_E2E") != "1" {
		t.Skip("set AHA2_BUILD_E2E=1 to build the Windows package (needs go and node)")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	return bash
}

// installGoCallRecorder wraps the real go binary so the E2E can prove which
// commands the build actually executed, including in the nested plugin module.
func installGoCallRecorder(t *testing.T, dir string) (recorder, logPath, realGo string) {
	t.Helper()
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	logPath = filepath.Join(dir, "go-calls.log")
	recorder = filepath.Join(dir, "go-recorder")
	script := "#!/usr/bin/env bash\n" +
		"set -euo pipefail\n" +
		"printf '%s\\t%s\\n' \"$PWD\" \"$*\" >> \"$AHA2_GO_CALL_LOG\"\n" +
		"exec \"$AHA2_REAL_GO\" \"$@\"\n"
	if err := os.WriteFile(recorder, []byte(script), 0o755); err != nil {
		t.Fatalf("write go recorder: %v", err)
	}
	return recorder, logPath, realGo
}

func assertGoCall(t *testing.T, logPath, wantArgs, cwdSuffix string) {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("go call log: %v", err)
	}
	wantSuffix := "/" + filepath.ToSlash(cwdSuffix)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		cwd, args, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if strings.HasSuffix(filepath.ToSlash(cwd), wantSuffix) && strings.HasPrefix(args, wantArgs) {
			return
		}
	}
	t.Errorf("go was never invoked as %q in %s; recorded calls:\n%s", wantArgs, cwdSuffix, data)
}

// snapshotSourceTree records every file in the checkout (generated directories
// included) by content hash, so a build that writes web/dist or the embedded
// asset directory is visible even though those paths are gitignored.
func snapshotSourceTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string, 4096)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			switch entry.Name() {
			case ".git", ".aha2-context", "node_modules", "tmp":
				return fs.SkipDir
			}
			return nil
		}
		if entry.Name() == ".git" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snapshot[rel] = "symlink:" + target
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		snapshot[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return snapshot
}

func assertSourceTreeUnchanged(t *testing.T, before, after map[string]string) {
	t.Helper()
	reported := 0
	for path, want := range before {
		got, ok := after[path]
		if ok && got == want {
			continue
		}
		if !ok {
			t.Errorf("build removed source tree entry %s", path)
		} else {
			t.Errorf("build rewrote source tree entry %s; only --output-dir may be written", path)
		}
		reported++
		if reported >= 10 {
			t.Errorf("further source tree changes suppressed")
			break
		}
	}
	for path := range after {
		if _, ok := before[path]; ok {
			continue
		}
		t.Errorf("build created %s inside the source tree; only --output-dir may be written", path)
		reported++
		if reported >= 10 {
			t.Errorf("further source tree changes suppressed")
			break
		}
	}
}

// copySourceTree copies the working tree without git metadata or generated
// directories, producing the kind of snapshot a build server may receive.
func copySourceTree(t *testing.T, source, dest string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel == "." {
				return os.MkdirAll(dest, 0o755)
			}
			switch entry.Name() {
			case ".git", ".aha2-context", ".tools", "artifacts", "dist", "node_modules", "output", "tmp":
				return fs.SkipDir
			}
			return os.MkdirAll(filepath.Join(dest, rel), 0o755)
		}
		if entry.Name() == ".git" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("copy source tree: %v", err)
	}
}

func buildScriptPath(root string) string {
	return filepath.Join(root, "scripts", "build-windows-package.sh")
}

func peSubsystemOffset(data []byte) (int, error) {
	if len(data) < 0x40 || data[0] != 'M' || data[1] != 'Z' {
		return 0, fmt.Errorf("not a PE file")
	}
	peOffset := int(uint32(data[0x3c]) | uint32(data[0x3d])<<8 | uint32(data[0x3e])<<16 | uint32(data[0x3f])<<24)
	subsystemOffset := peOffset + 24 + 68
	if subsystemOffset+2 > len(data) {
		return 0, fmt.Errorf("truncated PE header")
	}
	return subsystemOffset, nil
}
