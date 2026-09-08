package app

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestCreateKnowledgeCandidatesOnlyCreatesPendingProposals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "proposal-project", Name: "Proposal", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, _ := database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	existing := domain.KnowledgeEntry{
		ID: "knowledge-existing", Scope: "project", ProjectID: project.ID, ParentID: root.ID, Slug: "existing",
		Type: "practice", Title: "Existing", Body: "Old", Status: domain.KnowledgeVerified, Revision: 2,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), LastVerifiedAt: now.Add(-time.Hour),
	}
	if err := database.CreateKnowledge(ctx, existing); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, nil, StubExecutor{})
	entries, proposals, err := service.createKnowledgeCandidates(ctx, project.ID, "task-source", "turn-source", "main", "line-main", []KnowledgeCandidate{
		{Scope: "project", Type: "practice", Title: "New high confidence", Body: "Must wait for approval.", Confidence: .99},
		{EntryID: existing.ID, BaseRevision: existing.Revision, Scope: "project", Type: "practice", Title: "Existing revised", Body: "Pending replacement.", Confidence: .99},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || len(proposals) != 2 {
		t.Fatalf("entries=%#v proposals=%#v", entries, proposals)
	}
	for index := range proposals {
		if proposals[index].Status != domain.KnowledgeProposalPending || proposals[index].ReviewMode != "manual" || entries[index].Status != domain.KnowledgeCandidate || proposals[index].Proposed.ID != entries[index].ID {
			t.Fatalf("entry=%#v proposal=%#v", entries[index], proposals[index])
		}
	}
	if _, err := database.Knowledge(ctx, entries[0].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("new high confidence candidate auto-published: %v", err)
	}
	stale, err := database.Knowledge(ctx, existing.ID)
	if err != nil || stale.Status != domain.KnowledgeStale || stale.Revision != existing.Revision || stale.Body != existing.Body {
		t.Fatalf("existing entry=%#v err=%v", stale, err)
	}
	applicable, err := database.ListApplicableKnowledge(ctx, project.ID, "line-main", []domain.KnowledgeStatus{domain.KnowledgeVerified})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range applicable {
		if item.ID == existing.ID || item.ID == entries[0].ID {
			t.Fatalf("pending knowledge remained applicable: %#v", item)
		}
	}
}

func TestCreateKnowledgeCandidatesAutoApprovesWhenEnabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "auto-review-project", Name: "Auto", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateKnowledgeReviewSettings(ctx, domain.KnowledgeReviewSettings{AutoApprove: true, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, nil, StubExecutor{})
	entries, proposals, err := service.createKnowledgeCandidates(ctx, project.ID, "task-source", "turn-source", "main", "", []KnowledgeCandidate{{
		Scope: "project", Type: "practice", Title: "Auto approved", Body: "Published immediately.", Confidence: .9,
	}})
	if err != nil || len(entries) != 1 || len(proposals) != 1 {
		t.Fatalf("entries=%#v proposals=%#v err=%v", entries, proposals, err)
	}
	if proposals[0].Status != domain.KnowledgeProposalApproved || proposals[0].ReviewMode != "auto" || entries[0].Status != domain.KnowledgeVerified || entries[0].Revision != 1 {
		t.Fatalf("entry=%#v proposal=%#v", entries[0], proposals[0])
	}
	stored, err := database.Knowledge(ctx, entries[0].ID)
	if err != nil || stored.Status != domain.KnowledgeVerified {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
}

func TestGlobalAgentKnowledgeDefaultsToLessonCategories(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewService(database, nil, StubExecutor{})
	generalParent := store.GlobalGeneralKnowledgeID
	entries, _, err := service.createKnowledgeCandidates(ctx, "project", "task-source", "turn-source", "main", "", []KnowledgeCandidate{
		{Scope: "global", Type: "diagnostic", Title: "Technical lesson", Body: "Trigger, fix, verify.", Confidence: .9},
		{Scope: "global", Type: "practice", Title: "Behavior lesson", Body: "Trigger, action, acceptance.", Confidence: .9},
		{Scope: "global", ParentID: &generalParent, Type: "practice", Title: "Human reference", Body: "Reference.", Confidence: .9},
	})
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries=%#v err=%v", entries, err)
	}
	if entries[0].ParentID != store.GlobalTechnicalLessonsKnowledgeID || entries[1].ParentID != store.GlobalBehaviorLessonsKnowledgeID || entries[2].ParentID != store.GlobalGeneralKnowledgeID {
		t.Fatalf("unexpected global category routing: %#v", entries)
	}
}
