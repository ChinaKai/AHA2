package execution

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

type staticBackendSettings struct {
	value domain.BackendSettings
}

func (settings staticBackendSettings) BackendSettings(context.Context) (domain.BackendSettings, error) {
	return settings.value, nil
}

func TestConfiguredAdaptersUsePersistedTimeoutsForBothBackends(t *testing.T) {
	t.Parallel()
	executor := Executor{Settings: staticBackendSettings{value: domain.BackendSettings{
		IdleTimeoutSeconds: 17 * 60,
		TurnTimeoutSeconds: 11 * 60 * 60,
	}}}
	codex, claude, err := executor.configuredAdapters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if codex.IdleTimeout != 17*time.Minute || claude.IdleTimeout != 17*time.Minute {
		t.Fatalf("idle timeouts differ: codex=%s claude=%s", codex.IdleTimeout, claude.IdleTimeout)
	}
	if codex.Timeout != 11*time.Hour || claude.Timeout != 11*time.Hour {
		t.Fatalf("turn timeouts differ: codex=%s claude=%s", codex.Timeout, claude.Timeout)
	}
}

func TestConfiguredAdaptersUseTenMinuteAndTenHourDefaults(t *testing.T) {
	t.Parallel()
	codex, claude, err := (Executor{}).configuredAdapters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if codex.IdleTimeout != 10*time.Minute || claude.IdleTimeout != 10*time.Minute || codex.Timeout != 10*time.Hour || claude.Timeout != 10*time.Hour {
		t.Fatalf("unexpected defaults: codex=%#v claude=%#v", codex, claude)
	}
}

func TestClaudeNativeRuntimeUsesCLIAccountAndDefaultModel(t *testing.T) {
	t.Parallel()
	environment, model := claudeExecutionRuntime(app.ExecutionRequest{
		Snapshot: domain.RuntimeConfigSnapshot{EnvGroupID: domain.ClaudeNativeEnvGroupID},
		Model:    domain.Model{WireModel: "default"},
		Environment: map[string]string{
			"ANTHROPIC_API_KEY":  "must-clear",
			"ANTHROPIC_BASE_URL": "https://must-clear.example",
		},
	})
	if model != "" {
		t.Fatalf("native default model = %q, want CLI default", model)
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL"} {
		if environment[key] != "" {
			t.Fatalf("%s must be cleared for native account: %#v", key, environment)
		}
	}
}

// A profile-only login is invisible to the non-interactive channel the backend
// runs in, so the execution side has to lend the same credential the probe did.
// Otherwise detection reports "logged in" and the Turn then fails to
// authenticate: the failure merely moves from a visible result to an obscure one.
func TestClaudeNativeExecutionBorrowsProfileCredential(t *testing.T) {
	const token = "sk-ant-oat01-EXAMPLE-NOT-REAL"
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte("export CLAUDE_CODE_OAUTH_TOKEN="+token+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := domain.Workspace{ID: "ws", Locality: "local", Transport: "native"}

	borrowed := lendNativeClaudeCredential(context.Background(), app.ExecutionRequest{
		Snapshot: domain.RuntimeConfigSnapshot{EnvGroupID: domain.ClaudeNativeEnvGroupID},
		Workspace: local,
	}, map[string]string{})

	if got := borrowed[workspace.ClaudeCodeOAuthTokenEnv]; got != token {
		t.Fatalf("native execution credential = %q, want the profile's token", got)
	}
}

// A non-native source authenticates with the credentials its env group supplies,
// so the operator's profile login must not be mixed in behind its back.
func TestNonNativeExecutionKeepsItsOwnCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte("export CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-EXAMPLE-NOT-REAL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{"ANTHROPIC_API_KEY": "env-group-key"}

	after := lendNativeClaudeCredential(context.Background(), app.ExecutionRequest{
		Snapshot:  domain.RuntimeConfigSnapshot{EnvGroupID: "env-some-other-group"},
		Workspace: domain.Workspace{ID: "ws", Locality: "local", Transport: "native"},
	}, before)

	if _, present := after[workspace.ClaudeCodeOAuthTokenEnv]; present {
		t.Fatalf("non-native run was given the operator's profile login: %#v", after)
	}
	if after["ANTHROPIC_API_KEY"] != "env-group-key" {
		t.Fatalf("non-native credentials were altered: %#v", after)
	}
}
