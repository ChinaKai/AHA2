package store

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// TestPetApprovalCountsGroupByKindAndStatus pins the untruncated grouping the
// workbench footer relies on. Grouping by kind alone would collapse the three
// resolved statuses into one row and make a status filter's total wrong.
func TestPetApprovalCountsGroupByKindAndStatus(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	for index, item := range []struct{ kind, status string }{
		{"idea", "pending"},
		{"idea", "pending"},
		{"idea", "approved"},
		{"delete_task", "approved"},
		{"delete_task", "approved"},
		{"delete_task", "rejected"},
		{"delete_task", "cancelled"},
		{"owner_decision", "pending"},
	} {
		if err := database.CreatePetApproval(ctx, domain.PetApproval{
			ID: fmt.Sprintf("approval-%02d", index), Kind: item.kind,
			Title: item.kind + " " + item.status, Status: item.status,
			CreatedAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	counts, err := database.PetApprovalCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, group := range counts {
		got[group.Kind+"/"+group.Status] = group.Count
	}
	want := map[string]int{
		"idea/pending":           2,
		"idea/approved":          1,
		"delete_task/approved":   2,
		"delete_task/rejected":   1,
		"delete_task/cancelled":  1,
		"owner_decision/pending": 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("counts=%#v want %#v", got, want)
	}
}
