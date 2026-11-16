package knowledgevault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutoIndexPicksUpExternalFileChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Auto"}); err != nil {
		t.Fatal(err)
	}
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
	defer manager.Close()
	before, _ := autoIndexFingerprint(config.VaultPath)
	manager.StartAutoIndex(ctx, 10*time.Millisecond)

	file := filepath.Join(config.VaultPath, "global", "general", "external.md")
	document := "---\ntitle: 外部编辑\nscope: global\ntype: practice\nstatus: verified\n---\n# 外部编辑\n\n索引应自动收录。\n"
	if err := os.WriteFile(file, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	afterPrint, errPrint := autoIndexFingerprint(config.VaultPath)
	if errPrint != nil || before == afterPrint {
		t.Fatalf("fingerprint did not change: before=%q after=%q err=%v", before, afterPrint, errPrint)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		found := false
		_ = manager.WithReader(func(reader *Reader) error {
			entries, err := reader.Entries(ctx, "global", "")
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.Path == "global/general/external.md" {
					found = true
				}
			}
			return nil
		})
		if found {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	health := manager.AutoIndexHealth()
	t.Fatalf("external file change was not indexed automatically: health=%+v", health)
}

func TestRecoverIndexQuarantinesCorruptIndex(t *testing.T) {
	ctx := context.Background()
	config := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Recover"}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	// Replace the index with garbage after it was opened and closed.
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.IndexPath, []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := manager.RecoverIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Entries == 0 {
		t.Fatalf("recovered index has no entries: %#v", report)
	}
	matches, globErr := filepath.Glob(config.IndexPath + ".corrupt-*")
	if globErr != nil || len(matches) != 1 {
		t.Fatalf("corrupt index was not quarantined: %v %v", matches, globErr)
	}
	health := manager.AutoIndexHealth()
	if health.LastError != "" {
		t.Fatalf("unexpected auto index error: %s", health.LastError)
	}
}
