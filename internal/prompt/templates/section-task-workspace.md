## Task and workspace
- project: {{.ProjectName}}
- task: {{.TaskTitle}} ({{.TaskCode}})
- current goal: {{.CurrentGoalSummary}}
- workspace: {{.Workspace}}
- task workdir: {{.TaskWorkspace}}
- transport: {{.WorkspaceTransport}}{{if .TaskBranch}}
- branch: {{.TaskBranch}}{{end}}
