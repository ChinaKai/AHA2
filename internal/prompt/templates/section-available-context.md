## Available context
Large context is available as workspace files. Read only what is needed:{{range .AvailableContext}}{{if .Root}}
Paths under `{{.Root}}/` (each `aha://` URI below is also valid):{{end}}{{range .Entries}}
- `{{.Path}}` ({{.Description}}, {{.Chars}} chars){{end}}{{end}}
