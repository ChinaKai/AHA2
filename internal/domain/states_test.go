package domain

import "testing"

func TestTaskTransitions(t *testing.T) {
	t.Parallel()
	valid := [][2]TaskStatus{
		{TaskDraft, TaskPreparing},
		{TaskPreparing, TaskActive},
		{TaskActive, TaskWaitingUser},
		{TaskWaitingUser, TaskActive},
		{TaskWaitingUser, TaskCompleted},
		{TaskBlocked, TaskActive},
	}
	for _, pair := range valid {
		if err := ValidateTaskTransition(pair[0], pair[1]); err != nil {
			t.Fatalf("expected %s -> %s to be valid: %v", pair[0], pair[1], err)
		}
	}
	if err := ValidateTaskTransition(TaskCompleted, TaskActive); err == nil {
		t.Fatal("terminal task transition unexpectedly accepted")
	}
}

func TestTurnTransitions(t *testing.T) {
	t.Parallel()
	valid := [][2]TurnStatus{
		{TurnQueued, TurnPreparing},
		{TurnPreparing, TurnStarting},
		{TurnStarting, TurnRunning},
		{TurnRunning, TurnWaiting},
		{TurnWaiting, TurnRunning},
		{TurnRunning, TurnSucceeded},
	}
	for _, pair := range valid {
		if err := ValidateTurnTransition(pair[0], pair[1]); err != nil {
			t.Fatalf("expected %s -> %s to be valid: %v", pair[0], pair[1], err)
		}
	}
	if err := ValidateTurnTransition(TurnSucceeded, TurnRunning); err == nil {
		t.Fatal("terminal turn transition unexpectedly accepted")
	}
}
