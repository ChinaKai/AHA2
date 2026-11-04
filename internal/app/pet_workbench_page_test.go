package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetWorkbenchPageLimitsAndTotals(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	root := fixture.service.petWorkspaceRoot(fixture.ctx)
	if err := os.RemoveAll(filepath.Join(root, "diary")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "worklog")); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"diary", "worklog"} {
		dir := filepath.Join(root, section)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 35; i++ {
			name := time.Date(2099, 1, 1+i, 0, 0, 0, 0, time.UTC).Format("2006-01-02") + ".md"
			if err := os.WriteFile(filepath.Join(dir, name), []byte(section), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	now := fixture.createdAt
	for i := 0; i < 55; i++ {
		if err := fixture.database.CreatePetApproval(fixture.ctx, domain.PetApproval{
			ID: fmt.Sprintf("approval-page-%02d", i), Kind: "idea",
			Title: fmt.Sprintf("idea %02d", i), Summary: "summary", Risk: "low",
			Status: petApprovalPending, CreatedAt: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := fixture.service.PetWorkbenchWithOptions(fixture.ctx, PetWorkbenchOptions{
		ApprovalLimit: 50, DiaryLimit: 30, WorklogLimit: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.DiaryTotal != 35 || len(first.Diary) != 30 || first.WorklogTotal != 35 || len(first.Worklog) != 30 || first.ApprovalTotal != 55 || len(first.Approvals) != 50 {
		t.Fatalf("first page totals=%d/%d/%d lengths=%d/%d/%d", first.DiaryTotal, first.WorklogTotal, first.ApprovalTotal, len(first.Diary), len(first.Worklog), len(first.Approvals))
	}
	expanded, err := fixture.service.PetWorkbenchWithOptions(fixture.ctx, PetWorkbenchOptions{
		ApprovalLimit: 80, DiaryLimit: 60, WorklogLimit: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(expanded.Diary) != 35 || len(expanded.Worklog) != 35 || len(expanded.Approvals) != 55 {
		t.Fatalf("expanded lengths=%d/%d/%d", len(expanded.Diary), len(expanded.Worklog), len(expanded.Approvals))
	}
}
