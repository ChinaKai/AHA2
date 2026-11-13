import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const {
  agentConfigChanged,
  agentConfigSnapshot,
  agentConfigSummary,
  defaultAgentEffort,
  effortLevelsForBackend,
  renderAgentConfigDialog,
  renderAgentConfigForm,
  validateAgentConfigInput,
} = await import(pathToFileURL(resolve(root, "build", "task_agents.js")).href);

const model = {id: "model-1", backend: "codex", source: "env", display_name: "Model 1", provider_name: "Env", default_reasoning_effort: "low"};
const skill = {
  id: "skill-1", package_slug: "review", scope: "global", name: "Review", description: "Review changes",
  instructions: "Review", version: 1, status: "active", enabled: true, auto_attach: false, created_at: "", updated_at: "",
};
const mcp = {
  id: "mcp-1", name: "Tools", description: "Tool set", transport: "http", enabled: true, builtin: false,
  auto_inject: false, created_at: "", updated_at: "",
};
const task = {
  id: "task-1", project_id: "project-1", workspace_id: "workspace-1", title: "Task", original_request: "",
  current_goal: "", status: "active", isolation: "inplace", collaboration_mode: "auto", max_agents: 3,
  knowledge_policy: "inherit", skill_ids: ["skill-1"], mcp_server_ids: ["mcp-1"], total_tokens: 0,
  created_at: "", updated_at: "",
};
const agent = {
  task_id: "task-1", agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_snapshot_id: "",
  inherit_main: false, backend: "codex", model_source: "env", model_id: "model-1", reasoning_effort: "high",
  filesystem: "workspace-write", approval: "never", proxy_enabled: false, fast_mode: false,
  runtime_config_valid: true, unread_count: 0, created_at: "", updated_at: "",
};

function createForm(overrides = {}) {
  return renderAgentConfigForm({
    prefix: "task",
    models: [model],
    accounts: [],
    skills: [skill],
    mcpServers: [mcp],
    projectID: "project-1",
    effort: "high",
    skillIDs: [],
    mcpIDs: [],
    fastMode: false,
    skillsBody: "skills-body",
    mcpBody: "mcp-body",
    mcpTotal: 1,
    ...overrides,
  });
}

function assertFieldOrder(html) {
  const markers = ["执行配置", "推理强度", "技能", "MCP 工具", "Fast 模式"];
  const positions = markers.map(marker => html.indexOf(marker));
  for (const [index, position] of positions.entries()) {
    assert.notEqual(position, -1, `missing ${markers[index]} in the shared Agent config`);
  }
  assert.deepEqual([...positions].sort((a, b) => a - b), positions, `field order drifted: ${markers.join(" -> ")}`);
}

