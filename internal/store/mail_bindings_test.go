package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestMailBindingScopeIsolationAndCleanup(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, projectID, workspaceID := createMailTaskFixture(t, database)

	accountA := createMailAccountFixture(t, database, "Account A", "a@example.com")
	accountB := createMailAccountFixture(t, database, "Account B", "b@example.com")

	taskBinding, err := database.CreateMailBinding(ctx, domain.MailBinding{
		ScopeType: domain.MailBindingScopeTask, ScopeID: taskID, AccountID: accountA.ID,
		ApprovalMode: domain.MailApprovalAlways, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	workspaceBinding, err := database.CreateMailBinding(ctx, domain.MailBinding{
		ScopeType: domain.MailBindingScopeWorkspace, ScopeID: workspaceID, AccountID: accountB.ID,
		Enabled: true, AllowedRecipients: []string{"example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	projectBinding, err := database.CreateMailBinding(ctx, domain.MailBinding{
		ScopeType: domain.MailBindingScopeProject, ScopeID: projectID, AccountID: accountA.ID,
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	disabledProjectBinding, err := database.CreateMailBinding(ctx, domain.MailBinding{
		ScopeType: domain.MailBindingScopeProject, ScopeID: projectID, AccountID: accountB.ID,
		Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherProjectBinding, err := database.CreateMailBinding(ctx, domain.MailBinding{
		ScopeType: domain.MailBindingScopeProject, ScopeID: "project-other", AccountID: accountA.ID,
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := database.CreateMailBinding(ctx, domain.MailBinding{
		ScopeType: domain.MailBindingScopeTask, ScopeID: taskID, AccountID: accountA.ID,
		Enabled: true,
	}); err == nil {
		t.Fatal("duplicate scope/account binding passed the unique constraint")
	}

	visible, err := database.TaskMailBindings(ctx, taskID, workspaceID, projectID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 3 {
		t.Fatalf("visible bindings = %d, want 3: %+v", len(visible), visible)
	}
	if visible[0].ID != taskBinding.ID || visible[1].ID != workspaceBinding.ID || visible[2].ID != projectBinding.ID {
		t.Fatalf("binding precedence order is wrong: %+v", visible)
	}
	for _, binding := range visible {
		if binding.ID == disabledProjectBinding.ID || binding.ID == otherProjectBinding.ID {
			t.Fatalf("isolation leaked binding %s", binding.ID)
		}
	}

	projectBindings, err := database.MailBindings(ctx, domain.MailBindingScopeProject, projectID, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(projectBindings) != 2 {
		t.Fatalf("project bindings = %d, want 2", len(projectBindings))
	}
	enabledProjectBindings, err := database.MailBindings(ctx, domain.MailBindingScopeProject, projectID, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabledProjectBindings) != 1 || enabledProjectBindings[0].ID != projectBinding.ID {
		t.Fatalf("enabled project bindings = %+v", enabledProjectBindings)
	}

	beforeUpdate := projectBinding.UpdatedAt
	projectBinding.Enabled = false
	projectBinding.AllowedRecipients = []string{"qa.example.com"}
	if err := database.UpdateMailBinding(ctx, projectBinding); err != nil {
		t.Fatal(err)
	}
	updated, err := database.MailBinding(ctx, projectBinding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || len(updated.AllowedRecipients) != 1 || updated.AllowedRecipients[0] != "qa.example.com" {
		t.Fatalf("updated binding = %+v", updated)
	}
	if !updated.UpdatedAt.After(beforeUpdate) {
		t.Fatalf("binding update did not advance updated_at: %v <= %v", updated.UpdatedAt, beforeUpdate)
	}

	message, err := database.CreateMailMessage(ctx, domain.MailMessage{
		TaskID: taskID, AccountID: accountA.ID, BindingID: taskBinding.ID,
		Source: domain.MailMessageSourceAgent, IdempotencyKey: "binding-cleanup",
		FromAddress: "noreply@example.com", To: []string{"qa@example.com"},
		Subject: "Bound", Status: domain.MailMessageDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.BindingID != taskBinding.ID {
		t.Fatalf("message binding = %q", message.BindingID)
	}
	if err := database.DeleteMailBinding(ctx, taskBinding.ID); err != nil {
		t.Fatal(err)
	}
	cleaned, err := database.MailMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.BindingID != "" {
		t.Fatalf("deleted binding left message.binding_id = %q", cleaned.BindingID)
	}

	removed, err := database.DeleteMailBindingsForScope(ctx, domain.MailBindingScopeWorkspace, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed workspace bindings = %d, want 1", removed)
	}
	if _, err := database.MailBinding(ctx, workspaceBinding.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("workspace binding still present: %v", err)
	}
	if _, err := database.MailBinding(ctx, otherProjectBinding.ID); err != nil {
		t.Fatalf("scope cleanup removed another project's binding: %v", err)
	}

	visible, err = database.TaskMailBindings(ctx, taskID, workspaceID, projectID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 0 {
		t.Fatalf("visible bindings after cleanup = %+v", visible)
	}

	if err := database.DeleteMailBinding(ctx, "mail_binding_missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("delete missing binding error = %v", err)
	}
}
