package mail

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func baseTemplate() Template {
	return Template{
		ID:               "mail_template_test",
		Version:          3,
		SubjectTemplate:  "[AHA] {{project.name}} {{build.version}} 提测通知",
		BodyTextTemplate: "Hi,\n\n{{project.name}} 已完成构建。\n\n任务：{{task.title}}\n版本：{{build.version}}\n",
		BodyHTMLTemplate: `<h1>{{project.name}} 提测</h1><p>任务：<strong>{{task.title}}</strong></p>` +
			`<p><a href="https://example.com/tasks/{{task.id}}">查看任务</a></p>`,
		RequiredVariables: []string{"project.name", "build.version"},
	}
}

func baseVariables() map[string]string {
	return map[string]string{
		"project.name":  "Example",
		"build.version": "v1.2.3",
		"task.id":       "task_1",
		"task.title":    "OTA 回归",
	}
}

func requireCode(t *testing.T, err error, want domain.MailErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got nil", want)
	}
	code, ok := CodeOf(err)
	if !ok || code != want {
		t.Fatalf("error = %v, want code %s", err, want)
	}
}

func TestRenderGoldenTextAndHTML(t *testing.T) {
	preview, err := Render(baseTemplate(), Input{
		Variables: baseVariables(),
		To:        []string{"qa-team@example.com"},
		CC:        []string{"release-manager@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Subject != "[AHA] Example v1.2.3 提测通知" {
		t.Fatalf("subject = %q", preview.Subject)
	}
	wantText := "Hi,\n\nExample 已完成构建。\n\n任务：OTA 回归\n版本：v1.2.3\n"
	if preview.BodyText != wantText {
		t.Fatalf("text = %q, want %q", preview.BodyText, wantText)
	}
	wantHTML := `<h1>Example 提测</h1><p>任务：<strong>OTA 回归</strong></p>` +
		`<p><a href="https://example.com/tasks/task_1">查看任务</a></p>`
	if preview.BodyHTML != wantHTML {
		t.Fatalf("html = %q, want %q", preview.BodyHTML, wantHTML)
	}
	if len(preview.To) != 1 || preview.To[0] != "qa-team@example.com" ||
		len(preview.CC) != 1 || preview.CC[0] != "release-manager@example.com" {
		t.Fatalf("recipients = %#v %#v", preview.To, preview.CC)
	}
	if !preview.ApprovalRequired {
		t.Fatal("preview must require approval")
	}
	if preview.TemplateVersion != 3 {
		t.Fatalf("template version = %d", preview.TemplateVersion)
	}
}

func TestRenderEscapesVariableValues(t *testing.T) {
	template := Template{
		SubjectTemplate:  "Hi {{task.title}}",
		BodyTextTemplate: "{{task.title}}",
		BodyHTMLTemplate: `<p>{{task.title}}</p><a href="https://example.com/{{task.id}}">open</a>`,
	}
	preview, err := Render(template, Input{Variables: map[string]string{
		"task.title": `<script>alert("x")</script>`,
		"task.id":    "task_1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview.BodyHTML, "<script>") {
		t.Fatalf("script survived sanitization: %q", preview.BodyHTML)
	}
	if !strings.Contains(preview.BodyHTML, "&lt;script&gt;") {
		t.Fatalf("text value was not escaped: %q", preview.BodyHTML)
	}
}

func TestTemplateRejectsUnknownAndMalformedVariables(t *testing.T) {
	template := baseTemplate()
	template.SubjectTemplate = "[AHA] {{secret.token}}"
	_, err := Render(template, Input{Variables: baseVariables()})
	requireCode(t, err, domain.MailErrTemplateVariableUnknown)

	template = baseTemplate()
	template.BodyTextTemplate = "{{task.title"
	_, err = Render(template, Input{Variables: baseVariables()})
	requireCode(t, err, domain.MailErrTemplateInvalid)

	template = baseTemplate()
	template.BodyTextTemplate = "{{ task.title }}"
	_, err = Render(template, Input{Variables: baseVariables()})
	requireCode(t, err, domain.MailErrTemplateInvalid)
}

func TestRenderRejectsMissingVariables(t *testing.T) {
	variables := baseVariables()
	delete(variables, "task.id")
	_, err := Render(baseTemplate(), Input{Variables: variables})
	requireCode(t, err, domain.MailErrTemplateVariableMissing)

	variables = baseVariables()
	delete(variables, "project.name")
	_, err = Render(baseTemplate(), Input{Variables: variables})
	requireCode(t, err, domain.MailErrTemplateVariableMissing)
}

func TestTemplateRejectsUnsafeHTML(t *testing.T) {
	tests := []string{
		`<script>alert(1)</script>`,
		`<iframe src="https://example.com"></iframe>`,
		`<p onclick="alert(1)">x</p>`,
		`<img src="https://tracker.example.com/pixel.gif">`,
		`<p style="background:url(javascript:alert(1))">x</p>`,
		`<iframe srcdoc="<script>alert(1)</script>"></iframe>`,
		`<p title="{{task.title}}">x</p>`,
		`<a href="javascript:alert(1)">x</a>`,
		`<a href="data:text/html,<script>alert(1)</script>">x</a>`,
		`<a href="vbscript:msgbox(1)">x</a>`,
		`<a href="file:///etc/passwd">x</a>`,
		`<a href="//evil.example.com/x">x</a>`,
		`<a href="java&#x73;cript:alert(1)">x</a>`,
		"<a href=\"java\tscript:alert(1)\">x</a>",
		`<a href="/relative">x</a>`,
		`<a href="http://example.com">x</a>`,
		`<img src="{{task.id}}">`,
		`<!-- {{task.id}} -->`,
	}
	for _, html := range tests {
		template := Template{SubjectTemplate: "s", BodyTextTemplate: "t", BodyHTMLTemplate: html}
		err := ValidateTemplate(template)
		if err == nil {
			t.Fatalf("HTML %q was accepted", html)
		}
		requireCode(t, err, domain.MailErrTemplateInvalid)
	}
}

func TestRenderRejectsDangerousVariableURL(t *testing.T) {
	template := Template{
		SubjectTemplate:  "s",
		BodyTextTemplate: "t",
		BodyHTMLTemplate: `<a href="{{test.scope}}">x</a>`,
	}
	for _, payload := range []string{
		"javascript:alert(1)",
		"data:text/html,x",
		"vbscript:msgbox(1)",
		"file:///etc/passwd",
		"//evil.example.com",
		"https://example.com/\" onmouseover=\"alert(1)",
	} {
		_, err := Render(template, Input{Variables: map[string]string{"test.scope": payload}})
		if err == nil {
			t.Fatalf("URL payload %q was accepted", payload)
		}
		requireCode(t, err, domain.MailErrTemplateInvalid)
	}
}

func TestRenderRejectsAttributeEscapeAttempts(t *testing.T) {
	template := Template{
		SubjectTemplate:  "s",
		BodyTextTemplate: "t",
		BodyHTMLTemplate: `<p>safe</p>`,
	}
	// Variable values cannot introduce new attributes because they are escaped
	// and the final tree is re-validated.
	_, err := Render(template, Input{Variables: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	template.BodyHTMLTemplate = `<a href="https://example.com/{{task.id}}">x</a>`
	_, err = Render(template, Input{Variables: map[string]string{"task.id": `"><img src=x onerror=alert(1)>`}})
	requireCode(t, err, domain.MailErrTemplateInvalid)
}

func TestCIDMustMatchSelectedAttachment(t *testing.T) {
	template := Template{
		SubjectTemplate:  "s",
		BodyTextTemplate: "t",
		BodyHTMLTemplate: `<p><img src="cid:{{extra.image}}" alt="shot"></p>`,
	}
	attachments := []Attachment{{
		SourceType: "task_attachment", SourceID: "attachment_1",
		FileName: "shot.png", MediaType: "image/png", SizeBytes: 10, SHA256: strings.Repeat("a", 64),
	}}
	preview, err := Render(template, Input{
		Variables:   map[string]string{"extra.image": "attachment_1"},
		Attachments: attachments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.BodyHTML, "cid:attachment_1") {
		t.Fatalf("html = %q", preview.BodyHTML)
	}
	if preview.Attachments[0].ContentID != "attachment_1" {
		t.Fatalf("content id = %q", preview.Attachments[0].ContentID)
	}

	_, err = Render(template, Input{
		Variables:   map[string]string{"extra.image": "attachment_2"},
		Attachments: attachments,
	})
	requireCode(t, err, domain.MailErrTemplateInvalid)

	_, err = Render(template, Input{Variables: map[string]string{"extra.image": "attachment_1"}})
	requireCode(t, err, domain.MailErrTemplateInvalid)
}

func TestRenderRejectsOverlongSubjectAndBody(t *testing.T) {
	template := Template{SubjectTemplate: "{{test.scope}}", BodyTextTemplate: "{{test.notes}}"}
	_, err := Render(template, Input{Variables: map[string]string{
		"test.scope": strings.Repeat("a", MaxSubjectRunes+1),
		"test.notes": "ok",
	}})
	requireCode(t, err, domain.MailErrTemplateInvalid)

	_, err = Render(template, Input{Variables: map[string]string{
		"test.scope": "ok",
		"test.notes": strings.Repeat("b", MaxBodyTextBytes+1),
	}})
	requireCode(t, err, domain.MailErrTemplateInvalid)

	static := Template{SubjectTemplate: strings.Repeat("s", MaxSubjectRunes+1), BodyTextTemplate: "t"}
	requireCode(t, ValidateTemplate(static), domain.MailErrTemplateInvalid)
}

func TestRenderValidatesRecipients(t *testing.T) {
	template := Template{SubjectTemplate: "s", BodyTextTemplate: "t"}
	for _, recipient := range []string{"not-an-email", "a@example.com\r\nBcc: victim@example.com", "Name <a@example.com>", ""} {
		_, err := Render(template, Input{
			Variables: map[string]string{},
			To:        []string{recipient},
		})
		requireCode(t, err, domain.MailErrRecipientInvalid)
	}
	_, err := Render(template, Input{
		To: []string{"a@example.com"},
		CC: []string{"A@example.com"},
	})
	requireCode(t, err, domain.MailErrRecipientInvalid)
}

func TestHTMLOnlyTemplateGeneratesTextFallback(t *testing.T) {
	template := Template{
		SubjectTemplate:  "s",
		BodyHTMLTemplate: `<p>Hello <strong>QA</strong></p><ul><li>one</li><li>two</li></ul>`,
	}
	preview, err := Render(template, Input{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.BodyText, "Hello QA") ||
		!strings.Contains(preview.BodyText, "- one") ||
		!strings.Contains(preview.BodyText, "- two") {
		t.Fatalf("text fallback = %q", preview.BodyText)
	}
}

func TestExtraVariableLimits(t *testing.T) {
	template := Template{SubjectTemplate: "{{extra.a}}", BodyTextTemplate: "t"}
	_, err := Render(template, Input{Variables: map[string]string{"extra.a": strings.Repeat("x", MaxExtraBytes)}})
	requireCode(t, err, domain.MailErrTemplateInvalid)

	variables := map[string]string{"extra.a": "a"}
	_, err = Render(Template{SubjectTemplate: "s", BodyTextTemplate: "{{extra.a}}"}, Input{Variables: variables})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRenderControlledLinkPlaceholder(t *testing.T) {
	template := Template{
		SubjectTemplate:  "s",
		BodyTextTemplate: "下载：%%MAIL_LINK:attachment_1%%",
		BodyHTMLTemplate: `<p>下载：%%MAIL_LINK:attachment_1%%</p>`,
	}
	preview, err := Render(template, Input{Attachments: []Attachment{{
		SourceType: "task_attachment", SourceID: "attachment_1", FileName: "big.zip",
		MediaType: "application/zip", SizeBytes: MaxDirectAttachmentSize + 1,
		SHA256: strings.Repeat("b", 64), ContentID: "attachment_1",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	want := "%%MAIL_LINK:attachment_1%%"
	if !strings.Contains(preview.BodyText, want) || !strings.Contains(preview.BodyHTML, want) {
		t.Fatalf("link placeholder not preserved: %q / %q", preview.BodyText, preview.BodyHTML)
	}
	attachment := preview.Attachments[0]
	if attachment.DeliveryMode != "link" || attachment.LinkPlaceholder != want {
		t.Fatalf("attachment = %#v", attachment)
	}
}

func TestPlaceholderInHrefIsAllowedAndRendered(t *testing.T) {
	template := Template{
		SubjectTemplate:  "s",
		BodyTextTemplate: "t",
		BodyHTMLTemplate: `<a href="{{test.scope}}">x</a>`,
	}
	if err := ValidateTemplate(template); err != nil {
		t.Fatal(err)
	}
	preview, err := Render(template, Input{Variables: map[string]string{"test.scope": "https://example.com/a"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.BodyHTML, `href="https://example.com/a"`) {
		t.Fatalf("html = %q", preview.BodyHTML)
	}
}
