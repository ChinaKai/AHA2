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

func writeFixtureInstallers(t *testing.T) string {
	t.Helper()
	installerDir := t.TempDir()
	for _, name := range []string{"AHA2-Setup-User-x64.exe"} {
		if err := os.WriteFile(filepath.Join(installerDir, name), []byte("installer-"+name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return installerDir
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

func addManifestEntry(t *testing.T, root, relative string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	sum := sha256.Sum256(data)
	rewriteFixtureManifest(t, root, func(manifest map[string]any) {
		files, _ := manifest["files"].([]any)
		manifest["files"] = append(files, map[string]any{
			"path":   relative,
			"size":   len(data),
			"sha256": hex.EncodeToString(sum[:]),
		})
	})
}

func replaceWithSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink on this platform: %v", err)
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
		if !strings.Contains(out, "installer input node/LICENSE is missing") {
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

	t.Run("rejects a node file the installer does not copy", func(t *testing.T) {
		root := writeFixturePackage(t)
		if err := os.WriteFile(filepath.Join(root, "node", "extra.exe"), []byte("extra"), 0o644); err != nil {
			t.Fatal(err)
		}
		addManifestEntry(t, root, "node/extra.exe")
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted node/extra.exe, which Inno Setup does not copy:\n%s", out)
		}
		if !strings.Contains(out, "outside the package install scope: node/extra.exe") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})

	t.Run("rejects a symlinked server input", func(t *testing.T) {
		root := writeFixturePackage(t)
		server := filepath.Join(root, "aha2-windows-amd64.exe")
		target := filepath.Join(root, "server-real.bin")
		if err := os.Rename(server, target); err != nil {
			t.Fatal(err)
		}
		replaceWithSymlink(t, target, server)
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted a symlinked server input:\n%s", out)
		}
		if !strings.Contains(out, "installer input aha2-windows-amd64.exe is a symlink") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})

	t.Run("rejects a symlinked node runtime", func(t *testing.T) {
		root := writeFixturePackage(t)
		nodeExe := filepath.Join(root, "node", "node.exe")
		target := filepath.Join(root, "node", "node-real.exe")
		if err := os.Rename(nodeExe, target); err != nil {
			t.Fatal(err)
		}
		replaceWithSymlink(t, target, nodeExe)
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted a symlinked node runtime:\n%s", out)
		}
		if !strings.Contains(out, "installer input node/node.exe is a symlink") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})

	t.Run("rejects a symlinked browser-host root", func(t *testing.T) {
		root := writeFixturePackage(t)
		realDir := filepath.Join(root, "browser-host-real")
		if err := os.Rename(filepath.Join(root, "browser-host"), realDir); err != nil {
			t.Fatal(err)
		}
		replaceWithSymlink(t, realDir, filepath.Join(root, "browser-host"))
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted a symlinked browser-host root:\n%s", out)
		}
		if !strings.Contains(out, "browser host root is a symlink") {
			t.Errorf("unexpected verifier error: %s", out)
		}
	})

	t.Run("rejects a symlinked release manifest", func(t *testing.T) {
		root := writeFixturePackage(t)
		manifest := filepath.Join(root, "release-manifest.json")
		target := filepath.Join(root, "release-manifest.real.json")
		if err := os.Rename(manifest, target); err != nil {
			t.Fatal(err)
		}
		replaceWithSymlink(t, target, manifest)
		out, err := runNodeVerifier(t, root)
		if err == nil {
			t.Fatalf("verifier accepted a symlinked release manifest:\n%s", out)
		}
		if !strings.Contains(out, "release manifest is a symlink") {
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

type archiveEntry struct {
	name string
	data []byte
	mode os.FileMode
}

func writeArchiveFixture(t *testing.T, zipPath string, mutateDist func(map[string][]byte), mutateManifest func(map[string]any), extra ...archiveEntry) {
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
	entries := []archiveEntry{
		{name: "dist/release-manifest.json", data: append(encoded, '\n')},
		{name: "installer/AHA2-Setup-User-x64.exe", data: []byte("user-setup")},
	}
	for path, data := range files {
		entries = append(entries, archiveEntry{name: "dist/" + path, data: data})
	}
	entries = append(entries, extra...)
	sort.SliceStable(entries, func(left, right int) bool { return entries[left].name < entries[right].name })

	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		zipEntry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", entry.name, err)
		}
		if _, err := zipEntry.Write(entry.data); err != nil {
			t.Fatalf("write zip entry %s: %v", entry.name, err)
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

func runPackageAssembly(t *testing.T, packageDir, installerDir, zipPath string) (string, error) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH")
	}
	cmd := exec.Command(python, "scripts/assemble-windows-package.py",
		"--package-dir", packageDir,
		"--installer-dir", installerDir,
		"--output", zipPath)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestWindowsPackageWorkflowAssemblyRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not on PATH")
	}
	packageDir := writeFixturePackage(t)
	installerDir := writeFixtureInstallers(t)
	zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
	out, err := runPackageAssembly(t, packageDir, installerDir, zipPath)
	if err != nil {
		t.Fatalf("workflow assembly failed: %v\n%s", err, out)
	}

	// The assembler must emit only file entries: directory metadata is not part
	// of the deployable layout and the verifier rejects it.
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open assembled zip: %v", err)
	}
	defer reader.Close()
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || strings.HasSuffix(file.Name, "/") {
			t.Errorf("assembled archive contains directory metadata: %s", file.Name)
		}
		names = append(names, file.Name)
	}
	for _, required := range []string{
		"installer/AHA2-Setup-User-x64.exe",
		"dist/release-manifest.json",
		"dist/browser-host/tabs.mjs",
	} {
		found := false
		for _, name := range names {
			if name == required {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("assembled archive is missing %s", required)
		}
	}

	out, err = runArchiveVerifier(t, zipPath)
	if err != nil {
		t.Fatalf("archive verifier rejected the workflow assembly: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Windows package archive verified") {
		t.Errorf("unexpected archive verifier output: %s", out)
	}
}

// The assembler is the last step before the archive is published. Following a
// link would package bytes the release manifest never hashed, and skipping a
// directory link would publish a package with missing files, so every link must
// fail the build with a path-specific error.
func TestWindowsPackageAssemblyRejectsSymlinkedInputs(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not on PATH")
	}

	assemblerRejects := func(t *testing.T, packageDir, installerDir, want string) {
		t.Helper()
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		out, err := runPackageAssembly(t, packageDir, installerDir, zipPath)
		if err == nil {
			t.Fatalf("assembler accepted a link input (package=%s installer=%s):\n%s", packageDir, installerDir, out)
		}
		if !strings.Contains(out, want) {
			t.Errorf("assembler error does not name the rejected input; want %q in:\n%s", want, out)
		}
	}

	t.Run("rejects a symlinked user setup", func(t *testing.T) {
		packageDir := writeFixturePackage(t)
		installerDir := writeFixtureInstallers(t)
		setup := filepath.Join(installerDir, "AHA2-Setup-User-x64.exe")
		target := filepath.Join(installerDir, "AHA2-Setup-User-x64.real.exe")
		if err := os.Rename(setup, target); err != nil {
			t.Fatal(err)
		}
		replaceWithSymlink(t, target, setup)
		assemblerRejects(t, packageDir, installerDir, "installer is a symlink: "+setup)
	})

	t.Run("rejects a symlinked installer directory", func(t *testing.T) {
		packageDir := writeFixturePackage(t)
		realDir := writeFixtureInstallers(t)
		installerDir := filepath.Join(t.TempDir(), "installer")
		replaceWithSymlink(t, realDir, installerDir)
		assemblerRejects(t, packageDir, installerDir, "installer directory is a symlink: "+installerDir)
	})

	t.Run("rejects a symlinked package directory", func(t *testing.T) {
		realDir := writeFixturePackage(t)
		installerDir := writeFixtureInstallers(t)
		packageDir := filepath.Join(t.TempDir(), "package")
		replaceWithSymlink(t, realDir, packageDir)
		assemblerRejects(t, packageDir, installerDir, "package directory is a symlink: "+packageDir)
	})

	t.Run("rejects a symlinked package manifest", func(t *testing.T) {
		packageDir := writeFixturePackage(t)
		installerDir := writeFixtureInstallers(t)
		manifest := filepath.Join(packageDir, "release-manifest.json")
		target := filepath.Join(packageDir, "release-manifest.real.json")
		if err := os.Rename(manifest, target); err != nil {
			t.Fatal(err)
		}
		replaceWithSymlink(t, target, manifest)
		assemblerRejects(t, packageDir, installerDir, "package manifest is a symlink: "+manifest)
	})

	t.Run("rejects a symlinked file inside dist", func(t *testing.T) {
		packageDir := writeFixturePackage(t)
		installerDir := writeFixtureInstallers(t)
		file := filepath.Join(packageDir, "browser-host", "tabs.mjs")
		target := filepath.Join(packageDir, "browser-host", "tabs.real.mjs")
		if err := os.Rename(file, target); err != nil {
			t.Fatal(err)
		}
		replaceWithSymlink(t, target, file)
		assemblerRejects(t, packageDir, installerDir, "package input is a symlink: "+file)
	})

	t.Run("rejects a directory link inside dist", func(t *testing.T) {
		packageDir := writeFixturePackage(t)
		installerDir := writeFixtureInstallers(t)
		realDir := filepath.Join(t.TempDir(), "node_modules-real")
		if err := os.Rename(filepath.Join(packageDir, "browser-host", "node_modules"), realDir); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(packageDir, "browser-host", "node_modules")
		replaceWithSymlink(t, realDir, link)
		// A directory link used to be skipped silently, which dropped every
		// file below it from the archive without failing the build.
		assemblerRejects(t, packageDir, installerDir, "package input directory is a symlink: "+link)
	})
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

	t.Run("rejects the retired machine-level installer", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, nil, archiveEntry{name: "installer/AHA2-Setup-x64.exe", data: []byte("machine-setup")})
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted the retired machine-level installer:\n%s", out)
		}
		if !strings.Contains(out, "unexpected installer entry: installer/AHA2-Setup-x64.exe") {
			t.Errorf("unexpected archive verifier error: %s", out)
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

	t.Run("rejects a member that escapes the package root", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, nil, archiveEntry{name: "../outside.txt", data: []byte("outside")})
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted an escaping member:\n%s", out)
		}
		if !strings.Contains(out, "member path is not normalized") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})

	t.Run("rejects an unexpected root member", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, nil, archiveEntry{name: "unexpected.exe", data: []byte("unexpected")})
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted a root member:\n%s", out)
		}
		if !strings.Contains(out, "not a file under dist/ or installer/") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})

	t.Run("rejects an unexpected top-level directory", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, nil, archiveEntry{name: "plugins/evil.exe", data: []byte("evil")})
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted an unexpected top-level directory:\n%s", out)
		}
		if !strings.Contains(out, "unexpected root entry") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})

	t.Run("rejects a duplicate member", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, nil, archiveEntry{name: "dist/aha2-windows-amd64.exe", data: []byte("server-bytes")})
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted a duplicate member:\n%s", out)
		}
		if !strings.Contains(out, "duplicate members") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})

	t.Run("rejects a case-insensitive duplicate member", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, nil, archiveEntry{name: "dist/AHA2-WINDOWS-AMD64.EXE", data: []byte("server-bytes")})
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted a case-insensitive duplicate:\n%s", out)
		}
		if !strings.Contains(out, "duplicate members") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})

	t.Run("rejects a Windows separator or drive path", func(t *testing.T) {
		for _, name := range []string{`dist\evil.txt`, `/outside.txt`, `C:/outside.txt`} {
			zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
			writeArchiveFixture(t, zipPath, nil, nil, archiveEntry{name: name, data: []byte("evil")})
			out, err := runArchiveVerifier(t, zipPath)
			if err == nil {
				t.Fatalf("archive verifier accepted unsafe member name %q:\n%s", name, out)
			}
			if !strings.Contains(out, "Windows path separator") && !strings.Contains(out, "absolute path") {
				t.Errorf("unexpected archive verifier error for %q: %s", name, out)
			}
		}
	})

	t.Run("rejects a member Windows would normalize", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, nil, archiveEntry{name: "dist/evil.", data: []byte("evil")})
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted a trailing-dot member:\n%s", out)
		}
		if !strings.Contains(out, "ends with a dot or space") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})

	t.Run("rejects Windows reserved names and invalid characters", func(t *testing.T) {
		for _, name := range []string{"dist/CON.txt", "dist/NUL", "dist/a|b.txt", "dist/a?b.txt"} {
			zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
			writeArchiveFixture(t, zipPath, nil, nil, archiveEntry{name: name, data: []byte("evil")})
			out, err := runArchiveVerifier(t, zipPath)
			if err == nil {
				t.Fatalf("archive verifier accepted invalid Windows name %q:\n%s", name, out)
			}
			if !strings.Contains(out, "reserved Windows device name") && !strings.Contains(out, "invalid character") {
				t.Errorf("unexpected archive verifier error for %q: %s", name, out)
			}
		}
	})

	t.Run("rejects directory and symlink members", func(t *testing.T) {
		for _, entry := range []archiveEntry{
			{name: "dist/emptydir/"},
			{name: "dist/link", data: []byte("outside"), mode: os.ModeSymlink | 0o777},
		} {
			zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
			writeArchiveFixture(t, zipPath, nil, nil, entry)
			out, err := runArchiveVerifier(t, zipPath)
			if err == nil {
				t.Fatalf("archive verifier accepted a non-regular member %q:\n%s", entry.name, out)
			}
			if !strings.Contains(out, "directory entry") && !strings.Contains(out, "non-regular member") {
				t.Errorf("unexpected archive verifier error for %q: %s", entry.name, out)
			}
		}
	})

	t.Run("rejects a node file the installer does not copy", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "AHA2-Windows-Package-x64.zip")
		writeArchiveFixture(t, zipPath, nil, func(manifest map[string]any) {
			files, _ := manifest["files"].([]any)
			manifest["files"] = append(files, map[string]any{
				"path": "node/extra.exe", "size": 1, "sha256": strings.Repeat("a", 64),
			})
		})
		out, err := runArchiveVerifier(t, zipPath)
		if err == nil {
			t.Fatalf("archive verifier accepted node/extra.exe, which Inno Setup does not copy:\n%s", out)
		}
		if !strings.Contains(out, "outside the package install scope: node/extra.exe") {
			t.Errorf("unexpected archive verifier error: %s", out)
		}
	})
}

