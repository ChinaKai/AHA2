package app

import (
	"fmt"
	"sort"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// noteExecutionActivity keeps the minimal live tool state that the stored Turn
// cannot answer. Only event type, tool identity and tool name are inspected;
// command lines, tool arguments and output tails stay out of this tracker.
func (s *Service) noteExecutionActivity(turn domain.Turn, event ExecutionEvent) {
	switch event.Type {
	case "agent_command_started", "agent_command_finished", "agent_resumed":
	default:
		return
	}
	now := s.now().UTC()
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	state := s.activities[turn.ID]
	if state == nil {
		state = &domain.TurnActivityState{}
		s.activities[turn.ID] = state
	}
	switch event.Type {
	case "agent_command_started":
		id := activityToolID(event.Data)
		// Some providers repeat a start for the same call. Replace instead of
		// stacking duplicates so one finish cannot leave a stale open call.
		if id != "" {
			state.OpenTools = removeActivityToolCall(state.OpenTools, id)
		}
		state.OpenTools = append(state.OpenTools, domain.ActivityToolCall{
			ID: id, Name: activityToolName(event.Data), Since: now,
		})
	case "agent_command_finished":
		state.OpenTools = removeActivityToolCall(state.OpenTools, activityToolID(event.Data))
		if len(state.OpenTools) == 0 {
			state.ThinkingSince = now
		}
	case "agent_resumed":
		state.ThinkingSince = now
	}
}

func activityToolID(data map[string]any) string {
	return firstText(data, "tool_call_id", "tool_use_id")
}

func activityToolName(data map[string]any) string {
	value, ok := data["tool_name"]
	if !ok {
		return ""
	}
	return domain.NormalizeToolName(fmt.Sprint(value))
}

func removeActivityToolCall(tools []domain.ActivityToolCall, id string) []domain.ActivityToolCall {
	for index := len(tools) - 1; index >= 0; index-- {
		if tools[index].ID != id {
			continue
		}
		return append(tools[:index], tools[index+1:]...)
	}
	// Events without a provider call ID still close the most recent call, so a
	// provider that omits IDs cannot leave a stale tool phase behind forever.
	if id == "" && len(tools) > 0 {
		return tools[:len(tools)-1]
	}
	return tools
}

func (s *Service) activityState(turnID string) *domain.TurnActivityState {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	state := s.activities[turnID]
	if state == nil {
		return nil
	}
	snapshot := &domain.TurnActivityState{
		OpenTools:     append([]domain.ActivityToolCall(nil), state.OpenTools...),
		ThinkingSince: state.ThinkingSince,
	}
	return snapshot
}

func (s *Service) clearActivity(turnID string) {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	delete(s.activities, turnID)
}

func (s *Service) activitySnapshot(turn domain.Turn) domain.TaskActivity {
	return domain.DeriveActivity(turn, s.activityState(turn.ID), s.now().UTC())
}

// TaskActivity returns the activity snapshot for the Turn the Round card should
// describe: an unfinished Turn if one exists (Main first), otherwise the newest
// finished Turn. Task detail and SSE both go through this function so a refresh
// and a live event cannot disagree about the phase.
func (s *Service) TaskActivity(round domain.TaskRound, turns []domain.Turn) *domain.TaskActivity {
	candidates := make([]domain.Turn, 0, len(turns))
	for _, turn := range turns {
		if round.ID == "" || turn.RoundID == round.ID {
			candidates = append(candidates, turn)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		a, b := candidates[left], candidates[right]
		if a.Status.Terminal() != b.Status.Terminal() {
			return !a.Status.Terminal()
		}
		if (a.AgentID == "main") != (b.AgentID == "main") {
			return a.AgentID == "main"
		}
		if a.Generation != b.Generation {
			return a.Generation > b.Generation
		}
		if a.Attempt != b.Attempt {
			return a.Attempt > b.Attempt
		}
		if a.Sequence != b.Sequence {
			return a.Sequence > b.Sequence
		}
		return a.QueuedAt.After(b.QueuedAt)
	})
	activity := s.activitySnapshot(candidates[0])
	return &activity
}
