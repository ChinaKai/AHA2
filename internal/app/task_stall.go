package app

import (
	"slices"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const (
	taskStuckSelf    = "self"
	taskStuckOwner   = "owner"
	taskStuckOther   = "other"
	taskStuckNone    = "none"
	taskStuckUnknown = "unknown"

	// The workbench design gives a finished Turn 15 minutes before surfacing
	// it for a nudge. Exactly 15 minutes is still inside that grace period.
	taskStallGrace = 15 * time.Minute
)

type taskLineWait struct {
	on   string
	who  []string
	self domain.TaskLine
}

func taskStuckOn(lines []domain.TaskLine) taskLineWait {
	if len(lines) == 0 {
		return taskLineWait{on: taskStuckUnknown}
	}
	wait := taskLineWait{on: taskStuckNone}
	for _, line := range lines {
		if line.State == domain.TaskLineClosed {
			continue
		}
		switch {
		case line.Owner == domain.TaskLineOwnerSelf && line.State == domain.TaskLineOpen:
			return taskLineWait{on: taskStuckSelf, self: line}
		case line.Owner == domain.TaskLineOwnerOwner:
			wait.on = taskStuckOwner
			wait.who = nil
		case line.Owner == domain.TaskLineOwnerOther && wait.on != taskStuckOwner:
			wait.on = taskStuckOther
			if who := strings.TrimSpace(line.Who); who != "" && !slices.Contains(wait.who, who) {
				wait.who = append(wait.who, who)
			}
		}
	}
	return wait
}

type PetDeskStalledTask struct {
	TaskID    string    `json:"task_id"`
	TaskCode  string    `json:"task_code"`
	Title     string    `json:"title"`
	LineID    string    `json:"line_id"`
	LineTitle string    `json:"line_title"`
	Detail    string    `json:"detail"`
	Since     time.Time `json:"since"`
}

func taskStall(task domain.Task, card store.TaskStateCard, wait taskLineWait, turn store.MainTurnBrief,
	now time.Time, ignored, awaitingApproval, underReview bool) (PetDeskStalledTask, bool) {
	if task.Status != domain.TaskWaitingUser || !domain.TurnStatus(turn.Status).EndedCleanly() ||
		turn.FinishedAt.IsZero() || now.Sub(turn.FinishedAt) <= taskStallGrace ||
		ignored || awaitingApproval || underReview {
		return PetDeskStalledTask{}, false
	}
	detail := wait.self.Step
	switch wait.on {
	case taskStuckSelf:
	case taskStuckUnknown:
		detail = petDeskFirst(card.NextSteps)
		if detail == "" || petDeskFirst(card.OpenQuestions) != "" {
			return PetDeskStalledTask{}, false
		}
	default:
		return PetDeskStalledTask{}, false
	}
	return PetDeskStalledTask{
		TaskID: task.ID, TaskCode: task.Code, Title: task.Title, LineID: wait.self.ID,
		LineTitle: wait.self.Title, Detail: detail, Since: turn.FinishedAt,
	}, true
}
