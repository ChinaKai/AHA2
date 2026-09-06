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

func TestKnowledgeRootsAreAutomaticAndProtected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	global, err := database.EnsureKnowledgeRoot(ctx, "global", "")
	if err != nil {
		t.Fatal(err)
	}
	assertRoot := func(root domain.KnowledgeEntry, scope, projectID string) {
		t.Helper()
		if root.Scope != scope || root.ProjectID != projectID || root.ParentID != "" || root.Slug != "index" || !root.IsIndex || root.Status != domain.KnowledgeVerified || root.ProductLineID != "" {
			t.Fatalf("invalid root: %#v", root)
		}
	}
	assertRoot(global, "global", "")
	for _, projectID := range []string{"project-one", "project-two"} {
		if err := database.CreateProject(ctx, domain.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		root, err := database.EnsureKnowledgeRoot(ctx, "project", projectID)
		if err != nil {
			t.Fatal(err)
		}
		assertRoot(root, "project", projectID)
	}
	root, _ := database.EnsureKnowledgeRoot(ctx, "project", "project-one")
	entry := domain.KnowledgeEntry{
		ID: "entry", Scope: "project", ProjectID: "project-one", Slug: "entry", Type: "practice", Title: "Entry", Body: "Body",
		Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry, err = database.Knowledge(ctx, entry.ID)
	if err != nil || entry.ParentID != root.ID {
		t.Fatalf("default parent entry=%#v err=%v", entry, err)
	}
	entry.ParentID = ""
	entry.Body = "Updated"
	entry.Revision++
	if err := database.UpdateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry, _ = database.Knowledge(ctx, entry.ID)
	if entry.ParentID != root.ID {
		t.Fatalf("updated entry escaped root: %#v", entry)
	}

	root.Title = "Editable home"
	root.Body = "Updated home content."
	root.Revision++
	root.UpdatedAt = now.Add(time.Second)
	if err := database.UpdateKnowledge(ctx, root); err != nil {
		t.Fatalf("root content edit failed: %v", err)
	}
	edited, _ := database.Knowledge(ctx, root.ID)
	if edited.Title != root.Title || edited.Body != root.Body {
		t.Fatalf("root content was not updated: %#v", edited)
	}
	invalid := edited
	invalid.IsIndex = false
	if err := database.UpdateKnowledge(ctx, invalid); !errors.Is(err, ErrKnowledgeRootManaged) {
		t.Fatalf("root converted to document: %v", err)
	}
	invalid = edited
	invalid.Status = domain.KnowledgeStale
	if err := database.UpdateKnowledge(ctx, invalid); !errors.Is(err, ErrKnowledgeRootManaged) {
		t.Fatalf("root status changed: %v", err)
	}
	if err := database.DeleteKnowledge(ctx, root.ID); !errors.Is(err, ErrKnowledgeRootManaged) {
		t.Fatalf("root deletion error=%v", err)
	}
	manualRoot := knowledgeRootDefaults("project", "project-one", now)
	manualRoot.ID = "manual-root"
	if err := database.CreateKnowledge(ctx, manualRoot); !errors.Is(err, ErrKnowledgeRootManaged) {
		t.Fatalf("manual root creation error=%v", err)
	}
	feedback, err := database.FeedbackKnowledge(ctx, root.ID, "stale", timeString(now.Add(2*time.Second)))
	if err != nil || feedback.Status != domain.KnowledgeVerified || feedback.StaleCount != 1 {
		t.Fatalf("root feedback changed verification: %#v err=%v", feedback, err)
	}

	before, _ := database.Knowledge(ctx, root.ID)
	for range 3 {
		ensured, err := database.EnsureKnowledgeRoot(ctx, "project", "project-one")
		if err != nil || ensured.ID != root.ID {
			t.Fatalf("repeat ensure root=%#v err=%v", ensured, err)
		}
	}
	after, _ := database.Knowledge(ctx, root.ID)
	if !before.UpdatedAt.Equal(after.UpdatedAt) || before.Revision != after.Revision {
		t.Fatalf("idempotent ensure rewrote root: before=%#v after=%#v", before, after)
	}
}

func TestKnowledgeRootMigrationAttachesLegacyTopLevelEntries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "legacy-project", Name: "Legacy", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	global, _ := database.EnsureKnowledgeRoot(ctx, "global", "")
	projectRoot, _ := database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if _, err := database.db.ExecContext(ctx, `UPDATE knowledge_entries SET slug='home',status='candidate',product_line_id='legacy-line',last_verified_at='' WHERE id=?`, global.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM knowledge_entries WHERE id=?`, projectRoot.ID); err != nil {
		t.Fatal(err)
	}
	insertLegacy := func(id, scope, projectID string) {
		t.Helper()
		_, err := database.db.ExecContext(ctx, `INSERT INTO knowledge_entries(id,scope,project_id,parent_id,slug,sort_order,is_index,type,title,body,status,branch_scope,evidence_json,confidence,source_task_id,source_turn_id,created_at,updated_at,last_verified_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, scope, projectID, "", id, 0, false, "practice", id, "Legacy body", domain.KnowledgeVerified, "", "{}", 1, "", "", timeString(now), timeString(now), "")
		if err != nil {
			t.Fatal(err)
		}
	}
	insertLegacy("legacy-global", "global", "")
	insertLegacy("legacy-project-entry", "project", project.ID)
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=33`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		scope, projectID, legacyID string
	}{
		{"global", "", "legacy-global"},
		{"project", project.ID, "legacy-project-entry"},
	} {
		root, err := database.EnsureKnowledgeRoot(ctx, test.scope, test.projectID)
		if err != nil || !root.IsIndex || root.Status != domain.KnowledgeVerified || root.Slug != "index" {
			t.Fatalf("migrated root=%#v err=%v", root, err)
		}
		legacy, err := database.Knowledge(ctx, test.legacyID)
		if err != nil || legacy.ParentID != root.ID {
			t.Fatalf("legacy entry=%#v root=%#v err=%v", legacy, root, err)
		}
		var roots int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_entries WHERE scope=? AND project_id=? AND is_index=1`, test.scope, test.projectID).Scan(&roots); err != nil || roots != 1 {
			t.Fatalf("root count=%d err=%v", roots, err)
		}
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("repeat migration failed: %v", err)
	}
	var migrated bool
	if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=33)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v33 missing: migrated=%t err=%v", migrated, err)
	}
}

