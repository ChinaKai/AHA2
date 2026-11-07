import {readFile, mkdir, writeFile, copyFile, rm, cp} from "node:fs/promises";
import {createHash} from "node:crypto";
import {resolve} from "node:path";
import {stripTypeScriptTypes} from "node:module";

const root = resolve(import.meta.dirname, "..");
const source = resolve(root, "web");
const output = resolve(source, "dist");
await rm(output, {recursive: true, force: true});
await mkdir(output, {recursive: true});
await mkdir(resolve(output, "vendor"), {recursive: true});
await cp(resolve(source, "src", "assets", "assistant"), resolve(output, "assets", "assistant"), {recursive: true});
await copyFile(resolve(root, "assets", "aha-logo.svg"), resolve(output, "assets", "aha-logo.svg"));
await copyFile(resolve(root, "assets", "aha2-logo.png"), resolve(output, "assets", "aha2-logo.png"));
await copyFile(resolve(root, "assets", "aha2.ico"), resolve(output, "favicon.ico"));

for (const name of ["api", "i18n", "icons", "types", "pet_bubble", "pet_approvals", "runtime_picker", "task_agents", "task_composer", "task_filters", "hardware_terminal", "hardware_panel", "hardware_resources", "mail_cards", "mail_editor", "mail_resources", "mail_task", "resources", "desktop_panel", "task_tools", "markdown", "conversation_ui", "ui_helpers", "prompt_admin", "proxy_settings", "data_retention", "sync_settings", "codex_accounts", "knowledge_workspace", "channels", "assistant", "main"]) {
  const input = await readFile(resolve(source, "src", `${name}.ts`), "utf8");
  const transformed = stripTypeScriptTypes(input, {mode: "transform", sourceMap: false});
  const outputName = name === "main" ? "app" : name;
  await writeFile(resolve(output, `${outputName}.js`), transformed);
}
await copyFile(resolve(source, "index.html"), resolve(output, "index.html"));
await copyFile(resolve(source, "src", "styles.css"), resolve(output, "styles.css"));
const mailStyles = await readFile(resolve(source, "src", "mail.css"), "utf8");
await writeFile(resolve(output, "styles.css"), `${await readFile(resolve(output, "styles.css"), "utf8")}\n${mailStyles}`);
for (const name of ["xterm.js", "xterm.css", "xterm.LICENSE"]) {
  await copyFile(resolve(source, "vendor", name), resolve(output, "vendor", name));
}

