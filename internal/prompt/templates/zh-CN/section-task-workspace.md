## Task 与工作区
- 项目：{{.ProjectName}}
- 任务：{{.TaskTitle}}（{{.TaskCode}}）
- 当前目标：{{.CurrentGoalSummary}}
- 工作区：{{.Workspace}}
- Task 工作目录：{{.TaskWorkspace}}{{if .ScratchRoot}}
- scratch 目录：{{.ScratchRoot}}
- scratch 是唯一可写的 AHA 上下文目录；`<agent-id>/` 和 `shared-*` 均为只读{{end}}
- 传输方式：{{.WorkspaceTransport}}{{if .TaskBranch}}
- 分支：{{.TaskBranch}}{{end}}
