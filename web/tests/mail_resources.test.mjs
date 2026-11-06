import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const dist = resolve(import.meta.dirname, "..", "dist");
const cards = await import(pathToFileURL(resolve(dist, "mail_cards.js")));
const resources = await import(pathToFileURL(resolve(dist, "resources.js")));
const mailResources = await import(pathToFileURL(resolve(dist, "mail_resources.js")));
const mailTask = await import(pathToFileURL(resolve(dist, "mail_task.js")));

function message(overrides = {}) {
  return {
    id: "mail_message_1",
    task_id: "task_1",
    account_id: "mail_account_1",
    source: "agent",
    idempotency_key: "key-1",
    from_address: "noreply@example.com",
    to: ["qa@example.com"],
    cc: [],
    bcc: [],
    subject: "提测通知",
    body_text: "正文",
    template_version: 2,
    content_revision: 1,
    approval_digest: "sha256:abcdef1234567890",
    status: "pending_approval",
    created_at: "2026-11-06T09:00:00Z",
    updated_at: "2026-11-06T09:00:00Z",
    ...overrides,
  };
}

test("mail cancel follows the phase-one state rules", () => {
  for (const status of ["pending_approval", "queued", "failed", "dead_letter"]) {
    assert.equal(cards.mailCanCancel(status), true, status);
  }
  for (const status of ["sending", "sent", "rejected", "cancelled", "draft"]) {
    assert.equal(cards.mailCanCancel(status), false, status);
  }
  assert.equal(cards.mailCanRetry("failed"), true);
  assert.equal(cards.mailCanRetry("dead_letter"), true);
  assert.equal(cards.mailCanRetry("queued"), false);
});

test("pending card exposes approve/reject and never renders sending cancel", () => {
  const pending = cards.mailCardHtml({message: message(), attachments: [], deliveries: []});
  assert.match(pending, /data-mail-action="approve"/);
  assert.match(pending, /data-mail-action="reject"/);
  assert.match(pending, /data-mail-action="cancel"/);
  assert.match(pending, /待审批/);
  assert.match(pending, /abcdef123456/);

  const sending = cards.mailCardHtml({
    message: message({status: "sending"}),
    attachments: [],
    deliveries: [{state: "sending", attempts: 1, outcome_certainty: "unknown", updated_at: "2026-11-06T09:00:00Z"}],
  });
  assert.doesNotMatch(sending, /data-mail-action="cancel"/);
  assert.match(sending, /发送中，不可取消/);
});

test("timeline mail card is a status notice without approval actions", () => {
  const html = cards.mailConversationCardHtml({message: message(), attachments: [], deliveries: []}, false);
  assert.match(html, /已提交，等待 Owner 审批/);
  assert.match(html, /任务工具栏「邮件」面板/);
  assert.doesNotMatch(html, /data-mail-action/);
  assert.doesNotMatch(html, /mail-card-actions/);
});

test("older pending mail stays visible and actionable beyond the recent-five cut", () => {
  // Six non-pending messages plus one older pending message: the recent list
  // may cap at five, but the pending list must still expose the only approval.
  const cards = Array.from({length: 7}, (_, index) => ({
    message: message({id: `mail_message_${index + 1}`, subject: `邮件 ${index + 1}`, status: "sent"}),
    attachments: [],
    deliveries: [],
  }));
  cards[6] = {
    message: message({id: "mail_pending_old", subject: "较早的待审批", status: "pending_approval"}),
    attachments: [],
    deliveries: [],
  };
  const detail = {task: {id: "task_1", status: "active"}};
  const html = mailTask.renderTaskMailPanel(detail, {
    accounts: [],
    templates: [],
    messages: cards,
    attachments: [],
    build_artifacts: [],
  }, [], false);

  assert.match(html, /待审批（1）/);
  assert.match(html, /data-mail-card="mail_pending_old"/);
  assert.equal((html.match(/data-mail-action="approve"/g) || []).length, 1, "badge count matches one actionable pending mail");
  assert.ok(html.indexOf("待审批（1）") < html.indexOf("最近邮件"), "pending list is rendered before the recent list");
  assert.match(html, /data-mail-card="mail_message_5"/);
  assert.doesNotMatch(html, /data-mail-card="mail_message_6"/, "non-pending recent mail is still capped at five");
});

