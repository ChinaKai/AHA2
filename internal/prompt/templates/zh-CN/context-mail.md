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
{{range .MailTemplates}}- {{.ID}} | {{.Name}} | v{{.Version}} | {{.Scope}}{{if .ProjectID}}（{{.ProjectID}}）{{end}}{{if .Variables}} | 变量：{{.Variables}}{{end}}
{{end}}
## 规则

- 发送前必须先调用邮件预览工具；预览与发送使用同一套账号、模板、收件人与附件校验。
- 每次发送都会创建 `pending_approval`。你不能批准、拒绝、重试或取消邮件，
  任何其他工具也不能绕过 Owner 审批。
- 附件只能是当前 Task 附件或为本 Task 登记的构建产物。本地路径、外部 URL、
  原始文件字节以及可执行文件/脚本都会被拒绝。
- BCC 仅 Owner 可用；Agent 工具不接受 BCC，不要添加隐藏收件人。
- 同一封逻辑邮件使用稳定的幂等键；重复使用会返回原消息，不会重复创建。
- 不要把收件人地址、渲染后的正文或邮件状态细节写入日志或知识库。
