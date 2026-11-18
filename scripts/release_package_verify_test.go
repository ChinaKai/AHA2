package scripts

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// These tests execute the verifier that build-windows-installer.ps1 runs, and
// the archive verifier the Release publish job runs. They cover the negative
// cases the static checks cannot: a file the installer copies but the manifest
// omits, an empty manifest, a forged commit or Web version, and a tampered
// archive.

const (
	fixtureVersion    = "1.2.3"
	fixtureCommit     = "0123456789abcdef0123456789abcdef01234567"
	fixtureWebVersion = "v1.2.3.20260101.0123456789ab"
)

var fixturePackageFiles = map[string]string{
	"aha2-windows-amd64.exe":                               "server-bytes",
	"aha2-tray-windows-amd64.exe":                          "tray-bytes",
	"plugins/feishu/windows-amd64/aha2-channel-feishu.exe": "plugin-bytes",
	"plugins/feishu/windows-amd64/plugin.json":             `{"plugin_id":"feishu"}` + "\n",
	"browser-host/host.mjs":                                "host\n",
	"browser-host/server.mjs":                              "server\n",
	"browser-host/tabs.mjs":                                "tabs\n",
	"browser-host/lock.mjs":                                "lock\n",
	"browser-host/package.json":                            `{"name":"aha2-browser-host"}` + "\n",
	"browser-host/README.md":                               "readme\n",
	"browser-host/node_modules/playwright/package.json":    `{"name":"playwright"}` + "\n",
	"node/node.exe":                                        "node-exe-bytes",
	"node/LICENSE":                                         "node-license\n",
}

func writeFixturePackage(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for relative, content := range fixturePackageFiles {
		full := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", relative, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
	writeFixtureManifest(t, root, fixtureCommit, fixtureWebVersion)
	return root
}

func fixtureManifest(files map[string][]byte, commit, webVersion string) map[string]any {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	entries := make([]any, 0, len(paths))
	for _, path := range paths {
		data := files[path]
		sum := sha256.Sum256(data)
		entries = append(entries, map[string]any{
			"path":   path,
			"size":   len(data),
			"sha256": hex.EncodeToString(sum[:]),
		})
	}
	return map[string]any{
		"schema_version": 1,
		"product":        "AHA2",
		"version":        fixtureVersion,
		"commit":         commit,
		"built_at":       "2026-11-12T00:00:00Z",
		"generator": map[string]any{
			"script":      "scripts/build-windows-package.sh",
			"web_version": webVersion,
		},
		"files": entries,
	}
}

func writeFixtureManifest(t *testing.T, root, commit, webVersion string) {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path == filepath.Join(root, "release-manifest.json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walk fixture: %v", err)
	}
	raw, err := json.MarshalIndent(fixtureManifest(files, commit, webVersion), "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "release-manifest.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write fixture manifest: %v", err)
	}
}

func rewriteFixtureManifest(t *testing.T, root string, mutate func(map[string]any)) {
	t.Helper()
	path := filepath.Join(root, "release-manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture manifest: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse fixture manifest: %v", err)
	}
	mutate(manifest)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture manifest: %v", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		t.Fatalf("write fixture manifest: %v", err)
	}
}

func dropManifestEntry(manifest map[string]any, path string) {
	files, _ := manifest["files"].([]any)
	kept := files[:0]
	for _, item := range files {
		entry, _ := item.(map[string]any)
		if entry["path"] != path {
			kept = append(kept, item)
		}
	}
	manifest["files"] = kept
}

