package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
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
	reader, err := knowledgevault.OpenReader(ctx, config)
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

func TestKnowledgeVaultMigrateLegacyRefusesExistingVaultContent(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.EnsureGlobalKnowledgeCategories(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := database.CreateKnowledge(ctx, domain.KnowledgeEntry{
		ID: "knowledge_global_general_cli_legacy", Scope: "global",
		ParentID: store.GlobalGeneralKnowledgeID, Slug: "cli-legacy", Type: "practice",
		Title: "旧知识", Body: "CLI 迁移不应写入这条内容。", Status: domain.KnowledgeVerified,
		Confidence: 1, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	config, err := knowledgevault.DefaultConfig(dataDir).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	pagePath := filepath.Join(config.VaultPath, "global", "general", "cli-owner-edit.md")
	ownerContent := []byte("CLI owner page that must survive.\n")
	if err := os.WriteFile(pagePath, ownerContent, 0o644); err != nil {
		t.Fatal(err)
	}
	err = knowledgeVault([]string{
		"migrate-legacy", "--data-dir", dataDir,
		"--vault", config.VaultPath, "--index", config.IndexPath, "--commit=false",
	})
	if !errors.Is(err, knowledgevault.ErrTargetNotEmpty) {
		t.Fatalf("CLI migrate-legacy over a populated vault = %v, want ErrTargetNotEmpty", err)
	}
	after, err := os.ReadFile(pagePath)
	if err != nil || string(after) != string(ownerContent) {
		t.Fatalf("CLI refused migration changed the page: %q err=%v", after, err)
	}
	inventory, err := database.LegacyKnowledgeInventory(ctx)
	if err != nil || inventory.Entries != 1 {
		t.Fatalf("CLI refused migration changed legacy data: %+v err=%v", inventory, err)
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
