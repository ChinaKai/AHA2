package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestBackendSettingsDefaultsAndUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	settings, err := database.BackendSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.IdleTimeoutSeconds != domain.DefaultBackendIdleTimeoutSeconds || settings.TurnTimeoutSeconds != domain.DefaultBackendTurnTimeoutSeconds {
		t.Fatalf("unexpected defaults: %#v", settings)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	settings, err = database.UpdateBackendSettings(ctx, domain.BackendSettings{
		IdleTimeoutSeconds: 15 * 60,
		TurnTimeoutSeconds: 12 * 60 * 60,
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := database.BackendSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored.IdleTimeoutSeconds != settings.IdleTimeoutSeconds || stored.TurnTimeoutSeconds != settings.TurnTimeoutSeconds || !stored.UpdatedAt.Equal(now) {
		t.Fatalf("settings were not persisted: %#v", stored)
	}
}
