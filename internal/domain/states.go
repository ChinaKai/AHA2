package domain

import "fmt"

type TaskStatus string

const (
	TaskDraft       TaskStatus = "draft"
	TaskPreparing   TaskStatus = "preparing"
	TaskActive      TaskStatus = "active"
	TaskWaitingUser TaskStatus = "waiting_user"
	TaskBlocked     TaskStatus = "blocked"
	TaskCompleted   TaskStatus = "completed"
	TaskFailed      TaskStatus = "failed"
	TaskCancelled   TaskStatus = "cancelled"
)

var taskTransitions = map[TaskStatus]map[TaskStatus]bool{
	TaskDraft:       {TaskPreparing: true, TaskCancelled: true},
	TaskPreparing:   {TaskActive: true, TaskBlocked: true, TaskFailed: true, TaskCancelled: true},
	TaskActive:      {TaskWaitingUser: true, TaskBlocked: true, TaskCompleted: true, TaskFailed: true, TaskCancelled: true},
	TaskWaitingUser: {TaskActive: true, TaskCompleted: true, TaskCancelled: true},
	TaskBlocked:     {TaskActive: true, TaskWaitingUser: true, TaskFailed: true, TaskCancelled: true},
	TaskFailed:      {TaskActive: true, TaskWaitingUser: true, TaskCompleted: true, TaskCancelled: true},
	TaskCompleted:   {TaskWaitingUser: true},
	TaskCancelled:   {TaskWaitingUser: true},
}

func ValidateTaskTransition(from, to TaskStatus) error {
	if from == to {
		return nil
	}
	if taskTransitions[from][to] {
		return nil
	}
	return fmt.Errorf("invalid task transition %q -> %q", from, to)
}

func (status TaskStatus) Terminal() bool {
	return status == TaskCompleted || status == TaskFailed || status == TaskCancelled
}

type TurnStatus string

const (
	TurnQueued      TurnStatus = "queued"
	TurnPreparing   TurnStatus = "preparing"
	TurnStarting    TurnStatus = "starting"
	TurnRunning     TurnStatus = "running"
	TurnWaiting     TurnStatus = "waiting"
	TurnSucceeded   TurnStatus = "succeeded"
	TurnFailed      TurnStatus = "failed"
	TurnInterrupted TurnStatus = "interrupted"
	TurnBlocked     TurnStatus = "blocked"
)

var turnTransitions = map[TurnStatus]map[TurnStatus]bool{
	TurnQueued:    {TurnPreparing: true, TurnInterrupted: true, TurnFailed: true},
	TurnPreparing: {TurnStarting: true, TurnBlocked: true, TurnInterrupted: true, TurnFailed: true},
	TurnStarting:  {TurnRunning: true, TurnBlocked: true, TurnInterrupted: true, TurnFailed: true},
	TurnRunning:   {TurnWaiting: true, TurnSucceeded: true, TurnBlocked: true, TurnInterrupted: true, TurnFailed: true},
	TurnWaiting:   {TurnRunning: true, TurnSucceeded: true, TurnBlocked: true, TurnInterrupted: true, TurnFailed: true},
}

func ValidateTurnTransition(from, to TurnStatus) error {
	if from == to {
		return nil
	}
	if turnTransitions[from][to] {
		return nil
	}
	return fmt.Errorf("invalid turn transition %q -> %q", from, to)
}

func (status TurnStatus) Terminal() bool {
	return status == TurnSucceeded || status == TurnFailed || status == TurnInterrupted || status == TurnBlocked
}

type KnowledgeStatus string

const (
	KnowledgeObserved   KnowledgeStatus = "observed"
	KnowledgeCandidate  KnowledgeStatus = "candidate"
	KnowledgeVerified   KnowledgeStatus = "verified"
	KnowledgeStale      KnowledgeStatus = "stale"
	KnowledgeDeprecated KnowledgeStatus = "deprecated"
)
