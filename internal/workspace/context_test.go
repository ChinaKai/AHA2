package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestMaterializeContextWritesInsideNativeWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "main", "manifest.json")
	err := MaterializeContext(context.Background(), domain.Workspace{Transport: "native"}, root, root, map[string]string{
		target: `{"version":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != `{"version":1}` {
		t.Fatalf("materialized=%q err=%v", data, err)
	}
	if err := materializeLocalContext(root, map[string]string{filepath.Join(filepath.Dir(root), "escape"): "bad"}); err == nil {
		t.Fatal("workspace escape was accepted")
	}
}
