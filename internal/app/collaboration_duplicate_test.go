package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

type collaborationDuplicateFixture struct {
	database *store.Store
	service  *Service
	task     domain.Task
	round    domain.TaskRound
	turn     domain.Turn
}

func newCollaborationDuplicateFixture(t *testing.T) *collaborationDuplicateFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(database, secretStore, &stubExecutor{})
	service.collaborationDelay = time.Hour
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })

	now := time.Now().UTC()
	project := domain.Project{ID: "project-duplicate", Name: "Duplicate", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-duplicate", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-duplicate", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-duplicate", DisplayName: "Stub", ProviderID: "stub", Backend: "stub", WireModel: "stub",
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
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "duplicate assignment",
		Request: "delegate work", ModelID: model.ID, CollaborationMode: "auto", MaxAgents: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &collaborationDuplicateFixture{database: database, service: service, task: task}
	fixture.startMainTurn(t)
	return fixture
}

func (f *collaborationDuplicateFixture) startMainTurn(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: f.task.ID, Role: "user", Sender: "owner",
		Content: "assign work", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: f.task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: f.task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: domain.TurnRunning,
		RuntimeConfigSnapshotID: f.task.RuntimeConfigSnapshotID,
		InboxBatchID:            "inbox-batch-" + message.ID, Attempt: 1, Required: true, QueuedAt: now,
	}
	createdRound, createdTurn, err := f.database.CreateMessageRoundAndTurn(ctx, message, round, turn)
	if err != nil {
		t.Fatal(err)
	}
	f.round, f.turn = createdRound, createdTurn
}

func (f *collaborationDuplicateFixture) finishMainTurn(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	f.turn.Status = domain.TurnSucceeded
	f.turn.FinishedAt = now
	if err := f.database.UpdateTurn(ctx, f.turn, domain.TurnRunning); err != nil {
		t.Fatal(err)
	}
	f.round.Status = domain.RoundCompleted
	f.round.FinishedAt = now
	if err := f.database.UpdateRound(ctx, f.round, domain.RoundRunning); err != nil {
		t.Fatal(err)
	}
}

func (f *collaborationDuplicateFixture) submit(actions ...AgentAction) (string, int, error) {
	claims := agentapi.Claims{TaskID: f.task.ID, AgentID: "main", TurnID: f.turn.ID}
	return f.service.SubmitAgentCollaboration(context.Background(), claims, actions, "integrate")
}

func requireAssignmentRejection(t *testing.T, err error) *CollaborationAssignmentError {
	t.Helper()
	if err == nil {
		t.Fatal("duplicate assignment was silently accepted")
	}
	var rejected *CollaborationAssignmentError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %T %v, want *CollaborationAssignmentError", err, err)
	}
	if !strings.Contains(err.Error(), CollaborationAssignmentDuplicate) {
		t.Fatalf("error text does not carry the stable code: %q", err.Error())
	}
	return rejected
}

