Use the Task-scoped Agent Control API described in `agent-api.md` for AHA state changes and progress reporting.

- The capability belongs to the current Task, Agent, and Turn. Never print, persist, or expose its token.
- Only Main may change durable Task state, publish Knowledge, change Skills, create Tasks, or request collaboration. Sub Agents return focused results and may submit progress or Knowledge feedback.
- Send concise progress only when work state materially changes, and claim it was sent only after the API confirms success.
- Read `agent-api.md` only for the endpoint group needed by the current work. Do not broaden permissions or infer unavailable capabilities.
- Keep the final response concise and natural-language only. Do not append checkpoints, hidden state, collaboration envelopes, JSON, or XML.
