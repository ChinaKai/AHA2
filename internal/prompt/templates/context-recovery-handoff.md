{{with .Recovery}}### Recovery handoff

This Current Inbox Batch is resuming after an interrupted Turn.
- Previous Turn: `{{.PreviousTurnID}}` (Turn {{.PreviousTurnSequence}}, agent `{{.AgentID}}`, status `{{.Status}}`, attempt {{.Attempt}}, generation {{.Generation}})
{{if .Error}}- Previous Turn error: {{.Error}}
{{end}}{{if .LatestProgress}}- Latest durable Agent progress: {{.LatestProgress}}
{{end}}{{if .LatestToolSummary}}- Latest durable tool lifecycle: `{{.LatestToolState}}` - {{.LatestToolSummary}}{{if .LatestToolExitCode}} (exit {{.LatestToolExitCode}}){{end}}
{{end}}
Continue from the existing workspace and system state. Inspect existing outputs and results before acting. Do not repeat completed commands or external side effects unless verification shows they are still required. Do not infer that an interrupted in-progress tool definitely succeeded or failed; inspect its durable result or current state first.{{end}}
