package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestKnowledgeLibraryMigrationConvertsAHA1ArchiveProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{
		ID: "project-aha1-archive", Name: "Legacy (AHA1 archive)",
		Description: "Imported AHA1 knowledge archive for legacy project legacy-key.",
		ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=38`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	converted, err := database.Project(ctx, project.ID)
	if err != nil || converted.ProjectType != "knowledge" {
		t.Fatalf("archive project was not converted: %#v err=%v", converted, err)
	}
	libraries, err := database.ListKnowledgeLibraries(ctx)
	if err != nil || len(libraries) != 1 || libraries[0].ContainerProjectID != project.ID {
		t.Fatalf("libraries=%#v err=%v", libraries, err)
	}
	var migrated bool
	if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=38)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v38 missing: migrated=%t err=%v", migrated, err)
	}
	if _, err := database.KnowledgeLibrary(ctx, "missing"); err != sql.ErrNoRows {
		t.Fatalf("missing library error=%v", err)
	}
}

func TestSchemaV39PrunesImportedAHA1WorklogsWithTombstones(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-worklog-library", Name: "Library", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, err := database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	directory := domain.KnowledgeEntry{
		ID: "knowledge_aha1_dir_worklog", Scope: "project", ProjectID: project.ID, ParentID: root.ID,
		Slug: "worklog", Type: "practice", Title: "历史工作记录", Body: "AHA1 导入目录：`worklog`。",
		Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}
	worklog := domain.KnowledgeEntry{
		ID: "knowledge_aha1_worklog", Scope: "project", ProjectID: project.ID, ParentID: directory.ID,
		Slug: "task", Type: "task_worklog", Title: "Old worklog", Body: "approval backlog",
		Status: domain.KnowledgeCandidate, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateKnowledge(ctx, directory); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateKnowledge(ctx, worklog); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=39`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{worklog.ID, directory.ID} {
		if _, err := database.Knowledge(ctx, id); err != sql.ErrNoRows {
			t.Fatalf("imported worklog %s survived: %v", id, err)
		}
		if _, err := database.SyncTombstone(ctx, "knowledge", id); err != nil {
			t.Fatalf("worklog tombstone %s missing: %v", id, err)
		}
	}
	if _, err := database.Knowledge(ctx, root.ID); err != nil {
		t.Fatalf("library root was removed: %v", err)
	}
}

func TestKnowledgeLibraryBindUnbindAndProjectDeletePreserveContent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-target", Name: "Target", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	libraryProject := domain.Project{ID: "project-library", Name: "Imported library", Description: "Pending binding", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateProject(ctx, libraryProject); err != nil {
		t.Fatal(err)
	}
	libraries, err := database.ListKnowledgeLibraries(ctx)
	if err != nil || len(libraries) != 1 || libraries[0].ContainerProjectID != libraryProject.ID || libraries[0].BoundProjectID != "" {
		t.Fatalf("libraries=%#v err=%v", libraries, err)
	}
	root, err := database.EnsureKnowledgeRoot(ctx, "project", libraryProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	entry := domain.KnowledgeEntry{
		ID: "knowledge-library-entry", Scope: "project", ProjectID: libraryProject.ID, ParentID: root.ID,
		Slug: "imported", Type: "practice", Title: "Imported", Body: "Imported body",
		Status: domain.KnowledgeVerified, Confidence: 1, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}
	if err := database.CreateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	bound, err := database.BindKnowledgeLibrary(ctx, libraries[0].ID, project.ID, now)
	if err != nil || bound.BoundProjectID != project.ID {
		t.Fatalf("bound=%#v err=%v", bound, err)
	}
	items, err := database.ListApplicableKnowledge(ctx, project.ID, "", []domain.KnowledgeStatus{domain.KnowledgeVerified})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.ID == entry.ID {
			found = item.BoundProjectID == project.ID && item.ParentID == knowledgeRootID("project", project.ID) && item.Slug != entry.Slug
		}
		if item.ID == root.ID {
			t.Fatal("bound library root leaked into applicable project knowledge")
		}
	}
	if !found {
		t.Fatalf("bound knowledge missing or not namespaced: %#v", items)
	}
	if err := database.DeleteProject(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	library, err := database.KnowledgeLibrary(ctx, libraries[0].ID)
	if err != nil || library.BoundProjectID != "" {
		t.Fatalf("library was not retained as pending after project delete: %#v err=%v", library, err)
	}
	if stored, err := database.Knowledge(ctx, entry.ID); err != nil || stored.Body != entry.Body {
		t.Fatalf("library content was deleted with project: %#v err=%v", stored, err)
	}
}

