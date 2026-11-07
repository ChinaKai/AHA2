package app

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetAutonomousMessageFollowsOwnerLanguage(t *testing.T) {
	message := petAutonomousMessage(true, 20, "后台（服务端调度）", PetLayout{})
	for _, want := range []string{"[Autonomous behavior]", "20 minutes since the last autonomous review", "background (server scheduler)", "Page layout unavailable", "work wake-up", "core/agenda.md", "aha_pet_work_state", "inspection", "open_tasks", "owner_decision", "pet_request_approval", "NOOP"} {
		if !strings.Contains(message, want) {
			t.Fatalf("English autonomous message missing %q: %s", want, message)
		}
	}
	for _, unwanted := range []string{"[自主行为]", "主人已经", "页面布局", "Task A | check next turn"} {
		if strings.Contains(message, unwanted) {
			t.Fatalf("English autonomous message still contains %q: %s", unwanted, message)
		}
	}
	chinese := petAutonomousMessage(false, 20, "后台（服务端调度）", PetLayout{})
	for _, want := range []string{"[自主行为] 距上次自主复盘约 20 分钟", "工作唤醒", "core/agenda.md", "aha_pet_work_state", "inspection", "open_tasks", "owner_decision", "pet_request_approval", "巡检摘要", "NOOP"} {
		if !strings.Contains(chinese, want) {
			t.Fatalf("Chinese autonomous message missing %q: %s", want, chinese)
		}
	}
	if strings.Contains(chinese, "任务 A") {
		t.Fatalf("Chinese autonomous message should not inline open loops: %s", chinese)
	}
}

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
	// The autonomy gate is time-of-day dependent; disable the quiet window so
	// this test does not start failing whenever the suite runs at night.
	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateOwnerPetBudget(fixture.ctx, assistant.ID, assistant.PetIdleMinutes, false, assistant.PetQuietStart, assistant.PetQuietEnd, assistant.PetHourlyLimit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
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

func TestPetBrainQuietPollsDoNotConsumeHourlyBudget(t *testing.T) {
	fixture := newPetTaskFixture(t)
	// Cover the current local hour (plus margin) so the quiet gate rejects every
	// poll no matter when the suite runs.
	hour := time.Now().Hour()
	enablePetBrainTestAssistant(t, fixture, 15, true, (hour+23)%24, (hour+2)%24, 1)
	clock := time.Now().UTC()
	fixture.service.now = func() time.Time { return clock }
	var lastCall time.Time
	for attempt := 0; attempt < 4; attempt++ {
		lastCall = clock
		_, err := fixture.service.AskOwnerPetBrain(fixture.ctx, PetBrainContext{
			DeviceID: "quiet-poll", IdleSeconds: 900, Page: "test",
		})
		if err == nil || !strings.Contains(err.Error(), "安静时段") {
			t.Fatalf("attempt %d: expected quiet skip, got %v", attempt, err)
		}
		clock = clock.Add(15 * time.Minute)
	}
	// The 30-second throttle still applies before the business gates.
	clock = lastCall.Add(10 * time.Second)
	if _, err := fixture.service.AskOwnerPetBrain(fixture.ctx, PetBrainContext{
		DeviceID: "quiet-poll", IdleSeconds: 900, Page: "test",
	}); err == nil || !strings.Contains(err.Error(), "刚刚动过") {
		t.Fatalf("expected throttle skip, got %v", err)
	}
	if got := petBrainRunCount(fixture.service, "quiet-poll"); got != 0 {
		t.Fatalf("quiet polls consumed the hourly budget: runs=%d", got)
	}
}

func TestPetBrainQuotaRejectionsDoNotFillTheWindow(t *testing.T) {
	fixture := newPetTaskFixture(t)
	// The reported production shape: poll every 15 minutes with a limit of one
	// per rolling hour.
	base := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	if err := fixture.service.reservePetBrainRun("idle15", base, 1); err != nil {
		t.Fatalf("first run should be allowed: %v", err)
	}
	for _, minutes := range []int{15, 30, 45} {
		if err := fixture.service.reservePetBrainRun("idle15", base.Add(time.Duration(minutes)*time.Minute), 1); err == nil {
			t.Fatalf("+%dmin should be over the hourly limit", minutes)
		}
		if got := petBrainRunCount(fixture.service, "idle15"); got != 1 {
			t.Fatalf("+%dmin rejection changed the window: runs=%d", minutes, got)
		}
	}
	// Exactly one hour later the original run leaves the rolling window, so the
	// next poll runs instead of staying starved forever.
	if err := fixture.service.reservePetBrainRun("idle15", base.Add(time.Hour), 1); err != nil {
		t.Fatalf("next window should run: %v", err)
	}
	if got := petBrainRunCount(fixture.service, "idle15"); got != 1 {
		t.Fatalf("rolling-hour window should hold one run: runs=%d", got)
	}
}

func TestPetBrainIdle60KeepsHourlyBehavior(t *testing.T) {
	fixture := newPetTaskFixture(t)
	base := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	if err := fixture.service.reservePetBrainRun("idle60", base, 1); err != nil {
		t.Fatalf("first run should be allowed: %v", err)
	}
	if err := fixture.service.reservePetBrainRun("idle60", base.Add(30*time.Minute), 1); err == nil {
		t.Fatal("second run within the same rolling hour should be rejected")
	}
	if err := fixture.service.reservePetBrainRun("idle60", base.Add(time.Hour), 1); err != nil {
		t.Fatalf("next hour should run: %v", err)
	}
	if got := petBrainRunCount(fixture.service, "idle60"); got != 1 {
		t.Fatalf("runs=%d, want 1", got)
	}
}

func TestPetBrainZeroHourlyLimitIsUnlimited(t *testing.T) {
	fixture := newPetTaskFixture(t)
	base := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	for _, minutes := range []int{0, 15, 30, 45, 55} {
		if err := fixture.service.reservePetBrainRun("unlimited", base.Add(time.Duration(minutes)*time.Minute), 0); err != nil {
			t.Fatalf("+%dmin: limit=0 should never reject: %v", minutes, err)
		}
	}
	if got := petBrainRunCount(fixture.service, "unlimited"); got != 5 {
		t.Fatalf("runs=%d, want 5", got)
	}
}

func TestPetBrainBusyTaskSkipDoesNotConsumeHourlyBudget(t *testing.T) {
	fixture := newPetTaskFixture(t)
	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateOwnerPetBudget(fixture.ctx, assistant.ID, 15, false, assistant.PetQuietStart, assistant.PetQuietEnd, 1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fixture.createOrphanTurn(t, domain.TurnRunning)
	clock := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return clock }
	if _, err := fixture.service.AskOwnerPetBrain(fixture.ctx, PetBrainContext{
		DeviceID: "busy-task", IdleSeconds: 900, Page: "test",
	}); err == nil || !strings.Contains(err.Error(), "正在执行") {
		t.Fatalf("expected busy-task skip, got %v", err)
	}
	if got := petBrainRunCount(fixture.service, "busy-task"); got != 0 {
		t.Fatalf("busy-task skip consumed the hourly budget: runs=%d", got)
	}
}

func TestPetBrainReserveIsAtomicAcrossConcurrentPolls(t *testing.T) {
	fixture := newPetTaskFixture(t)
	base := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	const pollers = 32
	var granted int32
	var wait sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < pollers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if fixture.service.reservePetBrainRun("concurrent", base, 1) == nil {
				atomic.AddInt32(&granted, 1)
			}
		}()
	}
	close(start)
	wait.Wait()
	if granted != 1 {
		t.Fatalf("concurrent polls granted %d slots, want 1", granted)
	}
	if got := petBrainRunCount(fixture.service, "concurrent"); got != 1 {
		t.Fatalf("runs=%d, want 1", got)
	}
}

