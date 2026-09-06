package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestSharedTombstonesExportDeletesAndBlockOldUpserts(t *testing.T) {
	t.Parallel()
	ctx, database, now := context.Background(), businessStore(t), time.Now().UTC()
	types := []string{TypeProject, TypeProductLine, TypeKnowledge, TypeKnowledgeProposal, TypeSkill, TypeProvider, TypeModel, TypeEnvGroup, TypePromptOverride}
	for index, objectType := range types {
		id := "deleted-" + objectType
		key := "delete-key-" + objectType
		if _, err := database.ApplySyncTombstone(ctx, objectType, id, key, "1", now.Add(time.Duration(index)*time.Millisecond)); err != nil {
			t.Fatalf("apply tombstone %s: %v", objectType, err)
		}
		old := domain.SyncObject{Type: objectType, ID: id, Operation: "upsert", Payload: json.RawMessage(`{"old":true}`), SourceVersion: "1", IdempotencyKey: "old-" + objectType}
		if err := applyBusinessObject(ctx, database, old); err != nil {
			t.Fatalf("old upsert %s was not safely ignored: %v", objectType, err)
		}
	}
	objects, err := ExportBusinessObjects(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	deletes := map[string]domain.SyncObject{}
	for _, object := range objects {
		if object.Operation == "delete" {
			deletes[object.Type] = object
		}
	}
	if len(deletes) != len(types) {
		t.Fatalf("exported deletes=%#v", deletes)
	}
	for _, objectType := range types {
		object := deletes[objectType]
		if object.ID != "deleted-"+objectType || object.RemoteVersion != "1" || object.IdempotencyKey != "delete-key-"+objectType || len(object.Payload) != 0 {
			t.Fatalf("delete object %s=%#v", objectType, object)
		}
	}
}

func TestNewerKnowledgeVersionCanSupersedeTombstone(t *testing.T) {
	t.Parallel()
	ctx, database, now := context.Background(), businessStore(t), time.Now().UTC()
	root, err := database.EnsureKnowledgeRoot(ctx, "global", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ApplySyncTombstone(ctx, TypeKnowledge, "recreated", "delete-recreated", "1", now); err != nil {
		t.Fatal(err)
	}
	entry := domain.KnowledgeEntry{ID: "recreated", Scope: "global", ParentID: root.ID, Slug: "recreated", Type: "practice", Title: "Recreated", Body: "new", Status: domain.KnowledgeVerified, Revision: 2, CreatedAt: now, UpdatedAt: now}
	payload, _ := json.Marshal(entry)
	if err := applyBusinessObject(ctx, database, domain.SyncObject{Type: TypeKnowledge, ID: entry.ID, Operation: "upsert", Payload: payload, SourceVersion: "2", IdempotencyKey: "new-recreated"}); err != nil {
		t.Fatal(err)
	}
	created, err := database.Knowledge(ctx, entry.ID)
	if err != nil || created.Revision != 2 || created.Body != "new" {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	if _, err := database.SyncTombstone(ctx, TypeKnowledge, entry.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("superseded tombstone remained: %v", err)
	}
}

func TestOlderDeleteConflictsWithNewerKnowledge(t *testing.T) {
	t.Parallel()
	ctx, database, now := context.Background(), businessStore(t), time.Now().UTC()
	root, _ := database.EnsureKnowledgeRoot(ctx, "global", "")
	entry := domain.KnowledgeEntry{ID: "newer-than-delete", Scope: "global", ParentID: root.ID, Slug: "newer-than-delete", Type: "practice", Title: "Newer", Body: "revision two", Status: domain.KnowledgeVerified, Revision: 2, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	oldDelete := domain.SyncObject{Type: TypeKnowledge, ID: entry.ID, Operation: "delete", SourceVersion: "1", IdempotencyKey: "old-delete"}
	var conflict *ConflictError
	if err := applyBusinessObject(ctx, database, oldDelete); !errors.As(err, &conflict) {
		t.Fatalf("old delete error=%v", err)
	}
	if current, err := database.Knowledge(ctx, entry.ID); err != nil || current.Revision != 2 {
		t.Fatalf("newer entry was deleted: %#v err=%v", current, err)
	}
	oldDelete.SourceVersion = "2"
	oldDelete.IdempotencyKey = "current-delete"
	if err := applyBusinessObject(ctx, database, oldDelete); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Knowledge(ctx, entry.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("current delete did not apply: %v", err)
	}
}
