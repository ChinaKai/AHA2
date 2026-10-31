## Task 与工作区
- 项目：{{.ProjectName}}
- 任务：{{.TaskTitle}}（{{.TaskCode}}）
- 当前目标：{{.CurrentGoalSummary}}
- 工作区：{{.Workspace}}
- Task 工作目录：{{.TaskWorkspace}}
- 传输方式：{{.WorkspaceTransport}}{{if .TaskBranch}}
- 分支：{{.TaskBranch}}{{end}}
