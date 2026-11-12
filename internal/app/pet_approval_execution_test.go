package app

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func createHeavyTestApproval(t *testing.T, fixture *petTaskFixture, kind, payload string) domain.PetApproval {
	t.Helper()
	approval, err := fixture.service.CreatePetApproval(fixture.ctx, PetApprovalInput{
		Kind: kind, Title: "后台执行测试", Summary: "验证重审批在后台执行", Risk: "high", Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return approval
}

func TestHeavyPetApprovalConcurrentResolveClaimsOnce(t *testing.T) {
	fixture := newPetTaskFixture(t)
	approval := createHeavyTestApproval(t, fixture, "delete_workspace", `{"workspace_id":"missing"}`)
	release := make(chan struct{})
	var executions atomic.Int32
	fixture.service.SetPetApprovalExecutionHook(func(ctx context.Context, item domain.PetApproval) error {
		executions.Add(1)
		<-release
		return nil
	})

	var wait sync.WaitGroup
	results := make([]error, 2)
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, results[index] = fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", "")
		}(index)
	}
	wait.Wait()
	close(release)

	accepted := 0
	for _, err := range results {
		if err == nil {
			accepted++
			continue
		}
		if !strings.Contains(err.Error(), "已经处理过") {
			t.Fatalf("losing resolve returned %v, want an already-processed conflict", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted resolves=%d, want exactly 1 (results=%v)", accepted, results)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("background executions=%d, want exactly 1", got)
	}
	if final := waitPetApprovalFinal(t, fixture, approval.ID); final.Status != petApprovalApproved {
		t.Fatalf("approval did not finish approved: %#v", final)
	}
}

func TestHeavyPetApprovalResolveReturnsBeforeSlowExecution(t *testing.T) {
	fixture := newPetTaskFixture(t)
	approval := createHeavyTestApproval(t, fixture, "delete_task", `{"task_id":"missing"}`)
	fixture.service.SetPetApprovalExecutionHook(func(ctx context.Context, item domain.PetApproval) error {
		select {
		case <-time.After(300 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	started := time.Now()
	claimed, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", "")
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed >= time.Second {
		t.Fatalf("resolve waited %s for the heavy execution, want under 1s", elapsed)
	}
	if claimed.Status != petApprovalExecuting {
		t.Fatalf("claimed approval=%#v, want executing", claimed)
	}
	if final := waitPetApprovalFinal(t, fixture, approval.ID); final.Status != petApprovalApproved {
		t.Fatalf("background execution did not finish approved: %#v", final)
	}
}

func TestHeavyPetApprovalExecutionSurvivesRequestCancellation(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowCreateWorkspaces: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "断连测试项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "断连测试 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := fixture.service.PetDeleteWorkspace(fixture.ctx, workspace.ID, "验证请求断开不会取消后台删除")
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.SetPetApprovalExecutionHook(func(ctx context.Context, item domain.PetApproval) error {
		// The request context is canceled while this runs. Only the service
		// context reaches the hook, so the delete must still commit.
		time.Sleep(100 * time.Millisecond)
		return fixture.service.executePetApproval(ctx, item)
	})

	requestCtx, cancel := context.WithCancel(context.Background())
	claimed, err := fixture.service.ResolvePetApproval(requestCtx, approval.ID, "approve", "")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Status != petApprovalExecuting {
		t.Fatalf("claimed approval=%#v, want executing", claimed)
	}
	if final := waitPetApprovalFinal(t, fixture, approval.ID); final.Status != petApprovalApproved {
		t.Fatalf("approval did not finish approved after request cancellation: %#v", final)
	}
	if _, err := fixture.database.Workspace(fixture.ctx, workspace.ID); err == nil {
		t.Fatal("workspace still exists: the canceled request stopped the background delete")
	}
}

func TestLightPetApprovalStillResolvesSynchronously(t *testing.T) {
	fixture := newPetTaskFixture(t)
	approval := createHeavyTestApproval(t, fixture, "owner_decision", `{}`)
	var executions atomic.Int32
	fixture.service.SetPetApprovalExecutionHook(func(ctx context.Context, item domain.PetApproval) error {
		executions.Add(1)
		return nil
	})
	item, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", "")
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != petApprovalApproved || item.ResolvedAt.IsZero() {
		t.Fatalf("light approval=%#v, want an immediately approved record", item)
	}
	if executions.Load() != 0 {
		t.Fatal("light approval was routed through the background executor")
	}
}
