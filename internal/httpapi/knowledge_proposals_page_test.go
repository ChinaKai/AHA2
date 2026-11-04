package httpapi

import (
	"context"
	"encoding/json"
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

func TestKnowledgeProposalsListPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "proposal-page-project", Name: "Proposal Page", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, err := database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		id := string(rune('a' + index))
		entry := domain.KnowledgeEntry{
			ID: "proposal-page-entry-" + id, Scope: "project", ProjectID: project.ID, ParentID: root.ID,
			Slug: "proposal-page-entry-" + id, Type: "practice", Title: "Proposal " + id, Body: "Body",
			CreatedAt: now.Add(time.Duration(index) * time.Second), UpdatedAt: now.Add(time.Duration(index) * time.Second),
		}
		if _, err := database.CreateKnowledgeProposal(ctx, domain.KnowledgeProposal{
			ID: "proposal-page-" + id, EntryID: entry.ID, Proposed: entry,
			Status: domain.KnowledgeProposalPending, ReviewMode: "manual",
			CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}).Handler())
	defer server.Close()
	client := newCookieClient(t)
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge/proposals/proposal-page-b/approve", map[string]any{}, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("approve status=%d", response.StatusCode)
	}
	response.Body.Close()

	type pagePayload struct {
		Proposals []domain.KnowledgeProposal `json:"proposals"`
		Total     int                        `json:"total"`
		Page      int                        `json:"page"`
		Pages     int                        `json:"pages"`
		HasMore   bool                       `json:"has_more"`
	}
	get := func(query string) pagePayload {
		t.Helper()
		response, getErr := client.Get(server.URL + "/api/v1/knowledge/proposals" + query)
		if getErr != nil {
			t.Fatal(getErr)
		}
		defer response.Body.Close()
		var payload pagePayload
		decodeResponse(t, response, &payload)
		return payload
	}
	first := get("?status=all&page=1&limit=2")
	if first.Total != 3 || len(first.Proposals) != 2 || first.Pages != 2 || !first.HasMore {
		t.Fatalf("first page=%#v", first)
	}
	second := get("?status=all&page=2&limit=2")
	if second.Total != 3 || len(second.Proposals) != 1 || second.Pages != 2 || second.HasMore {
		t.Fatalf("second page=%#v", second)
	}
	pending := get("?status=pending&limit=0")
	if pending.Total != 2 || len(pending.Proposals) != 2 {
		t.Fatalf("pending=%#v", pending)
	}
	approved := get("?status=approved&limit=10")
	if approved.Total != 1 || len(approved.Proposals) != 1 {
		t.Fatalf("approved=%#v", approved)
	}

	response, err = client.Get(server.URL + "/api/v1/knowledge?include_proposals=false")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]json.RawMessage
	decodeResponse(t, response, &payload)
	if _, exists := payload["proposals"]; exists {
		t.Fatal("include_proposals=false still returned proposals")
	}
}
