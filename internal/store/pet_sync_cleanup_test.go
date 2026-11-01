package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetSyncMirrorCleanupMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{ID: "project_pet", Name: "宠物", ProjectType: "system", KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now}
	local := domain.Workspace{
		ID: "workspace_pet", ProjectID: project.ID, Name: "pet-workspace",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		OwnerDeviceID: "device-local", CreatedAt: now, UpdatedAt: now,
	}
	mirror := domain.Workspace{
		ID: "syncws_pet", ProjectID: project.ID, Name: "pet-workspace",
		Locality: "remote", Transport: "native", Health: "remote", ReadOnly: true,
		OwnerDeviceID: "work-windows", SourceWorkspaceID: "workspace_pet", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	for _, workspace := range []domain.Workspace{local, mirror} {
		if err := db.CreateWorkspace(ctx, workspace); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO sync_remote_task_objects(owner_device_id,object_type,object_id,task_id,project_id,payload_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		"work-windows", "task", "task_pet", "task_pet", project.ID, `{"workspace_id":"syncws_pet"}`, timeString(now), timeString(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=111`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Workspace(ctx, mirror.ID); err == nil {
		t.Fatal("pet sync mirror workspace was not removed")
	}
	if _, err := db.Workspace(ctx, local.ID); err != nil {
		t.Fatalf("local pet workspace was removed: %v", err)
	}
	if _, err := db.Project(ctx, project.ID); err != nil {
		t.Fatalf("local pet project was removed: %v", err)
	}
	var remoteObjects int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_remote_task_objects WHERE project_id=?`, project.ID).Scan(&remoteObjects); err != nil {
		t.Fatal(err)
	}
	if remoteObjects != 0 {
		t.Fatalf("pet remote objects=%d", remoteObjects)
	}
}
