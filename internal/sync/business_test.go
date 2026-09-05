package sync

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func businessStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestBusinessExportStripsLocalAndSecretFields(t *testing.T) {
	ctx, db, now := context.Background(), businessStore(t), time.Now().UTC()
	if err := db.CreateKnowledge(ctx, domain.KnowledgeEntry{ID: "k1", Scope: "project", ProjectID: "project-private", Type: "practice", Title: "K", Body: "body", Revision: 2, SourceTaskID: "task-private", SourceTurnID: "turn-private", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSkill(ctx, domain.Skill{ID: "s1", Scope: "project", ProjectID: "project-private", Name: "demo", Description: "demo skill", Instructions: "instructions", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	skill, err := db.Skill(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.UpdateSkillPackage(ctx, skill, 1, []domain.SkillFile{{Path: "SKILL.md", Content: "---\nname: demo\ndescription: demo skill\n---\n\nfull package"}, {Path: "scripts/run.ps1", Content: "Write-Output ok"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertProvider(ctx, domain.Provider{ID: "p1", Name: "P", BaseURL: "https://example.test", CredentialRef: "secret/provider", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertModel(ctx, domain.Model{ID: "m1", DisplayName: "M", ProviderID: "p1", Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "m", CodexAccountID: "account-private", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertEnvGroup(ctx, domain.EnvGroup{ID: "e1", Name: "E", ProviderID: "p1", Backend: "codex", Revision: 3, Environment: map[string]string{"SAFE": "value"}, SecretRefs: map[string]string{"TOKEN": "secret/token"}, SecretConfigured: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPromptTemplateOverride(ctx, "core-default", "override", now); err != nil {
		t.Fatal(err)
	}
	objects, err := ExportBusinessObjects(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 6 {
		t.Fatalf("objects=%d", len(objects))
	}
	raw, _ := json.Marshal(objects)
	text := string(raw)
	for _, forbidden := range []string{"project-private", "task-private", "turn-private", "secret/provider", "secret/token", "account-private"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("export leaked %q", forbidden)
		}
	}
	var full skillPayload
	for _, obj := range objects {
		if obj.BaseVersion != "" {
			t.Fatalf("initial export has base version")
		}
		if obj.Type == TypeSkill {
			if err := json.Unmarshal(obj.Payload, &full); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(full.Files) != 2 {
		t.Fatalf("skill files=%d", len(full.Files))
	}
}

func TestApplyBusinessObjectVersionConflict(t *testing.T) {
	ctx, db, now := context.Background(), businessStore(t), time.Now().UTC()
	remote := domain.KnowledgeEntry{ID: "k1", Scope: "global", Type: "practice", Title: "remote", Body: "one", Revision: 1, CreatedAt: now, UpdatedAt: now}
	raw, _ := json.Marshal(remote)
	obj := domain.SyncObject{Type: TypeKnowledge, ID: "k1", Operation: "upsert", Payload: raw}
	if err := applyBusinessObject(ctx, db, obj); err != nil {
		t.Fatal(err)
	}
	remote.Title, remote.Revision = "changed", 2
	obj.Payload, _ = json.Marshal(remote)
	obj.BaseVersion = "0"
	if err := applyBusinessObject(ctx, db, obj); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict=%v", err)
	}
	obj.BaseVersion = "1"
	if err := applyBusinessObject(ctx, db, obj); err != nil {
		t.Fatal(err)
	}
	got, err := db.Knowledge(ctx, "k1")
	if err != nil || got.Title != "changed" || got.Revision != 2 {
		t.Fatalf("knowledge=%#v err=%v", got, err)
	}
}

func TestApplyPreservesLocalProviderCredential(t *testing.T) {
	ctx, db, now := context.Background(), businessStore(t), time.Now().UTC()
	local := domain.Provider{ID: "p1", Name: "local", CredentialRef: "secret/local", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now}
	if err := db.UpsertProvider(ctx, local); err != nil {
		t.Fatal(err)
	}
	remote := domain.Provider{ID: "p1", Name: "remote", BaseURL: "https://remote.test", CreatedAt: now, UpdatedAt: now.Add(time.Second)}
	raw, _ := json.Marshal(remote)
	if err := applyBusinessObject(ctx, db, domain.SyncObject{Type: TypeProvider, ID: "p1", Operation: "upsert", Payload: raw, BaseVersion: timeVersion(now)}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Provider(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "remote" || got.CredentialRef != "secret/local" || !got.CredentialConfigured {
		t.Fatalf("provider=%#v", got)
	}
}

func TestApplySkillPackageAndVersionConflict(t *testing.T) {
	ctx, db, now := context.Background(), businessStore(t), time.Now().UTC()
	payload := skillPayload{Skill: domain.Skill{ID: "s1", Scope: "global", Name: "demo", Description: "demo", Instructions: "body", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}, Files: []domain.SkillFile{{Path: "SKILL.md", Content: "---\nname: demo\ndescription: demo\n---\n\nbody"}, {Path: "assets/note.txt", Content: "complete"}}}
	raw, _ := json.Marshal(payload)
	obj := domain.SyncObject{Type: TypeSkill, ID: "s1", Operation: "upsert", Payload: raw}
	if err := applyBusinessObject(ctx, db, obj); err != nil {
		t.Fatal(err)
	}
	got, err := db.Skill(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || len(got.PackageFiles) != 2 {
		t.Fatalf("skill=%#v", got)
	}
	obj.BaseVersion = "0"
	if err := applyBusinessObject(ctx, db, obj); !errors.Is(err, ErrConflict) {
		t.Fatalf("skill conflict=%v", err)
	}
}

func TestApplyPromptOverridePreservesVersion(t *testing.T) {
	ctx, db := context.Background(), businessStore(t)
	prompt := domain.PromptTemplate{ID: "core-default", Content: "remote", Version: 4}
	raw, _ := json.Marshal(prompt)
	obj := domain.SyncObject{Type: TypePromptOverride, ID: prompt.ID, Operation: "upsert", Payload: raw}
	if err := applyBusinessObject(ctx, db, obj); err != nil {
		t.Fatal(err)
	}
	got, err := db.PromptTemplateOverride(ctx, prompt.ID)
	if err != nil || got.Version != 4 {
		t.Fatalf("prompt=%#v err=%v", got, err)
	}
	obj.BaseVersion = "3"
	if err := applyBusinessObject(ctx, db, obj); !errors.Is(err, ErrConflict) {
		t.Fatalf("prompt conflict=%v", err)
	}
}
