package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestKnowledgeReviewSettingAndBatchApproval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "batch-review-project", Name: "Batch", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, err := database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	entry := domain.KnowledgeEntry{ID: "batch-proposal-entry", Scope: "project", ProjectID: project.ID, ParentID: root.ID, Slug: "batch-proposal", Type: "practice", Title: "Proposal", Body: "Body", CreatedAt: now, UpdatedAt: now}
	proposal, err := database.CreateKnowledgeProposal(ctx, domain.KnowledgeProposal{
		ID: "batch-proposal", EntryID: entry.ID, Proposed: entry, Status: domain.KnowledgeProposalPending,
		ReviewMode: "manual", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := domain.KnowledgeEntry{ID: "batch-legacy", Scope: "project", ProjectID: project.ID, ParentID: root.ID, Slug: "batch-legacy", Type: "practice", Title: "Legacy", Body: "Body", Status: domain.KnowledgeCandidate, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateKnowledge(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}).Handler())
	defer server.Close()
	client := newCookieClient(t)
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/knowledge-review", map[string]any{"auto_approve": true}, csrf)
	var settings struct {
		Review domain.KnowledgeReviewSettings `json:"review_settings"`
	}
	decodeResponse(t, response, &settings)
	if response.StatusCode != http.StatusOK || !settings.Review.AutoApprove {
		t.Fatalf("settings status=%d body=%#v", response.StatusCode, settings)
	}
	storedProposal, _ := database.KnowledgeProposal(ctx, proposal.ID)
	if storedProposal.Status != domain.KnowledgeProposalPending {
		t.Fatal("enabling automatic review changed an existing pending proposal")
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge/proposals/batch", map[string]any{
		"action": "approve", "proposal_ids": []string{proposal.ID}, "legacy_ids": []string{legacy.ID},
	}, csrf)
	var batch struct {
		OK        bool     `json:"ok"`
		Processed []string `json:"processed"`
		Failures  []any    `json:"failures"`
	}
	decodeResponse(t, response, &batch)
	if response.StatusCode != http.StatusOK || !batch.OK || len(batch.Processed) != 2 || len(batch.Failures) != 0 {
		t.Fatalf("batch status=%d body=%#v", response.StatusCode, batch)
	}
	storedProposal, err = database.KnowledgeProposal(ctx, proposal.ID)
	if err != nil || storedProposal.Status != domain.KnowledgeProposalApproved || storedProposal.ReviewMode != "manual" {
		t.Fatalf("proposal=%#v err=%v", storedProposal, err)
	}
	legacy, err = database.Knowledge(ctx, legacy.ID)
	if err != nil || legacy.Status != domain.KnowledgeVerified {
		t.Fatalf("legacy=%#v err=%v", legacy, err)
	}
}
