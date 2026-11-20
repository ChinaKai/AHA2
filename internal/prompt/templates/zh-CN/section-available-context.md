## 可用上下文
下面这些文件是按需读取的参考资料，不必通读，用到哪个读哪个：{{range .AvailableContext}}{{if .Root}}
路径位于 `{{.Root}}/`（下面的每个 `aha://` URI 也有效）：{{end}}{{range .Entries}}
- `{{.Path}}`（{{.Description}}，{{.Chars}} 字符）{{end}}{{end}}
