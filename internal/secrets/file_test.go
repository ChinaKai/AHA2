package secrets

import (
	"path/filepath"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secrets.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutMany(map[string]string{"env/test/API_KEY": "secret-value"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := reopened.Get("env/test/API_KEY")
	if !ok || value != "secret-value" {
		t.Fatalf("unexpected secret: %q, %v", value, ok)
	}
}