func runNodeVerifier(t *testing.T, root string) (string, error) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	args := []string{
		"scripts/verify-windows-package.mjs",
		"--manifest", filepath.Join(root, "release-manifest.json"),
		"--root", root,
		"--version", fixtureVersion,
		"--expected-commit", fixtureCommit,
		"--server", filepath.Join(root, "aha2-windows-amd64.exe"),
		"--tray", filepath.Join(root, "aha2-tray-windows-amd64.exe"),
		"--browser-host", filepath.Join(root, "browser-host"),
		"--node-exe", filepath.Join(root, "node", "node.exe"),
		"--node-license", filepath.Join(root, "node", "LICENSE"),
		"--feishu-plugin", filepath.Join(root, "plugins", "feishu", "windows-amd64", "aha2-channel-feishu.exe"),
		"--feishu-manifest", filepath.Join(root, "plugins", "feishu", "windows-amd64", "plugin.json"),
	}
	cmd := exec.Command(node, args...)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestWindowsPackageVerifierExecutable(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH")
	}

	t.Run("accepts a complete package", func(t *testing.T) {
		root := writeFixturePackage(t)
		out, err := runNodeVerifier(t, root)
		if err != nil {
			t.Fatalf("verifier rejected a complete package: %v\n%s", err, out)
		}
		if !strings.Contains(out, "Windows package verified") {
			t.Errorf("verifier output = %q", out)
		}
	})

	t.Run("rejects a required browser-host file missing from the manifest", func(t *testing.T) {
		root := writeFixturePackage(t)
		rewriteFixtureManifest(t, root, func(manifest map[string]any) {
			dropManifestEntry(manifest, "browser-host/tabs.mjs")
		})
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted a manifest without browser-host/tabs.mjs:\n%s", out)
		}
		if !strings.Contains(out, "manifest is missing required package file: browser-host/tabs.mjs") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})

	t.Run("rejects any browser-host file missing from the manifest", func(t *testing.T) {
		root := writeFixturePackage(t)
		// README.md is not in the required list, so this exercises the
		// installer-input-to-manifest direction rather than the scope check.
		rewriteFixtureManifest(t, root, func(manifest map[string]any) {
			dropManifestEntry(manifest, "browser-host/README.md")
		})
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted an unlisted browser-host file:\n%s", out)
		}
		if !strings.Contains(out, "installer would copy browser-host/README.md but the manifest does not list it") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})

	t.Run("rejects a node file missing from the manifest", func(t *testing.T) {
		root := writeFixturePackage(t)
		rewriteFixtureManifest(t, root, func(manifest map[string]any) {
			dropManifestEntry(manifest, "node/LICENSE")
		})
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted a manifest without node/LICENSE:\n%s", out)
		}
		if !strings.Contains(out, "manifest is missing required package file: node/LICENSE") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})

	t.Run("rejects a deleted browser-host input", func(t *testing.T) {
		root := writeFixturePackage(t)
		if err := os.Remove(filepath.Join(root, "browser-host", "tabs.mjs")); err != nil {
			t.Fatal(err)
		}
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted a manifest entry with no installer input:\n%s", out)
		}
		if !strings.Contains(out, "manifest lists browser-host/tabs.mjs but the installer does not copy it") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})

	t.Run("rejects a deleted node input", func(t *testing.T) {
		root := writeFixturePackage(t)
		if err := os.Remove(filepath.Join(root, "node", "LICENSE")); err != nil {
			t.Fatal(err)
		}
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted a missing node input:\n%s", out)
		}
		if !strings.Contains(out, "installer input is missing: node/LICENSE") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})

	t.Run("rejects a forged commit and Web version", func(t *testing.T) {
		root := writeFixturePackage(t)
		rewriteFixtureManifest(t, root, func(manifest map[string]any) {
			manifest["commit"] = strings.Repeat("0", 40)
			manifest["generator"].(map[string]any)["web_version"] = "v9.9.9.19000101.ffffffffffff"
		})
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted a forged identity:\n%s", out)
		}
		if !strings.Contains(out, "manifest commit") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})

	t.Run("rejects a Web version that does not describe the version", func(t *testing.T) {
		root := writeFixturePackage(t)
		rewriteFixtureManifest(t, root, func(manifest map[string]any) {
			manifest["generator"].(map[string]any)["web_version"] = "v9.9.9.19000101.0123456789ab"
		})
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted a mismatched Web version:\n%s", out)
		}
		if !strings.Contains(out, "must look like v1.2.3") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})

	t.Run("rejects a Web version whose hash does not match the commit", func(t *testing.T) {
		root := writeFixturePackage(t)
		rewriteFixtureManifest(t, root, func(manifest map[string]any) {
			manifest["generator"].(map[string]any)["web_version"] = "v1.2.3.20260101.ffffffffffff"
		})
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted a mismatched Web version hash:\n%s", out)
		}
		if !strings.Contains(out, "does not match manifest commit") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})

	t.Run("rejects a tampered installer input", func(t *testing.T) {
		root := writeFixturePackage(t)
		// Same length as server-bytes so the size check passes and the digest
		// check is what rejects the input.
		if err := os.WriteFile(filepath.Join(root, "aha2-windows-amd64.exe"), []byte("tampered-abc"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted a tampered input:\n%s", out)
		}
		if !strings.Contains(out, "manifest SHA256 for aha2-windows-amd64.exe does not match") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})
}

