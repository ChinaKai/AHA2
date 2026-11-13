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

// TestLocalBuildAndDeployBuildTheHelperBundle checks the local_build.sh entry
// point: a clean build failed the installer's helper check because the helper
// was never built. The build-and-deploy execution is pinned separately by
// TestBuildAndDeployActuallyRunsTheHelperBuild. Copying the bundle into an
// existing installation is deliberately not tested here; deployment has moved
// to the local installer and is out of this task.
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
	} {
		content := read(contract.file)
		if !strings.Contains(content, contract.want) {
			t.Fatalf("%s no longer contains %q", contract.file, contract.want)
		}
	}
}

// powerShellBlockAfter returns the body of the braced block whose opening brace
// ends at start. It is deliberately small: the scripts it reads are checked in
// data, and counting braces is enough to keep the assertions inside the step
// they name without embedding a PowerShell parser.
func powerShellBlockAfter(t *testing.T, content, marker string) string {
	t.Helper()
	start := strings.Index(content, marker)
	if start < 0 {
		t.Fatalf("marker not found: %q", marker)
	}
	open := start + len(marker)
	depth := 1
	for index := open; index < len(content); index++ {
		switch content[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[open:index]
			}
		}
	}
	t.Fatalf("unterminated block after %q", marker)
	return ""
}

// TestBuildAndDeployActuallyRunsTheHelperBuild pins the execution itself, not
// the step name or the arguments: deleting the Invoke-Native line used to leave
// a string-only test green because the script name and output directory were
// still present in the argument array.
func TestBuildAndDeployActuallyRunsTheHelperBuild(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "build-and-deploy-windows-user.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	block := powerShellBlockAfter(t, string(content), `Invoke-Step "Build Agent API stdio helpers" {`)
	argumentsLine := `$helperArguments += @("bash", "scripts/build-agent-proxy-helpers.sh", "dist/agent-proxy")`
	callLine := "Invoke-Native $wslExecutable $helperArguments"
	argumentsAt := strings.Index(block, argumentsLine)
	if argumentsAt < 0 {
		t.Fatalf("helper step does not assemble the build command: %s", argumentsLine)
	}
	callAt := strings.Index(block, callLine)
	if callAt < 0 {
		t.Fatalf("helper step assembles the build command but never runs it: missing %q", callLine)
	}
	if callAt < argumentsAt {
		t.Fatalf("%q runs before the command it is given is assembled", callLine)
	}
	if between := block[argumentsAt+len(argumentsLine) : callAt]; strings.TrimSpace(between) != "" {
		t.Fatalf("helper command is not followed directly by its execution: %q", between)
	}
}
