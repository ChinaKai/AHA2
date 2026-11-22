// Focused checks for the toolbar/channel merge that do not need a browser.
import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const tools = await import(pathToFileURL(resolve(root, "build", "task_tools.js")));
const i18n = await import(pathToFileURL(resolve(root, "build", "i18n.js")));
const {icon} = await import(pathToFileURL(resolve(root, "build", "icons.js")));
const detail = {
  task: {id: "t-1", code: "task-1", title: "T", status: "active", project_id: "p", workspace_id: "w"},
  turns: [],
  agents: [],
  memory: {},
};

test("channel tools share the channel glyph and keep connection state", () => {
  const channel = tools.renderTaskToolButtons("channel", {channelConnected: true}).match(/<button[^>]*data-task-tool="channel"[^>]*>.*?<\/button>/)?.[0] || "";
  assert.ok(channel.includes(icon("channel")));
  assert.ok(!channel.includes(icon("bot")));
  assert.match(channel, /connected/);
});

test("the permissions tool is removed and old links resolve to the channel page", () => {
  assert.equal(tools.normalizePinnedTaskTool("permissions"), "channel");
  assert.equal(tools.normalizePinnedTaskTool("channel"), "channel");
  assert.equal(tools.normalizePinnedTaskTool("nope"), "");
  assert.equal(tools.renderTaskToolContent("permissions", detail, "", "<section>channel</section>"), "<section>channel</section>");
  assert.equal(tools.renderTaskToolContent("channel", detail, "", "<section>channel</section>"), "<section>channel</section>");
});

test("the channel toolbar entry carries the pending permission badge", () => {
  const buttons = tools.renderTaskToolButtons("", {pendingPermissions: 2, pendingMail: 4});
  assert.doesNotMatch(buttons, /data-task-tool="permissions"/);
  const channel = buttons.match(/<button[^>]*data-task-tool="channel"[^>]*>.*?<\/button>/)?.[0] || "";
  assert.match(channel, /has-pending/);
  assert.match(channel, /<span class="task-tool-badge">2<\/span>/);
  assert.match(channel, /2 条待审批/);
  const mail = buttons.match(/<button[^>]*data-task-tool="mail"[^>]*>.*?<\/button>/)?.[0] || "";
  assert.match(mail, /<span class="task-tool-badge">4<\/span>/);
});

test("the channel badge caps at 99+", () => {
  const buttons = tools.renderTaskToolButtons("", {pendingPermissions: 120});
  const channel = buttons.match(/<button[^>]*data-task-tool="channel"[^>]*>.*?<\/button>/)?.[0] || "";
  assert.match(channel, /<span class="task-tool-badge">99\+<\/span>/);
});

test("new channel and approval copy has English translations", () => {
  i18n.applyLocale("en-US");
  const expected = {
    "查看历史": "View history",
    "只看待审批": "View pending only",
    "没有待审批": "Nothing pending",
    "当前没有待审批的权限申请。": "No pending permission requests.",
    "没有已处理的权限申请。": "No processed permission requests.",
    "群聊成员": "Group members",
    "勾选加入联调 · Agent 只会看到这里的人": "Select to join integration · The Agent only sees the people here",
    "同步群成员": "Sync group members",
    "身份": "Role",
    "APP / 云端 / 固件": "APP / Cloud / Firmware",
    "按姓名搜索": "Search by name",
    "没有匹配的成员": "No matching members",
    "申请时间": "Requested",
    "处理人": "Decided by",
    "处理说明": "Decision note",
    "已过期": "Expired",
  };
  for (const [source, translated] of Object.entries(expected)) {
    assert.equal(i18n.tr(source), translated, `missing English for ${source}`);
  }
  // Dynamic counts are translated by the DOM observer's pattern rules; the
  // browser test covers them at runtime.
  i18n.applyLocale("zh-CN");
});
