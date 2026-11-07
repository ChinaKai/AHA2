package app

import (
	"context"
	"path/filepath"
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
	started  chan struct{}
	claimed  chan backend.LiveBatch
	deliver  bool
	hadInbox chan bool
}

func (executor *streamInputExecutor) Execute(ctx context.Context, request ExecutionRequest, _ func(ExecutionEvent)) (ExecutionResult, error) {
	executor.hadInbox <- request.LiveInbox != nil
	if request.LiveInbox == nil || request.Turn.Generation > 1 || request.Turn.Sequence > 1 {
		return ExecutionResult{Reply: "follow-up", ProviderSessionID: "claude-live"}, nil
	}
	close(executor.started)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		batch, err := request.LiveInbox.Claim(ctx)
		if err != nil {
			return ExecutionResult{}, err
		}
		if len(batch.IDs) > 0 {
			// A second claim in the same Turn must not hand the batch out again.
			again, err := request.LiveInbox.Claim(ctx)
			if err != nil || len(again.IDs) != 0 {
				return ExecutionResult{}, err
			}
			if err := request.LiveInbox.Ack(ctx, batch, executor.deliver); err != nil {
				return ExecutionResult{}, err
			}
			executor.claimed <- batch
			return ExecutionResult{Reply: "first", ProviderSessionID: "claude-live"}, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ExecutionResult{Reply: "no live message"}, nil
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
	executor := &streamInputExecutor{
		started: make(chan struct{}), claimed: make(chan backend.LiveBatch, 1), deliver: deliver,
		hadInbox: make(chan bool, 4),
	}
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
	if _, err := service.SubmitMessage(ctx, task.ID, "mid-turn correction"); err != nil {
		t.Fatal(err)
	}
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

// Only Claude Turns receive a LiveInbox; other backends keep their existing
// delivery paths untouched.
func TestNonClaudeTurnHasNoLiveInbox(t *testing.T) {
	executor := &streamInputExecutor{
		started: make(chan struct{}), claimed: make(chan backend.LiveBatch, 1), hadInbox: make(chan bool, 4),
	}
	service, _, task := newLiveInboxTask(t, "codex", executor)
	if _, err := service.SubmitMessage(context.Background(), task.ID, "work"); err != nil {
		t.Fatal(err)
	}
	select {
	case had := <-executor.hadInbox:
		if had {
			t.Fatal("non-Claude Turn received a LiveInbox")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Turn did not start")
	}
}
