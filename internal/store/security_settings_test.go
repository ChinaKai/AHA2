package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestSecuritySettingsDefaultAndPersistence(t *testing.T) {
	t.Parallel()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	settings, err := database.SecuritySettings(context.Background())
	if err != nil || !settings.ValidateOrigin {
		t.Fatalf("default security settings = %#v, %v", settings, err)
	}
	settings, err = database.UpdateSecuritySettings(context.Background(), domain.SecuritySettings{ValidateOrigin: false})
	if err != nil || settings.ValidateOrigin {
		t.Fatalf("update security settings = %#v, %v", settings, err)
	}
	stored, err := database.SecuritySettings(context.Background())
	if err != nil || stored.ValidateOrigin || stored.UpdatedAt.IsZero() {
		t.Fatalf("stored security settings = %#v, %v", stored, err)
	}
}
