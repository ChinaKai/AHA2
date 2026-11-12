package workspace

import (
	"archive/tar"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/agentproxy"
)

// writeAgentProxyBundle lays out an installed agent-proxy directory: the helper
// binary plus the manifest the runtime verifies it against.
func writeAgentProxyBundle(t *testing.T, installDir string, helpers ...agentProxyBundleHelper) string {
	t.Helper()
	directory := filepath.Join(installDir, agentProxyBundleDirectoryName)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, helper := range helpers {
		if err := os.WriteFile(filepath.Join(directory, helper.File), []byte("#!/bin/sh\necho 'aha2 bundle "+helper.OS+"-"+helper.Arch+"'\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := agentProxyBundleManifest{Protocol: agentproxy.Protocol, Version: "1.0.0", Helpers: helpers}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, agentProxyManifestName), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	return directory
}

func agentProxyBundleEntry(t *testing.T, directory, osName, arch string) agentProxyBundleHelper {
	t.Helper()
	file := "aha2-agent-proxy-" + osName + "-" + arch
	content, err := os.ReadFile(filepath.Join(directory, file))
	if err != nil {
		t.Fatal(err)
	}
	return agentProxyBundleHelper{OS: osName, Arch: arch, File: file, SHA256: testSHA256(content)}
}

func fakeAgentProxyExecutable(t *testing.T, installDir string) string {
	t.Helper()
	executable := filepath.Join(installDir, "aha2.exe")
	if err := os.WriteFile(executable, []byte("host executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	return executable
}

// TestAgentProxyBundleSelectedForForeignWorkspace covers the Windows-host /
// Linux-workspace case: the running executable cannot be uploaded, so the helper
// packaged for the workspace platform wins and is recorded with that platform.
func TestAgentProxyBundleSelectedForForeignWorkspace(t *testing.T) {
	installDir := t.TempDir()
	executable := fakeAgentProxyExecutable(t, installDir)
	linux := agentProxyBundleHelper{OS: "linux", Arch: "amd64", File: "aha2-agent-proxy-linux-amd64"}
	directory := writeAgentProxyBundle(t, installDir, linux)
	t.Setenv(agentProxyHelperOverride, "")

	content, err := os.ReadFile(filepath.Join(directory, linux.File))
	if err != nil {
		t.Fatal(err)
	}
	linux.SHA256 = testSHA256(content)
	manifest := agentProxyBundleManifest{Protocol: agentproxy.Protocol, Version: "1.0.0", Helpers: []agentProxyBundleHelper{linux}}
	encoded, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(directory, agentProxyManifestName), encoded, 0o644); err != nil {
		t.Fatal(err)
	}

	source, err := agentProxyResolveSource("windows", "amd64", executable, "linux", "amd64")
	if err != nil {
		t.Fatalf("resolve bundled helper: %v", err)
	}
	if !source.bundled || source.explicit {
		t.Fatalf("source flags = bundled:%t explicit:%t", source.bundled, source.explicit)
	}
	if source.os != "linux" || source.arch != "amd64" {
		t.Fatalf("source platform = %s/%s, want linux/amd64", source.os, source.arch)
	}
	if want := filepath.Join(directory, linux.File); source.path != want {
		t.Fatalf("source path = %s, want %s", source.path, want)
	}
	if source.sha256 != linux.SHA256 {
		t.Fatalf("source sha = %s, want %s", source.sha256, linux.SHA256)
	}
}

func TestAgentProxyBundleMissingPlatformNamesSearchedPath(t *testing.T) {
	installDir := t.TempDir()
	executable := fakeAgentProxyExecutable(t, installDir)
	directory := writeAgentProxyBundle(t, installDir)
	t.Setenv(agentProxyHelperOverride, "")
	arm := agentProxyBundleHelper{OS: "linux", Arch: "arm64", File: "aha2-agent-proxy-linux-arm64", SHA256: testSHA256([]byte(""))}
	content, _ := os.ReadFile(filepath.Join(directory, arm.File))
	arm.SHA256 = testSHA256(content)
	encoded, _ := json.Marshal(agentProxyBundleManifest{Protocol: agentproxy.Protocol, Version: "1.0.0", Helpers: []agentProxyBundleHelper{arm}})
	if err := os.WriteFile(filepath.Join(directory, agentProxyManifestName), encoded, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := agentProxyResolveSource("windows", "amd64", executable, "linux", "amd64")
	if err == nil {
		t.Fatal("missing platform was accepted")
	}
	message := err.Error()
	for _, want := range []string{"linux/amd64", "windows/amd64", filepath.Join(directory, agentProxyManifestName), "linux/arm64", agentProxyHelperOverride} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not mention %q", message, want)
		}
	}
}

func TestAgentProxyBundleRejectsSHA256Mismatch(t *testing.T) {
	installDir := t.TempDir()
	executable := fakeAgentProxyExecutable(t, installDir)
	directory := writeAgentProxyBundle(t, installDir, agentProxyBundleHelper{OS: "linux", Arch: "amd64", File: "aha2-agent-proxy-linux-amd64", SHA256: strings.Repeat("0", 64)})
	t.Setenv(agentProxyHelperOverride, "")

	if _, err := agentProxyResolveSource("windows", "amd64", executable, "linux", "amd64"); err == nil {
		t.Fatal("sha256 mismatch was accepted")
	} else {
		message := err.Error()
		for _, want := range []string{"linux/amd64", filepath.Join(directory, "aha2-agent-proxy-linux-amd64"), "sha256"} {
			if !strings.Contains(message, want) {
				t.Fatalf("error %q does not mention %q", message, want)
			}
		}
	}
}

// TestAgentProxyBundleSurvivesTarRoundTrip is the packaging contract: what the
// release tar ships is still selectable and runnable after it is unpacked.
func TestAgentProxyBundleSurvivesTarRoundTrip(t *testing.T) {
	sourceInstall := t.TempDir()
	directory := writeAgentProxyBundle(t, sourceInstall, agentProxyBundleHelper{OS: "linux", Arch: "amd64", File: "aha2-agent-proxy-linux-amd64"})
	helper := agentProxyBundleEntry(t, directory, "linux", "amd64")
	manifest := agentProxyBundleManifest{Protocol: agentproxy.Protocol, Version: "1.0.0", Helpers: []agentProxyBundleHelper{helper}}
	encoded, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(directory, agentProxyManifestName), encoded, 0o644); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "agent-proxy.tar")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(file)
	for _, name := range []string{helper.File, agentProxyManifestName} {
		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteHeader(&tar.Header{Name: agentProxyBundleDirectoryName + "/" + name, Mode: 0o755, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	extracted := t.TempDir()
	file, err = os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(extracted, header.Name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, content, os.FileMode(header.Mode)); err != nil {
			t.Fatal(err)
		}
	}
	file.Close()
	executable := fakeAgentProxyExecutable(t, extracted)
	t.Setenv(agentProxyHelperOverride, "")

	source, err := agentProxyResolveSource("windows", "amd64", executable, "linux", "amd64")
	if err != nil {
		t.Fatalf("resolve extracted bundle: %v", err)
	}
	if source.sha256 != helper.SHA256 {
		t.Fatalf("extracted helper sha = %s, want %s", source.sha256, helper.SHA256)
	}
	output, err := exec.Command(source.path, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("run extracted helper: %v (%s)", err, output)
	}
	if !strings.Contains(string(output), "aha2") {
		t.Fatalf("extracted helper output = %q", output)
	}
}

// TestAgentProxyEnsureUsesBundledHelperForForeignWorkspace runs the full
// materialisation against the mock SSH server with a simulated Windows host, so
// the manifest written to the workspace is checked against the real remote
// platform rather than the host's.
func TestAgentProxyEnsureUsesBundledHelperForForeignWorkspace(t *testing.T) {
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workspace.Platform = "linux/amd64"

	installDir := t.TempDir()
	executable := fakeAgentProxyExecutable(t, installDir)
	wrapper := writeAgentProxyWrapper(t, installDir, "bundled")
	content, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	bundleHelper := agentProxyBundleHelper{OS: "linux", Arch: "amd64", File: "aha2-agent-proxy-linux-amd64", SHA256: testSHA256(content)}
	directory := filepath.Join(installDir, agentProxyBundleDirectoryName)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, bundleHelper.File), content, 0o755); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(agentProxyBundleManifest{Protocol: agentproxy.Protocol, Version: "1.0.0", Helpers: []agentProxyBundleHelper{bundleHelper}})
	if err := os.WriteFile(filepath.Join(directory, agentProxyManifestName), encoded, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(agentProxyHelperOverride, "")
	previousExecutable, previousPlatform := agentProxyExecutablePath, agentProxyHostPlatform
	agentProxyExecutablePath = func() (string, error) { return executable, nil }
	agentProxyHostPlatform = func() (string, string) { return "windows", "amd64" }
	t.Cleanup(func() {
		agentProxyExecutablePath, agentProxyHostPlatform = previousExecutable, previousPlatform
	})

	workDir := t.TempDir()
	helper, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-bundle", "1.0.0")
	if err != nil {
		t.Fatalf("ensure bundled helper: %v", err)
	}
	manifest := readTestManifest(t, filepath.Join(filepath.Dir(helper.Path), agentProxyManifestName))
	if manifest.OS != "linux" || manifest.Arch != "amd64" {
		t.Fatalf("workspace manifest platform = %s/%s, want linux/amd64", manifest.OS, manifest.Arch)
	}
	if manifest.SHA256 != bundleHelper.SHA256 {
		t.Fatalf("workspace manifest sha = %s, want %s", manifest.SHA256, bundleHelper.SHA256)
	}
}
