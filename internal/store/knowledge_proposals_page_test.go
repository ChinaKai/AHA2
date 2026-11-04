package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestKnowledgeProposalsPagePaginatesHistory(t *testing.T) {
	database, ctx, project, _ := proposalTestStore(t)
	now := time.Now().UTC()
	for index := 0; index < 3; index++ {
		createdAt := now.Add(time.Duration(index) * time.Second)
		entry := domain.KnowledgeEntry{
			ID: fmt.Sprintf("knowledge-page-%d", index), Scope: "project", ProjectID: project.ID,
			Slug: fmt.Sprintf("page-%d", index), Type: "practice", Title: fmt.Sprintf("Page %d", index),
			Body: "body", Confidence: .9, Status: domain.KnowledgeVerified,
			CreatedAt: createdAt, UpdatedAt: createdAt,
		}
		if _, err := database.CreateKnowledgeProposal(ctx, pendingProposal(fmt.Sprintf("proposal-page-%d", index), entry, 0, createdAt)); err != nil {
			t.Fatal(err)
		}
	}

	page, total, err := database.ListKnowledgeProposalsPage(ctx, "", "", "pending", 1, 2)
	if err != nil || total != 3 || len(page) != 2 {
		t.Fatalf("page1=%#v total=%d err=%v", page, total, err)
	}
	page, total, err = database.ListKnowledgeProposalsPage(ctx, "", "", "pending", 2, 2)
	if err != nil || total != 3 || len(page) != 1 {
		t.Fatalf("page2=%#v total=%d err=%v", page, total, err)
	}
	all, total, err := database.ListKnowledgeProposalsPage(ctx, "", "", "pending", 1, 0)
	if err != nil || total != 3 || len(all) != 3 {
		t.Fatalf("unpaginated=%#v total=%d err=%v", all, total, err)
	}

	if _, err := database.db.ExecContext(ctx, `UPDATE knowledge_proposals SET status='approved',decided_at=? WHERE id=?`, timeString(now), "proposal-page-1"); err != nil {
		t.Fatal(err)
	}
	approved, total, err := database.ListKnowledgeProposalsPage(ctx, "", "", "approved", 1, 10)
	if err != nil || total != 1 || len(approved) != 1 || approved[0].ID != "proposal-page-1" {
		t.Fatalf("approved=%#v total=%d err=%v", approved, total, err)
	}
	all, total, err = database.ListKnowledgeProposalsPage(ctx, "", "", "all", 1, 10)
	if err != nil || total != 3 || len(all) != 3 {
		t.Fatalf("all=%#v total=%d err=%v", all, total, err)
	}
}
