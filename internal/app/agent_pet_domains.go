package app

import (
	"context"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// AgentCoreMCPToolNames is shared by discovery and dispatch. The pet already
// has a permission-checked cross-project messaging tool; task_message retains
// its ordinary project-scoped contract and is never widened for the pet.
func (s *Service) AgentCoreMCPToolNames(ctx context.Context, call AgentCallContext) []string {
	return s.coreMCPNamesForSnapshot(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
}

func (s *Service) coreMCPNamesForSnapshot(ctx context.Context, task domain.Task, snapshotID, agentID string) []string {
	names := coreMCPToolNames(s.agentSnapshotWritable(ctx, task, snapshotID, agentID))
	if task.ID != petTaskID {
		return names
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if name != "task_message" {
			result = append(result, name)
		}
	}
	return result
}

// petDomainReadable rechecks the live permission rather than trusting runtime
// injection. No other Task in the internal pet Project inherits this access.
func (s *Service) petDomainReadable(ctx context.Context, task domain.Task) bool {
	if task.ID != petTaskID {
		return task.ProjectID != petProjectID
	}
	if task.ProjectID != petProjectID {
		return false
	}
	assistant, err := s.petAssistant(ctx)
	return err == nil && assistant.PetAllowViewTasks
}

// AgentKnowledgeMCPToolNames adds only the missing guide/feedback operations to
// the pet. Its existing knowledge and Skill management tools remain canonical.
func (s *Service) AgentKnowledgeMCPToolNames(ctx context.Context, call AgentCallContext) []string {
	return s.knowledgeMCPNamesForTask(ctx, call.Task, s.AgentWriteAllowed(ctx, call))
}
func (s *Service) knowledgeMCPNamesForTask(ctx context.Context, task domain.Task, write bool) []string {
	if task.ID != petTaskID {
		return knowledgeMCPToolNames(write)
	}
	names := []string{"knowledge_guide"}
	if _, err := s.petKnowledgeWritable(ctx); err == nil && write {
		names = append(names, "knowledge_feedback")
	}
	return names
}
