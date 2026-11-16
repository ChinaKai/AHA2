package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// archiveFailureVault keeps the real vault for every read and provisioning
// path but fails the archive step, which is the deletion failure the owner
// must be able to retry.
type archiveFailureVault struct {
	knowledgeVaultBackend
	err error
}

func (v archiveFailureVault) ArchiveProjectSpace(context.Context, string) (string, error) {
	return "", v.err
}

func enablePetProjectAdmin(t *testing.T, service *Service) {
	t.Helper()
	if _, err := service.OwnerAssistant(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateOwnerPetSettings(context.Background(), PetSettingsInput{
		Enabled: true, Actions: []string{"walk"}, IdleMinutes: 5,
		QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowCreateWorkspaces: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
}

func writeProjectKnowledgeDoc(t *testing.T, config knowledgevault.Config, relative, title, marker string) {
	t.Helper()
	raw := "---\n" +
		"title: " + title + "\n" +
		"slug: retained-note\n" +
		"scope: project\n" +
		"type: reference\n" +
		"status: verified\n" +
		"---\n# " + title + "\n\n" + marker + "\n"
	absolute := filepath.Join(config.VaultPath, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteProjectArchivesVaultSpaceAndCleansBindings(t *testing.T) {
	service, database, manager, config := newSpaceTestService(t)
	ctx := context.Background()
	enablePetProjectAdmin(t, service)
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "p529delete", Name: "Delete Me", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	slug := manifest.Projects[project.ID]
	if slug == "" {
		t.Fatalf("project has no vault space: %#v", manifest.Projects)
	}
	writeProjectKnowledgeDoc(t, config, "projects/"+slug+"/notes/retained.md", "删除后保留", "delete-archive-marker-529")
	if _, err := manager.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	for _, consumer := range []struct{ kind, id string }{
		{domain.KnowledgeBindingChannelInstance, "channel-529"},
		{domain.KnowledgeBindingChannelEndpoint, "channel-529::assistant_dm"},
	} {
		if _, err := database.ReplaceKnowledgeBindings(ctx, consumer.kind, consumer.id, []domain.KnowledgeBinding{{SpaceProject: project.ID}}); err != nil {
			t.Fatal(err)
		}
	}

	if err := service.deleteProjectNow(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Project(ctx, project.ID); err == nil {
		t.Fatal("project row still exists after deleteProjectNow")
	}
	for _, consumer := range []struct{ kind, id string }{
		{domain.KnowledgeBindingChannelInstance, "channel-529"},
		{domain.KnowledgeBindingChannelEndpoint, "channel-529::assistant_dm"},
	} {
		bindings, err := database.ListKnowledgeBindings(ctx, consumer.kind, consumer.id)
		if err != nil {
			t.Fatal(err)
		}
		if len(bindings) != 0 {
			t.Fatalf("%s bindings survived the project delete: %#v", consumer.kind, bindings)
		}
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "projects", slug)); !os.IsNotExist(err) {
		t.Fatalf("live knowledge directory still exists: %v", err)
	}
	after, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := after.Projects[project.ID]; exists {
		t.Fatalf("manifest still maps the deleted project: %#v", after.Projects)
	}
	archivedSlug := after.Archives[project.ID]
	if archivedSlug == "" {
		t.Fatalf("deleted project was not archived: %#v", after.Archives)
	}
	archivedDoc := filepath.Join(config.VaultPath, filepath.FromSlash("archive/"+archivedSlug), "notes", "retained.md")
	if _, err := os.Stat(archivedDoc); err != nil {
		t.Fatalf("archived document missing: %v", err)
	}
	if err := manager.WithReader(func(reader *knowledgevault.Reader) error {
		live, err := reader.Entries(ctx, "project", project.ID)
		if err != nil {
			return err
		}
		if len(live) != 0 {
			t.Fatalf("deleted project still has indexed entries: %#v", live)
		}
		found, err := reader.Search(ctx, knowledgevault.DocumentSearchOptions{
			Query: "delete-archive-marker-529", Limit: 10, IncludeArchives: true,
		})
		if err != nil {
			return err
		}
		if len(found.Results) != 1 || !found.Results[0].Archive {
			t.Fatalf("archived knowledge is not searchable: %#v", found.Results)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	events, err := database.ListPetEvents(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	seenDeleted := false
	for _, event := range events {
		if event.Kind == "project_delete_cleanup" && event.Detail == project.ID {
			t.Fatalf("successful delete wrote a cleanup failure event: %#v", event)
		}
		if event.Kind == "project_deleted" && event.Detail == project.ID {
			seenDeleted = true
		}
	}
	if !seenDeleted {
		t.Fatalf("project_deleted event missing: %#v", events)
	}
}

func TestDeleteProjectArchiveFailureIsReportedAndRetriedByReconcile(t *testing.T) {
	service, database, manager, config := newSpaceTestService(t)
	ctx := context.Background()
	enablePetProjectAdmin(t, service)
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "p529retry", Name: "Retry Me", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	slug := manifest.Projects[project.ID]
	service.knowledgeVault = archiveFailureVault{
		knowledgeVaultBackend: manager,
		err:                   errors.New("simulated archive io failure"),
	}

	err = service.deleteProjectNow(ctx, project.ID)
	if err == nil || !strings.Contains(err.Error(), "项目已删除") || !strings.Contains(err.Error(), "归档失败") {
		t.Fatalf("delete error = %v, want a retryable archive error", err)
	}
	if _, lookupErr := database.Project(ctx, project.ID); lookupErr == nil {
		t.Fatal("project row survived a failed archive step")
	}
	events, err := database.ListPetEvents(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	cleanupEvent := false
	for _, event := range events {
		if event.Kind == "project_delete_cleanup" && event.Detail == project.ID &&
			strings.Contains(event.Status, "simulated archive io failure") {
			cleanupEvent = true
		}
	}
	if !cleanupEvent {
		t.Fatalf("project_delete_cleanup event missing: %#v", events)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "projects", slug)); err != nil {
		t.Fatalf("failed archive already moved the live space: %v", err)
	}

	// Startup reconciliation is the retry path: the project row is gone but
	// the manifest still lists the space, so the orphan is archived.
	service.knowledgeVault = manager
	report, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	archived := false
	for _, id := range report.Archived {
		if id == project.ID {
			archived = true
		}
	}
	if !archived {
		t.Fatalf("orphan was not archived on reconcile: %#v", report)
	}
	after, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := after.Projects[project.ID]; exists {
		t.Fatalf("manifest still maps the retried orphan: %#v", after.Projects)
	}
	archivedSlug := after.Archives[project.ID]
	if archivedSlug == "" {
		t.Fatalf("retried orphan has no archive mapping: %#v", after.Archives)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "projects", slug)); !os.IsNotExist(err) {
		t.Fatalf("orphan live directory still exists after retry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash("archive/"+archivedSlug), "index.md")); err != nil {
		t.Fatalf("archived orphan root missing: %v", err)
	}
}

func TestReconcileArchivesManifestOrphans(t *testing.T) {
	service, database, _, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "p529orphan", Name: "Orphan Me", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	slug := manifest.Projects[project.ID]
	// Simulate a project row removed outside deleteProjectNow (or a crash
	// between the graph delete and the archive step).
	if err := database.DeleteProjectWithSyncTombstone(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	report, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Orphans) != 0 {
		t.Fatalf("orphans remain after reconcile: %#v", report.Orphans)
	}
	archived := false
	for _, id := range report.Archived {
		if id == project.ID {
			archived = true
		}
	}
	if !archived {
		t.Fatalf("orphan was not archived: %#v", report)
	}
	after, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := after.Projects[project.ID]; exists {
		t.Fatalf("orphan still mapped as a project: %#v", after.Projects)
	}
	if after.Archives[project.ID] == "" {
		t.Fatalf("orphan has no archive mapping: %#v", after.Archives)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "projects", slug)); !os.IsNotExist(err) {
		t.Fatalf("orphan live directory still exists: %v", err)
	}
}
