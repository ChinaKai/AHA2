package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestMailTemplateVersionsAreImmutable(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)

	created, err := database.CreateMailTemplate(ctx, domain.MailTemplate{
		Scope: domain.MailTemplateScopeGlobal, Name: "Release notice",
		Description:       "Initial",
		SubjectTemplate:   "[AHA] v1",
		BodyTextTemplate:  "text v1",
		BodyHTMLTemplate:  "<p>html v1</p>",
		DefaultTo:         []string{"qa@example.com"},
		DefaultCC:         []string{"release@example.com"},
		RequiredVariables: []string{"build.version"},
		CreatedBy:         "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.CurrentVersion != 1 || created.Status != domain.MailTemplateStatusActive {
		t.Fatalf("created template = %+v", created)
	}
	version1, err := database.MailTemplateVersion(ctx, created.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if version1.SubjectTemplate != "[AHA] v1" || version1.BodyTextTemplate != "text v1" ||
		len(version1.RequiredVariables) != 1 {
		t.Fatalf("version 1 = %+v", version1)
	}

	created.SubjectTemplate = "[AHA] v2"
	created.BodyTextTemplate = "text v2"
	created.BodyHTMLTemplate = "<p>html v2</p>"
	created.Description = "Second"
	created.DefaultTo = []string{"qa2@example.com"}
	created.RequiredVariables = []string{"build.version", "package.sha256"}
	updated, err := database.UpdateMailTemplate(ctx, created, 1)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CurrentVersion != 2 || updated.SubjectTemplate != "[AHA] v2" ||
		updated.Description != "Second" || len(updated.RequiredVariables) != 2 {
		t.Fatalf("updated template = %+v", updated)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("template update did not advance updated_at: %v <= %v", updated.UpdatedAt, created.UpdatedAt)
	}

	versions, err := database.MailTemplateVersions(ctx, created.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("versions = %+v", versions)
	}
	if versions[1].SubjectTemplate != "[AHA] v1" || versions[1].BodyHTMLTemplate != "<p>html v1</p>" {
		t.Fatalf("version 1 was rewritten: %+v", versions[1])
	}
	if versions[0].SubjectTemplate != "[AHA] v2" {
		t.Fatalf("version 2 content = %+v", versions[0])
	}
	if !versions[0].CreatedAt.After(versions[1].CreatedAt) {
		t.Fatalf("version 2 created_at %v is not newer than version 1 %v", versions[0].CreatedAt, versions[1].CreatedAt)
	}

	created.SubjectTemplate = "stale"
	if _, err := database.UpdateMailTemplate(ctx, created, 1); !errors.Is(err, ErrMailTemplateVersionConflict) {
		t.Fatalf("stale update error = %v, want ErrMailTemplateVersionConflict", err)
	}

	if err := database.ArchiveMailTemplate(ctx, created.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	archived, err := database.MailTemplate(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != domain.MailTemplateStatusArchived {
		t.Fatalf("archived status = %q", archived.Status)
	}
	archived.SubjectTemplate = "after archive"
	if _, err := database.UpdateMailTemplate(ctx, archived, 2); !errors.Is(err, ErrMailTemplateArchived) {
		t.Fatalf("archived update error = %v, want ErrMailTemplateArchived", err)
	}
	if err := database.ArchiveMailTemplate(ctx, created.ID, time.Now().UTC()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second archive error = %v, want sql.ErrNoRows", err)
	}
}

func TestMailTemplateUpdateRollsBackWhenVersionInsertConflicts(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)

	template, err := database.CreateMailTemplate(ctx, domain.MailTemplate{
		Scope: domain.MailTemplateScopeGlobal, Name: "Manual version",
		SubjectTemplate: "v1", BodyTextTemplate: "body",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a competing writer that already produced version 2.
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO mail_template_versions(
			template_id,version,subject_template,body_text_template,body_html_template,
			required_variables_json,created_by,created_at
		) VALUES(?,?,?,?,?,?,?,?)`,
		template.ID, 2, "manual v2", "body", "", "[]", "",
		mailTimeString(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}

	template.SubjectTemplate = "store v2"
	_, err = database.UpdateMailTemplate(ctx, template, 1)
	if !errors.Is(err, ErrMailTemplateVersionConflict) {
		t.Fatalf("duplicate version insert error = %v, want ErrMailTemplateVersionConflict", err)
	}
	stored, err := database.MailTemplate(ctx, template.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CurrentVersion != 1 {
		t.Fatalf("current_version after rollback = %d, want 1", stored.CurrentVersion)
	}
	if stored.SubjectTemplate != "v1" {
		t.Fatalf("template content was not rolled back: %q", stored.SubjectTemplate)
	}
	versions, err := database.MailTemplateVersions(ctx, template.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != 2 || versions[0].SubjectTemplate != "manual v2" {
		t.Fatalf("versions after rollback = %+v", versions)
	}
}

func TestMailTemplateConcurrentUpdatesDoNotDuplicateVersions(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)

	template, err := database.CreateMailTemplate(ctx, domain.MailTemplate{
		Scope: domain.MailTemplateScopeGlobal, Name: "Concurrent",
		SubjectTemplate: "v1", BodyTextTemplate: "body",
	})
	if err != nil {
		t.Fatal(err)
	}

	results := make([]error, 2)
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			candidate := template
			candidate.SubjectTemplate = fmt.Sprintf("concurrent v2 %d", index)
			_, err := database.UpdateMailTemplate(ctx, candidate, 1)
			results[index] = err
		}(index)
	}
	close(start)
	wait.Wait()

	successes, conflicts := 0, 0
	for _, result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, ErrMailTemplateVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent update error: %v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent update results: %d success, %d conflict; want 1/1", successes, conflicts)
	}

	var duplicateCount int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mail_template_versions WHERE template_id=? AND version=2`,
		template.ID).Scan(&duplicateCount); err != nil {
		t.Fatal(err)
	}
	if duplicateCount != 1 {
		t.Fatalf("version 2 rows = %d, want exactly 1", duplicateCount)
	}
	stored, err := database.MailTemplate(ctx, template.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CurrentVersion != 2 {
		t.Fatalf("current_version = %d, want 2", stored.CurrentVersion)
	}
	versions, err := database.MailTemplateVersions(ctx, template.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %+v, want 2", versions)
	}
}

func TestMailTemplateConcurrentUpdatesAcrossConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	template, err := first.CreateMailTemplate(ctx, domain.MailTemplate{
		Scope: domain.MailTemplateScopeGlobal, Name: "Cross connection",
		SubjectTemplate: "v1", BodyTextTemplate: "body",
	})
	if err != nil {
		t.Fatal(err)
	}

	results := make([]error, 2)
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index, store := range []*Store{first, second} {
		wait.Add(1)
		go func(index int, database *Store) {
			defer wait.Done()
			<-start
			candidate := template
			candidate.SubjectTemplate = fmt.Sprintf("cross v2 %d", index)
			_, err := database.UpdateMailTemplate(ctx, candidate, 1)
			results[index] = err
		}(index, store)
	}
	close(start)
	wait.Wait()

	successes, conflicts := 0, 0
	for _, result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, ErrMailTemplateVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected cross-connection update error: %v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("cross-connection updates: %d success, %d conflict; want 1/1", successes, conflicts)
	}
	var duplicates int
	if err := first.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mail_template_versions WHERE template_id=? AND version=2`,
		template.ID).Scan(&duplicates); err != nil {
		t.Fatal(err)
	}
	if duplicates != 1 {
		t.Fatalf("version 2 rows = %d, want 1", duplicates)
	}
}

func TestMailTemplateScopeAndListing(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)

	if _, err := database.CreateMailTemplate(ctx, domain.MailTemplate{
		Scope: domain.MailTemplateScopeProject, Name: "Missing project",
		SubjectTemplate: "subject",
	}); err == nil {
		t.Fatal("project template without project_id was accepted")
	}
	global, err := database.CreateMailTemplate(ctx, domain.MailTemplate{
		Scope: domain.MailTemplateScopeGlobal, ProjectID: "ignored",
		Name: "Global", SubjectTemplate: "subject",
	})
	if err != nil {
		t.Fatal(err)
	}
	if global.ProjectID != "" {
		t.Fatalf("global template project_id = %q, want empty", global.ProjectID)
	}
	projectTemplate, err := database.CreateMailTemplate(ctx, domain.MailTemplate{
		Scope: domain.MailTemplateScopeProject, ProjectID: "project-1",
		Name: "Project", SubjectTemplate: "subject",
	})
	if err != nil {
		t.Fatal(err)
	}

	globals, err := database.MailTemplates(ctx, domain.MailTemplateScopeGlobal, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(globals) != 1 || globals[0].ID != global.ID {
		t.Fatalf("global templates = %+v", globals)
	}
	projectScoped, err := database.MailTemplates(ctx, domain.MailTemplateScopeProject, "project-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(projectScoped) != 1 || projectScoped[0].ID != projectTemplate.ID {
		t.Fatalf("project templates = %+v", projectScoped)
	}
	otherProject, err := database.MailTemplates(ctx, domain.MailTemplateScopeProject, "project-2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherProject) != 0 {
		t.Fatalf("templates leaked across projects: %+v", otherProject)
	}
}
