// Phase 3b prompt page contract: only the two entries are top-level, anchors
// stay inside their entry, generated/resource anchors are preview-only, and the
// legacy ID editor remains available as a separate tab.
import assert from "node:assert/strict";
import test from "node:test";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const modulePath = resolve(import.meta.dirname, "..", "build", "prompt_admin.js");
const promptAdmin = await import(pathToFileURL(modulePath).href);

const main = {
  entry_id: "core", key: "main", legacy_id: "core.default", name: "AHA Core",
  description: "Core boundary", layer: "core", kind: "resident", required: true,
  editable: true, protected: false, dynamic: false, frozen_version: 2, version: 2,
  source: "builtin", order: 0, heading: true, condition: "always",
};
const attachment = {
  entry_id: "core", key: "attachment", legacy_id: "protocol.attachment-delivery",
  name: "Attachment Delivery Protocol", description: "Attachment boundary",
  layer: "protocol", kind: "resident", required: true, editable: false, protected: true,
  dynamic: false, frozen_version: 1, version: 1, source: "builtin", order: 1, heading: true,
  condition: "always",
};
const resource = {
  entry_id: "core", key: "resource-agent-api", legacy_id: "resource.agent-api",
  name: "Agent API Reference", description: "Read-only reference", layer: "resource",
  kind: "resource", required: true, editable: false, protected: true, dynamic: false,
  frozen_version: 11, version: 11, source: "builtin", order: 200, heading: false,
};
const generated = {
  entry_id: "task-context", key: "task", legacy_id: "context.task", name: "Task Context File",
  description: "Generated file", layer: "context", kind: "generated", required: true,
  editable: false, protected: true, dynamic: true, frozen_version: 1, version: 1,
  source: "builtin", order: 100, heading: false,
};
const roleMain = {
  entry_id: "core", key: "role-main", legacy_id: "role.main", name: "Main Agent",
  description: "Main role", layer: "role", kind: "resident", required: true,
  editable: true, protected: false, dynamic: false, frozen_version: 3, version: 3,
  source: "builtin", order: 2, heading: true, condition: "role:main",
};

const catalog = [
  {id: "core", name: "AHA 核心入口", description: "安全、身份、角色、渠道与边界", anchors: [main, roleMain, attachment, resource]},
  {id: "task-context", name: "任务上下文入口", description: "策略、Inbox 与生成文件", anchors: [generated]},
];

const templates = [
  {
    id: "role.main", name: "Main Agent", layer: "role", description: "Main role",
    content: "Legacy role body", source: "builtin", editable: true, required: true, version: 3,
  },
  {
    id: "protocol.attachment-delivery", name: "Attachment Delivery Protocol", layer: "protocol",
    description: "Attachment boundary", content: "Read-only body", source: "builtin",
    editable: false, required: true, version: 1,
  },
];

function preview(anchor, overrides = {}) {
  return {
    entry_id: anchor.entry_id, key: anchor.key, legacy_id: anchor.legacy_id, name: anchor.name,
    description: anchor.description, layer: anchor.layer, kind: anchor.kind, required: anchor.required, editable: anchor.editable,
    protected: anchor.protected, dynamic: anchor.dynamic, frozen_version: anchor.frozen_version,
    version: anchor.version, source: anchor.source,
    content: `${anchor.legacy_id} source`, rendered: `${anchor.legacy_id} rendered`,
    legacy_override: false, ...overrides,
  };
}

const mappings = [
  {legacy_id: "core.default", entry_id: "core", anchor_key: "main", kind: "resident", dynamic: false, editable: true, required: true, frozen_version: 2, builtin_version: 2},
  {legacy_id: "context.task", entry_id: "task-context", anchor_key: "task", kind: "generated", dynamic: true, editable: false, required: true, frozen_version: 1, builtin_version: 1},
];

function migration(overrides = {}) {
  return {mappings, merged: [], conflicts: [], orphans: [], ignored: [], ...overrides};
}

function view(overrides = {}) {
  return {
    tab: "entries",
    templates,
    selectedTemplateID: "role.main",
    drafts: {},
    catalog,
    catalogError: "",
    migration: migration(),
    migrationError: "",
    selectedEntryID: "core",
    selectedAnchorKey: "main",
    preview: preview(main),
    previewKey: "core#main",
    previewError: "",
    ...overrides,
  };
}

