# 最近上下文

最近已完成的用户与 Main Agent 交互，仅从聊天消息中筛选，按从旧到新排序。最后一轮是 Current Inbox Batch 的直接上下文，后者会单独提供。参考截图或“继续”之类的回复前，先用这段对话消解指代。{{range .RecentExchanges}}

## 第 {{.Number}} 轮
{{range .Users}}- 用户（{{.Sender}}）：{{.Summary}}
{{end}}- Main Agent：{{.Reply}}{{end}}
