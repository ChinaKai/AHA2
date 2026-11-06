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
{{range .MailTemplates}}- {{.ID}} | {{.Name}} | v{{.Version}} | {{.Mode}} | {{.Scope}}{{if .ProjectID}} ({{.ProjectID}}){{end}}{{if .Variables}} | variables: {{.Variables}}{{end}}
{{end}}{{if .DefaultGuideTemplates}}
## Default guide template (reference only)
The template below is a reference sample, not content to send. Write the final
subject and body yourself, keep all facts accurate, and do not leave `{{` or
`}}` placeholders. Call `mail_send` with `template_id` plus your final
`subject` and `body_text`/`body_html`; the template is recorded as the
reference version. Its example is never sent as a fallback.
{{range .DefaultGuideTemplates}}
### {{.Name}} ({{.ID}} v{{.Version}}, {{.Scope}})
{{if .Description}}- Filling instructions/checklist:
{{.Description}}{{if .DescriptionTruncated}}
- [description truncated to fit the context budget]{{end}}
{{end}}- Example subject: {{.Subject}}
{{if .BodyText}}- Example text body:
```
{{.BodyText}}
```{{if .BodyTextTruncated}}
- [text body truncated to fit the context budget]{{end}}
{{end}}{{if .BodyHTML}}- Example HTML body:
```html
{{.BodyHTML}}
```{{if .BodyHTMLTruncated}}
- [HTML body truncated to fit the context budget]{{end}}
{{end}}
{{end}}{{end}}
## Rules

- Always call the mail preview tool before sending. Preview and send use the
  same account, template, recipient and attachment validation.
- A guide-mode template requires your own final subject and body. It is a
  reference sample; never treat its example as the message and never rely on
  variable substitution.
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
