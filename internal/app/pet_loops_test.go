package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetOpenLoopsDropsClosedSections(t *testing.T) {
	fixture := newPetTaskFixture(t)
	content := `# 开放闭环

- [waiting] 活跃事项 A | 下轮检查 | Task 完成

## 已关闭

- [done] 旧事项 B | 已发布 | 已完成
### 历史归档
- [done] 旧事项 C

## 备注

- 保留这条备注
`
	if err := fixture.service.PetUpdateCore(fixture.ctx, "loops", content); err != nil {
		t.Fatal(err)
	}
	loops := fixture.service.PetOpenLoops(fixture.ctx)
	for _, want := range []string{"活跃事项 A", "## 备注", "保留这条备注"} {
		if !strings.Contains(loops, want) {
			t.Fatalf("active loops missing %q: %s", want, loops)
		}
	}
	for _, unwanted := range []string{"旧事项 B", "旧事项 C", "## 已关闭"} {
		if strings.Contains(loops, unwanted) {
			t.Fatalf("archived loops leaked %q: %s", unwanted, loops)
		}
	}
}

func TestPetAgendaIsSeededUpdatedAndSurfaced(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if agenda := fixture.service.PetAgenda(fixture.ctx); !strings.Contains(agenda, "# 自主议程") || !strings.Contains(agenda, "owner_decision") {
		t.Fatalf("agenda was not seeded with decision guidance: %q", agenda)
	}
	content := "# 自主议程\n\n- [next] 优化唤醒 | 创建 Task 验证 | 测试通过 | 否\n"
	if err := fixture.service.PetUpdateCore(fixture.ctx, "agenda", content); err != nil {
		t.Fatal(err)
	}
	if agenda := fixture.service.PetAgenda(fixture.ctx); !strings.Contains(agenda, "优化唤醒") {
		t.Fatalf("agenda update was not stored: %q", agenda)
	}
	state, err := fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Agenda, "优化唤醒") || !strings.HasSuffix(state.AgendaPath, "core/agenda.md") && !strings.HasSuffix(state.AgendaPath, `core\agenda.md`) {
		t.Fatalf("agenda missing from work state: %#v", state)
	}
	documents, err := fixture.service.PetCoreDocuments(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, document := range documents {
		if document.Name == "agenda.md" && strings.Contains(document.Content, "优化唤醒") {
			found = true
		}
	}
	if !found {
		t.Fatalf("agenda missing from core documents: %#v", documents)
	}
}

func TestPetWorkspaceSeedsLabAndReportsPath(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if agenda := fixture.service.PetAgenda(fixture.ctx); agenda == "" {
		t.Fatal("pet agenda was not seeded")
	}
	root := fixture.service.petWorkspaceRoot(fixture.ctx)
	readme, err := os.ReadFile(filepath.Join(root, petLabDir, "README.md"))
	if err != nil || !strings.Contains(string(readme), "直接写脚本") {
		t.Fatalf("pet-lab README=%q err=%v", readme, err)
	}
	state, err := fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(state.LabPath, petLabDir) && !strings.HasSuffix(state.LabPath, `pet-lab`) {
		t.Fatalf("pet-lab path missing from work state: %#v", state)
	}
}

func TestPetUpdateCoreRejectsOversizedAgenda(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if err := fixture.service.PetUpdateCore(fixture.ctx, "agenda", strings.Repeat("x", petCoreAgendaMaxBytes+1)); err == nil {
		t.Fatal("oversized agenda document was accepted")
	}
}

func TestPetAutonomyIntervalFloorsAtFiveMinutes(t *testing.T) {
	if got := petAutonomyInterval(domain.OwnerAssistant{PetIdleMinutes: 1}); got != 5*time.Minute {
		t.Fatalf("sub-minimum idle interval=%s, want 5m", got)
	}
	if got := petAutonomyInterval(domain.OwnerAssistant{PetIdleMinutes: 5}); got != 5*time.Minute {
		t.Fatalf("minimum idle interval=%s, want 5m", got)
	}
	if got := petAutonomyInterval(domain.OwnerAssistant{PetIdleMinutes: 45}); got != 45*time.Minute {
		t.Fatalf("long idle interval=%s, want 45m", got)
	}
}

func TestPetUpdateCoreRejectsOversizedLoops(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if err := fixture.service.PetUpdateCore(fixture.ctx, "loops", strings.Repeat("x", petCoreLoopsMaxBytes+1)); err == nil {
		t.Fatal("oversized loops document was accepted")
	}
}

func TestPetLoopsNoteWarnsWhenLedgerIsLarge(t *testing.T) {
	if note := petLoopsNote("- [waiting] 一项\n"); note != "" {
		t.Fatalf("small ledger warned: %q", note)
	}
	tooMany := strings.Repeat("- [waiting] 一项 | 下一步 | 关闭条件\n", petCoreLoopsSoftItems+1)
	if note := petLoopsNote(tooMany); note == "" {
		t.Fatal("item-heavy ledger did not warn")
	}
	tooLarge := strings.Repeat("x", petCoreLoopsSoftBytes+1)
	if note := petLoopsNote(tooLarge); note == "" {
		t.Fatal("byte-heavy ledger did not warn")
	}
}
