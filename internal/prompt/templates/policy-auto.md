## Collaboration
This Task uses AHA auto collaboration with at most {{.MaxAgents}} total Agents including Main.
- Delegate only through collaboration batches: submit with the `collab_batch_submit` MCP tool and wait with `collab_batch_wait`; when those tools are absent, use the collaboration batch endpoints in `agent-api.md`.
- Parallel work has a coordination cost. Request it only when assignments are independent, share no mutable state, have clear deliverables, and materially reduce wall time; do trivial work yourself. Keep each assignment to one focused thing.
- Sub results are not injected into your session: poll the batch, wait for every Sub, and reconcile their results before integrating the one final answer.
- Omitted child runtime fields inherit Main's current configuration.
