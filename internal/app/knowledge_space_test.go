package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		[]domain.KnowledgeBinding{{SpaceProject: first.ID, AccessMode: "read_write"}})
	if err != nil || len(bindings) != 1 || bindings[0].SpaceProject != first.ID {
		t.Fatalf("instance bindings = %#v err=%v", bindings, err)
	}
	// Endpoint-level binding replaces the instance default.
	bindings, err = service.ReplaceChannelKnowledgeBindings(ctx, "chan-bind", "assistant_dm",
		[]domain.KnowledgeBinding{{SpaceProject: second.ID}})
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
		[]domain.KnowledgeBinding{{SpaceProject: second.ID}}); !errors.Is(err, ErrKnowledgeSpaceNotReady) {
		t.Fatalf("unready binding error = %v", err)
	}
}

func TestReconcileKnowledgeSpacesProvisionsLegacyAndChannelHosts(t *testing.T) {
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
	for _, id := range []string{legacy.ID, channelHost.ID} {
		if !provisioned[id] {
			t.Fatalf("project %s was not provisioned: %#v", id, report)
		}
		stored, err := database.Project(ctx, id)
		if err != nil || stored.KnowledgeState != domain.KnowledgeStateReady {
			t.Fatalf("project %s state=%q err=%v", id, stored.KnowledgeState, err)
		}
		root, ok := knowledgevault.SpaceRootPath(config, id)
		if !ok {
			t.Fatalf("project %s has no manifest mapping", id)
		}
		if _, err := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash(root))); err != nil {
			t.Fatalf("project %s root missing: %v", id, err)
		}
	}

	// A manifest project whose root page disappeared is marked error for a
	// retry; reconciliation never silently reports it ready.
	root, _ := knowledgevault.SpaceRootPath(config, legacy.ID)
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
