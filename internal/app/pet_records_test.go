package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetCorrectionHistoryShowsEveryStateNewestFirstUpToFifty(t *testing.T) {
	f := newPetInsightsFixture(t)
	if records, err := f.service.PetCorrectionHistory(f.ctx); err != nil || len(records) != 0 {
		t.Fatalf("empty history = %+v err=%v", records, err)
	}
	correction := func(id, note, status string, age time.Duration) {
		t.Helper()
		detail := fmt.Sprintf(`{"approval_id":"a-%s","kind":"delete_workspace","title":"删除 %s","owner_note":%q}`, id, id, note)
		if err := f.database.AppendPetEvent(f.ctx, domain.PetEvent{
			ID: "pet_event_" + id, At: f.now.Add(-age).UTC(), Actor: "owner", Kind: petCorrectionEventKind, Detail: detail, Status: status,
		}); err != nil {
			t.Fatal(err)
		}
	}
	correction("absorbed", "已经写进原则", "absorbed", time.Hour)
	correction("fresh", "还要留着对比", "open", 2*time.Hour)
	correction("edge", "刚好七天", "open", petCorrectionMaxAge)               // still inside: the cutoff is inclusive
	correction("expired", "过期了", "open", petCorrectionMaxAge+time.Second) // a second past seven days
	correction("silent", "", "open", 3*time.Hour)                         // no written reason: not a correction
	// Other kinds never appear.
	f.event("pet", "task_created", "task-x", f.now)

	records, err := f.service.PetCorrectionHistory(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, record := range records {
		got = append(got, record.ApprovalID+":"+record.State)
	}
	want := "a-absorbed:absorbed a-fresh:open a-edge:open a-expired:expired"
	if strings.Join(got, " ") != want {
		t.Fatalf("history = %q, want %q", strings.Join(got, " "), want)
	}
	if records[1].Title != "删除 fresh" || records[1].OwnerNote != "还要留着对比" || !records[1].At.Equal(f.now.Add(-2*time.Hour).UTC()) {
		t.Fatalf("record fields = %+v", records[1])
	}

	// The newest 50, whatever their state.
	for index := 0; index < petCorrectionHistoryLimit+5; index++ {
		correction(fmt.Sprintf("bulk-%02d", index), "note", "absorbed", time.Duration(index)*time.Second)
	}
	records, err = f.service.PetCorrectionHistory(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != petCorrectionHistoryLimit || records[0].ApprovalID != "a-bulk-00" {
		t.Fatalf("limit: %d records, first %+v", len(records), records[0])
	}
}

func TestPetInsightsAddsRecordsOnlyWhenAsked(t *testing.T) {
	f := newPetInsightsFixture(t)
	plain, err := f.service.PetInsights(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Knowledge != nil || plain.CorrectionHistory != nil {
		t.Fatalf("the overview read must not carry records data: %+v", plain)
	}
	records, err := f.service.PetInsights(f.ctx, nil, PetInsightsOptions{Records: true})
	if err != nil {
		t.Fatal(err)
	}
	if records.Knowledge == nil || records.CorrectionHistory == nil || len(*records.CorrectionHistory) != 0 || records.KnowledgeError != "" || records.CorrectionsError != "" {
		t.Fatalf("records read = %+v", records)
	}
}

// legacyKnowledge writes a legacy knowledge row under the scope's root.
func legacyKnowledge(t *testing.T, f *petTaskFixture, scope, projectID, id, title string, status domain.KnowledgeStatus, feedback string, helped int) {
	t.Helper()
	root, err := f.database.EnsureKnowledgeRoot(f.ctx, scope, projectID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := f.database.CreateKnowledge(f.ctx, domain.KnowledgeEntry{
		ID: id, Scope: scope, ProjectID: projectID, ParentID: root.ID, Slug: strings.ReplaceAll(id, "_", "-"),
		Type: "practice", Title: title, Body: "正文", Status: status, FeedbackState: feedback, HelpedCount: helped,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPetKnowledgeHealthTotalsLegacySpacesAndSkipsSystemProjects(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	f := newPetTaskFixture(t)
	f.service.knowledgeVault = nil
	now := time.Now().UTC()
	for _, project := range []domain.Project{
		{ID: "project_alpha", Name: "Alpha", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now},
		{ID: "project_beta", Name: "Beta", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now},
		{ID: "project_system", Name: "System", ProjectType: "system", KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now},
	} {
		if err := f.database.CreateProject(f.ctx, project); err != nil {
			t.Fatal(err)
		}
	}
	legacyKnowledge(t, f, "global", "", "k_global_ok", "全局有效", domain.KnowledgeVerified, "helped", 3)
	legacyKnowledge(t, f, "global", "", "k_global_stale", "全局过时", domain.KnowledgeStale, "stale", 1)
	legacyKnowledge(t, f, "global", "", "k_global_old", "已废弃", domain.KnowledgeDeprecated, "", 9) // retired: not counted
	legacyKnowledge(t, f, "project", "project_alpha", "k_alpha_ok", "Alpha 有效", domain.KnowledgeVerified, "", 2)
	legacyKnowledge(t, f, "project", "project_alpha", "k_alpha_wrong", "Alpha 错误", domain.KnowledgeStale, "wrong", 0)
	legacyKnowledge(t, f, "project", "project_beta", "k_beta_ok", "Beta 有效", domain.KnowledgeVerified, "helped", 4)
	// A system project has no knowledge space; a row there must not count.
	legacyKnowledge(t, f, "project", "project_system", "k_system", "系统项目", domain.KnowledgeStale, "stale", 5)

	health, err := f.service.PetKnowledgeHealth(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Entries: two global (the retired one and AHA's category folders do not
	// count) plus three project rows. Helped: 3 + 1 + 2 + 0 + 4.
	if health.Entries != 5 || health.Pending != 2 || health.Helped != 10 || health.HelpedPartial || health.Failed != 0 || health.Truncated {
		t.Fatalf("totals = entries %d pending %d helped %d partial %v failed %d truncated %v",
			health.Entries, health.Pending, health.Helped, health.HelpedPartial, health.Failed, health.Truncated)
	}
	var spaces []string
	for _, space := range health.Spaces {
		spaces = append(spaces, fmt.Sprintf("%s:%s:%d/%d/%d", space.Scope, space.ProjectID, space.Entries, space.Pending, space.Helped))
		if space.ProjectID == "project_system" {
			t.Fatalf("system project read: %+v", space)
		}
	}
	if len(health.Spaces) != 3 || health.Projects != 2 {
		t.Fatalf("spaces = %v (projects %d)", spaces, health.Projects)
	}
	var pending []string
	for _, item := range health.PendingItems {
		pending = append(pending, item.Scope+":"+item.Space+":"+item.Title+":"+item.Status)
	}
	want := "global:global:全局过时:stale project:Alpha:Alpha 错误:wrong"
	if strings.Join(pending, " ") != want {
		t.Fatalf("pending = %q, want %q", strings.Join(pending, " "), want)
	}
}

func TestPetKnowledgeHealthListsAtMostTenPendingAndCapsProjects(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	f := newPetTaskFixture(t)
	f.service.knowledgeVault = nil
	now := time.Now().UTC()
	for index := 0; index < 14; index++ {
		legacyKnowledge(t, f, "global", "", fmt.Sprintf("k_stale_%02d", index), fmt.Sprintf("过时 %02d", index), domain.KnowledgeStale, "stale", 0)
	}
	for index := 0; index < petKnowledgeProjectLimit+3; index++ {
		if err := f.database.CreateProject(f.ctx, domain.Project{
			ID: fmt.Sprintf("project_%03d", index), Name: fmt.Sprintf("P%03d", index), ProjectType: "folder",
			KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	health, err := f.service.PetKnowledgeHealth(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("knowledge health over %d project spaces + global: %s (server reported %dms)", health.Projects, time.Since(started), health.ElapsedMS)
	if health.Pending != 14 || len(health.PendingItems) != petKnowledgePendingLimit || health.PendingItems[0].Title != "过时 00" {
		t.Fatalf("pending total %d, listed %d, first %+v", health.Pending, len(health.PendingItems), health.PendingItems[0])
	}
	if !health.Truncated || health.Projects != petKnowledgeProjectLimit {
		t.Fatalf("project cap: truncated %v projects %d", health.Truncated, health.Projects)
	}
}

func TestPetKnowledgeHealthCountsVaultWrongEntriesAndMarksHelpedUnknown(t *testing.T) {
	service, _, _, project, projectEntry, globalEntry, _ := vaultFeedbackFixture(t)
	ctx := context.Background()
	// stale on the project entry and wrong on the global one, through the
	// same feedback path the Web uses.
	if _, err := service.FeedbackWebKnowledge(ctx, projectEntry.ID, "stale"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.FeedbackWebKnowledge(ctx, globalEntry.ID, "wrong"); err != nil {
		t.Fatal(err)
	}
	health, err := service.PetKnowledgeHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bySpace := map[string]PetKnowledgeSpaceHealth{}
	for _, space := range health.Spaces {
		bySpace[space.Scope+":"+space.ProjectID] = space
	}
	global, projectSpace := bySpace["global:"], bySpace["project:"+project.ID]
	if global.Source != "vault" || projectSpace.Source != "vault" || global.HelpedKnown || projectSpace.HelpedKnown {
		t.Fatalf("vault spaces must report helped as unknown: global=%+v project=%+v", global, projectSpace)
	}
	// The wrong entry is counted, although the shared snapshot helpers drop it.
	if global.Entries != 1 || global.Pending != 1 || projectSpace.Entries != 1 || projectSpace.Pending != 1 {
		t.Fatalf("vault counts: global=%+v project=%+v", global, projectSpace)
	}
	if !health.HelpedPartial {
		t.Fatal("totals must say helped is partial when a vault space is read")
	}
	statuses := map[string]string{}
	for _, item := range health.PendingItems {
		statuses[item.ID] = item.Status
	}
	if statuses[globalEntry.ID] != "wrong" || statuses[projectEntry.ID] != "stale" {
		t.Fatalf("pending statuses = %v", statuses)
	}
}

// TestPetKnowledgeHealthVaultTiming measures the vault path on a vault with
// 20 bound projects of 25 entries each plus 100 global entries. The number
// is logged for the record; the assertion only checks the counts.
func TestPetKnowledgeHealthVaultTiming(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	f := newPetTaskFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	files := map[string]string{}
	globalRoot, err := f.database.EnsureKnowledgeRoot(ctx, "global", "")
	if err != nil {
		t.Fatal(err)
	}
	files["global/index.md"] = "---\nid: " + globalRoot.ID + "\ntitle: 全局首页\nscope: global\ntype: navigation\nstatus: verified\nis_index: true\n---\n入口。\n"
	for index := 0; index < 100; index++ {
		status := "verified"
		if index%10 == 0 {
			status = "stale"
		}
		files[fmt.Sprintf("global/general/g%03d.md", index)] = fmt.Sprintf("---\nid: g_%03d\ntitle: 全局 %03d\nscope: global\ntype: practice\nstatus: %s\nrevision: 1\n---\n正文。\n", index, index, status)
	}
	for project := 0; project < 20; project++ {
		item := domain.Project{ID: fmt.Sprintf("project_vault_%02d", project), Name: fmt.Sprintf("V%02d", project), ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
		if err := f.database.CreateProject(ctx, item); err != nil {
			t.Fatal(err)
		}
		root, err := f.database.EnsureKnowledgeRoot(ctx, "project", item.ID)
		if err != nil {
			t.Fatal(err)
		}
		dir := fmt.Sprintf("projects/v%02d", project)
		files[dir+"/index.md"] = "---\nid: " + root.ID + "\ntitle: " + item.Name + "\nscope: project\nproject_id: " + item.ID + "\ntype: navigation\nstatus: verified\nis_index: true\n---\n入口。\n"
		for entry := 0; entry < 25; entry++ {
			status := "verified"
			if entry == 0 {
				status = "wrong"
			}
			files[fmt.Sprintf("%s/e%02d.md", dir, entry)] = fmt.Sprintf("---\nid: p%02d_e%02d\ntitle: 条目 %02d\nscope: project\nproject_id: %s\ntype: practice\nstatus: %s\nrevision: 1\n---\n正文。\n", project, entry, entry, item.ID, status)
		}
	}
	manager := buildVaultReadManager(t, true, files)
	f.service.knowledgeVault = manager
	f.service.knowledgeVaultRuntimeRead = true
	started := time.Now()
	health, err := f.service.PetKnowledgeHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("vault knowledge health: %d spaces, %d entries, %d pending in %s (server reported %dms)",
		len(health.Spaces), health.Entries, health.Pending, time.Since(started), health.ElapsedMS)
	if health.Failed != 0 || health.Entries != 100+20*25 || health.Pending != 10+20 || len(health.PendingItems) != petKnowledgePendingLimit {
		t.Fatalf("vault health: failed %d entries %d pending %d listed %d", health.Failed, health.Entries, health.Pending, len(health.PendingItems))
	}
}

func TestPetLedgerStatusCountsUseTheSharedParser(t *testing.T) {
	f := newPetDeskFixture(t)
	root := f.service.petWorkspaceRoot(f.ctx)
	if err := os.MkdirAll(filepath.Join(root, "core"), 0o700); err != nil {
		t.Fatal(err)
	}
	loops := "# 开放闭环\n- 每项写：状态（open/waiting/blocked）| 事项 | 下一步 | 关闭条件\n- open | a | b | c\n- open | d | e | f\n- waiting | g | h | i\n  - blocked | 缩进 | x | y\n## 已关闭\n- done | 关了 | - | -\n"
	agenda := "# 自主议程\n- next | a | b | c | 否\n- [Waiting] | d | e | f | 是\n"
	for name, content := range map[string]string{petCoreLoopsPath: loops, petCoreAgendaPath: agenda} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	counts := f.service.PetLedgerStatusCounts(f.ctx)
	if len(counts) != 2 || counts[0].Name != "loops.md" || counts[0].Total != 3 || counts[0].Counts["open"] != 2 || counts[0].Counts["waiting"] != 1 || counts[0].Counts["done"] != 0 {
		t.Fatalf("loops counts = %+v", counts)
	}
	if counts[1].Name != "agenda.md" || counts[1].Total != 2 || counts[1].Counts["next"] != 1 || counts[1].Counts["waiting"] != 1 {
		t.Fatalf("agenda counts = %+v", counts[1])
	}
}
