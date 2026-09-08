import {readFile, mkdir, writeFile, copyFile, rm} from "node:fs/promises";
import {createHash} from "node:crypto";
import {resolve} from "node:path";
import {stripTypeScriptTypes} from "node:module";

const root = resolve(import.meta.dirname, "..");
const source = resolve(root, "web");
const output = resolve(source, "dist");
await rm(output, {recursive: true, force: true});
await mkdir(output, {recursive: true});
await mkdir(resolve(output, "vendor"), {recursive: true});

for (const name of ["api", "icons", "types", "runtime_picker", "task_agents", "task_composer", "hardware_terminal", "hardware_panel", "task_tools", "markdown", "conversation_ui", "ui_helpers", "prompt_admin", "proxy_settings", "sync_settings", "codex_accounts", "knowledge_workspace", "channels", "main"]) {
  const input = await readFile(resolve(source, "src", `${name}.ts`), "utf8");
  const transformed = stripTypeScriptTypes(input, {mode: "transform", sourceMap: false});
  const outputName = name === "main" ? "app" : name;
  await writeFile(resolve(output, `${outputName}.js`), transformed);
}
await copyFile(resolve(source, "index.html"), resolve(output, "index.html"));
await copyFile(resolve(source, "src", "styles.css"), resolve(output, "styles.css"));
for (const name of ["xterm.js", "xterm.css", "xterm.LICENSE"]) {
  await copyFile(resolve(source, "vendor", name), resolve(output, "vendor", name));
}

