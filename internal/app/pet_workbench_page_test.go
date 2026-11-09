package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if err := os.RemoveAll(filepath.Join(root, "worklog")); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"worklog"} {
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
		ApprovalLimit: 50, WorklogLimit: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.WorklogTotal != 35 || len(first.Worklog) != 30 || first.ApprovalTotal != 55 || len(first.Approvals) != 50 {
		t.Fatalf("first page totals=%d/%d lengths=%d/%d", first.WorklogTotal, first.ApprovalTotal, len(first.Worklog), len(first.Approvals))
	}
	expanded, err := fixture.service.PetWorkbenchWithOptions(fixture.ctx, PetWorkbenchOptions{
		ApprovalLimit: 80, WorklogLimit: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(expanded.Worklog) != 35 || len(expanded.Approvals) != 55 {
		t.Fatalf("expanded lengths=%d/%d", len(expanded.Worklog), len(expanded.Approvals))
	}
}

func TestPetWorkbenchRetiredJournalUnavailable(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	root := fixture.service.petWorkspaceRoot(fixture.ctx)
	if err := fixture.service.PetSaveWorkbenchDocument(fixture.ctx, "diary", "2026-11-09.md", "retired"); err == nil {
		t.Fatal("retired section still writable")
	}
	for _, tool := range fixture.service.PetMCPTools(fixture.ctx) {
		if tool.Name == "pet_write_diary" {
			t.Fatal("retired tool still advertised")
		}
	}
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "pet_write_diary", json.RawMessage(`{"date":"2026-11-09","content":"retired"}`)); err == nil {
		t.Fatal("retired tool still callable")
	}
	for _, call := range []struct {
		name string
		args string
	}{
		{"pet_update_core", `{"document":"identity","content":"preserved identity"}`},
		{"pet_write_worklog", `{"date":"2026-11-09","content":"preserved worklog"}`},
	} {
		if _, err := fixture.service.PetMCPTool(fixture.ctx, call.name, json.RawMessage(call.args)); err != nil {
			t.Fatalf("supported tool %s failed: %v", call.name, err)
		}
	}
	for _, file := range []struct{ path, content string }{{"core/identity.md", "preserved identity"}, {"worklog/2026-11-09.md", "preserved worklog"}} {
		raw, err := os.ReadFile(filepath.Join(root, file.path))
		if err != nil || string(raw) != file.content {
			t.Fatalf("supported tool write %s=%q %v", file.path, raw, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
			t.Fatal(err)
		}
		view, err := fixture.service.PetWorkbench(fixture.ctx)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"diary", "diary_total", "diary_limit"} {
			if _, exists := payload[field]; exists {
				t.Fatalf("retired field %q remains", field)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "diary")); !os.IsNotExist(err) {
			t.Fatalf("retired directory recreated: %v", err)
		}
	}
}

func TestPetPendingApprovalCountIgnoresPaginationAndResolved(t *testing.T) {
	fixture := newPetTaskFixture(t)
	assertCount := func(want int) {
		t.Helper()
		got, err := fixture.service.PetPendingApprovalCount(fixture.ctx)
		if err != nil || got != want {
			t.Fatalf("pending=%d err=%v want=%d", got, err, want)
		}
	}
	assertCount(0)
	for i := 0; i < 105; i++ {
		if err := fixture.database.CreatePetApproval(fixture.ctx, domain.PetApproval{ID: fmt.Sprintf("pending-%d", i), Kind: "owner_decision", Title: "pending", Status: "pending", CreatedAt: fixture.createdAt}); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{"approved", "rejected", "cancelled"} {
		if err := fixture.database.CreatePetApproval(fixture.ctx, domain.PetApproval{ID: status, Kind: "owner_decision", Title: status, Status: status, CreatedAt: fixture.createdAt}); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(105)
	page, _, err := fixture.service.PetApprovalsWithTotal(fixture.ctx, 1)
	if err != nil || len(page) != 1 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	assertCount(105)
}

func TestPetWorkspaceMigratesOnlyGeneratedLabGuide(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture.service.petWorkspaceRoot(fixture.ctx), petLabDir, "README.md")
	legacy := strings.Replace(petLabReadmeSeed, "core/、worklog/", "core/、diary/、worklog/", 1)
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(legacy, "\n", "\r\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetWorkbench(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != petLabReadmeSeed {
		t.Fatalf("guide migration failed: %q %v", raw, err)
	}
	custom := "# My custom lab guide\nOwner data remains here.\n"
	if err := os.WriteFile(path, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetWorkbench(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil || string(raw) != custom {
		t.Fatalf("custom guide overwritten: %q %v", raw, err)
	}
}
