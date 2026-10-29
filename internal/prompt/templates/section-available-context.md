## Available context
Large context is available as workspace files. Read only what is needed:{{range .AvailableContext}}{{if .Root}}
Paths under `{{.Root}}/` (each `aha://` URI below is also valid):{{end}}{{range .Entries}}
- `{{.Path}}` ({{.Description}}, {{.Chars}} chars){{end}}{{end}}

{{if .PetDiaryPaths}}
### 宠物日记
日记目录（按需读取其中的文件，不要一次全部读完）：
{{range .PetDiaryPaths}}- `{{.}}`
{{end}}{{end}}