package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestKnowledgeReviewSettingsAndProposalModePersist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	settings, err := database.KnowledgeReviewSettings(ctx)
	if err != nil || settings.AutoApprove {
		t.Fatalf("settings=%#v err=%v", settings, err)
	}
	settings, err = database.UpdateKnowledgeReviewSettings(ctx, domain.KnowledgeReviewSettings{AutoApprove: true, UpdatedAt: time.Now().UTC()})
	if err != nil || !settings.AutoApprove {
		t.Fatalf("settings=%#v err=%v", settings, err)
	}
	now := time.Now().UTC()
	project := domain.Project{ID: "review-settings-project", Name: "Review", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, err := database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	entry := domain.KnowledgeEntry{ID: "review-mode-entry", Scope: "project", ProjectID: project.ID, ParentID: root.ID, Slug: "review-mode", Type: "practice", Title: "Review mode", Body: "Body", CreatedAt: now, UpdatedAt: now}
	proposal := pendingProposal("review-mode-proposal", entry, 0, now)
	proposal.ReviewMode = "auto"
	created, err := database.CreateKnowledgeProposal(ctx, proposal)
	if err != nil || created.ReviewMode != "auto" {
		t.Fatalf("proposal=%#v err=%v", created, err)
	}
}
