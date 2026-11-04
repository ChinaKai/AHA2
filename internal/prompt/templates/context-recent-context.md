# Recent Context

Recent completed user and Main Agent exchanges, filtered from chat messages only and ordered oldest to newest. Each entry carries a timestamp; use the order and times to resolve “latest”, “earlier”, screenshots, or “continue” references. The final exchange is the immediate context for the Current Inbox Batch, which is provided separately.{{range .RecentExchanges}}

## Exchange {{.Number}}
{{range .Users}}- User ({{.Sender}}{{if .Role}}, {{.Role}}{{end}}{{if .Time}}, {{.Time}}{{end}}): {{.Summary}}
{{end}}- Main Agent{{if .Time}} ({{.Time}}){{end}}: {{.Reply}}{{end}}
