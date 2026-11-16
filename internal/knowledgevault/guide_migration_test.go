package knowledgevault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGuideMovesFromLegacyPathToVaultRoot(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Guide"}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(config.VaultPath, filepath.FromSlash(GuideRelativePath))
	legacy := filepath.Join(config.VaultPath, filepath.FromSlash(LegacyGuideRelativePath))
	custom := "---\ntitle: 自定义指南\nslug: knowledge-base-guide\nscope: global\ntype: navigation\nstatus: verified\n---\n\n# 自定义指南\n\n手写正文。\n"
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	// Reads stay compatible while only the old path exists, but report the
	// canonical root path.
	guide, err := ReadGuide(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if guide.Path != GuideRelativePath || guide.Title != "自定义指南" {
		t.Fatalf("legacy read guide = %#v", guide)
	}
	// EnsureGuide performs the one-time move and is idempotent.
	migrated, err := EnsureGuide(config.VaultPath, time.Now().UTC())
	if err != nil || !migrated {
		t.Fatalf("migrate = %t err=%v", migrated, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy guide still exists: %v", err)
	}
	raw, err := os.ReadFile(root)
	if err != nil || string(raw) != custom {
		t.Fatalf("root guide = %q err=%v", string(raw), err)
	}
	migrated, err = EnsureGuide(config.VaultPath, time.Now().UTC())
	if err != nil || migrated {
		t.Fatalf("second EnsureGuide = %t err=%v", migrated, err)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	reserved := false
	for _, file := range manifest.ReservedFiles {
		if file == GuideRelativePath {
			reserved = true
		}
	}
	if !reserved {
		t.Fatalf("manifest reserved_files missing %s: %#v", GuideRelativePath, manifest.ReservedFiles)
	}
}

func TestGuideIndexedAsGlobalNavigationAtRoot(t *testing.T) {
	ctx := context.Background()
	config := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Guide"}); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	entries, err := index.Entries(ctx, "global", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Path != GuideRelativePath {
			continue
		}
		found = true
		if entry.Scope != "global" || entry.Type != TypeNavigation || entry.ProjectID != "" {
			t.Fatalf("root guide entry = %#v", entry)
		}
	}
	if !found {
		t.Fatal("root guide is not indexed as a global navigation page")
	}
}
