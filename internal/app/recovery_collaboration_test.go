package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

// A claimed assignment survives a restart as a pending Inbox item while the
// collaboration member still points at the interrupted child Turn. Startup must
// reclaim it instead of trying to create a second Turn and failing.
func TestResumePendingReclaimsInterruptedCollaborationInbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, service, task, _, batchID, inboxID, turnsBefore := newCollaborationRecoveryFixture(
		t, true, domain.TurnInterrupted,
	)
	if err := service.ResumePending(ctx); err != nil {
		t.Fatalf("ResumePending: %v", err)
	}
	assertCollaborationInboxClosed(t, database, task.ID, "sub-001", inboxID)
	members, err := database.CollaborationMembers(ctx, batchID)
	if err != nil || len(members) != 1 {
		t.Fatalf("members=%#v err=%v", members, err)
	}
	if members[0].Status != domain.CollaborationMemberInterrupted || members[0].ChildTurnID == "" {
		t.Fatalf("member=%#v", members[0])
	}
	batch, err := database.CollaborationBatch(ctx, batchID)
	if err != nil || batch.Status != domain.CollaborationBatchComplete {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	count, err := database.TaskTurnCount(ctx, task.ID)
	if err != nil || count != turnsBefore {
		t.Fatalf("recovery created a turn: before=%d after=%d err=%v", turnsBefore, count, err)
	}
}

// If the linked child Turn is still active, the pending item is a duplicate of
// an assignment that is already running. It must not create a second Turn.
func TestResumePendingReusesActiveCollaborationInbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, service, task, round, batchID, inboxID, turnsBefore := newCollaborationRecoveryFixture(
		t, true, domain.TurnRunning,
	)
	if err := service.ResumePending(ctx); err != nil {
		t.Fatalf("ResumePending: %v", err)
	}
	assertCollaborationInboxClosed(t, database, task.ID, "sub-001", inboxID)
	members, err := database.CollaborationMembers(ctx, batchID)
	if err != nil || len(members) != 1 {
		t.Fatalf("members=%#v err=%v", members, err)
	}
	if members[0].Status != domain.CollaborationMemberRunning {
		t.Fatalf("member=%#v", members[0])
	}
	batch, err := database.CollaborationBatch(ctx, batchID)
	if err != nil || batch.Status != domain.CollaborationBatchPending {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	count, err := database.TaskTurnCount(ctx, task.ID)
	if err != nil || count != turnsBefore {
		t.Fatalf("active assignment created another turn: before=%d after=%d err=%v", turnsBefore, count, err)
	}
	turns, err := database.TurnsForRound(ctx, round.ID)
	if err != nil {
		t.Fatal(err)
	}
	subTurns := 0
	for _, turn := range turns {
		if turn.AgentID == "sub-001" {
			subTurns++
		}
	}
	if subTurns != 1 {
		t.Fatalf("sub-001 turns=%d", subTurns)
	}
}

// The normal path must be untouched: an unlinked member still gets its Turn.
func TestResumePendingCreatesTurnForUnlinkedCollaborationInbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, service, task, round, batchID, inboxID, turnsBefore := newCollaborationRecoveryFixture(
		t, false, domain.TurnQueued,
	)
	if err := service.ResumePending(ctx); err != nil {
		t.Fatalf("ResumePending: %v", err)
	}
	turn := waitForRoundAgentTurn(t, database, round.ID, "sub-001", domain.TurnSucceeded)
	assertCollaborationInboxClosed(t, database, task.ID, "sub-001", inboxID)
	members, err := database.CollaborationMembers(ctx, batchID)
	if err != nil || len(members) != 1 || members[0].ChildTurnID != turn.ID {
		t.Fatalf("members=%#v err=%v", members, err)
	}
	count, err := database.TaskTurnCount(ctx, task.ID)
	if err != nil || count != turnsBefore+1 {
		t.Fatalf("normal assignment did not create exactly one turn: before=%d after=%d err=%v", turnsBefore, count, err)
	}
}

