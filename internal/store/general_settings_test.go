package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestGeneralSettingsDefaultsAndUpdate(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	settings, err := database.GeneralSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Language != domain.LanguageAuto {
		t.Fatalf("default language = %q, want auto", settings.Language)
	}

	updated, err := database.UpdateGeneralSettings(ctx, domain.GeneralSettings{
		Language:  domain.LanguageEnglish,
		UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Language != domain.LanguageEnglish {
		t.Fatalf("updated language = %q", updated.Language)
	}
	reloaded, err := database.GeneralSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Language != domain.LanguageEnglish {
		t.Fatalf("reloaded language = %q", reloaded.Language)
	}
	if normalized := domain.NormalizeLanguage("fr-FR"); normalized != domain.LanguageAuto {
		t.Fatalf("unknown language normalized to %q", normalized)
	}
}