const versionSource = await Promise.all([
  readFile(resolve(output, "app.js")),
  readFile(resolve(output, "api.js")),
  readFile(resolve(output, "i18n.js")),
  readFile(resolve(output, "icons.js")),
  readFile(resolve(output, "pet_bubble.js")),
  readFile(resolve(output, "pet_approvals.js")),
  readFile(resolve(output, "runtime_picker.js")),
  readFile(resolve(output, "task_agents.js")),
  readFile(resolve(output, "task_composer.js")),
  readFile(resolve(output, "task_filters.js")),
  readFile(resolve(output, "hardware_panel.js")),
  readFile(resolve(output, "hardware_resources.js")),
  readFile(resolve(output, "mail_cards.js")),
  readFile(resolve(output, "mail_editor.js")),
  readFile(resolve(output, "mail_resources.js")),
  readFile(resolve(output, "mail_task.js")),
  readFile(resolve(output, "resources.js")),
  readFile(resolve(output, "desktop_panel.js")),
  readFile(resolve(output, "hardware_terminal.js")),
  readFile(resolve(output, "task_tools.js")),
  readFile(resolve(output, "markdown.js")),
  readFile(resolve(output, "conversation_ui.js")),
  readFile(resolve(output, "ui_helpers.js")),
  readFile(resolve(output, "prompt_admin.js")),
  readFile(resolve(output, "proxy_settings.js")),
  readFile(resolve(output, "data_retention.js")),
  readFile(resolve(output, "sync_settings.js")),
  readFile(resolve(output, "codex_accounts.js")),
  readFile(resolve(output, "knowledge_workspace.js")),
  readFile(resolve(output, "channels.js")),
  readFile(resolve(output, "assistant.js")),
  readFile(resolve(output, "styles.css")),
  readFile(resolve(output, "vendor", "xterm.js")),
  readFile(resolve(output, "vendor", "xterm.css")),
]);
const version = createHash("sha256").update(Buffer.concat(versionSource)).digest("hex").slice(0, 12);
const appPath = resolve(output, "app.js");
const versionedApp = (await readFile(appPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./i18n.js"', `"./i18n.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./pet_approvals.js"', `"./pet_approvals.js?v=${version}"`)
  .replaceAll('"./runtime_picker.js"', `"./runtime_picker.js?v=${version}"`)
  .replaceAll('"./task_agents.js"', `"./task_agents.js?v=${version}"`)
  .replaceAll('"./task_composer.js"', `"./task_composer.js?v=${version}"`)
  .replaceAll('"./task_filters.js"', `"./task_filters.js?v=${version}"`)
  .replaceAll('"./hardware_panel.js"', `"./hardware_panel.js?v=${version}"`)
  .replaceAll('"./desktop_panel.js"', `"./desktop_panel.js?v=${version}"`)
  .replaceAll('"./hardware_terminal.js"', `"./hardware_terminal.js?v=${version}"`)
  .replaceAll('"./task_tools.js"', `"./task_tools.js?v=${version}"`)
  .replaceAll('"./markdown.js"', `"./markdown.js?v=${version}"`)
  .replaceAll('"./conversation_ui.js"', `"./conversation_ui.js?v=${version}"`)
  .replaceAll('"./assistant.js"', `"./assistant.js?v=${version}"`);
const versionedAppWithHelpers = versionedApp
  .replaceAll('"./ui_helpers.js"', `"./ui_helpers.js?v=${version}"`)
  .replaceAll('"./prompt_admin.js"', `"./prompt_admin.js?v=${version}"`)
  .replaceAll('"./proxy_settings.js"', `"./proxy_settings.js?v=${version}"`)
  .replaceAll('"./data_retention.js"', `"./data_retention.js?v=${version}"`)
  .replaceAll('"./sync_settings.js"', `"./sync_settings.js?v=${version}"`)
  .replaceAll('"./codex_accounts.js"', `"./codex_accounts.js?v=${version}"`)
  .replaceAll('"./hardware_resources.js"', `"./hardware_resources.js?v=${version}"`);
const versionedAppWithKnowledge = versionedAppWithHelpers
  .replaceAll('"./knowledge_workspace.js"', `"./knowledge_workspace.js?v=${version}"`)
  .replaceAll('"./channels.js"', `"./channels.js?v=${version}"`)
  .replaceAll('"./resources.js"', `"./resources.js?v=${version}"`)
  .replaceAll('"./mail_task.js"', `"./mail_task.js?v=${version}"`)
  .replaceAll('"./mail_cards.js"', `"./mail_cards.js?v=${version}"`)
  .replaceAll('"./mail_resources.js"', `"./mail_resources.js?v=${version}"`);
await writeFile(appPath, versionedAppWithKnowledge);
const agentsPath = resolve(output, "task_agents.js");
const versionedAgents = (await readFile(agentsPath, "utf8"))
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./runtime_picker.js"', `"./runtime_picker.js?v=${version}"`);
await writeFile(agentsPath, versionedAgents);
const conversationPath = resolve(output, "conversation_ui.js");
const versionedConversation = (await readFile(conversationPath, "utf8"))
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./mail_cards.js"', `"./mail_cards.js?v=${version}"`)
  .replaceAll('"./markdown.js"', `"./markdown.js?v=${version}"`)
  .replaceAll('"./task_agents.js"', `"./task_agents.js?v=${version}"`);
await writeFile(conversationPath, versionedConversation);
const taskComposerPath = resolve(output, "task_composer.js");
const versionedTaskComposer = (await readFile(taskComposerPath, "utf8"))
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./i18n.js"', `"./i18n.js?v=${version}"`);
await writeFile(taskComposerPath, versionedTaskComposer);
const taskToolsPath = resolve(output, "task_tools.js");
const versionedTaskTools = (await readFile(taskToolsPath, "utf8"))
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./hardware_panel.js"', `"./hardware_panel.js?v=${version}"`)
  .replaceAll('"./desktop_panel.js"', `"./desktop_panel.js?v=${version}"`)
  .replaceAll('"./task_agents.js"', `"./task_agents.js?v=${version}"`);
await writeFile(taskToolsPath, versionedTaskTools);
const hardwarePanelPath = resolve(output, "hardware_panel.js");
const versionedHardwarePanel = (await readFile(hardwarePanelPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./hardware_terminal.js"', `"./hardware_terminal.js?v=${version}"`);
await writeFile(hardwarePanelPath, versionedHardwarePanel);
const hardwareResourcesPath = resolve(output, "hardware_resources.js");
const versionedHardwareResources = (await readFile(hardwareResourcesPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`);
await writeFile(hardwareResourcesPath, versionedHardwareResources);
const mailCardsPath = resolve(output, "mail_cards.js");
const versionedMailCards = (await readFile(mailCardsPath, "utf8"))
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`);
await writeFile(mailCardsPath, versionedMailCards);
const mailResourcesPath = resolve(output, "mail_resources.js");
const versionedMailResources = (await readFile(mailResourcesPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./mail_cards.js"', `"./mail_cards.js?v=${version}"`);
await writeFile(mailResourcesPath, versionedMailResources);
const mailTaskPath = resolve(output, "mail_task.js");
const versionedMailTask = (await readFile(mailTaskPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./mail_cards.js"', `"./mail_cards.js?v=${version}"`);
await writeFile(mailTaskPath, versionedMailTask);
const resourcesPath = resolve(output, "resources.js");
const versionedResources = (await readFile(resourcesPath, "utf8"))
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`)
  .replaceAll('"./hardware_resources.js"', `"./hardware_resources.js?v=${version}"`)
  .replaceAll('"./mail_resources.js"', `"./mail_resources.js?v=${version}"`);
await writeFile(resourcesPath, versionedResources);
const desktopPanelPath = resolve(output, "desktop_panel.js");
const versionedDesktopPanel = (await readFile(desktopPanelPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`);
await writeFile(desktopPanelPath, versionedDesktopPanel);
const promptAdminPath = resolve(output, "prompt_admin.js");
const versionedPromptAdmin = (await readFile(promptAdminPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`);
await writeFile(promptAdminPath, versionedPromptAdmin);
const proxySettingsPath = resolve(output, "proxy_settings.js");
const versionedProxySettings = (await readFile(proxySettingsPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`);
await writeFile(proxySettingsPath, versionedProxySettings);
// data_retention.js must import the same versioned i18n.js URL as app.js so
// both share one locale singleton.
const dataRetentionPath = resolve(output, "data_retention.js");
const versionedDataRetention = (await readFile(dataRetentionPath, "utf8"))
  .replaceAll('"./i18n.js"', `"./i18n.js?v=${version}"`);
await writeFile(dataRetentionPath, versionedDataRetention);
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
  .replaceAll('"./i18n.js"', `"./i18n.js?v=${version}"`)
  .replaceAll('"./markdown.js"', `"./markdown.js?v=${version}"`);
await writeFile(knowledgeWorkspacePath, versionedKnowledgeWorkspace);
const channelsPath = resolve(output, "channels.js");
const versionedChannels = (await readFile(channelsPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./icons.js"', `"./icons.js?v=${version}"`);
await writeFile(channelsPath, versionedChannels);
const assistantPath = resolve(output, "assistant.js");
const versionedAssistant = (await readFile(assistantPath, "utf8"))
  .replaceAll('"./api.js"', `"./api.js?v=${version}"`)
  .replaceAll('"./pet_bubble.js"', `"./pet_bubble.js?v=${version}"`);
await writeFile(assistantPath, versionedAssistant);
const indexPath = resolve(output, "index.html");
const versionedIndex = (await readFile(indexPath, "utf8"))
  .replace('href="/vendor/xterm.css"', `href="/vendor/xterm.css?v=${version}"`)
  .replace('href="/styles.css"', `href="/styles.css?v=${version}"`)
  .replace('src="/vendor/xterm.js"', `src="/vendor/xterm.js?v=${version}"`)
  .replace('src="/app.js"', `src="/app.js?v=${version}"`);
await writeFile(indexPath, versionedIndex);
console.log(`Built ${output}`);
