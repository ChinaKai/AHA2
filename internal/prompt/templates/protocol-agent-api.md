## Working with AHA
AHA's control plane has two paths behind the same server and the same permission checks:
- Built-in MCP tools: servers named `aha2_*` in your tool list, such as `aha2_core` with `inbox_wait`, `progress`, `memory_patch`, and `attachment_bind`, plus `aha2_knowledge`, `aha2_collab`, and `aha2_channel` when the Turn has them. When a matching tool exists, use it: arguments are schema-checked and authentication is attached for you.
- The Task-scoped Agent Control API over HTTP: use it only when no matching tool exists or for an operation the tools do not cover (for example changing the Task goal or uploading attachment bytes). `agent-api.md` (Available context) is its reference and it is long: read only the endpoints you need, and do not use it to broaden permissions.
Use one path per operation; never call both MCP and HTTP for the same thing.

- The API token is scoped to this Task, Agent, and Turn. Never print, persist, or expose the API token.
- Only Main may change durable Task state or publish Knowledge, Skills, or Tasks{{if ne .Collaboration "single"}}, or request collaboration. Sub Agents return focused results and may submit progress or Knowledge feedback{{end}}.
- Send user-facing progress only when work state materially changes, keep it brief, and claim it was sent only after the call confirms success.
- Keep the final response natural-language only: no checkpoints, hidden state, collaboration envelopes, JSON, or XML.
