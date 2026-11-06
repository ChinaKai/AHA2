import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const dist = resolve(import.meta.dirname, "..", "dist");
const cards = await import(pathToFileURL(resolve(dist, "mail_cards.js")));
const resources = await import(pathToFileURL(resolve(dist, "resources.js")));
const mailResources = await import(pathToFileURL(resolve(dist, "mail_resources.js")));

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

test("resources page keeps hardware and mail in one eight-item navigation shell", () => {
  const hardware = resources.renderResourcesPage({
    tab: "hardware",
    hardware: [],
    mail: {subTab: "accounts", accounts: [], templates: [], messages: [], projects: [], filterStatus: "", filterAccount: "", loading: false, error: ""},
  });
  assert.match(hardware, /class="resources-view"/);
  assert.match(hardware, /data-resources-tab="hardware"/);
  assert.match(hardware, /data-resources-tab="mail"/);
  assert.match(hardware, /hardware-resources-view/);
  assert.match(hardware, /资源/);

  const mail = resources.renderResourcesPage({
    tab: "mail",
    hardware: [],
    mail: {subTab: "accounts", accounts: [], templates: [], messages: [], projects: [], filterStatus: "", filterAccount: "", loading: false, error: ""},
  });
  assert.match(mail, /mail-resources-view/);
  assert.match(mail, /data-mail-subtab="accounts"/);
  assert.match(mail, /添加账号/);
  assert.doesNotMatch(mail, /credential_ref/);
});
