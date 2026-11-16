package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestLegacyExportAndCutoverToProjectSpace(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{
		ID: "project_legacy_library", Name: "Legacy Library", ProjectType: "knowledge",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, err := database.KnowledgeRootForProject(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []domain.KnowledgeEntry{
		{ID: "knowledge_legacy_one", Scope: "project", ProjectID: project.ID, ParentID: root.ID,
			Slug: "legacy-one", Type: "practice", Title: "旧库说明",
			Body:   "这是旧知识库中的一条说明，迁移后必须原样出现在项目空间。",
			Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now},
		// A navigation page is a schema seed, not knowledge: it must not make
		// the export inventory disagree with the exported ID set.
		{ID: "knowledge_legacy_nav", Scope: "project", ProjectID: project.ID, ParentID: root.ID,
			Slug: "legacy-nav", Type: "navigation", Title: "旧库导航",
			Body:   "旧库导航页正文，不属于实质知识。",
			Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now},
	} {
		if err := database.CreateKnowledge(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}

	config := knowledgevault.DefaultConfig(dataDir)
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "Cutover"}); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	service := NewService(database, nil, nil)
	service.SetKnowledgeVault(manager, true)
	service.SetKnowledgeVaultRuntimeWrite(true)

	exported, err := service.ExportLegacyKnowledgeToProjectSpace(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Exportable != 1 || exported.Written != 1 {
		t.Fatalf("export = %#v", exported)
	}
	rootRelative, ok := knowledgevault.SpaceRootPath(config, project.ID)
	if !ok {
		t.Fatal("project space was not created")
	}
	projectRoot := filepath.Join(config.VaultPath, filepath.Dir(filepath.FromSlash(rootRelative)))
	found := false
	walkErr := filepath.WalkDir(projectRoot, func(filePath string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") || entry.Name() == "index.md" {
			return nil
		}
		raw, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(raw), "旧知识库中的一条说明") {
			found = true
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if !found {
		t.Fatal("legacy entry was not written as a vault file")
	}

	report, err := service.CutoverLegacyKnowledge(ctx, project.ID, filepath.Join(dataDir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applied || report.BackedUpRows == 0 {
		t.Fatalf("cutover report = %#v", report)
	}
	// The running process must fail closed immediately, without a restart.
	if !service.knowledgeLegacyDisabled.Load() {
		t.Fatal("cutover did not switch the running process to fail-closed")
	}
	if decision := service.knowledgeReadDecision("project", "project-unbound-after-cutover"); !decision.Vault || decision.Err == nil {
		t.Fatalf("post-cutover read decision = %#v", decision)
	}
	selection, err := service.channelAllowedKnowledge(ctx, "channel-after-cutover", "group_digital_human", "")
	if err != nil || len(selection) != 0 {
		t.Fatalf("post-cutover unbound channel selection = %#v err=%v", selection, err)
	}
	if _, err := os.Stat(report.BackupPath); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	rows, err := database.LegacyKnowledgeRows(ctx)
	if err != nil || rows != 0 {
		t.Fatalf("legacy rows after cutover = %d err=%v", rows, err)
	}
	if _, err := database.Project(ctx, project.ID); err != nil {
		t.Fatalf("project disappeared: %v", err)
	}
	// The exported document is still searchable after the legacy table drop.
	foundEntry := false
	if err := manager.WithReader(func(reader *knowledgevault.Reader) error {
		entries, err := reader.Entries(ctx, "project", project.ID)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.Contains(entry.Body, "旧知识库中的一条说明") {
				foundEntry = true
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !foundEntry {
		t.Fatal("exported knowledge is not searchable after cutover")
	}
}

func TestCutoverAbortsWhenAnotherLibraryHasUnexportedKnowledge(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	target := domain.Project{ID: "project_target_library", Name: "Target", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	other := domain.Project{ID: "project_other_library", Name: "Other", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	for _, project := range []domain.Project{target, other} {
		if err := database.CreateProject(ctx, project); err != nil {
			t.Fatal(err)
		}
	}
	targetRoot, err := database.KnowledgeRootForProject(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherRoot, err := database.KnowledgeRootForProject(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []domain.KnowledgeEntry{
		{ID: "knowledge_target_doc", Scope: "project", ProjectID: target.ID, ParentID: targetRoot.ID, Slug: "target-doc", Type: "practice", Title: "目标旧知识", Body: "目标库的实质知识正文足够长。", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "knowledge_target_navigation", Scope: "project", ProjectID: target.ID, ParentID: targetRoot.ID, Slug: "target-nav", Type: "navigation", Title: "目标导航", Body: "导航页不是实质知识，不应阻止切黑。", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "knowledge_other_doc", Scope: "project", ProjectID: other.ID, ParentID: otherRoot.ID, Slug: "other-doc", Type: "practice", Title: "另一个库的旧知识", Body: "另一个库的实质知识，不能被误删。", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now},
	} {
		if err := database.CreateKnowledge(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	config := knowledgevault.DefaultConfig(dataDir)
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "Cutover"}); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	service := NewService(database, nil, nil)
	service.SetKnowledgeVault(manager, true)
	service.SetKnowledgeVaultRuntimeWrite(true)

	if _, err := service.CutoverLegacyKnowledge(ctx, target.ID, filepath.Join(dataDir, "backups")); err == nil {
		t.Fatal("cutover dropped tables while another library still had unexported knowledge")
	}
	applied, err := database.LegacyCutoverApplied(ctx)
	if err != nil || applied {
		t.Fatalf("v143 applied after aborted cutover: applied=%t err=%v", applied, err)
	}
	if _, err := database.Knowledge(ctx, "knowledge_other_doc"); err != nil {
		t.Fatalf("other library knowledge disappeared: %v", err)
	}
	inventory, err := database.LegacyKnowledgeInventory(ctx)
	if err != nil || inventory.Entries != 2 {
		t.Fatalf("substantive inventory = %#v err=%v, want 2", inventory, err)
	}
}
