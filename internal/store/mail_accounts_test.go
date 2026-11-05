package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestMailAccountLifecycleAndCredentialIsolation(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)

	accountID := domain.NewID("mail_account")
	credentialRef := domain.MailAccountCredentialRef(accountID)
	created, err := database.CreateMailAccount(ctx, domain.MailAccount{
		ID: accountID, Name: "Build notices", Address: "noreply@example.com",
		DisplayName: "AHA build", CredentialRef: credentialRef,
		CredentialConfigured: true,
		Aliases: []domain.MailAccountAlias{
			{Address: "build-bot@example.com", DisplayName: "Build bot"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.CredentialRef != "" {
		t.Fatal("created account leaked credential_ref")
	}
	if !created.CredentialConfigured {
		t.Fatal("created account should report credential_configured")
	}
	if created.Provider != domain.MailProviderNeteaseQiye ||
		created.SMTPHost != domain.MailDefaultSMTPHost ||
		created.SMTPPort != domain.MailDefaultSMTPPort ||
		created.SMTPSecurity != domain.MailSMTPSecuritySSL ||
		created.SMTPUsername != "noreply@example.com" {
		t.Fatalf("account defaults not applied: %+v", created)
	}

	stored, err := database.MailAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CredentialRef != "" {
		t.Fatal("MailAccount leaked credential_ref")
	}
	if !stored.CredentialConfigured || stored.Status != domain.MailAccountUnverified {
		t.Fatalf("unexpected credential/status state: %+v", stored)
	}
	if len(stored.Aliases) != 1 || stored.Aliases[0].Address != "build-bot@example.com" ||
		stored.Aliases[0].DisplayName != "Build bot" {
		t.Fatalf("aliases = %+v", stored.Aliases)
	}
	resolved, err := database.MailAccountCredentialRef(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != credentialRef {
		t.Fatalf("credential ref = %q, want %q", resolved, credentialRef)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "credential_ref") || strings.Contains(string(encoded), credentialRef) {
		t.Fatalf("account JSON leaked credential data: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"credential_configured":true`) {
		t.Fatalf("account JSON should expose only the configured flag: %s", encoded)
	}

	testedAt := time.Now().UTC().Add(-time.Minute)
	stored.Status = domain.MailAccountOK
	stored.LastTestAt = testedAt
	stored.Name = "Build notices v2"
	stored.Address = "release@example.com"
	stored.DisplayName = "AHA release"
	stored.Aliases = []domain.MailAccountAlias{{Address: "release-bot@example.com"}}
	updated, err := database.UpdateMailAccount(ctx, stored)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Build notices v2" || updated.Address != "release@example.com" ||
		updated.Status != domain.MailAccountOK || updated.LastTestAt.IsZero() {
		t.Fatalf("updated account = %+v", updated)
	}
	if len(updated.Aliases) != 1 || updated.Aliases[0].Address != "release-bot@example.com" {
		t.Fatalf("replaced aliases = %+v", updated.Aliases)
	}
	if !updated.CredentialConfigured {
		t.Fatal("updating account settings must not clear credential_configured")
	}

	rotatedRef := domain.MailAccountCredentialRef(accountID) + "-rotated"
	if err := database.SetMailAccountCredential(ctx, accountID, rotatedRef, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rotated, err := database.MailAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Status != domain.MailAccountUnverified || !rotated.LastTestAt.IsZero() ||
		rotated.LastTestErrorCode != "" || !rotated.CredentialConfigured {
		t.Fatalf("rotation did not reset account state: %+v", rotated)
	}
	if rotated.CredentialRef != "" {
		t.Fatal("rotated account read leaked credential_ref")
	}
	if ref, err := database.MailAccountCredentialRef(ctx, accountID); err != nil || ref != rotatedRef {
		t.Fatalf("rotated credential ref = %q, err=%v", ref, err)
	}

	if err := database.DisableMailAccount(ctx, accountID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	disabled, err := database.MailAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Status != domain.MailAccountDisabled {
		t.Fatalf("status after disable = %q", disabled.Status)
	}

	accounts, err := database.MailAccounts(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].ID != accountID {
		t.Fatalf("accounts = %+v", accounts)
	}

	plain, err := database.CreateMailAccount(ctx, domain.MailAccount{
		ID: domain.NewID("mail_account"), Name: "No credential",
		Address: "plain@example.com", CredentialConfigured: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plain.CredentialConfigured {
		t.Fatal("account without a Secret reference was marked configured")
	}
}

func TestMailAccountDeleteProtection(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, projectID, workspaceID := createMailTaskFixture(t, database)

	unused := createMailAccountFixture(t, database, "Unused", "unused@example.com")
	if err := database.DeleteMailAccount(ctx, unused.ID); err != nil {
		t.Fatalf("unused account delete: %v", err)
	}
	if _, err := database.MailAccount(ctx, unused.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted account still readable: %v", err)
	}

	bound := createMailAccountFixture(t, database, "Bound", "bound@example.com")
	binding, err := database.CreateMailBinding(ctx, domain.MailBinding{
		ScopeType: domain.MailBindingScopeProject, ScopeID: projectID,
		AccountID: bound.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteMailAccount(ctx, bound.ID); !errors.Is(err, ErrMailAccountInUse) {
		t.Fatalf("delete bound account error = %v, want ErrMailAccountInUse", err)
	}
	if err := database.DeleteMailBinding(ctx, binding.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteMailAccount(ctx, bound.ID); err != nil {
		t.Fatalf("delete after unbinding: %v", err)
	}

	used := createMailAccountFixture(t, database, "Used", "used@example.com")
	createMailMessageFixture(t, database, taskID, used.ID, "delete-protection")
	if err := database.DeleteMailAccount(ctx, used.ID); !errors.Is(err, ErrMailAccountInUse) {
		t.Fatalf("delete account with history error = %v, want ErrMailAccountInUse", err)
	}

	aliased := createMailAccountFixture(t, database, "Aliased", "aliased@example.com")
	aliased.Aliases = []domain.MailAccountAlias{{Address: "alias@example.com"}}
	if _, err := database.UpdateMailAccount(ctx, aliased); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteMailAccount(ctx, aliased.ID); err != nil {
		t.Fatalf("delete aliased account: %v", err)
	}
	var aliases int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mail_account_aliases WHERE account_id=?`, aliased.ID).Scan(&aliases); err != nil {
		t.Fatal(err)
	}
	if aliases != 0 {
		t.Fatalf("aliases left after account delete: %d", aliases)
	}

	// TaskMailBindings still sees the workspace task, which proves the fixture
	// scopes are wired and the delete check above was not passing vacuously.
	bindings, err := database.TaskMailBindings(ctx, taskID, workspaceID, projectID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 0 {
		t.Fatalf("expected no bindings after cleanup, got %d", len(bindings))
	}
}
