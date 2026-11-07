package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func writePetCoreFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "core"), 0o700); err != nil {
		t.Fatal(err)
	}
	for relative, content := range files {
		if err := os.WriteFile(filepath.Join(root, relative), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPetCoreDigestSummarizesCoreFiles(t *testing.T) {
	root := t.TempDir()
	writePetCoreFixture(t, root, map[string]string{
		petCoreIdentityPath:   "# 身份\n\n- 名字：栗子\n- 性格：温和\n",
		petCorePrinciplesPath: "# 做事准则\n\n- 先看清目标\n  - 子条目也保留\n- 不越权\n",
		petCoreLoopsPath: petCoreLoopsSeed + "\n- open | 事项 A | 下一步 | 关闭条件\n- waiting | 事项 B | x | y\n" +
			"- blocked | 事项 C | x | y\n- done | 已完成 D | x | y\n- open | 事项 E | x | y\n\n## 已关闭\n\n- open | 归档 F | x | y\n",
		petCoreAgendaPath: petCoreAgendaSeed + "\n- doing | 正在做 | x | y | 否\n- next | 议程 1 | x | y | 否\n" +
			"- waiting | 议程 2 | x | y | 是\n- next | 议程 3 | x | y | 否\n",
	})
	digest := buildPetCoreDigest(root, false)
	if digest.Identity != "- 名字：栗子\n- 性格：温和" || digest.IdentityTruncated {
		t.Fatalf("identity=%q truncated=%t", digest.Identity, digest.IdentityTruncated)
	}
	if !strings.Contains(digest.Principles, "  - 子条目也保留") || digest.PrinciplesTruncated {
		t.Fatalf("principles=%q", digest.Principles)
	}
	// The seeded format line ("每项写：状态（open/waiting/blocked）| ...") is not an
	// entry, closed entries and the archived section are skipped.
	if digest.OpenLoopsTotal != 4 || len(digest.OpenLoops) != 3 || !strings.HasPrefix(digest.OpenLoops[0], "open | 事项 A") || !strings.HasPrefix(digest.OpenLoops[2], "blocked | 事项 C") {
		t.Fatalf("open loops=%d %#v", digest.OpenLoopsTotal, digest.OpenLoops)
	}
	if digest.AgendaTotal != 3 || len(digest.Agenda) != 2 || !strings.HasPrefix(digest.Agenda[0], "next | 议程 1") || !strings.HasPrefix(digest.Agenda[1], "waiting | 议程 2") {
		t.Fatalf("agenda=%d %#v", digest.AgendaTotal, digest.Agenda)
	}
	if len(digest.Version) != 12 || len(digest.Missing) != 0 {
		t.Fatalf("version=%q missing=%v", digest.Version, digest.Missing)
	}
	if digest.Paths.Identity != filepath.Join(root, petCoreIdentityPath) || digest.Paths.Agenda != filepath.Join(root, petCoreAgendaPath) {
		t.Fatalf("paths=%#v", digest.Paths)
	}
	rendered := petCoreDigestPrompt(digest, false)
	for _, want := range []string{"core_version " + digest.Version, "名字：栗子", "不越权", "未闭环 4 条，前 3 条", "next/waiting 3 条，前 2 条", digest.Paths.Principles} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered digest missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "事项 E") || strings.Contains(rendered, "议程 3") || strings.Contains(rendered, "归档 F") {
		t.Fatalf("rendered digest exceeded item limits:\n%s", rendered)
	}
}

func TestPetCoreDigestTruncatesWithMarker(t *testing.T) {
	root := t.TempDir()
	var principles strings.Builder
	principles.WriteString("# 做事准则\n\n")
	// 60 rules of ~46 runes each exceed the 1500-rune principles budget even
	// before the nested details are counted.
	for index := 0; index < 60; index++ {
		principles.WriteString("- 规则" + strings.Repeat("长", 40) + strconv.Itoa(100+index) + "\n  - 细节说明\n")
	}
	principles.WriteString("- " + strings.Repeat("超长", 100) + "\n")
	writePetCoreFixture(t, root, map[string]string{
		petCoreIdentityPath:   "# 身份\n\n- 名字：" + strings.Repeat("栗", 400) + "\n",
		petCorePrinciplesPath: principles.String(),
	})
	digest := buildPetCoreDigest(root, false)
	if !digest.IdentityTruncated || utf8.RuneCountInString(digest.Identity) > petDigestIdentityLimit {
		t.Fatalf("identity not bounded: %d runes truncated=%t", utf8.RuneCountInString(digest.Identity), digest.IdentityTruncated)
	}
	if !strings.Contains(digest.Identity, "缩短 1 条") || !strings.Contains(digest.Identity, petCoreIdentityPath) {
		t.Fatalf("identity marker missing: %q", digest.Identity)
	}
	if !digest.PrinciplesTruncated || utf8.RuneCountInString(digest.Principles) > petDigestPrinciplesLimit {
		t.Fatalf("principles not bounded: %d runes", utf8.RuneCountInString(digest.Principles))
	}
	if !strings.HasPrefix(digest.Principles, "- 规则") || strings.Contains(digest.Principles, "细节说明") {
		t.Fatalf("principles should keep top-level rules first: %q", digest.Principles)
	}
	lines := strings.Split(digest.Principles, "\n")
	marker := lines[len(lines)-1]
	// 60 nested details are always omitted, plus every top-level rule that did
	// not fit; the marker must account for all of them.
	kept := len(lines) - 1
	if kept < 20 || kept >= 61 {
		t.Fatalf("kept %d top-level rules, want the budget to keep most but not all", kept)
	}
	want := "省略 " + strconv.Itoa(60+61-kept) + " 行、缩短 0 条"
	if !strings.Contains(marker, want) || !strings.Contains(marker, petCorePrinciplesPath) {
		t.Fatalf("principles marker=%q want %q", marker, want)
	}
	english := buildPetCoreDigest(root, true)
	if !strings.Contains(english.Principles, "digest truncated") {
		t.Fatalf("english marker missing: %q", english.Principles)
	}
}

// The live principles.md is ~4.2 KB: 13 top-level rules of up to ~80 runes,
// each with nested details. The digest must keep every top-level rule and drop
// only the nested lines.
func TestPetCoreDigestKeepsAllTopLevelRulesOfLiveSizedPrinciples(t *testing.T) {
	root := t.TempDir()
	var principles strings.Builder
	principles.WriteString("# 做事准则\n\n")
	for index := 1; index <= 13; index++ {
		principles.WriteString("- 第" + strconv.Itoa(index) + "条：" + strings.Repeat("规", 60) + "\n")
		principles.WriteString("  - " + strings.Repeat("细", 25) + "\n  - " + strings.Repeat("则", 25) + "\n")
	}
	if size := principles.Len(); size < 4000 || size > 4800 {
		t.Fatalf("fixture is %d bytes, want a live-sized ~4.2 KB file", size)
	}
	writePetCoreFixture(t, root, map[string]string{petCorePrinciplesPath: principles.String()})
	digest := buildPetCoreDigest(root, false)
	if !digest.PrinciplesTruncated || utf8.RuneCountInString(digest.Principles) > petDigestPrinciplesLimit {
		t.Fatalf("principles truncated=%t runes=%d", digest.PrinciplesTruncated, utf8.RuneCountInString(digest.Principles))
	}
	for index := 1; index <= 13; index++ {
		if !strings.Contains(digest.Principles, "- 第"+strconv.Itoa(index)+"条：") {
			t.Fatalf("top-level rule %d dropped:\n%s", index, digest.Principles)
		}
	}
	lines := strings.Split(digest.Principles, "\n")
	if marker := lines[len(lines)-1]; len(lines) != 14 || !strings.Contains(marker, "省略 26 行、缩短 0 条") {
		t.Fatalf("lines=%d marker=%q", len(lines), marker)
	}
}

func TestPetCoreDigestVersionTracksContent(t *testing.T) {
	root := t.TempDir()
	writePetCoreFixture(t, root, map[string]string{
		petCoreIdentityPath: "# 身份\n\n- 名字：栗子\n", petCorePrinciplesPath: "- 不越权\n",
		petCoreLoopsPath: "# 开放闭环\n", petCoreAgendaPath: "# 自主议程\n",
	})
	first := buildPetCoreDigest(root, false)
	if again := buildPetCoreDigest(root, false); again.Version != first.Version {
		t.Fatalf("version is not stable: %s vs %s", first.Version, again.Version)
	}
	writePetCoreFixture(t, root, map[string]string{petCoreAgendaPath: "# 自主议程\n\n- next | 新事项 | x | y | 否\n"})
	changed := buildPetCoreDigest(root, false)
	if changed.Version == first.Version {
		t.Fatalf("version did not change after agenda edit: %s", changed.Version)
	}
	// Moving bytes between files must not collide.
	writePetCoreFixture(t, root, map[string]string{petCoreAgendaPath: "# 自主议程\n", petCoreIdentityPath: "# 身份\n\n- 名字：栗子\n- 不越权\n", petCorePrinciplesPath: ""})
	if moved := buildPetCoreDigest(root, false); moved.Version == first.Version {
		t.Fatalf("version collided after moving content between files")
	}
}

func TestPetCoreDigestDegradesWhenFilesMissing(t *testing.T) {
	root := t.TempDir()
	writePetCoreFixture(t, root, map[string]string{petCoreIdentityPath: "  \n", petCoreLoopsPath: "- open | 只有闭环 | x | y\n"})
	digest := buildPetCoreDigest(root, false)
	if digest.Identity != "" || digest.Principles != "" || digest.OpenLoopsTotal != 1 || len(digest.Agenda) != 0 {
		t.Fatalf("degraded digest=%#v", digest)
	}
	if strings.Join(digest.Missing, ",") != strings.Join([]string{petCoreIdentityPath, petCorePrinciplesPath, petCoreAgendaPath}, ",") {
		t.Fatalf("missing=%v", digest.Missing)
	}
	if rendered := petCoreDigestPrompt(digest, false); !strings.Contains(rendered, "（缺失或为空）") {
		t.Fatalf("missing sections not marked:\n%s", rendered)
	}
	if empty := buildPetCoreDigest("", true); empty.Version == "" || len(empty.Missing) != 4 || empty.Paths.Loops != petCoreLoopsPath {
		t.Fatalf("rootless digest=%#v", empty)
	}
}

func TestRenderPetAssistantPromptIncludesCoreDigest(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, "core digest")
	if err := fixture.service.PetUpdateCore(fixture.ctx, "identity", "# 身份\n\n- 名字：栗子测试\n"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.PetUpdateCore(fixture.ctx, "principles", "# 做事准则\n\n- 坏消息第一时间说清楚\n"); err != nil {
		t.Fatal(err)
	}
	rendered, err := fixture.service.renderPetAssistantPrompt(fixture.ctx, "咕咕嘎嘎", "旧身份", "旧准则", "", PetLayout{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"核心摘要（core_version ", "名字：栗子测试", "坏消息第一时间说清楚", "core/agenda.md"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("assistant prompt missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "旧身份") {
		t.Fatalf("stored identity overrode the core file:\n%s", rendered)
	}

	// The stored values remain a fallback when a core file is empty.
	if err := fixture.service.PetUpdateCore(fixture.ctx, "principles", " "); err != nil {
		t.Fatal(err)
	}
	rendered, err = fixture.service.renderPetAssistantPrompt(fixture.ctx, "咕咕嘎嘎", "", "旧准则兜底", "", PetLayout{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "旧准则兜底") {
		t.Fatalf("stored principles fallback missing:\n%s", rendered)
	}
}

func TestPetTaskTurnPromptCarriesCoreDigest(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task := fixture.createPetDraft(t, "core digest turn")
	if err := fixture.service.PetUpdateCore(fixture.ctx, "identity", "# 身份\n\n- 名字：回合栗子\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := fixture.service.SubmitAgentMessage(fixture.ctx, task.ID, "main", "你是谁")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, fixture.database, turn.ID, domain.TurnSucceeded)
	prompts, _ := fixture.service.executor.(*stubExecutor).requestHistory()
	if len(prompts) == 0 {
		t.Fatal("turn did not reach the executor")
	}
	last := prompts[len(prompts)-1]
	// Section headings are "## " + template name; both anchors must exist or
	// the order checks below would pass without comparing anything.
	identitySectionAt := strings.Index(last, "## Pet Task Identity\n")
	roleAt := strings.Index(last, "## Main Agent\n")
	if identitySectionAt < 0 || roleAt < 0 {
		t.Fatalf("pet turn prompt missing section anchors identity=%d role=%d:\n%s", identitySectionAt, roleAt, last)
	}
	identityAt := strings.Index(last, "名字：回合栗子")
	digestAt := strings.Index(last, "核心摘要（core_version ")
	if identityAt < 0 || digestAt < 0 {
		t.Fatalf("pet turn prompt missing core digest:\n%s", last)
	}
	if digestAt < identitySectionAt || digestAt > roleAt || identityAt > roleAt {
		t.Fatalf("core digest should sit inside the pet identity section, before the role section: identity_section=%d digest=%d identity=%d role=%d", identitySectionAt, digestAt, identityAt, roleAt)
	}
	if strings.Count(last, "core_version ") != 1 {
		t.Fatalf("core digest rendered %d times", strings.Count(last, "core_version "))
	}
	state, err := fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(last, "core_version "+state.Core.Version) {
		t.Fatalf("turn prompt and work_state disagree on core_version %s", state.Core.Version)
	}
}

func TestNonPetTaskTurnPromptOmitsCoreDigest(t *testing.T) {
	fixture := newPetTaskFixture(t)
	// A populated pet workspace makes sure a wrongly injected digest would have
	// real content to leak, not just an empty header.
	fixture.createPetDraft(t, "core digest owner")
	if err := fixture.service.PetUpdateCore(fixture.ctx, "identity", "# 身份\n\n- 名字：不该出现的栗子\n"); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-core-digest-plain", Name: "Plain", CreatedAt: fixture.createdAt, UpdatedAt: fixture.createdAt}
	workspace := domain.Workspace{
		ID: "workspace-core-digest-plain", ProjectID: project.ID, Name: "plain", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: fixture.createdAt, UpdatedAt: fixture.createdAt,
	}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreateWorkspace(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "plain", Request: "plain request",
		ModelID: fixture.model.ID, Isolation: "inplace", Filesystem: "workspace-write", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := fixture.service.SubmitAgentMessage(fixture.ctx, task.ID, "main", "你好")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, fixture.database, turn.ID, domain.TurnSucceeded)
	prompts, _ := fixture.service.executor.(*stubExecutor).requestHistory()
	if len(prompts) == 0 {
		t.Fatal("turn did not reach the executor")
	}
	last := prompts[len(prompts)-1]
	if !strings.Contains(last, "## Main Agent\n") || !strings.Contains(last, "你好") {
		t.Fatalf("captured prompt is not the plain Task turn:\n%s", last)
	}
	for _, leaked := range []string{"core_version", "核心摘要", "Core digest", "不该出现的栗子"} {
		if strings.Contains(last, leaked) {
			t.Fatalf("non-pet Task prompt carries the pet core digest (%q):\n%s", leaked, last)
		}
	}
}

func TestPetWorkStateReturnsCoreDigest(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, "core digest state")
	if err := fixture.service.PetUpdateCore(fixture.ctx, "principles", "# 做事准则\n\n- 有始有终\n"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.PetUpdateCore(fixture.ctx, "loops", "# 开放闭环\n\n- waiting | 等主人确认 | 提醒 | 主人回复\n"); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Core.Principles != "- 有始有终" || state.Core.OpenLoopsTotal != 1 || len(state.Core.Version) != 12 {
		t.Fatalf("core digest=%#v", state.Core)
	}
	if !strings.Contains(state.OpenLoops, "等主人确认") || state.OpenLoopsPath != state.Core.Paths.Loops || state.AgendaPath != state.Core.Paths.Agenda || state.Agenda == "" || state.LabPath == "" {
		t.Fatalf("existing work_state fields changed: %#v", state)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"pending_approvals", "approvals", "work", "open_tasks", "inspection", "open_loops", "open_loops_path", "agenda", "agenda_path", "lab_path", "core"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("work_state JSON missing %q: %s", key, data)
		}
	}
	core, _ := payload["core"].(map[string]any)
	paths, _ := core["paths"].(map[string]any)
	for _, key := range []string{"core_version", "identity", "principles", "open_loops", "open_loops_total", "agenda", "agenda_total"} {
		if _, ok := core[key]; !ok {
			t.Fatalf("core JSON missing %q: %s", key, data)
		}
	}
	for _, key := range []string{"identity", "principles", "loops", "agenda"} {
		if value, _ := paths[key].(string); value == "" {
			t.Fatalf("core path %q missing: %s", key, data)
		}
	}
}