func countAgentTurns(t *testing.T, database *store.Store, roundID, agentID string) int {
	t.Helper()
	turns, err := database.TurnsForRound(context.Background(), roundID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, turn := range turns {
		if turn.AgentID == agentID {
			count++
		}
	}
	return count
}

// Regression: before the fix the second submission in the same Main Turn hit
// the partial unique index, INSERT OR IGNORE reported no row, and the dispatcher
// returned an empty batch with a nil error. The caller could not tell that
// nothing was queued.
func TestDuplicateAssignmentInSameMainTurnIsRejected(t *testing.T) {
	f := newCollaborationDuplicateFixture(t)
	batchID, created, err := f.submit(AgentAction{
		AgentID: "sub-001", Title: "First", Assignment: "implement the first task", Required: true,
	})
	if err != nil || created != 1 || batchID == "" {
		t.Fatalf("first assignment: batch=%q created=%d err=%v", batchID, created, err)
	}

	duplicateBatch, duplicateCreated, duplicateErr := f.submit(AgentAction{
		AgentID: "sub-001", Title: "Again", Assignment: "implement it again", Required: true,
	})
	rejected := requireAssignmentRejection(t, duplicateErr)
	if duplicateBatch != "" || duplicateCreated != 0 {
		t.Fatalf("duplicate assignment reported success: batch=%q created=%d", duplicateBatch, duplicateCreated)
	}
	if rejected.BatchID != "" || rejected.Created != 0 || len(rejected.Accepted) != 0 {
		t.Fatalf("duplicate-only batch reported accepted work: %#v", rejected)
	}
	if len(rejected.Rejected) != 1 {
		t.Fatalf("rejected agents = %#v, want one", rejected.Rejected)
	}
	item := rejected.Rejected[0]
	if item.AgentID != "sub-001" || item.Code != CollaborationAssignmentDuplicate ||
		item.Reason != CollaborationAssignmentDuplicateInTurn || !strings.Contains(item.Message, "sub-001") {
		t.Fatalf("rejection detail = %#v", item)
	}
	waitForRoundAgentTurn(t, f.database, f.round.ID, "sub-001", domain.TurnSucceeded)
	if turns := countAgentTurns(t, f.database, f.round.ID, "sub-001"); turns != 1 {
		t.Fatalf("rejected duplicate still produced %d sub-001 Turns", turns)
	}
}

func TestDuplicateAgentInSameBatchLeavesAcceptedActionsQueued(t *testing.T) {
	f := newCollaborationDuplicateFixture(t)
	batchID, created, err := f.submit(
		AgentAction{AgentID: "sub-001", Title: "First", Assignment: "first assignment", Required: true},
		AgentAction{AgentID: "sub-001", Title: "Duplicate", Assignment: "second assignment", Required: true},
		AgentAction{AgentID: "sub-002", Title: "Other", Assignment: "other assignment", Required: true},
	)
	rejected := requireAssignmentRejection(t, err)
	if batchID == "" || created != 2 || rejected.BatchID != batchID || rejected.Created != 2 {
		t.Fatalf("partial batch: batch=%q created=%d rejection=%#v", batchID, created, rejected)
	}
	if len(rejected.Accepted) != 2 || len(rejected.Rejected) != 1 {
		t.Fatalf("partial batch result = %#v", rejected)
	}
	item := rejected.Rejected[0]
	if item.AgentID != "sub-001" || item.Reason != CollaborationAssignmentDuplicateInBatch {
		t.Fatalf("within-batch rejection = %#v", item)
	}
	waitForRoundAgentTurn(t, f.database, f.round.ID, "sub-001", domain.TurnSucceeded)
	waitForRoundAgentTurn(t, f.database, f.round.ID, "sub-002", domain.TurnSucceeded)
	if turns := countAgentTurns(t, f.database, f.round.ID, "sub-001"); turns != 1 {
		t.Fatalf("within-batch duplicate still produced %d sub-001 Turns", turns)
	}
}

// A repeated agent_id in one batch is an input error even when the first
// action never reaches the queue. Previously the within-batch verdict was
// derived from a successful enqueue, so a configuration failure on the first
// action let the second one dispatch and the batch looked successful.
func TestDuplicateInBatchRejectedWhenFirstActionFailsConfiguration(t *testing.T) {
	f := newCollaborationDuplicateFixture(t)
	batchID, created, err := f.submit(
		AgentAction{
			AgentID: "sub-001", Title: "Broken", Assignment: "first assignment",
			ModelID: "model-missing", Required: true,
		},
		AgentAction{AgentID: "sub-001", Title: "Valid", Assignment: "second assignment", Required: true},
	)
	rejected := requireAssignmentRejection(t, err)
	if batchID != "" || created != 0 || rejected.BatchID != "" || rejected.Created != 0 {
		t.Fatalf("batch reported dispatched work: batch=%q created=%d rejection=%#v", batchID, created, rejected)
	}
	if len(rejected.Rejected) != 1 {
		t.Fatalf("rejected agents = %#v, want one", rejected.Rejected)
	}
	item := rejected.Rejected[0]
	if item.AgentID != "sub-001" || item.Code != CollaborationAssignmentDuplicate ||
		item.Reason != CollaborationAssignmentDuplicateInBatch {
		t.Fatalf("within-batch rejection = %#v", item)
	}
	if turns := countAgentTurns(t, f.database, f.round.ID, "sub-001"); turns != 0 {
		t.Fatalf("rejected duplicate still produced %d sub-001 Turns", turns)
	}
}

// The pre-check is only an optimization: two dispatches can both pass it and
// then race the INSERT. The partial unique index must decide, and the loser
// must surface the same explicit rejection instead of an empty success.
func TestConcurrentDispatchPastPrecheckIsRejectedByUniqueIndex(t *testing.T) {
	f := newCollaborationDuplicateFixture(t)
	var reached int32
	bothReached := make(chan struct{})
	release := make(chan struct{})
	f.service.assignmentInsertHook = func() {
		if atomic.AddInt32(&reached, 1) == 2 {
			close(bothReached)
		}
		<-release
	}
	type dispatchResult struct {
		batchID string
		created int
		err     error
	}
	results := make(chan dispatchResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			batchID, created, err := f.submit(AgentAction{
				AgentID: "sub-001", Title: "Race", Assignment: "race assignment", Required: true,
			})
			results <- dispatchResult{batchID: batchID, created: created, err: err}
		}()
	}
	select {
	case <-bothReached:
	case <-time.After(10 * time.Second):
		t.Fatal("both dispatches did not reach the insert hook")
	}
	close(release)

	successes, rejections := 0, 0
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err == nil {
			successes++
			if result.batchID == "" || result.created != 1 {
				t.Fatalf("winning dispatch: batch=%q created=%d", result.batchID, result.created)
			}
			continue
		}
		rejected := requireAssignmentRejection(t, result.err)
		if len(rejected.Rejected) != 1 || rejected.Rejected[0].AgentID != "sub-001" ||
			rejected.Rejected[0].Reason != CollaborationAssignmentDuplicateInTurn {
			t.Fatalf("losing dispatch rejection = %#v", rejected)
		}
		rejections++
	}
	if successes != 1 || rejections != 1 {
		t.Fatalf("race outcome: %d success(es), %d rejection(s)", successes, rejections)
	}
	waitForRoundAgentTurn(t, f.database, f.round.ID, "sub-001", domain.TurnSucceeded)
	if turns := countAgentTurns(t, f.database, f.round.ID, "sub-001"); turns != 1 {
		t.Fatalf("race produced %d sub-001 Turns, want 1", turns)
	}
}

