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
	"github.com/ChinaKai/AHA2/internal/store"
)

type provisioningVault struct {
	failingKnowledgeVault
	failProvision bool
}

func (v provisioningVault) CheckProjectSpacePrerequisites() error { return nil }
func (v provisioningVault) ProvisionProjectSpace(ctx context.Context, projectID, name string) (knowledgevault.ProjectSpaceResult, error) {
	if v.failProvision {
		return knowledgevault.ProjectSpaceResult{}, errors.New("simulated io failure")
	}
	return knowledgevault.ProjectSpaceResult{ProjectID: projectID, Slug: "fake", Created: true}, nil
}

func newSpaceTestService(t *testing.T) (*Service, *store.Store, *knowledgevault.Manager, knowledgevault.Config) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	config := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "Spaces"}); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	service := NewService(database, nil, nil)
	service.SetKnowledgeVault(manager, true)
	service.SetKnowledgeVaultRuntimeWrite(true)
	return service, database, manager, config
}

func TestCreateProjectInitializesItsVaultSpace(t *testing.T) {
	service, database, manager, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "project-new-space", Name: "New Space", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if project.KnowledgeState != domain.KnowledgeStateReady {
		t.Fatalf("created project state = %q", project.KnowledgeState)
	}
	stored, err := database.Project(ctx, project.ID)
	if err != nil || stored.KnowledgeState != domain.KnowledgeStateReady {
		t.Fatalf("stored project = %#v err=%v", stored, err)
	}
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	slug := manifest.Projects[project.ID]
	if slug == "" {
		t.Fatalf("manifest has no mapping for %s: %#v", project.ID, manifest.Projects)
	}
	root := filepath.Join(config.VaultPath, "projects", slug, "index.md")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("project root index missing: %v", err)
	}
	// The index was rebuilt as part of the same operation, so the new space is
	// immediately searchable without a manual rebuild.
	found := false
	if err := manager.WithReader(func(reader *knowledgevault.Reader) error {
		entries, err := reader.Entries(ctx, "project", project.ID)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsIndex && entry.Path == "projects/"+slug+"/index.md" {
				found = true
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("new project root index is not searchable")
	}
	// Repeated initialization reuses the slug and never rewrites the page.
	if _, err := manager.ProvisionProjectSpace(ctx, project.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	manifestAfter, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil || manifestAfter.Projects[project.ID] != slug {
		t.Fatalf("slug changed on retry: %q err=%v", manifestAfter.Projects[project.ID], err)
	}
}

func TestCreateProjectFailureLeavesRetryableErrorState(t *testing.T) {
	service, database, manager, config := newSpaceTestService(t)
	ctx := context.Background()
	service.knowledgeVault = provisioningVault{failProvision: true}
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "project-retry", Name: "Retry", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err == nil {
		t.Fatal("expected the provisioning failure to be reported")
	}
	var spaceErr *KnowledgeSpaceError
	if !errors.As(err, &spaceErr) || spaceErr.Code != "knowledge_space_init_failed" {
		t.Fatalf("error = %#v", err)
	}
	if project.KnowledgeState != domain.KnowledgeStateError {
		t.Fatalf("returned state = %q", project.KnowledgeState)
	}
	stored, err := database.Project(ctx, project.ID)
	if err != nil || stored.KnowledgeState != domain.KnowledgeStateError {
		t.Fatalf("stored state = %q err=%v", stored.KnowledgeState, err)
	}
	// No legacy root was created as a fallback.
	if _, err := database.KnowledgeRootForProject(ctx, project.ID); err == nil {
		t.Fatal("failed vault provisioning created a legacy knowledge root")
	}
	// Retry after fixing the vault reaches ready without creating a second row.
	service.knowledgeVault = manager
	retried, err := service.RetryProjectKnowledgeSpace(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.KnowledgeState != domain.KnowledgeStateReady {
		t.Fatalf("retried state = %q", retried.KnowledgeState)
	}
	root, ok := knowledgevault.SpaceRootPath(config, project.ID)
	if !ok {
		t.Fatal("retry did not create a manifest mapping")
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash(root))); err != nil {
		t.Fatalf("retry root missing: %v", err)
	}
}

func TestCreateProjectRejectsVaultPrerequisiteBeforeInsert(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	config := knowledgevault.DefaultConfig(t.TempDir())
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(database, nil, nil)
	service.SetKnowledgeVault(manager, true)
	now := time.Now().UTC()
	_, err = service.CreateProject(ctx, domain.Project{
		ID: "project-blocked", Name: "Blocked", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	var spaceErr *KnowledgeSpaceError
	if !errors.As(err, &spaceErr) || !IsKnowledgeSpaceNotReadyError(err) {
		t.Fatalf("error = %#v", err)
	}
	if _, lookupErr := database.Project(ctx, "project-blocked"); lookupErr == nil {
		t.Fatal("prerequisite failure still inserted the project row")
	}
}

func TestChannelBindingsReplaceAndRejectUnreadySpaces(t *testing.T) {
	service, database, _, _ := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	first, err := service.CreateProject(ctx, domain.Project{
		ID: "project-bound-one", Name: "Bound One", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateProject(ctx, domain.Project{
		ID: "project-bound-two", Name: "Bound Two", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := service.ReplaceChannelKnowledgeBindings(ctx, "chan-bind", "",
		[]domain.KnowledgeBinding{{SpaceProject: first.ID, AccessMode: "read_write"}}, boolPointer(false))
	if err != nil || len(bindings) != 1 || bindings[0].SpaceProject != first.ID {
		t.Fatalf("instance bindings = %#v err=%v", bindings, err)
	}
	// Endpoint-level binding replaces the instance default.
	bindings, err = service.ReplaceChannelKnowledgeBindings(ctx, "chan-bind", "assistant_dm",
		[]domain.KnowledgeBinding{{SpaceProject: second.ID}}, boolPointer(false))
	if err != nil || len(bindings) != 1 || bindings[0].SpaceProject != second.ID {
		t.Fatalf("endpoint bindings = %#v err=%v", bindings, err)
	}
	resolved, err := service.ChannelKnowledgeBindings(ctx, "chan-bind", "assistant_dm")
	if err != nil || len(resolved) != 1 || resolved[0].SpaceProject != second.ID {
		t.Fatalf("endpoint resolution = %#v err=%v", resolved, err)
	}
	resolved, err = service.ChannelKnowledgeBindings(ctx, "chan-bind", "group_digital_human")
	if err != nil || len(resolved) != 1 || resolved[0].SpaceProject != first.ID {
		t.Fatalf("instance fallback = %#v err=%v", resolved, err)
	}
	// An uninitialized project is not a valid binding target.
	if err := database.SetProjectKnowledgeState(ctx, second.ID, domain.KnowledgeStateUninitialized); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReplaceChannelKnowledgeBindings(ctx, "chan-bind", "assistant_dm",
		[]domain.KnowledgeBinding{{SpaceProject: second.ID}}, boolPointer(false)); !errors.Is(err, ErrKnowledgeSpaceNotReady) {
		t.Fatalf("unready binding error = %v", err)
	}
}

func TestReconcileKnowledgeSpacesProvisionsNormalProjectsAndSkipsInfrastructure(t *testing.T) {
	service, database, _, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	legacy := domain.Project{ID: "project-reconcile", Name: "Reconcile", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	channelHost := domain.Project{ID: "project-channel-host", Name: "Channel Host", ProjectType: "channel", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, channelHost); err != nil {
		t.Fatal(err)
	}
	systemProject := domain.Project{ID: "project-system-host", Name: "Pet", ProjectType: "system", KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, systemProject); err != nil {
		t.Fatal(err)
	}
	report, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Enabled {
		t.Fatal("reconciliation did not run")
	}
	provisioned := map[string]bool{}
	for _, id := range report.Provisioned {
		provisioned[id] = true
	}
	if !provisioned[legacy.ID] {
		t.Fatalf("normal project %s was not provisioned: %#v", legacy.ID, report)
	}
	stored, err := database.Project(ctx, legacy.ID)
	if err != nil || stored.KnowledgeState != domain.KnowledgeStateReady {
		t.Fatalf("project %s state=%q err=%v", legacy.ID, stored.KnowledgeState, err)
	}
	root, ok := knowledgevault.SpaceRootPath(config, legacy.ID)
	if !ok {
		t.Fatalf("project %s has no manifest mapping", legacy.ID)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash(root))); err != nil {
		t.Fatalf("project %s root missing: %v", legacy.ID, err)
	}

	// Infrastructure projects never get a space, are not reported as errors,
	// and keep no manifest mapping or directory.
	for _, project := range []domain.Project{channelHost, systemProject} {
		if provisioned[project.ID] {
			t.Fatalf("infrastructure project %s was provisioned: %#v", project.ID, report)
		}
		for _, id := range report.MarkedError {
			if id == project.ID {
				t.Fatalf("infrastructure project %s was marked error: %#v", project.ID, report)
			}
		}
		if _, mapped := knowledgevault.SpaceRootPath(config, project.ID); mapped {
			t.Fatalf("infrastructure project %s has a vault mapping", project.ID)
		}
		stored, err := database.Project(ctx, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.KnowledgeState == domain.KnowledgeStateError {
			t.Fatalf("infrastructure project %s was left in error state", project.ID)
		}
	}

	// A manifest project whose root page disappeared is marked error for a
	// retry; reconciliation never silently reports it ready.
	if err := os.Remove(filepath.Join(config.VaultPath, filepath.FromSlash(root))); err != nil {
		t.Fatal(err)
	}
	report, err = service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range report.MarkedError {
		if id == legacy.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing root was not reported: %#v", report)
	}
	retried, err := service.RetryProjectKnowledgeSpace(ctx, legacy.ID)
	if err != nil || retried.KnowledgeState != domain.KnowledgeStateReady {
		t.Fatalf("retry after missing root = %#v err=%v", retried, err)
	}
}

func TestReconcileCleansEmptyInfrastructureSpaces(t *testing.T) {
	service, database, manager, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	channelHost := domain.Project{ID: "project-empty-channel", Name: "Empty Channel", ProjectType: "channel", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, channelHost); err != nil {
		t.Fatal(err)
	}
	// Simulate the space an older release provisioned for the channel host.
	if _, err := manager.ProvisionProjectSpace(ctx, channelHost.ID, channelHost.Name); err != nil {
		t.Fatal(err)
	}
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	slug := manifest.Projects[channelHost.ID]
	if slug == "" {
		t.Fatalf("channel space was not provisioned: %#v", manifest.Projects)
	}
	spaceDir := filepath.Join(config.VaultPath, "projects", slug)
	if err := os.WriteFile(filepath.Join(spaceDir, ".gitkeep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(spaceDir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range report.Cleaned {
		if id == channelHost.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("empty infrastructure space was not cleaned: %#v", report)
	}
	manifest, err = knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, mapped := manifest.Projects[channelHost.ID]; mapped {
		t.Fatalf("empty infrastructure space is still mapped: %#v", manifest.Projects)
	}
	if _, statErr := os.Stat(spaceDir); !os.IsNotExist(statErr) {
		t.Fatalf("empty infrastructure directory still exists: %v", statErr)
	}
	projectsRoot, err := os.ReadFile(filepath.Join(config.VaultPath, "projects", "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(projectsRoot), "[[projects/"+slug+"/index") {
		t.Fatalf("cleaned project is still listed in projects/index.md:\n%s", projectsRoot)
	}
	stored, err := database.Project(ctx, channelHost.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.KnowledgeState != domain.KnowledgeStateUninitialized {
		t.Fatalf("cleaned project state = %q", stored.KnowledgeState)
	}
	if err := manager.WithReader(func(reader *knowledgevault.Reader) error {
		entries, entryErr := reader.Entries(ctx, "project", channelHost.ID)
		if entryErr != nil {
			return entryErr
		}
		if len(entries) != 0 {
			t.Fatalf("cleaned project still has index entries: %#v", entries)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Idempotent: a second startup has nothing to clean and does not fail.
	report, err = service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range report.Cleaned {
		if id == channelHost.ID {
			t.Fatalf("cleanup was not idempotent: %#v", report)
		}
	}
}

func TestReconcileRetainsNonEmptyInfrastructureSpace(t *testing.T) {
	service, database, manager, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	channelHost := domain.Project{ID: "project-nonempty-channel", Name: "Busy Channel", ProjectType: "channel", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, channelHost); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ProvisionProjectSpace(ctx, channelHost.ID, channelHost.Name); err != nil {
		t.Fatal(err)
	}
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	slug := manifest.Projects[channelHost.ID]
	writeProjectKnowledgeDoc(t, config, "projects/"+slug+"/notes/keep.md", "Keep", "retained-infra-marker")
	if _, err := manager.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}

	report, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range report.Cleaned {
		if id == channelHost.ID {
			t.Fatalf("non-empty infrastructure space was deleted: %#v", report)
		}
	}
	warned := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, channelHost.ID) && strings.Contains(warning, "已保留") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("non-empty infrastructure space was not reported: %#v", report.Warnings)
	}
	manifest, err = knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Projects[channelHost.ID] != slug {
		t.Fatalf("non-empty infrastructure mapping changed: %#v", manifest.Projects)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "projects", slug, "notes", "keep.md")); err != nil {
		t.Fatalf("non-empty infrastructure document was removed: %v", err)
	}
}

func writeAppTrashScaffolding(t *testing.T, directory, projectID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(directory, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	index := "---\ntitle: Scaffold\nslug: scaffold\nscope: project\nproject_id: " + projectID + "\ntype: index\nstatus: verified\n---\n# Scaffold\n"
	if err := os.WriteFile(filepath.Join(directory, "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".gitkeep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileSweepsInfraSpaceTrashAtStartup(t *testing.T) {
	service, _, _, config := newSpaceTestService(t)
	ctx := context.Background()
	trashDir := filepath.Join(config.VaultPath, ".trash")
	crashed := filepath.Join(trashDir, "infra-space-crashed-20260101T000000Z")
	writeAppTrashScaffolding(t, crashed, "project-crashed-infra")
	busy := filepath.Join(trashDir, "infra-space-busy-20260101T000000Z")
	writeAppTrashScaffolding(t, busy, "project-busy-infra")
	if err := os.MkdirAll(filepath.Join(busy, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(busy, "notes", "keep.md"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, relative := range report.TrashRemoved {
		if relative == ".trash/"+filepath.Base(crashed) {
			found = true
		}
	}
	if !found {
		t.Fatalf("crashed infra-space trash was not swept: %#v", report.TrashRemoved)
	}
	if _, statErr := os.Stat(crashed); !os.IsNotExist(statErr) {
		t.Fatalf("crashed infra-space trash still exists: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(busy, "notes", "keep.md")); statErr != nil {
		t.Fatalf("non-empty trash content was touched: %v", statErr)
	}
	warned := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "infra-space-busy-20260101T000000Z") && strings.Contains(warning, "已保留") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("non-empty trash entry was not reported: %#v", report.Warnings)
	}

	// Idempotent: every later startup finds only the retained entry.
	again, err := service.ReconcileKnowledgeSpaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.TrashRemoved) != 0 {
		t.Fatalf("second startup removed %#v", again.TrashRemoved)
	}
}

func TestPetSystemProjectTaskKnowledgeIsGlobalOnly(t *testing.T) {
	service, database, manager, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	petProject := domain.Project{ID: petProjectID, Name: "宠物", ProjectType: "system", KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, petProject); err != nil {
		t.Fatal(err)
	}
	global := "---\ntitle: Global Note\nslug: global-note\nscope: global\ntype: reference\nstatus: verified\n---\n# Global Note\n\nglobal-marker\n"
	if err := os.WriteFile(filepath.Join(config.VaultPath, "global", "note.md"), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}

	projectActive, projectStale, _, err := service.projectKnowledgeForTask(ctx, petProject.ID, domain.ProductLine{}, true)
	if err != nil {
		t.Fatalf("system project knowledge read failed: %v", err)
	}
	if len(projectActive) != 0 || len(projectStale) != 0 {
		t.Fatalf("system project knowledge = %#v stale=%#v", projectActive, projectStale)
	}
	globalActive, _, _, err := service.globalKnowledgeForTask(ctx, true)
	if err != nil {
		t.Fatalf("global knowledge read failed: %v", err)
	}
	found := false
	for _, entry := range globalActive {
		if entry.Title == "Global Note" {
			found = true
		}
	}
	if !found {
		t.Fatalf("global knowledge was not injected: %#v", globalActive)
	}
}

func TestRetryProjectKnowledgeSpaceRefusesInfrastructureProjects(t *testing.T) {
	service, database, _, config := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	channelHost := domain.Project{ID: "project-retry-channel", Name: "Retry Channel", ProjectType: "channel", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, channelHost); err != nil {
		t.Fatal(err)
	}
	_, err := service.RetryProjectKnowledgeSpace(ctx, channelHost.ID)
	var spaceErr *KnowledgeSpaceError
	if !errors.As(err, &spaceErr) || spaceErr.Code != "knowledge_space_not_applicable" {
		t.Fatalf("error = %#v", err)
	}
	if !IsKnowledgeSpaceConflictError(err) {
		t.Fatal("infrastructure retry was not reported as a conflict")
	}
	if _, mapped := knowledgevault.SpaceRootPath(config, channelHost.ID); mapped {
		t.Fatal("infrastructure retry created a vault mapping")
	}
}

func TestVaultLibrariesHideInfrastructureProjects(t *testing.T) {
	service, database, manager, _ := newSpaceTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	normal := domain.Project{ID: "project-visible", Name: "Visible", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	channelHost := domain.Project{ID: "project-hidden-channel", Name: "Hidden Channel", ProjectType: "channel", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	systemProject := domain.Project{ID: "project-hidden-system", Name: "Hidden Pet", ProjectType: "system", KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now}
	for _, project := range []domain.Project{normal, channelHost, systemProject} {
		if err := database.CreateProject(ctx, project); err != nil {
			t.Fatal(err)
		}
	}
	for _, project := range []domain.Project{normal, channelHost, systemProject} {
		if _, err := manager.ProvisionProjectSpace(ctx, project.ID, project.Name); err != nil {
			t.Fatal(err)
		}
	}
	libraries, err := service.VaultLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	visible := map[string]bool{}
	for _, item := range libraries.Projects {
		visible[item.ProjectID] = true
	}
	if !visible[normal.ID] {
		t.Fatalf("normal project is missing from the picker: %#v", libraries.Projects)
	}
	for _, id := range []string{channelHost.ID, systemProject.ID} {
		if visible[id] {
			t.Fatalf("infrastructure project %s is offered as a library: %#v", id, libraries.Projects)
		}
		for _, item := range libraries.Libraries {
			if item.Kind == "project" && item.ProjectID == id {
				t.Fatalf("infrastructure project %s is in the combined library list: %#v", id, libraries.Libraries)
			}
		}
	}
}

func TestLegacyCutoverFailsClosedAndStopsLegacyRootSeeding(t *testing.T) {
	service, _, _, _ := newSpaceTestService(t)
	service.SetLegacyKnowledgeDisabled(true)
	decision := service.knowledgeReadDecision("project", "project-unbound")
	if !decision.Vault || decision.Err == nil {
		t.Fatalf("post-cutover unbound read = %#v, want a fail-closed vault error", decision)
	}
	if service.LegacyKnowledgeRootsEnabled("project-unbound") {
		t.Fatal("post-cutover project creation would still seed a legacy knowledge root")
	}
	decision = service.knowledgeReadDecision("global", "")
	if !decision.Vault {
		t.Fatalf("post-cutover global read = %#v", decision)
	}
}
