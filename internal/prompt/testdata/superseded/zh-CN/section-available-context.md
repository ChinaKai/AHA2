## 可用上下文
大量上下文以工作区文件形式提供。只读取需要的内容：{{range .AvailableContext}}{{if .Root}}
路径位于 `{{.Root}}/`（下面的每个 `aha://` URI 也有效）：{{end}}{{range .Entries}}
- `{{.Path}}`（{{.Description}}，{{.Chars}} 字符）{{end}}{{end}}
