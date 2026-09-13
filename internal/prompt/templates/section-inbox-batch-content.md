Process the following messages routed to you by AHA. They are a fixed Inbox Batch. Preserve their order and source boundaries.
{{range .InboxItems}}
## Inbox {{.Sequence}} [{{.SourceKind}} from {{.SourceAgentID}}]
{{if .ChannelSender}}Channel sender: {{.ChannelSender}}
{{end}}{{if .ChannelConversation}}Channel conversation: {{.ChannelConversation}}
{{end}}{{if .MentionedParticipants}}Mentioned participants: {{.MentionedParticipants}}
{{end}}{{if .Content}}
{{.Content}}
{{end}}{{if .Attachments}}
Attachments (see the attachment index in Available context):
{{range .Attachments}}- {{.Name}} ({{.MediaType}}, {{.Size}} bytes, id {{.ID}})
{{end}}{{end}}{{end}}
