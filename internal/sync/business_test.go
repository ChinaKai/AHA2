package sync

import (
	"context"
	"database/sql"
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
	if err := db.CreateProject(ctx, domain.Project{ID: "project-private", Name: "Private", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateKnowledge(ctx, domain.KnowledgeEntry{ID: "k1", Scope: "project", ProjectID: "project-private", Type: "practice", Title: "K", Body: "body", Revision: 2, SourceTaskID: "task-private", SourceTurnID: "turn-private", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateKnowledge(ctx, domain.KnowledgeEntry{ID: "k-navigation", Scope: "project", ProjectID: "project-private", Type: "navigation", Title: "Map", Body: "navigation", Revision: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
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
	if len(objects) != 13 {
		t.Fatalf("objects=%d", len(objects))
	}
	raw, _ := json.Marshal(objects)
	text := string(raw)
	for _, forbidden := range []string{"task-private", "turn-private", "secret/provider", "secret/token", "account-private"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("export leaked %q", forbidden)
		}
	}
	projectKnowledgeExported, navigationExported := false, false
	for _, obj := range objects {
		if obj.Type == TypeKnowledge {
			var knowledge domain.KnowledgeEntry
			_ = json.Unmarshal(obj.Payload, &knowledge)
			projectKnowledgeExported = projectKnowledgeExported || knowledge.ID == "k1" && knowledge.ProjectID == "project-private" && knowledge.Scope == "project"
			navigationExported = navigationExported || knowledge.ID == "k-navigation" && knowledge.Type == "navigation"
		}
	}
	if !projectKnowledgeExported || !navigationExported {
		t.Fatalf("project knowledge or navigation was not exported: project=%t navigation=%t", projectKnowledgeExported, navigationExported)
	}
	var full skillPayload
	for _, obj := range objects {
		if obj.BaseVersion != "" {
			t.Fatalf("initial export has base version")
		}
		if obj.Type == TypeKnowledge && !strings.HasPrefix(obj.IdempotencyKey, "knowledge:v4:") {
			t.Fatalf("knowledge export retained legacy idempotency key: %q", obj.IdempotencyKey)
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

func TestModelSyncPreservesDefaultEnvGroup(t *testing.T) {
	t.Parallel()
	ctx, source, now := context.Background(), businessStore(t), time.Now().UTC()
	env := domain.EnvGroup{
		ID: "env-model-default", Name: "Gateway / gpt-env", ProviderID: "provider-model-default", Backend: "codex", Revision: 2,
		Environment: map[string]string{"OPENAI_MODEL": "gpt-env"}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-default", DisplayName: "GPT Env", ProviderID: env.ProviderID, Source: domain.ModelSourceProvider,
		Backend: env.Backend, WireModel: "gpt-env", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := source.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	if err := source.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	objects, err := ExportBusinessObjects(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	destination := businessStore(t)
	for _, object := range objects {
		if object.Type != TypeEnvGroup && object.Type != TypeModel {
			continue
		}
		if err := applyBusinessObject(ctx, destination, object); err != nil {
			t.Fatal(err)
		}
	}
	got, err := destination.Model(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultEnvGroupID != env.ID {
		t.Fatalf("synced default env group=%q, want %q", got.DefaultEnvGroupID, env.ID)
	}

	legacy := model
	legacy.DisplayName = "Legacy update"
	legacy.DefaultEnvGroupID = ""
	raw, _ := json.Marshal(legacy)
	if err := applyBusinessObject(ctx, destination, domain.SyncObject{Type: TypeModel, ID: model.ID, Operation: "upsert", Payload: raw}); err != nil {
		t.Fatal(err)
	}
	got, err = destination.Model(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultEnvGroupID != env.ID {
		t.Fatalf("legacy payload erased local default env group: %#v", got)
	}
}

func TestApplyLegacyProjectKnowledgeWithoutProjectIDAsGlobalFallback(t *testing.T) {
	ctx, db, now := context.Background(), businessStore(t), time.Now().UTC()
	legacy := domain.KnowledgeEntry{
		ID: "legacy-project-knowledge", Scope: "project", ProjectID: "", ParentID: "missing-project-root",
		Type: "practice", Title: "Legacy", Body: "portable fallback", Status: domain.KnowledgeVerified,
		ProductLineID: "legacy-line", BranchScope: "release/*", Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	raw, _ := json.Marshal(legacy)
	if err := applyBusinessObject(ctx, db, domain.SyncObject{Type: TypeKnowledge, ID: legacy.ID, Operation: "upsert", Payload: raw}); err != nil {
		t.Fatalf("legacy project knowledge failed first-device apply: %v", err)
	}
	got, err := db.Knowledge(ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope != "global" || got.ProjectID != "" || got.ParentID != store.GlobalGeneralKnowledgeID || got.ProductLineID != "" || got.BranchScope != "" {
		t.Fatalf("legacy fallback was not normalized safely: %#v", got)
	}
	diagnostic := legacy
	diagnostic.ID, diagnostic.Scope, diagnostic.ParentID, diagnostic.Type = "legacy-global-diagnostic", "global", store.GlobalKnowledgeRootID, "diagnostic"
	raw, _ = json.Marshal(diagnostic)
	if err := applyBusinessObject(ctx, db, domain.SyncObject{Type: TypeKnowledge, ID: diagnostic.ID, Operation: "upsert", Payload: raw}); err != nil {
		t.Fatal(err)
	}
	diagnostic, _ = db.Knowledge(ctx, diagnostic.ID)
	if diagnostic.ParentID != store.GlobalTechnicalLessonsKnowledgeID {
		t.Fatalf("legacy diagnostic parent=%s", diagnostic.ParentID)
	}
	preserved := domain.KnowledgeEntry{
		ID: "preserved-behavior", Scope: "global", ParentID: store.GlobalBehaviorLessonsKnowledgeID, Slug: "preserved-behavior",
		Type: "practice", Title: "Behavior", Body: "local", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateKnowledge(ctx, preserved); err != nil {
		t.Fatal(err)
	}
	incoming := preserved
	incoming.ParentID, incoming.Body, incoming.Revision = store.GlobalKnowledgeRootID, "remote replay", 2
	raw, _ = json.Marshal(incoming)
	if err := applyBusinessObject(ctx, db, domain.SyncObject{Type: TypeKnowledge, ID: incoming.ID, Operation: "upsert", Payload: raw}); err != nil {
		t.Fatal(err)
	}
	preserved, _ = db.Knowledge(ctx, preserved.ID)
	if preserved.ParentID != store.GlobalBehaviorLessonsKnowledgeID || preserved.Body != "remote replay" {
		t.Fatalf("sync replay lost behavior category: %#v", preserved)
	}

	legacy.ID, legacy.Scope, legacy.ParentID = "invalid-scope", "team", ""
	raw, _ = json.Marshal(legacy)
	err = applyBusinessObject(ctx, db, domain.SyncObject{Type: TypeKnowledge, ID: legacy.ID, Operation: "upsert", Payload: raw})
	if !errors.Is(err, store.ErrKnowledgeInvalidScope) {
		t.Fatalf("invalid non-legacy scope was accepted: %v", err)
	}
}

func TestKnowledgeExportKeyTracksStatusAndFeedbackWithoutRevisionChange(t *testing.T) {
	ctx, db, now := context.Background(), businessStore(t), time.Now().UTC()
	entry := domain.KnowledgeEntry{
		ID: "knowledge-state-key", Scope: "global", Type: "practice", Title: "State", Body: "body",
		Status: domain.KnowledgeVerified, Revision: 7, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	keyFor := func() string {
		t.Helper()
		objects, err := ExportBusinessObjects(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		for _, object := range objects {
			if object.Type == TypeKnowledge && object.ID == entry.ID {
				return object.IdempotencyKey
			}
		}
		t.Fatalf("knowledge %s was not exported", entry.ID)
		return ""
	}
	verifiedKey := keyFor()
	updated, err := db.FeedbackKnowledge(ctx, entry.ID, "stale", now.Add(time.Second).Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != entry.Revision {
		t.Fatalf("test requires a same-revision state transition: %#v", updated)
	}
	staleKey := keyFor()
	if staleKey == verifiedKey {
		t.Fatalf("status-only change reused sync key %q", staleKey)
	}
	updated, err = db.FeedbackKnowledge(ctx, entry.ID, "wrong", now.Add(2*time.Second).Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if wrongKey := keyFor(); wrongKey == staleKey {
		t.Fatalf("feedback-only change reused sync key %q", wrongKey)
	}
}

func TestApplyConcurrentPendingKnowledgeProposalsResolvesWithoutConflict(t *testing.T) {
	ctx, db, now := context.Background(), businessStore(t), time.Now().UTC()
	project := domain.Project{ID: "proposal-sync-project", Name: "Proposal sync", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, _ := db.EnsureKnowledgeRoot(ctx, "project", project.ID)
	entry := domain.KnowledgeEntry{
		ID: "proposal-sync-entry", Scope: "project", ProjectID: project.ID, ParentID: root.ID, Slug: "proposal-sync-entry",
		Type: "practice", Title: "Entry", Body: "base", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	localProposed := entry
	localProposed.Body = "local"
	local := domain.KnowledgeProposal{
		ID: "proposal-local", EntryID: entry.ID, BaseRevision: entry.Revision, Proposed: localProposed,
		Status: domain.KnowledgeProposalPending, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := db.CreateKnowledgeProposal(ctx, local); err != nil {
		t.Fatal(err)
	}
	remote := local
	remote.ID = "proposal-remote"
	remote.Proposed.Body = "remote"
	remote.CreatedAt, remote.UpdatedAt = now.Add(time.Second), now.Add(time.Second)
	raw, _ := json.Marshal(remote)
	if err := applyBusinessObject(ctx, db, domain.SyncObject{Type: TypeKnowledgeProposal, ID: remote.ID, Operation: "upsert", Payload: raw}); err != nil {
		t.Fatalf("concurrent pending proposal caused sync conflict: %v", err)
	}
	localStored, _ := db.KnowledgeProposal(ctx, local.ID)
	remoteStored, _ := db.KnowledgeProposal(ctx, remote.ID)
	if localStored.Status != domain.KnowledgeProposalRejected || remoteStored.Status != domain.KnowledgeProposalPending {
		t.Fatalf("pending proposal resolution diverged: local=%#v remote=%#v", localStored, remoteStored)
	}
	raw, _ = json.Marshal(local)
	if err := applyBusinessObject(ctx, db, domain.SyncObject{Type: TypeKnowledgeProposal, ID: local.ID, Operation: "upsert", Payload: raw}); err != nil {
		t.Fatalf("stale pending replay caused sync conflict: %v", err)
	}
	localStored, _ = db.KnowledgeProposal(ctx, local.ID)
	if localStored.Status != domain.KnowledgeProposalRejected {
		t.Fatalf("stale pending replay resurrected local proposal: %#v", localStored)
	}
}

func TestKnowledgeExportOrdersParentsBeforeChildren(t *testing.T) {
	ctx, db, now := context.Background(), businessStore(t), time.Now().UTC()
	project := domain.Project{ID: "project-knowledge-tree", Name: "Knowledge tree", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, err := db.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	parent := domain.KnowledgeEntry{
		ID: "z-parent", Scope: "project", ProjectID: project.ID, ParentID: root.ID, Slug: "parent",
		Type: "overview", Title: "Parent", Body: "parent", Status: domain.KnowledgeVerified,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	child := domain.KnowledgeEntry{
		ID: "a-child", Scope: "project", ProjectID: project.ID, ParentID: parent.ID, Slug: "child",
		Type: "practice", Title: "Child", Body: "child", Status: domain.KnowledgeVerified,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateKnowledge(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateKnowledge(ctx, child); err != nil {
		t.Fatal(err)
	}

	objects, err := ExportBusinessObjects(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	positions := map[string]int{}
	for index, object := range objects {
		if object.Type == TypeKnowledge {
			positions[object.ID] = index
		}
	}
	if !(positions[root.ID] < positions[parent.ID] && positions[parent.ID] < positions[child.ID]) {
		t.Fatalf("knowledge export is not topological: root=%d parent=%d child=%d", positions[root.ID], positions[parent.ID], positions[child.ID])
	}
}

func TestProductLineExportIsStableOrderedAndDependencyChecked(t *testing.T) {
	ctx, source, now := context.Background(), businessStore(t), time.Now().UTC()
	project := domain.Project{ID: "product-line-project", Name: "Project", CreatedAt: now, UpdatedAt: now}
	line := domain.ProductLine{ID: "product-line-main", ProjectID: project.ID, Name: "Main", BranchPattern: "main", Default: true, CreatedAt: now, UpdatedAt: now}
	if err := source.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := source.CreateProductLine(ctx, line); err != nil {
		t.Fatal(err)
	}

	export := func() ([]domain.SyncObject, string) {
		t.Helper()
		objects, err := ExportBusinessObjectsForDevice(ctx, source, "product-line-owner")
		if err != nil {
			t.Fatal(err)
		}
		projectIndex, lineIndex, lineKey := -1, -1, ""
		for index, object := range objects {
			switch {
			case object.Type == TypeProject && object.ID == project.ID:
				projectIndex = index
			case object.Type == TypeProductLine && object.ID == line.ID:
				lineIndex, lineKey = index, object.IdempotencyKey
			}
		}
		if projectIndex < 0 || lineIndex <= projectIndex || lineKey == "" {
			t.Fatalf("dependency order project=%d product_line=%d key=%q", projectIndex, lineIndex, lineKey)
		}
		return objects, lineKey
	}
	_, firstKey := export()
	_, stableKey := export()
	if stableKey != firstKey {
		t.Fatalf("unchanged product line key changed: %q != %q", stableKey, firstKey)
	}
	line.Name = "Main renamed"
	// A payload change cannot collide even if a caller retains the timestamp.
	if err := source.UpdateProductLine(ctx, line); err != nil {
		t.Fatal(err)
	}
	_, updatedKey := export()
	if updatedKey == firstKey {
		t.Fatalf("changed product line reused idempotency key %q", updatedKey)
	}

	destination := businessStore(t)
	raw, _ := json.Marshal(line)
	object := domain.SyncObject{Type: TypeProductLine, ID: line.ID, Operation: "upsert", Payload: raw, SourceVersion: timeVersion(line.UpdatedAt), IdempotencyKey: updatedKey}
	if err := applyBusinessObject(ctx, destination, object); err == nil || !strings.Contains(err.Error(), "project dependency") {
		t.Fatalf("product line applied before project: %v", err)
	}
	if err := applyBusinessObject(ctx, destination, graphObject(t, TypeProject, project.ID, "product-line-owner", project.ID, "", project)); err != nil {
		t.Fatal(err)
	}
	if err := applyBusinessObject(ctx, destination, object); err != nil {
		t.Fatal(err)
	}
	if got, err := destination.ProductLine(ctx, line.ID); err != nil || got.Name != line.Name || got.ProjectID != project.ID {
		t.Fatalf("synced product line=%#v err=%v", got, err)
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

func TestApplyKnowledgeSkipsTombstonedProjectAndWaitsForUnknownProject(t *testing.T) {
	ctx, db, now := context.Background(), businessStore(t), time.Now().UTC()
	entry := domain.KnowledgeEntry{ID: "stale-knowledge", Scope: "project", ProjectID: "deleted-project", Type: "practice", Title: "stale", Body: "stale", Revision: 1, CreatedAt: now, UpdatedAt: now}
	raw, _ := json.Marshal(entry)
	object := domain.SyncObject{Type: TypeKnowledge, ID: entry.ID, Operation: "upsert", Payload: raw}
	if _, err := db.ApplySyncTombstone(ctx, TypeProject, entry.ProjectID, "delete-project", timeVersion(now), now); err != nil {
		t.Fatal(err)
	}
	if err := applyBusinessObject(ctx, db, object); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Knowledge(ctx, entry.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale knowledge was imported: %v", err)
	}

	entry.ID, entry.ProjectID = "waiting-knowledge", "unknown-project"
	object.ID = entry.ID
	object.Payload, _ = json.Marshal(entry)
	var dependency *dependencyError
	if err := applyBusinessObject(ctx, db, object); !errors.As(err, &dependency) {
		t.Fatalf("unknown project error=%v", err)
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
