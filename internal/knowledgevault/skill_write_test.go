package knowledgevault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newSkillWriteVault(t *testing.T) (Config, *Manager) {
	t.Helper()
	ctx := context.Background()
	config := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Skill Writes", Commit: false}); err != nil {
		t.Fatal(err)
	}
	write := func(relative, content string) {
		target := filepath.Join(config.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("projects/alpha/index.md", "---\nid: knowledge_root_project_alpha\ntitle: Alpha\nscope: project\nproject_id: project_alpha\ntype: navigation\nstatus: verified\nis_index: true\n---\nAlpha.\n")
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	manager.SetRuntimeWrite(true)
	t.Cleanup(func() { _ = manager.Close() })
	return config, manager
}

func skillWriteCode(err error) string {
	var target *SkillWriteError
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

func TestVaultSkillCreateUpdatePreservesAssetsAndDeleteMovesToTrash(t *testing.T) {
	ctx := context.Background()
	config, manager := newSkillWriteVault(t)
	created, err := manager.CreateSkillPackage(ctx, SkillWriteRequest{
		Scope: SkillScopeGlobal, Slug: "review-demo", Name: "Review Demo",
		Description: "A demo skill", Instructions: "Do the review.",
		Files: []SkillPackageFile{
			{Path: "SKILL.md", Content: "---\nname: Review Demo\ndescription: A demo skill\n---\n\nDo the review.\n"},
			{Path: "scripts/check.sh", Content: "#!/bin/sh\necho check\n"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Key != "vault-skill:global:review-demo" || created.Version != 1 || len(created.Files) != 2 {
		t.Fatalf("created = %#v", created)
	}
	skillPath := filepath.Join(config.VaultPath, "global", "skills", "review-demo", "SKILL.md")
	raw, err := os.ReadFile(skillPath)
	if err != nil || !strings.Contains(string(raw), "name: Review Demo") || !strings.Contains(string(raw), "version: 1") {
		t.Fatalf("SKILL.md = %q err=%v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "global", "skills", "review-demo", "scripts", "check.sh")); err != nil {
		t.Fatal(err)
	}

	// An update that only supplies fields rewrites SKILL.md and preserves the
	// asset files in the package.
	updated, err := manager.UpdateSkillPackage(ctx, SkillWriteRequest{
		Key: created.Key, BaseVersion: 1, Instructions: "Do the deeper review.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 || updated.Name != "Review Demo" {
		t.Fatalf("updated = %#v", updated)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "global", "skills", "review-demo", "scripts", "check.sh")); err != nil {
		t.Fatalf("asset file was dropped by update: %v", err)
	}
	raw, _ = os.ReadFile(skillPath)
	if !strings.Contains(string(raw), "version: 2") || !strings.Contains(string(raw), "Do the deeper review.") {
		t.Fatalf("updated SKILL.md = %q", raw)
	}
	// A hand-managed binary asset is left untouched by a field-only update: the
	// writer rewrites SKILL.md, not the whole package.
	binaryAsset := filepath.Join(config.VaultPath, "global", "skills", "review-demo", "assets", "logo.png")
	if err := os.MkdirAll(filepath.Dir(binaryAsset), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryAsset, []byte{0x89, 'P', 'N', 'G', 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateSkillPackage(ctx, SkillWriteRequest{Key: created.Key, BaseVersion: 2, Instructions: "Binary-safe review."}); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(binaryAsset); err != nil || len(raw) != 5 {
		t.Fatalf("binary asset was changed by a field-only update: %v %#v", err, raw)
	}
	// The same holds for files the write contract would not accept as input:
	// a package that was imported or edited by hand can carry dot-files, and a
	// field-only update must not refuse to touch SKILL.md because of them.
	packageDir := filepath.Join(config.VaultPath, "global", "skills", "review-demo")
	dotFile := filepath.Join(packageDir, ".claude", "notes.md")
	if err := os.MkdirAll(filepath.Dir(dotFile), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dotFile, filepath.Join(packageDir, ".gitkeep")} {
		if err := os.WriteFile(path, []byte("kept by hand\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dotSafe, err := manager.UpdateSkillPackage(ctx, SkillWriteRequest{Key: created.Key, Instructions: "Dot-file-safe review."})
	if err != nil {
		t.Fatalf("a field-only update was refused because of existing dot-files: %v", err)
	}
	if dotSafe.Version != 4 {
		t.Fatalf("dot-file-safe update version = %d, want 4", dotSafe.Version)
	}
	if raw, err := os.ReadFile(dotFile); err != nil || string(raw) != "kept by hand\n" {
		t.Fatalf("dot-file was changed by a field-only update: %v %q", err, raw)
	}

	if _, err := manager.DeleteSkillPackage(ctx, created.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(config.VaultPath, "global", "skills", "review-demo")); !os.IsNotExist(err) {
		t.Fatalf("package still exists after delete: %v", err)
	}
	trash := filepath.Join(config.VaultPath, ".trash", "skills", "global")
	entries, err := os.ReadDir(trash)
	if err != nil || len(entries) != 1 {
		t.Fatalf("trash entries = %#v err=%v", entries, err)
	}
}

func TestVaultSkillWriteValidatesScopeTraversalDuplicateAndSymlinks(t *testing.T) {
	ctx := context.Background()
	config, manager := newSkillWriteVault(t)

	cases := []struct {
		name string
		req  SkillWriteRequest
		code string
	}{
		{
			name: "unbound project",
			req:  SkillWriteRequest{Scope: SkillScopeProject, ProjectID: "project_missing", Slug: "demo", Name: "Demo", Description: "demo", Instructions: "body"},
			code: skillWriteErrorForbidden,
		},
		{
			name: "traversal slug",
			req:  SkillWriteRequest{Scope: SkillScopeGlobal, Slug: "../escape", Name: "Demo", Description: "demo", Instructions: "body"},
			code: skillWriteErrorInvalid,
		},
		{
			name: "traversal file",
			req: SkillWriteRequest{Scope: SkillScopeGlobal, Slug: "files", Name: "Files", Description: "files", Instructions: "body",
				Files: []SkillPackageFile{{Path: "SKILL.md", Content: "---\nname: Files\ndescription: files\n---\n\nbody\n"}, {Path: "../escape.txt", Content: "x"}}},
			code: skillWriteErrorInvalid,
		},
		{
			name: "missing description",
			req:  SkillWriteRequest{Scope: SkillScopeGlobal, Slug: "nodesc", Name: "No Desc", Instructions: "body"},
			code: skillWriteErrorInvalid,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := manager.CreateSkillPackage(ctx, testCase.req); skillWriteCode(err) != testCase.code {
				t.Fatalf("err = %v code=%q want %q", err, skillWriteCode(err), testCase.code)
			}
		})
	}

	if _, err := manager.CreateSkillPackage(ctx, SkillWriteRequest{Scope: SkillScopeGlobal, Slug: "demo", Name: "Demo", Description: "demo", Instructions: "body"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateSkillPackage(ctx, SkillWriteRequest{Scope: SkillScopeGlobal, Slug: "demo", Name: "Demo", Description: "demo", Instructions: "body"}); skillWriteCode(err) != skillWriteErrorDuplicate {
		t.Fatalf("duplicate err = %v", err)
	}
	// A flat skills/ package keeps the retired global namespace: the same slug
	// must not be shadowed by a new global/skills package.
	flat := filepath.Join(config.VaultPath, "skills", "flat-demo")
	if err := os.MkdirAll(flat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flat, "SKILL.md"), []byte("---\nname: Flat\ndescription: flat\n---\n\nflat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateSkillPackage(ctx, SkillWriteRequest{Scope: SkillScopeGlobal, Slug: "flat-demo", Name: "Flat", Description: "flat", Instructions: "flat"}); skillWriteCode(err) != skillWriteErrorDuplicate {
		t.Fatalf("flat duplicate err = %v", err)
	}
}

func TestVaultSkillWriteRejectsSymlinkedLibraryAndOversizedFile(t *testing.T) {
	ctx := context.Background()
	config, manager := newSkillWriteVault(t)
	outside := t.TempDir()
	library := filepath.Join(config.VaultPath, "global", "skills")
	if err := os.RemoveAll(library); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, library); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateSkillPackage(ctx, SkillWriteRequest{Scope: SkillScopeGlobal, Slug: "demo", Name: "Demo", Description: "demo", Instructions: "body"}); skillWriteCode(err) != skillWriteErrorForbidden {
		t.Fatalf("symlinked library err = %v", err)
	}
	if err := os.Remove(library); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(library, 0o755); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", maxSkillPackageFileSize+1)
	_, err := manager.CreateSkillPackage(ctx, SkillWriteRequest{
		Scope: SkillScopeGlobal, Slug: "large", Name: "Large", Description: "large", Instructions: "body",
		Files: []SkillPackageFile{{Path: "SKILL.md", Content: "---\nname: Large\ndescription: large\n---\n\nbody\n"}, {Path: "assets/large.txt", Content: large}},
	})
	if skillWriteCode(err) != skillWriteErrorInvalid {
		t.Fatalf("oversized file err = %v", err)
	}
}

func TestVaultSkillUpdateVersionConflictAndArchiveReadOnly(t *testing.T) {
	ctx := context.Background()
	config, manager := newSkillWriteVault(t)
	created, err := manager.CreateSkillPackage(ctx, SkillWriteRequest{Scope: SkillScopeProject, ProjectID: "project_alpha", Slug: "alpha-skill", Name: "Alpha", Description: "alpha", Instructions: "body"})
	if err != nil {
		t.Fatal(err)
	}
	conflict := &SkillWriteError{}
	if _, err := manager.UpdateSkillPackage(ctx, SkillWriteRequest{Key: created.Key, BaseVersion: 99, Instructions: "new"}); !errors.As(err, &conflict) || conflict.Code != skillWriteErrorConflict || conflict.CurrentVersion != 1 {
		t.Fatalf("version conflict err = %v", err)
	}

	archive := filepath.Join(config.VaultPath, "archive", "beta", "skills", "old")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archive, "SKILL.md"), []byte("---\nname: Old\ndescription: old\n---\n\nold\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(config.VaultPath, "archive", "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.VaultPath, "archive", "beta", "index.md"), []byte("---\nid: knowledge_root_archive_beta\ntitle: Beta\nscope: archive\nproject_id: project_beta\ntype: navigation\nstatus: verified\nis_index: true\n---\nBeta.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	archiveKey := "vault-skill:archive:project_beta:old"
	if _, err := manager.UpdateSkillPackage(ctx, SkillWriteRequest{Key: archiveKey, Instructions: "new"}); skillWriteCode(err) != skillWriteErrorForbidden {
		t.Fatalf("archive update err = %v", err)
	}
	if _, err := manager.DeleteSkillPackage(ctx, archiveKey); skillWriteCode(err) != skillWriteErrorForbidden {
		t.Fatalf("archive delete err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(archive, "SKILL.md")); err != nil {
		t.Fatalf("archive skill was removed: %v", err)
	}
}
