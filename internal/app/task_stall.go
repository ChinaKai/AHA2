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

	// Without an open line of its own, the only sign that a Task could go on
	// is a next step left on its state card, and every Task that was ever
	// parked with one has that. Past this window such a Task is dormant, not
	// stalled: listing it would have the pet nudge work nobody has touched in
	// weeks. A Task that names an open line of its own is listed however old.
	taskStallCardWindow = 7 * 24 * time.Hour
)

type taskLineWait struct {
	on   string
	who  []string
	self domain.TaskLine
	// unclosed counts the lines that are not closed. A card whose lines are
	// all closed says nothing about who the Task waits for now.
	unclosed int
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
		wait.unclosed++
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
	switch {
	case wait.on == taskStuckSelf:
	// A Task that closed every line and then stopped is the case this list
	// exists for: it reads like a Task that never recorded lines, so the state
	// card decides. A paused or blocked line of its own is a stated reason to
	// wait and never counts.
	case wait.on == taskStuckUnknown, wait.on == taskStuckNone && wait.unclosed == 0:
		detail = truncateAssistantText(petDeskFirst(card.NextSteps), petDeskDetailRunes)
		if detail == "" || len(petDeskQuestions(card.OpenQuestions)) > 0 || now.Sub(turn.FinishedAt) > taskStallCardWindow {
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
