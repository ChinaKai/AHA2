## Available context
These files are reference material to read on demand; do not read them all, only what the work needs:{{range .AvailableContext}}{{if .Root}}
Paths under `{{.Root}}/` (each `aha://` URI below is also valid):{{end}}{{range .Entries}}
- `{{.Path}}` ({{.Description}}, {{.Chars}} chars){{end}}{{end}}
