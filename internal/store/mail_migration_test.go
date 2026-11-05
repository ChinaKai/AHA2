package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestMailSchemaV120MigrationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)

	var version int
	if err := database.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 120 {
		t.Fatalf("schema version = %d, want at least 120", version)
	}

	tables := []string{
		"mail_accounts", "mail_account_aliases", "mail_templates",
		"mail_template_versions", "mail_bindings", "build_artifacts",
		"mail_messages", "mail_attachments", "mail_deliveries",
		"mail_delivery_attempts",
	}
	for _, table := range tables {
		var exists bool
		if err := database.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("table %s was not created", table)
		}
	}

	indexes := []string{
		"idx_mail_accounts_address", "idx_mail_accounts_status",
		"idx_mail_aliases_address", "idx_mail_templates_active_name",
		"idx_mail_templates_project", "idx_mail_bindings_scope",
		"idx_mail_bindings_account", "idx_build_artifacts_task",
		"idx_mail_messages_task", "idx_mail_messages_pending",
		"idx_mail_messages_account", "idx_mail_messages_dedup",
		"idx_mail_messages_fingerprint", "idx_mail_attachments_message",
		"idx_mail_attachments_source", "idx_mail_attachments_link_hash",
		"idx_mail_deliveries_claim", "idx_mail_deliveries_reclaim",
		"idx_mail_deliveries_account", "idx_mail_deliveries_claim_account",
		"idx_mail_delivery_attempts_delivery",
	}
	for _, index := range indexes {
		var exists bool
		if err := database.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='index' AND name=?)`, index).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("index %s was not created", index)
		}
	}

	account := createMailAccountFixture(t, database, "Idempotent", "idempotent@example.com")

	// Re-running a partially marked migration must be a no-op: all DDL uses
	// IF NOT EXISTS and no data is rewritten.
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=120`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("re-applying schema v120: %v", err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("applying schema v120 a third time: %v", err)
	}
	var recorded int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=120`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Fatalf("schema v120 recorded %d times, want 1", recorded)
	}
	if _, err := database.MailAccount(ctx, account.ID); err != nil {
		t.Fatalf("existing data did not survive re-migration: %v", err)
	}
}

func TestMailTimeStringsAreFixedWidthAndOrdered(t *testing.T) {
	base := time.Date(2026, 11, 5, 15, 4, 5, 100000000, time.UTC)
	later := base.Add(time.Nanosecond)
	first, second := mailTimeString(base), mailTimeString(later)
	if len(first) != 30 || len(second) != 30 {
		t.Fatalf("mail time width = %d/%d, want 30: %q %q", len(first), len(second), first, second)
	}
	if first >= second {
		t.Fatalf("fixed-width times do not sort: %q >= %q", first, second)
	}
	if parsed := parseMailTime(first); !parsed.Equal(base) {
		t.Fatalf("round trip = %v, want %v", parsed, base)
	}
}

func TestMailTemplateVersionPrimaryKeyIsPerTemplate(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)

	first, err := database.CreateMailTemplate(ctx, domain.MailTemplate{
		Scope: domain.MailTemplateScopeGlobal, Name: "First",
		SubjectTemplate: "first v1", BodyTextTemplate: "body",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateMailTemplate(ctx, domain.MailTemplate{
		Scope: domain.MailTemplateScopeGlobal, Name: "Second",
		SubjectTemplate: "second v1", BodyTextTemplate: "body",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Both templates own a v1; the version number is scoped by template_id, not
	// globally unique.
	for _, template := range []domain.MailTemplate{first, second} {
		if _, err := database.MailTemplateVersion(ctx, template.ID, 1); err != nil {
			t.Fatalf("template %s v1: %v", template.ID, err)
		}
	}

	first.SubjectTemplate = "first v2"
	if _, err := database.UpdateMailTemplate(ctx, first, 1); err != nil {
		t.Fatal(err)
	}
	second.SubjectTemplate = "second v2"
	if _, err := database.UpdateMailTemplate(ctx, second, 1); err != nil {
		t.Fatal(err)
	}
	firstV2, err := database.MailTemplateVersion(ctx, first.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	secondV2, err := database.MailTemplateVersion(ctx, second.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if firstV2.SubjectTemplate != "first v2" || secondV2.SubjectTemplate != "second v2" {
		t.Fatalf("per-template v2 snapshots: %+v / %+v", firstV2, secondV2)
	}

	var version1Count, version2Count int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mail_template_versions WHERE version=1`).Scan(&version1Count); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mail_template_versions WHERE version=2`).Scan(&version2Count); err != nil {
		t.Fatal(err)
	}
	if version1Count != 2 || version2Count != 2 {
		t.Fatalf("version row counts = %d/%d, want 2/2", version1Count, version2Count)
	}

	// The composite primary key still rejects a duplicate within one template.
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO mail_template_versions(
			template_id,version,subject_template,body_text_template,body_html_template,
			required_variables_json,created_by,created_at
		) VALUES(?,?,?,?,?,?,?,?)`,
		first.ID, 2, "duplicate", "", "", "[]", "",
		mailTimeString(time.Now().UTC())); err == nil {
		t.Fatal("duplicate (template_id, version) passed the composite primary key")
	}
}

