package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func activityTestService(t *testing.T) (*Service, *store.Store, time.Time) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	service := NewService(database, nil, &stubExecutor{})
	now := time.Date(2026, 11, 13, 10, 0, 30, 0, time.UTC)
	service.now = func() time.Time { return now }
	return service, database, now
}

func activityTurn(now time.Time) domain.Turn {
	return domain.Turn{
		ID: "turn-1", TaskID: "task-1", RoundID: "round-1", AgentID: "main",
		Attempt: 1, Status: domain.TurnRunning, QueuedAt: now.Add(-30 * time.Second),
		PreparedAt: now.Add(-20 * time.Second), ContextReadyAt: now.Add(-15 * time.Second),
		SessionReadyAt: now.Add(-12 * time.Second), StartedAt: now.Add(-10 * time.Second),
		FirstEventAt: now.Add(-8 * time.Second), LastActivityAt: now.Add(-3 * time.Second),
	}
}

func TestExecutionEventsCarryTheSameActivityAsTaskDetail(t *testing.T) {
	service, database, now := activityTestService(t)
	ctx := context.Background()
	turn := activityTurn(now)

	service.noteExecutionActivity(turn, ExecutionEvent{
		Type: "agent_command_started",
		Data: map[string]any{
			"tool_use_id": "tool-1",
			"tool_name":   "Read /home/owner/.ssh/id_ed25519",
			"command":     "cat /home/owner/.ssh/id_ed25519",
		},
	})
	service.emitTurn(ctx, turn, "agent_command_started", map[string]any{
		"tool_use_id": "tool-1",
		"tool_name":   "Read /home/owner/.ssh/id_ed25519",
		"command":     "cat /home/owner/.ssh/id_ed25519",
	})

	events, err := database.EventsAfter(ctx, "task", turn.TaskID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	activity := &domain.TaskActivity{}
	raw, err := json.Marshal(events[0].Data["activity"])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, activity); err != nil {
		t.Fatal(err)
	}
	detail := service.TaskActivity(domain.TaskRound{ID: turn.RoundID, Sequence: 1}, []domain.Turn{turn})
	if detail == nil {
		t.Fatal("Task detail activity is nil")
	}
	if *detail != *activity {
		t.Fatalf("detail activity %+v differs from SSE activity %+v", *detail, *activity)
	}
	eventJSON, _ := json.Marshal(activity)
	if activity.Phase != domain.ActivityToolPhase || activity.ToolName != "Read" {
		t.Fatalf("activity = %+v, want tool/Read", activity)
	}
	if strings.Contains(string(eventJSON), "id_ed25519") || strings.Contains(string(eventJSON), "cat ") {
		t.Fatalf("activity leaked tool arguments or command: %s", eventJSON)
	}

	service.noteExecutionActivity(turn, ExecutionEvent{
		Type: "agent_command_finished",
		Data: map[string]any{"tool_use_id": "tool-1", "output_tail": "SECRET OUTPUT"},
	})
	after := service.activitySnapshot(turn)
	if after.Phase != domain.ActivityThinking || after.ToolName != "" {
		t.Fatalf("after tool finish activity = %+v, want thinking without tool name", after)
	}

	// Codex reports only a command, never a tool name. The activity must expose
	// the tool phase without copying any part of that command.
	service.noteExecutionActivity(turn, ExecutionEvent{
		Type: "agent_command_started",
		Data: map[string]any{"tool_call_id": "tool-2", "command": "cat SECRET-TOKEN"},
	})
	codex := service.activitySnapshot(turn)
	if codex.Phase != domain.ActivityToolPhase || codex.ToolName != "" {
		t.Fatalf("command-only tool activity = %+v, want tool without a name", codex)
	}
	codexJSON, _ := json.Marshal(codex)
	if strings.Contains(string(codexJSON), "SECRET-TOKEN") || strings.Contains(string(codexJSON), "cat ") {
		t.Fatalf("command-only activity leaked the command: %s", codexJSON)
	}
}

func TestTaskActivityTracksStallRetryAndTerminalPhases(t *testing.T) {
	service, _, now := activityTestService(t)
	turn := activityTurn(now)
	round := domain.TaskRound{ID: turn.RoundID, Sequence: 1}

	turn.StalledAt = now.Add(-5 * time.Second)
	if activity := service.TaskActivity(round, []domain.Turn{turn}); activity == nil || activity.Phase != domain.ActivityWaitingBackend {
		t.Fatalf("stalled activity = %+v", activity)
	}

	retry := activityTurn(now)
	retry.Attempt = 2
	retry.Status = domain.TurnQueued
	retry.FirstEventAt = time.Time{}
	if activity := service.TaskActivity(round, []domain.Turn{retry}); activity == nil || activity.Phase != domain.ActivityRetrying || activity.Attempt != 2 {
		t.Fatalf("retry activity = %+v", activity)
	}

	done := activityTurn(now)
	done.Status = domain.TurnSucceeded
	done.FinishedAt = now.Add(-time.Second)
	if activity := service.TaskActivity(round, []domain.Turn{done}); activity == nil || activity.Phase != domain.ActivityDone {
		t.Fatalf("done activity = %+v", activity)
	}

	failed := activityTurn(now)
	failed.ID = "turn-2"
	failed.Status = domain.TurnFailed
	failed.FinishedAt = now.Add(-2 * time.Second)
	if activity := service.TaskActivity(round, []domain.Turn{done, failed}); activity == nil || activity.Phase != domain.ActivityDone || activity.TurnID != done.ID {
		t.Fatalf("finished round activity = %+v, want newest finished turn", activity)
	}
}

func TestTaskActivityPrefersAnUnfinishedMainTurn(t *testing.T) {
	service, _, now := activityTestService(t)
	round := domain.TaskRound{ID: "round-1", Sequence: 1}
	doneAgent := activityTurn(now)
	doneAgent.ID, doneAgent.AgentID = "turn-agent", "reviewer"
	doneAgent.Status = domain.TurnSucceeded
	doneAgent.FinishedAt = now.Add(-time.Second)
	main := activityTurn(now)
	main.ID = "turn-main"
	if activity := service.TaskActivity(round, []domain.Turn{doneAgent, main}); activity == nil || activity.TurnID != "turn-main" {
		t.Fatalf("activity = %+v, want the running Main turn", activity)
	}
}
