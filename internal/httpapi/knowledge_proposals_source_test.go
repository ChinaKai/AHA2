package httpapi

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

func buildProposalSourceVault(t *testing.T) *knowledgevault.Manager {
	t.Helper()
	ctx := context.Background()
	config := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "Proposals"}); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"global/index.md":      "---\nid: knowledge_root_global\ntitle: Global\nscope: global\ntype: navigation\nstatus: verified\nis_index: true\n---\nGlobal.\n",
		"projects/pv/index.md": "---\nid: knowledge_root_project_http_vault\ntitle: Vault Project\nscope: project\nproject_id: project_http_vault\ntype: navigation\nstatus: verified\nis_index: true\n---\nProject.\n",
	}
	for relative, content := range files {
		target := filepath.Join(config.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index, err := knowledgevault.OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func TestKnowledgeProposalListsHideLegacyRowsForVaultProject(t *testing.T) {
	previous, existed := os.LookupEnv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS")
	if err := os.Unsetenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS", previous)
		} else {
			_ = os.Unsetenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS")
		}
	})
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project_http_vault", Name: "Vault", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, err := database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyEntry := domain.KnowledgeEntry{
		ID: "knowledge_http_vault_legacy", Scope: "project", ProjectID: project.ID, ParentID: root.ID,
		Slug: "legacy", Type: "practice", Title: "Legacy", Body: "legacy body",
		Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateKnowledge(ctx, legacyEntry); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateKnowledgeProposal(ctx, domain.KnowledgeProposal{
		ID: "proposal-http-legacy", EntryID: legacyEntry.ID, BaseRevision: legacyEntry.Revision, Proposed: legacyEntry,
		Status: domain.KnowledgeProposalPending, ReviewMode: "manual", TargetSource: domain.KnowledgeProposalTargetLegacy,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	vaultEntry := domain.KnowledgeEntry{
		ID: "knowledge_http_vault_new", Scope: "project", ProjectID: project.ID,
		Type: "practice", Title: "Vault", Body: "vault body", Status: domain.KnowledgeCandidate, Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := database.CreateKnowledgeProposal(ctx, domain.KnowledgeProposal{
		ID: "proposal-http-vault", EntryID: vaultEntry.ID, Proposed: vaultEntry,
		Status: domain.KnowledgeProposalPending, ReviewMode: "manual", TargetSource: domain.KnowledgeProposalTargetVault,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	appService := app.NewService(database, nil, app.StubExecutor{})
	appService.SetKnowledgeVault(buildProposalSourceVault(t), true)
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	defer server.Close()
	client := newCookieClient(t)
	registerOwner(t, client, server.URL)

	type proposalPayload struct {
		Proposals []domain.KnowledgeProposal `json:"proposals"`
		Total     int                        `json:"total"`
	}
	check := func(path string) {
		t.Helper()
		response, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var payload proposalPayload
		decodeResponse(t, response, &payload)
		if len(payload.Proposals) != 1 || payload.Proposals[0].ID != "proposal-http-vault" {
			t.Fatalf("%s exposed mixed proposals: %#v", path, payload)
		}
	}
	check("/api/v1/knowledge/proposals?status=pending&limit=0")
	check("/api/v1/knowledge?scope=project&project_id=" + project.ID)
}
