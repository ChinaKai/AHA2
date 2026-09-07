package sync

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestKnowledgeLibraryBindingSynchronizesAndUnbinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	open := func() *store.Store {
		database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { database.Close() })
		return database
	}
	source, destination := open(), open()
	now := time.Now().UTC()
	target := domain.Project{ID: "project-target", Name: "Target", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	container := domain.Project{ID: "project-library", Name: "Library", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := source.CreateProject(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := source.CreateProject(ctx, container); err != nil {
		t.Fatal(err)
	}
	library, err := source.KnowledgeLibrary(ctx, "library_"+container.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.BindKnowledgeLibrary(ctx, library.ID, target.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := source.CreateSkill(ctx, domain.Skill{
		ID: "skill-library", PackageSlug: "skill-library", Scope: "project", ProjectID: container.ID,
		Name: "Library skill", Description: "Bound skill", Instructions: "Use the bound skill.",
		Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	objects, err := ExportBusinessObjectsForDevice(ctx, source, "device-source")
	if err != nil {
		t.Fatal(err)
	}
	bindingIndex := -1
	projectIndexes := map[string]int{}
	for index, object := range objects {
		if object.Type == TypeProject {
			projectIndexes[object.ID] = index
		}
		if object.Type == TypeKnowledgeBinding {
			bindingIndex = index
		}
		if err := applyBusinessObject(ctx, destination, object); err != nil {
			t.Fatalf("apply %s/%s: %v", object.Type, object.ID, err)
		}
	}
	if bindingIndex < 0 || projectIndexes[target.ID] >= bindingIndex || projectIndexes[container.ID] >= bindingIndex {
		t.Fatalf("binding dependency order projects=%#v binding=%d", projectIndexes, bindingIndex)
	}
	received, err := destination.KnowledgeLibrary(ctx, library.ID)
	if err != nil || received.BoundProjectID != target.ID {
		t.Fatalf("received library=%#v err=%v", received, err)
	}
	receivedSkill, err := destination.Skill(ctx, "skill-library")
	if err != nil || receivedSkill.ProjectID != container.ID || receivedSkill.BoundProjectID != target.ID {
		t.Fatalf("library skill lost binding scope: %#v err=%v", receivedSkill, err)
	}
	if _, err := source.UnbindKnowledgeLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	objects, err = ExportBusinessObjectsForDevice(ctx, source, "device-source")
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		if object.Type == TypeKnowledgeBinding && object.Operation == "delete" {
			if err := applyBusinessObject(ctx, destination, object); err != nil {
				t.Fatal(err)
			}
		}
	}
	received, err = destination.KnowledgeLibrary(ctx, library.ID)
	if err != nil || received.BoundProjectID != "" {
		t.Fatalf("unbind did not synchronize: %#v err=%v", received, err)
	}
}