func TestDeleteKnowledgeLibraryRemovesContentProposalsAndSkills(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	container := domain.Project{ID: "project-delete-library", Name: "Delete library", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, container); err != nil {
		t.Fatal(err)
	}
	library, err := database.KnowledgeLibrary(ctx, "library_"+container.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := database.EnsureKnowledgeRoot(ctx, "project", container.ID)
	entry := domain.KnowledgeEntry{ID: "knowledge-delete-library", Scope: "project", ProjectID: container.ID, ParentID: root.ID, Slug: "doc", Type: "practice", Title: "Doc", Body: "Body", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now}
	if err := database.CreateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	proposed := domain.KnowledgeEntry{ID: "knowledge-delete-proposed", Scope: "project", ProjectID: container.ID, ParentID: root.ID, Slug: "proposed", Type: "practice", Title: "Proposed", Body: "Pending", Status: domain.KnowledgeCandidate, Revision: 1, CreatedAt: now, UpdatedAt: now}
	proposal := domain.KnowledgeProposal{ID: "proposal-delete-library", EntryID: proposed.ID, Proposed: proposed, Status: domain.KnowledgeProposalPending, CreatedAt: now, UpdatedAt: now}
	if _, err := database.CreateKnowledgeProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	skill := domain.Skill{ID: "skill-delete-library", PackageSlug: "skill-delete-library", Scope: "project", ProjectID: container.ID, Name: "Skill", Description: "Library skill", Instructions: "Instructions", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	deleted, err := database.DeleteKnowledgeLibrary(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Knowledge != 2 || deleted.Proposals != 1 || deleted.Skills != 1 {
		t.Fatalf("deleted=%#v", deleted)
	}
	for _, check := range []struct {
		name string
		err  error
	}{
		{"entry", func() error { _, err := database.Knowledge(ctx, entry.ID); return err }()},
		{"root", func() error { _, err := database.Knowledge(ctx, root.ID); return err }()},
		{"proposal", func() error { _, err := database.KnowledgeProposal(ctx, proposal.ID); return err }()},
		{"skill", func() error { _, err := database.Skill(ctx, skill.ID); return err }()},
		{"library", func() error { _, err := database.KnowledgeLibrary(ctx, library.ID); return err }()},
		{"container", func() error { _, err := database.Project(ctx, container.ID); return err }()},
	} {
		if check.err != sql.ErrNoRows {
			t.Fatalf("%s survived library deletion: %v", check.name, check.err)
		}
	}
	for _, tombstone := range []struct{ kind, id string }{
		{"knowledge", entry.ID}, {"knowledge", root.ID}, {"knowledge_proposal", proposal.ID},
		{"skill", skill.ID}, {"project", container.ID},
	} {
		if _, err := database.SyncTombstone(ctx, tombstone.kind, tombstone.id); err != nil {
			t.Fatalf("missing %s tombstone for %s: %v", tombstone.kind, tombstone.id, err)
		}
	}
}

func TestDetachAndDeleteProjectOwnedKnowledge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-owned-knowledge", Name: "Owned", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, _ := database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	parent := domain.KnowledgeEntry{ID: "knowledge-owned-parent", Scope: "project", ProjectID: project.ID, ParentID: root.ID, Slug: "parent", Type: "practice", Title: "Parent", Body: "Parent", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now}
	child := domain.KnowledgeEntry{ID: "knowledge-owned-child", Scope: "project", ProjectID: project.ID, ParentID: parent.ID, Slug: "child", Type: "practice", Title: "Child", Body: "Child", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now}
	if err := database.CreateKnowledge(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateKnowledge(ctx, child); err != nil {
		t.Fatal(err)
	}
	skill := domain.Skill{ID: "skill-owned", PackageSlug: "skill-owned", Scope: "project", ProjectID: project.ID, Name: "Owned skill", Instructions: "Instructions", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	library, err := database.DetachProjectKnowledge(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if library.BoundProjectID != "" || library.KnowledgeCount != 2 || library.SkillCount != 1 {
		t.Fatalf("detached library=%#v", library)
	}
	detachedParent, err := database.Knowledge(ctx, parent.ID)
	if err != nil || detachedParent.ProjectID != library.ContainerProjectID || detachedParent.ParentID == root.ID || detachedParent.Revision != 2 {
		t.Fatalf("detached parent=%#v err=%v", detachedParent, err)
	}
	detachedChild, err := database.Knowledge(ctx, child.ID)
	if err != nil || detachedChild.ProjectID != library.ContainerProjectID || detachedChild.ParentID != parent.ID {
		t.Fatalf("detached child=%#v err=%v", detachedChild, err)
	}
	detachedSkill, err := database.Skill(ctx, skill.ID)
	if err != nil || detachedSkill.ProjectID != library.ContainerProjectID {
		t.Fatalf("detached skill=%#v err=%v", detachedSkill, err)
	}
	if items, err := database.ListProjectKnowledge(ctx, project.ID, nil); err != nil || len(items) != 1 || !items[0].IsIndex {
		t.Fatalf("project-owned knowledge was not emptied: %#v err=%v", items, err)
	}
	if _, err := database.BindKnowledgeLibrary(ctx, library.ID, project.ID, now); err != nil {
		t.Fatal(err)
	}
	if items, err := database.ListApplicableKnowledge(ctx, project.ID, "", []domain.KnowledgeStatus{domain.KnowledgeVerified}); err != nil || len(items) != 3 {
		t.Fatalf("rebound project knowledge=%#v err=%v", items, err)
	}
	if _, err := database.UnbindKnowledgeLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DeleteKnowledgeLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Project(ctx, project.ID); err != nil {
		t.Fatalf("project was deleted with detached library: %v", err)
	}

	entry := domain.KnowledgeEntry{ID: "knowledge-owned-delete", Scope: "project", ProjectID: project.ID, ParentID: root.ID, Slug: "delete", Type: "practice", Title: "Delete", Body: "Delete", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now}
	if err := database.CreateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	deleted, err := database.DeleteProjectKnowledge(ctx, project.ID)
	if err != nil || deleted.Knowledge != 1 {
		t.Fatalf("delete project knowledge=%#v err=%v", deleted, err)
	}
	if _, err := database.Knowledge(ctx, root.ID); err != nil {
		t.Fatalf("project root was deleted: %v", err)
	}
}
