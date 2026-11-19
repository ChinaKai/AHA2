// Prompt page contract: the legacy template list is the main page, the
// textarea is read-only until the explicit edit mode, preview is disabled while
// editing, and the scenario dialog renders a complete demo snapshot.
import assert from "node:assert/strict";
import test from "node:test";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const modulePath = resolve(import.meta.dirname, "..", "build", "prompt_admin.js");
const promptAdmin = await import(pathToFileURL(modulePath).href);

const templates = [
  {
    id: "role.main", name: "Main Agent", layer: "role", description: "Main role",
    content: "Legacy role body", source: "override", editable: true, required: true, version: 4,
  },
  {
    id: "protocol.attachment-delivery", name: "Attachment Delivery Protocol", layer: "protocol",
    description: "Attachment boundary", content: "Read-only body", source: "builtin",
    editable: false, required: true, version: 1,
  },
];

function preview(overrides = {}) {
  return {
    scenario: "web-task-single-main",
    scenario_name: "Web 普通 Task，单 Agent，主 Agent",
    prompt: "## Core\n<full demo snapshot>\n## Current Inbox Batch\n这是一条测试消息",
    prompt_chars: 2048,
    demo: true,
    test_message: "这是一条测试消息",
    message_source: "owner",
    context_files: [{id: "task", path: "/demo/task.md", chars: 120}],
    ...overrides,
  };
}

function view(overrides = {}) {
  return {
    loaded: true,
    templates,
    selectedTemplateID: "role.main",
    editing: false,
    editContent: "",
    generation: 0,
    switchTargetID: "",
    previewScenario: "web-task-single-main",
    previewMessage: "这是一条测试消息",
    previewSource: "owner",
    preview: null,
    previewError: "",
    previewLoading: false,
    ...overrides,
  };
}

function textareaTag(html) {
  return html.match(/<textarea[^>]*data-prompt-template-content[^>]*>/)?.[0] || "";
}

test("default page is the legacy list with a read-only editor", () => {
  const html = promptAdmin.renderPromptAdmin(view());
  assert.match(html, /data-prompt-template="role\.main"/);
  assert.match(html, /data-prompt-template="protocol\.attachment-delivery"/);
  assert.equal((html.match(/data-prompt-template=/g) || []).length, 2);
  assert.match(textareaTag(html), /readonly/);
  assert.match(html, /Legacy role body/);
  assert.match(html, /data-prompt-edit/);
  assert.match(html, /id="preview-prompt-scenario"/);
  assert.doesNotMatch(html, /id="preview-prompt-scenario"[^>]*disabled/);
  assert.doesNotMatch(html, /data-prompt-save/);
  assert.doesNotMatch(html, /data-prompt-cancel/);
  assert.doesNotMatch(html, /data-prompt-reset/);
  assert.doesNotMatch(html, /data-prompt-preview-hint/);
  assert.match(html, /<span>只读<\/span>/);
});

test("editable drafts only appear in explicit edit mode", () => {
  const html = promptAdmin.renderPromptAdmin(view({editing: true, editContent: "Draft body"}));
  assert.doesNotMatch(textareaTag(html), /readonly/);
  assert.match(html, /Draft body/);
  assert.match(html, /data-prompt-save/);
  assert.match(html, /data-prompt-cancel/);
  assert.match(html, /data-prompt-reset/);
  assert.match(html, /<span>编辑中<\/span>/);
  assert.match(html, /data-prompt-preview-hint>先保存或取消编辑再预览</);
  assert.match(html, /id="preview-prompt-scenario"[^>]*disabled/);
});

test("read-only builtin templates never offer an edit button", () => {
  const html = promptAdmin.renderPromptAdmin(view({selectedTemplateID: "protocol.attachment-delivery"}));
  assert.match(textareaTag(html), /readonly/);
  assert.match(html, /内置只读模板/);
  assert.doesNotMatch(html, /data-prompt-edit/);
  assert.doesNotMatch(html, /data-prompt-save/);
});

test("switching while editing asks save, discard, or cancel", () => {
  const html = promptAdmin.renderPromptAdmin(view({
    editing: true,
    editContent: "Draft body",
    switchTargetID: "protocol.attachment-delivery",
  }));
  assert.match(html, /data-prompt-switch-dialog/);
  assert.match(html, /data-prompt-switch-save/);
  assert.match(html, /data-prompt-switch-discard/);
  assert.match(html, /data-prompt-switch-cancel/);
  assert.match(html, /保存并切换/);
  assert.match(html, /放弃修改并切换/);
  assert.match(html, /取消切换/);
  assert.match(html, /Attachment Delivery Protocol/);
});