// A restart interrupts main and its child together. The resumed main Turn is a
// new Turn, not the old batch's parent; finalize must still find and consume the
// old batch, otherwise an active resumed main Turn suppresses fallback and the
// Round stays running forever.
func TestResumePendingFinalizesInterruptedRoundCollaboration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state := newCollaborationRecoveryState(t, collaborationRecoveryInput{
		MainStatus: domain.TurnInterrupted, ChildStatus: domain.TurnInterrupted, Linked: true,
		AssignmentInbox: true, MainInboxPending: true,
		CollaborationDelay: 2 * time.Millisecond,
	})
	if err := state.service.ResumePending(ctx); err != nil {
		t.Fatalf("ResumePending: %v", err)
	}
	batch, round, task := waitForCollaborationSettled(t, state)
	if batch.Status != domain.CollaborationBatchConsumed {
		t.Fatalf("batch=%#v", batch)
	}
	if round.Status != domain.RoundCompleted {
		t.Fatalf("round=%#v", round)
	}
	if task.Status != domain.TaskWaitingUser {
		t.Fatalf("task=%#v", task)
	}
}

// The main Turn may already have ended while its child was interrupted. Startup
// must still wake main with the recovered result and settle the Round.
func TestResumePendingFinalizesAfterMainTurnEnded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state := newCollaborationRecoveryState(t, collaborationRecoveryInput{
		MainStatus: domain.TurnSucceeded, ChildStatus: domain.TurnInterrupted, Linked: true,
		AssignmentInbox:    true,
		CollaborationDelay: 2 * time.Millisecond,
	})
	if err := state.service.ResumePending(ctx); err != nil {
		t.Fatalf("ResumePending: %v", err)
	}
	batch, round, task := waitForCollaborationSettled(t, state)
	if batch.Status != domain.CollaborationBatchConsumed || round.Status != domain.RoundCompleted ||
		task.Status != domain.TaskWaitingUser {
		t.Fatalf("batch=%#v round=%#v task=%#v", batch, round, task)
	}
}

// Historical or crash-window data can have a terminal child Turn and a complete
// batch without the collaboration event that routeAgentOutcome would have
// written. Startup must backfill both the event and the Web conversation item,
// and repeating the recovery must not duplicate either.
func TestResumePendingBackfillsMissingCollaborationResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state := newCollaborationRecoveryState(t, collaborationRecoveryInput{
		MainStatus: domain.TurnInterrupted, ChildStatus: domain.TurnInterrupted, Linked: true,
		BatchStatus:        domain.CollaborationBatchComplete,
		CollaborationDelay: time.Hour,
	})
	if err := state.service.ResumePending(ctx); err != nil {
		t.Fatalf("ResumePending: %v", err)
	}
	assertBackfilledCollaborationResult(t, state, 1)
	batch, err := state.database.CollaborationBatch(ctx, state.batchID)
	if err != nil || batch.Status != domain.CollaborationBatchComplete {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}

	if err := state.service.ResumePending(ctx); err != nil {
		t.Fatalf("second ResumePending: %v", err)
	}
	assertBackfilledCollaborationResult(t, state, 1)
	batch, err = state.database.CollaborationBatch(ctx, state.batchID)
	if err != nil || batch.Status != domain.CollaborationBatchComplete {
		t.Fatalf("repeat recovery changed batch=%#v err=%v", batch, err)
	}
}

