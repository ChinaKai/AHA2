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
// repository retired in task-315. Deployment now belongs to the installer on
// the target machine, so these must stay gone and must not come back through a
// doc, build script or test.
var removedOperatorDeployScripts = []string{
	"deploy-windows-user.ps1",
	"build-and-deploy-windows-user.ps1",
	"deploy-windows.ps1",
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
	match := ""
	for _, name := range removedOperatorDeployScripts {
		if strings.Contains(content, name) && len(name) > len(match) {
			match = name
		}
	}
	return match
}

// TestRemovedScriptReferenceDetector protects the sweep below from going
// vacuous: an empty name list or a broken matcher would otherwise make the
// repository-wide assertion pass while references remain.
func TestRemovedScriptReferenceDetector(t *testing.T) {
	for _, name := range removedOperatorDeployScripts {
		if got := referencesRemovedScript("powershell.exe -File scripts\\" + name); got != name {
			t.Fatalf("detector missed %s (got %q)", name, got)
		}
	}
	if got := referencesRemovedScript("bash scripts/build-windows-package.sh --version 1.2.3"); got != "" {
		t.Fatalf("detector reported a false positive: %q", got)
	}
}

func TestOperatorDeployScriptsRemoved(t *testing.T) {
	root := repoRoot(t)
	for _, name := range removedOperatorDeployScripts {
		path := filepath.Join(root, "scripts", name)
		_, err := os.Stat(path)
		if err == nil {
			t.Errorf("scripts/%s still exists; the repository must only build packages", name)
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("stat scripts/%s: %v", name, err)
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
// disk. It needs the Go and Node toolchains, so it is opt-in:
//
//	AHA2_BUILD_E2E=1 go test ./scripts/ -run TestWindowsPackageBuild -v
func TestWindowsPackageBuildProducesAllArtifacts(t *testing.T) {
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
	root := repoRoot(t)
	output := t.TempDir()
	const version = "1.2.3"
	const commit = "0123456789abcdef0123456789abcdef01234567"
	cmd := exec.Command(bash, filepath.Join(root, "scripts", "build-windows-package.sh"),
		"--version", version,
		"--commit", commit,
		"--output-dir", output,
		"--skip-web-test",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "VERSION=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build-windows-package.sh failed: %v\n%s", err, out)
	}

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
