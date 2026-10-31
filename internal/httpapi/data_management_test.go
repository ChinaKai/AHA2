package httpapi

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDataInventoryCountsKnownCategories(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "aha2.db"), 120)
	writeTestFile(t, filepath.Join(root, "aha2.db-wal"), 30)
	writeTestFile(t, filepath.Join(root, "attachments", "a.bin"), 40)
	writeTestFile(t, filepath.Join(root, "backups", "user-installed-1", "aha2.exe"), 70)
	writeTestFile(t, filepath.Join(root, "pet-workspace", "core", "identity.md"), 9)
	writeTestFile(t, filepath.Join(root, "install.log"), 12)
	writeTestFile(t, filepath.Join(root, "unknown.bin"), 5)

	inventory, err := buildDataInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]int64{}
	for _, category := range inventory.Categories {
		byID[category.ID] = category.Bytes
	}
	if byID["database"] != 150 {
		t.Fatalf("database bytes=%d", byID["database"])
	}
	if byID["attachments"] != 40 || byID["backups"] != 70 || byID["pet-workspace"] != 9 {
		t.Fatalf("categories=%#v", byID)
	}
	if byID["logs"] != 12 {
		t.Fatalf("logs bytes=%d", byID["logs"])
	}
	if byID["other"] != 5 {
		t.Fatalf("other bytes=%d", byID["other"])
	}
	if inventory.TotalBytes != 286 {
		t.Fatalf("total=%d", inventory.TotalBytes)
	}
}

func TestCleanupDataKeepsRecentBackupsAndOnlyTouchesSafeFiles(t *testing.T) {
	root := t.TempDir()
	backupRoot := filepath.Join(root, "backups")
	base := time.Now().Add(-48 * time.Hour)
	for index, name := range []string{"user-installed-1", "user-installed-2", "user-installed-3"} {
		path := filepath.Join(backupRoot, name, "aha2.exe")
		writeTestFile(t, path, 100)
		modTime := base.Add(time.Duration(index) * time.Hour)
		if err := os.Chtimes(filepath.Join(backupRoot, name), modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(backupRoot, "keep-me", "data.txt"), 10)
	writeTestFile(t, filepath.Join(root, "install.log"), 25)
	writeTestFile(t, filepath.Join(root, "aha2.db"), 50)
	writeTestFile(t, filepath.Join(root, "attachments", "a.bin"), 40)

	result, err := cleanupDataDir(root, "backups", 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 1 || result.FreedBytes != 100 {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(filepath.Join(backupRoot, "user-installed-1")); !os.IsNotExist(err) {
		t.Fatalf("oldest backup still exists: %v", err)
	}
	for _, name := range []string{"user-installed-2", "user-installed-3", "keep-me"} {
		if _, err := os.Stat(filepath.Join(backupRoot, name)); err != nil {
			t.Fatalf("backup %s missing: %v", name, err)
		}
	}

	logResult, err := cleanupDataDir(root, "logs", 0)
	if err != nil {
		t.Fatal(err)
	}
	if logResult.Removed != 1 || logResult.FreedBytes != 25 {
		t.Fatalf("log result=%#v", logResult)
	}
	if _, err := os.Stat(filepath.Join(root, "install.log")); !os.IsNotExist(err) {
		t.Fatalf("log still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "aha2.db")); err != nil {
		t.Fatalf("database should be preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "attachments", "a.bin")); err != nil {
		t.Fatalf("attachments should be preserved: %v", err)
	}
	if _, err := cleanupDataDir(root, "attachments", 0); err == nil {
		t.Fatal("expected protected category cleanup to be rejected")
	}
}
