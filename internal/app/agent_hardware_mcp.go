package app

import (
	"context"
	"github.com/ChinaKai/AHA2/internal/domain"
)

// AgentHardwareMCPAllowed is the single server-side gate for the Task hardware
// domain MCP. It deliberately does not share the browser trigger: the domain is
// present exactly when the calling Task has at least one bound hardware group,
// regardless of channel input or routes. Pet access rechecks live view
// permission. Every method re-checks it, so tool visibility is never authorization.
func (s *Service) AgentHardwareMCPAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.hardwareMCPInjected(ctx, call.Task, call.Turn.InboxBatchID)
}

// Device I/O requires an explicitly writable Main snapshot and a writable
// resource. The pet additionally needs its live Task-management permission.
func (s *Service) AgentHardwareMCPWriteAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.AgentHardwareMCPAllowed(ctx, call) && s.hardwareMCPSnapshotWritable(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
}

func (s *Service) hardwareMCPSnapshotWritable(ctx context.Context, task domain.Task, snapshotID, agentID string) bool {
	if task.Status.Terminal() || !s.agentSnapshotWritable(ctx, task, snapshotID, agentID) {
		return false
	}
	if task.ID == petTaskID {
		assistant, err := s.petAssistant(ctx)
		return err == nil && assistant.PetAllowCreateTasks
	}
	return true
}