test("entries are the only top-level pages and anchors stay grouped inside them", () => {
  const html = promptAdmin.renderPromptAdmin(view());
  assert.equal((html.match(/data-prompt-entry=/g) || []).length, 2, "exactly two top-level entries");
  assert.match(html, /data-prompt-entry="core"/);
  assert.match(html, /data-prompt-entry="task-context"/);
  assert.equal((html.match(/data-prompt-tab=/g) || []).length, 3);
  assert.match(html, /旧模板 ID 仍作为别名解析到「入口#锚点」/);
  assert.match(html, /data-prompt-anchor="core#main"/);
  assert.match(html, /data-prompt-anchor="core#role-main"/);
  assert.match(html, /data-prompt-anchor="core#attachment"/);
  assert.match(html, /data-prompt-anchor="core#resource-agent-api"/);
  assert.match(html, /protocol\.attachment-delivery/);
  assert.match(html, /受保护\/只读/);
  assert.match(html, /只读资源/);
  // No legacy-scale top-level list and no editor controls on the entry view.
  assert.doesNotMatch(html, /data-prompt-template="role\.main"/);
  assert.doesNotMatch(html, /data-prompt-template-content/);
  assert.doesNotMatch(html, /id="save-prompt-template"/);
  assert.doesNotMatch(html, /id="reset-prompt-template"/);
});

test("generated fragments and resources render as preview-only panels", () => {
  const dynamic = promptAdmin.renderPromptAdmin(view({
    selectedEntryID: "task-context",
    selectedAnchorKey: "task",
    preview: preview(generated, {legacy_override: true, legacy_content: "old", legacy_override_version: 4}),
    previewKey: "task-context#task",
  }));
  assert.match(dynamic, /运行时生成\/不可单独编辑/);
  assert.match(dynamic, /不会写入或覆盖提示词 override/);
  assert.match(dynamic, /旧 ID context\.task 存在 Owner override/);
  assert.match(dynamic, /data-prompt-anchor-preview="content"/);
  assert.doesNotMatch(dynamic, /id="save-prompt-template"/);

  const readOnlyResource = promptAdmin.renderPromptAdmin(view({
    selectedAnchorKey: "resource-agent-api",
    preview: preview(resource),
    previewKey: "core#resource-agent-api",
  }));
  assert.match(readOnlyResource, /只读资源\/受保护锚点/);
  assert.match(readOnlyResource, /data-prompt-anchor-preview="content"/);
  assert.doesNotMatch(readOnlyResource, /id="save-prompt-template"/);
});

test("migration preview renders four groups and never offers a write action", () => {
  const states = [
    migration(),
    migration({merged: [{entry_id: "core", anchor_key: "role-main", legacy_ids: ["role.main"], version: 3, content_hash: "a", content: "body"}]}),
    migration({
      merged: [
        {entry_id: "core", anchor_key: "role-main", legacy_ids: ["role.main"], version: 3, content_hash: "a", content: "body"},
        {entry_id: "core", anchor_key: "main", legacy_ids: ["core.default"], version: 2, content_hash: "b", content: "body"},
      ],
      conflicts: [{legacy_id: "context.task", entry_id: "task-context", anchor_key: "task", code: "dynamic_anchor", message: "generated fragments cannot carry overrides", version: 1}],
      ignored: [{legacy_id: "role.sub", entry_id: "core", anchor_key: "role-sub", code: "superseded_builtin", message: "already ignored", version: 2}],
    }),
    migration({orphans: [{legacy_id: "retired.prompt", version: 1}]}),
  ];
  const counts = [[0, 0, 0, 0], [1, 0, 0, 0], [2, 1, 0, 1], [0, 0, 1, 0]];
  states.forEach((state, index) => {
    const html = promptAdmin.renderPromptAdmin(view({tab: "migration", migration: state}));
    assert.equal((html.match(/data-prompt-migration-group=/g) || []).length, 4);
    assert.match(html, new RegExp(`<b>${counts[index][0]}</b> 可迁移`));
    assert.match(html, new RegExp(`<b>${counts[index][1]}</b> 冲突`));
    assert.match(html, new RegExp(`<b>${counts[index][2]}</b> 孤儿`));
    assert.match(html, new RegExp(`<b>${counts[index][3]}</b> 已忽略`));
    assert.match(html, /不会写入数据库，不会自动迁移，也不会覆盖 Owner override/);
    assert.doesNotMatch(html, /一键迁移/);
    assert.doesNotMatch(html, /data-prompt-template-content/);
    assert.doesNotMatch(html, /id="save-prompt-template"/);
  });
  const multiple = promptAdmin.renderPromptAdmin(view({tab: "migration", migration: states[2]}));
  assert.match(multiple, /generated fragments cannot carry overrides/);
  assert.match(multiple, /旧路径已忽略/);
  const orphan = promptAdmin.renderPromptAdmin(view({tab: "migration", migration: states[3]}));
  assert.match(orphan, /保留只读/);
  assert.match(orphan, /不删除、不覆盖/);
});

test("legacy ID editor remains available with its original save and reset controls", () => {
  const html = promptAdmin.renderPromptAdmin(view({tab: "legacy"}));
  assert.match(html, /data-prompt-template="role\.main"/);
  assert.match(html, /data-prompt-template="protocol\.attachment-delivery"/);
  assert.match(html, /data-prompt-template-content/);
  assert.match(html, /id="save-prompt-template"/);
  assert.match(html, /id="reset-prompt-template"/);
  assert.match(html, /旧模板管理/);

  const readOnly = promptAdmin.renderPromptAdmin(view({tab: "legacy", selectedTemplateID: "protocol.attachment-delivery"}));
  assert.match(readOnly, /内置只读模板/);
  assert.doesNotMatch(readOnly, /id="save-prompt-template"/);
});
