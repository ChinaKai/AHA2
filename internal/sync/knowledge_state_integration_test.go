package sync_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/centersync"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func TestTwoDevicesSynchronizeAllKnowledgeAndProposalStates(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	server := httptest.NewServer(center.Handler())
	defer server.Close()
	makeDevice := func(id, token string) (*store.Store, *secrets.FileStore) {
		dir := t.TempDir()
		database, err := store.Open(ctx, filepath.Join(dir, "aha2.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		secretStore, err := secrets.Open(filepath.Join(dir, "secrets.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := secretStore.PutMany(map[string]string{syncer.DefaultTokenRef: token}); err != nil {
			t.Fatal(err)
		}
		if err := database.PutSyncSettings(ctx, domain.SyncSettings{Scope: "default", Enabled: true, Endpoint: server.URL, DeviceID: id, IntervalSeconds: 60}); err != nil {
			t.Fatal(err)
		}
		if err := center.PutDeviceToken(ctx, id, token); err != nil {
			t.Fatal(err)
		}
		return database, secretStore
	}

	source, sourceSecrets := makeDevice("knowledge-source", "source-token")
	destination, destinationSecrets := makeDevice("knowledge-destination", "destination-token")
	now := time.Now().UTC()
	project := domain.Project{ID: "knowledge-state-project", Name: "Knowledge state", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := source.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	entries := []domain.KnowledgeEntry{
		{ID: "global-candidate", Scope: "global", Type: "practice", Title: "Candidate", Body: "candidate", Status: domain.KnowledgeCandidate, Revision: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "project-stale", Scope: "project", ProjectID: project.ID, Type: "practice", Title: "Stale", Body: "stale", Status: domain.KnowledgeStale, Revision: 2, CreatedAt: now, UpdatedAt: now},
		{ID: "project-rejected", Scope: "project", ProjectID: project.ID, Type: "practice", Title: "Rejected base", Body: "base", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "global-pending", Scope: "global", Type: "practice", Title: "Pending base", Body: "base", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now},
	}
	for _, entry := range entries {
		if err := source.CreateKnowledge(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	proposal := func(id string, proposed domain.KnowledgeEntry, baseRevision int) domain.KnowledgeProposal {
		return domain.KnowledgeProposal{
			ID: id, EntryID: proposed.ID, BaseRevision: baseRevision, Proposed: proposed,
			SourceTaskID: "private-task", SourceTurnID: "private-turn",
			Status: domain.KnowledgeProposalPending, CreatedAt: now, UpdatedAt: now,
		}
	}
	_, err = source.CreateKnowledgeProposal(ctx, proposal("proposal-pending-new", domain.KnowledgeEntry{
		ID: "project-proposed-new", Scope: "project", ProjectID: project.ID, Type: "practice", Title: "Pending new", Body: "pending new",
	}, 0))
	if err != nil {
		t.Fatal(err)
	}
	approvedNew, err := source.CreateKnowledgeProposal(ctx, proposal("proposal-approved", domain.KnowledgeEntry{
		ID: "global-approved", Scope: "global", Type: "practice", Title: "Approved", Body: "approved",
	}, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := source.ApproveKnowledgeProposal(ctx, approvedNew.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	rejectedBase, _ := source.Knowledge(ctx, "project-rejected")
	rejectedProposed := rejectedBase
	rejectedProposed.Body = "rejected revision"
	rejected, err := source.CreateKnowledgeProposal(ctx, proposal("proposal-rejected", rejectedProposed, rejectedBase.Revision))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.RejectKnowledgeProposal(ctx, rejected.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	pendingBase, _ := source.Knowledge(ctx, "global-pending")
	pendingProposed := pendingBase
	pendingProposed.Body = "pending revision"
	if _, err := source.CreateKnowledgeProposal(ctx, proposal("proposal-pending-existing", pendingProposed, pendingBase.Revision)); err != nil {
		t.Fatal(err)
	}
	sourceRunner := syncer.Runner{Store: source, Secrets: sourceSecrets, TokenRef: syncer.DefaultTokenRef}
	destinationRunner := syncer.Runner{Store: destination, Secrets: destinationSecrets, TokenRef: syncer.DefaultTokenRef}
	if err := sourceRunner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := destinationRunner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	sourceKnowledge, err := source.ListKnowledge(ctx, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	destinationKnowledge, err := destination.ListKnowledge(ctx, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(destinationKnowledge) != len(sourceKnowledge) {
		t.Fatalf("knowledge count diverged: source=%d destination=%d", len(sourceKnowledge), len(destinationKnowledge))
	}
	destinationByID := make(map[string]domain.KnowledgeEntry, len(destinationKnowledge))
	for _, entry := range destinationKnowledge {
		destinationByID[entry.ID] = entry
	}
	for _, expected := range sourceKnowledge {
		actual, ok := destinationByID[expected.ID]
		if !ok || actual.Scope != expected.Scope || actual.ProjectID != expected.ProjectID || actual.ParentID != expected.ParentID || actual.Status != expected.Status || actual.Revision != expected.Revision || actual.Body != expected.Body || actual.IsIndex != expected.IsIndex {
			t.Fatalf("knowledge state mismatch for %s: source=%#v destination=%#v", expected.ID, expected, actual)
		}
	}
	for _, scope := range []struct{ scope, projectID string }{{"global", ""}, {"project", project.ID}} {
		roots := 0
		for _, entry := range destinationKnowledge {
			if entry.Scope == scope.scope && entry.ProjectID == scope.projectID && entry.IsIndex {
				roots++
			}
		}
		if roots != 1 {
			t.Fatalf("scope %s/%s has %d roots", scope.scope, scope.projectID, roots)
		}
	}

	sourceProposals, err := source.ListKnowledgeProposals(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	destinationProposals, err := destination.ListKnowledgeProposals(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceProposals) != 4 || len(destinationProposals) != len(sourceProposals) {
		t.Fatalf("proposal count diverged: source=%d destination=%d", len(sourceProposals), len(destinationProposals))
	}
	proposalByID := map[string]domain.KnowledgeProposal{}
	for _, item := range destinationProposals {
		proposalByID[item.ID] = item
		if item.SourceTaskID != "" || item.SourceTurnID != "" || item.Proposed.SourceTaskID != "" || item.Proposed.SourceTurnID != "" {
			t.Fatalf("proposal leaked task-local source ids: %#v", item)
		}
	}
	for _, expected := range sourceProposals {
		actual, ok := proposalByID[expected.ID]
		if !ok || actual.Status != expected.Status || actual.BaseRevision != expected.BaseRevision || actual.Proposed.Status != expected.Proposed.Status || actual.Proposed.Scope != expected.Proposed.Scope || actual.Proposed.ProjectID != expected.Proposed.ProjectID || actual.Proposed.Body != expected.Proposed.Body {
			t.Fatalf("proposal state mismatch for %s: source=%#v destination=%#v", expected.ID, expected, actual)
		}
	}

	if _, _, err := source.ApproveKnowledgeProposal(ctx, "proposal-pending-existing", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := sourceRunner.RunOnce(ctx); err != nil {
		t.Fatalf("source approval sync failed: %v", err)
	}
	if err := destinationRunner.RunOnce(ctx); err != nil {
		t.Fatalf("destination approval sync failed: %v", err)
	}
	approved, err := destination.KnowledgeProposal(ctx, "proposal-pending-existing")
	if err != nil || approved.Status != domain.KnowledgeProposalApproved {
		t.Fatalf("pending proposal did not transition to approved: %#v err=%v", approved, err)
	}
	published, err := destination.Knowledge(ctx, "global-pending")
	if err != nil || published.Status != domain.KnowledgeVerified || published.Body != "pending revision" || published.Revision != 2 {
		t.Fatalf("approved proposal content did not converge: %#v err=%v", published, err)
	}
}
