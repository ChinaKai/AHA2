# 最近上下文

最近已完成的用户与 Main Agent 交互，仅从聊天消息中筛选，按从旧到新排序。每条都带时间；用顺序和时间消解“最新”“之前”、截图或“继续”之类的指代。最后一轮是 Current Inbox Batch 的直接上下文，后者会单独提供。{{range .RecentExchanges}}

## 第 {{.Number}} 轮
{{range .Users}}- 用户（{{.Sender}}{{if .Role}}，{{.Role}}{{end}}{{if .Time}}，{{.Time}}{{end}}）：{{.Summary}}
{{end}}- Main Agent{{if .Time}}（{{.Time}}）{{end}}：{{.Reply}}{{end}}
