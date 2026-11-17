//go:build unix

package knowledgevault

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestAutoCommitRejectsNonRegularFilesWithoutBlocking(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.VaultPath, "pipe.bin")
	writeGitTestFile(t, config.VaultPath, "pipe.bin", "regular")
	if _, err := git.Commit(context.Background(), "regular baseline"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan AutoCommitScan, 1)
	go func() {
		scan, _ := git.ScanAutoCommit(ctx)
		done <- scan
	}()
	select {
	case scan := <-done:
		if scan.BlockedTotal != 1 || scan.Blocked[0].Reason != "not_regular" {
			t.Fatalf("FIFO classification = %+v", scan)
		}
		if len(scan.Eligible) != 0 {
			t.Fatalf("FIFO became eligible: %+v", scan.Eligible)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("scan blocked while opening a FIFO")
	}
}
