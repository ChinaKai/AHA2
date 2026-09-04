import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

test("built web contains responsive application", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  const agents = await readFile(resolve(root, "dist", "task_agents.js"), "utf8");
  const taskComposer = await readFile(resolve(root, "dist", "task_composer.js"), "utf8");
  const taskTools = await readFile(resolve(root, "dist", "task_tools.js"), "utf8");
  const hardwarePanel = await readFile(resolve(root, "dist", "hardware_panel.js"), "utf8");
  const hardwareTerminal = await readFile(resolve(root, "dist", "hardware_terminal.js"), "utf8");
  const conversation = await readFile(resolve(root, "dist", "conversation_ui.js"), "utf8");
  const helpers = await readFile(resolve(root, "dist", "ui_helpers.js"), "utf8");
  const promptAdmin = await readFile(resolve(root, "dist", "prompt_admin.js"), "utf8");
  const css = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  const index = await readFile(resolve(root, "dist", "index.html"), "utf8");
  assert.match(script, /api\.authStatus/);
  assert.match(script, /EventSource/);
  assert.match(script, /api\.agentConversation/);
  assert.match(script, /api\.agentMessage/);
  assert.match(script, /name="ssh_auth"/);
  assert.match(script, /name="ssh_password"/);
  assert.match(script, /name="clear_ssh_password"/);
  assert.match(script, /workspace-clear-secret/);
  assert.match(css, /\.hardware-clear-secret, \.workspace-clear-secret/);
  assert.match(script, /ssh_password_configured/);
  assert.match(script, /clear_ssh_password:\s*payload\.clear_ssh_password === "on"/);
  assert.match(script, /delete body\.clear_ssh_password/);
  assert.match(script, /data-conversation-category/);
  assert.match(agents, /agent-turn-card/);
  assert.match(agents, /AHA 系统路由/);
  assert.doesNotMatch(agents, /virtual-agent-batch/);
  assert.match(css, /\.aha-orchestration-card/);
  assert.match(script, /renderPromptAdmin/);
  assert.match(script, /"prompts",\s*"bot"/);
  assert.match(script, /agent-config-form/);
  assert.match(taskComposer, /id="composer-agent"/);
  assert.match(taskTools, /data-task-tool/);
  assert.match(taskTools, /id="close-task-tool"/);
  assert.match(taskTools, /task-tool-panel/);
  assert.match(taskTools, /renderHardwarePanel/);
  assert.match(hardwarePanel, /hardware-config-form/);
  assert.match(hardwarePanel, /connectHardware/);
  assert.match(hardwarePanel, /sendHardware/);
  assert.match(hardwarePanel, /自定义\.\.\./);
  assert.match(hardwarePanel, /commonBaudrates/);
  assert.match(hardwarePanel, />SSH<\/option>/);
  assert.match(hardwarePanel, /name="ssh_auth"/);
  assert.match(hardwarePanel, /有密码时优先密码/);
  assert.match(hardwarePanel, /data-hardware-config="ssh"/);
  assert.match(hardwarePanel, /data-hardware-config="login"/);
  assert.match(hardwarePanel, /hardware-save-top/);
  assert.match(hardwarePanel, /aha2:hardware-draft:/);
  assert.match(hardwarePanel, /sessionStorage\.setItem/);
  assert.match(hardwarePanel, /password_configured:\s*group\.password_configured/);
  assert.match(hardwarePanel, /lastError/);
  assert.match(hardwarePanel, /stream\.lastError \|\| stream\.status\?\.error/);
  assert.match(hardwarePanel, /item\.direction === "rx"/);
  assert.doesNotMatch(hardwarePanel, /hardware-output|hardware-output-fallback|formatEvent/);
  assert.match(hardwarePanel, /sendEncoding:\s*"text"/);
  assert.match(hardwarePanel, /access:\s*"read_write"/);
  assert.match(hardwareTerminal, /addEventListener\("touchstart", focusTerminal/);
  assert.match(hardwareTerminal, /textarea\.inputMode = "text"/);
  assert.match(hardwareTerminal, /hardware-mobile-terminal-input/);
  assert.match(hardwareTerminal, /hardware-mobile-terminal-view/);
  assert.match(hardwareTerminal, /terminal\.buffer\.active/);
  assert.match(hardwareTerminal, /onFallbackSend/);
  assert.match(hardwareTerminal, /writeFallback/);
  assert.match(hardwarePanel, /bytesToBase64/);
  assert.match(hardwarePanel, /"base64"/);
  assert.match(hardwareTerminal, /replace\(\/\\r\\n\|\\r\|\\n\/g, "\\r"\)/);
  assert.match(css, /\.hardware-xterm \.xterm-rows > div/);
  assert.match(css, /\.hardware-xterm\.mobile-terminal-compat \.xterm/);
  assert.match(api, /hardware\/serial-ports/);
  assert.match(hardwareTerminal, /new WebSocket/);
  assert.match(hardwareTerminal, /window\.Terminal/);
  assert.match(hardwareTerminal, /data instanceof Blob/);
  assert.match(hardwareTerminal, /after:\s*String/);
  assert.match(hardwareTerminal, /TextEncoder/);
  assert.match(hardwareTerminal, /send\s*\(data/);
  assert.match(hardwareTerminal, /type:\s*"resize"/);
  assert.match(index, /vendor\/xterm\.js\?v=[a-f0-9]{12}/);
  assert.match(index, /vendor\/xterm\.css\?v=[a-f0-9]{12}/);
  assert.match(hardwarePanel, /data-hardware-terminal-key="ctrl-c"/);
  assert.match(hardwarePanel, /const sendForm = root\.querySelector/);
  assert.doesNotMatch(script, /data-task-tab=|mobile-agent-strip|conversation-filters/);
  assert.match(taskTools, /renderTaskMemory\(detail\.memory\)/);
  assert.doesNotMatch(script, /data-realtime-state class=/);
  assert.match(script, /task-head-subline/);
  assert.match(taskTools, /iconName: "hardware"/);
  assert.match(taskTools, /iconName: "browser"/);
  assert.match(script, /state\.taskTool === tool/);
  assert.doesNotMatch(script, /<h3>Task Memory<\/h3>.*Context Evidence/s);
  assert.match(script, /<details class="prompt-snapshot" open>/);
  assert.match(script, /refreshTaskRuntime/);
  assert.match(script, /startTaskFallback/);
  assert.match(agents, /data-live-elapsed-ms/);
  assert.match(agents, /ms > 0 && ms < 1000/);
  assert.match(agents, /metrics\.session_id/);
  assert.match(agents, /class="session-id"/);
  assert.match(agents, /function renderTaskMemory/);
  assert.match(conversation, /data-copy-message/);
  assert.match(conversation, /data-toggle-message/);
  assert.match(conversation, /messageCollapseChars = 900/);
  assert.match(helpers, /sessionStorage/);
  assert.match(helpers, /navigator\.clipboard/);
  assert.match(taskComposer, /composer-target-wrap/);
  assert.match(taskComposer, /conversation-filter-popover/);
  assert.match(taskComposer, /data-conversation-category/);
  assert.match(promptAdmin, /prompt-template-layout/);
  assert.doesNotMatch(promptAdmin, /prompt-route-list|Effective Prompt|Context Manifest/);
  assert.doesNotMatch(api, /prompts\/routes|prompts\/preview|context\/resources/);
  assert.match(script, /shell\(renderPromptAdmin\(\)\)/);
  assert.match(script, /heartbeat/);
  assert.doesNotMatch(css, /\.conversation-event/);
  assert.match(css, /\.message\.agent-tool-message/);
  assert.match(css, /\.task-tool-actions/);
  assert.match(css, /\.agent-turn-row/);
  assert.match(css, /\.ctx-view/);
  assert.match(css, /\.task-tool-panel\.open/);
  assert.match(css, /\.task-tool-panel\s*\{[^}]*inset:\s*0;[^}]*width:\s*100%;[^}]*height:\s*100%;/s);
  assert.doesNotMatch(css, /\.task-tool-panel\s*\{[^}]*position:\s*fixed;/s);
  assert.doesNotMatch(css, /\.task-tool-panel \.ctx-token-grid/);
  assert.match(css, /\.task-memory-tool\s*\{[^}]*width:\s*min\(960px,100%\);[^}]*margin:\s*0 auto;/s);
  assert.match(css, /@media \(max-width: 760px\)/);
  assert.match(css, /\.mobile-header \{ display: none; \}/);
  assert.match(script, /taskDraft/);
  assert.match(script, /taskRuntimeSignature/);
  assert.match(script, /flushDeferredRender/);
  assert.match(script, /document\.activeElement instanceof HTMLTextAreaElement/);
  assert.match(script, /updateTaskLiveRegions/);
  assert.match(script, /agent-turn-slot/);
  assert.doesNotMatch(script, /task-action-menu/);
  assert.match(script, /slash-command-menu/);
  assert.match(script, /executeTaskSlashCommand/);
  assert.match(script, /\/interrupt/);
  assert.match(script, /\/complete/);
  assert.match(script, /\/reopen/);
  assert.match(script, /taskFailureBannerHtml/);
  assert.match(script, /formatTokenCount\(task\.total_tokens\)/);
  assert.match(script, /Intl\.NumberFormat\("en-US"\)/);
  assert.match(agents, /context_inconsistent/);
  assert.match(script, /id="task-isolation"/);
  assert.match(script, /id="task-worktree-settings"/);
  assert.match(script, /syncTaskGitIsolation/);
  assert.doesNotMatch(script, /id="ws-isolation"|id="ws-worktree-dir"/);
  assert.match(conversation, /agent-update-message/);
  assert.match(conversation, /agent-tool-message/);
  assert.match(conversation, /agent-routed-message/);
  assert.doesNotMatch(conversation, /conversation-event/);
  assert.match(conversation, /visibleAgentText/);
  assert.match(css, /overflow-wrap: anywhere/);
  assert.doesNotMatch(script, /data-task-action="delete"/);
  assert.match(script, /requestAnimationFrame\(runTaskClock\)/);
  assert.match(script, /composer-focused/);
  assert.match(script, /visualViewport/);
  assert.match(script, /task-view-active/);
  assert.match(api, /cache: "no-store"/);
  assert.match(script, /addEventListener\("heartbeat"/);
  assert.match(css, /body\.composer-focused \.composer-target-wrap/);
  assert.match(css, /body\.task-view-active/);
  assert.match(css, /overscroll-behavior: contain/);
  assert.match(index, /app\.js\?v=[a-f0-9]{12}/);
  assert.match(index, /styles\.css\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/api\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/task_agents\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/task_composer\.js\?v=[a-f0-9]{12}/);
  assert.match(taskComposer, /\.\/icons\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/task_tools\.js\?v=[a-f0-9]{12}/);
  assert.match(taskTools, /\.\/icons\.js\?v=[a-f0-9]{12}/);
  assert.match(taskTools, /\.\/task_agents\.js\?v=[a-f0-9]{12}/);
  assert.match(taskTools, /\.\/hardware_panel\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/hardware_panel\.js\?v=[a-f0-9]{12}/);
  assert.match(hardwarePanel, /\.\/api\.js\?v=[a-f0-9]{12}/);
  assert.match(hardwarePanel, /\.\/hardware_terminal\.js\?v=[a-f0-9]{12}/);
  assert.match(agents, /\.\/icons\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/conversation_ui\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/ui_helpers\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/prompt_admin\.js\?v=[a-f0-9]{12}/);
  assert.match(promptAdmin, /\.\/api\.js\?v=[a-f0-9]{12}/);
});

