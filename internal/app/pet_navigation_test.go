package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func openPetTaskForTest(t *testing.T, fixture *petTaskFixture, reference string) map[string]any {
	t.Helper()
	raw, err := fixture.service.PetMCPTool(fixture.ctx, "pet_open_task", json.RawMessage(fmt.Sprintf(`{"task":%q}`, reference)))
	if err != nil {
		t.Fatalf("pet_open_task(%q): %v", reference, err)
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("pet_open_task result %q: %v", raw, err)
	}
	return payload
}

func TestPetOpenTaskQueuesWithoutWebClientAndConsumesOnce(t *testing.T) {
	fixture := newPetTaskFixture(t)
	target := fixture.createPetDraft(t, "待打开的任务")

	// No SSE subscriber is attached here, which is the background-Turn shape.
	payload := openPetTaskForTest(t, fixture, target.ID)
	if payload["status"] != domain.PetNavigationQueued {
		t.Fatalf("status=%v, want queued: %#v", payload["status"], payload)
	}
	navID, _ := payload["navigation_id"].(string)
	if navID == "" || payload["task_id"] != target.ID || payload["expires_at"] == "" {
		t.Fatalf("queued payload=%#v", payload)
	}

	claimed, expired, err := fixture.service.ClaimPetNavigation(fixture.ctx, "web-one", nil)
	if err != nil {
		t.Fatal(err)
	}
	if expired != 0 || len(claimed) != 1 || claimed[0].ID != navID || claimed[0].TaskID != target.ID || claimed[0].ConsumedBy != "web-one" {
		t.Fatalf("claim=%#v expired=%d", claimed, expired)
	}
	again, _, err := fixture.service.ClaimPetNavigation(fixture.ctx, "web-two", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second session replayed the action: %#v", again)
	}
	stored, err := fixture.database.PetNavigationAction(fixture.ctx, navID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.PetNavigationConsumed || stored.ConsumedBy != "web-one" {
		t.Fatalf("stored action=%#v", stored)
	}
}

func TestPetOpenTaskAcceptsTaskCode(t *testing.T) {
	fixture := newPetTaskFixture(t)
	target := fixture.createPetDraft(t, "按编号打开")
	if target.Code == "" {
		t.Fatal("test Task has no code")
	}
	payload := openPetTaskForTest(t, fixture, target.Code)
	if payload["task_id"] != target.ID {
		t.Fatalf("code resolved to %v, want %s: %#v", payload["task_id"], target.ID, payload)
	}
	claimed, _, err := fixture.service.ClaimPetNavigation(fixture.ctx, "web-code", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].TaskID != target.ID {
		t.Fatalf("claimed=%#v", claimed)
	}
}

func TestPetOpenTaskReportsUnknownTask(t *testing.T) {
	fixture := newPetTaskFixture(t)
	_, err := fixture.service.PetMCPTool(fixture.ctx, "pet_open_task", json.RawMessage(`{"task":"task-999999"}`))
	if err == nil || !strings.Contains(err.Error(), "找不到要打开的 Task") {
		t.Fatalf("unknown task error=%v", err)
	}
	if strings.Contains(err.Error(), "没有可执行的宠物动作") {
		t.Fatalf("unknown task still reported as a generic no-op: %v", err)
	}
}

func TestPetNavigationExpiresWithoutMisnavigating(t *testing.T) {
	fixture := newPetTaskFixture(t)
	base := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return base }
	target := fixture.createPetDraft(t, "过期任务")
	payload := openPetTaskForTest(t, fixture, target.ID)
	navID, _ := payload["navigation_id"].(string)

	fixture.service.now = func() time.Time { return base.Add(petNavigationTTL + time.Second) }
	claimed, expired, err := fixture.service.ClaimPetNavigation(fixture.ctx, "late-web", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 || expired != 1 {
		t.Fatalf("expired claim=%#v expired=%d", claimed, expired)
	}
	stored, err := fixture.database.PetNavigationAction(fixture.ctx, navID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.PetNavigationExpired {
		t.Fatalf("stored status=%q", stored.Status)
	}
	// An expired row never becomes claimable again.
	fixture.service.now = func() time.Time { return base }
	claimed, expired, err = fixture.service.ClaimPetNavigation(fixture.ctx, "time-traveller", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 || expired != 0 {
		t.Fatalf("expired action was replayed: %#v expired=%d", claimed, expired)
	}
}

func TestPetNavigationClaimIsSingleUseAcrossSessions(t *testing.T) {
	fixture := newPetTaskFixture(t)
	target := fixture.createPetDraft(t, "并发领取")
	openPetTaskForTest(t, fixture, target.ID)

	const sessions = 12
	start := make(chan struct{})
	errs := make(chan error, sessions)
	var winners int32
	var wait sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			claimed, _, err := fixture.service.ClaimPetNavigation(fixture.ctx, fmt.Sprintf("web-%d", index), nil)
			if err != nil {
				errs <- err
				return
			}
			if len(claimed) > 0 {
				atomic.AddInt32(&winners, 1)
			}
		}(i)
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent claim: %v", err)
	}
	if winners != 1 {
		t.Fatalf("queued action was claimed %d times", winners)
	}
}

func TestPetOpenTaskStillPublishesLiveNavigation(t *testing.T) {
	fixture := newPetTaskFixture(t)
	target := fixture.createPetDraft(t, "实时导航")
	events, unsubscribe := fixture.service.Hub().SubscribeAll()
	defer unsubscribe()

	payload := openPetTaskForTest(t, fixture, target.ID)
	navID, _ := payload["navigation_id"].(string)
	select {
	case event := <-events:
		if event.Type != "pet_action" {
			t.Fatalf("event type=%q", event.Type)
		}
		actions, _ := event.Data["actions"].([]PetBrainAction)
		if len(actions) != 1 || actions[0].Type != "open_task" || actions[0].Task != target.ID || actions[0].NavigationID != navID {
			t.Fatalf("live actions=%#v", actions)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("live pet_action was not published")
	}
}
