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
	otherProject, err := service.CreateProject(ctx, domain.Project{
		ID: "p529keep", Name: "Keep Me", ProjectType: "folder",
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
		if _, err := database.ReplaceKnowledgeBindings(ctx, consumer.kind, consumer.id, []domain.KnowledgeBinding{{SpaceProject: project.ID}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelInstance, "channel-keep", []domain.KnowledgeBinding{{SpaceProject: otherProject.ID}}, nil); err != nil {
		t.Fatal(err)
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
	keptBindings, err := database.ListKnowledgeBindings(ctx, domain.KnowledgeBindingChannelInstance, "channel-keep")
	if err != nil {
		t.Fatal(err)
	}
	if len(keptBindings) != 1 || keptBindings[0].SpaceProject != otherProject.ID {
		t.Fatalf("another project's binding was removed: %#v", keptBindings)
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
	if err == nil || !strings.Contains(err.Error(), "已删除") || !strings.Contains(err.Error(), "归档失败") {
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
	service, database, manager, config := newSpaceTestService(t)
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
	// A failed archive attempt stays in Orphans and retries next startup.
	service.knowledgeVault = archiveFailureVault{
		knowledgeVaultBackend: manager,
		err:                   errors.New("simulated orphan archive failure"),
	}
	failed, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stillOrphan := false
	for _, id := range failed.Orphans {
		if id == project.ID {
			stillOrphan = true
		}
	}
	if !stillOrphan {
		t.Fatalf("failed orphan archive was dropped from the report: %#v", failed)
	}
	preserved, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if preserved.Projects[project.ID] == "" {
		t.Fatalf("failed orphan archive removed the manifest mapping: %#v", preserved.Projects)
	}

	service.knowledgeVault = manager
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

func TestReconcileRepairsIndexAfterArchiveRebuildFailure(t *testing.T) {
	service, database, manager, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "p529stale", Name: "Stale Index", ProjectType: "folder",
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
	writeProjectKnowledgeDoc(t, config, "projects/"+slug+"/notes/retained.md", "重建失败后保留", "stale-archive-marker-529")
	if _, err := manager.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	// Simulate the exact partial state: the directory move and manifest update
	// succeeded, but the index rebuild failed before the rows were replaced.
	if _, err := knowledgevault.ArchiveProjectSpace(config, project.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := manager.WithReader(func(reader *knowledgevault.Reader) error {
		stale, err := reader.Entries(ctx, "project", project.ID)
		if err != nil {
			return err
		}
		if len(stale) == 0 {
			t.Fatal("test setup did not retain the stale project index rows")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteProjectWithSyncTombstone(ctx, project.ID); err != nil {
		t.Fatal(err)
	}

	report, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Warnings) == 0 || !strings.Contains(strings.Join(report.Warnings, "\n"), project.ID) {
		t.Fatalf("stale archive index was not reported: %#v", report)
	}
	if err := manager.WithReader(func(reader *knowledgevault.Reader) error {
		live, err := reader.Entries(ctx, "project", project.ID)
		if err != nil {
			return err
		}
		if len(live) != 0 {
			t.Fatalf("old project entries survived the reconcile rebuild: %#v", live)
		}
		found, err := reader.Search(ctx, knowledgevault.DocumentSearchOptions{
			Query: "stale-archive-marker-529", Limit: 10, IncludeArchives: true,
		})
		if err != nil {
			return err
		}
		if len(found.Results) != 1 || found.Results[0].Scope != "archive" {
			t.Fatalf("archived knowledge is not searchable after repair: %#v", found.Results)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateProjectRejectsReusedArchivedProjectID(t *testing.T) {
	service, database, _, config := newSpaceTestService(t)
	ctx := context.Background()
	enablePetProjectAdmin(t, service)
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "p529reuse", Name: "Reuse Me", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	originalSlug := manifest.Projects[project.ID]
	if err := service.deleteProjectNow(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateProject(ctx, domain.Project{
		ID: project.ID, Name: "Reused ID", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	var spaceErr *KnowledgeSpaceError
	if !errors.As(err, &spaceErr) || spaceErr.Code != "project_id_archived" {
		t.Fatalf("reused archived project id error = %#v", err)
	}
	if _, lookupErr := database.Project(ctx, project.ID); lookupErr == nil {
		t.Fatal("reused archived project id still created a project row")
	}
	after, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Archives[project.ID] == "" {
		t.Fatalf("archived mapping disappeared: %#v", after.Archives)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "archive", filepath.FromSlash(after.Archives[project.ID]))); err != nil {
		t.Fatalf("original archive missing: %v (old slug %s)", err, originalSlug)
	}
}

func TestReconcileRefusesSyncProjectReusingArchivedID(t *testing.T) {
	service, database, _, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "p529sync", Name: "Sync Reuse", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgevault.ArchiveProjectSpace(config, project.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteProjectWithSyncTombstone(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	// A sync/import replay calls store.CreateProject directly, bypassing the
	// Service.CreateProject guard. Provisioning must still refuse the ID.
	replay := domain.Project{
		ID: project.ID, Name: "Sync Replay", ProjectType: "folder",
		KnowledgePolicy: "enabled", KnowledgeState: domain.KnowledgeStateReady,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateProject(ctx, replay); err != nil {
		t.Fatal(err)
	}
	report, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := database.Project(ctx, project.ID)
	if err != nil || stored.KnowledgeState != domain.KnowledgeStateError {
		t.Fatalf("sync replay project state = %q err=%v", stored.KnowledgeState, err)
	}
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := manifest.Projects[project.ID]; exists {
		t.Fatalf("sync replay created a live mapping beside the archive: %#v", manifest.Projects)
	}
	if manifest.Archives[project.ID] == "" {
		t.Fatalf("archive mapping disappeared: %#v", manifest.Archives)
	}
	warnings := strings.Join(report.Warnings, "\n")
	if !strings.Contains(warnings, project.ID) || !strings.Contains(warnings, "project_id_archived") {
		t.Fatalf("sync replay refusal was not reported: %#v", report)
	}
	markedError := false
	for _, id := range report.MarkedError {
		if id == project.ID {
			markedError = true
		}
	}
	if !markedError {
		t.Fatalf("ready sync replay was not marked error: %#v", report)
	}
	if !strings.Contains(warnings, "删除这个项目行或改用新的项目 ID") {
		t.Fatalf("sync replay has no recovery path in the warning: %#v", report)
	}
}

func TestCreateProjectRejectsReuseDuringArchiveManifestWindow(t *testing.T) {
	service, database, _, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "p529window", Name: "Window Reuse", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(config.VaultPath, ".aha-vault.json")
	beforeArchive, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteProjectWithSyncTombstone(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	archivedPath, err := knowledgevault.ArchiveProjectSpace(config, project.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the write failure window: the directory moved, but the old
	// manifest (projects mapping, no archive mapping) is still on disk.
	if err := os.WriteFile(manifestPath, beforeArchive, 0o644); err != nil {
		t.Fatal(err)
	}
	if archived, ok := knowledgevault.ArchivedProjectSpace(config, project.ID); !ok {
		t.Fatalf("archive directory scan did not claim the moved ID: %s", archived)
	}
	_, err = service.CreateProject(ctx, domain.Project{
		ID: project.ID, Name: "Reused", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	var spaceErr *KnowledgeSpaceError
	if !errors.As(err, &spaceErr) || spaceErr.Code != "project_id_archived" {
		t.Fatalf("window reuse error = %#v", err)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash(archivedPath), "index.md")); err != nil {
		t.Fatalf("archived files were disturbed: %v", err)
	}
	if _, err := database.Project(ctx, project.ID); err == nil {
		t.Fatal("window reuse created a project row")
	}
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Archives[project.ID] != "" {
		t.Fatalf("window reuse wrote an archive mapping: %#v", manifest.Archives)
	}
}

func TestCreateProjectAllowsReuseAfterArchiveDirectoryRemoved(t *testing.T) {
	service, database, _, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "p529deletedarchive", Name: "Deleted Archive", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	archivedPath, err := knowledgevault.ArchiveProjectSpace(config, project.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteProjectWithSyncTombstone(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(config.VaultPath, filepath.FromSlash(archivedPath))); err != nil {
		t.Fatal(err)
	}
	reused, err := service.CreateProject(ctx, domain.Project{
		ID: project.ID, Name: "Reused", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("stale archive mapping blocked reuse: %v", err)
	}
	if reused.KnowledgeState != domain.KnowledgeStateReady {
		t.Fatalf("reused project state = %q", reused.KnowledgeState)
	}
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Projects[project.ID] == "" {
		t.Fatalf("reused project has no live mapping: %#v", manifest.Projects)
	}
	if manifest.Archives[project.ID] != "" {
		t.Fatalf("stale archive mapping survived: %#v", manifest.Archives)
	}
}

func TestReconcileWarnsAboutArchiveWithoutRootIndex(t *testing.T) {
	service, database, _, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "p529noroot", Name: "No Root", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	archivedPath, err := knowledgevault.ArchiveProjectSpace(config, project.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteProjectWithSyncTombstone(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(config.VaultPath, filepath.FromSlash(archivedPath), "index.md")
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	report, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(report.Warnings, "\n"), "缺少根 index.md") {
		t.Fatalf("missing archive root was not reported: %#v", report)
	}
}

func TestReconcileRepairsMissingArchiveRoot(t *testing.T) {
	service, database, manager, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "p529repair", Name: "Missing Root", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	archivedPath, err := knowledgevault.ArchiveProjectSpace(config, project.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteProjectWithSyncTombstone(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	archiveDir := filepath.Join(config.VaultPath, filepath.FromSlash(archivedPath))
	if err := os.RemoveAll(archiveDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A rebuild backfills the archive root page from the manifest mapping, so
	// remove it again to simulate a page that disappeared after indexing.
	if _, err := manager.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(archiveDir, "index.md")); err != nil {
		t.Fatal(err)
	}

	report, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(archiveDir, "index.md")); statErr != nil {
		t.Fatalf("archive root page was not recreated: %v", statErr)
	}
	warnings := strings.Join(report.Warnings, "\n")
	if !strings.Contains(warnings, "缺少根 index.md") || !strings.Contains(warnings, "已安排重建补全目录页") {
		t.Fatalf("missing archive root was not reported as repairable: %#v", report)
	}
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Archives[project.ID] == "" {
		t.Fatalf("archive mapping was lost during repair: %#v", manifest.Archives)
	}
}

func TestReconcileNonReadySyncReplayShowsRecoveryPath(t *testing.T) {
	service, database, _, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "p529syncuninit", Name: "Sync Unready", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgevault.ArchiveProjectSpace(config, project.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteProjectWithSyncTombstone(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	// Sync replay without a local knowledge state: provisioning fails and the
	// row stays in error until the owner removes it or uses a new ID.
	if err := database.CreateProject(ctx, domain.Project{
		ID: project.ID, Name: "Sync Replay", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	report, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := database.Project(ctx, project.ID)
	if err != nil || stored.KnowledgeState != domain.KnowledgeStateError {
		t.Fatalf("non-ready replay state = %q err=%v", stored.KnowledgeState, err)
	}
	warnings := strings.Join(report.Warnings, "\n")
	if !strings.Contains(warnings, "project_id_archived") || !strings.Contains(warnings, "删除这个项目行或改用新的项目 ID") {
		t.Fatalf("non-ready replay warning has no recovery path: %#v", report)
	}
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := manifest.Projects[project.ID]; exists {
		t.Fatalf("non-ready replay created a live mapping: %#v", manifest.Projects)
	}
}
