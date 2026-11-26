package app

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const (
	lifecycleTimelineLimit = 200
	lifecycleTurnLimit     = 100
)

type TaskLifecycle struct {
	Task              lifecycleTask     `json:"task"`
	Now               lifecycleNow      `json:"now"`
	Lines             []lifecycleLine   `json:"lines"`
	Parallel          lifecycleParallel `json:"parallel"`
	Timeline          []lifecycleEvent  `json:"timeline"`
	TimelineTruncated bool              `json:"timeline_truncated,omitempty"`
	Turns             []lifecycleTurn   `json:"turns"`
	TurnsTruncated    bool              `json:"turns_truncated,omitempty"`
	Stats             lifecycleStats    `json:"stats"`
}

type lifecycleTask struct {
	ID          string            `json:"id"`
	Code        string            `json:"code"`
	Title       string            `json:"title"`
	Status      domain.TaskStatus `json:"status"`
	Project     string            `json:"project"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	CurrentGoal string            `json:"current_goal"`
}

type lifecycleNow struct {
	Status        string    `json:"status"`
	NextSteps     []string  `json:"next_steps"`
	OpenQuestions []string  `json:"open_questions"`
	UpdatedAt     time.Time `json:"updated_at"`
	StuckOn       string    `json:"stuck_on"`
	StuckWho      []string  `json:"stuck_who"`
	IdleSince     time.Time `json:"idle_since"`
	LinesRecorded bool      `json:"lines_recorded"`
}

type lifecycleLine struct {
	domain.TaskLine
	ClosedAt time.Time `json:"closed_at"`
}

type lifecycleParallel struct {
	Subs    []store.TaskLifecycleSub `json:"subs"`
	Reviews []lifecycleReview        `json:"reviews"`
}

type lifecycleReview struct {
	TaskID    string            `json:"task_id"`
	Code      string            `json:"code"`
	Status    domain.TaskStatus `json:"status"`
	Decision  string            `json:"decision"`
	Reason    string            `json:"reason"`
	DecidedAt time.Time         `json:"decided_at"`
}

type lifecycleEvent struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Title  string    `json:"title"`
	Detail string    `json:"detail"`
	RefID  string    `json:"ref_id"`
}

type lifecycleTurn struct {
	ID              string            `json:"id"`
	Sequence        int               `json:"sequence"`
	Status          domain.TurnStatus `json:"status"`
	StartedAt       time.Time         `json:"started_at"`
	FinishedAt      time.Time         `json:"finished_at"`
	DurationSeconds float64           `json:"duration_seconds"`
	Summary         string            `json:"summary"`
	Error           string            `json:"error"`
}

type lifecycleStats struct {
	Turns           int     `json:"turns"`
	DurationSeconds float64 `json:"duration_seconds"`
	Tokens          int64   `json:"tokens"`
}

func (s *Service) TaskLifecycle(ctx context.Context, task domain.Task) (TaskLifecycle, error) {
	project, err := s.store.Project(ctx, task.ProjectID)
	if err != nil {
		return TaskLifecycle{}, err
	}
	card, err := s.store.TaskLifecycleMemory(ctx, task.ID)
	if err != nil {
		return TaskLifecycle{}, err
	}
	// Tasks from older imports can have a goal history without a memory row.
	// TaskLifecycleMemory still returns that history.
	turns, err := s.store.TaskLifecycleMainTurns(ctx, task.ID, lifecycleTurnLimit)
	if err != nil {
		return TaskLifecycle{}, err
	}
	approvals, err := s.store.TaskLifecycleApprovals(ctx, task.ID)
	if err != nil {
		return TaskLifecycle{}, err
	}
	reviews, err := s.store.TaskLifecycleReviews(ctx, task.ID)
	if err != nil {
		return TaskLifecycle{}, err
	}
	subs, err := s.store.TaskLifecycleSubs(ctx, task.ID)
	if err != nil {
		return TaskLifecycle{}, err
	}
	view := TaskLifecycle{
		Task: lifecycleTask{
			ID: task.ID, Code: task.Code, Title: task.Title, Status: task.Status,
			Project: project.Name, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, CurrentGoal: task.CurrentGoal,
		},
		Now: lifecycleNow{
			Status: card.Status, NextSteps: append([]string{}, card.NextSteps...),
			OpenQuestions: append([]string{}, card.OpenQuestions...), UpdatedAt: card.StatusUpdatedAt,
			StuckWho: []string{}, LinesRecorded: len(card.Lines) > 0,
		},
		Lines:    make([]lifecycleLine, 0, len(card.Lines)),
		Parallel: lifecycleParallel{Subs: subs, Reviews: []lifecycleReview{}},
		Turns:    make([]lifecycleTurn, 0, min(len(turns), lifecycleTurnLimit)),
		Stats:    lifecycleStats{Turns: len(turns)},
	}
	for _, line := range card.Lines {
		item := lifecycleLine{TaskLine: line}
		if line.ClosedAt != nil {
			item.ClosedAt = *line.ClosedAt
		}
		view.Lines = append(view.Lines, item)
	}
	slices.SortStableFunc(view.Lines, func(a, b lifecycleLine) int {
		switch {
		case a.State == domain.TaskLineClosed && b.State != domain.TaskLineClosed:
			return 1
		case b.State == domain.TaskLineClosed && a.State != domain.TaskLineClosed:
			return -1
		default:
			return 0
		}
	})
	if task.Status == domain.TaskWaitingUser {
		wait := taskStuckOn(card.Lines)
		view.Now.StuckOn, view.Now.StuckWho = wait.on, append(view.Now.StuckWho, wait.who...)
		if len(turns) > 0 {
			if err := s.setLifecycleIdleSince(ctx, &view, task, card, wait, turns[0], approvals, reviews); err != nil {
				return TaskLifecycle{}, err
			}
		}
	}
	for index, turn := range turns {
		var duration float64
		if !turn.StartedAt.IsZero() && turn.FinishedAt.After(turn.StartedAt) {
			duration = turn.FinishedAt.Sub(turn.StartedAt).Seconds()
		}
		view.Stats.DurationSeconds += duration
		if index < lifecycleTurnLimit {
			line, _, _ := strings.Cut(turn.Result, "\n")
			view.Turns = append(view.Turns, lifecycleTurn{
				ID: turn.ID, Sequence: turn.Sequence, Status: turn.Status,
				StartedAt: turn.StartedAt, FinishedAt: turn.FinishedAt, DurationSeconds: duration,
				Summary: strings.TrimSpace(line), Error: strings.TrimSpace(turn.Error),
			})
		}
	}
	view.TurnsTruncated = len(turns) > lifecycleTurnLimit
	lineEvents, err := s.store.TaskLineEvents(ctx, task.ID)
	if err != nil {
		return TaskLifecycle{}, err
	}
	view.buildTimeline(task, card, approvals, reviews, lineEvents)
	return view, nil
}

func (s *Service) setLifecycleIdleSince(ctx context.Context, view *TaskLifecycle, task domain.Task,
	card domain.TaskMemory, wait taskLineWait, turn domain.Turn, approvals []domain.PetApproval, reviews []store.TaskLifecycleReviewResult) error {
	watch, err := s.store.PetTaskWatch(ctx, task.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	row, stalled := taskStall(task, store.TaskStateCard{NextSteps: card.NextSteps, OpenQuestions: card.OpenQuestions},
		wait, store.MainTurnBrief{Status: string(turn.Status), FinishedAt: turn.FinishedAt}, s.now().UTC(),
		petWorkStateWatchIgnored(watch),
		slices.ContainsFunc(approvals, func(a domain.PetApproval) bool { return a.Status == "pending" || a.Status == "executing" }),
		slices.ContainsFunc(reviews, func(r store.TaskLifecycleReviewResult) bool { return petCodeReviewInFlight(r.Status) }))
	if stalled && task.ID != petTaskID {
		view.Now.IdleSince = row.Since
	}
	return nil
}

func (view *TaskLifecycle) buildTimeline(task domain.Task, card domain.TaskMemory, approvals []domain.PetApproval,
	reviews []store.TaskLifecycleReviewResult, lines []domain.TaskLineEvent) {
	events := []lifecycleEvent{{At: task.CreatedAt, Kind: "created", Title: task.Title, RefID: task.ID}}
	for _, goal := range card.GoalHistory {
		events = append(events, lifecycleEvent{
			At: goal.CreatedAt, Kind: "goal", Title: goal.ChangeType,
			Detail: strings.TrimSpace(goal.GoalAfter + "\n" + goal.Reason), RefID: goal.ID,
		})
	}
	for _, note := range card.Constraints {
		events = append(events, lifecycleEvent{At: note.CreatedAt, Kind: "constraint", Detail: note.Text, RefID: note.TurnID})
	}
	for _, approval := range approvals {
		events = append(events, lifecycleEvent{
			At: approval.CreatedAt, Kind: "approval_requested", Title: approval.Title, Detail: approval.Summary, RefID: approval.ID,
		})
		switch approval.Status {
		case "approved", "rejected", "cancelled":
			events = append(events, lifecycleEvent{
				At: approval.ResolvedAt, Kind: "approval_resolved", Title: approval.Status, Detail: approval.DecisionReason, RefID: approval.ID,
			})
		}
	}
	for _, review := range reviews {
		item := lifecycleReview{TaskID: review.TaskID, Code: review.Code, Status: review.Status}
		if review.TurnStatus.EndedCleanly() {
			if verdict := petCodeReviewVerdict(review.Result); verdict != "" {
				item.Decision = strings.TrimPrefix(verdict, "VERDICT: ")
				_, reason, _ := strings.Cut(strings.TrimSpace(review.Result), "\n")
				item.Reason, item.DecidedAt = strings.TrimSpace(reason), review.FinishedAt
				events = append(events, lifecycleEvent{
					At: item.DecidedAt, Kind: "review", Title: item.Decision, Detail: item.Reason, RefID: review.TaskID,
				})
			}
		}
		// A recorded completion decision also gives linked reviews without a
		// readable verdict a durable explanation.
		if item.Decision == "" && task.ReviewTaskID == review.TaskID {
			item.Decision, item.Reason, item.DecidedAt = task.ReviewDecision, task.ReviewReason, task.ReviewDecidedAt
		}
		view.Parallel.Reviews = append(view.Parallel.Reviews, item)
	}
	if task.ReviewDecision != "" {
		events = append(events, lifecycleEvent{
			At: task.ReviewDecidedAt, Kind: "review", Title: task.ReviewDecision, Detail: task.ReviewReason, RefID: task.ReviewTaskID,
		})
	}
	for _, line := range lines {
		after := line.After
		owner := after.Owner
		if after.Who != "" {
			owner += " / " + after.Who
		}
		detail := []string{owner, after.State}
		if after.Step != "" {
			detail = append(detail, after.Step)
		}
		if after.Blocker != "" {
			detail = append(detail, after.Blocker)
		}
		events = append(events, lifecycleEvent{
			At: line.CreatedAt, Kind: "line_" + line.Kind, Title: after.Title, Detail: strings.Join(detail, " · "), RefID: line.LineID,
		})
	}
	slices.SortStableFunc(events, func(a, b lifecycleEvent) int { return a.At.Compare(b.At) })
	view.TimelineTruncated = len(events) > lifecycleTimelineLimit
	view.Timeline = events[max(0, len(events)-lifecycleTimelineLimit):]
}
