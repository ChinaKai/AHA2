package mail

import (
	stdhtml "html"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	// MaxAttachments and the size thresholds mirror design section 7.1.
	// Attachments within the per-file and total limits are always sent direct;
	// the renderer no longer downgrades an oversized set to a controlled link.
	MaxAttachments          = 10
	MaxAttachmentBytes      = 50 << 20
	MaxTotalAttachmentBytes = 100 << 20
)

// Template is the content that must be rendered. It mirrors the immutable
// Mail Template Version snapshot; callers should pass the version they intend
// to use, not mutable template fields. An empty Mode means legacy render
// behavior so older internal callers keep working unchanged.
type Template struct {
	ID                string
	Version           int
	Mode              domain.MailTemplateMode
	SubjectTemplate   string
	BodyTextTemplate  string
	BodyHTMLTemplate  string
	RequiredVariables []string
	DefaultTo         []string
	DefaultCC         []string
}

// IsGuide reports whether the template is a reference sample. An empty mode is
// the legacy render path.
func (template Template) IsGuide() bool {
	return template.Mode == domain.MailTemplateModeGuide
}

// Attachment is preview metadata for a controlled attachment reference. It
// never contains file bytes or a local path.
type Attachment struct {
	SourceType   string `json:"source_type"`
	SourceID     string `json:"source_id"`
	FileName     string `json:"file_name"`
	MediaType    string `json:"media_type"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	ContentID    string `json:"content_id"`
	DeliveryMode string `json:"delivery_mode"`
	// LinkPlaceholder is the stable body token the delivery worker replaces
	// with a controlled download URL. It is emitted only for link-mode
	// attachments; the token itself is not a secret.
	LinkPlaceholder string `json:"link_placeholder,omitempty"`
}

// Input is the caller supplied render input. Variables contain only values;
// Task/Project/Workspace resolution is the caller's responsibility because
// this package must not depend on the store.
type Input struct {
	// Variables are caller supplied (build/test/release/extra only).
	Variables map[string]string
	// ServerVariables are resolved by trusted server code (task/project/
	// workspace/agent/owner/package). They take precedence over caller input.
	ServerVariables map[string]string
	// Subject, BodyText and BodyHTML are the caller's final content for a
	// guide-mode template. They are ignored by render-mode templates.
	Subject     string
	BodyText    string
	BodyHTML    string
	To          []string
	CC          []string
	Attachments []Attachment
}

// Preview is the rendered, sanitized snapshot candidate. Creating a Mail
// Message from it belongs to the approval task.
type Preview struct {
	TemplateID       string                  `json:"template_id,omitempty"`
	TemplateVersion  int                     `json:"template_version,omitempty"`
	TemplateMode     domain.MailTemplateMode `json:"template_mode,omitempty"`
	Subject          string                  `json:"subject"`
	To               []string                `json:"to"`
	CC               []string                `json:"cc"`
	BodyText         string                  `json:"body_text"`
	BodyHTML         string                  `json:"body_html"`
	Attachments      []Attachment            `json:"attachments"`
	Warnings         []string                `json:"warnings"`
	ApprovalRequired bool                    `json:"approval_required"`
}

// ValidateTemplate checks syntax, the variable allowlist, size limits and the
// HTML safety subset. It is called on save and again before every render.
func ValidateTemplate(template Template) error {
	if template.IsGuide() {
		return validateGuideTemplate(template)
	}
	if strings.TrimSpace(template.SubjectTemplate) == "" {
		return errorf(domain.MailErrTemplateInvalid, "subject template is required")
	}
	if utf8.RuneCountInString(template.SubjectTemplate) > MaxSubjectRunes {
		return errorf(domain.MailErrTemplateInvalid, "subject template exceeds %d characters", MaxSubjectRunes)
	}
	if len(template.BodyTextTemplate) > MaxBodyTextBytes {
		return errorf(domain.MailErrTemplateInvalid, "text body template exceeds %d bytes", MaxBodyTextBytes)
	}
	if len(template.BodyHTMLTemplate) > MaxBodyHTMLBytes {
		return errorf(domain.MailErrTemplateInvalid, "HTML body template exceeds %d bytes", MaxBodyHTMLBytes)
	}
	if strings.TrimSpace(template.BodyTextTemplate) == "" && strings.TrimSpace(template.BodyHTMLTemplate) == "" {
		return errorf(domain.MailErrTemplateInvalid, "template requires a text or HTML body")
	}
	if _, err := validatePlaceholders(template.SubjectTemplate); err != nil {
		return err
	}
	if _, err := validatePlaceholders(template.BodyTextTemplate); err != nil {
		return err
	}
	if _, _, _, err := processHTML(template.BodyHTMLTemplate, htmlTemplateMode, nil); err != nil {
		return err
	}
	for _, name := range template.RequiredVariables {
		names, err := ParsePlaceholders("{{" + name + "}}")
		if err != nil || len(names) != 1 || names[0] != name {
			return errorf(domain.MailErrTemplateInvalid, "required variable %q has invalid syntax", name)
		}
		if !VariableNameAllowed(name) {
			return errorf(domain.MailErrTemplateVariableUnknown, "unknown required variable %q", name)
		}
	}
	if _, err := NormalizeRecipients(template.DefaultTo); err != nil {
		return err
	}
	if _, err := NormalizeRecipients(template.DefaultCC); err != nil {
		return err
	}
	return nil
}

// validateGuideTemplate checks the reference sample's shape and HTML safety.
// Placeholders and required_variables are intentionally not interpreted: in
// guide mode every template body is a literal example, never a render input.
func validateGuideTemplate(template Template) error {
	if strings.TrimSpace(template.SubjectTemplate) == "" {
		return errorf(domain.MailErrTemplateInvalid, "subject example is required")
	}
	if utf8.RuneCountInString(template.SubjectTemplate) > MaxSubjectRunes {
		return errorf(domain.MailErrTemplateInvalid, "subject example exceeds %d characters", MaxSubjectRunes)
	}
	if len(template.BodyTextTemplate) > MaxBodyTextBytes {
		return errorf(domain.MailErrTemplateInvalid, "text body example exceeds %d bytes", MaxBodyTextBytes)
	}
	if len(template.BodyHTMLTemplate) > MaxBodyHTMLBytes {
		return errorf(domain.MailErrTemplateInvalid, "HTML body example exceeds %d bytes", MaxBodyHTMLBytes)
	}
	if strings.TrimSpace(template.BodyTextTemplate) == "" && strings.TrimSpace(template.BodyHTMLTemplate) == "" {
		return errorf(domain.MailErrTemplateInvalid, "guide template requires a text or HTML body example")
	}
	if _, _, _, err := processHTML(template.BodyHTMLTemplate, htmlGuideTemplateMode, nil); err != nil {
		return err
	}
	if _, err := NormalizeRecipients(template.DefaultTo); err != nil {
		return err
	}
	if _, err := NormalizeRecipients(template.DefaultCC); err != nil {
		return err
	}
	return nil
}

// Render substitutes variables, escapes values for their context and runs the
// final sanitizer. Any unsafe result is rejected; no snapshot is produced.
func Render(template Template, input Input) (Preview, error) {
	if err := ValidateTemplate(template); err != nil {
		return Preview{}, err
	}
	if template.IsGuide() {
		return renderGuide(template, input, false)
	}
	variables, err := normalizeVariables(input.Variables)
	if err != nil {
		return Preview{}, err
	}
	serverVariables, err := normalizeServerVariables(input.ServerVariables)
	if err != nil {
		return Preview{}, err
	}
	for name, value := range serverVariables {
		variables[name] = value
	}
	names, err := templatePlaceholders(template)
	if err != nil {
		return Preview{}, err
	}
	for _, name := range names {
		if _, ok := variables[name]; !ok {
			return Preview{}, errorf(domain.MailErrTemplateVariableMissing, "missing variable %q", name)
		}
	}
	for _, name := range template.RequiredVariables {
		if _, ok := variables[name]; !ok {
			return Preview{}, errorf(domain.MailErrTemplateVariableMissing, "missing required variable %q", name)
		}
	}

	subject, err := substituteTemplate(template.SubjectTemplate, variables, false)
	if err != nil {
		return Preview{}, err
	}
	for _, r := range subject {
		if r < 0x20 || r == 0x7f {
			return Preview{}, errorf(domain.MailErrTemplateInvalid, "subject must not contain control characters")
		}
	}
	if utf8.RuneCountInString(subject) > MaxSubjectRunes {
		return Preview{}, errorf(domain.MailErrTemplateInvalid, "rendered subject exceeds %d characters", MaxSubjectRunes)
	}

	bodyText, err := substituteTemplate(template.BodyTextTemplate, variables, false)
	if err != nil {
		return Preview{}, err
	}
	bodyText = normalizeNewlines(bodyText)

	attachments, err := prepareAttachments(input.Attachments)
	if err != nil {
		return Preview{}, err
	}
	if len(attachments) > MaxAttachments {
		return Preview{}, errorf(domain.MailErrAttachmentTooLarge, "at most %d attachments are allowed", MaxAttachments)
	}
	total := int64(0)
	for _, attachment := range attachments {
		if attachment.SizeBytes > MaxAttachmentBytes {
			return Preview{}, errorf(domain.MailErrAttachmentTooLarge,
				"attachment %q exceeds the %d-byte per-file limit", attachment.FileName, MaxAttachmentBytes)
		}
		total += attachment.SizeBytes
	}
	if total > MaxTotalAttachmentBytes {
		return Preview{}, errorf(domain.MailErrTotalTooLarge,
			"attachments exceed the %d-byte total limit", MaxTotalAttachmentBytes)
	}
	allowedCIDs := make(map[string]bool, len(attachments))
	for index := range attachments {
		// Delivery mode is server computed. Attachments inside both limits are
		// always direct; callers cannot request a link or smuggle a placeholder.
		attachments[index].DeliveryMode = "direct"
		attachments[index].LinkPlaceholder = ""
		if attachments[index].ContentID != "" {
			allowedCIDs[attachments[index].ContentID] = true
		}
	}

	bodyHTML := ""
	warnings := make([]string, 0)
	if strings.TrimSpace(template.BodyHTMLTemplate) != "" {
		substituted, err := substituteTemplate(template.BodyHTMLTemplate, variables, true)
		if err != nil {
			return Preview{}, err
		}
		if bodyHTML, _, warnings, err = processHTML(substituted, htmlFinalMode, allowedCIDs); err != nil {
			return Preview{}, err
		}
	}
	if strings.TrimSpace(bodyText) == "" && bodyHTML != "" {
		// Never send HTML-only mail; derive a safe text alternative.
		bodyText = htmlToText(bodyHTML)
	}
	if len(bodyText) > MaxBodyTextBytes {
		return Preview{}, errorf(domain.MailErrTemplateInvalid, "rendered text body exceeds %d bytes", MaxBodyTextBytes)
	}
	if len(bodyHTML) > MaxBodyHTMLBytes {
		return Preview{}, errorf(domain.MailErrTemplateInvalid, "rendered HTML body exceeds %d bytes", MaxBodyHTMLBytes)
	}

	to, cc, err := normalizeRecipientSet(input.To, input.CC)
	if err != nil {
		return Preview{}, err
	}

	return Preview{
		TemplateID:       template.ID,
		TemplateVersion:  template.Version,
		TemplateMode:     template.Mode,
		Subject:          subject,
		To:               to,
		CC:               cc,
		BodyText:         bodyText,
		BodyHTML:         bodyHTML,
		Attachments:      attachments,
		Warnings:         warnings,
		ApprovalRequired: true,
	}, nil
}

// PreviewGuideExample renders a guide template's literal sample content. It is
// preview-only: the example is shown to the Owner/editor but can never be sent
// because message creation calls Render, which requires caller content.
func PreviewGuideExample(template Template, input Input) (Preview, error) {
	if err := ValidateTemplate(template); err != nil {
		return Preview{}, err
	}
	if !template.IsGuide() {
		return Render(template, input)
	}
	return renderGuide(template, input, true)
}

// renderGuide applies the caller's final subject/body through the same HTML
// sanitizer and attachment policy as render mode. It never substitutes
// variables and never falls back to the template example for a real send.
func renderGuide(template Template, input Input, allowExample bool) (Preview, error) {
	subject := input.Subject
	bodyText := input.BodyText
	bodyHTML := input.BodyHTML
	usingExample := false
	if allowExample && strings.TrimSpace(subject) == "" &&
		strings.TrimSpace(bodyText) == "" && strings.TrimSpace(bodyHTML) == "" {
		subject = template.SubjectTemplate
		bodyText = template.BodyTextTemplate
		bodyHTML = template.BodyHTMLTemplate
		usingExample = true
	}
	if strings.TrimSpace(subject) == "" {
		return Preview{}, errorf(domain.MailErrTemplateContentMissing,
			"guide template requires the caller to provide the final subject")
	}
	if strings.TrimSpace(bodyText) == "" && strings.TrimSpace(bodyHTML) == "" {
		return Preview{}, errorf(domain.MailErrTemplateContentMissing,
			"guide template requires the caller to provide the final body")
	}
	if !usingExample {
		if err := rejectGuidePlaceholders(subject, bodyText, bodyHTML); err != nil {
			return Preview{}, err
		}
	}
	for _, r := range subject {
		if r < 0x20 || r == 0x7f {
			return Preview{}, errorf(domain.MailErrTemplateInvalid, "subject must not contain control characters")
		}
	}
	if utf8.RuneCountInString(subject) > MaxSubjectRunes {
		return Preview{}, errorf(domain.MailErrTemplateInvalid, "subject exceeds %d characters", MaxSubjectRunes)
	}
	bodyText = normalizeNewlines(bodyText)
	if len(bodyText) > MaxBodyTextBytes {
		return Preview{}, errorf(domain.MailErrTemplateInvalid, "text body exceeds %d bytes", MaxBodyTextBytes)
	}
	if len(bodyHTML) > MaxBodyHTMLBytes {
		return Preview{}, errorf(domain.MailErrTemplateInvalid, "HTML body exceeds %d bytes", MaxBodyHTMLBytes)
	}

	attachments, allowedCIDs, err := prepareGuideAttachments(input.Attachments)
	if err != nil {
		return Preview{}, err
	}
	finalHTML := ""
	warnings := make([]string, 0)
	if strings.TrimSpace(bodyHTML) != "" {
		if usingExample {
			// The example is preview-only and is shown in a sandboxed iframe.
			// Validate the guide allowlist but keep its literal placeholder text
			// (a final-mode URL check would reject example placeholders that are
			// never substituted).
			if _, _, _, err := processHTML(bodyHTML, htmlGuideTemplateMode, allowedCIDs); err != nil {
				return Preview{}, err
			}
			finalHTML = bodyHTML
		} else if finalHTML, _, warnings, err = processHTML(bodyHTML, htmlFinalMode, allowedCIDs); err != nil {
			return Preview{}, err
		}
	}
	if strings.TrimSpace(bodyText) == "" && finalHTML != "" {
		bodyText = htmlToText(finalHTML)
	}
	if len(bodyText) > MaxBodyTextBytes {
		return Preview{}, errorf(domain.MailErrTemplateInvalid, "text body exceeds %d bytes", MaxBodyTextBytes)
	}
	if len(finalHTML) > MaxBodyHTMLBytes {
		return Preview{}, errorf(domain.MailErrTemplateInvalid, "HTML body exceeds %d bytes", MaxBodyHTMLBytes)
	}
	if !usingExample {
		// Re-check the sanitized/derived output so braces introduced by the
		// HTML-to-text fallback or canonical rendering cannot slip through.
		if err := rejectGuidePlaceholders(subject, bodyText, finalHTML); err != nil {
			return Preview{}, err
		}
	}

	to, cc, err := normalizeRecipientSet(input.To, input.CC)
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		TemplateID:       template.ID,
		TemplateVersion:  template.Version,
		TemplateMode:     domain.MailTemplateModeGuide,
		Subject:          subject,
		To:               to,
		CC:               cc,
		BodyText:         bodyText,
		BodyHTML:         finalHTML,
		Attachments:      attachments,
		Warnings:         warnings,
		ApprovalRequired: true,
	}, nil
}

func rejectGuidePlaceholders(subject, bodyText, bodyHTML string) error {
	for _, part := range []struct {
		name  string
		value string
	}{
		{"subject", subject},
		{"body_text", bodyText},
		{"body_html", bodyHTML},
	} {
		if strings.Contains(part.value, "{{") || strings.Contains(part.value, "}}") {
			return errorf(domain.MailErrTemplatePlaceholderUnresolved,
				"guide-mode %s still contains a {{...}} placeholder; write the final text instead", part.name)
		}
	}
	return nil
}

func prepareGuideAttachments(input []Attachment) ([]Attachment, map[string]bool, error) {
	attachments, err := prepareAttachments(input)
	if err != nil {
		return nil, nil, err
	}
	if len(attachments) > MaxAttachments {
		return nil, nil, errorf(domain.MailErrAttachmentTooLarge, "at most %d attachments are allowed", MaxAttachments)
	}
	total := int64(0)
	for _, attachment := range attachments {
		if attachment.SizeBytes > MaxAttachmentBytes {
			return nil, nil, errorf(domain.MailErrAttachmentTooLarge,
				"attachment %q exceeds the %d-byte per-file limit", attachment.FileName, MaxAttachmentBytes)
		}
		total += attachment.SizeBytes
	}
	if total > MaxTotalAttachmentBytes {
		return nil, nil, errorf(domain.MailErrTotalTooLarge,
			"attachments exceed the %d-byte total limit", MaxTotalAttachmentBytes)
	}
	allowedCIDs := make(map[string]bool, len(attachments))
	for index := range attachments {
		// Delivery mode stays server computed: guide mode cannot request a
		// controlled link or smuggle a placeholder.
		attachments[index].DeliveryMode = "direct"
		attachments[index].LinkPlaceholder = ""
		if attachments[index].ContentID != "" {
			allowedCIDs[attachments[index].ContentID] = true
		}
	}
	return attachments, allowedCIDs, nil
}

// CallerVariables returns the caller-supplied namespaces (build/test/release/
// extra) referenced by a template. Editor previews and template saves use the
// same scan so required_variables never drifts from the placeholders that will
// actually be substituted.
func CallerVariables(template Template) ([]string, error) {
	names, err := templatePlaceholders(template)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if CallerVariableAllowed(name) {
			result = append(result, name)
		}
	}
	return result, nil
}

func templatePlaceholders(template Template) ([]string, error) {
	names := make([]string, 0)
	subject, err := validatePlaceholders(template.SubjectTemplate)
	if err != nil {
		return nil, err
	}
	text, err := validatePlaceholders(template.BodyTextTemplate)
	if err != nil {
		return nil, err
	}
	_, htmlNames, _, err := processHTML(template.BodyHTMLTemplate, htmlTemplateMode, nil)
	if err != nil {
		return nil, err
	}
	names = append(names, subject...)
	names = append(names, text...)
	names = append(names, htmlNames...)
	return uniqueSorted(names), nil
}

func substituteTemplate(value string, variables map[string]string, escapeHTML bool) (string, error) {
	var builder strings.Builder
	builder.Grow(len(value))
	for index := 0; index < len(value); {
		if strings.HasPrefix(value[index:], "{{") {
			end := strings.Index(value[index+2:], "}}")
			if end < 0 {
				return "", errorf(domain.MailErrTemplateInvalid, "unterminated placeholder")
			}
			name := value[index+2 : index+2+end]
			replacement, ok := variables[name]
			if !ok {
				return "", errorf(domain.MailErrTemplateVariableMissing, "missing variable %q", name)
			}
			if escapeHTML {
				replacement = stdhtml.EscapeString(replacement)
			}
			builder.WriteString(replacement)
			index += 2 + end + 2
			continue
		}
		builder.WriteByte(value[index])
		index++
	}
	return builder.String(), nil
}

func normalizeNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

// NormalizeRecipient accepts only a bare RFC 5322 addr-spec. Display names,
// whitespace, control characters and comment syntax are rejected so a
// recipient can never smuggle an SMTP header.
func NormalizeRecipient(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value {
		return "", errorf(domain.MailErrRecipientInvalid, "recipient is empty or has surrounding whitespace")
	}
	for _, r := range trimmed {
		if r <= 0x20 || r == 0x7f || r == '<' || r == '>' || r == ',' || r == ';' {
			return "", errorf(domain.MailErrRecipientInvalid, "recipient contains illegal characters")
		}
	}
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil || parsed.Name != "" || parsed.Address != trimmed {
		return "", errorf(domain.MailErrRecipientInvalid, "recipient is not a valid email address")
	}
	local, domainPart, found := strings.Cut(parsed.Address, "@")
	if !found || local == "" || domainPart == "" || !strings.Contains(domainPart, ".") {
		return "", errorf(domain.MailErrRecipientInvalid, "recipient is not a valid email address")
	}
	return trimmed, nil
}

// NormalizeRecipients validates and de-duplicates a recipient list.
func NormalizeRecipients(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		address, err := NormalizeRecipient(value)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(address)
		if seen[key] {
			return nil, errorf(domain.MailErrRecipientInvalid, "duplicate recipient")
		}
		seen[key] = true
		result = append(result, address)
	}
	if len(result) > MaxRecipients {
		return nil, errorf(domain.MailErrRecipientInvalid, "too many recipients")
	}
	return result, nil
}

func normalizeRecipientSet(to, cc []string) ([]string, []string, error) {
	normalizedTo, err := NormalizeRecipients(to)
	if err != nil {
		return nil, nil, err
	}
	normalizedCC, err := NormalizeRecipients(cc)
	if err != nil {
		return nil, nil, err
	}
	seen := make(map[string]bool, len(normalizedTo)+len(normalizedCC))
	for _, address := range normalizedTo {
		seen[strings.ToLower(address)] = true
	}
	for _, address := range normalizedCC {
		key := strings.ToLower(address)
		if seen[key] {
			return nil, nil, errorf(domain.MailErrRecipientInvalid, "duplicate recipient")
		}
		seen[key] = true
	}
	if len(seen) > MaxRecipients {
		return nil, nil, errorf(domain.MailErrRecipientInvalid, "too many recipients")
	}
	return normalizedTo, normalizedCC, nil
}

func prepareAttachments(input []Attachment) ([]Attachment, error) {
	if len(input) == 0 {
		return []Attachment{}, nil
	}
	result := make([]Attachment, 0, len(input))
	seen := make(map[string]bool, len(input))
	for _, attachment := range input {
		if attachment.ContentID == "" {
			// The content id of a preview snapshot is the underlying Task
			// attachment id: stable, unique within the Task and not a path.
			attachment.ContentID = attachment.SourceID
		}
		if attachment.ContentID == "" {
			return nil, ErrAttachmentNotFound()
		}
		if seen[attachment.ContentID] {
			// The same source attachment may arrive through both
			// attachment_ids and build_artifact_ids; keep exactly one snapshot.
			continue
		}
		seen[attachment.ContentID] = true
		result = append(result, attachment)
	}
	return result, nil
}

// htmlToText produces the required text/plain alternative from sanitized HTML.
func htmlToText(input string) string {
	contextNode := &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := xhtml.ParseFragment(strings.NewReader(input), contextNode)
	if err != nil {
		return ""
	}
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		switch node.Type {
		case xhtml.TextNode:
			builder.WriteString(node.Data)
		case xhtml.ElementNode:
			switch strings.ToLower(node.Data) {
			case "br":
				builder.WriteByte('\n')
			case "p", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre", "table", "caption":
				builder.WriteString("\n\n")
			case "li":
				builder.WriteString("\n- ")
			case "tr":
				builder.WriteByte('\n')
			case "td", "th":
				// Cells become tab-separated columns; the line trimming below
				// removes the leading/trailing tabs so each row stays readable.
				builder.WriteString("\t")
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			switch strings.ToLower(node.Data) {
			case "p", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre", "table", "caption":
				builder.WriteString("\n\n")
			}
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	lines := strings.Split(strings.ReplaceAll(builder.String(), " ", " "), "\n")
	output := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		output = append(output, line)
	}
	return strings.TrimSpace(strings.Join(output, "\n"))
}
