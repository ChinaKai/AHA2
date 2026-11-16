package knowledgevault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureProjectSpaceCreatesManifestRootAndIndex(t *testing.T) {
	ctx := context.Background()
	config := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Spaces"}); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	result, err := EnsureProjectSpace(config, "project-one", "Alpha Project", stamp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Slug != "alpha-project" || !result.Created {
		t.Fatalf("result = %#v", result)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Projects["project-one"] != "alpha-project" || manifest.ProjectNames["project-one"] != "Alpha Project" {
		t.Fatalf("manifest = %#v", manifest.Projects)
	}
	raw, err := os.ReadFile(filepath.Join(config.VaultPath, "projects", "alpha-project", "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	document := ParseDocument(string(raw))
	if document.Frontmatter.Scope != "project" || document.Frontmatter.ProjectID != "project-one" ||
		document.Frontmatter.IsIndex == nil || !*document.Frontmatter.IsIndex {
		t.Fatalf("root frontmatter = %#v", document.Frontmatter)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	entries, err := index.Entries(ctx, "project", "project-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].IsIndex {
		t.Fatalf("entries = %#v", entries)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}

	// A second call is an idempotent repair and keeps the existing slug even
	// when the project was renamed.
	result, err = EnsureProjectSpace(config, "project-one", "Renamed Project", stamp.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Created || result.Slug != "alpha-project" {
		t.Fatalf("second result = %#v", result)
	}
	manifest, err = ReadManifest(config.VaultPath)
	if err != nil || manifest.ProjectNames["project-one"] != "Renamed Project" {
		t.Fatalf("rename not recorded: %#v err=%v", manifest.ProjectNames, err)
	}
}

func TestEnsureProjectSpaceUsesStableSlugSuffixOnConflict(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Spaces"}); err != nil {
		t.Fatal(err)
	}
	first, err := EnsureProjectSpace(config, "project-first", "Same Name", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureProjectSpace(config, "project-second", "Same Name", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if first.Slug == second.Slug {
		t.Fatalf("conflicting names shared slug %q", first.Slug)
	}
	if !strings.HasPrefix(second.Slug, "same-name-") {
		t.Fatalf("second slug = %q, want a stable suffix", second.Slug)
	}
	// A manual directory without a manifest mapping is reserved too.
	manual := filepath.Join(config.VaultPath, "projects", "manual-only")
	if err := os.MkdirAll(manual, 0o755); err != nil {
		t.Fatal(err)
	}
	third, err := EnsureProjectSpace(config, "project-third", "Manual Only", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if third.Slug == "manual-only" {
		t.Fatal("space reused a manual directory that has no manifest mapping")
	}
}

func TestCheckProjectSpacePrerequisitesRequiresInitializedWritableVault(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	if err := CheckProjectSpacePrerequisites(config); err == nil {
		t.Fatal("missing vault passed the prerequisite check")
	}
	if _, err := Bootstrap(config, InitOptions{VaultName: "Spaces"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckProjectSpacePrerequisites(config); err != nil {
		t.Fatalf("initialized vault failed the prerequisite check: %v", err)
	}
}
