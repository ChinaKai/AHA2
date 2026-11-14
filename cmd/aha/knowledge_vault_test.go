package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

func runKnowledgeVaultInit(t *testing.T, args ...string) map[string]any {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	runErr := knowledgeVault(append([]string{"init"}, args...))
	os.Stdout = original
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	if runErr != nil {
		t.Fatalf("knowledge-vault init %v: %v", args, runErr)
	}
	var report map[string]any
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("init output %q is not JSON: %v", output, err)
	}
	return report
}

func TestKnowledgeVaultInitSamplesCreatesFilesAndIndex(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	report := runKnowledgeVaultInit(t, "--data-dir", dataDir, "--samples", "--commit=false")
	samples, _ := report["samples"].([]any)
	if len(samples) != 4 {
		t.Fatalf("samples = %#v, want 4", report["samples"])
	}
	if report["rebuild"] == nil {
		t.Fatalf("init report has no rebuild summary: %#v", report)
	}
	config, err := knowledgevault.DefaultConfig(dataDir).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range knowledgevault.SampleDocuments(time.Now()) {
		path := filepath.Join(config.VaultPath, filepath.FromSlash(sample.Path))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("sample %s missing: %v", sample.Path, err)
		}
	}
	reader, err := knowledgevault.OpenReader(ctx, config, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if !reader.IndexReady() {
		t.Fatal("init --samples did not build a readable index")
	}
	doc, err := reader.Document(ctx, knowledgevault.SampleWelcomeID, "")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title == "" {
		t.Fatal("sample document has no title")
	}

	// A second --samples run reports the skip and leaves the files alone.
	samplePath := filepath.Join(config.VaultPath, filepath.FromSlash(knowledgevault.SampleDocuments(time.Now())[0].Path))
	before, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	second := runKnowledgeVaultInit(t, "--data-dir", dataDir, "--samples", "--commit=false")
	if second["samples_skipped"] == nil {
		t.Fatalf("second init report = %#v, want samples_skipped", second)
	}
	after, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("init --samples overwrote an existing vault")
	}
}

func TestKnowledgeVaultInitWithoutSamplesKeepsTheOldBehavior(t *testing.T) {
	dataDir := t.TempDir()
	report := runKnowledgeVaultInit(t, "--data-dir", dataDir, "--commit=false")
	if report["samples"] != nil || report["rebuild"] != nil {
		t.Fatalf("plain init report = %#v, want no samples and no rebuild", report)
	}
	config, err := knowledgevault.DefaultConfig(dataDir).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	samplePath := filepath.Join(config.VaultPath, filepath.FromSlash(knowledgevault.SampleDocuments(time.Now())[0].Path))
	if _, err := os.Stat(samplePath); !os.IsNotExist(err) {
		t.Fatalf("plain init created a sample file: %v", err)
	}
}