test("hardware terminal accepts WebView Blob binary frames", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {terminalFrameBytes} = await import(pathToFileURL(resolve(root, "dist", "hardware_terminal.js")));
  const expected = new Uint8Array([0x1b, 0x5b, 0x33, 0x32, 0x6d]);
  assert.deepEqual(await terminalFrameBytes(expected.buffer), expected);
  assert.deepEqual(await terminalFrameBytes(new Blob([expected])), expected);
});

test("message collapse uses Unicode character count", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderConversationList} = await import(pathToFileURL(resolve(root, "dist", "conversation_ui.js")));
  const item = summary => ({
    id: "message-1",
    task_id: "task-1",
    agent_id: "main",
    stream_agent_id: "main",
    category: "chat",
    kind: "agent_message",
    summary,
    created_at: "2026-09-03T00:00:00Z",
  });
  const manyLines = Array.from({length: 400}, () => "x").join("\n");
  assert.doesNotMatch(renderConversationList([item(manyLines)], null, false), /data-toggle-message/);
  assert.doesNotMatch(renderConversationList([item("😀".repeat(500))], null, false), /data-toggle-message/);
  assert.match(renderConversationList([item("字".repeat(901))], null, false), /data-toggle-message/);
});

test("tool events render as message bubbles", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderConversationList} = await import(pathToFileURL(resolve(root, "dist", "conversation_ui.js")));
  const html = renderConversationList([{
    sequence: 1,
    id: "tool-1",
    task_id: "task-1",
    agent_id: "main",
    stream_agent_id: "main",
    category: "tool",
    kind: "agent_command_finished",
    summary: "go test ./...",
    payload: {output_tail: "ok"},
    created_at: "2026-09-03T00:00:00Z",
  }], null, false);
  assert.match(html, /class="message agent\s+agent-tool-message/);
  assert.match(html, />工具</);
  assert.match(html, /message-output/);
  assert.match(html, /\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/);
  assert.doesNotMatch(html, /conversation-event/);
});
