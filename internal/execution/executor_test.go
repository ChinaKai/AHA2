package execution

import (
	"context"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
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
