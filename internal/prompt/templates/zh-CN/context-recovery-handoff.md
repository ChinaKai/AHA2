{{with .Recovery}}### 中断续接
本 Current Inbox Batch 是在 Turn 中断后恢复的。
- 上一 Turn：`{{.PreviousTurnID}}`（Turn {{.PreviousTurnSequence}}，Agent `{{.AgentID}}`，状态 `{{.Status}}`，attempt {{.Attempt}}，generation {{.Generation}}）
{{if .Error}}- 上一 Turn 错误：{{.Error}}
{{end}}{{if .LatestProgress}}- 最新持久 Agent 进度：{{.LatestProgress}}
{{end}}{{if .LatestToolSummary}}- 最新持久工具生命周期：`{{.LatestToolState}}` - {{.LatestToolSummary}}{{if .LatestToolExitCode}}（exit {{.LatestToolExitCode}}）{{end}}
{{end}}
从现有工作区和系统状态继续。行动前检查已有输出和结果。除非验证表明仍然需要，不要重复已完成的命令或外部副作用。不要推断中断中的工具一定成功或失败；先检查其持久结果或当前状态。{{end}}
