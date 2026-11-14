package domain

import (
	"strings"
	"testing"
	"time"
)

func TestDeriveActivityCoversTurnLifecycle(t *testing.T) {
	base := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	now := base.Add(30 * time.Second)
	queued := func() Turn {
		return Turn{
			ID: "turn-1", TaskID: "task-1", RoundID: "round-1", AgentID: "main",
			Attempt: 1, Status: TurnQueued, QueuedAt: base,
		}
	}
	toolState := &TurnActivityState{OpenTools: []ActivityToolCall{
		{ID: "tool-1", Name: "Read", Since: base.Add(20 * time.Second)},
	}}
	cases := []struct {
		name     string
		mutate   func(*Turn)
		state    *TurnActivityState
		phase    ActivityPhase
		toolName string
		since    time.Time
		elapsed  int64
	}{
		{
			name:   "queued",
			mutate: func(turn *Turn) {},
			phase:  ActivityQueued, since: base, elapsed: 30_000,
		},
		{
			name: "preparing",
			mutate: func(turn *Turn) {
				turn.Status = TurnPreparing
				turn.PreparedAt = base.Add(5 * time.Second)
			},
			phase: ActivityPreparing, since: base.Add(5 * time.Second), elapsed: 25_000,
		},
		{
			name: "session wake",
			mutate: func(turn *Turn) {
				turn.Status = TurnStarting
				turn.PreparedAt = base.Add(5 * time.Second)
				turn.ContextReadyAt = base.Add(10 * time.Second)
			},
			phase: ActivitySessionWake, since: base.Add(10 * time.Second), elapsed: 20_000,
		},
		{
			name: "thinking",
			mutate: func(turn *Turn) {
				turn.Status = TurnRunning
				turn.StartedAt = base.Add(10 * time.Second)
				turn.FirstEventAt = base.Add(12 * time.Second)
			},
			phase: ActivityThinking, since: base.Add(12 * time.Second), elapsed: 18_000,
		},
		{
			name: "tool",
			mutate: func(turn *Turn) {
				turn.Status = TurnRunning
				turn.StartedAt = base.Add(10 * time.Second)
				turn.FirstEventAt = base.Add(12 * time.Second)
			},
			state: toolState, phase: ActivityToolPhase, toolName: "Read",
			since: base.Add(20 * time.Second), elapsed: 10_000,
		},
		{
			name: "waiting backend",
			mutate: func(turn *Turn) {
				turn.Status = TurnRunning
				turn.StartedAt = base.Add(10 * time.Second)
				turn.FirstEventAt = base.Add(12 * time.Second)
				turn.StalledAt = base.Add(15 * time.Second)
			},
			phase: ActivityWaitingBackend, since: base.Add(15 * time.Second), elapsed: 15_000,
		},
		{
			name: "retrying",
			mutate: func(turn *Turn) {
				turn.Attempt = 2
				turn.QueuedAt = base.Add(8 * time.Second)
			},
			phase: ActivityRetrying, since: base.Add(8 * time.Second), elapsed: 22_000,
		},
		{
			name: "finalizing",
			mutate: func(turn *Turn) {
				turn.Status = TurnRunning
				turn.StartedAt = base.Add(10 * time.Second)
				turn.BackendFinishedAt = base.Add(25 * time.Second)
			},
			phase: ActivityFinalizing, since: base.Add(25 * time.Second), elapsed: 5_000,
		},
		{
			name: "done",
			mutate: func(turn *Turn) {
				turn.Status = TurnSucceeded
				turn.StartedAt = base.Add(10 * time.Second)
				turn.FinishedAt = base.Add(20 * time.Second)
			},
			phase: ActivityDone, since: base.Add(20 * time.Second), elapsed: 10_000,
		},
		{
			name: "failed",
			mutate: func(turn *Turn) {
				turn.Status = TurnFailed
				turn.StartedAt = base.Add(10 * time.Second)
				turn.FinishedAt = base.Add(18 * time.Second)
			},
			phase: ActivityFailed, since: base.Add(18 * time.Second), elapsed: 12_000,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			turn := queued()
			test.mutate(&turn)
			activity := DeriveActivity(turn, test.state, now)
			if activity.Phase != test.phase {
				t.Fatalf("phase = %q, want %q", activity.Phase, test.phase)
			}
			if activity.ToolName != test.toolName {
				t.Fatalf("tool_name = %q, want %q", activity.ToolName, test.toolName)
			}
			if !activity.Since.Equal(test.since) || activity.SinceMS != test.since.UnixMilli() {
				t.Fatalf("since = %s (%d), want %s", activity.Since, activity.SinceMS, test.since)
			}
			if activity.ElapsedMS != test.elapsed {
				t.Fatalf("elapsed_ms = %d, want %d", activity.ElapsedMS, test.elapsed)
			}
			if activity.Attempt != turn.Attempt || activity.TurnID != turn.ID || activity.RoundID != turn.RoundID || activity.AgentID != turn.AgentID {
				t.Fatalf("identity fields missing: %+v", activity)
			}
		})
	}
}

func TestDeriveActivityThinkingResetsAfterTool(t *testing.T) {
	base := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	turn := Turn{
		ID: "turn-1", RoundID: "round-1", AgentID: "main", Attempt: 1,
		Status: TurnRunning, QueuedAt: base, StartedAt: base.Add(time.Second),
		FirstEventAt: base.Add(2 * time.Second),
	}
	state := &TurnActivityState{ThinkingSince: base.Add(20 * time.Second)}
	activity := DeriveActivity(turn, state, base.Add(25*time.Second))
	if activity.Phase != ActivityThinking {
		t.Fatalf("phase = %q, want thinking", activity.Phase)
	}
	if activity.ElapsedMS != 5_000 {
		t.Fatalf("elapsed_ms = %d, want 5000", activity.ElapsedMS)
	}
}

func TestNormalizeToolNameDropsArgumentsAndControls(t *testing.T) {
	cases := map[string]string{
		"Read":                      "Read",
		"  Bash  ":                  "Bash",
		"Read /etc/passwd":          "Read",
		"Read(secrets.env)":         "Read",
		"Read\n-----BEGIN KEY-----": "Read",
		"tool\x00name":              "toolname",
		strings.Repeat("a", 70):     strings.Repeat("a", 64),
		"":                          "",
	}
	for input, want := range cases {
		if got := NormalizeToolName(input); got != want {
			t.Fatalf("NormalizeToolName(%q) = %q, want %q", input, got, want)
		}
	}
}
