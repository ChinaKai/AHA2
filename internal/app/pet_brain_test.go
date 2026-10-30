package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestParsePetBrainValidatesActionsAndSay(t *testing.T) {
	result := parsePetBrain("```json\n{\"say\":\"我去看看\",\"actions\":[{\"type\":\"play\",\"name\":\"jump\"},{\"type\":\"play\",\"name\":\"turn\"},{\"type\":\"move_to\",\"zone\":\"left\"},{\"type\":\"open_task\",\"task\":\"task-032\"}]}\n```")
	if result.Say != "我去看看" {
		t.Fatalf("say=%q", result.Say)
	}
	if len(result.Actions) != 2 || result.Actions[0].Type != "play" || result.Actions[1].Type != "move_to" {
		t.Fatalf("actions=%#v", result.Actions)
	}
}

func TestParsePetBrainRejectsMalformedAndUnsafeActions(t *testing.T) {
	if result := parsePetBrain("not json"); result.Say != "" || len(result.Actions) != 0 {
		t.Fatalf("malformed=%#v", result)
	}
	result := parsePetBrain(`{"say":"x","actions":[{"type":"play","name":"explode"},{"type":"open_task","task":"../../etc"}]}`)
	if len(result.Actions) != 0 {
		t.Fatalf("unsafe=%#v", result.Actions)
	}
}

func TestAskOwnerPetBrainReopensFailedPetTask(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, "旧的宠物需求")
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SubmitMessage(fixture.ctx, petTaskID, "主人第一条消息"); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
	now := fixture.service.now().UTC()
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, petTaskID, domain.TaskWaitingUser, domain.TaskActive, timeString(now), ""); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, petTaskID, domain.TaskActive, domain.TaskFailed, timeString(now), ""); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.AskOwnerPetBrain(fixture.ctx, PetBrainContext{
		DeviceID: "test-reopen", IdleSeconds: 1200, Page: "test",
	}); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
	if count := fixture.turnCount(t); count != 2 {
		t.Fatalf("autonomy did not submit after reopen: turn count=%d", count)
	}
}

func TestPetAutonomySkipOnlyHidesExpectedSkips(t *testing.T) {
	t.Parallel()
	for _, message := range []string{
		"宠物刚刚动过，稍后再试", "现在处于宠物安静时段", "宠物这个小时已经够活跃了",
		"宠物 Task 正在执行，跳过本次自主行为",
	} {
		if !petAutonomySkip(errString(message)) {
			t.Fatalf("expected skip for %q", message)
		}
	}
	if petAutonomySkip(errString("claude returned no agent message")) {
		t.Fatal("real backend failure was treated as a skip")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestRecordPetAutonomyErrorWritesActivity(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.service.recordPetAutonomyError(fixture.ctx, errors.New("backend unavailable"))
	events, err := fixture.database.ListPetEvents(fixture.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == "autonomy_error" && event.Status == "failed" && strings.Contains(event.Detail, "backend unavailable") {
			return
		}
	}
	t.Fatalf("autonomy error was not recorded: %#v", events)
}
