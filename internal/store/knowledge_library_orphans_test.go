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

func TestKnowledgeLibraryDeleteWithoutContainer(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(map[bool]string{false: "unbound", true: "bound"}[bound], func(t *testing.T) {
			ctx := context.Background()
			database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			now := time.Now().UTC()
			container := domain.Project{ID: "orphan", Name: "Orphan", ProjectType: "knowledge", CreatedAt: now, UpdatedAt: now}
			target := domain.Project{ID: "target", Name: "Target", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
			for _, project := range []domain.Project{container, target} {
				if err := database.CreateProject(ctx, project); err != nil {
					t.Fatal(err)
				}
			}
			libraryID := knowledgeLibraryID(container.ID)
			if bound {
				if _, err := database.BindKnowledgeLibrary(ctx, libraryID, target.ID, "external", now); err != nil {
					t.Fatal(err)
				}
			}
			// Simulate a legacy partial cleanup on one connection, never production data.
			conn, err := database.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{"PRAGMA foreign_keys=OFF", "DELETE FROM knowledge_entries WHERE project_id='orphan'", "DELETE FROM projects WHERE id='orphan'", "PRAGMA foreign_keys=ON"} {
				if _, err := conn.ExecContext(ctx, query); err != nil {
					conn.Close()
					t.Fatal(err)
				}
			}
			conn.Close()
			if _, err := database.Project(ctx, container.ID); err != sql.ErrNoRows {
				t.Fatalf("container error=%v", err)
			}
			if _, err := database.KnowledgeLibrary(ctx, libraryID); err != nil {
				t.Fatal(err)
			}
			deleted, err := database.DeleteKnowledgeLibrary(ctx, libraryID)
			if err != nil || deleted != (domain.KnowledgeLibraryDeletion{}) {
				t.Fatalf("deleted=%#v err=%v", deleted, err)
			}
			if _, err := database.KnowledgeLibrary(ctx, libraryID); err != sql.ErrNoRows {
				t.Fatalf("library survived: %v", err)
			}
			if _, err := database.KnowledgeLibraryBinding(ctx, libraryID); err != sql.ErrNoRows {
				t.Fatalf("binding survived: %v", err)
			}
			if bound {
				if _, err := database.SyncTombstone(ctx, "knowledge_binding", libraryID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := database.Project(ctx, target.ID); err != nil {
				t.Fatalf("unrelated project removed: %v", err)
			}
			if _, err := database.DeleteKnowledgeLibrary(ctx, libraryID); !errors.Is(err, ErrKnowledgeLibraryNotFound) {
				t.Fatalf("repeat delete error=%v", err)
			}
		})
	}
}

func TestKnowledgeLibraryDeleteWithoutRoot(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	container := domain.Project{ID: "partial", Name: "Partial", ProjectType: "knowledge", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, container); err != nil {
		t.Fatal(err)
	}
	root, err := database.EnsureKnowledgeRoot(ctx, "project", container.ID)
	if err != nil {
		t.Fatal(err)
	}
	entry := domain.KnowledgeEntry{ID: "partial-entry", Scope: "project", ProjectID: container.ID, ParentID: root.ID, Slug: "doc", Type: "practice", Title: "Doc", Body: "Body", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now}
	if err := database.CreateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	proposal := domain.KnowledgeProposal{ID: "partial-proposal", EntryID: "proposed", Proposed: domain.KnowledgeEntry{ID: "proposed", Scope: "project", ProjectID: container.ID, ParentID: root.ID, Slug: "new", Type: "practice", Title: "New", Body: "Body", Status: domain.KnowledgeCandidate, Revision: 1, CreatedAt: now, UpdatedAt: now}, Status: domain.KnowledgeProposalPending, CreatedAt: now, UpdatedAt: now}
	if _, err := database.CreateKnowledgeProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	skill := domain.Skill{ID: "partial-skill", PackageSlug: "partial-skill", Scope: "project", ProjectID: container.ID, Name: "Skill", Instructions: "Instructions", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM knowledge_entries WHERE id=?`, root.ID); err != nil {
		t.Fatal(err)
	}
	deleted, err := database.DeleteKnowledgeLibrary(ctx, knowledgeLibraryID(container.ID))
	if err != nil || deleted.Knowledge != 1 || deleted.Proposals != 1 || deleted.Skills != 1 {
		t.Fatalf("deleted=%#v err=%v", deleted, err)
	}
	for _, check := range []struct {
		name string
		err  error
	}{
		{"entry", func() error { _, err := database.Knowledge(ctx, entry.ID); return err }()},
		{"proposal", func() error { _, err := database.KnowledgeProposal(ctx, proposal.ID); return err }()},
		{"skill", func() error { _, err := database.Skill(ctx, skill.ID); return err }()},
		{"container", func() error { _, err := database.Project(ctx, container.ID); return err }()},
		{"library", func() error { _, err := database.KnowledgeLibrary(ctx, knowledgeLibraryID(container.ID)); return err }()},
	} {
		if check.err != sql.ErrNoRows {
			t.Fatalf("%s survived: %v", check.name, check.err)
		}
	}
	if _, err := database.SyncTombstone(ctx, "knowledge", root.ID); err != sql.ErrNoRows {
		t.Fatalf("missing root counted as deleted: %v", err)
	}
}

func TestKnowledgeLibraryDeleteWithoutRootPreservesSafetyChecks(t *testing.T) {
	for _, reference := range []string{"tasks", "workspaces"} {
		t.Run(reference, func(t *testing.T) {
			ctx := context.Background()
			database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			now := time.Now().UTC()
			container := domain.Project{ID: "protected", Name: "Protected", ProjectType: "knowledge", CreatedAt: now, UpdatedAt: now}
			if err := database.CreateProject(ctx, container); err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(ctx, `DELETE FROM knowledge_entries WHERE project_id=? AND is_index=1`, container.ID); err != nil {
				t.Fatal(err)
			}
			if reference == "tasks" {
				taskID := attachmentTestTask(t, database)
				if _, err := database.db.ExecContext(ctx, `UPDATE tasks SET project_id=? WHERE id=?`, container.ID, taskID); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := database.CreateProject(ctx, domain.Project{ID: "project-attachment", Name: "Target", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
				if err := database.CreateWorkspace(ctx, domain.Workspace{ID: "protected-workspace", ProjectID: container.ID, Name: "Workspace", Locality: "local", Transport: "native", RootPath: t.TempDir(), CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
			}
			libraryID := knowledgeLibraryID(container.ID)
			if _, err := database.BindKnowledgeLibrary(ctx, libraryID, "project-attachment", "external", now); err != nil {
				t.Fatal(err)
			}
			deleted, err := database.DeleteKnowledgeLibrary(ctx, libraryID)
			if !errors.Is(err, ErrKnowledgeLibraryInUse) || deleted != (domain.KnowledgeLibraryDeletion{}) {
				t.Fatalf("deleted=%#v err=%v", deleted, err)
			}
			if _, err := database.Project(ctx, container.ID); err != nil {
				t.Fatalf("container removed: %v", err)
			}
			if _, err := database.KnowledgeLibrary(ctx, libraryID); err != nil {
				t.Fatalf("library removed: %v", err)
			}
			if _, err := database.KnowledgeLibraryBinding(ctx, libraryID); err != nil {
				t.Fatalf("binding removed: %v", err)
			}
			if reference == "tasks" {
				if _, err := database.Task(ctx, "task-attachment"); err != nil {
					t.Fatalf("task removed: %v", err)
				}
			} else if _, err := database.Workspace(ctx, "protected-workspace"); err != nil {
				t.Fatalf("workspace removed: %v", err)
			}
		})
	}
}

func TestKnowledgeLibraryProjectDeletionCascadesLibraryAndBinding(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	for _, project := range []domain.Project{
		{ID: "container", Name: "Container", ProjectType: "knowledge", CreatedAt: now, UpdatedAt: now},
		{ID: "target", Name: "Target", ProjectType: "folder", CreatedAt: now, UpdatedAt: now},
	} {
		if err := database.CreateProject(ctx, project); err != nil {
			t.Fatal(err)
		}
	}
	libraryID := knowledgeLibraryID("container")
	if _, err := database.BindKnowledgeLibrary(ctx, libraryID, "target", "external", now); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteProject(ctx, "container"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.KnowledgeLibrary(ctx, libraryID); err != sql.ErrNoRows {
		t.Fatalf("library survived project deletion: %v", err)
	}
	if _, err := database.KnowledgeLibraryBinding(ctx, libraryID); err != sql.ErrNoRows {
		t.Fatalf("binding survived project deletion: %v", err)
	}
	if _, err := database.Project(ctx, "target"); err != nil {
		t.Fatal(err)
	}
}
