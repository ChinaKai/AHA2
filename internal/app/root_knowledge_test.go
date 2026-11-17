package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestTaskKnowledgeSwitchRetiresInherit(t *testing.T) {
	if got := normalizeKnowledgePolicy(""); got != "enabled" {
		t.Fatalf("default policy = %q", got)
	}
	if got := normalizeKnowledgePolicy("inherit"); got != "enabled" {
		t.Fatalf("inherit policy = %q", got)
	}
	if got := normalizeKnowledgePolicy("disabled"); got != "disabled" {
		t.Fatalf("disabled policy = %q", got)
	}
	projectDisabled := domain.Project{KnowledgePolicy: "disabled"}
	projectEnabled := domain.Project{KnowledgePolicy: "enabled"}
	cases := []struct {
		policy string
		want   bool
	}{
		{"", true},
		{"enabled", true},
		{"inherit", true},
		{"disabled", false},
		{"unexpected-legacy-value", true},
	}
	for _, testCase := range cases {
		if got := knowledgeEnabled(projectDisabled, domain.Task{KnowledgePolicy: testCase.policy}); got != testCase.want {
			t.Fatalf("project-disabled task policy %q = %v want %v", testCase.policy, got, testCase.want)
		}
		if got := knowledgeEnabled(projectEnabled, domain.Task{KnowledgePolicy: testCase.policy}); got != testCase.want {
			t.Fatalf("project-enabled task policy %q = %v want %v", testCase.policy, got, testCase.want)
		}
	}
}

func TestPetRootKnowledgeSnapshotAndTaskScope(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	if err := database.CreateOwner(ctx, domain.Owner{ID: "owner-root", Username: "owner", PasswordHash: "hash", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	manager := buildVaultReadManager(t, true, map[string]string{
		"notes/doc.md":             "---\nid: knowledge_root_note\ntitle: Root note\nscope: root\ntype: reference\nstatus: verified\n---\nRoot body.\n",
		"projects/pet/index.md":    "---\nid: knowledge_root_project_pet\ntitle: 宠物项目\nscope: project\nproject_id: project_pet\ntype: navigation\nstatus: verified\nis_index: true\n---\nPet.\n",
		"global/index.md":          "---\nid: knowledge_root_global\ntitle: 全局首页\nscope: global\ntype: navigation\nstatus: verified\nis_index: true\n---\nGlobal.\n",
		"global/general/shared.md": "---\nid: knowledge_shared\ntitle: 共享\nscope: global\ntype: reference\nstatus: verified\n---\nShared.\n",
	})
	service := &Service{store: database, knowledgeVault: manager, knowledgeVaultRuntimeRead: true}

	active, stale, err := service.vaultRootSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("root stale entries = %#v", stale)
	}
	found := false
	for _, entry := range active {
		if entry.ID == "knowledge_root_note" {
			found = true
			if entry.Scope != "root" || entry.ProjectID != "" {
				t.Fatalf("root entry shape = %#v", entry)
			}
		}
	}
	if !found {
		t.Fatalf("root snapshot = %#v", active)
	}
	if !service.isPetKnowledgeTask(ctx, domain.Task{ID: petTaskID, ProjectID: petProjectID}) {
		t.Fatal("pet Task was not recognized as a root-readable Task")
	}
	if service.isPetKnowledgeTask(ctx, domain.Task{ID: "task-normal", ProjectID: "project-normal"}) {
		t.Fatal("an ordinary Task was treated as the pet Task")
	}
}
