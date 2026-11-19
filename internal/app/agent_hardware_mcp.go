package app

import "context"

// AgentHardwareMCPAllowed is the single server-side gate for the Task hardware
// domain MCP. It deliberately does not share the browser trigger: the domain is
// present exactly when the calling Task has at least one bound hardware group,
// and never for the pet Task or a channel-hosted Turn. Every MCP method
// re-checks it, so tool visibility is never authorization.
func (s *Service) AgentHardwareMCPAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.hardwareMCPInjected(ctx, call.Task, call.Turn.InboxBatchID)
}
