package legacyimport

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// TestAHA1ImportRebuildsDeletedSkillWithFullPackage covers the recovery path a
// review found broken: a deleted skill is re-imported under the same ID. The
// create raises the version above the tombstone, so the importer must write the
// complete package in that same call instead of a follow-up CAS update with the
// stale planned version.
func TestAHA1ImportRebuildsDeletedSkillWithFullPackage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	const slug = "rebuild-demo"
	id := stableID("skill_aha1", slug)
	if err := database.CreateSkill(ctx, domain.Skill{
		ID: id, PackageSlug: slug, Scope: "global", Name: "Rebuild Demo", Description: "Original",
		Instructions: "Old instructions.", Version: 1, Status: "active", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteSkill(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SyncTombstone(ctx, "skill", id); err != nil {
		t.Fatalf("delete did not leave a tombstone: %v", err)
	}

	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "aha-knowledge.json"), `{"schema_version":1}`)
	writeTestFile(t, filepath.Join(source, "skills", slug, "SKILL.md"), `---
name: "Rebuild Demo"
description: "Re-imported after delete"
---

Re-imported instructions.
`)
	writeTestFile(t, filepath.Join(source, "skills", slug, "scripts", "run.sh"), "echo rebuilt\n")

	plan, err := Analyze(ctx, database, Options{SourceDir: source})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Report.SkillCreate != 1 {
		t.Fatalf("deleted skill was not planned as a create: %#v", plan.Report)
	}
	if _, err := Execute(ctx, database, plan); err != nil {
		t.Fatalf("rebuild import failed: %v", err)
	}

	rebuilt, err := database.Skill(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Version != 2 {
		t.Fatalf("rebuilt version=%d, want 2 (above the tombstone)", rebuilt.Version)
	}
	if len(rebuilt.PackageFiles) != 2 {
		t.Fatalf("rebuilt package files=%v", rebuilt.Files)
	}
	if _, err := database.SyncTombstone(ctx, "skill", id); err == nil {
		t.Fatal("rebuild kept the tombstone")
	}
}