func TestPetBrainReleaseReturnsFailedSubmissionBudget(t *testing.T) {
	fixture := newPetTaskFixture(t)
	base := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	if err := fixture.service.reservePetBrainRun("failed-submit", base, 1); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	fixture.service.releasePetBrainRun("failed-submit", base)
	if got := petBrainRunCount(fixture.service, "failed-submit"); got != 0 {
		t.Fatalf("released run still counted: runs=%d", got)
	}
	if err := fixture.service.reservePetBrainRun("failed-submit", base, 1); err != nil {
		t.Fatalf("released budget should be reusable: %v", err)
	}
}

func TestAskOwnerPetBrainReleasesBudgetWhenSubmissionFails(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetBrainTestAssistant(t, fixture, 15, false, 0, 0, 1)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)

	clock := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return clock }
	before := fixture.turnCount(t)

	// Delete the model behind the Pet Task's frozen runtime snapshot. Every gate
	// in AskOwnerPetBrain still passes, so the only failure is SubmitMessage
	// rejecting the turn. This exercises the entry-point rollback instead of
	// calling releasePetBrainRun directly.
	if err := fixture.database.DeleteModel(fixture.ctx, fixture.model.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.AskOwnerPetBrain(fixture.ctx, PetBrainContext{
		DeviceID: "entry-rollback", IdleSeconds: 900, Page: "test",
	}); err == nil || !strings.Contains(err.Error(), "模型已删除") {
		t.Fatalf("expected SubmitMessage runtime failure, got %v", err)
	}
	if got := petBrainRunCount(fixture.service, "entry-rollback"); got != 0 {
		t.Fatalf("failed submission kept the reserved run: runs=%d", got)
	}
	if got := fixture.turnCount(t); got != before {
		t.Fatalf("failed submission still queued a turn: count=%d, want %d", got, before)
	}

	// Restoring the runtime and stepping past the per-device throttle must let the
	// next poll reserve and submit again.
	if err := fixture.database.UpsertModel(fixture.ctx, fixture.model); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Minute)
	if _, err := fixture.service.AskOwnerPetBrain(fixture.ctx, PetBrainContext{
		DeviceID: "entry-rollback", IdleSeconds: 900, Page: "test",
	}); err != nil {
		t.Fatalf("next run should succeed: %v", err)
	}
	if got := petBrainRunCount(fixture.service, "entry-rollback"); got != 1 {
		t.Fatalf("next run did not reserve a slot: runs=%d", got)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
	if got := fixture.turnCount(t); got != before+1 {
		t.Fatalf("turn count=%d, want %d", got, before+1)
	}
}