test("create and detail render the same Agent config component in the same order", async () => {
  const create = createForm();
  const detail = renderAgentConfigDialog({task, agents: [agent], turns: [], memory: {task_id: "task-1", current_goal: ""}}, "main", [model], [], [skill], [mcp]);
  assertFieldOrder(create);
  assertFieldOrder(detail);
  const app = await readFile(resolve(root, "build", "app.js"), "utf8");
  assert.match(app, /renderAgentConfigForm\(\{/, "Create Task does not reuse the shared Agent config form");
  assert.match(app, /基本信息/);
  assert.match(app, /任务设置/);
  assert.match(create, /高级配置/);
  assert.match(detail, /高级配置/);
});

test("model and reasoning are visible, advanced folds start collapsed, and effort defaults to high", () => {
  const html = createForm();
  assert.match(html, /<option value="high" selected>/);
  assert.doesNotMatch(html, /<details[^>]*\bopen\b/, "a fold opened by default");
  assert.match(html, /data-advanced-summary>技能 0 · MCP 0 · Fast 关</);
  assert.match(html, /id="task-skills-fold"[\s\S]*未选择/);
  assert.match(html, /id="task-mcp-fold"[\s\S]*已选 0|id="task-mcp-fold"[\s\S]*未选择/);
  assert.match(html, /id="task-fast-fold"[\s\S]*Fast 模式/);
  assert.equal(defaultAgentEffort("codex"), "high");
  assert.equal(defaultAgentEffort("claude"), "high");
  assert.deepEqual(effortLevelsForBackend("claude"), ["low", "medium", "high", "xhigh", "max"]);
});

test("the detail dialog keeps the shared copy and reports selected Skills and MCP tools", () => {
  const html = renderAgentConfigDialog({task, agents: [agent], turns: [], memory: {task_id: "task-1", current_goal: ""}}, "main", [model], [], [skill], [mcp]);
  assert.match(html, /id="agent-config-skills-fold"[\s\S]*>技能<[\s\S]*已选 1</);
  assert.match(html, /id="agent-config-mcp-fold"[\s\S]*>MCP 工具<[\s\S]*已选 1</);
  assert.match(html, /name="mcp_server_ids" value="mcp-1" checked/);
  assert.doesNotMatch(html, /task_mcp_ids/, "detail still uses a second MCP field name");
  assert.match(html, /data-advanced-summary>技能 1 · MCP 1 · Fast 关</);
  assert.match(html, /<button type="button" data-close>取消<\/button>/);
  assert.match(html, /<button class="primary" value="default">保存 Agent 配置<\/button>/);
});

test("summary, snapshot and dirty comparison are stable and ignore identity fields", () => {
  assert.equal(agentConfigSummary(2, 3, true), "技能 2 · MCP 3 · Fast 开");
  assert.equal(agentConfigSummary(0, 0, false), "技能 0 · MCP 0 · Fast 关");
  assert.equal(agentConfigSnapshot({b: "2", a: ["y", "x"]}), "a=x,y&b=2");
  assert.equal(agentConfigSnapshot({agent_id: "main", start_mode: "immediate", b: "1"}), "b=1");
  assert.equal(agentConfigChanged("", "b=1"), false, "an unopened dialog has no baseline");
  assert.equal(agentConfigChanged("b=1", "b=1"), false);
  assert.equal(agentConfigChanged("b=1", "b=2"), true);
});

test("validation explains missing models, invalid effort and unavailable selections", () => {
  assert.deepEqual(
    validateAgentConfigInput({backend: "codex", modelSource: "env", modelID: "", reasoningEffort: "high", availableSkillIDs: [], availableMCPIDs: []}),
    ["请选择模型"],
  );
  const errors = validateAgentConfigInput({
    backend: "codex", modelSource: "env", modelID: "model-1", reasoningEffort: "ultra",
    skillIDs: ["missing-skill"], availableSkillIDs: ["skill-1"],
    mcpIDs: ["missing-mcp"], availableMCPIDs: ["mcp-1"],
  });
  assert.match(errors.join("\n"), /推理强度无效，可选值：low \/ medium \/ high \/ xhigh/);
  assert.match(errors.join("\n"), /所选技能不可用，请重新选择：missing-skill/);
  assert.match(errors.join("\n"), /所选 MCP 工具不可用，请重新选择：missing-mcp/);
  assert.deepEqual(
    validateAgentConfigInput({backend: "claude", modelSource: "claude_native", modelID: "claude-x", reasoningEffort: "max"}),
    [],
  );
});

test("closing with unsaved edits asks first and cancel performs no write", async () => {
  const app = await readFile(resolve(root, "build", "app.js"), "utf8");
  assert.match(app, /function confirmAgentConfigClose/);
  assert.match(app, /Agent 配置尚未保存，关闭将丢弃这些修改。确定关闭？/);
  assert.match(app, /agentConfigBaseline = ""/, "saving does not reset the dirty baseline");
  assert.match(app, /addEventListener\("cancel"/, "Esc bypasses the unsaved-changes prompt");
  assert.match(app, /bindForm\("#agent-config-form"/);
  // Cancelling only closes the dialog; the write path is the submit handler.
  assert.match(app, /data-close>取消/);
});