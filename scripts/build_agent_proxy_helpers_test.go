package scripts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type agentProxyManifest struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
	Helpers  []struct {
		OS     string `json:"os"`
		Arch   string `json:"arch"`
		File   string `json:"file"`
		SHA256 string `json:"sha256"`
	} `json:"helpers"`
}

// TestBuildAgentProxyHelpersScriptProducesVerifiedBundle runs the real build
// script with a stub go command. It pins the contract the runtime reads: the
// manifest lists os/arch/file/sha256 for every helper, and the hashes match the
// files that were produced.
func TestBuildAgentProxyHelpersScriptProducesVerifiedBundle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the build script runs under bash")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}
	repo := repoRoot(t)
	work := t.TempDir()
	fakeGo := filepath.Join(work, "go")
	script := "#!/bin/sh\n" +
		"out=\"\"\n" +
		"while [ $# -gt 0 ]; do\n" +
		"  if [ \"$1\" = \"-o\" ]; then shift; out=\"$1\"; fi\n" +
		"  shift\n" +
		"done\n" +
		"[ -n \"$out\" ] || exit 2\n" +
		"printf 'fake helper %s/%s\\n' \"$GOOS\" \"$GOARCH\" > \"$out\"\n"
	if err := os.WriteFile(fakeGo, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(work, "agent-proxy")
	command := exec.Command("bash", filepath.Join(repo, "scripts", "build-agent-proxy-helpers.sh"), output)
	command.Env = append(os.Environ(),
		"GO_BIN="+fakeGo,
		"VERSION=1.2.3",
		"WEB_VERSION=v1.2.3.test",
		"AHA2_AGENT_PROXY_TARGETS=linux/amd64 linux/arm64",
	)
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build helpers: %v\n%s", err, combined)
	}
	content, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest agentProxyManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatalf("parse manifest: %v\n%s", err, content)
	}
	if manifest.Protocol != 1 || manifest.Version != "1.2.3" || len(manifest.Helpers) != 2 {
		t.Fatalf("manifest = %#v", manifest)
	}
	want := map[string]bool{"linux/amd64": false, "linux/arm64": false}
	for _, helper := range manifest.Helpers {
		platform := helper.OS + "/" + helper.Arch
		if _, ok := want[platform]; !ok {
			t.Fatalf("unexpected platform in manifest: %s", platform)
		}
		content, err := os.ReadFile(filepath.Join(output, helper.File))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		if got := hex.EncodeToString(sum[:]); got != helper.SHA256 {
			t.Fatalf("%s sha = %s, manifest = %s", helper.File, got, helper.SHA256)
		}
		if !strings.Contains(string(content), "fake helper "+platform) {
			t.Fatalf("%s content = %q", helper.File, content)
		}
		want[platform] = true
	}
	for platform, seen := range want {
		if !seen {
			t.Fatalf("manifest is missing %s", platform)
		}
	}
}

// TestLocalBuildAndDeployBuildTheHelperBundle is a wiring regression for the
// review finding that a clean local build failed the installer's helper check:
// both build entry points must produce dist/agent-proxy before packaging.
// Copying the bundle into an existing installation is deliberately not tested
// here; deployment has moved to the local installer and is out of this task.
func TestLocalBuildAndDeployBuildTheHelperBundle(t *testing.T) {
	repo := repoRoot(t)
	read := func(name string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	for _, contract := range []struct {
		file string
		want string
	}{
		{"local_build.sh", "build-agent-proxy-helpers.sh"},
		{"local_build.sh", "-InputHelperDir"},
		{"local_build.sh", "dist/agent-proxy"},
		{"scripts/build-and-deploy-windows-user.ps1", "Build Agent API stdio helpers"},
		{"scripts/build-and-deploy-windows-user.ps1", "scripts/build-agent-proxy-helpers.sh"},
		{"scripts/build-and-deploy-windows-user.ps1", "dist/agent-proxy"},
	} {
		content := read(contract.file)
		if !strings.Contains(content, contract.want) {
			t.Fatalf("%s no longer contains %q", contract.file, contract.want)
		}
	}
}
