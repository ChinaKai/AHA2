package legacyimport

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// TestAHA1KnowledgeImportUpdateKeepsLocalAutoAttach guards the update path:
// AHA1 archives carry no auto_attach setting, so importing changed SKILL.md
// content for an existing skill must keep the local "auto attach" choice,
// while a brand-new skill still defaults to AutoAttach=false.
func TestAHA1KnowledgeImportUpdateKeepsLocalAutoAttach(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	const existingSlug = "auto-attach-demo"
	existingID := stableID("skill_aha1", existingSlug)
	if err := database.CreateSkill(ctx, domain.Skill{
		ID: existingID, PackageSlug: existingSlug, Scope: "global",
		Name: "Auto Attach Demo", Description: "Local install", Instructions: "Old local instructions.",
		Version: 1, Status: "active", Enabled: true, AutoAttach: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	seeded, err := database.Skill(ctx, existingID)
	if err != nil {
		t.Fatal(err)
	}
	if !seeded.AutoAttach || seeded.Version != 1 {
		t.Fatalf("seed skill has unexpected state: %#v", seeded)
	}

	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "aha-knowledge.json"), `{"schema_version":1}`)
	writeTestFile(t, filepath.Join(source, "skills", existingSlug, "SKILL.md"), `---
name: "Auto Attach Demo"
description: "Updated from AHA1 archive"
---

Updated instructions imported from AHA1.
`)
	writeTestFile(t, filepath.Join(source, "skills", "brand-new", "SKILL.md"), `---
name: "Brand New"
description: "Fresh import"
---

Brand new skill instructions.
`)

	plan, err := Analyze(ctx, database, Options{SourceDir: source})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Report.SkillUpdate != 1 || plan.Report.SkillCreate != 1 {
		t.Fatalf("unexpected skill actions: %#v", plan.Report)
	}

	if _, err := Execute(ctx, database, plan); err != nil {
		t.Fatal(err)
	}

	updated, err := database.Skill(ctx, existingID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.AutoAttach {
		t.Fatalf("legacy import update cleared local AutoAttach: %#v", updated)
	}
	if updated.Version != 2 {
		t.Fatalf("legacy import update version = %d, want 2", updated.Version)
	}
	if !strings.Contains(updated.Description, "Updated from AHA1 archive") || !strings.Contains(updated.Instructions, "Updated instructions imported from AHA1.") {
		t.Fatalf("legacy import update did not apply archive content: %#v", updated)
	}
	entry := ""
	for _, file := range updated.PackageFiles {
		if file.Path == "SKILL.md" {
			entry = file.Content
		}
	}
	if !strings.Contains(entry, "Updated from AHA1 archive") || !strings.Contains(entry, "Updated instructions imported from AHA1.") {
		t.Fatalf("stored SKILL.md was not refreshed from the archive: %q", entry)
	}

	fresh, err := database.Skill(ctx, stableID("skill_aha1", "brand-new"))
	if err != nil {
		t.Fatal(err)
	}
	if fresh.AutoAttach {
		t.Fatalf("newly imported skill should default AutoAttach=false: %#v", fresh)
	}
	if !strings.Contains(fresh.Instructions, "Brand new skill instructions.") {
		t.Fatalf("newly imported skill was not written: %#v", fresh)
	}
}