func powershellForTest(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"pwsh", "powershell"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("PowerShell is not on PATH")
	return ""
}

// runInstallerBuilder exercises the complete Windows builder, not just the
// verifier: it must fail before Inno Setup runs when an installer input is a
// symlink (including a Windows junction, which the verifier also rejects).
func runInstallerBuilder(t *testing.T, root string) (string, error) {
	t.Helper()
	shell := powershellForTest(t)
	pluginDir := filepath.Join(root, "plugins", "feishu", "windows-amd64")
	args := []string{
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", "scripts/build-windows-installer.ps1",
		"-RepoPath", repoRoot(t),
		"-InputExe", filepath.Join(root, "aha2-windows-amd64.exe"),
		"-InputTrayExe", filepath.Join(root, "aha2-tray-windows-amd64.exe"),
		"-InputBrowserHost", filepath.Join(root, "browser-host"),
		"-InputNodeExe", filepath.Join(root, "node", "node.exe"),
		"-InputNodeLicense", filepath.Join(root, "node", "LICENSE"),
		"-InputFeishuPlugin", filepath.Join(pluginDir, "aha2-channel-feishu.exe"),
		"-InputFeishuManifest", filepath.Join(pluginDir, "plugin.json"),
		"-InputManifest", filepath.Join(root, "release-manifest.json"),
		"-ExpectedCommit", fixtureCommit,
		"-Version", fixtureVersion,
		"-OutputDir", filepath.Join(t.TempDir(), "installer"),
	}
	cmd := exec.Command(shell, args...)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestWindowsInstallerBuilderRejectsSymlinkedInputs(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		if _, err := exec.LookPath("powershell"); err != nil {
			t.Skip("PowerShell is not on PATH")
		}
	}

	t.Run("rejects a symlinked server input", func(t *testing.T) {
		root := writeFixturePackage(t)
		server := filepath.Join(root, "aha2-windows-amd64.exe")
		target := filepath.Join(root, "server-real.bin")
		if err := os.Rename(server, target); err != nil {
			t.Fatal(err)
		}
		replaceWithSymlink(t, target, server)
		out, err := runInstallerBuilder(t, root)
		if err == nil {
			t.Fatalf("installer builder accepted a symlinked server input:\n%s", out)
		}
		if !strings.Contains(out, "is a symlink") {
			t.Errorf("unexpected installer builder error: %s", out)
		}
	})

	t.Run("rejects a symlinked browser-host root", func(t *testing.T) {
		root := writeFixturePackage(t)
		realDir := filepath.Join(root, "browser-host-real")
		if err := os.Rename(filepath.Join(root, "browser-host"), realDir); err != nil {
			t.Fatal(err)
		}
		replaceWithSymlink(t, realDir, filepath.Join(root, "browser-host"))
		out, err := runInstallerBuilder(t, root)
		if err == nil {
			t.Fatalf("installer builder accepted a symlinked browser-host root:\n%s", out)
		}
		if !strings.Contains(out, "browser host root is a symlink") {
			t.Errorf("unexpected installer builder error: %s", out)
		}
	})
}
