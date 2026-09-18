package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// TestKnowledgeIndexesIgnoresPageOrdering is the regression guard for the
// channel knowledge-scope picker, which used to build its project list by
// filtering a page of entries.
//
// That page is ordered by updated_at DESC, so once the library holds more
// recently-updated entries than a page can carry, only the handful of projects
// touched most recently survive the filter. Every other project silently loses
// the ability to be granted, which looks like "the list is missing projects"
// with no error anywhere.
func TestKnowledgeIndexesIgnoresPageOrdering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()

	// More projects than a list page holds, created oldest-first so that the
	// newest entries are guaranteed to occupy the whole first page.
	const projects = 60
	for index := 0; index < projects; index++ {
		projectID := "project-index-" + string(rune('a'+index%26)) + string(rune('a'+index/26))
		if err := database.CreateProject(ctx, domain.Project{ID: projectID, Name: projectID, CreatedAt: now.Add(time.Duration(index) * time.Second), UpdatedAt: now.Add(time.Duration(index) * time.Second)}); err != nil {
			t.Fatal(err)
		}
		root, err := database.EnsureKnowledgeRoot(ctx, "project", projectID)
		if err != nil {
			t.Fatal(err)
		}
		if root.Status != domain.KnowledgeVerified {
			t.Fatalf("root for %s is %q", projectID, root.Status)
		}
	}

	indexes, err := database.KnowledgeIndexes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != projects {
		t.Fatalf("KnowledgeIndexes returned %d roots, want %d", len(indexes), projects)
	}

	// Prove the old approach really is lossy on this same data, so this test
	// fails for the right reason if the endpoint ever goes back to paging.
	page, _, err := database.ListKnowledgePage(ctx, "", "", []domain.KnowledgeStatus{domain.KnowledgeVerified}, time.Time{}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	onPage := 0
	for _, entry := range page {
		if entry.IsIndex {
			onPage++
		}
	}
	if onPage >= projects {
		t.Fatalf("page held %d roots, so this fixture no longer reproduces the bug", onPage)
	}
	t.Logf("indexes=%d, roots visible on one page=%d", len(indexes), onPage)
}