func TestMailSchemaV120Constraints(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)

	account, err := database.CreateMailAccount(ctx, domain.MailAccount{
		ID: domain.NewID("mail_account"), Name: "Constraints",
		Address: "constraints@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx,
		`UPDATE mail_accounts SET status='bogus' WHERE id=?`, account.ID); err == nil {
		t.Fatal("invalid account status passed the CHECK constraint")
	}

	if _, err := database.CreateMailAccount(ctx, domain.MailAccount{
		ID: domain.NewID("mail_account"), Name: "Duplicate",
		Address: "CONSTRAINTS@example.com",
	}); !errors.Is(err, ErrMailAddressInUse) {
		t.Fatalf("duplicate case-insensitive address error = %v, want ErrMailAddressInUse", err)
	}

	aliasConflict, err := database.CreateMailAccount(ctx, domain.MailAccount{
		ID: domain.NewID("mail_account"), Name: "Alias conflict",
		Address: "other@example.com",
		Aliases: []domain.MailAccountAlias{{Address: "constraints@example.com"}},
	})
	if !errors.Is(err, ErrMailAddressInUse) {
		t.Fatalf("alias conflict error = %v, want ErrMailAddressInUse; account=%+v", err, aliasConflict)
	}

	template, err := database.CreateMailTemplate(ctx, domain.MailTemplate{
		Scope: domain.MailTemplateScopeGlobal, Name: "Release",
		SubjectTemplate: "Release", BodyTextTemplate: "Body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateMailTemplate(ctx, domain.MailTemplate{
		Scope: domain.MailTemplateScopeGlobal, Name: "Release",
		SubjectTemplate: "Release", BodyTextTemplate: "Body",
	}); err == nil {
		t.Fatal("duplicate active template name passed the partial unique index")
	}
	if err := database.ArchiveMailTemplate(ctx, template.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateMailTemplate(ctx, domain.MailTemplate{
		Scope: domain.MailTemplateScopeGlobal, Name: "Release",
		SubjectTemplate: "Release", BodyTextTemplate: "Body",
	}); err != nil {
		t.Fatalf("archived name should not block a new active template: %v", err)
	}

	taskID, _, _ := createMailTaskFixture(t, database)
	createMailMessageFixture(t, database, taskID, account.ID, "same-key")
	if _, err := database.CreateMailMessage(ctx, domain.MailMessage{
		TaskID: taskID, AccountID: account.ID, Source: domain.MailMessageSourceAgent,
		IdempotencyKey: "same-key", FromAddress: "noreply@example.com",
		To: []string{"qa@example.com"}, Subject: "Duplicate",
	}); err == nil {
		t.Fatal("duplicate task idempotency key passed the unique constraint")
	}
}
