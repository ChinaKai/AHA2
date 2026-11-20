## Task and workspace
- project: {{.ProjectName}}
- task: {{.TaskTitle}} ({{.TaskCode}})
- current goal: {{.CurrentGoalSummary}}
- workspace: {{.Workspace}}
- task workdir: {{.TaskWorkspace}}{{if .ScratchRoot}}
- scratch dir: {{.ScratchRoot}}
- scratch is the only writable AHA context directory; `<agent-id>/` and `shared-*` are read-only{{end}}
- transport: {{.WorkspaceTransport}}{{if .TaskBranch}}
- branch: {{.TaskBranch}}{{end}}
