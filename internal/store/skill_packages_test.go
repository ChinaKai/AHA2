package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func skillPackageTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func skillTestFiles(body string) []domain.SkillFile {
	return []domain.SkillFile{{Path: "SKILL.md", Content: "---\nname: \"demo\"\ndescription: \"demo\"\n---\n\n" + body + "\n"}}
}

// seedPackageSkill creates a skill and gives it an extra package file. Hydration
// can rebuild SKILL.md from the row, so only a file like scripts/keep.sh can
// prove the directory itself survived.
func seedPackageSkill(t *testing.T, database *Store, id, slug, body string) domain.Skill {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := database.CreateSkill(ctx, domain.Skill{ID: id, PackageSlug: slug, Scope: "global", Name: "demo", Description: "demo", Instructions: body, Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	created, err := database.Skill(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	files := append(skillTestFiles(body), domain.SkillFile{Path: "scripts/keep.sh", Content: "echo keep\n"})
	updated, err := database.UpdateSkillPackage(ctx, created, created.Version, files)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func requirePackageFile(t *testing.T, database *Store, slug, path, want string) {
	t.Helper()
	file := filepath.Join(database.dataDir, "knowledge", "skills", slug, filepath.FromSlash(path))
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("package file %s is gone: %v", path, err)
	}
	if want != "" && !strings.Contains(string(content), want) {
		t.Fatalf("package file %s = %q", path, content)
	}
}

// TestCreateSkillPackageSlugConflictKeepsExistingPackage pins that a rejected
// insert never touches the filesystem: the package directory belongs to the
// skill that already owns the slug.
func TestCreateSkillPackageSlugConflictKeepsExistingPackage(t *testing.T) {
	ctx := context.Background()
	database := skillPackageTestStore(t)
	now := time.Now().UTC()
	before := seedPackageSkill(t, database, "skill-holder", "slug-collision", "original body")
	intruder := func() domain.Skill {
		return domain.Skill{ID: "skill-intruder", PackageSlug: "slug-collision", Scope: "global", Name: "demo", Description: "demo", Instructions: "intruder body", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
	}
	if _, err := database.CreateSkillPackage(ctx, intruder(), skillTestFiles("intruder body")); err == nil {
		t.Fatal("slug conflict was accepted")
	}
	requirePackageFile(t, database, "slug-collision", "scripts/keep.sh", "echo keep")
	requirePackageFile(t, database, "slug-collision", "SKILL.md", "original body")
	if err := database.CreateSkill(ctx, intruder()); err == nil {
		t.Fatal("slug conflict was accepted without files")
	}
	requirePackageFile(t, database, "slug-collision", "scripts/keep.sh", "echo keep")
	requirePackageFile(t, database, "slug-collision", "SKILL.md", "original body")

	after, err := database.Skill(ctx, "skill-holder")
	if err != nil {
		t.Fatalf("existing skill disappeared after the rejected create: %v", err)
	}
	if after.Version != before.Version || after.Instructions != "original body" || len(after.PackageFiles) != len(before.PackageFiles) {
		t.Fatalf("existing skill changed: before=%#v after=%#v", before, after)
	}
	if _, err := database.Skill(ctx, "skill-intruder"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("intruder row exists: %v", err)
	}
	// An invalid package is rejected before anything reaches the filesystem.
	if _, err := database.CreateSkillPackage(ctx, intruder(), []domain.SkillFile{{Path: "notes.txt", Content: "no entry"}}); err == nil {
		t.Fatal("invalid package was accepted")
	}
	requirePackageFile(t, database, "slug-collision", "scripts/keep.sh", "echo keep")
	requirePackageFile(t, database, "slug-collision", "SKILL.md", "original body")
}

// TestReplaceSkillPackageRenameFailureKeepsOriginal drives the final swap into
// failure and checks the previous package survives. The caller must not
// compensate again: the swap restores the directory itself.
func TestReplaceSkillPackageRenameFailureKeepsOriginal(t *testing.T) {
	ctx := context.Background()
	database := skillPackageTestStore(t)
	current := seedPackageSkill(t, database, "skill-swap", "swap-demo", "original body")
	previous := skillPackageRename
	skillPackageRename = func(string, string) error { return errors.New("rename failed") }
	defer func() { skillPackageRename = previous }()

	if _, err := database.UpdateSkillPackage(ctx, current, current.Version, skillTestFiles("replacement body")); err == nil {
		t.Fatal("rename failure was ignored")
	}
	requirePackageFile(t, database, "swap-demo", "scripts/keep.sh", "echo keep")
	requirePackageFile(t, database, "swap-demo", "SKILL.md", "original body")
	after, err := database.Skill(ctx, "skill-swap")
	if err != nil {
		t.Fatalf("skill disappeared after the failed swap: %v", err)
	}
	if after.Version != current.Version || after.Instructions != "original body" {
		t.Fatalf("failed swap changed the skill: %#v", after)
	}
	if _, err := os.Stat(filepath.Join(database.dataDir, "knowledge", "skills", "swap-demo.backup")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup was left behind: %v", err)
	}
}

// TestSkillPackageSwapRollbackIsIdempotent pins that compensating twice cannot
// destroy the package the first rollback already restored.
func TestSkillPackageSwapRollbackIsIdempotent(t *testing.T) {
	ctx := context.Background()
	database := skillPackageTestStore(t)
	current := seedPackageSkill(t, database, "skill-idem", "idem-demo", "original body")
	files, err := validateSkillPackageFiles(skillTestFiles("swapped body"))
	if err != nil {
		t.Fatal(err)
	}
	swap, err := database.replaceSkillPackage(current, files)
	if err != nil {
		t.Fatal(err)
	}
	swap.rollback()
	swap.rollback()
	requirePackageFile(t, database, "idem-demo", "scripts/keep.sh", "echo keep")
	requirePackageFile(t, database, "idem-demo", "SKILL.md", "original body")
	after, err := database.Skill(ctx, "skill-idem")
	if err != nil {
		t.Fatalf("skill disappeared after rollback: %v", err)
	}
	if after.Instructions != "original body" || after.Version != current.Version {
		t.Fatalf("rollback did not restore the original package: %#v", after)
	}
}
