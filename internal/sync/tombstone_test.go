package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestSharedTombstonesExportDeletesAndBlockOldUpserts(t *testing.T) {
	t.Parallel()
	ctx, database, now := context.Background(), businessStore(t), time.Now().UTC()
	types := []string{TypeProject, TypeProductLine, TypeKnowledge, TypeKnowledgeProposal, TypeSkill, TypeProvider, TypeModel, TypeEnvGroup, TypeCodexAccount, TypePromptOverride}
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

// TestSkillTombstoneBlocksFieldReplayAcrossVersions pins the 2026-11-16 field
// case: four skills came back because their replay was newer than the
// tombstone, eleven stayed deleted because theirs was not. Both groups must
// stay deleted, and neither may consume the tombstone.
func TestSkillTombstoneBlocksFieldReplayAcrossVersions(t *testing.T) {
	t.Parallel()
	ctx, database, now := context.Background(), businessStore(t), time.Now().UTC()
	cases := []struct {
		id       string
		deleted  int
		incoming int
	}{
		{"skill_2a80a6bb1cbafe158502", 16682, 16691},
		{"skill_b35184c23f0303b3f49b", 6582, 6588},
		{"skill_aha1_44161b82fa6e5373f35d95e1", 15200, 15207},
		{"skill_b3aedf31cb801b533243", 7151, 7157},
		{"skill-legacy-lower-01", 40, 39},
		{"skill-legacy-equal-02", 40, 40},
		{"skill-legacy-lower-03", 90, 1},
		{"skill-legacy-equal-04", 7, 7},
		{"skill-legacy-lower-05", 21, 20},
		{"skill-legacy-equal-06", 12, 12},
		{"skill-legacy-lower-07", 300, 299},
		{"skill-legacy-equal-08", 55, 55},
		{"skill-legacy-lower-09", 64, 63},
		{"skill-legacy-equal-10", 2, 2},
		{"skill-legacy-lower-11", 500, 499},
	}
	for _, testCase := range cases {
		t.Run(testCase.id, func(t *testing.T) {
			if _, err := database.ApplySyncTombstone(ctx, TypeSkill, testCase.id, "delete:"+testCase.id, strconv.Itoa(testCase.deleted), now); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(skillPayload{
				Skill: domain.Skill{ID: testCase.id, Scope: "global", Name: "demo", Description: "demo", Instructions: "replayed body", Version: testCase.incoming, Status: "active", Enabled: true},
				Files: []domain.SkillFile{{Path: "SKILL.md", Content: "---\nname: \"demo\"\ndescription: \"demo\"\n---\n\nreplayed body\n"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := applyBusinessObject(ctx, database, domain.SyncObject{Type: TypeSkill, ID: testCase.id, Operation: "upsert", Payload: payload, SourceVersion: strconv.Itoa(testCase.incoming)}); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Skill(ctx, testCase.id); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("tombstoned skill was recreated: %v", err)
			}
			tombstone, err := database.SyncTombstone(ctx, TypeSkill, testCase.id)
			if err != nil {
				t.Fatalf("tombstone was consumed: %v", err)
			}
			if tombstone.Version != strconv.Itoa(testCase.deleted) {
				t.Fatalf("tombstone version=%q", tombstone.Version)
			}
		})
	}
}

// TestCreateSkillClearsSupersededTombstone covers the recovery path a review
// found missing: a deliberate local rebuild of a deleted skill ID has to drop
// the tombstone in the same transaction, otherwise the export carries both the
// delete and the upsert and peers lose the rebuilt skill.
func TestCreateSkillClearsSupersededTombstone(t *testing.T) {
	ctx, database, now := context.Background(), businessStore(t), time.Now().UTC()
	if err := database.CreateSkill(ctx, domain.Skill{ID: "skill-rebuilt", Scope: "global", Name: "demo", Description: "demo", Instructions: "old body", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteSkill(ctx, "skill-rebuilt"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SyncTombstone(ctx, TypeSkill, "skill-rebuilt"); err != nil {
		t.Fatalf("delete did not write a tombstone: %v", err)
	}
	if err := database.CreateSkill(ctx, domain.Skill{ID: "skill-rebuilt", Scope: "global", Name: "demo", Description: "demo", Instructions: "rebuilt body", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SyncTombstone(ctx, TypeSkill, "skill-rebuilt"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("superseded tombstone survived the rebuild: %v", err)
	}
	rebuilt, err := database.Skill(ctx, "skill-rebuilt")
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Version != 2 || rebuilt.Instructions != "rebuilt body" {
		t.Fatalf("rebuilt skill=%#v", rebuilt)
	}
	// An old event replayed by a peer must not undo the rebuild: the stale
	// version check drops it before the package is touched.
	payload, err := json.Marshal(skillPayload{
		Skill: domain.Skill{ID: "skill-rebuilt", Scope: "global", Name: "demo", Description: "demo", Instructions: "old body", Version: 1, Status: "active", Enabled: true},
		Files: []domain.SkillFile{{Path: "SKILL.md", Content: "---\nname: \"demo\"\ndescription: \"demo\"\n---\n\nold body\n"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := applyBusinessObject(ctx, database, domain.SyncObject{Type: TypeSkill, ID: "skill-rebuilt", Operation: "upsert", Payload: payload, SourceVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	got, err := database.Skill(ctx, "skill-rebuilt")
	if err != nil {
		t.Fatalf("stale replay removed the rebuilt skill: %v", err)
	}
	if got.Version != 2 || got.Instructions != "rebuilt body" {
		t.Fatalf("stale replay overwrote the rebuild: %#v", got)
	}
	// Known gap (release note): a peer whose local version is above the
	// delete's source version keeps its copy and records a conflict instead
	// of deleting. Pinned here so the behaviour is not changed by accident.
	if err := applyBusinessObject(ctx, database, domain.SyncObject{Type: TypeSkill, ID: "skill-rebuilt", Operation: "delete", IdempotencyKey: "delete:skill-rebuilt", SourceVersion: "1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("older delete error=%v", err)
	}

}

func TestApplySkillResurrectClearsTombstoneBothWays(t *testing.T) {
	t.Parallel()
	ctx, database, now := context.Background(), businessStore(t), time.Now().UTC()
	if _, err := database.ApplySyncTombstone(ctx, TypeSkill, "skill-resurrect", "delete:skill-resurrect", "1", now); err != nil {
		t.Fatal(err)
	}
	newPayload := func(version int, token string) json.RawMessage {
		raw, err := json.Marshal(skillPayload{
			Skill:     domain.Skill{ID: "skill-resurrect", Scope: "global", Name: "demo", Description: "demo", Instructions: "rebuilt body", Version: version, Status: "active", Enabled: true},
			Files:     []domain.SkillFile{{Path: "SKILL.md", Content: "---\nname: \"demo\"\ndescription: \"demo\"\n---\n\nrebuilt body\n"}},
			Resurrect: token,
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	// A resurrect below the tombstone version must stay blocked.
	if err := applyBusinessObject(ctx, database, domain.SyncObject{Type: TypeSkill, ID: "skill-resurrect", Operation: "upsert", Payload: newPayload(1, "resurrect:old"), SourceVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SyncTombstone(ctx, TypeSkill, "skill-resurrect"); err != nil {
		t.Fatal(err)
	}
	// A resurrect above it clears the tombstone and recreates the skill.
	if err := applyBusinessObject(ctx, database, domain.SyncObject{Type: TypeSkill, ID: "skill-resurrect", Operation: "upsert", Payload: newPayload(2, "resurrect:new"), SourceVersion: "2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SyncTombstone(ctx, TypeSkill, "skill-resurrect"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("resurrect did not clear the tombstone: %v", err)
	}
	rebuilt, err := database.Skill(ctx, "skill-resurrect")
	if err != nil || rebuilt.Version != 2 || rebuilt.Instructions != "rebuilt body" {
		t.Fatalf("resurrected skill=%#v err=%v", rebuilt, err)
	}
}

func TestCreateSkillRefusesUnknownVersionTombstone(t *testing.T) {
	t.Parallel()
	ctx, database, now := context.Background(), businessStore(t), time.Now().UTC()
	if _, err := database.ApplySyncTombstone(ctx, TypeSkill, "skill-unknown", "delete:skill-unknown", "", now); err != nil {
		t.Fatal(err)
	}
	err := database.CreateSkill(ctx, domain.Skill{ID: "skill-unknown", Scope: "global", Name: "demo", Description: "demo", Instructions: "body", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now})
	if err == nil {
		t.Fatal("rebuild over an unknown-version tombstone was accepted")
	}
	if !strings.Contains(err.Error(), "not numeric") {
		t.Fatalf("error=%v", err)
	}
	if _, err := database.SyncTombstone(ctx, TypeSkill, "skill-unknown"); err != nil {
		t.Fatalf("unknown-version tombstone was removed: %v", err)
	}
	if _, err := database.Skill(ctx, "skill-unknown"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("skill row was created despite the refused rebuild: %v", err)
	}
}

func TestSkillFilesMatchRejectsDuplicatePaths(t *testing.T) {
	t.Parallel()
	current := []domain.SkillFile{{Path: "SKILL.md", Content: "a"}, {Path: "assets/x.txt", Content: "b"}}
	incoming := []domain.SkillFile{{Path: "SKILL.md", Content: "a"}, {Path: "./SKILL.md", Content: "a"}}
	if skillFilesMatch(current, incoming) {
		t.Fatal("duplicate path was treated as identical")
	}
	duplicated := []domain.SkillFile{{Path: "SKILL.md", Content: "a"}, {Path: "SKILL.md", Content: "a"}}
	if skillFilesMatch(duplicated, duplicated) {
		t.Fatal("duplicate current path was treated as identical")
	}
	if !skillFilesMatch(current, []domain.SkillFile{{Path: "./SKILL.md", Content: "a"}, {Path: "assets/x.txt", Content: "b"}}) {
		t.Fatal("clean identical package was treated as different")
	}
}
