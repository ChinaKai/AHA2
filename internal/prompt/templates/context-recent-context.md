# Recent Context

Recent completed user and Main Agent exchanges, filtered from chat messages only and ordered oldest to newest. The final exchange is the immediate context for the Current Inbox Batch, which is provided separately. Use this dialogue to resolve references such as screenshots or “continue” replies before consulting a stored goal.{{range .RecentExchanges}}

## Exchange {{.Number}}
{{range .Users}}- User ({{.Sender}}): {{.Summary}}
{{end}}- Main Agent: {{.Reply}}{{end}}
