package scripts

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These tests pin the contract between the three pieces that make a Windows
// package deployable: scripts/build-windows-package.sh writes the manifest,
// scripts/build-windows-installer.ps1 installs exactly the files the manifest
// names, and the Release workflow publishes the official PackageDir layout.
// They are static checks so they run on Linux CI, where Inno Setup and the
// Windows toolchain are unavailable.

func readRepoFile(t *testing.T, root, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(data)
}

func TestInstallerInstallsPackageManifest(t *testing.T) {
	root := repoRoot(t)
	iss := readRepoFile(t, root, "installer/windows/AHA2.iss")

	// The manifest must be an explicit, overridable source so the Release job
	// can point at the package it just built.
	if !strings.Contains(iss, `#ifndef SourceManifest`) ||
		!strings.Contains(iss, `#define SourceManifest "..\..\dist\release-manifest.json"`) {
		t.Error("AHA2.iss must define an overridable SourceManifest defaulting to dist\\release-manifest.json")
	}
	manifestLine := regexp.MustCompile(`Source:\s*"\{#SourceManifest\}"\s*;\s*DestDir:\s*"\{app\}"\s*;\s*DestName:\s*"release-manifest\.json"`)
	if !manifestLine.MatchString(iss) {
		t.Error("AHA2.iss must install the package manifest byte-for-byte as {app}\\release-manifest.json")
	}
	if regexp.MustCompile(`SourceManifest[^\n]*Flags:[^\n]*(deleteafterinstall|replacesameversion)`).MatchString(iss) {
		t.Error("the installed release manifest must not be rewritten by installer flags")
	}

	// Fixed mapping the deploy script verifies: each manifest path must land
	// on the documented install path with its basename unchanged.
	for _, want := range []string{
		`Source: "{#SourceExe}"; DestDir: "{app}"; DestName: "aha2.exe"`,
		`Source: "{#SourceTrayExe}"; DestDir: "{app}"; DestName: "aha2-tray.exe"`,
		`Source: "{#SourceBrowserHost}\*"; DestDir: "{app}\browser-host"`,
		`Source: "{#SourceNodeExe}"; DestDir: "{app}\node"; DestName: "node.exe"`,
		`Source: "{#SourceNodeLicense}"; DestDir: "{app}\node"; DestName: "LICENSE"`,
		`Source: "{#SourceFeishuPlugin}"; DestDir: "{code:SelectedDataDir}\plugins\channels\feishu"; DestName: "aha2-channel-feishu.exe"`,
		`Source: "{#SourceFeishuManifest}"; DestDir: "{code:SelectedDataDir}\plugins\channels\feishu"; DestName: "plugin.json"`,
	} {
		if !strings.Contains(iss, want) {
			t.Errorf("installer mapping is missing: %s", want)
		}
	}
}

func TestInstallerBuilderValidatesPackageManifest(t *testing.T) {
	root := repoRoot(t)
	builder := readRepoFile(t, root, "scripts/build-windows-installer.ps1")

	if !strings.Contains(builder, `[string]$InputManifest = ""`) {
		t.Error("build-windows-installer.ps1 must accept -InputManifest")
	}
	if !strings.Contains(builder, `Join-Path $repo "dist\release-manifest.json"`) {
		t.Error("build-windows-installer.ps1 must default -InputManifest to dist\\release-manifest.json")
	}
	for _, contract := range []string{
		"Release manifest not found",
		`"/DSourceManifest=$manifestInputPath"`,
		"Release manifest version",
		"Release manifest commit",
		"generator.web_version",
		"Release manifest path is not a safe package-relative path",
		"Release manifest is missing required install file",
		"outside the installer's install scope",
		"Release manifest SHA256 does not match the installer input",
		"Get-FileHash -Algorithm SHA256",
	} {
		if !strings.Contains(builder, contract) {
			t.Errorf("build-windows-installer.ps1 does not enforce: %s", contract)
		}
	}
	// Both installers must receive the same manifest path in the workflow; the
	// builder itself must never regenerate or edit it.
	if strings.Contains(builder, "Set-Content") || strings.Contains(builder, "Out-File") {
		t.Error("build-windows-installer.ps1 must not rewrite the release manifest")
	}
}