const versionSource = await Promise.all([
  readFile(resolve(output, "app.js")),
  readFile(resolve(output, "api.js")),
  readFile(resolve(output, "icons.js")),
  readFile(resolve(output, "runtime_picker.js")),
  readFile(resolve(output, "task_agents.js")),
  readFile(resolve(output, "task_composer.js")),
  readFile(resolve(output, "hardware_panel.js")),
  readFile(resolve(output, "hardware_terminal.js")),
  readFile(resolve(output, "task_tools.js")),
  readFile(resolve(output, "markdown.js")),
  readFile(resolve(output, "conversation_ui.js")),
  readFile(resolve(output, "ui_helpers.js")),
  readFile(resolve(output, "prompt_admin.js")),
  readFile(resolve(output, "proxy_settings.js")),
  readFile(resolve(output, "sync_settings.js")),
  readFile(resolve(output, "codex_accounts.js")),
  readFile(resolve(output, "knowledge_workspace.js")),
  readFile(resolve(output, "channels.js")),
  readFile(resolve(output, "styles.css")),
  readFile(resolve(output, "vendor", "xterm.js")),
  readFile(resolve(output, "vendor", "xterm.css")),
]);
const version = createHash("sha256").update(Buffer.concat(versionSource)).digest("hex").slice(0, 12);
const appPath = resolve(output, "app.js");
const versionedApp = (await readFile(appPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./runtime_picker.js"', `"./runtime_picker.js?v=${version}"`)
  .replaceAll('"./task_agents.js"', `"./task_agents.js?v=${version}"`)
  .replaceAll('"./task_composer.js"', `"./task_composer.js?v=${version}"`)
  .replaceAll('"./hardware_panel.js"', `"./hardware_panel.js?v=${version}"`)
  .replaceAll('"./hardware_terminal.js"', `"./hardware_terminal.js?v=${version}"`)
  .replaceAll('"./task_tools.js"', `"./task_tools.js?v=${version}"`)
  .replaceAll('"./markdown.js"', `"./markdown.js?v=${version}"`)
  .replaceAll('"./conversation_ui.js"', `"./conversation_ui.js?v=${version}"`);
const versionedAppWithHelpers = versionedApp
  .replaceAll('"./ui_helpers.js"', `"./ui_helpers.js?v=${version}"`)
  .replaceAll('"./prompt_admin.js"', `"./prompt_admin.js?v=${version}"`)
  .replaceAll('"./proxy_settings.js"', `"./proxy_settings.js?v=${version}"`)
  .replaceAll('"./sync_settings.js"', `"./sync_settings.js?v=${version}"`)
  .replaceAll('"./codex_accounts.js"', `"./codex_accounts.js?v=${version}"`);
const versionedAppWithKnowledge = versionedAppWithHelpers
  .replaceAll('"./knowledge_workspace.js"', `"./knowledge_workspace.js?v=${version}"`)
  .replaceAll('"./channels.js"', `"./channels.js?v=${version}"`);
await writeFile(appPath, versionedAppWithKnowledge);
const agentsPath = resolve(output, "task_agents.js");
const versionedAgents = (await readFile(agentsPath, "utf8"))
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./runtime_picker.js"', `"./runtime_picker.js?v=${version}"`);
await writeFile(agentsPath, versionedAgents);
const conversationPath = resolve(output, "conversation_ui.js");
const versionedConversation = (await readFile(conversationPath, "utf8"))
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./markdown.js"', `"./markdown.js?v=${version}"`)
  .replaceAll('"./task_agents.js"', `"./task_agents.js?v=${version}"`);
await writeFile(conversationPath, versionedConversation);
const taskComposerPath = resolve(output, "task_composer.js");
const versionedTaskComposer = (await readFile(taskComposerPath, "utf8"))
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`);
await writeFile(taskComposerPath, versionedTaskComposer);
const taskToolsPath = resolve(output, "task_tools.js");
const versionedTaskTools = (await readFile(taskToolsPath, "utf8"))
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./hardware_panel.js"', `"./hardware_panel.js?v=${version}"`)
  .replaceAll('"./task_agents.js"', `"./task_agents.js?v=${version}"`);
await writeFile(taskToolsPath, versionedTaskTools);
const hardwarePanelPath = resolve(output, "hardware_panel.js");
const versionedHardwarePanel = (await readFile(hardwarePanelPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./hardware_terminal.js"', `"./hardware_terminal.js?v=${version}"`);
await writeFile(hardwarePanelPath, versionedHardwarePanel);
const promptAdminPath = resolve(output, "prompt_admin.js");
const versionedPromptAdmin = (await readFile(promptAdminPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`);
await writeFile(promptAdminPath, versionedPromptAdmin);
const proxySettingsPath = resolve(output, "proxy_settings.js");
const versionedProxySettings = (await readFile(proxySettingsPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`);
await writeFile(proxySettingsPath, versionedProxySettings);
const syncSettingsPath = resolve(output, "sync_settings.js");
const versionedSyncSettings = (await readFile(syncSettingsPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`);
await writeFile(syncSettingsPath, versionedSyncSettings);
const codexAccountsPath = resolve(output, "codex_accounts.js");
const versionedCodexAccounts = (await readFile(codexAccountsPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`);
await writeFile(codexAccountsPath, versionedCodexAccounts);
const knowledgeWorkspacePath = resolve(output, "knowledge_workspace.js");
const versionedKnowledgeWorkspace = (await readFile(knowledgeWorkspacePath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./markdown.js"', `"./markdown.js?v=${version}"`);
await writeFile(knowledgeWorkspacePath, versionedKnowledgeWorkspace);
const channelsPath = resolve(output, "channels.js");
const versionedChannels = (await readFile(channelsPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`);
await writeFile(channelsPath, versionedChannels);
const indexPath = resolve(output, "index.html");
const versionedIndex = (await readFile(indexPath, "utf8"))
  .replace('href="/vendor/xterm.css"', `href="/vendor/xterm.css?v=${version}"`)
  .replace('href="/styles.css"', `href="/styles.css?v=${version}"`)
  .replace('src="/vendor/xterm.js"', `src="/vendor/xterm.js?v=${version}"`)
  .replace('src="/app.js"', `src="/app.js?v=${version}"`);
await writeFile(indexPath, versionedIndex);
console.log(`Built ${output}`);
