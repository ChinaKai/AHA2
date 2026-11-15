package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestVaultSkillPreferencePersistsAndUpdates(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if items, err := database.ListVaultSkillPreferences(ctx); err != nil || len(items) != 0 {
		t.Fatalf("fresh preferences = %#v err=%v", items, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	global := domain.VaultSkillPreference{
		SkillID: "vault-skill:global:demo", Scope: "global", AutoAttach: true,
		GlobalAttachConfirmedAt: now, UpdatedAt: now,
	}
	if err := database.SetVaultSkillPreference(ctx, global); err != nil {
		t.Fatal(err)
	}
	items, err := database.ListVaultSkillPreferences(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("stored preferences = %#v err=%v", items, err)
	}
	if items[0].SkillID != global.SkillID || !items[0].AutoAttach || items[0].GlobalAttachConfirmedAt.IsZero() {
		t.Fatalf("stored preference = %#v", items[0])
	}
	// A scope move and disable overwrite the same row: one preference per key.
	global.Scope, global.ProjectID, global.AutoAttach = "project", "project_demo", false
	global.GlobalAttachConfirmedAt, global.UpdatedAt = time.Time{}, now.Add(time.Minute)
	if err := database.SetVaultSkillPreference(ctx, global); err != nil {
		t.Fatal(err)
	}
	items, err = database.ListVaultSkillPreferences(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("updated preferences = %#v err=%v", items, err)
	}
	if items[0].AutoAttach || items[0].Scope != "project" || items[0].ProjectID != "project_demo" || !items[0].GlobalAttachConfirmedAt.IsZero() {
		t.Fatalf("updated preference = %#v", items[0])
	}
}

func TestDeleteProjectRemovesItsVaultSkillPreferences(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Second)
	project := domain.Project{ID: "project_prefs", Name: "Prefs", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	for _, item := range []domain.VaultSkillPreference{
		{SkillID: "vault-skill:global:demo", Scope: "global", AutoAttach: true, UpdatedAt: now},
		{SkillID: "vault-skill:project:project_prefs:demo", Scope: "project", ProjectID: project.ID, AutoAttach: true, UpdatedAt: now},
	} {
		if err := database.SetVaultSkillPreference(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.DeleteProject(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	items, err := database.ListVaultSkillPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SkillID != "vault-skill:global:demo" {
		t.Fatalf("project deletion left preferences behind: %#v", items)
	}
}
