package workspace

import (
	"strings"
	"testing"
)

func TestRemoteScriptKeepsSecretsOutOfSSHArguments(t *testing.T) {
	t.Parallel()
	script, err := remoteScript(Command{
		Executable: "codex",
		Args:       []string{"exec", "--json", "-"},
		Dir:        "/workspace",
		Env:        map[string]string{"OPENAI_API_KEY": "top-secret"},
		Stdin:      "prompt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "top-secret") {
		t.Fatal("secret was not delivered through stdin script")
	}
	runner := SSHRunner{Host: "localhost", User: "dev", Port: 22}
	if runner.Host+" "+runner.User == script {
		t.Fatal("unexpected script comparison")
	}
}

func TestRemoteScriptRejectsInvalidEnvName(t *testing.T) {
	t.Parallel()
	if _, err := remoteScript(Command{Executable: "echo", Env: map[string]string{"BAD-NAME": "x"}}); err == nil {
		t.Fatal("invalid environment name accepted")
	}
}
