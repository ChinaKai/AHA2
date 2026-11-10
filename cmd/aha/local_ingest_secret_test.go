package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/secrets"
)

func TestStoreLocalIngestSecretRoundTrip(t *testing.T) {
	t.Setenv("AHA2_DATA_DIR", "")
	dataDir := t.TempDir()
	secret := strings.Repeat("a", 32)
	var output bytes.Buffer
	if err := storeLocalIngestSecretFrom(strings.NewReader("\n"+secret+"\n"), &output, []string{"--data-dir", dataDir}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("command output echoed the secret")
	}
	store, err := secrets.Open(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	value, ok := store.Get(app.LocalOwnerIngestSecretRef)
	if !ok || value != secret {
		t.Fatalf("stored=%q ok=%v", value, ok)
	}
	output.Reset()
	if err := storeLocalIngestSecretFrom(nil, &output, []string{"--data-dir", dataDir, "--clear"}); err != nil {
		t.Fatal(err)
	}
	store, err = secrets.Open(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(app.LocalOwnerIngestSecretRef); ok {
		t.Fatal("secret was not cleared")
	}
}

func TestStoreLocalIngestSecretRejectsWeakValues(t *testing.T) {
	t.Setenv("AHA2_DATA_DIR", "")
	dataDir := t.TempDir()
	for _, value := range []string{"short", strings.Repeat("a", 16) + " " + strings.Repeat("b", 16)} {
		if err := storeLocalIngestSecretFrom(strings.NewReader(value), &bytes.Buffer{}, []string{"--data-dir", dataDir}); err == nil {
			t.Fatalf("weak secret %q was accepted", value)
		}
	}
	if err := storeLocalIngestSecretFrom(strings.NewReader(strings.Repeat("a", 32)), &bytes.Buffer{}, []string{"--data-dir", dataDir, "extra"}); err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("positional argument error=%v", err)
	}
}
