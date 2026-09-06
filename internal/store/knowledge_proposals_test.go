package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func proposalTestStore(t *testing.T) (*Store, context.Context, domain.Project, domain.KnowledgeEntry) {
	t.Helper()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	project := domain.Project{ID: "proposal-project", Name: "Proposal", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, err := database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return database, ctx, project, root
}

func pendingProposal(id string, entry domain.KnowledgeEntry, baseRevision int, now time.Time) domain.KnowledgeProposal {
	return domain.KnowledgeProposal{
		ID: id, EntryID: entry.ID, BaseRevision: baseRevision, Proposed: entry,
		SourceTaskID: "task-source", SourceTurnID: "turn-source", Status: domain.KnowledgeProposalPending,
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestKnowledgeProposalMigrationAndNewEntryApproval(t *testing.T) {
	t.Parallel()
	database, ctx, project, root := proposalTestStore(t)
	var migrated bool
	if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=34)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v34 missing: migrated=%t err=%v", migrated, err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP TABLE knowledge_proposals`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=34`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("migrate schema v33 to v34: %v", err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=34)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v34 was not restored: migrated=%t err=%v", migrated, err)
	}
	now := time.Now().UTC()
	proposed := domain.KnowledgeEntry{
		ID: "knowledge-new", Scope: "project", ProjectID: project.ID, Slug: "new-entry", Type: "practice",
		Title: "New entry", Body: "Pending content", Confidence: .99, Status: domain.KnowledgeVerified,
		EvidenceJSON: `[{"source":"test"}]`, CreatedAt: now, UpdatedAt: now,
	}
	created, err := database.CreateKnowledgeProposal(ctx, pendingProposal("proposal-new", proposed, 0, now))
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != domain.KnowledgeProposalPending || created.Proposed.Status != domain.KnowledgeCandidate || created.Proposed.ParentID != root.ID || created.Proposed.Revision != 1 || created.Proposed.EvidenceJSON != proposed.EvidenceJSON {
		t.Fatalf("created proposal=%#v", created)
	}
	if _, err := database.Knowledge(ctx, proposed.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pending proposal published early: %v", err)
	}
	items, err := database.ListKnowledgeProposals(ctx, "project", project.ID)
	if err != nil || len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("proposals=%#v err=%v", items, err)
	}
	approved, entry, err := database.ApproveKnowledgeProposal(ctx, created.ID, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != domain.KnowledgeProposalApproved || approved.DecidedAt.IsZero() || entry.Status != domain.KnowledgeVerified || entry.Revision != 1 || entry.ParentID != root.ID || entry.FeedbackState != "" {
		t.Fatalf("approved=%#v entry=%#v", approved, entry)
	}
	if _, _, err := database.ApproveKnowledgeProposal(ctx, created.ID, now.Add(2*time.Second)); !errors.Is(err, ErrKnowledgeProposalResolved) {
		t.Fatalf("repeat approval error=%v", err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=34`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("repeat v34 migration failed: %v", err)
	}
	items, err = database.ListKnowledgeProposals(ctx, "project", project.ID)
	if err != nil || len(items) != 1 || items[0].Status != domain.KnowledgeProposalApproved {
		t.Fatalf("migration did not preserve proposals: %#v err=%v", items, err)
	}
}

func TestKnowledgeRevisionProposalStalesThenPublishesExactRevision(t *testing.T) {
	t.Parallel()
	database, ctx, project, root := proposalTestStore(t)
	now := time.Now().UTC()
	existing := domain.KnowledgeEntry{
		ID: "knowledge-existing", Scope: "project", ProjectID: project.ID, ParentID: root.ID, Slug: "existing", Type: "practice",
		Title: "Existing", Body: "Old body", Status: domain.KnowledgeVerified, Revision: 3, HelpedCount: 4, StaleCount: 2,
		FeedbackState: "helped", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), LastVerifiedAt: now.Add(-time.Hour),
	}
	if err := database.CreateKnowledge(ctx, existing); err != nil {
		t.Fatal(err)
	}
	proposed := existing
	proposed.Title, proposed.Body, proposed.Slug = "Revised", "New body", "revised"
	created, err := database.CreateKnowledgeProposal(ctx, pendingProposal("proposal-existing", proposed, existing.Revision, now))
	if err != nil {
		t.Fatal(err)
	}
	if created.BaseEntry == nil || created.BaseEntry.Title != existing.Title || created.BaseEntry.Body != existing.Body || created.BaseEntry.Revision != existing.Revision {
		t.Fatalf("proposal did not preserve base snapshot: %#v", created.BaseEntry)
	}
	stale, err := database.Knowledge(ctx, existing.ID)
	if err != nil || stale.Status != domain.KnowledgeStale || stale.Revision != existing.Revision {
		t.Fatalf("old entry was not staled: %#v err=%v", stale, err)
	}
	applicable, err := database.ListApplicableKnowledge(ctx, project.ID, "", []domain.KnowledgeStatus{domain.KnowledgeVerified})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range applicable {
		if entry.ID == existing.ID {
			t.Fatal("stale entry remained applicable")
		}
	}
	stale.Status = domain.KnowledgeVerified
	if err := database.UpdateKnowledge(ctx, stale); !errors.Is(err, ErrKnowledgeProposalPending) {
		t.Fatalf("pending revision was re-verified directly: %v", err)
	}
	if err := database.VerifyKnowledge(ctx, existing.ID, timeString(now.Add(time.Second))); !errors.Is(err, ErrKnowledgeProposalPending) {
		t.Fatalf("pending revision was verified through helper: %v", err)
	}
	if err := database.DeleteKnowledge(ctx, existing.ID); !errors.Is(err, ErrKnowledgeProposalPending) {
		t.Fatalf("entry with pending proposal was deleted: %v", err)
	}
	duplicate := pendingProposal("proposal-duplicate", proposed, existing.Revision, now.Add(time.Millisecond))
	if _, err := database.CreateKnowledgeProposal(ctx, duplicate); !errors.Is(err, ErrKnowledgeProposalPending) {
		t.Fatalf("duplicate pending proposal error=%v", err)
	}
	_, published, err := database.ApproveKnowledgeProposal(ctx, created.ID, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if published.Status != domain.KnowledgeVerified || published.Revision != existing.Revision+1 || published.Title != proposed.Title || published.Body != proposed.Body || published.HelpedCount != existing.HelpedCount || published.StaleCount != existing.StaleCount || published.FeedbackState != "" {
		t.Fatalf("published revision=%#v", published)
	}
	approved, err := database.KnowledgeProposal(ctx, created.ID)
	if err != nil || approved.BaseEntry == nil || approved.BaseEntry.Body != existing.Body {
		t.Fatalf("approved proposal lost base snapshot: %#v err=%v", approved, err)
	}
}

func TestKnowledgeProposalRejectAndRevisionConflictKeepEntryStale(t *testing.T) {
	t.Parallel()
	database, ctx, project, root := proposalTestStore(t)
	now := time.Now().UTC()
	existing := domain.KnowledgeEntry{
		ID: "knowledge-reject", Scope: "project", ProjectID: project.ID, ParentID: root.ID, Slug: "reject", Type: "practice",
		Title: "Reject", Body: "Old", Status: domain.KnowledgeVerified, Revision: 2, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateKnowledge(ctx, existing); err != nil {
		t.Fatal(err)
	}
	proposed := existing
	proposed.Body = "Rejected body"
	created, err := database.CreateKnowledgeProposal(ctx, pendingProposal("proposal-reject", proposed, existing.Revision, now))
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := database.RejectKnowledgeProposal(ctx, created.ID, now.Add(time.Second))
	if err != nil || rejected.Status != domain.KnowledgeProposalRejected || rejected.DecidedAt.IsZero() {
		t.Fatalf("rejected=%#v err=%v", rejected, err)
	}
	stale, _ := database.Knowledge(ctx, existing.ID)
	if stale.Status != domain.KnowledgeStale || stale.Body != existing.Body || stale.Revision != existing.Revision {
		t.Fatalf("rejection changed old entry: %#v", stale)
	}
	if _, err := database.RejectKnowledgeProposal(ctx, created.ID, now.Add(2*time.Second)); !errors.Is(err, ErrKnowledgeProposalResolved) {
		t.Fatalf("repeat rejection error=%v", err)
	}

	stale.Status = domain.KnowledgeVerified
	if err := database.UpdateKnowledge(ctx, stale); err != nil {
		t.Fatal(err)
	}
	conflictProposal, err := database.CreateKnowledgeProposal(ctx, pendingProposal("proposal-conflict", proposed, existing.Revision, now.Add(3*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE knowledge_entries SET revision=revision+1 WHERE id=?`, existing.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.ApproveKnowledgeProposal(ctx, conflictProposal.ID, now.Add(4*time.Second)); !errors.Is(err, ErrKnowledgeProposalRevision) {
		t.Fatalf("approval revision conflict error=%v", err)
	}
	pending, _ := database.KnowledgeProposal(ctx, conflictProposal.ID)
	if pending.Status != domain.KnowledgeProposalPending {
		t.Fatalf("conflicted proposal resolved unexpectedly: %#v", pending)
	}
}

func TestKnowledgeProposalConcurrentApprovalPublishesOnce(t *testing.T) {
	database, ctx, project, _ := proposalTestStore(t)
	now := time.Now().UTC()
	entry := domain.KnowledgeEntry{ID: "knowledge-race", Scope: "project", ProjectID: project.ID, Slug: "race", Type: "practice", Title: "Race", Body: "Once", CreatedAt: now, UpdatedAt: now}
	proposal, err := database.CreateKnowledgeProposal(ctx, pendingProposal("proposal-race", entry, 0, now))
	if err != nil {
		t.Fatal(err)
	}
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, err := database.ApproveKnowledgeProposal(ctx, proposal.ID, now.Add(time.Second))
			errorsFound <- err
		}()
	}
	wait.Wait()
	close(errorsFound)
	succeeded, resolved := 0, 0
	for err := range errorsFound {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrKnowledgeProposalResolved) {
			resolved++
		} else {
			t.Fatalf("unexpected concurrent approval error=%v", err)
		}
	}
	if succeeded != 1 || resolved != 1 {
		t.Fatalf("concurrent approvals succeeded=%d resolved=%d", succeeded, resolved)
	}
	published, err := database.Knowledge(ctx, entry.ID)
	if err != nil || published.Revision != 1 || published.Status != domain.KnowledgeVerified {
		t.Fatalf("published=%#v err=%v", published, err)
	}
}

