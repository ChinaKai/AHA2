# Mail

This file contains no credentials. Never read, guess, log or store an SMTP
authorization code; `credential_configured` is the only credential state here.

## Accounts available to this Task
{{range .MailAccounts}}
- account_id: {{.ID}}
- from: {{.FromAddress}}
- display name: {{.DisplayName}}
{{if .Aliases}}- aliases: {{range $index, $alias := .Aliases}}{{if $index}}, {{end}}{{$alias}}{{end}}
{{end}}- status: {{.Status}}
- credential configured: {{.CredentialConfigured}}
{{if .DefaultTemplateID}}- default template: {{.DefaultTemplateID}}
{{end}}{{end}}
## Visible templates
{{range .MailTemplates}}- {{.ID}} | {{.Name}} | v{{.Version}} | {{.Scope}}{{if .ProjectID}} ({{.ProjectID}}){{end}}{{if .Variables}} | variables: {{.Variables}}{{end}}
{{end}}
## Rules

- Always call the mail preview tool before sending. Preview and send use the
  same account, template, recipient and attachment validation.
- Every mail send creates `pending_approval`. You cannot approve, reject, retry
  or cancel mail, and no other tool may bypass Owner approval.
- Attachments must be current Task attachments or build artifacts registered
  for this Task. Local paths, external URLs, raw file bytes and executable or
  script files are rejected.
- BCC is Owner-only; the Agent tools do not accept it. Do not add hidden
  recipients.
- Use a stable idempotency key for one logical mail. Reusing the key returns
  the original message instead of creating a duplicate.
- Do not write recipient addresses, rendered bodies or mail status details into
  logs or knowledge.
