# Agent API

The AHA2 control plane exposes a Task-scoped API at {{.AgentAPIURL}}.
Use `AHA2_AGENT_API_TOKEN` as a Bearer token. Never print, persist, or expose it. The capability expires when this Turn finishes.

Send UTF-8 JSON with `Content-Type: application/json; charset=utf-8`. Windows PowerShell 5.1 must send UTF-8 bytes, for example `[Text.Encoding]::UTF8.GetBytes($json)`.

## Turn state

- `GET /api/v1/agent/capabilities`
- `PATCH /api/v1/agent/turn/memory`
- `POST /api/v1/agent/turn/attachments`
- `POST /api/v1/agent/turn/messages`
- `POST /api/v1/agent/collaboration/batches`
- `GET /api/v1/agent/project/workspaces`
- `GET /api/v1/agent/project/runtimes`
- `POST /api/v1/agent/tasks`
- `GET /api/v1/agent/tasks/{task}`

The memory endpoint accepts exactly one of `append` or `replace`, with optional `current_goal` and the decisions, facts, excluded, progress, verification, and next_actions arrays. Task Memory is not default Prompt context. Use replace only when the caller can carry forward every still-valid item.

Progress messages use:

```json
{"message":"concise user-facing progress","attachment_ids":[]}
```

Collaboration batches use:

```json
{"actions":[{"agent_id":"sub-001","title":"...","assignment":"...","required":true}],"main_followup":"..."}
```

Task creation is Main-only, stays inside the current Project, and inherits the current runtime when model fields are omitted. To select another configured runtime, first read the project runtime list and submit its exact non-secret fields.

## Attachment delivery

Upload the actual file as multipart/form-data with one `file` field, read `attachment.id`, then bind it through `attachment_ids` in a turn message. Upload alone does not publish. Never reuse IDs across Tasks or claim delivery without a confirmed result.

## Channel operations

Channel context endpoints are available only with a server-verified ChannelContext. Private Owner assistants may read channel context/catalog and preview takeover, exit, task creation, status, or handoff decisions. Preview creates confirmation only. Group digital-human Turns may submit handoffs but cannot use catalog or control actions.

For an ordinary Task with an active group primary channel, Main may read selected contacts and send one durable blocker coordination message. Use stable request IDs for retries, set `purpose` to `blocker`, and only mention contacts returned by the current Task channel.

- `GET /api/v1/agent/channel/context`
- `GET /api/v1/agent/channel/catalog`
- `POST /api/v1/agent/channel/actions/preview`
- `GET /api/v1/agent/channel/contacts`
- `POST /api/v1/agent/channel/messages`
- `POST /api/v1/agent/channel/reply-decision`
- `POST /api/v1/agent/channel/handoffs`

Action previews use `{"operation":"takeover|exit|create_task|status_change|handoff_decision","target_id":"optional","intent":{...}}`. Contact messages use `{"request_id":"stable-retry-key","purpose":"blocker","message":"...","mention_identity_link_ids":["channel_identity_..."]}`.

When the current group message sender is a verified bot, call
`POST /api/v1/agent/channel/reply-decision` before the final answer:
`{"decision":"continue"}` sends the final answer back to the channel;
`{"decision":"end"}` keeps the final answer in AHA Web and closes this bot exchange.
The server also enforces the configured maximum consecutive bot turns. Do not use
the ordinary channel message endpoint for the bot's normal reply.

## Knowledge and Skills

- `GET /api/v1/agent/knowledge`
- `GET /api/v1/agent/knowledge/{id}`
- `POST /api/v1/agent/knowledge/candidates`
- `POST /api/v1/agent/knowledge/{id}/feedback`
- `GET /api/v1/agent/skills`
- `POST /api/v1/agent/skills`
- `GET /api/v1/agent/skills/{id}`
- `PUT /api/v1/agent/skills/{id}`

Knowledge changes are proposals. Use a revision only after the response reports it verified. Existing revisions require `base_revision`; Skill updates require the current `base_version` and replace the complete selected package.

Knowledge candidates use:

```json
{"candidates":[{"entry_id":"","base_revision":0,"scope":"project","parent_id":"","slug":"topic","sort_order":0,"is_index":false,"type":"practice","title":"...","body":"...","confidence":0.8,"product_line_id":""}]}
```

Feedback uses `{"kind":"helped|stale|wrong"}`.

## Managed processes

- `GET /api/v1/agent/processes`
- `POST /api/v1/agent/processes`
- `GET /api/v1/agent/processes/{name}`
- `POST /api/v1/agent/processes/{name}/stop`

Managed processes are owned by AHA2, continue after the current Turn, and must use a working directory inside the Task workspace.

Process creation uses `{"name":"dev-server","executable":"...","args":[],"cwd":"...","env":{}}`.

## Hardware

- `GET /api/v1/agent/hardware`
- `GET /api/v1/agent/hardware/{hardware}/terminal`
- `POST /api/v1/agent/hardware/{hardware}/connect`
- `POST /api/v1/agent/hardware/{hardware}/disconnect`
- `POST /api/v1/agent/hardware/{hardware}/send`
- `POST /api/v1/agent/hardware/{hardware}/login`

Terminal reads accept `transport=serial|network`, `after`, and `limit`. Sends use `{"data":"...","encoding":"text|hex"}`. Login accepts configurable prompts, line ending, wakeup, timeout, and retries.

The API cannot change hardware configuration and enforces the Task hardware access mode. All requests require `Authorization: Bearer $AHA2_AGENT_API_TOKEN`.
