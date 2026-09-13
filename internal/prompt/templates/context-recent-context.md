# Recent Context

Recent completed user and Main Agent exchanges. The Current Inbox Batch is provided separately.{{range .RecentExchanges}}

## Exchange {{.Number}}
{{range .Users}}- User ({{.Sender}}): {{.Summary}}
{{end}}- Main Agent: {{.Reply}}{{end}}
