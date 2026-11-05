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
	if strings.Contains(string(encoded), `"revision"`) {
		t.Fatalf("account JSON exposed internal revision: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"credential_configured":true`) {
		t.Fatalf("account JSON should expose only the configured flag: %s", encoded)
	}

	stored.Name = "Build notices v2"
	stored.Address = "release@example.com"
	stored.DisplayName = "AHA release"
	stored.Aliases = []domain.MailAccountAlias{{Address: "release-bot@example.com"}}
	// Settings updates must not touch status or the last test result.
	stored.Status = domain.MailAccountOK
	stored.LastTestAt = time.Now().UTC().Add(-time.Minute)
	stored.LastTestErrorCode = "stale"
	updated, err := database.UpdateMailAccount(ctx, stored)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Build notices v2" || updated.Address != "release@example.com" {
		t.Fatalf("updated account = %+v", updated)
	}
	if updated.Status != domain.MailAccountUnverified || !updated.LastTestAt.IsZero() ||
		updated.LastTestErrorCode != "" {
		t.Fatalf("settings update overwrote status/test state: %+v", updated)
	}
	if err := database.SetMailAccountStatus(ctx, accountID, domain.MailAccountOK, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	updated, err = database.MailAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.MailAccountOK {
		t.Fatalf("explicit status update = %+v", updated)
	}
	if len(updated.Aliases) != 1 || updated.Aliases[0].Address != "release-bot@example.com" {
		t.Fatalf("replaced aliases = %+v", updated.Aliases)
	}
	if !updated.CredentialConfigured {
		t.Fatal("updating account settings must not clear credential_configured")
	}
	if updated.CredentialRef != "" {
		t.Fatal("updated account read leaked credential_ref")
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("update did not advance updated_at: %v <= %v", updated.UpdatedAt, created.UpdatedAt)
	}

	rotatedRef := domain.MailAccountCredentialRef(accountID)
	if err := database.SetMailAccountCredential(ctx, accountID, time.Now().UTC()); err != nil {
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

	if _, err := database.CreateMailAccount(ctx, domain.MailAccount{
		ID: domain.NewID("mail_account"), Name: "Self alias",
		Address: "self@example.com",
		Aliases: []domain.MailAccountAlias{{Address: "SELF@example.com"}},
	}); !errors.Is(err, ErrMailAddressInUse) {
		t.Fatalf("alias equal to the primary address error = %v, want ErrMailAddressInUse", err)
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

func TestDisabledMailAccountStaysDisabledAcrossCredentialAndHostChanges(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	account, err := database.CreateMailAccount(ctx, domain.MailAccount{
		ID: domain.NewID("mail_account"), Name: "Disabled account",
		Address: "disabled@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetMailAccountStatus(ctx, account.ID, domain.MailAccountDisabled, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := database.SetMailAccountCredential(ctx, account.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stored, err := database.MailAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.MailAccountDisabled {
		t.Fatalf("credential rotation re-enabled disabled account: %#v", stored)
	}
	if !stored.CredentialConfigured {
		t.Fatal("credential rotation did not set credential_configured")
	}
	if !stored.LastTestAt.IsZero() || stored.LastTestErrorCode != "" {
		t.Fatalf("test result was not cleared: %#v", stored)
	}
	if err := database.ResetMailAccountVerification(ctx, account.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stored, err = database.MailAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.MailAccountDisabled {
		t.Fatalf("verification reset re-enabled disabled account: %#v", stored)
	}
	if err := database.SetMailAccountStatus(ctx, account.ID, domain.MailAccountUnverified, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stored, err = database.MailAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.MailAccountUnverified {
		t.Fatalf("explicit enable failed: %#v", stored)
	}
}

func TestMailAccountTestResultRequiresUnchangedEnabledAccount(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	accountID := domain.NewID("mail_account")
	account, err := database.CreateMailAccount(ctx, domain.MailAccount{
		ID: accountID, Name: "Race account", Address: "race@example.com",
		CredentialRef: domain.MailAccountCredentialRef(accountID), CredentialConfigured: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := database.MailAccountCredentialRef(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	expectation := MailAccountTestExpectation{
		Revision: account.Revision, SMTPHost: account.SMTPHost,
		SMTPUsername: account.SMTPUsername, CredentialRef: ref,
	}
	applied, err := database.SetMailAccountTestResult(ctx, accountID, expectation, domain.MailAccountOK, "", time.Now().UTC())
	if err != nil || !applied {
		t.Fatalf("matching test result applied=%v err=%v", applied, err)
	}
	stored, err := database.MailAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.MailAccountOK {
		t.Fatalf("status = %s", stored.Status)
	}

	if err := database.SetMailAccountStatus(ctx, accountID, domain.MailAccountDisabled, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stale := expectation
	applied, err = database.SetMailAccountTestResult(ctx, accountID, stale, domain.MailAccountOK, "", time.Now().UTC())
	if err != nil || applied {
		t.Fatalf("disabled account accepted stale test result: applied=%v err=%v", applied, err)
	}
	stored, err = database.MailAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.MailAccountDisabled {
		t.Fatalf("disabled account status = %s", stored.Status)
	}

	if err := database.SetMailAccountStatus(ctx, accountID, domain.MailAccountUnverified, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	enabled, err := database.MailAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	beforeRotation := MailAccountTestExpectation{
		Revision: enabled.Revision, SMTPHost: enabled.SMTPHost,
		SMTPUsername: enabled.SMTPUsername, CredentialRef: ref,
	}
	// Rotate with the exact timestamp already stored. updated_at cannot detect
	// this change; only the revision counter can.
	if err := database.SetMailAccountCredential(ctx, accountID, enabled.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	rotated, err := database.MailAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if !rotated.UpdatedAt.Equal(enabled.UpdatedAt) {
		t.Fatalf("rotation unexpectedly changed timestamp: %v -> %v", enabled.UpdatedAt, rotated.UpdatedAt)
	}
	if rotated.Revision <= enabled.Revision {
		t.Fatalf("rotation did not advance revision: %d <= %d", rotated.Revision, enabled.Revision)
	}
	applied, err = database.SetMailAccountTestResult(ctx, accountID, beforeRotation, domain.MailAccountOK, "", time.Now().UTC())
	if err != nil || applied {
		t.Fatalf("same-timestamp rotation accepted stale test result: applied=%v err=%v", applied, err)
	}
	stored, err = database.MailAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.MailAccountUnverified {
		t.Fatalf("rotation status = %s", stored.Status)
	}

	beforeHost, err := database.MailAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	beforeHostExpectation := MailAccountTestExpectation{
		Revision: beforeHost.Revision, SMTPHost: beforeHost.SMTPHost,
		SMTPUsername: beforeHost.SMTPUsername, CredentialRef: ref,
	}
	beforeHost.SMTPHost = domain.MailBackupSMTPHost
	if _, err := database.UpdateMailAccount(ctx, beforeHost); err != nil {
		t.Fatal(err)
	}
	applied, err = database.SetMailAccountTestResult(ctx, accountID, beforeHostExpectation, domain.MailAccountOK, "", time.Now().UTC())
	if err != nil || applied {
		t.Fatalf("host change accepted stale test result: applied=%v err=%v", applied, err)
	}
}

func TestMailAccountStaleSettingsUpdateCannotOverwriteStatusOrTestState(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	accountID := domain.NewID("mail_account")
	account, err := database.CreateMailAccount(ctx, domain.MailAccount{
		ID: accountID, Name: "Stale account", Address: "stale@example.com",
		CredentialRef: domain.MailAccountCredentialRef(accountID), CredentialConfigured: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a handler that read the account, then a concurrent disable
	// committed before the settings update was written back.
	stale := account
	if err := database.SetMailAccountStatus(ctx, accountID, domain.MailAccountDisabled, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stale.Name = "Renamed while disabled"
	stale.Status = domain.MailAccountOK
	stale.LastTestAt = time.Now().UTC()
	stale.LastTestErrorCode = "stale-error"
	updated, err := database.UpdateMailAccount(ctx, stale)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.MailAccountDisabled {
		t.Fatalf("stale update overwrote disabled: %#v", updated)
	}
	if !updated.LastTestAt.IsZero() || updated.LastTestErrorCode != "" {
		t.Fatalf("stale update overwrote test state: %#v", updated)
	}
	if updated.Name != "Renamed while disabled" {
		t.Fatalf("settings update did not apply: %#v", updated)
	}

	// The same applies to a rotation: its unverified status/test reset survives
	// a stale settings write.
	if err := database.SetMailAccountStatus(ctx, accountID, domain.MailAccountUnverified, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stale2, err := database.MailAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetMailAccountCredential(ctx, accountID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stale2.Name = "Renamed after rotation"
	stale2.Status = domain.MailAccountOK
	stale2.LastTestAt = time.Now().UTC()
	stale2.LastTestErrorCode = "stale-error"
	updated, err = database.UpdateMailAccount(ctx, stale2)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.MailAccountUnverified {
		t.Fatalf("stale update overwrote rotation status: %#v", updated)
	}
	if !updated.LastTestAt.IsZero() || updated.LastTestErrorCode != "" {
		t.Fatalf("stale update overwrote rotation test state: %#v", updated)
	}
}
