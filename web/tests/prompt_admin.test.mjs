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
    message_source_name: "Owner",
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

function sourceFieldTag(html) {
  return html.match(/<label[^>]*data-prompt-scenario-source-field[^>]*>/)?.[0] || "";
}

function sourceSelect(html) {
  return html.match(/<select[^>]*data-prompt-scenario-source[^>]*>[\s\S]*?<\/select>/)?.[0] || "";
}

test("web scenarios offer Owner and pet as visible, selectable sources", () => {
  const html = promptAdmin.renderPromptAdmin(view({previewSource: "pet"}));
  assert.doesNotMatch(sourceFieldTag(html), /\bhidden\b/);
  const select = sourceSelect(html);
  assert.doesNotMatch(select, /\bdisabled\b/);
  assert.match(select, /value="owner"[^>]*>Owner</);
  assert.match(select, /value="pet"[^>]*selected[^>]*>宠物</);
});

test("group scenarios offer channel group member and group bot sources", () => {
  const html = promptAdmin.renderPromptAdmin(view({
    previewScenario: "channel-group-task-single-task-agent",
    previewSource: "channel-group-bot",
  }));
  assert.doesNotMatch(sourceFieldTag(html), /\bhidden\b/);
  const select = sourceSelect(html);
  assert.doesNotMatch(select, /\bdisabled\b/);
  assert.match(select, /value="channel-group-member"[^>]*>渠道群聊成员</);
  assert.match(select, /value="channel-group-bot"[^>]*selected[^>]*>群聊机器人</);
  // Switching back to the same scenario keeps the requested source, while a
  // source from another scenario family falls back to the group member.
  assert.equal(promptAdmin.resolvePreviewSource("channel-group-task-single-task-agent", "channel-group-bot"), "channel-group-bot");
  assert.equal(promptAdmin.resolvePreviewSource("channel-group-task-single-task-agent", "owner"), "channel-group-member");
  assert.equal(promptAdmin.resolvePreviewSource("web-task-single-main", "pet"), "pet");
  assert.equal(promptAdmin.resolvePreviewSource("web-task-single-main", "channel-group-bot"), "owner");
});

test("private channel scenarios keep a fixed Owner source and hide the switch", () => {
  const html = promptAdmin.renderPromptAdmin(view({
    previewScenario: "channel-private-task-single-task-agent",
    previewSource: "owner",
  }));
  assert.match(sourceFieldTag(html), /\bhidden\b/);
  const select = sourceSelect(html);
  assert.match(select, /\bdisabled\b/);
  assert.match(select, /value="channel-owner"[^>]*selected[^>]*>渠道私聊 Owner</);
});

test("scenario result renders the complete snapshot and char count", () => {
  const html = promptAdmin.renderPromptAdmin(view({preview: preview()}));
  assert.match(html, /data-prompt-scenario-output/);
  assert.match(html, /Web 普通 Task，单 Agent，主 Agent/);
  assert.match(html, /web-task-single-main · Owner/);
  assert.match(html, /2,048 chars/);
  assert.match(html, /DEMO/);
  assert.match(html, /&lt;full demo snapshot&gt;/);
  assert.match(html, /这是一条测试消息/);
  assert.doesNotMatch(html, /<full demo snapshot>/);
});

test("group bot preview shows the bot source name and rendered sender header", () => {
  const html = promptAdmin.renderPromptAdmin(view({
    previewScenario: "channel-group-task-single-task-agent",
    previewSource: "channel-group-bot",
    preview: preview({
      scenario: "channel-group-task-single-task-agent",
      scenario_name: "渠道群聊，已绑定 Task，单 Agent，任务 Agent，群聊规则",
      message_source: "channel-group-bot",
      message_source_name: "群聊机器人",
      prompt: "## Current Inbox Batch\n渠道发送者：构建机器人（角色=participant，Bot，非 Owner）\n请检查构建结果",
    }),
  }));
  assert.match(html, /channel-group-task-single-task-agent · 群聊机器人/);
  assert.match(html, /渠道发送者：构建机器人（角色=participant，Bot，非 Owner）/);
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