func archiveFixtureDist(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path == filepath.Join(root, "release-manifest.json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walk fixture: %v", err)
	}
	return files
}

func writeArchiveFixture(t *testing.T, zipPath string, mutateDist func(map[string][]byte), mutateManifest func(map[string]any)) {
	t.Helper()
	root := writeFixturePackage(t)
	files := archiveFixtureDist(t, root)
	manifest := fixtureManifest(files, fixtureCommit, fixtureWebVersion)
	if mutateDist != nil {
		mutateDist(files)
	}
	if mutateManifest != nil {
		mutateManifest(manifest)
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal archive manifest: %v", err)
	}
	entries := map[string][]byte{
		"dist/release-manifest.json":        append(encoded, '\n'),
		"installer/AHA2-Setup-x64.exe":      []byte("machine-setup"),
		"installer/AHA2-Setup-User-x64.exe": []byte("user-setup"),
	}
	for path, data := range files {
		entries["dist/"+path] = data
	}
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := entry.Write(entries[name]); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
}

func runArchiveVerifier(t *testing.T, zipPath string) (string, error) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH")
	}
	cmd := exec.Command(python, "scripts/verify-windows-package-archive.py", zipPath,
		"--version", fixtureVersion, "--commit", fixtureCommit)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestWindowsPackageArchiveVerifierExecutable(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not on PATH")
	}

	t.Run("accepts the official layout", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, nil)
		out, err := runArchiveVerifier(t, zipPath)
		if err != nil {
			t.Fatalf("archive verifier rejected a valid archive: %v\n%s", err, out)
		}
		if !strings.Contains(out, "Windows package archive verified") {
			t.Errorf("archive verifier output = %q", out)
		}
	})

	t.Run("rejects an empty manifest", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, func(manifest map[string]any) {
			manifest["files"] = []any{}
		})
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted an empty manifest:\n%s", out)
		}
		if !strings.Contains(out, "must list at least one package file") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})

	t.Run("rejects a missing required package file", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, func(manifest map[string]any) {
			dropManifestEntry(manifest, "node/LICENSE")
		})
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted a manifest without node/LICENSE:\n%s", out)
		}
		if !strings.Contains(out, "missing required package file: node/LICENSE") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})

	t.Run("rejects a dist file the manifest does not list", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, func(files map[string][]byte) {
			files["ha2sneaky.dll"] = []byte("sneaky")
		}, nil)
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted an unlisted dist file:\n%s", out)
		}
		if !strings.Contains(out, "contains dist/ha2sneaky.dll") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})

	t.Run("rejects a tampered archive member", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, func(files map[string][]byte) {
			// Same length as server-bytes so the size check passes and the
			// digest check is what rejects the archive.
			files["aha2-windows-amd64.exe"] = []byte("tampered-abc")
		}, nil)
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted a tampered member:\n%s", out)
		}
		if !strings.Contains(out, "SHA256 mismatch for dist/aha2-windows-amd64.exe") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})

	t.Run("rejects a forged commit", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, func(manifest map[string]any) {
			manifest["commit"] = strings.Repeat("0", 40)
			manifest["generator"].(map[string]any)["web_version"] = "v9.9.9.19000101.ffffffffffff"
		})
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted a forged commit:\n%s", out)
		}
		if !strings.Contains(out, "does not match expected commit") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})

	t.Run("rejects a Web version that does not describe the version", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, func(manifest map[string]any) {
			manifest["generator"].(map[string]any)["web_version"] = "v9.9.9.19000101.0123456789ab"
		})
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted a mismatched Web version:\n%s", out)
		}
		if !strings.Contains(out, "must look like v1.2.3") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})
}