func TestPetBrainHourlyLimitRecoversAcrossWindows(t *testing.T) {
	fixture := newPetTaskFixture(t)
	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	// idle=15, limit=1: four polls an hour, one actual autonomous turn.
	if err := fixture.database.UpdateOwnerPetBudget(fixture.ctx, assistant.ID, 15, false, assistant.PetQuietStart, assistant.PetQuietEnd, 1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fixture.createPetDraft(t, "旧的宠物需求")
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SubmitMessage(fixture.ctx, petTaskID, "主人第一条消息"); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)

	base := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	var clockNanos atomic.Int64
	clockNanos.Store(base.UnixNano())
	fixture.service.now = func() time.Time { return time.Unix(0, clockNanos.Load()).UTC() }
	if _, err := fixture.service.AskOwnerPetBrain(fixture.ctx, PetBrainContext{
		DeviceID: "scheduler", IdleSeconds: 900, Page: "后台（服务端调度）",
	}); err != nil {
		t.Fatalf("first autonomous run: %v", err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
	if got := fixture.turnCount(t); got != 2 {
		t.Fatalf("turn count=%d, want 2", got)
	}
	if got := petBrainRunCount(fixture.service, "scheduler"); got != 1 {
		t.Fatalf("runs=%d, want 1", got)
	}
	for _, minutes := range []int{15, 30, 45} {
		clockNanos.Store(base.Add(time.Duration(minutes) * time.Minute).UnixNano())
		if _, err := fixture.service.AskOwnerPetBrain(fixture.ctx, PetBrainContext{
			DeviceID: "scheduler", IdleSeconds: 900, Page: "后台（服务端调度）",
		}); err == nil || !strings.Contains(err.Error(), "够活跃") {
			t.Fatalf("+%dmin: expected hourly-limit skip, got %v", minutes, err)
		}
	}
	if got := fixture.turnCount(t); got != 2 {
		t.Fatalf("quota-rejected polls submitted turns: count=%d", got)
	}
	if got := petBrainRunCount(fixture.service, "scheduler"); got != 1 {
		t.Fatalf("quota-rejected polls changed the window: runs=%d", got)
	}
	clockNanos.Store(base.Add(time.Hour).UnixNano())
	if _, err := fixture.service.AskOwnerPetBrain(fixture.ctx, PetBrainContext{
		DeviceID: "scheduler", IdleSeconds: 900, Page: "后台（服务端调度）",
	}); err != nil {
		t.Fatalf("next window should recover: %v", err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
	if got := fixture.turnCount(t); got != 3 {
		t.Fatalf("turn count after recovery=%d, want 3", got)
	}
}

func enablePetBrainTestAssistant(t *testing.T, fixture *petTaskFixture, idleMinutes int, quietEnabled bool, quietStart, quietEnd, hourlyLimit int) {
	t.Helper()
	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateOwnerPetSettings(fixture.ctx, assistant.ID, true, assistant.BrainModelID, assistant.PetActions, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateOwnerPetBudget(fixture.ctx, assistant.ID, idleMinutes, quietEnabled, quietStart, quietEnd, hourlyLimit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func petBrainRunCount(service *Service, deviceID string) int {
	service.petBrainMu.Lock()
	defer service.petBrainMu.Unlock()
	return len(service.petBrainRuns[deviceID])
}

func TestPetAutonomyStatusCountsOnlyCurrentHour(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetBrainTestAssistant(t, fixture, 15, false, 0, 0, 1)
	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	fixture.service.petBrainMu.Lock()
	fixture.service.petBrainRuns = map[string][]time.Time{
		"server_autonomy": {
			now.Add(-2 * time.Hour),
			// Exactly on the cutoff is already outside the rolling window,
			// matching reservePetBrainRun's strictly-after comparison.
			now.Add(-time.Hour),
			now.Add(-30 * time.Minute),
			now.Add(-time.Minute),
		},
	}
	fixture.service.petBrainMu.Unlock()
	if _, _, runs := fixture.service.petAutonomyStatus(assistant); runs != 2 {
		t.Fatalf("status counted stale runs: runs=%d, want 2", runs)
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
