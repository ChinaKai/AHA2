package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestKnowledgeProposalCarriesCompleteProposedEntry(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	proposal := KnowledgeProposal{
		ID: "proposal-1", EntryID: "knowledge-1", BaseRevision: 2, Status: KnowledgeProposalPending,
		SourceTaskID: "task-1", SourceTurnID: "turn-1", CreatedAt: now, UpdatedAt: now,
		Proposed: KnowledgeEntry{
			ID: "knowledge-1", Scope: "project", ProjectID: "project-1", ParentID: "root", Slug: "entry",
			SortOrder: 3, Type: "practice", Title: "Entry", Body: "Complete body", Status: KnowledgeCandidate,
			BranchScope: "main", ProductLineID: "line-1", Confidence: .9, Revision: 3, ContentHash: "hash",
			VerifiedCommit: "commit", SourceTaskID: "task-1", SourceTurnID: "turn-1", CreatedAt: now, UpdatedAt: now,
		},
	}
	data, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"status":"pending"`, `"base_revision":2`, `"proposed"`, `"parent_id":"root"`, `"body":"Complete body"`} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("proposal JSON missing %s: %s", expected, data)
		}
	}
}