func TestKnowledgeRootRevisionUsesExistingV33Root(t *testing.T) {
	t.Parallel()
	database, ctx, _, root := proposalTestStore(t)
	now := time.Now().UTC()
	proposed := root
	proposed.Title, proposed.Body = "Revised home", "Approved root content."
	proposal, err := database.CreateKnowledgeProposal(ctx, pendingProposal("proposal-root", proposed, root.Revision, now))
	if err != nil {
		t.Fatal(err)
	}
	stale, err := database.Knowledge(ctx, root.ID)
	if err != nil || stale.Status != domain.KnowledgeVerified || !stale.IsIndex {
		t.Fatalf("pending root revision=%#v err=%v", stale, err)
	}
	applicable, err := database.ListApplicableKnowledge(ctx, stale.ProjectID, "", []domain.KnowledgeStatus{domain.KnowledgeVerified})
	if err != nil || len(applicable) == 0 || applicable[0].ID != root.ID {
		t.Fatalf("pending root disappeared from applicable knowledge: %#v err=%v", applicable, err)
	}
	_, published, err := database.ApproveKnowledgeProposal(ctx, proposal.ID, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if published.ID != root.ID || !published.IsIndex || published.Status != domain.KnowledgeVerified || published.Revision != root.Revision+1 || published.Title != proposed.Title {
		t.Fatalf("published root=%#v", published)
	}
	var roots int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_entries WHERE scope=? AND project_id=? AND is_index=1`, root.Scope, root.ProjectID).Scan(&roots); err != nil || roots != 1 {
		t.Fatalf("root count=%d err=%v", roots, err)
	}
}
