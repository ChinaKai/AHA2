package app

import (
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// The pet is the Owner's partner with interests of its own. A wake that has no
// work waiting becomes free time while the day's allowance lasts; work always
// comes first, and the allowance is counted in Turns.
func TestPetIdleWakeBecomesFreeTimeWithinTheDailyAllowance(t *testing.T) {
	fixture := newPetTaskFixture(t)
	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if assistant.PetFreeDailyTurns != 3 {
		t.Fatalf("default free-time allowance=%d, want 3", assistant.PetFreeDailyTurns)
	}
	// Quiet hours off and no hourly cap, so only the free-time allowance decides.
	if err := fixture.database.UpdateOwnerPetBudget(fixture.ctx, assistant.ID, assistant.PetIdleMinutes, false, assistant.PetQuietStart, assistant.PetQuietEnd, 0, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateOwnerPetFreeDailyTurns(fixture.ctx, assistant.ID, 2, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fixture.createPetDraft(t, "free time")
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SubmitMessage(fixture.ctx, petTaskID, "你好"); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)

	wake := func(device string) string {
		t.Helper()
		if _, err := fixture.service.AskOwnerPetBrain(fixture.ctx, PetBrainContext{
			DeviceID: device, IdleSeconds: 900, Page: "后台（服务端调度）",
		}); err != nil {
			t.Fatal(err)
		}
		waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
		prompts, _ := fixture.service.executor.(*stubExecutor).requestHistory()
		return prompts[len(prompts)-1]
	}
	used := func() int {
		t.Helper()
		current, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		return current.PetFreeUsed
	}

	// Work waiting: the pet marked an agenda item as its own next step.
	if err := fixture.service.PetUpdateCore(fixture.ctx, "agenda", "# 自主议程\n\n- next | 整理巡检脚本 | 自己能推进\n"); err != nil {
		t.Fatal(err)
	}
	if prompt := wake("work-first"); strings.Contains(prompt, "自由时间") || !strings.Contains(prompt, "[自主行为]") {
		t.Fatalf("a wake with work waiting was handed out as free time:\n%s", prompt)
	}
	if got := used(); got != 0 {
		t.Fatalf("a work wake consumed the free-time allowance: used=%d", got)
	}

	// Only items waiting on the Owner remain: those are not work for the pet.
	if err := fixture.service.PetUpdateCore(fixture.ctx, "agenda", "# 自主议程\n\n- waiting | 等 owner 验收 | owner 决定\n"); err != nil {
		t.Fatal(err)
	}
	first := wake("free-1")
	if !strings.Contains(first, "[自由时间 今天第 1/2 次]") || strings.Contains(first, "[自主行为]") {
		t.Fatalf("an idle wake was not free time:\n%s", first)
	}
	if !strings.Contains(first, "不要打扰 owner") {
		t.Fatal("a background free-time wake did not keep the pet from disturbing the Owner")
	}
	if second := wake("free-2"); !strings.Contains(second, "[自由时间 今天第 2/2 次]") {
		t.Fatalf("the second idle wake was not free time:\n%s", second)
	}
	// The allowance is spent: the next idle wake is an ordinary one again.
	if third := wake("free-3"); strings.Contains(third, "自由时间") || !strings.Contains(third, "[自主行为]") {
		t.Fatalf("free time continued past the daily allowance:\n%s", third)
	}
	if got := used(); got != 2 {
		t.Fatalf("free-time Turns used=%d, want 2", got)
	}

	// A new local day starts a fresh count.
	next := time.Now().Add(24 * time.Hour)
	fixture.service.now = func() time.Time { return next }
	if fresh := wake("next-day"); !strings.Contains(fresh, "[自由时间 今天第 1/2 次]") {
		t.Fatalf("the allowance did not reset on a new day:\n%s", fresh)
	}

	// An allowance of zero turns free time off.
	if err := fixture.database.UpdateOwnerPetFreeDailyTurns(fixture.ctx, assistant.ID, 0, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if off := wake("off"); strings.Contains(off, "自由时间") {
		t.Fatalf("free time was handed out with the allowance set to 0:\n%s", off)
	}
}
