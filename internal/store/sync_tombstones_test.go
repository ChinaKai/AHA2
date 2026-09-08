package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestSharedObjectDeletesPersistVersionedTombstones(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var migrated bool
	if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=37)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v37 missing: migrated=%t err=%v", migrated, err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP TABLE sync_tombstones`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=37`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("upgrade to v37: %v", err)
	}

	now := time.Now().UTC()
	root, _ := database.EnsureKnowledgeRoot(ctx, "global", "")
	knowledge := domain.KnowledgeEntry{ID: "deleted-knowledge", Scope: "global", ParentID: root.ID, Slug: "deleted-knowledge", Type: "practice", Title: "Delete", Body: "Delete", Status: domain.KnowledgeVerified, Revision: 3, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateKnowledge(ctx, knowledge); err != nil {
		t.Fatal(err)
	}
	proposal := domain.KnowledgeProposal{ID: "deleted-proposal", EntryID: "proposal-entry", Proposed: domain.KnowledgeEntry{ID: "proposal-entry", Scope: "global", ParentID: root.ID, Slug: "proposal-entry", Type: "practice", Title: "Proposal", Body: "Body", Revision: 1}, Status: domain.KnowledgeProposalRejected, CreatedAt: now, UpdatedAt: now}
	if err := database.ImportKnowledgeProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	skill := domain.Skill{ID: "deleted-skill", Scope: "global", Name: "Delete skill", Instructions: "Delete", Version: 4, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	provider := domain.Provider{ID: "deleted-provider", Name: "Provider", CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{ID: "deleted-model", DisplayName: "Model", ProviderID: provider.ID, Backend: "codex", WireModel: "model", CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	group := domain.EnvGroup{ID: "deleted-env", Name: "Env", ProviderID: provider.ID, Backend: "codex", Revision: 5, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertPromptTemplateOverride(ctx, "deleted-prompt", "content", now); err != nil {
		t.Fatal(err)
	}

	deletes := []struct {
		objectType, id, version string
		delete                  func() error
	}{
		{"knowledge", knowledge.ID, "3", func() error { return database.DeleteKnowledge(ctx, knowledge.ID) }},
		{"knowledge_proposal", proposal.ID, timeString(now), func() error { return database.DeleteKnowledgeProposal(ctx, proposal.ID) }},
		{"skill", skill.ID, "4", func() error { return database.DeleteSkill(ctx, skill.ID) }},
		{"model", model.ID, timeString(now), func() error { return database.DeleteModel(ctx, model.ID) }},
		{"env_group", group.ID, "5", func() error { return database.DeleteEnvGroup(ctx, group.ID) }},
		{"provider", provider.ID, timeString(now), func() error { return database.DeleteProvider(ctx, provider.ID) }},
		{"prompt_override", "deleted-prompt", "1", func() error { return database.DeletePromptTemplateOverride(ctx, "deleted-prompt") }},
	}
	for _, test := range deletes {
		if err := test.delete(); err != nil {
			t.Fatalf("delete %s/%s: %v", test.objectType, test.id, err)
		}
		tombstone, err := database.SyncTombstone(ctx, test.objectType, test.id)
		if err != nil || tombstone.Version != test.version || tombstone.SyncKey == "" || tombstone.DeletedAt.IsZero() {
			t.Fatalf("tombstone %s/%s=%#v err=%v", test.objectType, test.id, tombstone, err)
		}
	}
	items, err := database.ListSyncTombstones(ctx)
	if err != nil || len(items) != len(deletes) {
		t.Fatalf("tombstones=%#v err=%v", items, err)
	}
	if _, err := database.Knowledge(ctx, knowledge.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("knowledge still exists: %v", err)
	}
	if _, err := database.Model(ctx, model.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("model still exists: %v", err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

func TestApplySyncTombstoneRestrictsRootDeletionToKnowledgeLibraries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()

	project := domain.Project{ID: "project-root-protected", Name: "Protected", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	projectRoot, err := database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ApplySyncTombstone(ctx, "knowledge", projectRoot.ID, "delete-project-root", "1", now); !errors.Is(err, ErrKnowledgeRootManaged) {
		t.Fatalf("ordinary project root delete error=%v", err)
	}
	if _, err := database.Knowledge(ctx, projectRoot.ID); err != nil {
		t.Fatalf("ordinary project root was deleted: %v", err)
	}

	libraryProject := domain.Project{ID: "project-library-root-delete", Name: "Library", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, libraryProject); err != nil {
		t.Fatal(err)
	}
	libraryRoot, err := database.EnsureKnowledgeRoot(ctx, "project", libraryProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ApplySyncTombstone(ctx, "knowledge", libraryRoot.ID, "delete-library-root", "1", now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Knowledge(ctx, libraryRoot.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("knowledge library root survived tombstone: %v", err)
	}

	projectFirst := domain.Project{ID: "project-library-project-first", Name: "Project first", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, projectFirst); err != nil {
		t.Fatal(err)
	}
	projectFirstRoot, err := database.EnsureKnowledgeRoot(ctx, "project", projectFirst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ApplySyncTombstone(ctx, "project", projectFirst.ID, "delete-library-project-first", timeString(now), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Knowledge(ctx, projectFirstRoot.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("project-first library root survived project tombstone: %v", err)
	}
	if _, err := database.ApplySyncTombstone(ctx, "knowledge", projectFirstRoot.ID, "delete-library-root-after-project", "1", now); err != nil {
		t.Fatalf("late library root tombstone failed: %v", err)
	}
}
