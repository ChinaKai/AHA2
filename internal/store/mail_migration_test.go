package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
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
		"idx_mail_deliveries_active_message",
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

func TestMailSchemaIndexShapesAndQueryPlans(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)

	indexColumns := func(index string) []string {
		rows, err := database.db.QueryContext(ctx, `PRAGMA index_info(`+index+`)`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var columns []string
		for rows.Next() {
			var seqno, cid int
			var name string
			if err := rows.Scan(&seqno, &cid, &name); err != nil {
				t.Fatal(err)
			}
			columns = append(columns, name)
		}
		return columns
	}
	indexSQL := func(index string) string {
		var sql string
		if err := database.db.QueryRowContext(ctx,
			`SELECT COALESCE(sql,'') FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&sql); err != nil {
			t.Fatal(err)
		}
		return sql
	}

	for _, check := range []struct {
		index   string
		columns []string
	}{
		{"idx_mail_deliveries_claim", []string{"state", "available_at"}},
		{"idx_mail_deliveries_reclaim", []string{"state", "lease_until"}},
		{"idx_mail_deliveries_active_message", []string{"message_id"}},
		{"idx_mail_messages_task", []string{"task_id", "status", "created_at"}},
	} {
		columns := indexColumns(check.index)
		if strings.Join(columns, ",") != strings.Join(check.columns, ",") {
			t.Errorf("%s columns = %v, want %v", check.index, columns, check.columns)
		}
	}
	for _, check := range []struct {
		index    string
		fragment string
	}{
		{"idx_mail_deliveries_claim", "WHERE state = 'queued'"},
		{"idx_mail_deliveries_reclaim", "WHERE state = 'sending'"},
		{"idx_mail_deliveries_active_message", "WHERE state IN ('queued','sending')"},
	} {
		if sql := indexSQL(check.index); !strings.Contains(sql, check.fragment) {
			t.Errorf("%s SQL %q does not contain %q", check.index, sql, check.fragment)
		}
	}

	plan := func(query string, args ...any) string {
		rows, err := database.db.QueryContext(ctx, `EXPLAIN QUERY PLAN `+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var details []string
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		return strings.Join(details, " | ")
	}
	now := mailTimeString(time.Now().UTC())
	if plan := plan(`SELECT id FROM mail_deliveries
		WHERE state='queued' AND available_at<=?
		ORDER BY available_at,created_at LIMIT 1`, now); !strings.Contains(plan, "idx_mail_deliveries_claim") {
		t.Errorf("claim query plan did not use idx_mail_deliveries_claim: %s", plan)
	}
	if plan := plan(`SELECT id FROM mail_deliveries
		WHERE state='sending' AND lease_until<=?
		ORDER BY lease_until LIMIT 1`, now); !strings.Contains(plan, "idx_mail_deliveries_reclaim") {
		t.Errorf("reclaim query plan did not use idx_mail_deliveries_reclaim: %s", plan)
	}
}

func TestV119BusinessDataUpgradesToV120(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	taskID, projectID, _ := createMailTaskFixture(t, database)

	// Roll the mail objects back out to simulate a v119 database that already
	// holds business data. Dropping children first keeps foreign keys happy.
	for _, table := range []string{
		"mail_delivery_attempts", "mail_deliveries", "mail_attachments",
		"mail_messages", "build_artifacts", "mail_bindings",
		"mail_template_versions", "mail_templates", "mail_account_aliases",
		"mail_accounts",
	} {
		if _, err := database.db.ExecContext(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	// Roll back both mail migrations: v121 ALTERs the v120 tables, so leaving
	// v121 recorded would skip the revision column after the tables are dropped.
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version IN (120,121)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen v119 database: %v", err)
	}
	defer upgraded.Close()

	if _, err := upgraded.Task(ctx, taskID); err != nil {
		t.Fatalf("business task did not survive upgrade: %v", err)
	}
	if _, err := upgraded.Project(ctx, projectID); err != nil {
		t.Fatalf("business project did not survive upgrade: %v", err)
	}
	var version int
	if err := upgraded.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 121 {
		t.Fatalf("schema version after upgrade = %d", version)
	}
	account := createMailAccountFixture(t, upgraded, "Upgraded", "upgraded@example.com")
	if _, err := upgraded.MailAccount(ctx, account.ID); err != nil {
		t.Fatalf("mail tables were not recreated: %v", err)
	}
}

func TestSQLiteConstraintErrorClassification(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	account := createMailAccountFixture(t, database, "Classification", "classification@example.com")
	now := mailTimeString(time.Now().UTC())

	_, err := database.db.ExecContext(ctx, `
		INSERT INTO mail_accounts(id,address,smtp_username,created_at,updated_at)
		VALUES(?,?,?,?,?)`,
		domain.NewID("mail_account"), account.Address, account.Address, now, now)
	if err == nil || !isUniqueConstraintError(err) {
		t.Fatalf("duplicate address error = %v, want a typed unique constraint", err)
	}
	if isForeignKeyConstraintError(err) {
		t.Fatalf("unique constraint misclassified as foreign key: %v", err)
	}

	_, err = database.db.ExecContext(ctx, `
		INSERT INTO mail_bindings(id,scope_type,scope_id,account_id,created_at,updated_at)
		VALUES(?,?,?,?,?,?)`,
		domain.NewID("mail_binding"), domain.MailBindingScopeProject,
		"project-classification", "mail_account_missing", now, now)
	if err == nil || !isForeignKeyConstraintError(err) {
		t.Fatalf("missing account error = %v, want a typed foreign key constraint", err)
	}
	if isUniqueConstraintError(err) {
		t.Fatalf("foreign key misclassified as unique: %v", err)
	}
}

func TestMailSchemaV121RevisionColumnIsIdempotent(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	var version int
	if err := database.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 121 {
		t.Fatalf("schema version = %d, want at least 121", version)
	}
	hasRevision := func() bool {
		rows, err := database.db.QueryContext(ctx, `PRAGMA table_info(mail_accounts)`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				t.Fatal(err)
			}
			if name == "revision" {
				return true
			}
		}
		return false
	}
	if !hasRevision() {
		t.Fatal("mail_accounts.revision column is missing")
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=121`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("re-applying schema v121: %v", err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("applying schema v121 a third time: %v", err)
	}
	if !hasRevision() {
		t.Fatal("re-migration dropped or failed to keep revision")
	}
	var recorded int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=121`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Fatalf("schema v121 recorded %d times, want 1", recorded)
	}
	account := createMailAccountFixture(t, database, "V121", "v121@example.com")
	stored, err := database.MailAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 0 {
		t.Fatalf("new account revision = %d, want 0", stored.Revision)
	}
}
