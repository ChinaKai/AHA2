package instancelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireIsExclusiveAndReusableAfterClose(t *testing.T) {
	dataDir := t.TempDir()
	first, err := Acquire(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, FileName)); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
	if _, err := Acquire(dataDir); !errors.Is(err, ErrHeld) {
		t.Fatalf("second Acquire error=%v, want ErrHeld", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(dataDir)
	if err != nil {
		t.Fatalf("Acquire after Close: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}