func TestReleaseWorkflowPublishesPackageLayout(t *testing.T) {
	root := repoRoot(t)
	workflow := readRepoFile(t, root, ".github/workflows/release.yml")

	packageProbe := strings.Index(workflow, "build-windows-package.sh")
	installerProbe := strings.Index(workflow, "build-windows-installer.ps1")
	if packageProbe < 0 || installerProbe < 0 || packageProbe > installerProbe {
		t.Fatal("Release workflow must build the Windows package before the installers")
	}
	if !strings.Contains(workflow, `--output-dir dist/package`) {
		t.Error("Release workflow must build the package into dist/package")
	}
	if got := strings.Count(workflow, "-InputManifest"); got < 2 {
		t.Errorf("Release workflow must pass -InputManifest to both installer builds, got %d", got)
	}
	for _, contract := range []string{
		"Compress-Archive",
		"AHA2-Windows-Package-x64.zip",
		"installer/AHA2-Setup-x64.exe",
		"installer/AHA2-Setup-User-x64.exe",
		"dist/release-manifest.json",
		"release-assets/*",
		`test "$(find dist -maxdepth 1 -type f | wc -l)" -eq 13`,
		"zipfile.ZipFile",
		"hashlib.sha256",
	} {
		if !strings.Contains(workflow, contract) {
			t.Errorf("Release workflow is missing the package layout contract: %s", contract)
		}
	}
	// The archive carries the same installers that are also released on their
	// own; the standalone assets must not disappear.
	if strings.Count(workflow, "AHA2-Setup-User-x64.exe") < 3 {
		t.Error("Release workflow must assemble, verify and still publish AHA2-Setup-User-x64.exe")
	}
	if !strings.Contains(workflow, `-OutputDir (Join-Path $PWD "dist\installer")`) {
		t.Error("Release workflow must write the Windows installers to dist\\installer")
	}
}

func TestManifestRequiredScopeMatchesInstallerMapping(t *testing.T) {
	root := repoRoot(t)
	manifest := readRepoFile(t, root, "scripts/release-manifest.mjs")

	for _, path := range []string{
		"aha2-windows-amd64.exe",
		"aha2-tray-windows-amd64.exe",
		"plugins/feishu/windows-amd64/aha2-channel-feishu.exe",
		"plugins/feishu/windows-amd64/plugin.json",
		"browser-host/host.mjs",
		"browser-host/server.mjs",
		"browser-host/tabs.mjs",
		"browser-host/lock.mjs",
		"browser-host/package.json",
		"browser-host/node_modules/playwright/package.json",
		"node/node.exe",
		"node/LICENSE",
	} {
		if !strings.Contains(manifest, `"`+path+`"`) {
			t.Errorf("REQUIRED_PACKAGE_FILES is missing %s", path)
		}
	}
	for _, prefix := range []string{`"browser-host/"`, `"node/"`} {
		if !strings.Contains(manifest, prefix) {
			t.Errorf("package path scope is missing prefix %s", prefix)
		}
	}
}

func TestDeploymentDocsDescribePackageLayout(t *testing.T) {
	root := repoRoot(t)
	docs := readRepoFile(t, root, "docs/deployment.md")

	for _, contract := range []string{
		"AHA2-Windows-Package-x64.zip",
		`<PackageDir>\installer\AHA2-Setup-User-x64.exe`,
		`<PackageDir>\dist\release-manifest.json`,
		`{app}\release-manifest.json`,
		"-InputManifest",
		`{app}\browser-host`,
		`{app}\node`,
		`plugins\channels\feishu`,
	} {
		if !strings.Contains(docs, contract) {
			t.Errorf("docs/deployment.md is missing the package layout contract: %s", contract)
		}
	}
}