test("template editor insert-table skeleton is sanitizer-safe and editable", () => {
  const html = mailResources.mailTemplateTableSkeleton(2, 3);
  assert.match(html, /<table style="border-collapse: collapse; border-spacing: 0; width: 100%;">/);
  assert.match(html, /<thead>/);
  assert.match(html, /<tbody>/);
  assert.equal((html.match(/<th /g) || []).length, 3, "one th per column");
  assert.equal((html.match(/<td /g) || []).length, 6, "rows x columns td cells");
  for (const match of html.matchAll(/style="([^"]*)"/g)) {
    for (const declaration of match[1].split(";").map(value => value.trim()).filter(Boolean)) {
      const [property, ...value] = declaration.split(":");
      assert.ok(["border", "border-collapse", "border-spacing", "padding", "text-align", "vertical-align", "width", "background-color", "font-weight"].includes(property.trim()), property);
      assert.doesNotMatch(value.join(":"), /url\(|expression|@import|!important|position|display|calc\(/i);
    }
  }
  // Skeleton clamps to the same limits the editor validates.
  assert.equal((mailResources.mailTemplateTableSkeleton(0, 0).match(/<td /g) || []).length, 1);
  assert.equal((mailResources.mailTemplateTableSkeleton(999, 99).match(/<td /g) || []).length, 500);
});

test("send preview uses the selected account and renders server HTML in a sandbox", () => {
  const data = mailTask.taskMailPreviewData({
    subject: "构建完成",
    to: ["qa@example.com"],
    cc: ["lead@example.com"],
    body_text: "纯文本回退",
    body_html: "<p>HTML 正文</p>",
    warnings: ["示例警告"],
    attachments: [{
      source_type: "task_attachment", source_id: "att_1", file_name: "log.txt",
      media_type: "text/plain", size_bytes: 12, sha256: "abcdef",
      content_id: "att_1", delivery_mode: "direct",
    }],
  }, {display_name: "AHA 构建通知", from_address: "noreply@example.com"});
  assert.equal(data.title, "发送预览");
  assert.equal(data.fromName, "AHA 构建通知");
  assert.equal(data.fromAddress, "noreply@example.com");
  assert.equal(data.bodyHtml, "<p>HTML 正文</p>");
  assert.equal(data.bodyText, "纯文本回退");
  assert.equal(data.width, 600);
  assert.deepEqual(data.warnings, ["示例警告"]);

  const html = cards.mailPreviewDialogHtml(data);
  assert.match(html, /sandbox=""/, "HTML preview is sandboxed");
  assert.match(html, /srcdoc="[^"]*&lt;p&gt;HTML 正文&lt;\/p&gt;"/, "srcdoc carries the server HTML");
  assert.match(html, /纯文本回退/);
  assert.match(html, /AHA 构建通知/);
  assert.match(html, /noreply@example\.com/);
  assert.match(html, /qa@example\.com/);
  assert.match(html, /log\.txt/);
});

test("message detail preview renders the stored HTML snapshot in a sandbox", () => {
  const snapshot = message({
    from_name: "AHA 构建通知",
    body_text: "审批纯文本",
    body_html: "<p>审批 HTML 正文</p>",
  });
  const data = mailResources.messagePreviewData(snapshot, [{
    id: "mail_attachment_1", message_id: snapshot.id, position: 0,
    source_type: "task_attachment", source_id: "att_1", file_name: "log.txt",
    media_type: "text/plain", size_bytes: 12, sha256: "abcdef",
    delivery_mode: "direct", disposition: "attachment", created_at: "2026-11-06T09:00:00Z",
  }]);
  assert.equal(data.subject, snapshot.subject);
  assert.equal(data.fromAddress, snapshot.from_address);
  assert.equal(data.bodyHtml, snapshot.body_html);
  assert.equal(data.width, 600);

  const html = cards.mailPreviewDialogHtml(data);
  assert.match(html, /sandbox=""/);
  assert.match(html, /srcdoc="[^"]*&lt;p&gt;审批 HTML 正文&lt;\/p&gt;"/);
  assert.match(html, /审批纯文本/);
  assert.match(html, /log\.txt/);
});

test("task compose offers no-template direct fields and builds a direct payload", async () => {
  const source = await readFile(resolve(dist, "mail_task.js"), "utf8");
  assert.match(source, /不使用模板/);
  assert.match(source, /mail-compose-direct/);
  assert.match(source, /正文（HTML 主编辑区）/);

  const common = ["attachment_1"];
  const artifacts = ["artifact_1"];
  const direct = mailTask.mailComposePayload({
    account_id: "mail_account_1", template_id: "  ", subject: "  直发主题  ",
    body_html: "<p>直发正文</p>", to: "qa@example.com, lead@example.com", cc: "", bcc: "",
  }, "task_1", {}, common, artifacts);
  assert.equal(direct.template_id, "");
  assert.equal(direct.subject, "直发主题");
  assert.equal(direct.body_html, "<p>直发正文</p>");
  assert.equal(direct.body_text, "");
  assert.deepEqual(direct.to, ["qa@example.com", "lead@example.com"]);
  assert.deepEqual(direct.attachment_ids, common);
  assert.deepEqual(direct.build_artifact_ids, artifacts);

  const templated = mailTask.mailComposePayload({
    account_id: "mail_account_1", template_id: "mail_template_1",
    subject: "ignored", body_html: "<p>ignored</p>", to: "qa@example.com",
  }, "task_1", {build_version: "1"}, [], []);
  assert.equal(templated.template_id, "mail_template_1");
  assert.ok(!("subject" in templated), "template path never sends direct subject");
  assert.ok(!("body_html" in templated), "template path never sends direct body");
});

test("template editor derives required variables and has no scope or project field", () => {
  const variables = cards.mailTemplateVariables(
    "{{build.version}} {{extra.build_no}}",
    "{{task.title}} {{test.scope}}",
    "<p>{{release.date}}</p>",
  );
  assert.deepEqual(variables, ["build.version", "extra.build_no", "release.date", "test.scope"]);

  const payload = mailResources.mailTemplateSubmitPayload({
    name: "提测通知",
    subject_template: "{{task.title}}",
    body_text_template: "",
    body_html_template: "<p>{{build.version}}</p>",
    default_to: "qa@example.com, lead@example.com",
    default_cc: "",
    required_variables: "release.date",
  });
  assert.equal(payload.scope, "global");
  assert.equal(payload.name, "提测通知");
  assert.deepEqual(payload.required_variables, ["build.version"]);
  assert.deepEqual(payload.default_to, ["qa@example.com", "lead@example.com"]);
  assert.ok(!("project_id" in payload), "the editor no longer submits a project");
  assert.ok(!("description" in payload));

  const form = mailResources.templateForm();
  assert.doesNotMatch(form, /name="scope"/, "scope is not a user-editable field");
  assert.doesNotMatch(form, /name="project_id"/, "project is not a user-editable field");
  assert.doesNotMatch(form, /name="required_variables"/, "required variables are scanned, not typed");
  assert.match(form, /name="body_html_template"/);
  assert.match(form, /name="body_text_template"/);
  assert.match(form, /高级：默认 CC 与自定义纯文本/);
  assert.match(form, /data-mail-template-preview-form/);

  const legacy = mailResources.templateForm({
    id: "mail_template_legacy", scope: "project", project_id: "project_1", name: "旧模板",
    subject_template: "s", body_text_template: "", body_html_template: "", default_to: [], default_cc: [],
    required_variables: [], current_version: 1, status: "active", created_at: "", updated_at: "",
  });
  assert.match(legacy, /旧项目模板/);
  assert.doesNotMatch(legacy, /name="scope"/);
});

test("preview samples cover documented variables and the dialog exposes width, tabs and envelope", () => {
  const names = ["task.id", "task.title", "task.url", "project.name", "workspace.name", "build.version", "test.scope", "release.date", "extra.note"];
  const samples = cards.mailPreviewSamples(names);
  for (const name of names) assert.ok(samples[name], `${name} has a sample value`);
  assert.equal(samples["extra.note"], "示例-extra.note");

  const payload = cards.mailPreviewVariablePayload(names, samples);
  assert.equal(payload.sample_variables["task.title"], samples["task.title"]);
  assert.equal(payload.sample_variables["workspace.name"], samples["workspace.name"]);
  assert.equal(payload.variables["build.version"], samples["build.version"]);
  assert.equal(payload.variables["release.date"], samples["release.date"]);
  assert.deepEqual(payload.warnings, [], "built-in covered names need no warning");

  const unknown = cards.mailPreviewVariablePayload(["extra.unknown"]);
  assert.deepEqual(unknown.warnings, ["缺少示例变量：extra.unknown"]);
  assert.equal(unknown.variables["extra.unknown"], "示例-extra.unknown");

  const html = cards.mailPreviewDialogHtml({
    subject: "构建完成",
    fromName: "AHA",
    fromAddress: "noreply@example.com",
    to: ["qa@example.com"],
    cc: ["cc@example.com"],
    bodyText: "纯文本正文",
    bodyHtml: "<p>HTML 正文</p>",
    attachments: [],
    warnings: ["缺少示例变量：extra.unknown"],
    samples,
    onSampleRefresh: async () => ({subject: "x"}),
  });
  assert.match(html, /data-mail-preview-width="600"/);
  assert.match(html, /data-mail-preview-width="375"/);
  assert.match(html, /data-mail-preview-tab="html"/);
  assert.match(html, /data-mail-preview-tab="text"/);
  assert.match(html, /sandbox=""/, "HTML preview is sandboxed");
  assert.match(html, /noreply@example\.com/);
  assert.match(html, /qa@example\.com/);
  assert.match(html, /cc@example\.com/);
  assert.match(html, /缺少示例变量：extra\.unknown/);
  assert.match(html, /Outlook/);
  assert.match(html, /纯文本正文/);
  assert.match(html, /data-mail-sample-input="task\.id"/);
  assert.match(html, /data-mail-preview-refresh/);

  const empty = cards.mailPreviewDialogHtml({subject: "空正文", bodyText: "", bodyHtml: ""});
  assert.match(empty, /邮件没有 HTML 正文/);
  assert.match(empty, /邮件没有纯文本正文/);
});

test("pending card opens an approval preview with the snapshot and decision buttons", () => {
  const card = {
    message: message({body_text: "审批纯文本", body_html: "<p>审批 HTML 正文</p>", bcc: ["secret@example.com"]}),
    attachments: [{id: "mail_attachment_1", message_id: "mail_message_1", position: 0, source_type: "task_attachment", source_id: "att_1", file_name: "log.txt", media_type: "text/plain", size_bytes: 12, sha256: "abcdef", delivery_mode: "direct", disposition: "attachment", created_at: "2026-11-06T09:00:00Z"}],
    deliveries: [],
  };
  const cardHTML = cards.mailCardHtml(card);
  assert.match(cardHTML, /data-mail-action="review"/);
  assert.match(cardHTML, /查看并审批/);

  const dialog = cards.mailPreviewDialogHtml({
    title: "查看并审批",
    subject: card.message.subject,
    fromName: card.message.from_name,
    fromAddress: card.message.from_address,
    to: card.message.to,
    cc: card.message.cc,
    bcc: card.message.bcc,
    bodyText: card.message.body_text,
    bodyHtml: card.message.body_html,
    attachments: card.attachments,
    onApprove: async () => true,
    onReject: async () => true,
  });
  assert.match(dialog, /审批 HTML 正文/);
  assert.match(dialog, /审批纯文本/);
  assert.match(dialog, /data-mail-preview-decision="approve"/);
  assert.match(dialog, /data-mail-preview-decision="reject"/);
  assert.match(dialog, /log\.txt/);
  assert.match(dialog, /BCC/);
  assert.match(dialog, /sandbox=""/);
});

test("binding form grid prevents right-edge clipping and wraps below 560px", async () => {
  const css = await readFile(resolve(dist, "styles.css"), "utf8");
  const formRule = css.match(/\.task-mail-binding-form\s*\{([^}]*)\}/);
  assert.ok(formRule, "binding form rule exists");
  assert.match(formRule[1], /grid-template-columns:\s*minmax\(0,\s*1fr\)\s+minmax\(0,\s*1fr\)\s+auto/);
  const labelRule = css.match(/\.task-mail-binding-form label\s*\{([^}]*)\}/);
  assert.match(labelRule[1], /min-width:\s*0/);
  const selectRule = css.match(/\.task-mail-binding-form select\s*\{([^}]*)\}/);
  assert.match(selectRule[1], /min-width:\s*0/);
  assert.match(selectRule[1], /text-overflow:\s*ellipsis/);
  const buttonRule = css.match(/\.task-mail-binding-form button\s*\{([^}]*)\}/);
  assert.match(buttonRule[1], /white-space:\s*nowrap/);

  const narrow = css.match(/@media \(max-width: 560px\) \{([\s\S]*?)\n\}/);
  assert.ok(narrow, "560px media query exists");
  assert.match(narrow[1], /\.task-mail-binding-form\s*\{\s*grid-template-columns:\s*minmax\(0,\s*1fr\)/);
  assert.match(narrow[1], /\.task-mail-binding-form button\s*\{\s*width:\s*100%/);
});

