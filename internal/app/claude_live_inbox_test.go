package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

// streamInputExecutor stands in for a stream-input Claude run: it claims
// through ExecutionRequest.LiveInbox while the Turn is running and settles
// the claim the way the adapter does after the CLI's replay.
type streamInputExecutor struct {
	started       chan struct{}
	submitted     chan struct{}
	submittedOnce sync.Once
	claimed       chan backend.LiveBatch
	deliver       bool
	hadInbox      chan bool
}

// markSubmitted opens the claim gate once the test has durably enqueued the
// mid-turn message. Reading it from the test goroutine keeps the claim
// deterministic: without the gate the executor raced an arbitrary polling
// deadline against the test process's own scheduling.
func (executor *streamInputExecutor) markSubmitted() {
	executor.submittedOnce.Do(func() { close(executor.submitted) })
}

func (executor *streamInputExecutor) Execute(ctx context.Context, request ExecutionRequest, _ func(ExecutionEvent)) (ExecutionResult, error) {
	executor.hadInbox <- request.LiveInbox != nil
	if request.LiveInbox == nil || request.Turn.Generation > 1 || request.Turn.Sequence > 1 {
		return ExecutionResult{Reply: "follow-up", ProviderSessionID: "claude-live"}, nil
	}
	close(executor.started)
	select {
	case <-executor.submitted:
	case <-ctx.Done():
		return ExecutionResult{}, ctx.Err()
	}
	batch, err := request.LiveInbox.Claim(ctx)
	if err != nil {
		return ExecutionResult{}, err
	}
	if len(batch.IDs) == 0 {
		return ExecutionResult{}, fmt.Errorf("live inbox claim was empty after the mid-turn message was submitted")
	}
	// A second claim in the same Turn must not hand the batch out again.
	if again, err := request.LiveInbox.Claim(ctx); err != nil {
		return ExecutionResult{}, err
	} else if len(again.IDs) != 0 {
		return ExecutionResult{}, fmt.Errorf("second live inbox claim repeated %d message(s)", len(again.IDs))
	}
	if err := request.LiveInbox.Ack(ctx, batch, executor.deliver); err != nil {
		return ExecutionResult{}, err
	}
	executor.claimed <- batch
	return ExecutionResult{Reply: "first", ProviderSessionID: "claude-live"}, nil
}

func newLiveInboxTask(t *testing.T, backendName string, executor Executor) (*Service, *store.Store, domain.Task) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{ID: "project-claude-live", Name: "AHA2", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-claude-live", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-claude-live", Name: "env", ProviderID: "provider", Backend: backendName, Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-claude-live", DisplayName: "Model", ProviderID: "provider", Backend: backendName, WireModel: "model",
		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(database, secretStore, executor)
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "claude live", Request: "work", ModelID: model.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, database, task
}

func runClaudeLiveInboxTurn(t *testing.T, deliver bool) (*store.Store, domain.Task, backend.LiveBatch) {
	t.Helper()
	return runClaudeLiveInboxTurnWithSubmit(t, deliver, func(ctx context.Context, service *Service, taskID string) error {
		_, err := service.SubmitMessage(ctx, taskID, "mid-turn correction")
		return err
	})
}

func runClaudeLiveInboxTurnWithSubmit(
	t *testing.T, deliver bool,
	submitMidTurn func(context.Context, *Service, string) error,
) (*store.Store, domain.Task, backend.LiveBatch) {
	t.Helper()
	executor := &streamInputExecutor{
		started: make(chan struct{}), submitted: make(chan struct{}),
		claimed: make(chan backend.LiveBatch, 1), deliver: deliver,
		hadInbox: make(chan bool, 4),
	}
	// Unblock the executor even when the test fails before the mid-turn
	// message is submitted, so a failed fixture cannot strand the goroutine.
	t.Cleanup(executor.markSubmitted)
	service, database, task := newLiveInboxTask(t, "claude", executor)
	ctx := context.Background()
	if _, err := service.SubmitMessage(ctx, task.ID, "work"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Claude Turn did not start")
	}
	if err := submitMidTurn(ctx, service, task.ID); err != nil {
		t.Fatal(err)
	}
	executor.markSubmitted()
	var batch backend.LiveBatch
	select {
	case batch = <-executor.claimed:
	case <-time.After(10 * time.Second):
		t.Fatal("live message was not claimed through ExecutionRequest.LiveInbox")
	}
	if !<-executor.hadInbox {
		t.Fatal("Claude Turn had no LiveInbox")
	}
	return database, task, batch
}