func TestKnowledgeHierarchyStillRejectsInvalidParentsAndCycles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	for _, id := range []string{"project-one", "project-two"} {
		if err := database.CreateProject(ctx, domain.Project{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	root, _ := database.EnsureKnowledgeRoot(ctx, "project", "project-one")
	entry := func(id, projectID, parentID, slug string) domain.KnowledgeEntry {
		return domain.KnowledgeEntry{ID: id, Scope: "project", ProjectID: projectID, ParentID: parentID, Slug: slug, Type: "practice", Title: id, Body: id, Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now}
	}
	section := entry("section", "project-one", root.ID, "section")
	leaf := entry("leaf", "project-one", section.ID, "leaf")
	if err := database.CreateKnowledge(ctx, section); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateKnowledge(ctx, leaf); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateKnowledge(ctx, entry("duplicate", "project-one", root.ID, "section")); !errors.Is(err, ErrKnowledgeSiblingSlug) {
		t.Fatalf("duplicate sibling error=%v", err)
	}
	if err := database.CreateKnowledge(ctx, entry("invalid", "project-one", root.ID, "Not Valid")); !errors.Is(err, ErrKnowledgeInvalidSlug) {
		t.Fatalf("invalid slug error=%v", err)
	}
	if err := database.CreateKnowledge(ctx, entry("cross-project", "project-two", root.ID, "cross")); !errors.Is(err, ErrKnowledgeParent) {
		t.Fatalf("cross-project parent error=%v", err)
	}
	global, _ := database.EnsureKnowledgeRoot(ctx, "global", "")
	globalChild := domain.KnowledgeEntry{ID: "cross-scope", Scope: "global", ParentID: root.ID, Slug: "cross", Type: "practice", Title: "Cross", Body: "Cross", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateKnowledge(ctx, globalChild); !errors.Is(err, ErrKnowledgeParent) {
		t.Fatalf("cross-scope parent error=%v global=%s", err, global.ID)
	}
	section.ParentID = leaf.ID
	if err := database.UpdateKnowledge(ctx, section); !errors.Is(err, ErrKnowledgeCycle) {
		t.Fatalf("cycle error=%v", err)
	}
	if err := database.DeleteKnowledge(ctx, section.ID); !errors.Is(err, ErrKnowledgeHasChildren) {
		t.Fatalf("parent deletion error=%v", err)
	}
	if err := database.DeleteKnowledge(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing deletion error=%v", err)
	}
}
