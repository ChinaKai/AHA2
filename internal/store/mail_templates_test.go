package store

import (
	"context"
	"errors"
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