test("preview dialog lists all ten stable scenarios and the default message", () => {
  const html = promptAdmin.renderPromptAdmin(view());
  assert.equal((html.match(/data-prompt-scenario-select[\s\S]*?<\/select>/)?.[0].match(/<option /g) || []).length, 10);
  assert.match(html, /Web 宠物 Task，单 Agent，主 Agent/);
  assert.match(html, /渠道群聊，已绑定 Task，多 Agent，主 Agent，群聊规则/);
  assert.match(html, /data-prompt-scenario-message[^>]*>这是一条测试消息<\/textarea>/);
  assert.match(html, /data-prompt-scenario-source/);
  assert.match(html, /data-prompt-scenario-refresh/);
  assert.match(html, /预览只使用已保存模板，不合并未保存草稿/);
});

test("scenario result renders the complete snapshot and char count", () => {
  const html = promptAdmin.renderPromptAdmin(view({preview: preview()}));
  assert.match(html, /data-prompt-scenario-output/);
  assert.match(html, /Web 普通 Task，单 Agent，主 Agent/);
  assert.match(html, /2,048 chars/);
  assert.match(html, /DEMO/);
  assert.match(html, /&lt;full demo snapshot&gt;/);
  assert.match(html, /这是一条测试消息/);
  assert.doesNotMatch(html, /<full demo snapshot>/);
});

test("preview errors replace the snapshot instead of showing partial text", () => {
  const html = promptAdmin.renderPromptAdmin(view({previewError: "预览失败：message too long"}));
  assert.match(html, /预览失败：message too long/);
  assert.doesNotMatch(html, /data-prompt-scenario-output/);
});

test("test message edits stay separate from the template draft", () => {
  const html = promptAdmin.renderPromptAdmin(view({
    editing: true,
    editContent: "TEMPLATE DRAFT",
    previewMessage: "临时测试消息",
    preview: preview({test_message: "临时测试消息"}),
  }));
  assert.match(html, /TEMPLATE DRAFT/);
  assert.match(html, /临时测试消息/);
  // The dialog stays disabled during editing; changing its message is not a
  // template draft and cannot enable preview.
  assert.match(html, /id="preview-prompt-scenario"[^>]*disabled/);
});

test("a delayed save response cannot end another template's edit", () => {
  const editor = promptAdmin.createPromptEditorState();
  promptAdmin.beginPromptEdit(editor, "role.main", "A draft");
  const token = promptAdmin.beginPromptSave(editor);

  // Pause A's response, cancel, switch to B, and start typing there.
  promptAdmin.cancelPromptEdit(editor);
  editor.selectedTemplateID = "protocol.attachment-delivery";
  promptAdmin.beginPromptEdit(editor, "protocol.attachment-delivery", "B draft");
  promptAdmin.updatePromptDraft(editor, "B draft v2");

  // A's late response must not close B's editor or clear B's draft.
  assert.equal(promptAdmin.finishPromptSave(editor, token), false);
  assert.equal(editor.editing, true);
  assert.equal(editor.selectedTemplateID, "protocol.attachment-delivery");
  assert.equal(editor.editContent, "B draft v2");
});

test("typing during an in-flight save keeps the newer draft editable", () => {
  const editor = promptAdmin.createPromptEditorState();
  promptAdmin.beginPromptEdit(editor, "role.main", "A draft");
  const token = promptAdmin.beginPromptSave(editor);
  promptAdmin.updatePromptDraft(editor, "A draft v2");
  assert.equal(promptAdmin.finishPromptSave(editor, token), false);
  assert.equal(editor.editing, true);
  assert.equal(editor.editContent, "A draft v2");
});

test("a save only finishes its own unchanged session", () => {
  const editor = promptAdmin.createPromptEditorState();
  promptAdmin.beginPromptEdit(editor, "role.main", "A draft");
  const token = promptAdmin.beginPromptSave(editor);
  assert.equal(promptAdmin.finishPromptSave(editor, token), true);
  assert.equal(editor.editing, false);
  assert.equal(editor.editContent, "");
});