test("template list hides archived templates until the toggle and badges legacy project scope", () => {
  const template = (overrides = {}) => ({
    id: "mail_template_active", scope: "global", project_id: "", name: "启用模板",
    subject_template: "s", body_text_template: "t", body_html_template: "", default_to: [], default_cc: [],
    required_variables: ["build.version"], current_version: 1, status: "active",
    created_at: "2026-11-06T09:00:00Z", updated_at: "2026-11-06T09:00:00Z", ...overrides,
  });
  const data = showArchived => ({
    subTab: "templates", accounts: [], messages: [], projects: [{id: "project_1", name: "AHA2"}],
    templates: [
      template(),
      template({id: "mail_template_legacy", scope: "project", project_id: "project_1", name: "旧项目", required_variables: []}),
      template({id: "mail_template_archived", name: "归档模板", status: "archived"}),
    ],
    filterStatus: "", filterAccount: "", showArchived, loading: false, error: "",
  });
  const daily = mailResources.renderMailResources(data(false));
  assert.match(daily, /显示归档模板/);
  assert.match(daily, /data-mail-template="mail_template_active"/);
  assert.match(daily, /data-mail-template="mail_template_legacy"/);
  assert.match(daily, /旧项目模板/);
  assert.doesNotMatch(daily, /data-mail-template="mail_template_archived"/, "archived templates stay out of the daily list");
  assert.doesNotMatch(daily, /已归档模板（/);

  const withArchived = mailResources.renderMailResources(data(true));
  assert.match(withArchived, /已归档模板（1）/);
  assert.match(withArchived, /data-mail-template="mail_template_archived"/);
  assert.ok(withArchived.indexOf("已归档模板（1）") > withArchived.indexOf("data-mail-template=\"mail_template_active\""), "archived section is separate and after active templates");
  assert.match(withArchived, /data-mail-template-purge="mail_template_active"/);
  assert.match(withArchived, /data-mail-template-purge="mail_template_archived"/);
});

test("resources page keeps hardware and mail in one eight-item navigation shell", () => {
  const hardware = resources.renderResourcesPage({
    tab: "hardware",
    hardware: [],
    mail: {subTab: "accounts", accounts: [], templates: [], messages: [], projects: [], filterStatus: "", filterAccount: "", showArchived: false, loading: false, error: ""},
  });
  assert.match(hardware, /class="resources-view"/);
  assert.match(hardware, /data-resources-tab="hardware"/);
  assert.match(hardware, /data-resources-tab="mail"/);
  assert.match(hardware, /hardware-resources-view/);
  assert.match(hardware, /资源/);

  const mail = resources.renderResourcesPage({
    tab: "mail",
    hardware: [],
    mail: {subTab: "accounts", accounts: [], templates: [], messages: [], projects: [], filterStatus: "", filterAccount: "", showArchived: false, loading: false, error: ""},
  });
  assert.match(mail, /mail-resources-view/);
  assert.match(mail, /data-mail-subtab="accounts"/);
  assert.match(mail, /添加账号/);
  assert.doesNotMatch(mail, /credential_ref/);
});