// The unique index is scoped to the source Turn: a later Main Turn must still
// be able to assign the same sub-agent (task-627's second review round).
func TestAssignmentToSameAgentInNewMainTurnStillSucceeds(t *testing.T) {
	f := newCollaborationDuplicateFixture(t)
	if _, created, err := f.submit(AgentAction{
		AgentID: "sub-001", Title: "First", Assignment: "first round", Required: true,
	}); err != nil || created != 1 {
		t.Fatalf("first round: created=%d err=%v", created, err)
	}
	firstRound := f.round.ID
	f.finishMainTurn(t)
	f.startMainTurn(t)

	batchID, created, err := f.submit(AgentAction{
		AgentID: "sub-001", Title: "Second", Assignment: "second round", Required: true,
	})
	if err != nil || created != 1 || batchID == "" {
		t.Fatalf("new Turn assignment: batch=%q created=%d err=%v", batchID, created, err)
	}
	waitForRoundAgentTurn(t, f.database, firstRound, "sub-001", domain.TurnSucceeded)
	waitForRoundAgentTurn(t, f.database, f.round.ID, "sub-001", domain.TurnSucceeded)
	if turns := countAgentTurns(t, f.database, firstRound, "sub-001"); turns != 1 {
		t.Fatalf("first round sub-001 Turns = %d", turns)
	}
	if turns := countAgentTurns(t, f.database, f.round.ID, "sub-001"); turns != 1 {
		t.Fatalf("second round sub-001 Turns = %d", turns)
	}
}