// A delivered live claim settles inside the running Turn: no follow-up Turn,
// nothing left pending.
func TestClaudeTurnLiveInboxDeliversThroughClaimAck(t *testing.T) {
	database, task, batch := runClaudeLiveInboxTurn(t, true)
	if len(batch.IDs) != 1 || batch.Text == "" {
		t.Fatalf("batch = %#v", batch)
	}
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	ctx := context.Background()
	if pending, err := database.PendingInboxCount(ctx, task.ID); err != nil || pending != 0 {
		t.Fatalf("pending=%d err=%v", pending, err)
	}
	round, err := database.LatestRound(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turns, err := database.TurnsForRound(ctx, round.ID); err != nil || len(turns) != 1 {
		t.Fatalf("delivered live message started another Turn: turns=%d err=%v", len(turns), err)
	}
}

// A pet-delegated message follows the same in-Turn claim/ack path, but its
// injection text keeps the pet authority distinct from the owner.
func TestClaudeTurnLiveInboxDeliversPetDelegatedMessage(t *testing.T) {
	database, task, batch := runClaudeLiveInboxTurnWithSubmit(t, true,
		func(ctx context.Context, service *Service, taskID string) error {
			_, err := service.SubmitPetDelegatedMessage(ctx, taskID, "pet delegation probe")
			return err
		})
	if len(batch.IDs) != 1 {
		t.Fatalf("batch = %#v", batch)
	}
	for _, expected := range []string{
		"[AHA live inbox] 1 new message(s)",
		"### Message 1",
		"Source: pet (authority=owner_delegate)",
		"pet delegation probe",
	} {
		if !strings.Contains(batch.Text, expected) {
			t.Fatalf("pet injection text missing %q:\n%s", expected, batch.Text)
		}
	}
	if strings.Contains(batch.Text, "owner (authority=owner)") {
		t.Fatalf("pet message was rendered as the owner:\n%s", batch.Text)
	}
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	ctx := context.Background()
	if pending, err := database.PendingInboxCount(ctx, task.ID); err != nil || pending != 0 {
		t.Fatalf("pending=%d err=%v", pending, err)
	}
	round, err := database.LatestRound(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turns, err := database.TurnsForRound(ctx, round.ID); err != nil || len(turns) != 1 {
		t.Fatalf("delivered pet message started another Turn: turns=%d err=%v", len(turns), err)
	}
}

// A rolled-back claim is still owed to the model, so the Turn boundary picks
// it up again instead of losing it.
func TestClaudeTurnLiveInboxRollbackRequeues(t *testing.T) {
	database, task, _ := runClaudeLiveInboxTurn(t, false)
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		round, err := database.LatestRound(ctx, task.ID)
		if err == nil {
			turns, _ := database.TurnsForRound(ctx, round.ID)
			if len(turns) > 1 {
				return
			}
			pending, _ := database.PendingInboxCount(ctx, task.ID)
			current, _ := database.Task(ctx, task.ID)
			if pending > 0 && current.Status == domain.TaskWaitingUser {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("rolled-back live message was neither requeued nor picked up by a follow-up Turn")
}

// inboxProbeExecutor reports whether the Turn carried a LiveInbox and stops.
type inboxProbeExecutor struct {
	hadInbox chan bool
}

func (executor *inboxProbeExecutor) Execute(_ context.Context, request ExecutionRequest, _ func(ExecutionEvent)) (ExecutionResult, error) {
	executor.hadInbox <- request.LiveInbox != nil
	return ExecutionResult{Reply: "done"}, nil
}

// Codex Turns receive a LiveInbox too; the adapter decides whether to claim it
// from its own effective live-input switch.
func TestCodexTurnReceivesLiveInbox(t *testing.T) {
	executor := &inboxProbeExecutor{hadInbox: make(chan bool, 4)}
	service, _, task := newLiveInboxTask(t, "codex", executor)
	if _, err := service.SubmitMessage(context.Background(), task.ID, "work"); err != nil {
		t.Fatal(err)
	}
	select {
	case had := <-executor.hadInbox:
		if !had {
			t.Fatal("Codex Turn did not receive a LiveInbox")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Turn did not start")
	}
}

// gatedExecutor keeps a Turn running until the test releases it, so the test
// decides exactly when the live inbox settles a mid-turn message relative to
// the follow-up scheduler's pending snapshot.
type gatedExecutor struct {
	started     chan struct{}
	release     chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
}

func (executor *gatedExecutor) Execute(ctx context.Context, _ ExecutionRequest, _ func(ExecutionEvent)) (ExecutionResult, error) {
	executor.startOnce.Do(func() { close(executor.started) })
	select {
	case <-executor.release:
	case <-ctx.Done():
		return ExecutionResult{}, ctx.Err()
	}
	return ExecutionResult{Reply: "first", ProviderSessionID: "claude-live"}, nil
}

func (executor *gatedExecutor) releaseTurn() {
	executor.releaseOnce.Do(func() { close(executor.release) })
}

// startGatedLiveInboxTurn starts one running Claude Turn whose live inbox the
// test will drive, and returns the Turn the scheduler must not duplicate.
func startGatedLiveInboxTurn(t *testing.T) (*Service, *store.Store, domain.Task, *gatedExecutor, domain.Turn) {
	t.Helper()
	ctx := context.Background()
	executor := &gatedExecutor{started: make(chan struct{}), release: make(chan struct{})}
	service, database, task := newLiveInboxTask(t, "claude", executor)
	t.Cleanup(executor.releaseTurn)
	if _, err := service.SubmitMessage(ctx, task.ID, "work"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Claude Turn did not start")
	}
	running, err := database.ActiveTurnForAgent(ctx, task.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	return service, database, task, executor, running
}

// TestSubmitMessageAcceptsLiveInboxDeliveryOfSnapshotItem reproduces the
// Submit/Claim TOCTOU: scheduleAgent reads a pending snapshot, the running
// Turn's live inbox claims, acknowledges and processes that message, and the
// Turn ends before the scheduler reaches its claim. The submit must succeed,
// because the message is already delivered, and it must not start a second
// Turn that delivers the same text again.
func TestSubmitMessageAcceptsLiveInboxDeliveryOfSnapshotItem(t *testing.T) {
	ctx := context.Background()
	service, database, task, executor, running := startGatedLiveInboxTurn(t)
	service.scheduleSnapshotHook = func() {
		service.scheduleSnapshotHook = nil
		pending, err := database.PendingAgentInbox(ctx, task.ID, "main")
		if err != nil || len(pending) != 1 {
			t.Errorf("schedule snapshot pending=%#v err=%v", pending, err)
			executor.releaseTurn()
			return
		}
		batch, err := service.claimLiveInbox(ctx, task, running, 20)
		if err != nil || len(batch.Messages) != 1 {
			t.Errorf("live claim messages=%#v err=%v", batch.Messages, err)
			executor.releaseTurn()
			return
		}
		if _, err := service.ackLiveInbox(ctx, task, running, []string{batch.Messages[0].ID}, true); err != nil {
			t.Errorf("live ack: %v", err)
			executor.releaseTurn()
			return
		}
		if err := database.MarkLiveInboxProcessedForTurn(ctx, running.ID, time.Now().UTC()); err != nil {
			t.Errorf("mark live inbox processed: %v", err)
		}
		executor.releaseTurn()
		waitForTurn(t, database, running.ID, domain.TurnSucceeded)
	}
	submitted, err := service.SubmitMessage(ctx, task.ID, "mid-turn correction")
	if err != nil {
		t.Fatalf("SubmitMessage rejected a message the live inbox already delivered: %v", err)
	}
	if submitted.ID != "" {
		t.Fatalf("live-delivered message started a duplicate Turn %s", submitted.ID)
	}
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	turns, err := database.ListTurns(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns=%d, want only the original Turn", len(turns))
	}
	if pending, err := database.PendingInboxCount(ctx, task.ID); err != nil || pending != 0 {
		t.Fatalf("pending=%d err=%v, want 0", pending, err)
	}
}

// TestSubmitMessageStillRejectsNonLiveInboxConflict proves the fix does not
// swallow real conflicts: when the pending item is consumed by a non-live
// path, SubmitMessage keeps reporting the concurrent change.
func TestSubmitMessageStillRejectsNonLiveInboxConflict(t *testing.T) {
	ctx := context.Background()
	service, database, task, executor, running := startGatedLiveInboxTurn(t)
	service.scheduleSnapshotHook = func() {
		service.scheduleSnapshotHook = nil
		if err := database.CancelPendingInboxForRound(ctx, running.RoundID, time.Now().UTC()); err != nil {
			t.Errorf("cancel pending inbox: %v", err)
			executor.releaseTurn()
			return
		}
		executor.releaseTurn()
		waitForTurn(t, database, running.ID, domain.TurnSucceeded)
	}
	submitted, err := service.SubmitMessage(ctx, task.ID, "conflicted correction")
	if err == nil || !strings.Contains(err.Error(), "inbox item changed concurrently") {
		t.Fatalf("non-live conflict was swallowed: turn=%#v err=%v", submitted, err)
	}
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	turns, listErr := database.ListTurns(ctx, task.ID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(turns) != 1 {
		t.Fatalf("conflicted message started a Turn: turns=%d", len(turns))
	}
}

// TestLiveInboxScheduleAgentRendersOnlyClaimedSnapshotItems covers the
// production render closure with a mixed stale snapshot: one live-deliverable
// owner message and one non-live pending assignment. Once the live inbox
// steers the owner message, the follow-up Turn must contain only the
// assignment and report inbox_count=1; repeating the owner text would deliver
// it twice.
func TestLiveInboxScheduleAgentRendersOnlyClaimedSnapshotItems(t *testing.T) {
	ctx := context.Background()
	service, database, task, executor, running := startGatedLiveInboxTurn(t)
	const assignmentText = "pending assignment text"
	if _, err := database.EnqueueAgentRoute(ctx, domain.AgentInboxItem{
		ID: domain.NewID("inbox"), TaskID: task.ID, RoundID: running.RoundID,
		TargetAgentID: "main", SourceAgentID: "main", SourceKind: "assignment",
		SourceTurnID: running.ID, Content: assignmentText,
		Payload: map[string]any{"parent_turn_id": running.ID},
		Status:  "pending", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	service.scheduleSnapshotHook = func() {
		service.scheduleSnapshotHook = nil
		pending, err := database.PendingAgentInbox(ctx, task.ID, "main")
		if err != nil || len(pending) != 2 {
			t.Errorf("schedule snapshot pending=%#v err=%v", pending, err)
			executor.releaseTurn()
			return
		}
		batch, err := service.claimLiveInbox(ctx, task, running, 20)
		if err != nil || len(batch.Messages) != 1 || !strings.Contains(batch.InjectionText, "mid-turn owner text") {
			t.Errorf("live claim messages=%#v err=%v", batch.Messages, err)
			executor.releaseTurn()
			return
		}
		if _, err := service.ackLiveInbox(ctx, task, running, []string{batch.Messages[0].ID}, true); err != nil {
			t.Errorf("live ack: %v", err)
			executor.releaseTurn()
			return
		}
		if err := database.MarkLiveInboxProcessedForTurn(ctx, running.ID, time.Now().UTC()); err != nil {
			t.Errorf("mark live inbox processed: %v", err)
		}
		executor.releaseTurn()
		waitForTurn(t, database, running.ID, domain.TurnSucceeded)
	}
	submitted, err := service.SubmitMessage(ctx, task.ID, "mid-turn owner text")
	if err != nil {
		t.Fatal(err)
	}
	if submitted.ID == "" {
		t.Fatal("pending assignment did not start a follow-up Turn")
	}
	if strings.Contains(submitted.Instruction, "mid-turn owner text") {
		t.Fatalf("follow-up Turn repeated live-delivered text: %q", submitted.Instruction)
	}
	if !strings.Contains(submitted.Instruction, assignmentText) {
		t.Fatalf("follow-up Turn lost the pending assignment: %q", submitted.Instruction)
	}
	events, err := database.EventsAfter(ctx, "task", task.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	queued := false
	for _, event := range events {
		if event.Type != "turn_queued" || fmt.Sprint(event.Data["turn_id"]) != submitted.ID {
			continue
		}
		queued = true
		if count := fmt.Sprint(event.Data["inbox_count"]); count != "1" {
			t.Fatalf("turn_queued inbox_count=%s, want 1", count)
		}
	}
	if !queued {
		t.Fatalf("no turn_queued event for the follow-up Turn %s", submitted.ID)
	}
}

// Backends without a live input surface keep their existing delivery paths.
func TestDSHTurnHasNoLiveInbox(t *testing.T) {
	executor := &inboxProbeExecutor{hadInbox: make(chan bool, 4)}
	service, _, task := newLiveInboxTask(t, "dsh", executor)
	if _, err := service.SubmitMessage(context.Background(), task.ID, "work"); err != nil {
		t.Fatal(err)
	}
	select {
	case had := <-executor.hadInbox:
		if had {
			t.Fatal("DSH Turn received a LiveInbox")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Turn did not start")
	}
}
