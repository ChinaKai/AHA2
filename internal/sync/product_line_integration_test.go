package sync_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/centersync"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func TestProductLinesConvergeAndDeletesSurviveNewDeviceReplay(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = center.Close() })
	server := httptest.NewServer(center.Handler())
	t.Cleanup(server.Close)
	type device struct {
		store  *store.Store
		runner syncer.Runner
	}
	makeDevice := func(id, token string) device {
		t.Helper()
		dir := t.TempDir()
		database, err := store.Open(ctx, filepath.Join(dir, "aha2.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		secretStore, err := secrets.Open(filepath.Join(dir, "secrets.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := secretStore.PutMany(map[string]string{syncer.DefaultTokenRef: token}); err != nil {
			t.Fatal(err)
		}
		if err := database.PutSyncSettings(ctx, domain.SyncSettings{Scope: "default", Enabled: true, Endpoint: server.URL, DeviceID: id, IntervalSeconds: 60}); err != nil {
			t.Fatal(err)
		}
		if err := center.PutDeviceToken(ctx, id, token); err != nil {
			t.Fatal(err)
		}
		return device{store: database, runner: syncer.Runner{Store: database, Secrets: secretStore, TokenRef: syncer.DefaultTokenRef}}
	}

	deviceA := makeDevice("product-line-a", "product-line-token-a")
	deviceB := makeDevice("product-line-b", "product-line-token-b")
	now := time.Now().UTC()
	project := domain.Project{ID: "shared-product-line-project", Name: "Shared project", CreatedAt: now, UpdatedAt: now}
	mainLine := domain.ProductLine{ID: "shared-main-line", ProjectID: project.ID, Name: "Main", BranchPattern: "main", Default: true, CreatedAt: now, UpdatedAt: now}
	obsoleteLine := domain.ProductLine{ID: "shared-obsolete-line", ProjectID: project.ID, Name: "Obsolete", BranchPattern: "release/old", CreatedAt: now, UpdatedAt: now}
	if err := deviceA.store.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	for _, line := range []domain.ProductLine{mainLine, obsoleteLine} {
		if err := deviceA.store.CreateProductLine(ctx, line); err != nil {
			t.Fatal(err)
		}
	}
	for _, runner := range []*syncer.Runner{&deviceA.runner, &deviceB.runner, &deviceA.runner} {
		if err := runner.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if lines, err := deviceB.store.ListProductLines(ctx, project.ID); err != nil || len(lines) != 2 {
		t.Fatalf("initial product lines=%#v err=%v", lines, err)
	}

	mainLine.Name = "Main updated"
	mainLine.BranchPattern = "main|develop"
	mainLine.UpdatedAt = now.Add(time.Second)
	if err := deviceA.store.UpdateProductLine(ctx, mainLine); err != nil {
		t.Fatal(err)
	}
	if err := deviceA.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := deviceB.store.ProductLine(ctx, mainLine.ID); err != nil || got.Name != mainLine.Name || got.BranchPattern != mainLine.BranchPattern {
		t.Fatalf("updated product line=%#v err=%v", got, err)
	}

	if err := deviceA.store.DeleteProductLine(ctx, obsoleteLine.ID); err != nil {
		t.Fatal(err)
	}
	if err := deviceA.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// B is still offline from the delete. Change stale content without advancing
	// its source version so it produces a distinct late upsert after the delete.
	staleLine, err := deviceB.store.ProductLine(ctx, obsoleteLine.ID)
	if err != nil {
		t.Fatal(err)
	}
	staleLine.Name = "Offline stale edit"
	if err := deviceB.store.UpdateProductLine(ctx, staleLine); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := deviceA.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	events, _, err := center.Pull(ctx, "audit", 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var obsoleteOperations []string
	for _, event := range events {
		if event.ObjectType == syncer.TypeProductLine && event.ObjectID == obsoleteLine.ID {
			obsoleteOperations = append(obsoleteOperations, event.Operation)
		}
	}
	if len(obsoleteOperations) < 3 || obsoleteOperations[len(obsoleteOperations)-2] != "delete" || obsoleteOperations[len(obsoleteOperations)-1] != "upsert" {
		t.Fatalf("test did not produce a late product line upsert: %v", obsoleteOperations)
	}
	for _, current := range []device{deviceA, deviceB} {
		if _, err := current.store.ProductLine(ctx, obsoleteLine.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("late old upsert resurrected product line: %v", err)
		}
	}

	var staleProject domain.SyncObject
	objects, err := syncer.ExportBusinessObjectsForDevice(ctx, deviceB.store, "product-line-b")
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		if object.Type == syncer.TypeProject && object.ID == project.ID {
			staleProject = object
			break
		}
	}
	if staleProject.ID == "" {
		t.Fatal("stale project snapshot was not exported")
	}
	staleProject.IdempotencyKey = "project:late-old-upsert-after-delete"
	if err := deviceA.store.DeleteProject(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	if err := deviceA.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.store.EnqueueSync(ctx, domain.SyncOutboxItem{Scope: "default", Object: staleProject}); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := deviceA.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertDeleted := func(label string, current device) {
		t.Helper()
		if _, err := current.store.Project(ctx, project.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("%s project survived delete: %v", label, err)
		}
		if lines, err := current.store.ListProductLines(ctx, project.ID); err != nil || len(lines) != 0 {
			t.Fatalf("%s product lines survived project delete: %#v err=%v", label, lines, err)
		}
		for _, identity := range []struct{ objectType, id string }{
			{syncer.TypeProject, project.ID},
			{syncer.TypeProductLine, mainLine.ID},
			{syncer.TypeProductLine, obsoleteLine.ID},
		} {
			if _, err := current.store.SyncTombstone(ctx, identity.objectType, identity.id); err != nil {
				t.Fatalf("%s missing tombstone %s/%s: %v", label, identity.objectType, identity.id, err)
			}
		}
	}
	assertDeleted("device A", deviceA)
	assertDeleted("device B", deviceB)

	deviceC := makeDevice("product-line-c", "product-line-token-c")
	if err := deviceC.runner.RunOnce(ctx); err != nil {
		t.Fatalf("new device replay failed: %v", err)
	}
	assertDeleted("new device C", deviceC)
}