func assertBackfilledCollaborationResult(t *testing.T, state *collaborationRecoveryState, want int) {
	t.Helper()
	ctx := context.Background()
	progress, err := state.database.CollaborationProgress(ctx, state.batchID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(progress.Results) != want || progress.Results[0].ChildTurnID != state.child.ID ||
		progress.Results[0].Status != string(domain.TurnInterrupted) {
		t.Fatalf("progress=%#v", progress)
	}
	page, err := state.database.ConversationPageForAgent(ctx, state.task.ID, "main", 0, 0, 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	conversationID := domain.CollaborationResultConversationID(state.child.ID)
	found := 0
	for _, item := range page.Items {
		if item.ID != conversationID {
			continue
		}
		found++
		if item.Kind != "collaboration_error_routed" || item.Category != "error" {
			t.Fatalf("conversation item=%#v", item)
		}
	}
	if found != want {
		t.Fatalf("collaboration conversation items=%d want %d", found, want)
	}
}

func waitForCollaborationSettled(
	t *testing.T,
	state *collaborationRecoveryState,
) (domain.CollaborationBatch, domain.TaskRound, domain.Task) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(15 * time.Second)
	for {
		batch, err := state.database.CollaborationBatch(ctx, state.batchID)
		if err != nil {
			t.Fatal(err)
		}
		round, err := state.database.Round(ctx, state.round.ID)
		if err != nil {
			t.Fatal(err)
		}
		task, err := state.database.Task(ctx, state.task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if batch.Status == domain.CollaborationBatchConsumed && round.Status.Terminal() {
			return batch, round, task
		}
		if time.Now().After(deadline) {
			t.Fatalf("collaboration did not settle: batch=%q round=%q task=%q", batch.Status, round.Status, task.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertCollaborationInboxClosed(t *testing.T, database *store.Store, taskID, agentID, inboxID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		pending, err := database.PendingAgentInbox(context.Background(), taskID, agentID)
		if err != nil {
			t.Fatal(err)
		}
		pendingItem := false
		for _, item := range pending {
			if item.ID == inboxID {
				pendingItem = true
			}
		}
		count, countErr := database.PendingInboxCount(context.Background(), taskID)
		if countErr != nil {
			t.Fatal(countErr)
		}
		if !pendingItem && count == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("inbox %s is still pending (task pending count=%d)", inboxID, count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type collaborationRecoveryInput struct {
	MainStatus         domain.TurnStatus
	ChildStatus        domain.TurnStatus
	Linked             bool
	BatchStatus        string
	AssignmentInbox    bool
	MainInboxPending   bool
	CollaborationDelay time.Duration
}

type collaborationRecoveryState struct {
	database          *store.Store
	service           *Service
	task              domain.Task
	round             domain.TaskRound
	parent            domain.Turn
	child             domain.Turn
	batchID           string
	assignmentInboxID string
	mainInboxID       string
	turnsBefore       int
}

// newCollaborationRecoveryFixture is the original narrow fixture: an
// interrupted main Turn, an optional linked child Turn, and a pending
// assignment Inbox.
func newCollaborationRecoveryFixture(
	t *testing.T,
	linked bool,
	childStatus domain.TurnStatus,
) (*store.Store, *Service, domain.Task, domain.TaskRound, string, string, int) {
	t.Helper()
	state := newCollaborationRecoveryState(t, collaborationRecoveryInput{
		MainStatus: domain.TurnInterrupted, ChildStatus: childStatus, Linked: linked,
		AssignmentInbox: true, CollaborationDelay: time.Hour,
	})
	return state.database, state.service, state.task, state.round, state.batchID, state.assignmentInboxID, state.turnsBefore
}

func newCollaborationRecoveryState(t *testing.T, input collaborationRecoveryInput) *collaborationRecoveryState {
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
	delay := input.CollaborationDelay
	if delay <= 0 {
		delay = time.Hour
	}
	// Keep the collaboration fallback timer out of the test's assertion window
	// unless the test explicitly drives the fallback flow.
	service.collaborationDelay = delay
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })

	now := time.Now().UTC()
	project := domain.Project{ID: "project-collaboration-recovery", Name: "Recovery", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-collaboration-recovery", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	env := domain.EnvGroup{
		ID: "env-collaboration-recovery", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-collaboration-recovery", DisplayName: "Stub", ProviderID: "stub", Backend: "stub",
		WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID: "runtime-collaboration-recovery", WorkspaceID: workspace.ID, Backend: "stub",
		ModelID: model.ID, WireModel: "stub", EnvGroupID: env.ID, EnvGroupRevision: 1, CreatedAt: now,
	}
	task := domain.Task{
		ID: "task-collaboration-recovery", ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "Collaboration recovery", OriginalRequest: "recover", CurrentGoal: "recover",
		Status: domain.TaskActive, RuntimeConfigSnapshotID: snapshot.ID,
		CollaborationMode: "auto", MaxAgents: 2, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, env) },
		func() error { return database.UpsertModel(ctx, model) },
		func() error {
			_, err := database.CreateTaskWithSnapshot(ctx, snapshot, task)
			return err
		},
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: "recover the interrupted collaboration", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	parent := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: input.MainStatus,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, InboxBatchID: "inbox-batch-parent",
		Attempt: 1, Required: true, QueuedAt: now,
		Error: "AHA service restarted during this turn",
	}
	if input.MainStatus.Terminal() {
		parent.FinishedAt = now
	}
	createdRound, createdParent, err := database.CreateMessageRoundAndTurn(ctx, message, round, parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpsertTaskAgent(ctx, domain.TaskAgent{
		TaskID: task.ID, AgentID: "sub-001", Role: "sub", Status: "idle", Title: "Sub",
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	batchID := domain.NewID("collaboration_batch")
	if err := database.CreateCollaborationBatch(ctx, domain.CollaborationBatch{
		ID: batchID, TaskID: task.ID, RoundID: createdRound.ID, ParentTurnID: createdParent.ID,
		Status: domain.CollaborationBatchPending, CreatedAt: now, UpdatedAt: now,
	}, []domain.CollaborationMember{{
		BatchID: batchID, AgentID: "sub-001", Required: true,
		Status: domain.CollaborationMemberQueued, CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	var child domain.Turn
	if input.Linked {
		child = domain.Turn{
			ID: domain.NewID("turn"), TaskID: task.ID, RoundID: createdRound.ID, AgentID: "sub-001",
			Sequence: createdParent.Sequence + 1, ParentTurnID: createdParent.ID,
			InputMessageID: message.ID, Status: input.ChildStatus,
			RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, InboxBatchID: "inbox-batch-child",
			Attempt: 1, Generation: 1, Required: true, QueuedAt: now,
			Error: "AHA service restarted during this turn",
		}
		if input.ChildStatus.Terminal() {
			child.FinishedAt = now
		}
		if err := database.CreateTurn(ctx, child); err != nil {
			t.Fatal(err)
		}
		if err := database.LinkCollaborationMemberTurn(ctx, batchID, "sub-001", child.ID, now); err != nil {
			t.Fatal(err)
		}
	}
	switch input.BatchStatus {
	case domain.CollaborationBatchComplete:
		if err := database.MarkCollaborationDelivered(ctx, batchID, 0, domain.CollaborationBatchComplete, now); err != nil {
			t.Fatal(err)
		}
	case domain.CollaborationBatchFallback:
		if err := database.MarkCollaborationFallback(ctx, batchID, now); err != nil {
			t.Fatal(err)
		}
	case domain.CollaborationBatchConsumed:
		if err := database.MarkCollaborationConsumed(ctx, batchID, now); err != nil {
			t.Fatal(err)
		}
	}
	assignmentInboxID := ""
	if input.AssignmentInbox {
		assignmentInboxID = domain.NewID("inbox")
		if _, err := database.EnqueueAgentRoute(ctx, domain.AgentInboxItem{
			ID: assignmentInboxID, TaskID: task.ID, RoundID: createdRound.ID, TargetAgentID: "sub-001",
			SourceAgentID: "main", SourceKind: "assignment", SourceTurnID: createdParent.ID,
			Content: "implement the interrupted assignment",
			Payload: map[string]any{
				"collaboration_batch_id": batchID, "parent_turn_id": createdParent.ID, "required": true,
			},
			Status: "pending", CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mainInboxID := ""
	if input.MainInboxPending {
		mainInboxID = domain.NewID("inbox")
		if _, err := database.EnqueueAgentRoute(ctx, domain.AgentInboxItem{
			ID: mainInboxID, TaskID: task.ID, RoundID: createdRound.ID, TargetAgentID: "main",
			SourceAgentID: "owner", SourceKind: "owner_message",
			Content: "continue the interrupted work", Status: "pending", CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	turnsBefore, err := database.TaskTurnCount(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &collaborationRecoveryState{
		database: database, service: service, task: task, round: createdRound,
		parent: createdParent, child: child, batchID: batchID,
		assignmentInboxID: assignmentInboxID, mainInboxID: mainInboxID,
		turnsBefore: turnsBefore,
	}
}
