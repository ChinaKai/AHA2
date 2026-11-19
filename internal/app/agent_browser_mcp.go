package app

import "context"

// AgentBrowserMCPAllowed is the single server-side gate for the shared-browser
// domain MCP. The runtime injects the server only when browserMCPInjected
// returns true, and every MCP method re-checks this gate, so knowing the URL or
// reading tools/list is never an authorization shortcut. The domain is absent
// for the pet Task, for a Task without an active browser grant, and for a
// channel-hosted Turn.
func (s *Service) AgentBrowserMCPAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.browserMCPInjected(ctx, call.Task, call.Turn.InboxBatchID)
}
