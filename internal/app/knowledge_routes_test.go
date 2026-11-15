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

func knowledgeRouteFixture(t *testing.T, projectID string) (*Service, *store.Store, *knowledgevault.Manager, domain.Project) {
	t.Helper()
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowManageKnowledge: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{
		ID: projectID, Name: "Knowledge Route", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	manager := buildVaultReadManager(t, true, vaultCanaryFiles())
	fixture.service.knowledgeVault = manager
	fixture.service.knowledgeVaultRuntimeRead = true
	fixture.service.knowledgeVaultRuntimeWrite = true
	return fixture.service, fixture.database, manager, project
}

func setKnowledgeAutoApprove(t *testing.T, database *store.Store, enabled bool) {
	t.Helper()
	if _, err := database.UpdateKnowledgeReviewSettings(context.Background(), domain.KnowledgeReviewSettings{
		AutoApprove: enabled, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

func vaultRouteBody() string {
	return "写入路由测试正文，必须超过二十个字符才能满足文件知识库的正文长度要求。"
}

func TestPetVaultWriteFollowsManualReviewAndReadsBack(t *testing.T) {
	service, database, manager, project := knowledgeRouteFixture(t, "project_other")
	ctx := context.Background()
	setKnowledgeAutoApprove(t, database, false)

	submitted, err := service.PetSubmitKnowledge(ctx, "project", project.ID, []KnowledgeCandidate{{
		Type: "practice", Title: "路由写入手动评审", Body: vaultRouteBody(), Confidence: 0.9,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(submitted.Entries) != 1 || submitted.Entries[0].Status != string(domain.KnowledgeCandidate) {
		t.Fatalf("submitted entries=%#v", submitted.Entries)
	}
	if len(submitted.Proposals) != 1 || submitted.Proposals[0].Status != string(domain.KnowledgeProposalPending) {
		t.Fatalf("submitted proposals=%#v", submitted.Proposals)
	}
	proposal, err := database.KnowledgeProposal(ctx, submitted.Proposals[0].ID)
	if err != nil || proposal.TargetSource != domain.KnowledgeProposalTargetVault {
		t.Fatalf("proposal=%#v err=%v", proposal, err)
	}
	if _, found, err := service.vaultEntryByID(ctx, proposal.EntryID); err != nil || found {
		t.Fatalf("manual proposal published early: found=%v err=%v", found, err)
	}
	if legacy, err := database.Knowledge(ctx, proposal.EntryID); err == nil || legacy.ID != "" {
		t.Fatalf("manual vault proposal wrote the legacy table: %#v err=%v", legacy, err)
	}

	approved, entry, err := service.ApproveKnowledgeProposal(ctx, proposal.ID, time.Now().UTC(), "评审通过")
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != domain.KnowledgeProposalApproved || approved.TargetSource != domain.KnowledgeProposalTargetVault {
		t.Fatalf("approved proposal=%#v", approved)
	}
	if entry.Status != domain.KnowledgeVerified || entry.Revision != 1 || entry.Scope != "project" || entry.ProjectID != project.ID {
		t.Fatalf("approved entry=%#v", entry)
	}
	vaultEntry, found, err := service.vaultEntryByID(ctx, entry.ID)
	if err != nil || !found || vaultEntry.ProjectID != project.ID {
		t.Fatalf("vault entry=%#v found=%v err=%v", vaultEntry, found, err)
	}
	if _, err := database.Knowledge(ctx, entry.ID); err == nil {
		t.Fatal("vault write also created a legacy knowledge entry")
	}

	view, err := service.PetListKnowledge(ctx, "project", project.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, item := range view.Entries {
		if item.ID == entry.ID && item.Status == string(domain.KnowledgeVerified) {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("read-after-write did not list %s: %#v", entry.ID, view.Entries)
	}
	detail, err := service.PetKnowledgeDetail(ctx, entry.ID)
	if err != nil || !strings.Contains(detail.Body, "文件知识库") {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}

	var search knowledgevault.SearchResponse
	if err := manager.WithReader(func(reader *knowledgevault.Reader) error {
		var searchErr error
		search, searchErr = reader.Search(ctx, knowledgevault.DocumentSearchOptions{Query: "路由写入手动评审", ProjectID: project.ID, Limit: 10})
		return searchErr
	}); err != nil {
		t.Fatal(err)
	}
	searched := false
	for _, result := range search.Results {
		if result.ID == entry.ID {
			searched = true
		}
	}
	if !searched {
		t.Fatalf("vault search did not reflect the write: %#v", search.Results)
	}

	if _, err := service.DeleteWebKnowledge(ctx, entry.ID); !errors.Is(err, ErrKnowledgeVaultDeleteUnsupported) {
		t.Fatalf("vault delete error=%v", err)
	}
}

func TestPetVaultWriteAutoReviewPublishesImmediately(t *testing.T) {
	service, database, _, project := knowledgeRouteFixture(t, "project_other")
	ctx := context.Background()
	setKnowledgeAutoApprove(t, database, true)

	submitted, err := service.PetSubmitKnowledge(ctx, "project", project.ID, []KnowledgeCandidate{{
		Type: "diagnostic", Title: "路由写入自动评审", Body: vaultRouteBody(), Confidence: 0.8,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(submitted.Entries) != 1 || submitted.Entries[0].Status != string(domain.KnowledgeVerified) {
		t.Fatalf("auto review entries=%#v", submitted.Entries)
	}
	if len(submitted.Proposals) != 1 || submitted.Proposals[0].Status != string(domain.KnowledgeProposalApproved) {
		t.Fatalf("auto review proposals=%#v", submitted.Proposals)
	}
	if _, err := database.Knowledge(ctx, submitted.Entries[0].ID); err == nil {
		t.Fatal("auto vault write created a legacy entry")
	}
	if _, found, err := service.vaultEntryByID(ctx, submitted.Entries[0].ID); err != nil || !found {
		t.Fatalf("auto write missing from vault: found=%v err=%v", found, err)
	}
}

func TestPetVaultRevisionKeepsBaseRevision(t *testing.T) {
	service, database, _, project := knowledgeRouteFixture(t, "project_other")
	ctx := context.Background()
	setKnowledgeAutoApprove(t, database, true)

	submitted, err := service.PetSubmitKnowledge(ctx, "project", project.ID, []KnowledgeCandidate{{
		Type: "practice", Title: "路由修订初稿", Body: vaultRouteBody(), Confidence: 0.7,
	}})
	if err != nil {
		t.Fatal(err)
	}
	entryID := submitted.Entries[0].ID
	setKnowledgeAutoApprove(t, database, false)
	revised, err := service.PetSubmitKnowledge(ctx, "project", project.ID, []KnowledgeCandidate{{
		EntryID: entryID, BaseRevision: 1, Type: "practice", Title: "路由修订第二稿",
		Body: "修订后的正文仍然需要超过二十个字符，以符合文件知识库的正文长度要求。", Confidence: 0.8,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(revised.Proposals) != 1 || revised.Proposals[0].BaseRevision != 1 {
		t.Fatalf("revision proposals=%#v", revised.Proposals)
	}
	if _, found, _ := service.vaultEntryByID(ctx, entryID); !found {
		t.Fatal("pending revision removed the original vault entry")
	}
	_, entry, err := service.ApproveKnowledgeProposal(ctx, revised.Proposals[0].ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if entry.Revision != 2 || entry.Title != "路由修订第二稿" {
		t.Fatalf("revised entry=%#v", entry)
	}
	if _, err := service.PetSubmitKnowledge(ctx, "project", project.ID, []KnowledgeCandidate{{
		EntryID: entryID, BaseRevision: 1, Type: "practice", Title: "过期修订",
		Body: vaultRouteBody(),
	}}); err == nil || !strings.Contains(err.Error(), "base_revision") {
		t.Fatalf("stale revision error=%v", err)
	}
}

func TestPetUnboundProjectStaysLegacyWithWarning(t *testing.T) {
	service, database, _, project := knowledgeRouteFixture(t, "project_other")
	ctx := context.Background()
	unbound := domain.Project{
		ID: "project_unbound", Name: "Unbound", ProjectType: "folder", KnowledgePolicy: "enabled",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := database.CreateProject(ctx, unbound); err != nil {
		t.Fatal(err)
	}
	setKnowledgeAutoApprove(t, database, true)
	submitted, err := service.PetSubmitKnowledge(ctx, "project", unbound.ID, []KnowledgeCandidate{{
		Type: "practice", Title: "未绑定项目旧库写入", Body: vaultRouteBody(), Confidence: 0.6,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if submitted.Warning == "" {
		t.Fatalf("unbound fallback did not warn: %#v", submitted)
	}
	if len(submitted.Entries) != 1 || submitted.Entries[0].Status != string(domain.KnowledgeVerified) {
		t.Fatalf("legacy auto entries=%#v", submitted.Entries)
	}
	entry, err := database.Knowledge(ctx, submitted.Entries[0].ID)
	if err != nil || entry.ProjectID != unbound.ID {
		t.Fatalf("legacy entry=%#v err=%v", entry, err)
	}
	if _, found, _ := service.vaultEntryByID(ctx, submitted.Entries[0].ID); found {
		t.Fatal("unbound project wrote the vault")
	}
	_ = project
}

func TestPetVaultReadWithoutWriteFailsClosed(t *testing.T) {
	service, database, _, project := knowledgeRouteFixture(t, "project_other")
	ctx := context.Background()
	service.SetKnowledgeVaultRuntimeWrite(false)
	_, err := service.PetSubmitKnowledge(ctx, "project", project.ID, []KnowledgeCandidate{{
		Type: "practice", Title: "关闭写入不得回退", Body: vaultRouteBody(),
	}})
	var writeErr *knowledgevault.WriteError
	if !errors.As(err, &writeErr) || writeErr.Code != knowledgevault.WriteErrorDisabled {
		t.Fatalf("write-off error=%v", err)
	}
	items, listErr := database.ListKnowledgeProposals(ctx, "project", project.ID)
	if listErr != nil || len(items) != 0 {
		t.Fatalf("write-off created proposals=%#v err=%v", items, listErr)
	}
}

func TestLegacyProposalCannotBeApprovedAfterVaultBecomesReadSource(t *testing.T) {
	service, database, _, project := knowledgeRouteFixture(t, "project_other")
	ctx := context.Background()
	setKnowledgeAutoApprove(t, database, false)
	service.knowledgeVaultRuntimeRead = false
	submitted, err := service.PetSubmitKnowledge(ctx, "project", project.ID, []KnowledgeCandidate{{
		Type: "practice", Title: "切换前旧库提案", Body: vaultRouteBody(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(submitted.Proposals) != 1 {
		t.Fatalf("legacy proposals=%#v", submitted.Proposals)
	}
	proposal, err := database.KnowledgeProposal(ctx, submitted.Proposals[0].ID)
	if err != nil || proposal.TargetSource != domain.KnowledgeProposalTargetLegacy {
		t.Fatalf("legacy proposal=%#v err=%v", proposal, err)
	}
	service.knowledgeVaultRuntimeRead = true
	service.SetKnowledgeVaultRuntimeWrite(true)
	if _, _, err := service.ApproveKnowledgeProposal(ctx, proposal.ID, time.Now().UTC()); !errors.Is(err, ErrKnowledgeReadSourceMismatch) {
		t.Fatalf("approval after switch error=%v", err)
	}
	if _, err := database.Knowledge(ctx, proposal.EntryID); err == nil {
		t.Fatal("legacy proposal approval wrote a hidden legacy entry")
	}
}

func vaultFeedbackFixture(t *testing.T) (*Service, *store.Store, *knowledgevault.Manager, domain.Project, domain.KnowledgeEntry, domain.KnowledgeEntry, domain.KnowledgeEntry) {
	t.Helper()
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newPetTaskFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	project := domain.Project{
		ID: "project_feedback_route", Name: "Feedback Route", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	projectRoot, err := fixture.database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	globalRoot, err := fixture.database.EnsureKnowledgeRoot(ctx, "global", "")
	if err != nil {
		t.Fatal(err)
	}
	entry := func(id, scope, projectID, parentID, title string) domain.KnowledgeEntry {
		item := domain.KnowledgeEntry{
			ID: id, Scope: scope, ProjectID: projectID, ParentID: parentID,
			Slug: strings.ReplaceAll(id, "_", "-"),
			Type: "practice", Title: title, Body: "旧库反馈测试正文。", Status: domain.KnowledgeVerified,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		}
		if err := fixture.database.CreateKnowledge(ctx, item); err != nil {
			t.Fatal(err)
		}
		created, err := fixture.database.Knowledge(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	projectEntry := entry("knowledge_feedback_project", "project", project.ID, projectRoot.ID, "旧库项目反馈行")
	globalEntry := entry("knowledge_feedback_global", "global", "", globalRoot.ID, "旧库全局反馈行")
	legacyOnlyGlobal := entry("knowledge_feedback_legacy_only", "global", "", globalRoot.ID, "仅旧库全局反馈行")

	manager := buildVaultReadManager(t, true, map[string]string{
		"projects/feedback-route/index.md":    "---\nid: " + projectRoot.ID + "\ntitle: Feedback Route\nscope: project\nproject_id: " + project.ID + "\ntype: navigation\nstatus: verified\nis_index: true\n---\n项目入口。\n",
		"projects/feedback-route/feedback.md": "---\nid: " + projectEntry.ID + "\ntitle: " + projectEntry.Title + "\nscope: project\nproject_id: " + project.ID + "\ntype: practice\nstatus: verified\nrevision: 1\n---\n旧库反馈测试正文。\n",
		"global/index.md":                     "---\nid: " + globalRoot.ID + "\ntitle: 全局首页\nscope: global\ntype: navigation\nstatus: verified\nis_index: true\n---\n全局入口。\n",
		"global/general/feedback.md":          "---\nid: " + globalEntry.ID + "\ntitle: " + globalEntry.Title + "\nscope: global\ntype: practice\nstatus: verified\nrevision: 1\n---\n旧库反馈测试正文。\n",
	})
	service := fixture.service
	service.knowledgeVault = manager
	service.knowledgeVaultRuntimeRead = true
	service.knowledgeVaultRuntimeWrite = true
	return service, fixture.database, manager, project, projectEntry, globalEntry, legacyOnlyGlobal
}

func TestWebKnowledgeFeedbackFollowsVaultAndNeverUpdatesLegacy(t *testing.T) {
	service, database, manager, _, projectEntry, globalEntry, legacyOnlyGlobal := vaultFeedbackFixture(t)
	ctx := context.Background()
	before := map[string]domain.KnowledgeEntry{
		projectEntry.ID: projectEntry, globalEntry.ID: globalEntry, legacyOnlyGlobal.ID: legacyOnlyGlobal,
	}
	assertLegacyUnchanged := func(t *testing.T, id string) {
		t.Helper()
		original := before[id]
		after, err := database.Knowledge(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if after.FeedbackState != original.FeedbackState || after.HelpedCount != original.HelpedCount ||
			after.StaleCount != original.StaleCount || !after.UpdatedAt.Equal(original.UpdatedAt) {
			t.Fatalf("legacy row %s changed: before=%#v after=%#v", id, original, after)
		}
	}

	result, err := service.FeedbackWebKnowledge(ctx, projectEntry.ID, "stale")
	if err != nil || result.Source != "vault" || result.Entry.Status != domain.KnowledgeStale || result.Entry.FeedbackState != "stale" {
		t.Fatalf("project feedback result=%#v err=%v", result, err)
	}
	assertLegacyUnchanged(t, projectEntry.ID)
	path := filepath.Join(manager.Config().VaultPath, "projects", "feedback-route", "feedback.md")
	if raw, err := os.ReadFile(path); err != nil || !strings.Contains(string(raw), "status: stale") {
		t.Fatalf("vault file was not marked stale: %q err=%v", raw, err)
	}

	result, err = service.FeedbackWebKnowledge(ctx, globalEntry.ID, "helped")
	if err != nil || result.Source != "vault" {
		t.Fatalf("global feedback result=%#v err=%v", result, err)
	}
	assertLegacyUnchanged(t, globalEntry.ID)

	if _, err := service.FeedbackWebKnowledge(ctx, legacyOnlyGlobal.ID, "helped"); !errors.Is(err, ErrKnowledgeReadSourceMismatch) {
		t.Fatalf("legacy-only global feedback error=%v, want a read-source mismatch", err)
	}
	assertLegacyUnchanged(t, legacyOnlyGlobal.ID)

	service.SetKnowledgeVaultRuntimeWrite(false)
	var writeErr *knowledgevault.WriteError
	if _, err := service.FeedbackWebKnowledge(ctx, projectEntry.ID, "helped"); !errors.As(err, &writeErr) || writeErr.Code != knowledgevault.WriteErrorDisabled {
		t.Fatalf("write-gate-off feedback error=%v", err)
	}
	assertLegacyUnchanged(t, projectEntry.ID)
	service.SetKnowledgeVaultRuntimeWrite(true)

	// An unbound project reads the legacy tables, but global knowledge still
	// reads the vault: global feedback must keep going to the vault.
	t.Setenv(knowledgeVaultReadProjectsEnv, "project_elsewhere")
	result, err = service.FeedbackWebKnowledge(ctx, globalEntry.ID, "helped")
	if err != nil || result.Source != "vault" {
		t.Fatalf("unbound global feedback result=%#v err=%v", result, err)
	}
	assertLegacyUnchanged(t, globalEntry.ID)
}

func TestPetGlobalKnowledgeWritesVault(t *testing.T) {
	service, database, _, _ := knowledgeRouteFixture(t, "project_other")
	ctx := context.Background()
	setKnowledgeAutoApprove(t, database, true)
	submitted, err := service.PetSubmitKnowledge(ctx, "global", "", []KnowledgeCandidate{{
		Type: "practice", Title: "全局路由写入", Body: vaultRouteBody(), Confidence: 0.9,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(submitted.Entries) != 1 || submitted.Entries[0].Scope != "global" || submitted.Entries[0].ProjectID != "" {
		t.Fatalf("global entries=%#v", submitted.Entries)
	}
	if _, err := database.Knowledge(ctx, submitted.Entries[0].ID); err == nil {
		t.Fatal("global vault write created a legacy entry")
	}
	if _, found, err := service.vaultEntryByID(ctx, submitted.Entries[0].ID); err != nil || !found {
		t.Fatalf("global vault entry missing: found=%v err=%v", found, err)
	}
	view, err := service.PetListKnowledge(ctx, "global", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, item := range view.Entries {
		if item.ID == submitted.Entries[0].ID {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("global read-after-write did not list the entry: %#v", view.Entries)
	}
}

func TestPetDeleteVaultOnlyEntryIsExplicitlyUnsupported(t *testing.T) {
	service, database, _, project := knowledgeRouteFixture(t, "project_other")
	ctx := context.Background()
	setKnowledgeAutoApprove(t, database, true)
	submitted, err := service.PetSubmitKnowledge(ctx, "project", project.ID, []KnowledgeCandidate{{
		Type: "practice", Title: "宠物删除 vault 条目", Body: vaultRouteBody(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := service.vaultEntryByID(ctx, submitted.Entries[0].ID); err != nil || !found {
		t.Fatalf("fixture entry missing from vault: found=%v err=%v", found, err)
	}
	if _, err := service.PetDeleteKnowledge(ctx, submitted.Entries[0].ID, "清理已经过期的知识条目"); !errors.Is(err, ErrKnowledgeVaultDeleteUnsupported) {
		t.Fatalf("PetDeleteKnowledge error=%v", err)
	}
	if _, found, err := service.vaultEntryByID(ctx, submitted.Entries[0].ID); err != nil || !found {
		t.Fatalf("refused Pet delete removed the vault entry: found=%v err=%v", found, err)
	}
}

func TestWebKnowledgeWritesVaultAndRefusesDelete(t *testing.T) {
	service, database, _, project := knowledgeRouteFixture(t, "project_other")
	ctx := context.Background()
	created, err := service.CreateWebKnowledge(ctx, KnowledgeWriteInput{
		Scope: "project", ProjectID: project.ID, Type: "practice",
		Title: "Web 路由创建", Body: vaultRouteBody(), Confidence: 0.7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Source != "vault" || created.Entry.Status != domain.KnowledgeVerified {
		t.Fatalf("created=%#v", created)
	}
	if _, err := database.Knowledge(ctx, created.Entry.ID); err == nil {
		t.Fatal("Web vault create wrote the legacy table")
	}
	updated, err := service.UpdateWebKnowledge(ctx, created.Entry.ID, KnowledgeWriteInput{
		Scope: "project", ProjectID: project.ID, Type: "practice",
		Title: "Web 路由更新", Body: "更新后的正文仍然需要超过二十个字符，以符合文件知识库的正文长度要求。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Source != "vault" || updated.Entry.Revision != 2 || updated.Entry.Title != "Web 路由更新" {
		t.Fatalf("updated=%#v", updated)
	}
	if _, err := service.DeleteWebKnowledge(ctx, created.Entry.ID); !errors.Is(err, ErrKnowledgeVaultDeleteUnsupported) {
		t.Fatalf("vault delete error=%v", err)
	}

	unbound := domain.Project{
		ID: "project_unbound", Name: "Unbound", ProjectType: "folder", KnowledgePolicy: "enabled",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := database.CreateProject(ctx, unbound); err != nil {
		t.Fatal(err)
	}
	legacyCreated, err := service.CreateWebKnowledge(ctx, KnowledgeWriteInput{
		Scope: "project", ProjectID: unbound.ID, Type: "practice",
		Title: "未绑定 Web 旧库", Body: vaultRouteBody(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if legacyCreated.Source != "legacy" || legacyCreated.Warning == "" {
		t.Fatalf("legacy create=%#v", legacyCreated)
	}
	if _, err := database.Knowledge(ctx, legacyCreated.Entry.ID); err != nil {
		t.Fatalf("legacy create was not stored: %v", err)
	}
	if _, err := service.DeleteWebKnowledge(ctx, legacyCreated.Entry.ID); err != nil {
		t.Fatalf("legacy delete failed: %v", err)
	}
}
