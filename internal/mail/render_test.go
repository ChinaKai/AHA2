package mail

import (
	"fmt"
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

func baseCallerVariables() map[string]string {
	return map[string]string{"build.version": "v1.2.3"}
}

func baseServerVariables() map[string]string {
	return map[string]string{
		"project.name": "Example",
		"task.id":      "task_1",
		"task.title":   "OTA 回归",
	}
}

func baseInput() Input {
	return Input{Variables: baseCallerVariables(), ServerVariables: baseServerVariables()}
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
	input := baseInput()
	input.To = []string{"qa-team@example.com"}
	input.CC = []string{"release-manager@example.com"}
	preview, err := Render(baseTemplate(), input)
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
	preview, err := Render(template, Input{ServerVariables: map[string]string{
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
	_, err := Render(template, baseInput())
	requireCode(t, err, domain.MailErrTemplateVariableUnknown)

	template = baseTemplate()
	template.BodyTextTemplate = "{{task.title"
	_, err = Render(template, baseInput())
	requireCode(t, err, domain.MailErrTemplateInvalid)

	template = baseTemplate()
	template.BodyTextTemplate = "{{ task.title }}"
	_, err = Render(template, baseInput())
	requireCode(t, err, domain.MailErrTemplateInvalid)
}

func TestRenderRejectsMissingVariables(t *testing.T) {
	input := baseInput()
	delete(input.ServerVariables, "task.id")
	_, err := Render(baseTemplate(), input)
	requireCode(t, err, domain.MailErrTemplateVariableMissing)

	input = baseInput()
	delete(input.ServerVariables, "project.name")
	_, err = Render(baseTemplate(), input)
	requireCode(t, err, domain.MailErrTemplateVariableMissing)
}

func TestRenderRejectsCallerSuppliedServerVariables(t *testing.T) {
	template := Template{SubjectTemplate: "s", BodyTextTemplate: "t"}
	for _, name := range []string{"task.id", "project.name", "workspace.name", "agent.name", "owner.name", "package.sha256"} {
		_, err := Render(template, Input{Variables: map[string]string{name: "forged"}})
		requireCode(t, err, domain.MailErrForbidden)
	}
	// Caller-controlled namespaces remain accepted.
	for _, name := range []string{"build.version", "test.scope", "release.version", "extra.note"} {
		if _, err := Render(template, Input{Variables: map[string]string{name: "ok"}}); err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
	}
}

func TestRenderRejectsVariableLinkPlaceholder(t *testing.T) {
	template := Template{SubjectTemplate: "s", BodyTextTemplate: "{{extra.note}}"}
	_, err := Render(template, Input{Variables: map[string]string{"extra.note": "%%MAIL_LINK:attachment_1%%"}})
	requireCode(t, err, domain.MailErrTemplateInvalid)
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
		`<p>x<!--[if mso]><img src="https://tracker.example.com/p.gif"><![endif]--></p>`,
		`<![CDATA[<img src="https://tracker.example.com/p.gif">]]>`,
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

func TestURLValidationRejectsAmbiguousForms(t *testing.T) {
	tests := []string{
		`https:evil.example`,
		`https://good.example.com@evil.example/`,
		`https://exam​ple.com/`,
		`https://пример.example/`,
		`mailto:a@example.com?bcc=victim@example.com`,
		`mailto:a@example.com?subject=x`,
		`mailto://a@example.com`,
		`https://`,
		`https:///path`,
	}
	for _, raw := range tests {
		template := Template{SubjectTemplate: "s", BodyTextTemplate: "t", BodyHTMLTemplate: `<a href="` + raw + `">x</a>`}
		requireCode(t, ValidateTemplate(template), domain.MailErrTemplateInvalid)
	}
	valid := Template{
		SubjectTemplate: "s", BodyTextTemplate: "t",
		BodyHTMLTemplate: `<a href="https://example.com/a">x</a><a href="mailto:qa@example.com">m</a>`,
	}
	if err := ValidateTemplate(valid); err != nil {
		t.Fatalf("valid URLs rejected: %v", err)
	}
}

func TestFinalSanitizeAllowsBraceTextFromVariables(t *testing.T) {
	template := Template{
		SubjectTemplate:  "{{test.scope}}",
		BodyTextTemplate: "{{test.scope}}",
		BodyHTMLTemplate: `<p>{{test.scope}}</p>`,
	}
	preview, err := Render(template, Input{Variables: map[string]string{"test.scope": "literal {{not-a-variable}}"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.BodyText, "{{not-a-variable}}") ||
		!strings.Contains(preview.BodyHTML, "{{not-a-variable}}") {
		t.Fatalf("braces were consumed: %q / %q", preview.BodyText, preview.BodyHTML)
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
	_, err = Render(template, Input{ServerVariables: map[string]string{"task.id": `"><img src=x onerror=alert(1)>`}})
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

func TestTableInlineStyleAllowlist(t *testing.T) {
	template := Template{
		SubjectTemplate: "s",
		BodyHTMLTemplate: `<table style="border-collapse:collapse; border-spacing:0; width:100%;">` +
			`<thead><tr style="background-color: #f6f8fa;">` +
			`<th colspan="2" rowspan="1" style="border: 1px solid #d0d7de; padding: 6px 8px; text-align: left; vertical-align: middle;">列</th>` +
			`</tr></thead><tbody><tr>` +
			`<td style="border-bottom: 1px solid #d0d7de; background-color: #ffffff; color: #111111; font-weight: 700; white-space: nowrap;">值</td>` +
			`</tr></tbody></table>`,
	}
	if err := ValidateTemplate(template); err != nil {
		t.Fatalf("safe table style was rejected: %v", err)
	}
	preview, err := Render(template, Input{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		`border-collapse:collapse`, `border-spacing:0`, `width:100%`,
		`background-color: #f6f8fa`, `text-align: left`, `vertical-align: middle`,
		`font-weight: 700`, `colspan="2"`, `rowspan="1"`,
	} {
		if !strings.Contains(preview.BodyHTML, want) {
			t.Fatalf("rendered HTML missing %q: %s", want, preview.BodyHTML)
		}
	}
}

func TestTableStyleRejectsDangerousDeclarations(t *testing.T) {
	tests := []string{
		`<table style="position: fixed">x</table>`,
		`<table style="display: block">x</table>`,
		`<table style="width: expression(alert(1))">x</table>`,
		`<table style="width: 100% !important">x</table>`,
		`<table style="width: 100% ! important">x</table>`,
		`<table style="width: 100% !IMPORTANT">x</table>`,
		`<table style="width: 100% ! Important">x</table>`,
		`<table style="border: 1px solid red ! important">x</table>`,
		`<table style="@import url(https://evil.example/x.css)">x</table>`,
		`<table style="background-color: url(https://evil.example/pixel)">x</table>`,
		`<table style="background-color: javascript:alert(1)">x</table>`,
		`<table style="width: calc(100% - 10px)">x</table>`,
		`<table style="transform: rotate(1deg)">x</table>`,
		`<table style="border: 1px solid red /* comment */">x</table>`,
		`<table><tr><td style="border: 1px solid red; display: flex">x</td></tr></table>`,
		`<p style="border: 1px solid red">x</p>`,
		`<table><caption style="text-align: left">x</caption></table>`,
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

func TestHTMLTableTextFallback(t *testing.T) {
	template := Template{
		SubjectTemplate: "s",
		BodyHTMLTemplate: `<table><thead><tr><th>项目</th><th>状态</th></tr></thead>` +
			`<tbody><tr><td>构建</td><td>通过</td></tr><tr><td>测试</td><td>通过</td></tr></tbody></table>`,
	}
	preview, err := Render(template, Input{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "项目\t状态\n构建\t通过\n测试\t通过"; preview.BodyText != want {
		t.Fatalf("table text fallback = %q, want %q", preview.BodyText, want)
	}
	if strings.Contains(preview.BodyText, "<table") || strings.Contains(preview.BodyText, "<td") {
		t.Fatalf("table fallback leaked HTML tags: %q", preview.BodyText)
	}

	explicit := template
	explicit.BodyTextTemplate = "自定义纯文本"
	preview, err = Render(explicit, Input{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.BodyText != "自定义纯文本" {
		t.Fatalf("explicit text body was replaced: %q", preview.BodyText)
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

func testAttachment(index int, size int64) Attachment {
	sourceID := fmt.Sprintf("attachment_%d", index)
	return Attachment{
		SourceType: "task_attachment", SourceID: sourceID, FileName: fmt.Sprintf("file_%d.bin", index),
		MediaType: "application/octet-stream", SizeBytes: size,
		SHA256: strings.Repeat("a", 64), ContentID: sourceID,
	}
}

func TestAttachmentSizePolicy(t *testing.T) {
	template := Template{SubjectTemplate: "s", BodyTextTemplate: "t"}

	// Exactly 50 MiB for one file is allowed and stays direct.
	exactFile, err := Render(template, Input{Attachments: []Attachment{testAttachment(1, MaxAttachmentBytes)}})
	if err != nil {
		t.Fatalf("exact per-file limit rejected: %v", err)
	}
	if exactFile.Attachments[0].DeliveryMode != "direct" || exactFile.Attachments[0].LinkPlaceholder != "" {
		t.Fatalf("exact per-file attachment = %#v", exactFile.Attachments[0])
	}

	// Exactly 100 MiB in total (10 x 10 MiB) is allowed and stays direct.
	exactTotal := make([]Attachment, 0, MaxAttachments)
	for index := 0; index < MaxAttachments; index++ {
		exactTotal = append(exactTotal, testAttachment(index, 10<<20))
	}
	preview, err := Render(template, Input{Attachments: exactTotal})
	if err != nil {
		t.Fatalf("exact total limit rejected: %v", err)
	}
	if len(preview.Attachments) != MaxAttachments {
		t.Fatalf("attachments = %d", len(preview.Attachments))
	}
	for _, attachment := range preview.Attachments {
		if attachment.DeliveryMode != "direct" || attachment.LinkPlaceholder != "" {
			t.Fatalf("attachment inside limits = %#v", attachment)
		}
	}

	// One byte over the per-file limit is rejected.
	_, err = Render(template, Input{Attachments: []Attachment{testAttachment(1, MaxAttachmentBytes+1)}})
	requireCode(t, err, domain.MailErrAttachmentTooLarge)

	// One byte over the total limit is rejected even when every file fits.
	overTotal := make([]Attachment, 0, 10)
	for index := 0; index < 9; index++ {
		overTotal = append(overTotal, testAttachment(index, 10<<20))
	}
	overTotal = append(overTotal, testAttachment(9, (10<<20)+1))
	_, err = Render(template, Input{Attachments: overTotal})
	requireCode(t, err, domain.MailErrTotalTooLarge)

	// The attachment count limit is independent of size.
	tooMany := make([]Attachment, 0, MaxAttachments+1)
	for index := 0; index <= MaxAttachments; index++ {
		tooMany = append(tooMany, testAttachment(index, 1))
	}
	_, err = Render(template, Input{Attachments: tooMany})
	requireCode(t, err, domain.MailErrAttachmentTooLarge)
}

func TestDirectAttachmentCanBeCIDReferencedAtCurrentLimit(t *testing.T) {
	template := Template{
		SubjectTemplate:  "s",
		BodyTextTemplate: "t",
		BodyHTMLTemplate: `<img src="cid:attachment_1">`,
	}
	attachment := Attachment{
		SourceType: "task_attachment", SourceID: "attachment_1", FileName: "big.zip",
		MediaType: "application/zip", SizeBytes: MaxAttachmentBytes,
		SHA256: strings.Repeat("c", 64), ContentID: "attachment_1",
	}
	preview, err := Render(template, Input{Attachments: []Attachment{attachment}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Attachments[0].DeliveryMode != "direct" ||
		!strings.Contains(preview.BodyHTML, "cid:attachment_1") {
		t.Fatalf("attachment = %#v html = %q", preview.Attachments[0], preview.BodyHTML)
	}
}

func TestDuplicateAttachmentSourcesAreDeduped(t *testing.T) {
	template := Template{SubjectTemplate: "s", BodyTextTemplate: "t"}
	attachment := Attachment{
		SourceType: "task_attachment", SourceID: "attachment_1", FileName: "a.bin",
		MediaType: "application/octet-stream", SizeBytes: 10,
		SHA256: strings.Repeat("d", 64), ContentID: "attachment_1",
	}
	artifact := attachment
	artifact.SourceType = "build_artifact"
	artifact.SourceID = "build_artifact_1"
	preview, err := Render(template, Input{Attachments: []Attachment{attachment, artifact}})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Attachments) != 1 || preview.Attachments[0].SourceType != "task_attachment" {
		t.Fatalf("attachments = %#v", preview.Attachments)
	}
}

func TestRenderNeverChoosesLinkMode(t *testing.T) {
	template := Template{
		SubjectTemplate:  "s",
		BodyTextTemplate: "下载：%%MAIL_LINK:attachment_1%%",
		BodyHTMLTemplate: `<p>下载：%%MAIL_LINK:attachment_1%%</p>`,
	}
	preview, err := Render(template, Input{Attachments: []Attachment{{
		SourceType: "task_attachment", SourceID: "attachment_1", FileName: "big.zip",
		MediaType: "application/zip", SizeBytes: MaxAttachmentBytes,
		SHA256: strings.Repeat("b", 64), ContentID: "attachment_1",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	attachment := preview.Attachments[0]
	if attachment.DeliveryMode != "direct" || attachment.LinkPlaceholder != "" {
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

func guideTemplate() Template {
	return Template{
		ID:      "mail_template_guide",
		Version: 4,
		Mode:    domain.MailTemplateModeGuide,
		// The example deliberately contains placeholder-looking text: guide
		// templates are literal samples and never substitute variables.
		SubjectTemplate:   "[AHA] {{task.title}} 提测通知（参考）",
		BodyTextTemplate:  "这是参考正文，保留 {{task.title}} 原样。",
		BodyHTMLTemplate:  `<p>这是参考 HTML，保留 {{task.title}} 原样。</p>`,
		RequiredVariables: []string{"task.title"},
	}
}

func TestGuideRenderUsesCallerContentAndSanitizes(t *testing.T) {
	template := guideTemplate()
	if err := ValidateTemplate(template); err != nil {
		t.Fatalf("guide template validation: %v", err)
	}
	preview, err := Render(template, Input{
		Subject:  "最终主题：v1.0.0",
		BodyText: "最终纯文本正文",
		BodyHTML: `<p onclick="alert(1)">最终 HTML 正文</p>`,
	})
	if err == nil {
		t.Fatalf("unsafe guide HTML was accepted: %#v", preview)
	}
	requireCode(t, err, domain.MailErrTemplateInvalid)

	preview, err = Render(template, Input{
		Subject:  "最终主题：v1.0.0",
		BodyHTML: `<p>最终 <strong>HTML</strong> 正文</p>`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.TemplateID != template.ID || preview.TemplateVersion != template.Version ||
		preview.TemplateMode != domain.MailTemplateModeGuide {
		t.Fatalf("reference template metadata = %#v", preview)
	}
	if preview.Subject != "最终主题：v1.0.0" {
		t.Fatalf("subject = %q", preview.Subject)
	}
	if strings.Contains(preview.BodyHTML, "{{") || !strings.Contains(preview.BodyHTML, "<strong>HTML</strong>") {
		t.Fatalf("html = %q", preview.BodyHTML)
	}
	// The text fallback is derived from the caller's sanitized HTML, never the
	// template example.
	if !strings.Contains(preview.BodyText, "最终 HTML 正文") || strings.Contains(preview.BodyText, "参考正文") {
		t.Fatalf("text = %q", preview.BodyText)
	}
}

func TestGuideRenderRequiresCallerContent(t *testing.T) {
	template := guideTemplate()
	_, err := Render(template, Input{BodyHTML: "<p>x</p>"})
	requireCode(t, err, domain.MailErrTemplateContentMissing)
	_, err = Render(template, Input{Subject: "only subject"})
	requireCode(t, err, domain.MailErrTemplateContentMissing)
	_, err = Render(template, Input{Subject: "s", BodyHTML: "   "})
	requireCode(t, err, domain.MailErrTemplateContentMissing)
}

func TestGuideRenderRejectsUnresolvedPlaceholders(t *testing.T) {
	template := guideTemplate()
	for name, input := range map[string]Input{
		"subject":   {Subject: "{{task.title}}", BodyHTML: "<p>x</p>"},
		"body_text": {Subject: "s", BodyText: "还有 {{build.version}}", BodyHTML: "<p>x</p>"},
		"body_html": {Subject: "s", BodyHTML: "<p>{{package.name}}</p>"},
		"closing":   {Subject: "s", BodyHTML: "<p>orphan }}</p>"},
	} {
		_, err := Render(template, input)
		if err == nil {
			t.Fatalf("%s: unresolved placeholder was accepted", name)
		}
		requireCode(t, err, domain.MailErrTemplatePlaceholderUnresolved)
	}
}

func TestGuidePreviewExampleShowsLiteralSample(t *testing.T) {
	template := guideTemplate()
	preview, err := PreviewGuideExample(template, Input{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.TemplateMode != domain.MailTemplateModeGuide {
		t.Fatalf("mode = %q", preview.TemplateMode)
	}
	if !strings.Contains(preview.Subject, "{{task.title}}") ||
		!strings.Contains(preview.BodyHTML, "{{task.title}}") {
		t.Fatalf("example was substituted: %#v", preview)
	}
}
