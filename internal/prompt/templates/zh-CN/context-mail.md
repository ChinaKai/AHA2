# 邮件

本文件不包含凭据。绝不要读取、猜测、记录或保存 SMTP 授权码；
`credential_configured` 是这里唯一的凭据状态。

## 当前 Task 可用账号
{{range .MailAccounts}}
- account_id：{{.ID}}
- 发件地址：{{.FromAddress}}
- 显示名：{{.DisplayName}}
{{if .Aliases}}- 别名：{{range $index, $alias := .Aliases}}{{if $index}}，{{end}}{{$alias}}{{end}}
{{end}}- 状态：{{.Status}}
- 已配置授权码：{{.CredentialConfigured}}
{{if .DefaultTemplateID}}- 默认模板：{{.DefaultTemplateID}}
{{end}}{{end}}
## 可见模板
{{range .MailTemplates}}- {{.ID}} | {{.Name}} | v{{.Version}} | {{.Mode}} | {{.Scope}}{{if .ProjectID}}（{{.ProjectID}}）{{end}}{{if .Variables}} | 变量：{{.Variables}}{{end}}
{{end}}{{if .DefaultGuideTemplates}}
## 默认 guide 模板（仅作参考）
下面的模板是参考样板，不是要发送的内容。请自己写最终主题和正文，确保事实准确，
并且不要留下 `{{` 或 `}}` 占位符。调用 `mail_send` 时同时传入 `template_id`
和你的最终 `subject` 与 `body_text`/`body_html`；模板会作为参考版本记录。
模板示例不会作为回退内容发送。
{{range .DefaultGuideTemplates}}
### {{.Name}}（{{.ID}} v{{.Version}}，{{.Scope}}）
{{if .Description}}- 填写说明/检查清单：
{{.Description}}{{if .DescriptionTruncated}}
- [填写说明过长，已按上下文预算截断]{{end}}
{{end}}- 示例主题：{{.Subject}}
{{if .BodyText}}- 示例纯文本正文：
```
{{.BodyText}}
```{{if .BodyTextTruncated}}
- [纯文本正文过长，已按上下文预算截断]{{end}}
{{end}}{{if .BodyHTML}}- 示例 HTML 正文：
```html
{{.BodyHTML}}
```{{if .BodyHTMLTruncated}}
- [HTML 正文过长，已按上下文预算截断]{{end}}
{{end}}
{{end}}{{end}}
## 规则

- 发送前必须先调用邮件预览工具；预览与发送使用同一套账号、模板、收件人与附件校验。
- guide 模式模板要求你提供最终主题和正文。它只是参考样板；不要把示例当作邮件正文，
  也不要依赖变量替换。
- 每次发送都会创建 `pending_approval`。你不能批准、拒绝、重试或取消邮件，
  任何其他工具也不能绕过 Owner 审批。
- 附件只能是当前 Task 附件或为本 Task 登记的构建产物。本地路径、外部 URL、
  原始文件字节以及可执行文件/脚本都会被拒绝。
- BCC 仅 Owner 可用；Agent 工具不接受 BCC，不要添加隐藏收件人。
- 同一封逻辑邮件使用稳定的幂等键；重复使用会返回原消息，不会重复创建。
- 不要把收件人地址、渲染后的正文或邮件状态细节写入日志或知识库。
