import {api} from "./api.js";
import {t, currentLocale, formatLocaleDateTime} from "./i18n.js";
import {icon} from "./icons.js";
import {renderMarkdown} from "./markdown.js";
import type {
  KnowledgeVaultDoc,
  KnowledgeVaultDocDiff,
  KnowledgeVaultFile,
  KnowledgeVaultFileEntry,
  KnowledgeVaultDiffLine,
  KnowledgeVaultGitResponse,
  KnowledgeVaultGitStatus,
  KnowledgeVaultInbox,
  KnowledgeVaultLibrary,
  KnowledgeVaultLibraries,
  KnowledgeVaultOutlineItem,
  KnowledgeVaultRecent,
  KnowledgeVaultRecentItem,
  KnowledgeVaultSearch,
  KnowledgeVaultSearchResult,
  KnowledgeVaultSettings,
  KnowledgeVaultSkill,
  KnowledgeVaultSkills,
  KnowledgeVaultTreeNode,
  KnowledgeVaultTree,
} from "./types.js";

type VaultTab = "knowledge" | "skills";
type MobilePane = "list" | "doc";
type LoadState = "idle" | "loading" | "ready" | "error";
type SearchScope = "all" | "project" | "directory";
type PreviewTab = "diff" | "full";
type GitBusy = "" | "init" | "remote" | "commit" | "pull" | "push" | "sync";

// Settings and Git labels live in this lazy route chunk rather than the
// first-screen i18n catalog: they are only needed once the knowledge page has
// loaded, and keeping them here preserves the first-screen size budget.
const localText: Record<string, [zhCN: string, enUS: string]> = {
  "kv.filesToggle": ["切换到真实文件目录", "Switch to the real file directory"],
  "kv.filesRoot": ["vault 根目录", "Vault root"],
  "kv.filesUp": ["上一级目录", "Parent directory"],
  "kv.filesEmpty": ["这个目录没有可显示的文件。", "This directory has no visible files."],
  "kv.fileOpenManager": ["在文件管理器中打开", "Open in file manager"],
  "kv.fileDownload": ["下载文件", "Download file"],
  "kv.fileBinary": ["该文件不是文本，已提供下载。", "This file is not text; download it instead."],
  "kv.fileTooLarge": ["该文件过大，已提供下载。", "This file is too large to preview; download it instead."],
  "kv.archiveBadge": ["归档", "Archived"],
  "kv.filesSelect": ["从左侧文件目录中选择一个文件。", "Choose a file from the directory on the left."],
  "kv.skillsAutoHint": ["开启后，新建任务会默认选中该技能；创建任务时仍可手动取消。", "When on, new tasks select this skill by default; you can still remove it while creating a task."],
  "kv.skillAutoAttach": ["任务自动启用", "Auto-enable for tasks"],
  "kv.skillAutoToggleAria": ["切换「{name}」任务自动启用", "Toggle auto-enable for {name}"],
  "kv.skillSourceVault": ["vault 技能", "Vault skill"],
  "kv.skillGlobalConfirm": ["将「{name}」设为全局自动启用？所有已绑定知识库的项目在新建任务时都会默认带上这个技能。", "Enable “{name}” globally? Every project bound to the knowledge vault will preselect this skill for new tasks."],
  "kv.skillAutoEnabled": ["已开启「{name}」的任务自动启用。", "Auto-enable for “{name}” is on."],
  "kv.skillAutoDisabled": ["已关闭「{name}」的任务自动启用。", "Auto-enable for “{name}” is off."],
  "kv.skillAutoPending": ["已保存「{name}」的偏好，但知识库运行时读取未开启，暂时不会自动启用。", "The preference for “{name}” was saved, but knowledge vault runtime reads are off, so it will not take effect yet."],
  "kv.skillGlobalHint": ["开启后，所有已绑定知识库的项目新建任务时默认选中；创建任务时仍可取消。", "When on, every project bound to the knowledge vault preselects this skill for new tasks; it can still be removed there."],
  "kv.skillProjectHint": ["开启后，该项目新建任务时默认选中；创建任务时仍可取消。", "When on, new tasks in this project preselect this skill; it can still be removed there."],
  "kv.skillRuntimeOff": ["知识库运行时读取未开启，vault 技能不会自动启用。", "Knowledge vault runtime reads are off, so vault skills will not auto-enable."],
  "kv.skillIndexNotReady": ["知识库索引尚未就绪，vault 技能暂不可用。", "The knowledge vault index is not ready, so vault skills are unavailable."],
  "kv.skillProjectNotBound": ["当前项目未绑定知识库项目库，vault 技能不会自动启用。", "This project is not bound to a vault project library, so vault skills will not auto-enable."],
  "kv.skillArchiveReadonly": ["归档技能只读，不能自动启用。", "Archived skills are read-only and cannot auto-enable."],
  "kv.skillPending": ["偏好已保存，但当前条件下暂不生效。", "The preference is saved but does not take effect under the current conditions."],
  "kv.skillGlobalConfirmRequired": ["全局启用需要二次确认，请重试并确认弹窗。", "Global enabling needs the second confirmation; retry and confirm the dialog."],
  "kv.skillNotFound": ["该 vault 技能已不存在，请刷新后重试。", "This vault skill no longer exists. Refresh and try again."],
  "kv.skillVaultDisabled": ["知识库功能未启用，无法修改任务自动启用。", "The knowledge vault is disabled, so auto-enable cannot be changed."],
  "kv.blocked.title": ["知识库暂时不可用", "Knowledge vault unavailable"],
  "kv.blocked.statusMigration": ["待迁移", "Migration needed"],
  "kv.blocked.statusInit": ["待初始化", "Not initialized"],
  "kv.blocked.statusDisabled": ["已关闭", "Disabled"],
  "kv.blocked.migration": ["升级后还没有迁移或导入知识。请先完成迁移或导入，旧知识库的内容才会出现在这里。", "This upgrade has not migrated or imported knowledge yet. Complete the migration or import first; legacy knowledge will then appear here."],
  "kv.blocked.uninitialized": ["文件知识库还没有初始化，或索引尚未就绪。可以创建示例知识库、迁移或导入已有知识库。", "The file knowledge vault is not initialized or its index is not ready. Create the sample vault, migrate, or import an existing vault."],
  "kv.blocked.disabled": ["文件知识库当前已关闭。可以重新启用，或迁移/导入已有知识库。", "The file knowledge vault is off. Re-enable it, or migrate/import an existing vault."],
  "kv.blocked.action": ["去高级设置迁移或导入", "Open advanced settings to migrate or import"],
  "kv.blocked.actionInit": ["去高级设置初始化、迁移或导入", "Open advanced settings to initialize, migrate, or import"],
  "kv.blocked.note": ["旧知识库只作为迁移来源，不再在知识库页展示。", "The legacy knowledge base is only a migration source and is no longer shown on this page."],
  "kv.settingsIndexAction": ["索引操作", "Index action"],
  "kv.settingsIndexStale": ["索引待自动修复", "Index will be repaired automatically"],
  "kv.gitTitle": ["Git 同步", "Git sync"],
  "kv.gitRepo": ["仓库", "Repository"],
  "kv.gitUninitialized": ["未初始化", "Not initialized"],
  "kv.gitInitialized": ["已初始化", "Initialized"],
  "kv.gitBranch": ["分支", "Branch"],
  "kv.gitRemote": ["远程", "Remote"],
  "kv.gitRemoteUnset": ["未设置", "Not set"],
  "kv.copyVaultPath": ["复制库路径", "Copy vault path"],
  "kv.copyRemote": ["复制远程地址", "Copy remote address"],
  "kv.gitWorktree": ["工作区", "Worktree"],
  "kv.gitClean": ["干净", "Clean"],
  "kv.gitDirty": ["有 {count} 个未提交修改", "{count} uncommitted changes"],
  "kv.gitSyncState": ["同步状态", "Sync state"],
  "kv.gitInSync": ["已同步", "In sync"],
  "kv.gitAhead": ["领先 {count}", "{count} ahead"],
  "kv.gitBehind": ["落后 {count}", "{count} behind"],
  "kv.gitNoTracking": ["尚未建立远程跟踪", "No remote tracking branch yet"],
  "kv.gitLastCommit": ["最近提交", "Last commit"],
  "kv.gitLastSync": ["最近同步", "Last sync"],
  "kv.gitNone": ["无", "None"],
  "kv.gitRemoteInput": ["SSH remote", "SSH remote"],
  "kv.gitRemotePlaceholder": ["git@host:path 或 ssh://user@host/path", "git@host:path or ssh://user@host/path"],
  "kv.gitBranchInput": ["分支", "Branch"],
  "kv.gitSaveRemote": ["保存设置", "Save settings"],
  "kv.gitInit": ["初始化仓库", "Initialize repository"],
  "kv.gitCommit": ["提交", "Commit"],
  "kv.gitCommitMessage": ["提交说明（可选）", "Commit message (optional)"],
  "kv.gitCommitPlaceholder": ["留空使用默认说明", "Leave blank for the default message"],
  "kv.gitPull": ["拉取", "Pull"],
  "kv.gitPush": ["推送", "Push"],
  "kv.gitSync": ["同步", "Sync"],
  "kv.gitInitializing": ["初始化中…", "Initializing…"],
  "kv.gitSaving": ["保存中…", "Saving…"],
  "kv.gitCommitting": ["提交中…", "Committing…"],
  "kv.gitPulling": ["拉取中…", "Pulling…"],
  "kv.gitPushing": ["推送中…", "Pushing…"],
  "kv.gitSyncing": ["同步中…", "Syncing…"],
  "kv.gitUnavailable": ["当前环境没有可用的 git 命令，Git 管理已禁用。", "Git is not available in this environment; Git management is disabled."],
  "kv.gitLoadFailed": ["读取 Git 状态失败", "Failed to read Git status"],
  "kv.gitHint": ["仅支持 SSH remote，使用系统 SSH agent 或默认密钥；Web 不保存密码或 token。所有同步均为手动操作，拉取只做 fast-forward，分叉时停止而不会合并或强推。", "SSH remotes only, using your system SSH agent or default key; the Web UI never stores passwords or tokens. Every sync is manual, pulls are fast-forward only, and divergence stops instead of merging or force pushing."],
  "kv.sidebarCollapse": ["收起目录栏", "Collapse contents sidebar"],
  "kv.sidebarExpand": ["展开目录栏", "Expand contents sidebar"],
  "kv.resizeSidebar": ["调整目录栏宽度", "Resize contents sidebar"],
  "kv.activityTitle": ["动态与更新", "Activity & updates"],
  "kv.activitySubtitle": ["最近更新与待整理", "Recent updates and inbox"],
  "kv.activityRecent": ["最近更新", "Recently updated"],
  "kv.activityInbox": ["待整理", "Inbox"],
  "kv.activityRecentCount": ["{count} 篇", "{count} documents"],
  "kv.activityInboxGroupCount": ["{count} 条", "{count} items"],
  "kv.activityBackToTree": ["返回目录", "Back to contents"],
  "kv.docBackToActivity": ["返回动态", "Back to activity"],
  "kv.previewTitle": ["文档预览", "Document preview"],
  "kv.openOriginal": ["打开原文", "Open document"],
  "kv.closePreview": ["关闭预览", "Close preview"],
  "kv.previewDiff": ["变化", "Changes"],
  "kv.previewFull": ["全文", "Full text"],
  "kv.previewDiffAria": ["文档变更", "Document changes"],
  "kv.previewUncommitted": ["未提交改动", "Uncommitted changes"],
  "kv.previewCommit": ["提交 {hash}", "Commit {hash}"],
  "kv.previewDiffStats": ["+{added} −{removed}", "+{added} −{removed}"],
  "kv.previewDiffTruncated": ["变更过长，仅显示前 400 行", "Diff is long; showing the first 400 lines only"],
  "kv.previewDiffEmpty": ["与基线相同，没有可显示的差异。", "Same as the baseline; no differences to show."],
  "kv.previewDiffUnavailable": ["无法显示变更：{reason}", "Changes unavailable: {reason}"],
  "kv.previewDiffFailed": ["读取变更失败，已显示全文。", "Could not read changes; showing the full document."],
  "kv.recentShowUnchanged": ["显示仅时间更新", "Show timestamp-only updates"],
  "kv.recentHideUnchanged": ["隐藏仅时间更新", "Hide timestamp-only updates"],
  "kv.changeTimestampOnly": ["仅时间更新", "Timestamp only"],
  "kv.changeUnknown": ["变更未知", "Change unknown"],
};

function kt(key: string, values: Record<string, string | number> = {}): string {
  const [zh, en] = localText[key] ?? ["", ""];
  const template = (currentLocale() === "en-US" ? en : zh) || t(key);
  return template.replace(/\{(\w+)\}/g, (_, name: string) => String(values[name] ?? `{${name}}`));
}

// Git is only rendered by this lazy route, so its layout CSS rides in the same
// chunk instead of growing the first-screen stylesheet.
const GIT_STYLES = `
.kv-settings-notice { margin: var(--space-2) 0 0; font-size: var(--font-size-12); line-height: 1.5; overflow-wrap: anywhere; }
.kv-settings-notice.error { color: var(--color-danger); }
.kv-settings-section { margin-top: var(--space-4); padding-top: var(--space-3); border-top: 1px solid var(--color-border); min-width: 0; }
.kv-settings-section h3 { margin: 0; font-size: var(--font-size-14); }
/* Both form rows share one grid: the commit message spans the remote and
   branch tracks, and its button sits in the same auto track as 保存设置 so
   both right edges line up at every width above the stacking breakpoint. */
.kv-git-form { display: grid; grid-template-columns: minmax(0, 1fr) minmax(120px, 0.55fr) auto; gap: var(--space-2); align-items: end; min-width: 0; }
.kv-git-form label { display: grid; gap: 4px; margin: 0; min-width: 0; font-size: var(--font-size-12); color: var(--color-text-muted); }
.kv-git-commit-label { grid-column: 1 / span 2; }
#kv-git-commit { grid-column: 3; }
.kv-git-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: var(--space-2); }
.kv-settings-copy { display: flex; align-items: flex-start; gap: 6px; }
.kv-settings-copy > span { min-width: 0; overflow-wrap: anywhere; }
.kv-settings-copy .kv-copy { flex: 0 0 auto; }
@media (max-width: 760px) {
  .kv-git-form { grid-template-columns: minmax(0, 1fr); }
  .kv-git-form button { justify-self: start; }
  .kv-git-commit-label, #kv-git-commit { grid-column: 1; }
}
`;

function installGitStyles(): void {
  if (document.getElementById("kv-git-styles")) return;
  const style = document.createElement("style");
  style.id = "kv-git-styles";
  style.textContent = GIT_STYLES;
  document.head.append(style);
}

// The tree and resizable sidebar also belong to this lazy route. Their rules
// override the base vault layout once the page renders, so the first-screen
// stylesheet keeps its original size budget.
const TREE_STYLES = `
.knowledge-vault { --kv-resizer-width: 6px; --kv-tree-toggle-size: 28px; --kv-tree-icon-size: 22px; }
.kv-workspace { grid-template-columns: var(--kv-tree-width) var(--kv-resizer-width) minmax(0, 1fr) var(--kv-context-width); }
/* The resizer is omitted from the DOM while collapsed, so the grid must drop
   its column too or every following item shifts one track left and the reader
   lands in the zero-width resizer track. */
@media (min-width: 1181px) {
  .knowledge-vault[data-kv-sidebar-collapsed="true"] .kv-workspace { grid-template-columns: var(--kv-tree-width) minmax(0, 1fr) var(--kv-context-width); }
}
.kv-panel-head { align-items: center; }
.kv-panel-head span { margin-right: auto; }
.kv-sidebar-toggle {
  flex: 0 0 auto; display: inline-grid; place-items: center; width: 28px; height: 28px;
  padding: 0; border: 1px solid var(--color-border-subtle); border-radius: 5px; background: var(--color-surface);
  color: var(--color-text-muted); cursor: pointer;
}
.kv-sidebar-toggle:hover { background: var(--color-surface-muted); color: var(--color-text); border-color: var(--color-border); }
.kv-sidebar-toggle .icon { width: 15px; height: 15px; }
.kv-tree-panel.collapsed { padding: 8px 5px; overflow: hidden; }
.kv-tree-panel.collapsed .kv-panel-head { justify-content: center; padding: 2px 0 8px; border-bottom: 0; }
.kv-tree-panel.collapsed .kv-panel-head span,
.kv-tree-panel.collapsed .kv-panel-head small,
.kv-tree-panel.collapsed .kv-tree-body { display: none; }
.kv-tree-resizer {
  position: relative; align-self: stretch; width: 6px; min-height: 180px;
  border-radius: 999px; cursor: col-resize; touch-action: none;
}
.kv-tree-resizer::after {
  content: ""; position: absolute; inset: 0 2px; border-radius: 999px;
  background: var(--color-border-strong); opacity: 1; transition: background-color .12s ease, opacity .12s ease;
}
.kv-tree-resizer:hover::after, .kv-tree-resizer:focus-visible::after { background: var(--color-text-muted); opacity: 1; }
body.kv-resizing { cursor: col-resize; user-select: none; }
.kv-tree-list .kv-tree-list { padding-left: 12px; }
.kv-tree-row { gap: 6px; min-width: 0; border-radius: 5px; }
.kv-tree-row-toggle { cursor: pointer; }
.kv-tree-toggle {
  flex: 0 0 var(--kv-tree-toggle-size); width: var(--kv-tree-toggle-size); height: 30px;
  padding: 0; border: 0; border-radius: 5px; background: transparent; color: var(--color-text-muted); cursor: pointer;
}
.kv-tree-toggle .icon { width: 14px; height: 14px; }
.kv-tree-toggle:hover { color: var(--color-text); }
.kv-tree-toggle-space { flex: 0 0 var(--kv-tree-toggle-size); width: var(--kv-tree-toggle-size); height: 30px; }
.kv-tree-folder-icon, .kv-tree-doc-icon {
  flex: 0 0 var(--kv-tree-icon-size); display: inline-grid; place-items: center;
  width: var(--kv-tree-icon-size); color: var(--color-text-muted);
}
.kv-tree-folder-icon .icon, .kv-tree-doc-icon .icon { width: 15px; height: 15px; }
.kv-tree-link.kv-tree-folder-link { flex: 1 1 auto; max-width: none; font-weight: 500; }
.kv-tree-link.kv-tree-folder-title {
  justify-content: flex-start; min-height: 0; padding: 5px 6px 5px 0;
  border: 0; background: transparent; font-family: inherit; font-size: var(--font-size-13);
  text-align: left; cursor: pointer;
}
.kv-tree-title { padding-left: 6px; }
.kv-tree-link { padding: 5px 6px; }
.kv-files-path { display: flex; flex-wrap: wrap; align-items: center; gap: 4px; padding: 2px 4px 6px; font-size: 12px; color: var(--color-text-muted); }
.kv-files-list { margin-top: 4px; }
.kv-file-text { margin: 0; padding: 12px; overflow-x: auto; white-space: pre-wrap; word-break: break-word; font-size: 12px; }
.kv-tree-doc-link { padding-left: 0; }
/* The activity entry is pinned above the real directory list, outside the
   tree's folder/leaf model. It uses the same selected pill as tree rows. */
.kv-tree-home { margin-bottom: 6px; padding-bottom: 6px; border-bottom: 1px solid var(--color-border-subtle); }
.kv-activity-link { width: 100%; border: 0; background: transparent; font: inherit; text-align: left; cursor: pointer; }
.kv-activity-link > .icon { flex: 0 0 15px; width: 15px; height: 15px; color: var(--color-text-muted); }
.kv-activity-link.selected > .icon { color: var(--color-accent-ink); }
.kv-activity-link .kv-count { margin-left: auto; }
/* Documents now return to the activity page, so the back control is useful at
   every width, not only in the phone list/doc split. */
.kv-back { display: inline-flex; }
.kv-activity { display: grid; gap: var(--space-6); max-width: 980px; min-width: 0; }
/* The directory list is already on screen beside the activity page at wide
   widths; this control only exists to leave the phone's reader pane. */
.kv-activity-back { display: none; }
.kv-activity-head { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-3); }
.kv-activity-head h1 { margin: 0 0 4px; font-size: clamp(24px, 3vw, 34px); line-height: 1.2; overflow-wrap: anywhere; }
.kv-activity-block { display: grid; gap: var(--space-3); min-width: 0; }
/* The feed/inbox blocks are grid items inside the reader column. Without an
   explicit zero minimum their longest unbreakable path becomes the column's
   min-content width and pushes the page past the viewport at mid widths. */
.kv-activity-block > *, .kv-feed, .kv-feed-day, .kv-inbox, .kv-inbox-group { min-width: 0; }
/* These lists are grids sized by their items. An implicit auto track takes the
   widest row's max-content width, so pin it to the available column instead. */
.kv-feed-day ul, .kv-inbox-list { grid-template-columns: minmax(0, 1fr); }
.kv-activity-block-head { display: flex; align-items: baseline; justify-content: space-between; gap: var(--space-3); padding-bottom: 8px; border-bottom: 1px solid var(--color-border-subtle); }
.kv-activity-block-head h2 { margin: 0; font-size: var(--font-size-20); }
.kv-activity-block-head small { color: var(--color-text-muted); font-size: var(--font-size-12); }
.kv-activity-block-actions { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: flex-end; gap: var(--space-2); min-width: 0; }
.kv-recent-toggle { padding: 0; border: 0; background: transparent; color: var(--color-text-muted); font: inherit; font-size: var(--font-size-12); text-decoration: underline; cursor: pointer; }
.kv-recent-toggle:hover { color: var(--color-text); }
.kv-recent-toggle[aria-pressed="true"] { color: var(--color-accent-ink); }
.kv-change-note { align-self: flex-start; margin-top: 1px; color: var(--color-text-subtle); font-size: var(--font-size-11); font-style: italic; }
.kv-inbox-total { margin: 0; color: var(--color-text-muted); font-size: var(--font-size-13); }
.kv-inbox-group { display: grid; gap: 4px; margin-bottom: var(--space-4); }
.kv-inbox-group-head { display: flex; align-items: center; gap: 8px; min-width: 0; }
.kv-inbox-group-head h3 { margin: 0; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: var(--font-size-13); }
.kv-inbox-group-head .kv-chip { flex: 0 0 auto; }
@media (min-width: 761px) {
  .kv-workspace.kv-workspace-activity { grid-template-columns: var(--kv-tree-width) var(--kv-resizer-width) minmax(0, 1fr); }
  .knowledge-vault[data-kv-sidebar-collapsed="true"] .kv-workspace.kv-workspace-activity { grid-template-columns: var(--kv-tree-width) minmax(0, 1fr); }
}
@media (max-width: 1180px) {
  .kv-workspace { grid-template-columns: var(--kv-tree-width) var(--kv-resizer-width) minmax(0, 1fr); }
}
/* Mid widths hide the context panel, so there are only two tracks; mobile
   keeps its single stacked column and never uses this override. */
@media (min-width: 761px) and (max-width: 1180px) {
  .knowledge-vault[data-kv-sidebar-collapsed="true"] .kv-workspace { grid-template-columns: var(--kv-tree-width) minmax(0, 1fr); }
}
@media (max-width: 1180px) {
  /* The controls are inside .kv-topbar-main now; let them wrap as the same
     full-width second line they occupied before the mobile row layout. */
  .kv-topbar-main { flex-wrap: wrap; }
}
@media (max-width: 760px) {
  .knowledge-vault { --kv-tree-toggle-size: 32px; }
  .kv-workspace { grid-template-columns: minmax(0, 1fr); gap: 0; }
  .kv-tree-toggle, .kv-tree-toggle-space, .kv-tree-folder-title { height: 36px; }
  .kv-sidebar-toggle, .kv-tree-resizer { display: none; }
  .kv-tree-panel.collapsed { padding: 0; overflow: visible; }
  .kv-tree-panel.collapsed .kv-panel-head { justify-content: space-between; padding: 2px 6px 8px; border-bottom: 1px solid var(--color-border-subtle); }
  .kv-tree-panel.collapsed .kv-panel-head span, .kv-tree-panel.collapsed .kv-panel-head small { display: inline; }
  .kv-tree-panel.collapsed .kv-tree-body { display: block; }
  .kv-activity-head { flex-wrap: wrap; }
  .kv-activity-block-head { flex-wrap: wrap; }
  .kv-activity-block-actions { flex: 1 1 100%; justify-content: space-between; }
  .kv-activity-back { display: inline-flex; order: -1; margin-bottom: 0; }
  /* The title keeps its own row; tabs, search and settings share the row below
     as three group items that never wrap into a second line. */
  .kv-topbar-main { flex-direction: row; flex-wrap: wrap; align-items: center; gap: 6px 8px; }
  .kv-heading { flex: 1 1 100%; }
  .kv-tabs { flex: 1 1 auto; width: auto; }
  .kv-controls { order: 0; flex: 0 0 auto; justify-content: flex-end; margin-left: 0; gap: 6px; }
  .kv-tab { min-height: 40px; }
  .kv-search-trigger, .kv-settings-trigger { width: 40px; min-height: 40px; height: 40px; padding: 0; justify-content: center; }
}
`;

// Recent rows open this in-page preview instead of navigating. It borrows the
// search panel's floating-card language but renders the fetched document body,
// so the Owner can judge the update before committing to the reader route.
const PREVIEW_STYLES = `
.kv-preview-panel {
  position: fixed; z-index: 60; top: 72px; left: 50%; transform: translateX(-50%);
  width: min(680px, calc(100vw - 32px)); max-height: min(80dvh, 760px);
  display: flex; flex-direction: column; gap: var(--space-3);
  padding: var(--space-4); border: 1px solid var(--color-border); border-radius: var(--radius-card);
  background: var(--color-surface-raised); box-shadow: 0 24px 70px rgba(43, 33, 27, .24);
}
.kv-preview-head { display: flex; align-items: flex-start; gap: var(--space-2); }
.kv-preview-head > div { flex: 1 1 auto; min-width: 0; }
.kv-preview-head strong { display: block; font-size: var(--font-size-16); overflow-wrap: anywhere; }
.kv-preview-meta { display: flex; flex-wrap: wrap; gap: 2px 10px; margin: 4px 0 0; color: var(--color-text-muted); font-size: var(--font-size-12); overflow-wrap: anywhere; }
.kv-preview-body { flex: 1 1 auto; min-height: 120px; overflow: auto; overscroll-behavior: contain; }
.kv-preview-tools { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; }
.kv-preview-tabs { display: inline-flex; padding: 2px; border: 1px solid var(--color-border); border-radius: var(--radius-control); background: var(--color-surface); }
.kv-preview-tab {
  min-height: 32px; padding: 0 12px; border: 0; border-radius: calc(var(--radius-control) - 2px);
  background: transparent; color: var(--color-text-muted); font-size: var(--font-size-12); font-weight: 600; cursor: pointer;
}
.kv-preview-tab[aria-pressed="true"] { background: var(--color-accent); color: var(--color-text-inverse); }
.kv-preview-tab:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 1px; }
.kv-diff-body { padding: 0; border: 1px solid var(--color-border-subtle); border-radius: var(--radius-control); background: var(--color-surface); }
.kv-diff-meta {
  display: flex; flex-wrap: wrap; gap: 2px 10px; padding: 7px 10px;
  border-bottom: 1px solid var(--color-border-subtle); color: var(--color-text-muted);
  font-size: var(--font-size-12); overflow-wrap: anywhere;
}
.kv-diff-meta .kv-diff-count { font-variant-numeric: tabular-nums; }
.kv-diff-notice { margin: 0; padding: 9px 10px; color: var(--color-warning-ink); background: var(--color-warning-soft); font-size: var(--font-size-12); line-height: 1.55; }
.kv-diff { font-family: var(--font-family-mono, ui-monospace, SFMono-Regular, Menlo, Consolas, monospace); font-size: var(--font-size-12); line-height: 1.55; }
.kv-diff-row { display: grid; grid-template-columns: 42px 42px 20px minmax(0, 1fr); align-items: start; }
.kv-diff-no { padding: 0 6px; text-align: right; color: var(--color-text-muted); font-variant-numeric: tabular-nums; user-select: none; }
.kv-diff-sign { text-align: center; font-weight: 700; user-select: none; }
.kv-diff-row code { display: block; min-width: 0; padding: 0 10px 0 2px; white-space: pre-wrap; overflow-wrap: anywhere; tab-size: 4; }
.kv-diff-hunk { border-top: 1px solid var(--color-border-subtle); border-bottom: 1px solid var(--color-border-subtle); background: var(--color-surface-sunken, var(--color-surface)); }
.kv-diff-hunk code, .kv-diff-hunk .kv-diff-no { color: var(--color-text-muted); }
.kv-diff-add { background: var(--color-success-soft); }
.kv-diff-add .kv-diff-sign { color: var(--color-success-ink); }
.kv-diff-remove { background: var(--color-danger-soft); }
.kv-diff-remove .kv-diff-sign { color: var(--color-danger-ink); }
.kv-diff-empty { margin: 0; }
.kv-preview-notice { margin: 0 0 var(--space-2); padding: 9px 10px; border-radius: var(--radius-control); background: var(--color-warning-soft); color: var(--color-warning-ink); font-size: var(--font-size-12); line-height: 1.55; }
.kv-preview-actions { display: flex; justify-content: flex-end; }
.kv-preview-open {
  display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 40px; padding: 0 14px;
  border-radius: var(--radius-control); background: var(--color-accent); color: var(--color-text-inverse);
  font-size: var(--font-size-13); font-weight: 600; text-decoration: none;
}
.kv-preview-open:hover { background: var(--color-accent-hover); color: var(--color-text-inverse); }
.kv-preview-open .icon { width: 16px; height: 16px; }
@media (max-width: 760px) {
  /* Full-width sheet above the bottom navigation; the body scrolls inside it. */
  .kv-preview-panel {
    top: 0; left: 0; transform: none; width: 100vw;
    height: calc(100dvh - var(--mobile-bottom-nav-height));
    max-height: calc(100dvh - var(--mobile-bottom-nav-height));
    border-radius: 0; padding: 12px;
  }
  .kv-preview-actions .kv-preview-open { width: 100%; }
  .kv-preview-tools { gap: 6px; }
  .kv-preview-tab { min-height: 36px; }
  .kv-diff-row { grid-template-columns: 34px 34px 16px minmax(0, 1fr); }
  .kv-diff-no { padding: 0 4px; }
}
`;

function installPreviewStyles(): void {
  if (typeof document === "undefined" || document.getElementById("kv-preview-styles")) return;
  const style = document.createElement("style");
  style.id = "kv-preview-styles";
  style.textContent = PREVIEW_STYLES;
  document.head.append(style);
}

function installTreeStyles(): void {
  if (typeof document === "undefined" || document.getElementById("kv-tree-styles")) return;
  const style = document.createElement("style");
  style.id = "kv-tree-styles";
  style.textContent = TREE_STYLES;
  document.head.append(style);
}

export interface KnowledgeVaultContext {
  render: () => void;
  setMessage: (kind: "notice" | "error", message: string) => void;
  /** Speaks a transient notice without re-rendering the page. */
  announce?: (message: string) => void;
}

interface VaultState {
  initialized: boolean;
  loading: LoadState;
  error: string;
  // The main knowledge route always renders this page. When the vault switch
  // is off the libraries call reports knowledge_vault_disabled; the page then
  // shows a blocked state with a migration/import entry instead of falling
  // back to the legacy workspace UI.
  blocked: boolean;
  blockedStatus: string;
  libraries: KnowledgeVaultLibrary[];
  projects: KnowledgeVaultLibrary[];
  vaultReady: boolean;
  indexReady: boolean;
  vaultName: string;
  vaultPath: string;
  obsidianVault: string;
  obsidianReady: boolean;
  rebuiltAt: string;
  warning: string;
  tab: VaultTab;
  libraryID: string;
  typeFilter: string;
  statusFilter: string;
  tagFilter: string;
  tree: KnowledgeVaultTreeNode[];
  treeState: LoadState;
  treeError: string;
  treeKey: string;
  filesMode: boolean;
  filesPath: string;
  files: KnowledgeVaultFileEntry[];
  filesState: LoadState;
  filesError: string;
  file: KnowledgeVaultFile | null;
  fileRequestPath: string;
  fileState: LoadState;
  fileError: string;
  openAvailable: boolean;
  recentKey: string;
  recentIncludeUnchanged: boolean;
  inboxKey: string;
  expanded: Set<string>;
  selectedID: string;
  doc: KnowledgeVaultDoc | null;
  docState: LoadState;
  docError: string;
  recent: KnowledgeVaultRecentItem[];
  recentState: LoadState;
  recentError: string;
  previewID: string;
  previewDoc: KnowledgeVaultDoc | null;
  previewState: LoadState;
  previewError: string;
  previewTab: PreviewTab;
  previewDiff: KnowledgeVaultDocDiff | null;
  previewDiffState: LoadState;
  previewDiffError: string;
  inbox: KnowledgeVaultInbox | null;
  inboxState: LoadState;
  inboxError: string;
  skills: KnowledgeVaultSkill[];
  skillsState: LoadState;
  skillsError: string;
  // The catalog reports the runtime gate once, so each card can explain why
  // auto-attach would not take effect without extra requests.
  skillRuntimeRead: boolean;
  skillBusy: string;
  searchOpen: boolean;
  searchQuery: string;
  searchScope: SearchScope;
  searchType: string;
  searchStatus: string;
  searchTag: string;
  searchState: LoadState;
  searchError: string;
  searchResults: KnowledgeVaultSearchResult[];
  feedback: "" | "helped" | "stale";
  contextOpen: boolean;
  settingsOpen: boolean;
  settingsState: LoadState;
  settings: KnowledgeVaultSettings | null;
  settingsError: string;
  gitState: LoadState;
  git: KnowledgeVaultGitStatus | null;
  gitError: string;
  gitBusy: GitBusy;
  gitNotice: {kind: "notice" | "error"; message: string} | null;
  gitRemoteDraft: string;
  gitBranchDraft: string;
  gitCommitDraft: string;
  mobilePane: MobilePane;
  listScroll: number;
  docScroll: number;
  pendingSearch: number;
  sidebarWidth: number;
  sidebarCollapsed: boolean;
}

// The activity page is a fixed first row in the tree, not a vault directory.
// A sentinel in selectedID keeps it selectable without inventing a document id
// that the doc API could accidentally resolve.
const VAULT_ACTIVITY_ID = "__kv_activity__";
const SIDEBAR_WIDTH_KEY = "aha2.knowledgeVault.sidebarWidth";
const SIDEBAR_COLLAPSED_KEY = "aha2.knowledgeVault.sidebarCollapsed";
const SIDEBAR_DEFAULT_WIDTH = 270;
const SIDEBAR_MIN_WIDTH = 220;
const SIDEBAR_MAX_WIDTH = 460;
const SIDEBAR_COLLAPSED_WIDTH = 48;
const SIDEBAR_RESIZER_WIDTH = 6;
const SIDEBAR_STEP = 12;

function sidebarMaximumWidth(): number {
  const viewport = typeof window === "undefined" ? 0 : window.innerWidth;
  if (!Number.isFinite(viewport) || viewport <= 0) return SIDEBAR_MAX_WIDTH;
  // The sidebar never takes more than 40% of the viewport, even when the
  // persisted desktop width is wider than the current window.
  return Math.max(SIDEBAR_MIN_WIDTH, Math.min(SIDEBAR_MAX_WIDTH, Math.floor(viewport * 0.4)));
}

function clampSidebarWidth(value: number): number {
  if (!Number.isFinite(value)) return SIDEBAR_DEFAULT_WIDTH;
  return Math.max(SIDEBAR_MIN_WIDTH, Math.min(sidebarMaximumWidth(), Math.round(value)));
}

function readStoredSidebarWidth(): number {
  try {
    if (typeof localStorage === "undefined") return SIDEBAR_DEFAULT_WIDTH;
    const parsed = Number(localStorage.getItem(SIDEBAR_WIDTH_KEY));
    return Number.isFinite(parsed) && parsed > 0 ? clampSidebarWidth(parsed) : SIDEBAR_DEFAULT_WIDTH;
  } catch {
    return SIDEBAR_DEFAULT_WIDTH;
  }
}

function readStoredSidebarCollapsed(): boolean {
  try {
    return typeof localStorage !== "undefined" && localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === "1";
  } catch {
    return false;
  }
}

function persistSidebarWidth(width: number): void {
  try {
    if (typeof localStorage !== "undefined") localStorage.setItem(SIDEBAR_WIDTH_KEY, String(clampSidebarWidth(width)));
  } catch {
    // Storage can be unavailable in embedded webviews; the current width
    // still applies for this page.
  }
}

function persistSidebarCollapsed(collapsed: boolean): void {
  try {
    if (typeof localStorage !== "undefined") localStorage.setItem(SIDEBAR_COLLAPSED_KEY, collapsed ? "1" : "0");
  } catch {
    // See persistSidebarWidth.
  }
}

function initialSidebarWidth(): number {
  return readStoredSidebarWidth();
}

function initialState(): VaultState {
  return {
    initialized: false, loading: "idle", error: "",
    blocked: false, blockedStatus: "",
    libraries: [], projects: [], vaultReady: false, indexReady: false,
    vaultName: "", vaultPath: "", obsidianVault: "", obsidianReady: false,
    rebuiltAt: "", warning: "",
    tab: "knowledge", libraryID: "global",
    typeFilter: "", statusFilter: "", tagFilter: "",
    tree: [], treeState: "idle", treeError: "", treeKey: "", expanded: new Set(),
    filesMode: false, filesPath: "", files: [], filesState: "idle", filesError: "",
    file: null, fileRequestPath: "", fileState: "idle", fileError: "", openAvailable: false,
    recentKey: "", recentIncludeUnchanged: false, inboxKey: "",
    selectedID: VAULT_ACTIVITY_ID, doc: null, docState: "idle", docError: "",
    recent: [], recentState: "idle", recentError: "",
    previewID: "", previewDoc: null, previewState: "idle", previewError: "",
    previewTab: "diff", previewDiff: null, previewDiffState: "idle", previewDiffError: "",
    inbox: null, inboxState: "idle", inboxError: "",
    skills: [], skillsState: "idle", skillsError: "",
    skillRuntimeRead: false, skillBusy: "",
    searchOpen: false, searchQuery: "", searchScope: "all",
    searchType: "", searchStatus: "", searchTag: "",
    searchState: "idle", searchError: "", searchResults: [],
    feedback: "", contextOpen: false,
    settingsOpen: false, settingsState: "idle", settings: null, settingsError: "",
    gitState: "idle", git: null, gitError: "", gitBusy: "", gitNotice: null,
    gitRemoteDraft: "", gitBranchDraft: "", gitCommitDraft: "",
    mobilePane: "list", listScroll: 0, docScroll: 0, pendingSearch: 0,
    sidebarWidth: initialSidebarWidth(), sidebarCollapsed: readStoredSidebarCollapsed(),
  };
}

let state = initialState();
let binding = false;
let keyboardBound = false;
let hashBound = false;
let sidebarViewportBound = false;
let searchTimer: ReturnType<typeof setTimeout> | null = null;
let treeRequest = 0;
let recentRequest = 0;
let inboxRequest = 0;
let previewRequest = 0;
// Focus returns to the recent row after the preview is dismissed.
let previewTriggerID = "";
const pendingLoads = new Set<string>();
// One tree covers every library: its roots are the vault's real top-level
// directories (global/ and projects/), so the page no longer has a separate
// library/project scope selector.
const VAULT_TREE_LIBRARY = "all";

export function __setKnowledgeVaultStateForTest(patch: Partial<VaultState>): void {
  state = {...state, ...patch};
}

export function __resetKnowledgeVaultStateForTest(): void {
  state = initialState();
  binding = false;
}

// The Phase 3 settings panel calls this after a switch, init, or rebuild so the
// page cannot render libraries/tree/doc data fetched under the previous switch
// generation. Global keyboard/hash listeners stay bound; only the reload guard
// is cleared so the next visit fetches fresh data.
export function resetKnowledgeVaultPage(): void {
  state = initialState();
  binding = false;
  pendingLoads.clear();
  if (searchTimer !== null) {
    clearTimeout(searchTimer);
    searchTimer = null;
  }
  treeRequest++;
  recentRequest++;
  inboxRequest++;
}

/* Pure URI builders ------------------------------------------------------ */

function encodeURIPath(value: string): string {
  const normalized = String(value ?? "").replaceAll("\\", "/");
  const drive = normalized.match(/^([A-Za-z]):(.*)$/);
  if (drive) {
    return `${drive[1]}:${drive[2].split("/").map(encodeURIComponent).join("/")}`;
  }
  return normalized.split("/").map(encodeURIComponent).join("/");
}

export function vscodeLocalURI(absolutePath: string): string {
  const normalized = String(absolutePath ?? "").replaceAll("\\", "/").replace(/^\/+/, "");
  return `vscode://file/${encodeURIPath(normalized)}`;
}

export function obsidianURI(vaultName: string, relativePath: string): string {
  const file = String(relativePath ?? "").replaceAll("\\", "/").replace(/\.md$/i, "");
  return `obsidian://open?vault=${encodeURIComponent(vaultName)}&file=${encodeURIComponent(file)}`;
}

export function vaultQuery(params: Record<string, string | number | undefined | null>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") continue;
    query.set(key, String(value));
  }
  return query.toString();
}

export function docHash(doc: {id: string; scope?: string; project_id?: string}): string {
  const scope = doc.scope === "global" || doc.scope === "project" || doc.scope === "archive" ? doc.scope : "";
  return `#knowledge?${vaultQuery({doc: doc.id, scope, project: doc.project_id || ""})}`;
}

export function parseDocHash(hash: string): {id: string; library: string; project: string} | null {
  const raw = String(hash ?? "").replace(/^#/, "");
  const [name, query = ""] = raw.split("?");
  if (name !== "knowledge") return null;
  const params = new URLSearchParams(query);
  const id = params.get("doc") || "";
  const scope = params.get("scope") || params.get("library") || "";
  const project = params.get("project") || "";
  let library = "";
  if (scope === "global") library = "global";
  else if (scope === "project" && project) library = `project:${project}`;
  else if (scope === "archive" && project) library = `archive:${project}`;
  else if (scope) library = scope;
  return {id, library, project};
}

export function escapeVaultHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

export function highlightVaultText(value: unknown, query: string): string {
  const text = String(value ?? "");
  const needle = String(query ?? "").trim();
  if (!needle) return escapeVaultHTML(text);
  const index = text.toLowerCase().indexOf(needle.toLowerCase());
  if (index < 0) return escapeVaultHTML(text);
  return `${escapeVaultHTML(text.slice(0, index))}<mark>${escapeVaultHTML(text.slice(index, index + needle.length))}</mark>${escapeVaultHTML(text.slice(index + needle.length))}`;
}

function localDayKey(value: string | number | Date): string {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

export function groupRecentByDay(items: KnowledgeVaultRecentItem[], now: Date = new Date()): Array<{key: string; label: string; items: KnowledgeVaultRecentItem[]}> {
  const today = localDayKey(now);
  const yesterday = localDayKey(new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1));
  const groups = new Map<string, KnowledgeVaultRecentItem[]>();
  for (const item of items) {
    // Group by when the content changed, not by a later mtime/export touch.
    const key = localDayKey(item.changed_at || item.modified_at || item.updated || "");
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key)!.push(item);
  }
  return [...groups.entries()].map(([key, groupItems]) => ({
    key,
    label: key === today ? t("kv.today") : key === yesterday ? t("kv.yesterday") : (key ? formatLocaleDateTime(`${key}T00:00:00`, {year: "numeric", month: "short", day: "numeric"}) : t("kv.unknownDate")),
    items: groupItems,
  }));
}

/* Small view helpers ----------------------------------------------------- */

function typeLabel(type: string): string {
  const key = `kv.type.${type}`;
  const label = t(key);
  return label === key ? type : label;
}

function statusLabel(status: string): string {
  const key = `kv.status.${status}`;
  const label = t(key);
  return label === key ? status : label;
}

function safeDateTime(value?: string): string {
  if (!value) return t("kv.never");
  return formatLocaleDateTime(value, {year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit"});
}

export function vaultEmptyState(kind: "loading" | "error" | "empty", message = "", retry = false): string {
  const title = kind === "loading" ? t("kv.loading") : kind === "error" ? t("kv.error") : t("kv.empty");
  const body = message || (kind === "empty" ? t("kv.emptyHint") : "");
  return `<div class="kv-state kv-state-${kind}" role="status" aria-live="polite">
    <strong>${escapeVaultHTML(title)}</strong>
    ${body ? `<p>${escapeVaultHTML(body)}</p>` : ""}
    ${retry ? `<button type="button" class="secondary" data-kv-retry>${t("kv.retry")}</button>` : ""}
  </div>`;
}

function badge(text: string, kind = ""): string {
  return `<span class="kv-badge${kind ? ` kv-badge-${kind}` : ""}">${escapeVaultHTML(text)}</span>`;
}

function metaBadges(item: {type: string; status: string}): string {
  return `${badge(typeLabel(item.type), "type")}${badge(statusLabel(item.status), item.status === "stale" || item.status === "pending" ? "warn" : "status")}`;
}

function openActionLabel(base: string, context = ""): string {
  return context ? t("kv.openActionContext", {action: base, name: context}) : base;
}

function openActions(absolutePath: string, relativePath: string, obsidianFile = "", options: {compact?: boolean; context?: string; copyKey?: string} = {}): string {
  const local = vscodeLocalURI(absolutePath);
  const obsidian = state.obsidianVault ? obsidianURI(state.obsidianVault, obsidianFile || relativePath) : "";
  const obsidianEnabled = Boolean(obsidian) && state.obsidianReady;
  const vscodeLabel = openActionLabel(t("kv.openVSCodeLocal"), options.context);
  const obsidianLabel = openActionLabel(t("kv.openObsidian"), options.context);
  const copyLabel = openActionLabel(t(options.copyKey || "kv.copyPath"), options.context);
  const obsidianHint = `${obsidianLabel}：${t("kv.obsidianUnregistered")}`;
  return `<div class="kv-open-actions${options.compact ? " compact" : ""}">
    <a class="kv-open" href="${escapeVaultHTML(local)}" target="_blank" rel="noopener" title="${escapeVaultHTML(vscodeLabel)}" aria-label="${escapeVaultHTML(vscodeLabel)}">${icon("monitor")}</a>
    ${obsidianEnabled
      ? `<a class="kv-open" href="${escapeVaultHTML(obsidian)}" target="_blank" rel="noopener" title="${escapeVaultHTML(obsidianLabel)}" aria-label="${escapeVaultHTML(obsidianLabel)}">${icon("branch")}</a>`
      : `<button type="button" class="kv-open" disabled title="${escapeVaultHTML(obsidianHint)}" aria-label="${escapeVaultHTML(obsidianHint)}">${icon("branch")}</button>`}
    <button type="button" class="kv-open kv-copy" data-kv-copy="${escapeVaultHTML(absolutePath)}" title="${escapeVaultHTML(copyLabel)}" aria-label="${escapeVaultHTML(copyLabel)}">${icon("copy")}</button>
  </div>`;
}

/* Directory tree --------------------------------------------------------- */

const VAULT_INDEX_FILE = "index.md";

function normalizeTreePath(value: unknown): string {
  return String(value ?? "").replaceAll("\\", "/").replace(/^\/+|\/+$/g, "");
}

// Inbox documents are listed only on the "待整理" page. The browse tree never
// shows them, matching Reader.Inbox: any path segment named inbox is excluded.
function isInboxVaultPath(value: string): boolean {
  return normalizeTreePath(value).split("/").some(segment => segment.toLowerCase() === "inbox");
}

function treeBaseName(value: string): string {
  const normalized = normalizeTreePath(value);
  const separator = normalized.lastIndexOf("/");
  return separator < 0 ? normalized : normalized.slice(separator + 1);
}

function treeDirectoryPath(value: string): string {
  const normalized = normalizeTreePath(value);
  const separator = normalized.lastIndexOf("/");
  return separator < 0 ? "" : normalized.slice(0, separator);
}

export function isVaultIndexPath(value: string): boolean {
  return treeBaseName(value).toLowerCase() === VAULT_INDEX_FILE;
}

function treeScopeRoot(value: string): string {
  const segments = normalizeTreePath(value).split("/").filter(Boolean);
  if (segments[0] === "global") return "global";
  if ((segments[0] === "projects" || segments[0] === "archive" || segments[0] === "orphans") && segments.length >= 2) {
    return `${segments[0]}/${segments[1]}`;
  }
  return segments.length >= 2 ? segments[0] : "";
}

function cloneVaultDocNode(node: KnowledgeVaultTreeNode): KnowledgeVaultTreeNode {
  return {
    id: node.id,
    title: node.title,
    path: normalizeTreePath(node.path),
    type: node.type,
    status: node.status,
    scope: node.scope,
    project_id: node.project_id,
    updated: node.updated,
    is_index: isVaultIndexPath(node.path),
    system: false,
    children: [],
  };
}

// The server tree already includes every index document and leaf, but it uses
// the index document's title as the folder label and nests by parent/index
// links. Flattening by path lets the UI rebuild the real vault directories:
// directory names always come from the path basename.
function collectVaultDocuments(nodes: KnowledgeVaultTreeNode[], documents: Map<string, KnowledgeVaultTreeNode>): void {
  for (const node of nodes) {
    const relative = normalizeTreePath(node.path);
    if (isInboxVaultPath(relative)) continue;
    if (relative && /\.md$/i.test(relative)) {
      const key = `${node.scope || ""}\u0000${node.project_id || ""}\u0000${relative}`;
      if (!documents.has(key)) documents.set(key, cloneVaultDocNode(node));
    }
    collectVaultDocuments(node.children || [], documents);
  }
}

// The generated directory list uses vault-relative wikilinks such as
// [[global/general/topic|主题]]. Resolve them against the loaded tree so the
// knowledge reader can open the target instead of showing raw brackets.
function vaultWikilinkIndex(): Map<string, KnowledgeVaultTreeNode> {
  const byPath = new Map<string, KnowledgeVaultTreeNode>();
  const visit = (nodes: KnowledgeVaultTreeNode[]): void => {
    for (const node of nodes) {
      const relative = normalizeTreePath(node.path).replace(/\.md$/i, "");
      // Virtual folder rows (no index.md) are navigation-only, not documents.
      if (relative && !node.id.startsWith("vault-dir:")) byPath.set(relative, node);
      visit(node.children || []);
    }
  };
  visit(state.tree);
  return byPath;
}

function uniqueVaultNode(nodes: KnowledgeVaultTreeNode[]): KnowledgeVaultTreeNode | null {
  const seen = new Map<string, KnowledgeVaultTreeNode>();
  for (const node of nodes) seen.set(node.id, node);
  return seen.size === 1 ? [...seen.values()][0] : null;
}

// The generated directory list stores Obsidian wikilinks ([[path|label]]). The
// shared markdown renderer keeps those literal on purpose, so the reader turns
// list-item wikilinks into ordinary hash links before rendering; the anchors
// are swapped for real document links once the vault tree is available.
const vaultWikilinkHrefPrefix = "#vault-wikilink=";

export function rewriteVaultWikilinks(markdown: string): string {
  let fence = "";
  return markdown.split("\n").map(line => {
    const fenceMarker = line.match(/^ {0,3}(`{3,}|~{3,})/);
    if (fenceMarker) {
      if (!fence) fence = fenceMarker[1][0];
      else if (fenceMarker[1][0] === fence) fence = "";
      return line;
    }
    if (fence) return line;
    const match = line.match(/^([ \t]*[-*+][ \t]+)\[\[(.+?)\]\](.*)$/);
    if (!match) return line;
    const [, bullet, inner, rest] = match;
    const separator = inner.indexOf("|");
    const target = (separator >= 0 ? inner.slice(0, separator) : inner).trim();
    const label = (separator >= 0 ? inner.slice(separator + 1) : inner).trim() || target;
    if (!target) return line;
    const escapedLabel = label.replace(/[\\\[\]]/g, "\\$&");
    return `${bullet}[${escapedLabel}](${vaultWikilinkHrefPrefix}${encodeURIComponent(target)})${rest}`;
  }).join("\n");
}

export function resolveVaultWikilink(target: string, byPath: Map<string, KnowledgeVaultTreeNode>): KnowledgeVaultTreeNode | null {
  const normalized = normalizeTreePath(target).trim().replace(/\.md$/i, "").replace(/\/+$/, "");
  if (!normalized) return null;
  const direct = byPath.get(normalized);
  if (direct) return direct;
  // Generated lists name a directory's page as `<dir>/index`.
  const directory = byPath.get(normalized.replace(/\/index$/i, ""));
  if (directory) return directory;
  // A link may also name the directory rather than its index page.
  const nested = byPath.get(`${normalized}/index`);
  if (nested) return nested;
  // Obsidian also accepts a shortest-unique path ([[topic]]) or a title.
  const suffix = `/${normalized}`;
  const bySuffix = uniqueVaultNode([...byPath].filter(([relative]) => relative.endsWith(suffix)).map(([, node]) => node));
  if (bySuffix) return bySuffix;
  return uniqueVaultNode([...byPath.values()].filter(node => node.title === target.trim()));
}

// A single library tree is rooted at its library directory (global/, or
// projects/<slug>/), which must not appear as a row. The mixed "all libraries"
// view keeps those scope directories visible instead.
export function vaultTreeRoot(documents: KnowledgeVaultTreeNode[], libraryID = ""): string {
  if (documents.length === 0) return "";
  if (libraryID === "global") {
    return documents.some(doc => normalizeTreePath(doc.path).startsWith("global/")) ? "global" : "";
  }
  if (libraryID.startsWith("project:") || libraryID.startsWith("archive:") || libraryID === "orphan") {
    const roots = new Set(documents.map(doc => treeScopeRoot(doc.path)).filter(root => root && root !== "global"));
    return roots.size === 1 ? [...roots][0] : "";
  }
  return "";
}

interface VaultDirectoryBucket {
  name: string;
  path: string;
  index?: KnowledgeVaultTreeNode;
  directories: Map<string, VaultDirectoryBucket>;
  documents: KnowledgeVaultTreeNode[];
}

function newVaultDirectory(path: string): VaultDirectoryBucket {
  return {name: treeBaseName(path), path, directories: new Map(), documents: []};
}

function ensureVaultDirectory(root: VaultDirectoryBucket, directories: Map<string, VaultDirectoryBucket>, target: string, rootPath: string): VaultDirectoryBucket {
  const normalized = normalizeTreePath(target);
  if (!normalized || normalized === rootPath) return root;
  const existing = directories.get(normalized);
  if (existing) return existing;
  const parentPath = treeDirectoryPath(normalized);
  const parent = parentPath ? ensureVaultDirectory(root, directories, parentPath, rootPath) : root;
  const bucket = newVaultDirectory(normalized);
  parent.directories.set(bucket.name, bucket);
  directories.set(normalized, bucket);
  return bucket;
}

function representativeVaultDocument(bucket: VaultDirectoryBucket): KnowledgeVaultTreeNode | undefined {
  if (bucket.index) return bucket.index;
  if (bucket.documents.length > 0) return bucket.documents[0];
  for (const child of bucket.directories.values()) {
    const found = representativeVaultDocument(child);
    if (found) return found;
  }
  return undefined;
}

function compareVaultTreeText(left: string, right: string): number {
  const a = left.toLowerCase();
  const b = right.toLowerCase();
  if (a !== b) return a < b ? -1 : 1;
  if (left === right) return 0;
  return left < right ? -1 : 1;
}

function vaultDirectoryChildren(bucket: VaultDirectoryBucket): KnowledgeVaultTreeNode[] {
  const folders = [...bucket.directories.values()]
    .sort((left, right) => compareVaultTreeText(left.name, right.name))
    .map(vaultFolderNode);
  const documents = bucket.documents
    .slice()
    .sort((left, right) => compareVaultTreeText(left.title, right.title) || compareVaultTreeText(left.id, right.id))
    .map(doc => ({...doc, children: [] as KnowledgeVaultTreeNode[]}));
  return [...folders, ...documents];
}

function vaultFolderNode(bucket: VaultDirectoryBucket): KnowledgeVaultTreeNode {
  const index = bucket.index;
  const representative = representativeVaultDocument(bucket);
  return {
    id: index?.id || `vault-dir:${bucket.path}`,
    title: bucket.name,
    path: bucket.path,
    type: "folder",
    status: index?.status || "",
    scope: index?.scope || representative?.scope || "",
    project_id: index?.project_id || representative?.project_id,
    updated: index?.updated,
    is_index: Boolean(index),
    system: false,
    has_index: Boolean(index),
    children: vaultDirectoryChildren(bucket),
  };
}

// Rebuilds the selectable tree from the API's real vault paths. Each directory
// becomes a folder row named after its path segment; its index.md stays the
// folder's document and is never repeated as a leaf. Inbox paths are dropped by
// collectVaultDocuments because those documents belong to the "待整理" page.
export function buildVaultDirectoryTree(nodes: KnowledgeVaultTreeNode[], libraryID = ""): KnowledgeVaultTreeNode[] {
  const documentsByKey = new Map<string, KnowledgeVaultTreeNode>();
  collectVaultDocuments(nodes || [], documentsByKey);
  const documents = [...documentsByKey.values()];
  if (documents.length === 0) return [];

  const rootPath = vaultTreeRoot(documents, libraryID);
  const root = newVaultDirectory(rootPath);
  const directories = new Map<string, VaultDirectoryBucket>([[rootPath, root]]);
  for (const doc of documents) {
    const relative = normalizeTreePath(doc.path);
    const directoryPath = treeDirectoryPath(relative);
    const bucket = ensureVaultDirectory(root, directories, directoryPath, rootPath);
    if (isVaultIndexPath(relative)) {
      // The library root index is the page behind the tree, not a folder row.
      if (directoryPath === rootPath) continue;
      if (!bucket.index) bucket.index = doc;
      continue;
    }
    bucket.documents.push(doc);
  }
  return vaultDirectoryChildren(root);
}

/* Page render ------------------------------------------------------------ */

function vaultRootAttributes(): string {
  const treeWidth = state.sidebarCollapsed ? SIDEBAR_COLLAPSED_WIDTH : state.sidebarWidth;
  const resizerWidth = state.sidebarCollapsed ? 0 : SIDEBAR_RESIZER_WIDTH;
  return `style="--kv-tree-width:${treeWidth}px;--kv-resizer-width:${resizerWidth}px" data-kv-sidebar-collapsed="${state.sidebarCollapsed}"`;
}

function blockedPageHTML(): string {
  const migration = state.blockedStatus === "needs_migration";
  const uninitialized = state.blockedStatus === "needs_rebuild";
  const body = migration
    ? kt("kv.blocked.migration")
    : uninitialized
      ? kt("kv.blocked.uninitialized")
      : kt("kv.blocked.disabled");
  const status = migration
    ? `<span class="kv-badge kv-badge-warn">${kt("kv.blocked.statusMigration")}</span>`
    : uninitialized
      ? `<span class="kv-badge kv-badge-warn">${kt("kv.blocked.statusInit")}</span>`
      : `<span class="kv-badge">${kt("kv.blocked.statusDisabled")}</span>`;
  const kind = migration ? "needs_migration" : uninitialized ? "needs_rebuild" : "disabled";
  const action = migration ? kt("kv.blocked.action") : kt("kv.blocked.actionInit");
  return `<div class="knowledge-vault knowledge-vault-blocked" data-kv-blocked="${kind}">
    <header class="kv-topbar"><div class="kv-topbar-main"><div class="kv-heading"><strong>${t("kv.title")}</strong><small>${t("kv.subtitle")}</small></div></div></header>
    <div class="kv-state kv-state-blocked" role="status">
      ${status}
      <strong>${kt("kv.blocked.title")}</strong>
      <p>${escapeVaultHTML(body)}</p>
      <div class="kv-blocked-actions"><button type="button" class="primary" data-kv-open-vault-settings>${action}</button></div>
      <p class="kv-blocked-note">${kt("kv.blocked.note")}</p>
    </div>
  </div>`;
}

export function renderKnowledgeVaultPage(_context: KnowledgeVaultContext): string {
  installTreeStyles();
  installPreviewStyles();
  // The main knowledge navigation always renders this page. A disabled vault
  // gets an explicit blocked state and a migration/import entry; it never
  // falls back to the legacy workspace UI.
  if (state.blocked) return blockedPageHTML();
  if (!state.initialized || state.loading === "loading") {
    return `<div class="knowledge-vault" data-kv-tab="${state.tab}" ${vaultRootAttributes()}>${vaultEmptyState("loading")}</div>`;
  }
  if (state.loading === "error") {
    return `<div class="knowledge-vault" data-kv-tab="${state.tab}" ${vaultRootAttributes()}>${vaultEmptyState("error", state.error, true)}</div>`;
  }
  return `<div class="knowledge-vault${state.contextOpen ? " context-open" : ""}" data-kv-tab="${state.tab}" ${vaultRootAttributes()}>
    ${topbarHTML()}
    ${searchPanelHTML()}
    ${previewPanelHTML()}
    <div class="kv-content" data-kv-mobile-pane="${state.mobilePane}">
      ${state.tab === "knowledge" ? knowledgeBodyHTML() : skillsBodyHTML()}
    </div>
    ${settingsDialogHTML()}
  </div>`;
}

function topbarHTML(): string {
  // Recent and the inbox now live on the activity page, so the top level keeps
  // only the two true sections: knowledge and skills.
  const tabs: Array<[VaultTab, string, string]> = [
    ["knowledge", "knowledge", "kv.tab.knowledge"],
    ["skills", "bot", "kv.tab.skills"],
  ];
  return `<header class="kv-topbar">
    <div class="kv-topbar-main">
      <div class="kv-heading"><strong>${t("kv.title")}</strong><small>${t("kv.subtitle")}</small></div>
      <nav class="kv-tabs" aria-label="${t("kv.sections")}">
        ${tabs.map(([tab, glyph, key]) => `<button type="button" aria-current="${state.tab === tab ? "page" : "false"}" class="kv-tab kv-tab-${tab}" data-kv-tab-button="${tab}">${icon(glyph)}<span>${t(key)}</span></button>`).join("")}
      </nav>
      <div class="kv-controls">
        <button type="button" class="kv-search-trigger" data-kv-search-open aria-label="${t("kv.searchShortcut")}" title="${t("kv.searchShortcut")}">${icon("search")}<span>${t("kv.search")}</span></button>
        <button type="button" class="kv-settings-trigger icon-button" data-kv-settings aria-label="${t("kv.settings")}" title="${t("kv.settings")}">${icon("settings")}</button>
      </div>
    </div>
  </header>`;
}

function searchPanelHTML(): string {
  const typeOptions = ["", "navigation", "decision", "practice", "diagnostic", "reference", "architecture"].map(type => `<option value="${type}"${state.searchType === type ? " selected" : ""}>${type ? typeLabel(type) : t("kv.allTypes")}</option>`).join("");
  const statusOptions = ["", "verified", "stale", "pending", "draft", "unverified", "conflict"].map(value => `<option value="${value}"${state.searchStatus === value ? " selected" : ""}>${value ? statusLabel(value) : t("kv.allStatuses")}</option>`).join("");
  const scopeOptions: Array<[SearchScope, string]> = [["all", t("kv.searchScopeAll")], ["project", t("kv.searchScopeProject")], ["directory", t("kv.searchScopeDirectory")]];
  return `<section class="kv-search-panel" ${state.searchOpen ? "" : "hidden"} aria-label="${t("kv.search")}" role="dialog" aria-modal="false">
    <div class="kv-search-head">
      <label class="kv-search-field">${icon("search")}<input id="kv-search" type="search" value="${escapeVaultHTML(state.searchQuery)}" placeholder="${t("kv.searchPlaceholder")}" aria-label="${t("kv.searchPlaceholder")}" autocomplete="off" data-allow-rerender="true"></label>
      <button type="button" class="icon-button" data-kv-search-close aria-label="${t("kv.closeSearch")}" title="${t("kv.closeSearch")}">${icon("close")}</button>
    </div>
    <div class="kv-search-filters">
      <label><span>${t("kv.searchScope")}</span><select id="kv-search-scope">${scopeOptions.map(([value, label]) => `<option value="${value}"${state.searchScope === value ? " selected" : ""}>${escapeVaultHTML(label)}</option>`).join("")}</select></label>
      <label><span>${t("kv.type")}</span><select id="kv-search-type">${typeOptions}</select></label>
      <label><span>${t("kv.status")}</span><select id="kv-search-status">${statusOptions}</select></label>
      <label><span>${t("kv.tag")}</span><input id="kv-search-tag" type="text" value="${escapeVaultHTML(state.searchTag)}" placeholder="${t("kv.tagPlaceholder")}" data-allow-rerender="true"></label>
    </div>
    <div class="kv-search-results" aria-live="polite">
      ${searchResultsHTML()}
    </div>
  </section>`;
}

function previewPanelHTML(): string {
  if (!state.previewID) return "";
  const summary = state.recent.find(item => item.id === state.previewID) || null;
  const doc = state.previewState === "ready" ? state.previewDoc : null;
  const title = doc?.title || summary?.title || kt("kv.previewTitle");
  const updated = doc ? (doc.modified_at || doc.updated) : summary?.modified_at;
  const path = doc?.path || summary?.path || "";
  const diff = state.previewDiff;
  const diffUnavailable = state.previewDiffState === "ready" && diff !== null && !diff.available;
  const markdownBody = doc ? `<div class="markdown-body">${renderMarkdown(rewriteVaultWikilinks(doc.body))}</div>` : "";
  const tabs = `<div class="kv-preview-tools">
    <div class="kv-preview-tabs" aria-label="${kt("kv.previewDiffAria")}">
      <button type="button" class="kv-preview-tab" data-kv-preview-tab="diff" aria-pressed="${state.previewTab === "diff"}">${kt("kv.previewDiff")}</button>
      <button type="button" class="kv-preview-tab" data-kv-preview-tab="full" aria-pressed="${state.previewTab === "full"}">${kt("kv.previewFull")}</button>
    </div>
  </div>`;
  let body: string;
  if (state.previewState === "error") {
    // The preview has its own retry button; the shared data-kv-retry handler
    // would retry whichever other panel happens to be in an error state.
    body = `${vaultEmptyState("error", state.previewError)}<button type="button" class="secondary" data-kv-preview-retry>${t("kv.retry")}</button>`;
  } else if (!doc) {
    body = `<div class="kv-preview-body">${vaultEmptyState("loading")}</div>`;
  } else if (state.previewTab === "diff" && state.previewDiffState !== "idle") {
    if (state.previewDiffState === "loading") {
      body = `<div class="kv-preview-body">${vaultEmptyState("loading")}</div>`;
    } else if (state.previewDiffState === "error") {
      body = `<div class="kv-preview-body"><p class="kv-preview-notice">${kt("kv.previewDiffFailed")}</p>${markdownBody}</div>`;
    } else if (diffUnavailable) {
      body = `<div class="kv-preview-body"><p class="kv-preview-notice">${diffUnavailableText(diff)}</p>${markdownBody}</div>`;
    } else if (!diff || diff.lines.length === 0) {
      body = `<div class="kv-preview-body"><p class="kv-preview-notice">${kt("kv.previewDiffEmpty")}</p></div>`;
    } else {
      body = `<div class="kv-preview-body kv-diff-body">${diffMetaHTML(diff)}${renderDiffHTML(diff)}</div>`;
    }
  } else {
    // The full-text view stays on the previous markdown markup. When the diff
    // is unavailable the reason sits above the body instead of replacing it.
    const notice = diffUnavailable
      ? `<p class="kv-preview-notice">${diffUnavailableText(diff)}</p>`
      : state.previewDiffState === "error"
        ? `<p class="kv-preview-notice">${kt("kv.previewDiffFailed")}</p>`
        : "";
    body = notice
      ? `<div class="kv-preview-body">${notice}${markdownBody}</div>`
      : `<div class="markdown-body kv-preview-body">${renderMarkdown(rewriteVaultWikilinks(doc.body))}</div>`;
  }
  const targetID = doc?.id || state.previewID;
  const href = doc
    ? docHash({id: doc.id, scope: doc.scope, project_id: doc.project_id})
    : docHash({id: state.previewID, scope: summary?.scope, project_id: summary?.project_id});
  return `<section class="kv-preview-panel" data-kv-preview-panel role="dialog" aria-modal="false" aria-labelledby="kv-preview-title">
    <div class="kv-preview-head">
      <div>
        <strong id="kv-preview-title">${escapeVaultHTML(title)}</strong>
        <p class="kv-preview-meta">${updated ? `<span>${t("kv.updated")}: ${escapeVaultHTML(safeDateTime(updated))}</span>` : ""}${path ? `<span>${escapeVaultHTML(path)}</span>` : ""}</p>
      </div>
      <button type="button" class="icon-button" data-kv-preview-close aria-label="${kt("kv.closePreview")}" title="${kt("kv.closePreview")}">${icon("close")}</button>
    </div>
    ${tabs}
    ${body}
    <footer class="kv-preview-actions">
      <a class="kv-preview-open" href="${escapeVaultHTML(href)}" data-kv-doc="${escapeVaultHTML(targetID)}">${icon("panel")}<span>${kt("kv.openOriginal")}</span></a>
    </footer>
  </section>`;
}

function diffUnavailableText(diff: KnowledgeVaultDocDiff): string {
  const reason = escapeVaultHTML(diff.reason || kt("kv.previewDiffEmpty"));
  return kt("kv.previewDiffUnavailable", {reason});
}

function diffBaselineLabel(diff: KnowledgeVaultDocDiff): string {
  if (diff.baseline === "uncommitted") return kt("kv.previewUncommitted");
  const hash = (diff.commit || diff.baseline || "").slice(0, 8);
  return hash ? kt("kv.previewCommit", {hash}) : "";
}

function diffMetaHTML(diff: KnowledgeVaultDocDiff): string {
  const parts: string[] = [];
  const baseline = diffBaselineLabel(diff);
  if (baseline) parts.push(`<span>${escapeVaultHTML(baseline)}</span>`);
  if (diff.committed_at && diff.baseline !== "uncommitted") parts.push(`<span>${escapeVaultHTML(safeDateTime(diff.committed_at))}</span>`);
  parts.push(`<span class="kv-diff-count">${escapeVaultHTML(kt("kv.previewDiffStats", {added: diff.added, removed: diff.removed}))}</span>`);
  if (diff.truncated) parts.push(`<span>${escapeVaultHTML(kt("kv.previewDiffTruncated"))}</span>`);
  return `<div class="kv-diff-meta">${parts.join("")}</div>`;
}

function renderDiffHTML(diff: KnowledgeVaultDocDiff): string {
  const rows = diff.lines.map((line: KnowledgeVaultDiffLine) => {
    if (line.kind === "hunk") {
      return `<div class="kv-diff-row kv-diff-hunk"><span class="kv-diff-no"></span><span class="kv-diff-no"></span><span class="kv-diff-sign"></span><code>${escapeVaultHTML(line.text)}</code></div>`;
    }
    const sign = line.kind === "add" ? "+" : line.kind === "remove" ? "-" : " ";
    const oldNo = line.kind === "add" ? "" : String(line.old_no || "");
    const newNo = line.kind === "remove" ? "" : String(line.new_no || "");
    return `<div class="kv-diff-row kv-diff-${line.kind}"><span class="kv-diff-no">${oldNo}</span><span class="kv-diff-no">${newNo}</span><span class="kv-diff-sign" aria-hidden="true">${sign}</span><code>${escapeVaultHTML(line.text)}</code></div>`;
  }).join("");
  return `<div class="kv-diff" role="group" aria-label="${kt("kv.previewDiffAria")}">${rows}</div>`;
}

function searchResultsHTML(): string {
  if (state.searchState === "loading") return vaultEmptyState("loading");
  if (state.searchState === "error") return vaultEmptyState("error", state.searchError, true);
  if (!state.searchQuery.trim() && !state.searchTag.trim()) return `<p class="kv-hint">${t("kv.searchHint")}</p>`;
  if (state.searchState === "ready" && state.searchResults.length === 0) return vaultEmptyState("empty");
  return state.searchResults.map(item => `<div class="kv-search-result">
    <a class="kv-search-result-link" href="${escapeVaultHTML(docHash({id: item.id, scope: item.scope, project_id: item.project_id}))}" data-kv-doc="${escapeVaultHTML(item.id)}">
      <span class="kv-search-result-main"><strong>${highlightVaultText(item.title, state.searchQuery)}</strong><small>${escapeVaultHTML(item.scope || "")}${item.project_id ? ` · ${escapeVaultHTML(item.project_id)}` : ""} · ${highlightVaultText(item.path, state.searchQuery)}</small></span>
      ${item.snippet ? `<p>${highlightVaultText(item.snippet, state.searchQuery)}</p>` : ""}
    </a>
    <span class="kv-search-result-meta">${metaBadges(item)}${item.archive || item.scope === "archive" ? badge(kt("kv.archiveBadge"), "warn") : ""}${item.tags?.length ? item.tags.map(tag => `<button type="button" class="kv-tag" data-kv-tag="${escapeVaultHTML(tag)}">#${escapeVaultHTML(tag)}</button>`).join("") : ""}</span>
  </div>`).join("");
}

function isActivitySelected(): boolean {
  return state.selectedID === VAULT_ACTIVITY_ID;
}

function knowledgeBodyHTML(): string {
  const sidebarLabel = state.sidebarCollapsed ? kt("kv.sidebarExpand") : kt("kv.sidebarCollapse");
  const activity = isActivitySelected();
  // The activity page consumes the reader column only; keeping an empty
  // context column beside it would leave a dead gutter at wide widths.
  return `<div class="kv-workspace${activity ? " kv-workspace-activity" : ""}" data-kv-mobile-pane="${state.mobilePane}">
    <nav id="kv-tree-panel" class="kv-tree-panel${state.sidebarCollapsed ? " collapsed" : ""}" aria-label="${t("kv.tree")}">
      <div class="kv-panel-head">
        <button type="button" class="kv-sidebar-toggle icon-button" data-kv-sidebar-toggle aria-expanded="${!state.sidebarCollapsed}" aria-controls="kv-tree-panel" aria-label="${escapeVaultHTML(sidebarLabel)}" title="${escapeVaultHTML(sidebarLabel)}">${icon(state.sidebarCollapsed ? "chevron-right" : "chevron-left")}</button>
        <span>${t("kv.tree")}</span>
        <small>${state.filesMode ? (state.filesState === "ready" ? String(state.files.length) : "") : (state.treeState === "ready" ? `${state.tree.length} ${t("kv.groups")}` : "")}</small>
        <button type="button" class="kv-sidebar-toggle icon-button" data-kv-files-toggle aria-pressed="${state.filesMode}" aria-label="${escapeVaultHTML(kt("kv.filesToggle"))}" title="${escapeVaultHTML(kt("kv.filesToggle"))}">${icon("folder")}</button>
      </div>
      <div class="kv-tree-body">${state.filesMode ? filesBodyHTML() : treeBodyHTML()}</div>
    </nav>
    ${state.sidebarCollapsed ? "" : `<div class="kv-tree-resizer" role="separator" aria-orientation="vertical" tabindex="0" aria-label="${escapeVaultHTML(kt("kv.resizeSidebar"))}" aria-valuemin="${SIDEBAR_MIN_WIDTH}" aria-valuemax="${sidebarMaximumWidth()}" aria-valuenow="${state.sidebarWidth}" data-kv-resizer></div>`}
    <article class="kv-doc-panel${activity ? " kv-activity-panel" : ""}">
      ${activity ? activityBodyHTML() : (state.filesMode ? filePanelHTML() : docBodyHTML())}
    </article>
    ${activity ? "" : `<aside class="kv-context-panel" aria-label="${t("kv.context")}">
      ${contextBodyHTML()}
    </aside>`}
  </div>`;
}

// Kept local instead of joining the shared icon table: that table is part of
// the first-screen entry chunk, which sits exactly on its gzip budget.
const ACTIVITY_ICON = '<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></svg>';

function activityEntryHTML(): string {
  const selected = isActivitySelected();
  const count = state.inboxState === "ready" ? state.inbox?.count ?? 0 : 0;
  return `<div class="kv-tree-home">
    <button type="button" class="kv-tree-link kv-activity-link${selected ? " selected" : ""}" data-kv-activity aria-current="${selected ? "page" : "false"}">
      ${ACTIVITY_ICON}<span class="kv-tree-title">${kt("kv.activityTitle")}</span>${count > 0 ? `<span class="kv-count">${count}</span>` : ""}
    </button>
  </div>`;
}

function treeBodyHTML(): string {
  const home = activityEntryHTML();
  if (state.treeState === "loading") return `${home}${vaultEmptyState("loading")}`;
  if (state.treeState === "error") return `${home}${vaultEmptyState("error", state.treeError, true)}`;
  if (!state.indexReady) return `${home}${vaultEmptyState("empty", t("kv.indexMissing"))}`;
  if (state.tree.length === 0) return `${home}${vaultEmptyState("empty")}`;
  return `${home}<ul class="kv-tree-list">${state.tree.map(node => treeNodeHTML(node)).join("")}</ul>`;
}

function isVaultFolderNode(node: KnowledgeVaultTreeNode): boolean {
  if (node.type === "folder") return true;
  // Old API payloads used the index document as a folder node whose path was
  // the document path; keep those payloads renderable during a rollout.
  return (node.children?.length || 0) > 0 && !/\.md$/i.test(String(node.path ?? ""));
}

function treeNodeHTML(node: KnowledgeVaultTreeNode, depth = 0): string {
  const children = node.children || [];
  // Directories start collapsed like `tree` output; only an explicit toggle (or
  // the selected document's ancestor path) puts an id in `expanded`.
  const expanded = state.expanded.has(node.id);
  const selected = node.id === state.selectedID;
  if (!isVaultFolderNode(node)) {
    // Documents keep an empty chevron slot and a document icon, so their titles
    // share the same left edge as folder titles at the same depth.
    return `<li class="kv-tree-node kv-tree-doc" style="--kv-depth:${depth}"><a class="kv-tree-link kv-tree-doc-link${selected ? " selected" : ""}" href="${escapeVaultHTML(docHash(node))}" data-kv-doc="${escapeVaultHTML(node.id)}" aria-current="${selected ? "page" : "false"}"><span class="kv-tree-toggle-space" aria-hidden="true"></span><span class="kv-tree-doc-icon" aria-hidden="true">${icon("file")}</span><span class="kv-tree-title">${escapeVaultHTML(node.title)}</span>${badge(typeLabel(node.type), "type")}</a></li>`;
  }
  const hasIndex = node.has_index ?? Boolean(node.is_index);
  const canToggle = children.length > 0;
  const id = escapeVaultHTML(node.id);
  const toggleLabel = expanded ? t("kv.collapseNode") : t("kv.expandNode");
  // The leftmost chevron only expands or collapses the branch; it never opens
  // a document. Folder titles open the index page and toggle the branch at the
  // same time, so the selected pill never blocks expanding.
  const toggle = canToggle
    ? `<button type="button" class="kv-tree-toggle" data-kv-toggle="${id}" aria-expanded="${expanded}" aria-label="${escapeVaultHTML(toggleLabel)}" title="${escapeVaultHTML(toggleLabel)}">${icon(expanded ? "chevron-down" : "chevron-right")}</button>`
    : `<span class="kv-tree-toggle-space" aria-hidden="true"></span>`;
  const folderIcon = `<span class="kv-tree-folder-icon" aria-hidden="true">${icon("folder")}</span>`;
  const title = `<span class="kv-tree-title">${escapeVaultHTML(node.title)}</span>`;
  const titleActions = `${hasIndex ? ` data-kv-title-open="${id}"` : ""}${canToggle ? ` data-kv-title-toggle="${id}"` : ""}`;
  const titleClass = `kv-tree-link kv-tree-folder-link kv-tree-folder-title${selected ? " selected" : ""}`;
  const titleCurrent = selected ? ` aria-current="page"` : "";
  const titleControl = hasIndex || canToggle
    ? `<button type="button" class="${titleClass}" data-kv-folder-title="${id}"${titleActions}${titleCurrent}>${title}</button>`
    : `<span class="kv-tree-link kv-tree-folder-link" data-kv-folder-title="${id}">${title}</span>`;
  // The row around the title toggles the branch as well, so the whitespace
  // right of the title stays a large hit area.
  const rowAttributes = canToggle ? ` data-kv-row-toggle="${id}"` : "";
  return `<li class="kv-tree-node kv-tree-folder" style="--kv-depth:${depth}">
    <div class="kv-tree-row${canToggle ? " kv-tree-row-toggle" : ""}"${rowAttributes}>
      ${toggle}
      ${folderIcon}
      ${titleControl}
    </div>
    ${expanded && canToggle ? `<ul class="kv-tree-list">${children.map(child => treeNodeHTML(child, depth + 1)).join("")}</ul>` : ""}
  </li>`;
}


/* Real file browser ------------------------------------------------------ */
// The directory is a navigable view of the real vault: directories are
// fetched one level at a time, files are previewed from disk, and nothing
// requires the file to have been indexed first.
function filesParentPath(value: string): string {
  const normalized = normalizeTreePath(value);
  const separator = normalized.lastIndexOf("/");
  return separator < 0 ? "" : normalized.slice(0, separator);
}

function filesBreadcrumbHTML(): string {
  const current = normalizeTreePath(state.filesPath);
  const segments = current ? current.split("/") : [];
  const parts = [`<button type="button" class="kv-tag" data-kv-files-dir="">${escapeVaultHTML(kt("kv.filesRoot"))}</button>`];
  let accumulated = "";
  for (const segment of segments) {
    accumulated = accumulated ? `${accumulated}/${segment}` : segment;
    parts.push(`<span aria-hidden="true">/</span><button type="button" class="kv-tag" data-kv-files-dir="${escapeVaultHTML(accumulated)}">${escapeVaultHTML(segment)}</button>`);
  }
  return `<div class="kv-files-path">${parts.join("")}</div>`;
}

function filesBodyHTML(): string {
  if (state.filesState === "loading") return vaultEmptyState("loading");
  if (state.filesState === "error") return `${vaultEmptyState("error", state.filesError)}<button type="button" class="secondary" data-kv-files-retry>${t("kv.retry")}</button>`;
  const rows = state.files.length === 0
    ? `<li class="kv-empty-inline">${escapeVaultHTML(kt("kv.filesEmpty"))}</li>`
    : state.files.map(entry => {
      if (entry.is_dir) {
        return `<li class="kv-tree-node kv-tree-folder"><button type="button" class="kv-tree-link kv-tree-folder-link" data-kv-files-dir="${escapeVaultHTML(entry.path)}"><span class="kv-tree-folder-icon" aria-hidden="true">${icon("folder")}</span><span class="kv-tree-title">${escapeVaultHTML(entry.name)}</span></button></li>`;
      }
      if (entry.kind === "binary") {
        return `<li class="kv-tree-node kv-tree-doc"><a class="kv-tree-link kv-tree-doc-link" href="${escapeVaultHTML(api.knowledgeVaultFileDownloadURL(entry.path))}" data-kv-files-download="${escapeVaultHTML(entry.path)}"><span class="kv-tree-doc-icon" aria-hidden="true">${icon("file")}</span><span class="kv-tree-title">${escapeVaultHTML(entry.name)}</span>${badge(kt("kv.fileDownload"), "type")}</a></li>`;
      }
      return `<li class="kv-tree-node kv-tree-doc"><button type="button" class="kv-tree-link kv-tree-doc-link${state.file?.path === entry.path ? " selected" : ""}" data-kv-files-open="${escapeVaultHTML(entry.path)}"><span class="kv-tree-doc-icon" aria-hidden="true">${icon("file")}</span><span class="kv-tree-title">${escapeVaultHTML(entry.name)}</span></button></li>`;
    }).join("");
  const up = state.filesPath ? `<button type="button" class="kv-back" data-kv-files-dir="${escapeVaultHTML(filesParentPath(state.filesPath))}">${icon("chevron-up")}<span>${escapeVaultHTML(kt("kv.filesUp"))}</span></button>` : "";
  const reveal = state.openAvailable && state.filesPath
    ? `<button type="button" class="secondary" data-kv-reveal="${escapeVaultHTML(state.filesPath)}">${icon("folder")}<span>${escapeVaultHTML(kt("kv.fileOpenManager"))}</span></button>`
    : "";
  return `${filesBreadcrumbHTML()}${up}${reveal}<ul class="kv-tree-list kv-files-list">${rows}</ul>`;
}

function stripFrontmatterForPreview(value: string): string {
  const match = String(value ?? "").match(/^---\r?\n[\s\S]*?\r?\n---\r?\n?/);
  return match ? value.slice(match[0].length) : value;
}

function filePanelHTML(): string {
  if (state.fileState === "loading") return vaultEmptyState("loading");
  if (state.fileState === "error") return `${vaultEmptyState("error", state.fileError)}<button type="button" class="secondary" data-kv-file-retry>${t("kv.retry")}</button>`;
  if (state.file) return fileBodyHTML();
  return vaultEmptyState("empty", kt("kv.filesSelect"));
}

function fileBodyHTML(): string {
  const file = state.file;
  if (!file) return docBodyHTML();
  const copyAbsolute = `<button type="button" class="kv-open kv-copy" data-kv-copy="${escapeVaultHTML(file.absolute_path)}" title="${escapeVaultHTML(t("kv.copyPath"))}" aria-label="${escapeVaultHTML(t("kv.copyPath"))}">${icon("copy")}</button>`;
  const reveal = state.openAvailable
    ? `<button type="button" class="secondary" data-kv-reveal="${escapeVaultHTML(file.path)}">${icon("folder")}<span>${escapeVaultHTML(kt("kv.fileOpenManager"))}</span></button>`
    : "";
  const download = `<a class="secondary" href="${escapeVaultHTML(api.knowledgeVaultFileDownloadURL(file.path))}" download>${icon("file")}<span>${escapeVaultHTML(kt("kv.fileDownload"))}</span></a>`;
  const body = file.kind === "markdown"
    ? `<div class="markdown-body kv-doc-body" data-kv-doc-body>${renderMarkdown(stripFrontmatterForPreview(file.content))}</div>`
    : `<pre class="kv-file-text">${escapeVaultHTML(file.content)}</pre>`;
  return `<header class="kv-doc-head"><div class="kv-doc-title-row"><h1 id="kv-file-title">${escapeVaultHTML(file.title || file.name)}</h1><div class="kv-open-actions">${copyAbsolute}</div></div><div class="kv-doc-meta-badges">${badge(file.kind, "type")}</div></header>${body}<footer class="kv-doc-footer"><div class="kv-open-actions">${reveal}${download}</div></footer>`;
}

async function loadFiles(path: string, force = false): Promise<void> {
  const key = normalizeTreePath(path);
  if (!force && state.filesState === "ready" && state.filesPath === key) return;
  state.filesPath = key;
  state.filesState = "loading";
  state.filesError = "";
  rerender();
  try {
    const payload = await api.knowledgeVaultFiles(key);
    if (state.filesPath !== key) return;
    state.files = payload.entries || [];
    state.filesState = "ready";
  } catch (error) {
    if (state.filesPath !== key) return;
    state.filesState = "error";
    state.filesError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

async function loadFile(path: string): Promise<void> {
  state.fileRequestPath = normalizeTreePath(path);
  state.fileState = "loading";
  state.fileError = "";
  state.file = null;
  state.filesMode = true;
  state.mobilePane = "doc";
  rerender();
  try {
    const payload = await api.knowledgeVaultFile(path);
    state.file = payload.file;
    state.fileState = "ready";
    state.openAvailable = state.openAvailable || false;
  } catch (error) {
    state.fileState = "error";
    state.fileError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

async function loadOpenInfo(): Promise<void> {
  try {
    const payload = await api.knowledgeVaultOpenInfo();
    state.openAvailable = Boolean(payload.open_available);
    if (payload.vault_path && !state.vaultPath) state.vaultPath = payload.vault_path;
  } catch {
    state.openAvailable = false;
  }
  rerender();
}

async function revealVaultPath(path: string): Promise<void> {
  try {
    await api.revealKnowledgeVaultPath(path);
  } catch (error) {
    state.filesError = error instanceof Error ? error.message : String(error);
    rerender();
  }
}

function docBodyHTML(): string {
  const back = `<button type="button" class="kv-back" data-kv-back>${icon("chevron-up")}<span>${kt("kv.docBackToActivity")}</span></button>`;
  if (state.docState === "loading") return `${back}${vaultEmptyState("loading")}`;
  if (state.docState === "error") return `${back}${vaultEmptyState("error", state.docError, true)}`;
  if (!state.doc) return `${back}${vaultEmptyState("empty", t("kv.selectDoc"))}`;
  const doc = state.doc;
  const crumbs = doc.path.split("/");
  return `${back}
    <div class="kv-doc-toolbar">
      <nav class="kv-breadcrumb" aria-label="${t("kv.breadcrumb")}">${crumbs.map((part, index) => `<span${index === crumbs.length - 1 ? ' aria-current="page"' : ""}>${escapeVaultHTML(part.replace(/\.md$/i, ""))}</span>`).join('<i aria-hidden="true">/</i>')}</nav>
      <div class="kv-doc-actions">
        ${openActions(doc.absolute_path, doc.relative_path, doc.obsidian_file)}
      </div>
    </div>
    <div class="kv-doc-head">
      <h1>${escapeVaultHTML(doc.title)}</h1>
      <div class="kv-doc-subhead">
        <p class="kv-doc-meta"><span>${t("kv.source")}: ${escapeVaultHTML(doc.library_name)}</span>${doc.project_id ? `<span>${t("kv.project")}: ${escapeVaultHTML(doc.project_id)}</span>` : ""}<span>${t("kv.updated")}: ${escapeVaultHTML(safeDateTime(doc.modified_at || doc.updated))}</span><span>${t("kv.status")}: ${escapeVaultHTML(statusLabel(doc.status))}</span></p>
        <button type="button" class="kv-context-toggle" data-kv-context-toggle aria-expanded="${state.contextOpen}" aria-label="${t("kv.context")}" title="${t("kv.context")}">${icon("panel")}<span>${t("kv.context")}</span></button>
      </div>
    </div>
    ${doc.warning ? `<div class="kv-warning" role="status">${escapeVaultHTML(doc.warning)}</div>` : ""}
    <div class="kv-doc-meta-badges">${metaBadges(doc)}${doc.tags?.map(tag => `<button type="button" class="kv-tag" data-kv-tag="${escapeVaultHTML(tag)}">#${escapeVaultHTML(tag)}</button>`).join("") || ""}</div>
    <div class="markdown-body kv-doc-body" data-kv-doc-body>${renderMarkdown(rewriteVaultWikilinks(doc.body))}</div>
    <footer class="kv-feedback">
      <span>${t("kv.feedbackPrompt")}</span>
      <button type="button" class="kv-feedback-button${state.feedback === "helped" ? " active" : ""}" data-kv-feedback="helped">${icon("shield")}<span>${t("kv.helpful")}</span></button>
      <button type="button" class="kv-feedback-button${state.feedback === "stale" ? " active" : ""}" data-kv-feedback="stale">${icon("clock")}<span>${t("kv.stale")}</span></button>
      ${state.feedback ? `<small role="status">${t("kv.feedbackReadonly")}</small>` : ""}
    </footer>`;
}

function contextBodyHTML(): string {
  if (!state.doc) return `<p class="kv-hint">${t("kv.selectDoc")}</p>`;
  const doc = state.doc;
  const outline = doc.outline.length
    ? `<ol class="kv-outline">${doc.outline.map((item: KnowledgeVaultOutlineItem) => `<li style="--kv-level:${item.level}"><a href="#${escapeVaultHTML(item.slug)}" data-kv-outline="${escapeVaultHTML(item.slug)}">${escapeVaultHTML(item.text)}</a></li>`).join("")}</ol>`
    : `<p class="kv-hint">${t("kv.noOutline")}</p>`;
  const tags = doc.tags?.length
    ? `<div class="kv-tag-list">${doc.tags.map(tag => `<button type="button" class="kv-tag" data-kv-tag="${escapeVaultHTML(tag)}">#${escapeVaultHTML(tag)}</button>`).join("")}</div>`
    : `<p class="kv-hint">${t("kv.noTags")}</p>`;
  const backlinks = doc.backlinks.length
    ? `<ul class="kv-backlinks">${doc.backlinks.map(item => `<li><a href="${escapeVaultHTML(docHash({id: item.id}))}" data-kv-doc="${escapeVaultHTML(item.id)}">${escapeVaultHTML(item.title)}</a></li>`).join("")}</ul>`
    : `<p class="kv-hint">${t("kv.noBacklinks")}</p>`;
  return `<section class="kv-context-section"><h3>${t("kv.outline")}</h3>${outline}</section>
    <section class="kv-context-section"><h3>${t("kv.tags")}</h3>${tags}</section>
    <section class="kv-context-section"><h3>${t("kv.backlinks")}</h3>${backlinks}</section>
    <button type="button" class="kv-context-close" data-kv-context-toggle>${t("kv.close")}</button>`;
}

function activityBodyHTML(): string {
  const recentCount = state.recentState === "ready" ? state.recent.length : 0;
  return `<section class="kv-activity" aria-labelledby="kv-activity-title">
    <header class="kv-activity-head">
      <div>
        <h1 id="kv-activity-title">${kt("kv.activityTitle")}</h1>
        <p class="kv-hint">${kt("kv.activitySubtitle")}</p>
      </div>
      <button type="button" class="kv-back kv-activity-back" data-kv-back-tree>${icon("chevron-up")}<span>${kt("kv.activityBackToTree")}</span></button>
    </header>
    <section class="kv-activity-block" aria-labelledby="kv-activity-inbox">
      <header class="kv-activity-block-head">
        <h2 id="kv-activity-inbox">${kt("kv.activityInbox")}</h2>
      </header>
      ${inboxFeedHTML()}
    </section>
    <section class="kv-activity-block" aria-labelledby="kv-activity-recent">
      <header class="kv-activity-block-head">
        <h2 id="kv-activity-recent">${kt("kv.activityRecent")}</h2>
        <div class="kv-activity-block-actions">
          ${recentUnchangedToggleHTML()}
          <small>${recentCount > 0 ? kt("kv.activityRecentCount", {count: recentCount}) : ""}</small>
        </div>
      </header>
      ${recentFeedHTML()}
    </section>
  </section>`;
}

// The switch is the only escape hatch for a timestamp-only row. It stays
// visible above an empty feed so the owner can reveal hidden updates.
function recentUnchangedToggleHTML(): string {
  if (!state.indexReady) return "";
  const active = state.recentIncludeUnchanged;
  return `<button type="button" class="kv-recent-toggle" data-kv-recent-unchanged aria-pressed="${active ? "true" : "false"}">${kt(active ? "kv.recentHideUnchanged" : "kv.recentShowUnchanged")}</button>`;
}

function recentFeedHTML(): string {
  if (state.recentState === "loading" || state.recentState === "idle") return vaultEmptyState("loading");
  if (state.recentState === "error") return vaultEmptyState("error", state.recentError, true);
  if (!state.indexReady) return vaultEmptyState("empty", t("kv.indexMissing"));
  if (state.recent.length === 0) return vaultEmptyState("empty", t("kv.recentEmpty"));
  return `<section class="kv-feed" aria-label="${kt("kv.activityRecent")}">
    ${groupRecentByDay(state.recent).map(group => `<section class="kv-feed-day"><h2>${escapeVaultHTML(group.label)}</h2><ul>${group.items.map(item => {
      const changedAt = item.changed_at || item.modified_at;
      const changeNote = item.change_kind === "none" ? kt("kv.changeTimestampOnly") : item.change_kind === "unknown" ? kt("kv.changeUnknown") : "";
      return `<li><a class="kv-feed-item" href="${escapeVaultHTML(docHash({id: item.id, scope: item.scope, project_id: item.project_id}))}" data-kv-preview="${escapeVaultHTML(item.id)}" aria-haspopup="dialog">
      <span class="kv-feed-time">${escapeVaultHTML(formatLocaleDateTime(changedAt, {hour: "2-digit", minute: "2-digit"}))}</span>
      <span class="kv-feed-main"><strong>${escapeVaultHTML(item.title)}</strong><small>${escapeVaultHTML(item.library)}${item.project_id ? ` · ${escapeVaultHTML(item.project_id)}` : ""} · ${escapeVaultHTML(item.path)}</small>${changeNote ? `<span class="kv-change-note">${escapeVaultHTML(changeNote)}</span>` : ""}</span>
      ${metaBadges(item)}
    </a></li>`;
    }).join("")}</ul></section>`).join("")}
  </section>`;
}

interface VaultInboxGroup {
  key: string;
  library: string;
  count: number;
  items: KnowledgeVaultRecentItem[];
}

// The inbox endpoint already returns per-library counts plus rows that carry
// library_id/library, so the activity page can group client-side without a new
// API. Counts lead the order; any item whose library is absent from counts is
// appended instead of being dropped.
function groupInboxByLibrary(inbox: KnowledgeVaultInbox): VaultInboxGroup[] {
  const groups = new Map<string, VaultInboxGroup>();
  for (const entry of inbox.counts || []) {
    const key = entry.library_id || entry.library;
    if (!key) continue;
    groups.set(key, {key, library: entry.library || key, count: entry.count, items: []});
  }
  for (const item of inbox.items || []) {
    const key = item.library_id || item.library || "";
    if (!key) continue;
    let group = groups.get(key);
    if (!group) {
      group = {key, library: item.library || key, count: 0, items: []};
      groups.set(key, group);
    }
    group.items.push(item);
    if (!(inbox.counts || []).some(entry => (entry.library_id || entry.library) === key)) group.count++;
  }
  return [...groups.values()].filter(group => group.items.length > 0);
}

function inboxFeedHTML(): string {
  if (state.inboxState === "loading" || state.inboxState === "idle") return vaultEmptyState("loading");
  if (state.inboxState === "error") return vaultEmptyState("error", state.inboxError, true);
  if (!state.indexReady) return vaultEmptyState("empty", t("kv.indexMissing"));
  if (!state.inbox || state.inbox.items.length === 0) return vaultEmptyState("empty", t("kv.inboxEmpty"));
  const groups = groupInboxByLibrary(state.inbox);
  return `<section class="kv-inbox" aria-label="${kt("kv.activityInbox")}">
    <p class="kv-inbox-total">${t("kv.inboxCount", {count: state.inbox.count})}</p>
    ${groups.map(group => `<section class="kv-inbox-group" data-kv-inbox-library="${escapeVaultHTML(group.key)}">
      <header class="kv-inbox-group-head"><h3>${escapeVaultHTML(group.library)}</h3><span class="kv-chip kv-chip-static">${kt("kv.activityInboxGroupCount", {count: group.count})}</span></header>
      <ul class="kv-inbox-list">${group.items.map(item => `<li><a class="kv-inbox-item" href="${escapeVaultHTML(docHash({id: item.id, scope: item.scope, project_id: item.project_id}))}" data-kv-doc="${escapeVaultHTML(item.id)}">
        <span class="kv-feed-main"><strong>${escapeVaultHTML(item.title)}</strong><small>${escapeVaultHTML(item.path)} · ${escapeVaultHTML(item.library)}</small></span>
        ${badge(typeLabel(item.type), "type")}
      </a></li>`).join("")}</ul>
    </section>`).join("")}
  </section>`;
}

function vaultProjectName(projectID?: string): string {
  if (!projectID) return "";
  return state.projects.find(item => item.project_id === projectID)?.name || projectID;
}

function skillAutoHint(skill: KnowledgeVaultSkill): string {
  switch (skill.unavailable_reason) {
    case "runtime_read_off":
      return kt("kv.skillRuntimeOff");
    case "index_not_ready":
      return kt("kv.skillIndexNotReady");
    case "project_not_bound":
      return kt("kv.skillProjectNotBound");
    case "archive_read_only":
      return kt("kv.skillArchiveReadonly");
  }
  if (skill.auto_attach && !skill.auto_attach_effective) return kt("kv.skillPending");
  return skill.scope === "global" ? kt("kv.skillGlobalHint") : kt("kv.skillProjectHint");
}

function skillsBodyHTML(): string {
  if (state.skillsState === "loading") return vaultEmptyState("loading");
  if (state.skillsState === "error") return vaultEmptyState("error", state.skillsError, true);
  if (state.skills.length === 0) return vaultEmptyState("empty", t("kv.skillsEmpty"));
  const runtimeHint = state.skillRuntimeRead ? "" : `<p class="kv-hint kv-hint-warn">${kt("kv.skillRuntimeOff")}</p>`;
  return `<section class="kv-skills" aria-label="${t("kv.tab.skills")}">
    <p class="kv-hint">${t("kv.skillsReadonly")}</p>
    ${runtimeHint}
    <p class="kv-hint">${kt("kv.skillsAutoHint")}</p>
    <div class="kv-skill-grid">${state.skills.map(skill => {
      const scopeText = skill.scope === "global"
        ? kt("kv.scope.global")
        : `${kt("kv.scope.project")} · ${skill.project_name || vaultProjectName(skill.project_id)}`;
      const busy = state.skillBusy === skill.key;
      const disabled = !skill.can_update || busy;
      return `<article class="kv-skill-card" data-kv-skill-card="${escapeVaultHTML(skill.key)}">
      <header><div class="kv-skill-title"><strong>${escapeVaultHTML(skill.name)}</strong><small class="kv-skill-meta">${badge(kt("kv.skillSourceVault"), "type")} ${escapeVaultHTML(scopeText)}</small></div></header>
      <p>${escapeVaultHTML(skill.description || t("kv.noDescription"))}</p>
      <small>${escapeVaultHTML(skill.library)} · ${t("kv.skillFiles", {count: skill.file_count})}</small>
      <footer class="kv-skill-footer">
        <label class="kv-skill-auto" title="${escapeVaultHTML(skillAutoHint(skill))}">
          <input type="checkbox" role="switch" data-kv-skill-auto="${escapeVaultHTML(skill.key)}" aria-label="${escapeVaultHTML(kt("kv.skillAutoToggleAria", {name: skill.name}))}" ${skill.auto_attach ? "checked" : ""} ${disabled ? "disabled" : ""}>
          <span class="kv-skill-auto-track" aria-hidden="true"></span>
          <span class="kv-skill-auto-label">${kt("kv.skillAutoAttach")}</span>
        </label>
        <div class="kv-skill-actions">${openActions(skill.open_path, `${skill.relative_path}/SKILL.md`, "", {compact: true, context: skill.name, copyKey: "kv.copySkillPath"})}</div>
      </footer>
      <p class="kv-skill-status">${escapeVaultHTML(skillAutoHint(skill))}</p>
    </article>`;
    }).join("")}</div>
  </section>`;
}

function settingsCopyHTML(value: string, labelKey: string): string {
  if (!value) return "";
  const label = kt(labelKey);
  return `<button type="button" class="kv-open kv-copy" data-kv-copy="${escapeVaultHTML(value)}" title="${escapeVaultHTML(label)}" aria-label="${escapeVaultHTML(label)}">${icon("copy")}</button>`;
}

function settingsDialogHTML(): string {
  const ready = (value: boolean) => value ? t("kv.settingsReady") : t("kv.settingsNotReady");
  const settings = state.settings;
  // The settings endpoint is authoritative for these three fields. Library and
  // document payloads also carry them, but may lag behind a rebuild; prefer the
  // freshly loaded settings so the dialog and the rebuild notice agree.
  const vaultPath = settings?.vault_path || state.vaultPath;
  const indexReady = settings ? Boolean(settings.index_ready) : state.indexReady;
  const rebuiltAt = settings?.rebuilt_at || state.rebuiltAt;
  // Index maintenance is automatic (write path, file watcher, startup
  // reconciliation). The gear panel only reports health; there is deliberately
  // no owner-facing rebuild switch or button.
  const health = !settings
    ? t("kv.settingsNotReady")
    : indexReady
      ? (settings.index_stale ? kt("kv.settingsIndexStale") : t("kv.settingsReady"))
      : t("kv.settingsNotReady");
  const settingsError = state.settingsError
    ? `<p class="kv-settings-notice error" role="alert">${escapeVaultHTML(state.settingsError)}</p><button type="button" class="secondary" data-kv-settings-retry>${t("kv.retry")}</button>`
    : "";
  return `<dialog id="kv-settings-dialog" class="kv-settings">
    <header><h2>${t("kv.settings")}</h2><button type="button" class="icon-button" data-kv-settings-close aria-label="${t("kv.close")}">${icon("close")}</button></header>
    <dl>
      <div><dt>${t("kv.settingsVault")}</dt><dd class="kv-settings-value kv-settings-copy"><span>${escapeVaultHTML(vaultPath || t("kv.none"))}</span>${settingsCopyHTML(vaultPath, "kv.copyVaultPath")}</dd></div>
      <div><dt>${t("kv.settingsIndex")}</dt><dd class="kv-settings-value">${escapeVaultHTML(indexReady ? t("kv.settingsReady") : t("kv.settingsNotReady"))}</dd></div>
      <div><dt>${t("kv.settingsRebuilt")}</dt><dd class="kv-settings-value">${escapeVaultHTML(rebuiltAt ? safeDateTime(rebuiltAt) : t("kv.none"))}</dd></div>
      <div><dt>${kt("kv.settingsIndexAction")}</dt><dd class="kv-settings-value">${escapeVaultHTML(health)}</dd></div>
      <div><dt>${t("kv.settingsObsidian")}</dt><dd class="kv-settings-value">${escapeVaultHTML(state.obsidianVault || t("kv.none"))} · ${escapeVaultHTML(ready(state.obsidianReady))}</dd></div>
    </dl>
    ${settingsError}
    ${gitSectionHTML()}
    <p class="kv-hint">${t("kv.settingsHint")}</p>
    <footer><button type="button" data-kv-settings-close>${t("kv.close")}</button></footer>
  </dialog>`;
}

/* Settings dialog ------------------------------------------------------- */

function applyVaultSettings(settings: KnowledgeVaultSettings): void {
  state.settings = settings;
  state.settingsState = "ready";
  state.settingsError = "";
  state.vaultPath = settings.vault_path || state.vaultPath;
  state.indexReady = Boolean(settings.index_ready);
  state.vaultReady = Boolean(settings.vault_ready);
  state.rebuiltAt = settings.rebuilt_at || "";
}

async function loadVaultSettings(): Promise<void> {
  if (state.settingsState === "loading") return;
  state.settingsState = "loading";
  state.settingsError = "";
  rerender();
  try {
    const result = await api.knowledgeVaultSettings();
    applyVaultSettings(result.settings);
  } catch (error) {
    state.settingsState = "error";
    state.settingsError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

function reopenSettingsDialog(): void {
  if (!state.settingsOpen) return;
  const dialog = document.querySelector<HTMLDialogElement>("#kv-settings-dialog");
  if (!dialog || dialog.open) return;
  dialog.showModal();
}

function openSettingsDialog(): void {
  state.settingsOpen = true;
  rerender();
  if (state.settingsState === "idle" || state.settingsState === "error") void loadVaultSettings();
  void loadVaultGitStatus();
  requestAnimationFrame(reopenSettingsDialog);
}

function closeSettingsDialog(): void {
  state.settingsOpen = false;
  document.querySelector<HTMLDialogElement>("#kv-settings-dialog")?.close();
}

// Native ESC goes through cancel/close without calling the close button, so it
// must clear the open intent too. A close event queued by the button (or by a
// dialog instance a render already replaced) is ignored: the state is already
// false, or the event no longer belongs to the dialog currently in the DOM.
function handleSettingsDialogDismissed(event: Event): void {
  if (!state.settingsOpen) return;
  const dialog = event.currentTarget;
  if (!(dialog instanceof HTMLDialogElement) || document.querySelector("#kv-settings-dialog") !== dialog) return;
  state.settingsOpen = false;
}

// The files changed on disk; every projection the page cached is one index
// generation old. Reload them in place so the selected document, its deep link
// and the open settings dialog all survive.
/* Git settings ---------------------------------------------------------- */

const GIT_BUSY_LABEL: Record<Exclude<GitBusy, "">, string> = {
  init: "kv.gitInitializing", remote: "kv.gitSaving", commit: "kv.gitCommitting",
  pull: "kv.gitPulling", push: "kv.gitPushing", sync: "kv.gitSyncing",
};

function gitActionLabel(kind: Exclude<GitBusy, "">, key: string): string {
  return kt(state.gitBusy === kind ? GIT_BUSY_LABEL[kind] : key);
}

function gitNoticeHTML(): string {
  if (!state.gitNotice) return "";
  const role = state.gitNotice.kind === "error" ? "alert" : "status";
  return `<p class="kv-settings-notice ${state.gitNotice.kind === "error" ? "error" : "success"}" role="${role}">${escapeVaultHTML(state.gitNotice.message)}</p>`;
}

function gitSectionHTML(): string {
  const git = state.git;
  let body = "";
  if (state.gitState === "error" && !git) {
    body = `<p class="kv-settings-notice error" role="alert">${kt("kv.gitLoadFailed")}：${escapeVaultHTML(state.gitError)}</p>
      <button type="button" class="secondary" data-kv-git-retry>${t("kv.retry")}</button>`;
  } else if (!git) {
    body = `<p class="kv-hint" role="status">${t("kv.loading")}</p>`;
  } else if (!git.available) {
    body = `<p class="kv-settings-notice error" role="alert">${kt("kv.gitUnavailable")}</p>`;
  } else {
    const busy = Boolean(state.gitBusy);
    const disabled = busy ? " disabled" : "";
    const repo = git.initialized ? kt("kv.gitInitialized") : kt("kv.gitUninitialized");
    // Prefer the full SSH remote the API now returns; older payloads only had
    // the masked form, which still beats showing nothing.
    const remoteDisplay = git.remote_set ? git.remote_full || git.remote || "" : "";
    const worktree = git.clean ? kt("kv.gitClean") : kt("kv.gitDirty", {count: git.changed});
    let syncState = kt("kv.gitNoTracking");
    if (git.tracking) {
      const parts: string[] = [];
      if (git.ahead > 0) parts.push(kt("kv.gitAhead", {count: git.ahead}));
      if (git.behind > 0) parts.push(kt("kv.gitBehind", {count: git.behind}));
      syncState = parts.length > 0 ? parts.join(" · ") : kt("kv.gitInSync");
    }
    const lastCommit = git.last_commit
      ? `${git.last_commit}${git.last_commit_message ? ` · ${git.last_commit_message}` : ""}${git.last_commit_at ? ` · ${safeDateTime(git.last_commit_at)}` : ""}`
      : kt("kv.gitNone");
    const lastSync = git.last_sync_at ? safeDateTime(git.last_sync_at) : kt("kv.gitNone");
    // One grid holds both form rows so the two buttons share the same auto
    // track: the commit message spans the remote and branch tracks, and its
    // button's right edge lines up with 保存设置 at every non-stacked width.
    const remoteForm = git.initialized ? `<div class="kv-git-form">
      <label><span>${kt("kv.gitRemoteInput")}</span><input id="kv-git-remote-input" type="text" data-allow-rerender="true" placeholder="${kt("kv.gitRemotePlaceholder")}" value="${escapeVaultHTML(state.gitRemoteDraft)}" autocomplete="off" spellcheck="false"${disabled}></label>
      <label><span>${kt("kv.gitBranchInput")}</span><input id="kv-git-branch-input" type="text" data-allow-rerender="true" value="${escapeVaultHTML(state.gitBranchDraft || git.branch)}" autocomplete="off" spellcheck="false"${disabled}></label>
      <button type="button" id="kv-git-remote-save" class="secondary"${disabled}>${gitActionLabel("remote", "kv.gitSaveRemote")}</button>
      <label class="kv-git-commit-label"><span>${kt("kv.gitCommitMessage")}</span><input id="kv-git-commit-message" type="text" data-allow-rerender="true" maxlength="200" placeholder="${kt("kv.gitCommitPlaceholder")}" value="${escapeVaultHTML(state.gitCommitDraft)}" autocomplete="off"${disabled}></label>
      <button type="button" id="kv-git-commit" class="secondary"${git.clean ? " disabled" : disabled}>${gitActionLabel("commit", "kv.gitCommit")}</button>
    </div>` : "";
    const actions = git.initialized ? `
      <div class="kv-git-actions">
        <button type="button" id="kv-git-pull"${(!git.remote_set || !git.clean) ? " disabled" : disabled}>${gitActionLabel("pull", "kv.gitPull")}</button>
        <button type="button" id="kv-git-push"${(!git.remote_set || !git.has_commits || (git.tracking && git.ahead === 0)) ? " disabled" : disabled}>${gitActionLabel("push", "kv.gitPush")}</button>
        <button type="button" id="kv-git-sync" class="primary"${(!git.remote_set || !git.clean) ? " disabled" : disabled}>${gitActionLabel("sync", "kv.gitSync")}</button>
      </div>` : `<div class="kv-git-actions"><button type="button" id="kv-git-init" class="primary"${disabled}>${gitActionLabel("init", "kv.gitInit")}</button></div>`;
    body = `<dl>
      <div><dt>${kt("kv.gitRepo")}</dt><dd class="kv-settings-value">${repo}</dd></div>
      <div><dt>${kt("kv.gitBranch")}</dt><dd class="kv-settings-value">${escapeVaultHTML(git.branch)}</dd></div>
      <div><dt>${kt("kv.gitRemote")}</dt><dd class="kv-settings-value kv-settings-copy"><span>${escapeVaultHTML(remoteDisplay || kt("kv.gitRemoteUnset"))}</span>${settingsCopyHTML(remoteDisplay, "kv.copyRemote")}</dd></div>
      <div><dt>${kt("kv.gitWorktree")}</dt><dd class="kv-settings-value">${escapeVaultHTML(worktree)}</dd></div>
      <div><dt>${kt("kv.gitSyncState")}</dt><dd class="kv-settings-value">${escapeVaultHTML(syncState)}</dd></div>
      <div><dt>${kt("kv.gitLastCommit")}</dt><dd class="kv-settings-value">${escapeVaultHTML(lastCommit)}</dd></div>
      <div><dt>${kt("kv.gitLastSync")}</dt><dd class="kv-settings-value">${escapeVaultHTML(lastSync)}</dd></div>
    </dl>
    ${remoteForm}
    ${actions}
    ${gitNoticeHTML()}
    <p class="kv-hint">${kt("kv.gitHint")}</p>`;
  }
  return `<section class="kv-settings-section kv-git" aria-labelledby="kv-git-title">
    <h3 id="kv-git-title">${kt("kv.gitTitle")}</h3>
    ${body}
  </section>`;
}

function applyGitStatus(git: KnowledgeVaultGitStatus): void {
  state.git = git;
  state.gitState = "ready";
  state.gitError = "";
}

function knowledgeVaultGitRequest(action: "status" | "init" | "remote" | "commit" | "pull" | "push" | "sync", payload?: {remote?: string; branch?: string; message?: string}): Promise<KnowledgeVaultGitResponse> {
  return api.request(`/api/v1/knowledge-vault/git/${action}`, action === "status" ? {} : {method: action === "remote" ? "PUT" : "POST", body: payload ? JSON.stringify(payload) : undefined});
}

async function loadVaultGitStatus(): Promise<void> {
  if (state.gitState === "loading") return;
  state.gitState = "loading";
  state.gitError = "";
  rerender();
  try {
    const result = await knowledgeVaultGitRequest("status");
    applyGitStatus(result.git);
  } catch (error) {
    state.gitState = "error";
    state.gitError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

async function runGitOperation(kind: Exclude<GitBusy, "">, action: () => Promise<{git: KnowledgeVaultGitStatus; message?: string}>): Promise<void> {
  if (state.gitBusy) return;
  state.gitBusy = kind;
  state.gitNotice = null;
  rerender();
  try {
    const result = await action();
    applyGitStatus(result.git);
    state.gitRemoteDraft = "";
    state.gitBranchDraft = "";
    state.gitCommitDraft = "";
    state.gitNotice = {kind: "notice", message: result.message || kt("kv.gitInSync")};
  } catch (error) {
    state.gitNotice = {kind: "error", message: error instanceof Error ? error.message : String(error)};
  } finally {
    state.gitBusy = "";
    rerender();
  }
}

async function initializeVaultGit(): Promise<void> {
  await runGitOperation("init", () => knowledgeVaultGitRequest("init"));
}

async function saveVaultGitRemote(): Promise<void> {
  const remote = document.querySelector<HTMLInputElement>("#kv-git-remote-input")?.value.trim() || "";
  const branch = document.querySelector<HTMLInputElement>("#kv-git-branch-input")?.value.trim() || "";
  await runGitOperation("remote", () => knowledgeVaultGitRequest("remote", {remote, branch}));
}

async function commitVaultGit(): Promise<void> {
  const message = document.querySelector<HTMLInputElement>("#kv-git-commit-message")?.value.trim() || "";
  await runGitOperation("commit", () => knowledgeVaultGitRequest("commit", {message}));
}

async function pullVaultGit(): Promise<void> {
  await runGitOperation("pull", () => knowledgeVaultGitRequest("pull"));
}

async function pushVaultGit(): Promise<void> {
  await runGitOperation("push", () => knowledgeVaultGitRequest("push"));
}

async function syncVaultGit(): Promise<void> {
  await runGitOperation("sync", () => knowledgeVaultGitRequest("sync"));
}

/* Tree expansion -------------------------------------------------------- */

// Returns the ids of the folders between the tree root and `id` (excluding the
// node itself), or null when the node is not in this tree. The trail drives the
// automatic expansion that keeps a deep-linked document visible while every
// sibling folder stays collapsed.
export function vaultTreeAncestors(nodes: KnowledgeVaultTreeNode[], id: string, trail: string[] = []): string[] | null {
  for (const node of nodes) {
    if (node.id === id) return trail;
    const children = node.children || [];
    if (children.length === 0) continue;
    const found = vaultTreeAncestors(children, id, [...trail, node.id]);
    if (found) return found;
  }
  return null;
}

function expandTreePathTo(id: string): void {
  if (!id) return;
  const ancestors = vaultTreeAncestors(state.tree, id);
  if (!ancestors) return;
  for (const ancestor of ancestors) state.expanded.add(ancestor);
}

function toggleTreeFolder(id: string): void {
  if (!id) return;
  if (state.expanded.has(id)) state.expanded.delete(id);
  else state.expanded.add(id);
  rerender();
}

function toggleSidebar(): void {
  state.sidebarCollapsed = !state.sidebarCollapsed;
  persistSidebarCollapsed(state.sidebarCollapsed);
  rerender();
}

// Width changes update only the CSS custom property and the separator's ARIA
// value while the pointer is down. Re-rendering the page on every pointermove
// would replace the dragged element and make the drag unusable.
function applySidebarWidth(root: HTMLElement, width: number, persist: boolean): void {
  const clamped = clampSidebarWidth(width);
  state.sidebarWidth = clamped;
  root.style.setProperty("--kv-tree-width", `${clamped}px`);
  const resizer = root.querySelector<HTMLElement>("[data-kv-resizer]");
  if (resizer) {
    resizer.setAttribute("aria-valuenow", String(clamped));
    resizer.setAttribute("aria-valuemax", String(sidebarMaximumWidth()));
  }
  if (persist) persistSidebarWidth(clamped);
}

function bindSidebarResizer(root: HTMLElement): void {
  const resizer = root.querySelector<HTMLElement>("[data-kv-resizer]");
  if (!resizer) return;
  let dragging = false;
  let pointerID = -1;
  let dragStartX = 0;
  let dragStartWidth = SIDEBAR_DEFAULT_WIDTH;
  const finish = (event: PointerEvent): void => {
    if (!dragging) return;
    dragging = false;
    document.body.classList.remove("kv-resizing");
    if (pointerID >= 0 && resizer.hasPointerCapture(pointerID)) {
      try { resizer.releasePointerCapture(pointerID); } catch { /* already released */ }
    }
    pointerID = -1;
    applySidebarWidth(root, dragStartWidth + (event.clientX - dragStartX), true);
  };
  resizer.addEventListener("pointerdown", event => {
    if (event.button !== 0) return;
    event.preventDefault();
    dragging = true;
    pointerID = event.pointerId;
    dragStartX = event.clientX;
    dragStartWidth = state.sidebarWidth;
    resizer.focus({preventScroll: true});
    try { resizer.setPointerCapture(event.pointerId); } catch { /* pointer capture is best-effort */ }
    document.body.classList.add("kv-resizing");
  });
  resizer.addEventListener("pointermove", event => {
    if (!dragging) return;
    applySidebarWidth(root, dragStartWidth + (event.clientX - dragStartX), false);
  });
  resizer.addEventListener("pointerup", finish);
  resizer.addEventListener("pointercancel", finish);
  resizer.addEventListener("dblclick", () => {
    applySidebarWidth(root, SIDEBAR_DEFAULT_WIDTH, true);
  });
  resizer.addEventListener("keydown", event => {
    let next = state.sidebarWidth;
    if (event.key === "ArrowLeft") next -= SIDEBAR_STEP;
    else if (event.key === "ArrowRight") next += SIDEBAR_STEP;
    else if (event.key === "Home") next = SIDEBAR_MIN_WIDTH;
    else if (event.key === "End") next = sidebarMaximumWidth();
    else return;
    event.preventDefault();
    applySidebarWidth(root, next, true);
  });
}

function bindSidebarViewport(): void {
  if (sidebarViewportBound || typeof window === "undefined") return;
  sidebarViewportBound = true;
  window.addEventListener("resize", () => {
    const root = document.querySelector<HTMLElement>(".knowledge-vault");
    if (!root || state.sidebarCollapsed) return;
    applySidebarWidth(root, state.sidebarWidth, false);
  });
}

/* Data loading ----------------------------------------------------------- */

function contextFromConfig(): KnowledgeVaultContext {
  return {render: () => {}, setMessage: () => {}, announce: () => {}};
}
let activeContext: KnowledgeVaultContext = contextFromConfig();

function rerender(): void {
  activeContext.render();
}

function applyLibraries(payload: KnowledgeVaultLibraries): void {
  state.libraries = payload.libraries || [];
  state.projects = payload.projects || [];
  state.vaultReady = Boolean(payload.vault_ready);
  state.indexReady = Boolean(payload.index_ready);
  state.vaultName = payload.vault_name || "";
  state.vaultPath = payload.vault_path || "";
  state.obsidianVault = payload.obsidian_vault || "";
  state.obsidianReady = Boolean(payload.obsidian_ready);
  state.rebuiltAt = payload.rebuilt_at || "";
  state.warning = payload.warning || "";
  if (!state.libraries.some(item => item.id === state.libraryID)) {
    state.libraryID = state.libraries[0]?.id || "global";
  }
}

// The blocked page is still the real vault route, so it asks the settings API
// which blocked state this instance is in (upgrade awaiting migration vs a
// plain disabled vault). A failure only costs the more specific copy.
async function loadBlockedReadiness(): Promise<void> {
  try {
    const result = await api.knowledgeVaultSettings();
    state.blockedStatus = result.settings?.readiness?.status || "";
  } catch {
    state.blockedStatus = "";
  }
}

async function loadLibraries(): Promise<boolean> {
  state.loading = "loading";
  rerender();
  try {
    const payload = await api.knowledgeVaultLibraries();
    applyLibraries(payload);
    state.initialized = true;
    state.loading = "ready";
    state.error = "";
    if (!payload.vault_ready) {
      // Enabled but not initialized yet: keep the vault route and explain the
      // init/migrate/import choices instead of showing an empty workspace.
      state.blocked = true;
      state.blockedStatus = "needs_rebuild";
      await loadBlockedReadiness();
      if (!state.blockedStatus || state.blockedStatus === "ready") state.blockedStatus = "needs_rebuild";
      rerender();
      return false;
    }
    rerender();
    return true;
  } catch (error) {
    const detail = error as {code?: string; status?: number} | null;
    if (detail?.code === "knowledge_vault_disabled" || detail?.status === 404) {
      state.blocked = true;
      state.initialized = true;
      state.loading = "ready";
      state.error = "";
      await loadBlockedReadiness();
      rerender();
      return false;
    }
    state.initialized = true;
    state.loading = "error";
    state.error = error instanceof Error ? error.message : String(error);
    rerender();
    return false;
  }
}

function loadOnce(key: string, action: () => Promise<void>): void {
  if (pendingLoads.has(key)) return;
  pendingLoads.add(key);
  void action().catch(error => {
    activeContext.setMessage("error", error instanceof Error ? error.message : String(error));
  }).finally(() => pendingLoads.delete(key));
}

async function loadTree(force = false, preserveExpansion = false): Promise<void> {
  const key = VAULT_TREE_LIBRARY;
  // A failed tree stays failed until an explicit retry. The page binding runs
  // on every render, so retrying errored loads here would form an
  // error -> rerender -> request loop.
  if (!force && state.treeKey === key) return;
  const ticket = ++treeRequest;
  state.treeKey = key;
  state.treeState = "loading";
  state.treeError = "";
  // A freshly loaded tree starts collapsed; only the selected document's
  // ancestor path is reopened after the nodes arrive. A rebuild refresh keeps
  // the folders the user already opened.
  if (!preserveExpansion) state.expanded = new Set();
  rerender();
  try {
    const payload: KnowledgeVaultTree = await api.knowledgeVaultTree(key);
    if (ticket !== treeRequest || state.treeKey !== key) return;
    state.tree = buildVaultDirectoryTree(payload.nodes || [], key);
    state.indexReady = payload.index_ready;
    state.treeState = "ready";
    expandTreePathTo(state.selectedID);
  } catch (error) {
    if (ticket !== treeRequest || state.treeKey !== key) return;
    state.treeState = "error";
    state.treeError = error instanceof Error ? error.message : String(error);
  }
  rerender();
  // A document opened before the tree arrived may still have unresolved
  // wikilinks; resolve them against the tree that just loaded.
  if (state.doc && state.docState === "ready") decorateDocBody();
}

async function loadDoc(id: string, force = false): Promise<void> {
  if (!id) return;
  state.filesMode = false;
  state.file = null;
  // Any route into the reader dismisses the in-page preview first.
  const hadPreview = Boolean(state.previewID);
  clearPreviewState();
  if (!force && state.selectedID === id && state.docState === "ready") {
    if (hadPreview) rerender();
    return;
  }
  state.selectedID = id;
  state.docState = "loading";
  state.docError = "";
  state.feedback = "";
  state.doc = null;
  // Clicking a document, a search result, or following a deep link reopens only
  // that document's ancestor folders so its row is visible in the tree.
  expandTreePathTo(id);
  rerender();
  try {
    const payload = await api.knowledgeVaultDoc(id);
    if (state.selectedID !== id) return;
    state.doc = payload.doc;
    state.docState = "ready";
  } catch (error) {
    if (state.selectedID !== id) return;
    state.docState = "error";
    state.docError = error instanceof Error ? error.message : String(error);
  }
  rerender();
  decorateDocBody();
}

async function loadRecent(force = false): Promise<void> {
  // Recent is always the all-libraries feed; every row carries its own
  // library/project/path context instead of relying on a topbar scope.
  const includeUnchanged = state.recentIncludeUnchanged;
  const key = includeUnchanged ? "include-unchanged" : "";
  // Only an explicit retry may leave the error state: an automatic retry here
  // would loop because rendering re-runs the tab loaders.
  if (!force && state.recentKey === key && state.recentState !== "idle") return;
  const ticket = ++recentRequest;
  state.recentKey = key;
  state.recentState = "loading";
  rerender();
  try {
    const payload: KnowledgeVaultRecent = await api.knowledgeVaultRecent("", 120, includeUnchanged);
    if (ticket !== recentRequest || state.recentKey !== key) return;
    // Older servers return every row; keep the default list content-only until
    // the owner explicitly asks for timestamp-only updates.
    state.recent = (payload.items || []).filter(item => includeUnchanged || item.content_changed !== false);
    state.indexReady = payload.index_ready;
    state.recentState = "ready";
  } catch (error) {
    if (ticket !== recentRequest || state.recentKey !== key) return;
    state.recentState = "error";
    state.recentError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

async function loadInbox(force = false): Promise<void> {
  // Like recent, the inbox defaults to every library.
  const key = "";
  // Same error guard as recent: stay put until the retry button forces a load.
  if (!force && state.inboxKey === key && state.inboxState !== "idle") return;
  const ticket = ++inboxRequest;
  state.inboxKey = key;
  state.inboxState = "loading";
  rerender();
  try {
    const payload = await api.knowledgeVaultInbox(key);
    if (ticket !== inboxRequest || state.inboxKey !== key) return;
    state.inbox = payload;
    state.indexReady = payload.index_ready;
    state.inboxState = "ready";
  } catch (error) {
    if (ticket !== inboxRequest || state.inboxKey !== key) return;
    state.inboxState = "error";
    state.inboxError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

async function loadPreview(id: string): Promise<void> {
  if (!id) return;
  const ticket = ++previewRequest;
  state.previewID = id;
  state.previewDoc = null;
  state.previewState = "loading";
  state.previewError = "";
  // Every open starts on the change view; an unavailable diff switches the
  // panel to the full text without a second round trip or a blank dialog.
  state.previewTab = "diff";
  state.previewDiff = null;
  state.previewDiffState = "loading";
  state.previewDiffError = "";
  rerender();
  const [docResult, diffResult] = await Promise.allSettled([
    api.knowledgeVaultDoc(id),
    api.knowledgeVaultDiff(id),
  ]);
  if (ticket !== previewRequest || state.previewID !== id) return;
  if (docResult.status === "fulfilled") {
    state.previewDoc = docResult.value.doc;
    state.previewState = "ready";
  } else {
    state.previewState = "error";
    state.previewError = docResult.reason instanceof Error ? docResult.reason.message : String(docResult.reason);
  }
  if (diffResult.status === "fulfilled") {
    state.previewDiff = diffResult.value.diff;
    state.previewDiffState = "ready";
    if (!state.previewDiff.available) state.previewTab = "full";
  } else {
    state.previewDiffState = "error";
    state.previewDiffError = diffResult.reason instanceof Error ? diffResult.reason.message : String(diffResult.reason);
    state.previewTab = "full";
  }
  rerender();
}

// Opening the preview never touches selectedID or the hash: the activity page
// stays the current route until the explicit 打开原文 link calls enterDoc.
function openPreview(id: string): void {
  if (!id) return;
  const active = document.activeElement;
  previewTriggerID = active instanceof HTMLElement ? (active.dataset.kvPreview || "") : "";
  void loadPreview(id);
}

function clearPreviewState(): void {
  state.previewID = "";
  state.previewDoc = null;
  state.previewState = "idle";
  state.previewError = "";
  state.previewTab = "diff";
  state.previewDiff = null;
  state.previewDiffState = "idle";
  state.previewDiffError = "";
  previewTriggerID = "";
  previewRequest++;
}

function closePreview(restoreFocus = true): void {
  if (!state.previewID) return;
  const id = previewTriggerID;
  clearPreviewState();
  rerender();
  if (!restoreFocus || !id) return;
  requestAnimationFrame(() => {
    const escaped = typeof CSS !== "undefined" && CSS.escape ? CSS.escape(id) : id;
    document.querySelector<HTMLElement>(`[data-kv-preview="${escaped}"]`)?.focus({preventScroll: true});
  });
}

async function loadSkills(force = false): Promise<void> {
  if (!force && state.skillsState === "ready") return;
  state.skillsState = "loading";
  rerender();
  try {
    const payload: KnowledgeVaultSkills = await api.knowledgeVaultSkills();
    state.skills = payload.skills || [];
    state.skillRuntimeRead = Boolean(payload.runtime_read);
    state.skillsState = "ready";
  } catch (error) {
    state.skillsState = "error";
    state.skillsError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

function vaultSkillErrorText(error: unknown): string {
  const code = (error as {code?: string} | null)?.code || "";
  switch (code) {
    case "global_confirmation_required":
      return kt("kv.skillGlobalConfirmRequired");
    case "vault_skill_not_found":
      return kt("kv.skillNotFound");
    case "vault_skill_not_auto_attachable":
      return kt("kv.skillArchiveReadonly");
    case "knowledge_vault_disabled":
      return kt("kv.skillVaultDisabled");
  }
  return error instanceof Error ? error.message : String(error);
}

// Persists only the preference. Vault files stay untouched; the backend also
// enforces the global second confirmation, so a bypassed confirm still fails.
async function setSkillAutoAttach(key: string, enabled: boolean): Promise<void> {
  const skill = state.skills.find(item => item.key === key);
  if (!skill || state.skillBusy) {
    rerender();
    return;
  }
  let confirmGlobal = false;
  if (enabled && skill.scope === "global") {
    confirmGlobal = window.confirm(kt("kv.skillGlobalConfirm", {name: skill.name}));
    if (!confirmGlobal) {
      // Re-render so the checkbox snaps back to the persisted state.
      rerender();
      return;
    }
  }
  state.skillBusy = key;
  rerender();
  try {
    const response = await api.updateKnowledgeVaultSkillAutoAttach(key, enabled, confirmGlobal);
    const index = state.skills.findIndex(item => item.key === key);
    if (index >= 0) {
      // Keep the catalog-only fields (project_bound, unavailable_reason) and
      // apply just the preference fields from the write response.
      state.skills[index] = {
        ...state.skills[index],
        auto_attach: response.skill.auto_attach,
        auto_attach_effective: response.skill.auto_attach_effective,
        auto_attach_confirmed: response.skill.auto_attach_confirmed,
      };
    }
    if (enabled && !response.skill.auto_attach_effective) {
      activeContext.setMessage("notice", kt("kv.skillAutoPending", {name: skill.name}));
    } else {
      activeContext.setMessage("notice", kt(enabled ? "kv.skillAutoEnabled" : "kv.skillAutoDisabled", {name: skill.name}));
    }
  } catch (error) {
    activeContext.setMessage("error", vaultSkillErrorText(error));
  } finally {
    state.skillBusy = "";
    rerender();
  }
}

function loadTab(tab: VaultTab): void {
  // Tree/recent/inbox dedupe on the parameter key inside each loader. A shared
  // loader-name key would drop the request for a newly selected library while
  // the previous one is still in flight.
  if (tab === "knowledge") {
    void loadTree();
    // Activity is the default selection, so its two feeds load with the tree.
    // When a document is selected the cached activity data stays untouched;
    // switching back to the activity entry reuses it instead of refetching.
    if (isActivitySelected()) {
      void loadRecent();
      void loadInbox();
    }
  } else {
    loadOnce("skills", () => loadSkills());
  }
}

function currentVaultDirectory(): string {
  const relative = String(state.doc?.path || "");
  const separator = relative.lastIndexOf("/");
  return separator < 0 ? "" : relative.slice(0, separator);
}

function currentSearchContext(): {library: string; project: string} {
  if (state.doc) return {library: state.doc.library_id || "", project: state.doc.project_id || ""};
  return {library: state.libraryID === "global" ? "" : state.libraryID, project: ""};
}

function performSearch(): void {
  const query = state.searchQuery.trim();
  const tag = state.searchTag.trim();
  if (!query && !tag) {
    state.searchResults = [];
    state.searchState = "idle";
    rerender();
    return;
  }
  const ticket = ++state.pendingSearch;
  state.searchState = "loading";
  rerender();
  const scoped = state.searchScope !== "all";
  const context = scoped ? currentSearchContext() : {library: "", project: ""};
  const directory = state.searchScope === "directory" ? currentVaultDirectory() : "";
  void api.knowledgeVaultSearch({
    q: query, tag,
    library: context.library, project: context.project,
    type: state.searchType, status: state.searchStatus, limit: 40,
  }).then((payload: KnowledgeVaultSearch) => {
    if (ticket !== state.pendingSearch) return;
    let results = payload.results || [];
    // The read API has no path-prefix filter, so "current directory" narrows
    // the same scoped result set in the client.
    if (directory) results = results.filter(item => item.path === directory || item.path.startsWith(`${directory}/`));
    state.searchResults = results;
    state.searchState = "ready";
    rerender();
  }).catch((error: unknown) => {
    if (ticket !== state.pendingSearch) return;
    state.searchState = "error";
    state.searchError = error instanceof Error ? error.message : String(error);
    rerender();
  });
}

function scheduleSearch(): void {
  if (searchTimer) clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    searchTimer = null;
    performSearch();
  }, 160);
}

function openSearch(): void {
  state.searchOpen = true;
  rerender();
  requestAnimationFrame(() => document.querySelector<HTMLInputElement>("#kv-search")?.focus());
}

function closeSearch(): void {
  state.searchOpen = false;
  rerender();
}

function syncHash(doc: KnowledgeVaultDoc | null): void {
  if (!doc) return;
  // A doc request can finish after the user switched to another view. Writing
  // the deep link then would route the next refresh back to knowledge.
  if (!document.querySelector(".knowledge-vault")) return;
  try {
    window.history.replaceState(null, "", docHash(doc));
  } catch {
    // History can be unavailable in embedded webviews; deep linking is then
    // only best-effort and the page state still works.
  }
}

function clearDocHash(): void {
  if (typeof window === "undefined" || !document.querySelector(".knowledge-vault")) return;
  try {
    window.history.replaceState(null, "", "#knowledge");
  } catch {
    // See syncHash: the activity page still works without history support.
  }
}

function isMobileViewport(): boolean {
  const width = typeof window === "undefined" ? 0 : Number(window.innerWidth);
  return Number.isFinite(width) && width > 0 && width <= 760;
}

/* Interaction ------------------------------------------------------------ */

function selectActivity(options: {mobilePane?: MobilePane; refresh?: boolean} = {}): void {
  state.selectedID = VAULT_ACTIVITY_ID;
  state.doc = null;
  state.docState = "idle";
  state.docError = "";
  state.feedback = "";
  state.searchOpen = false;
  clearPreviewState();
  if (options.mobilePane) state.mobilePane = options.mobilePane;
  clearDocHash();
  void loadRecent(Boolean(options.refresh));
  void loadInbox(Boolean(options.refresh));
  rerender();
}

function openActivity(): void {
  // The activity entry is also the refresh affordance: re-entering it pulls
  // fresh recent/inbox data even when the page is already showing.
  selectActivity({mobilePane: "doc", refresh: true});
  if (typeof window !== "undefined") requestAnimationFrame(() => window.scrollTo(0, 0));
}

function backToActivity(): void {
  state.docScroll = typeof window === "undefined" ? 0 : window.scrollY;
  const mobile = isMobileViewport();
  // On phones "back" returns to the directory list, where the activity entry is
  // selected at the top. On wider screens it returns to the activity page.
  selectActivity({mobilePane: "list"});
  if (typeof window === "undefined") return;
  requestAnimationFrame(() => window.scrollTo(0, mobile ? state.listScroll : 0));
}

function enterDoc(id: string, libraryHint = ""): void {
  if (libraryHint && state.libraries.some(item => item.id === libraryHint)) state.libraryID = libraryHint;
  if (state.mobilePane === "list") state.listScroll = window.scrollY;
  state.mobilePane = "doc";
  state.searchOpen = false;
  void loadDoc(id).then(() => {
    const doc = state.doc;
    if (!doc) return;
    if (state.libraries.some(item => item.id === doc.library_id)) state.libraryID = doc.library_id;
    syncHash(doc);
    if (state.mobilePane === "doc") window.scrollTo(0, 0);
    void loadTree(false);
    decorateDocBody();
  });
}

function backToList(): void {
  state.docScroll = window.scrollY;
  state.mobilePane = "list";
  rerender();
  requestAnimationFrame(() => window.scrollTo(0, state.listScroll));
}

function setTab(tab: VaultTab): void {
  state.tab = tab;
  state.mobilePane = "list";
  clearPreviewState();
  rerender();
  loadTab(tab);
}

function normalizedHeadingText(value: string | null | undefined): string {
  return String(value ?? "").replace(/\s+/g, " ").trim();
}

function renderedHeadingText(markdown: string): string {
  const holder = document.createElement("div");
  holder.innerHTML = renderMarkdown(`# ${markdown}`);
  return normalizedHeadingText(holder.querySelector("h1")?.textContent || markdown);
}

function decorateDocBody(): void {
  const body = document.querySelector<HTMLElement>("[data-kv-doc-body]");
  if (!body || !state.doc) return;
  const headings = [...body.querySelectorAll<HTMLElement>("h1,h2,h3,h4,h5,h6")];
  const outline = state.doc.outline;
  // Match by rendered text and level rather than array position. This keeps
  // outline links correct when the index is older than the file or when a
  // heading was filtered out (for example, a `#` line inside a code fence).
  let cursor = 0;
  for (const item of outline) {
    const wanted = renderedHeadingText(item.text);
    for (let index = cursor; index < headings.length; index += 1) {
      const heading = headings[index];
      if (Number(heading.tagName.slice(1)) !== item.level) continue;
      if (normalizedHeadingText(heading.textContent) !== wanted) continue;
      if (!heading.id) heading.id = item.slug;
      cursor = index + 1;
      break;
    }
  }
  decorateWikilinks(body);
}

// Wikilinks are placeholder hash links; once the tree is available they
// become real document links (or plain labels when the target does not exist),
// so nothing dead is clickable.
function decorateWikilinks(body: HTMLElement): void {
  const links = [...body.querySelectorAll<HTMLAnchorElement>(`a[href^="${vaultWikilinkHrefPrefix}"]`)];
  if (!links.length) return;
  const byPath = vaultWikilinkIndex();
  for (const link of links) {
    // Placeholder links point inside the reader even before they resolve, so
    // they must never inherit the renderer's new-tab attributes.
    link.removeAttribute("target");
    link.removeAttribute("rel");
    const raw = link.getAttribute("href") || "";
    let target = "";
    try {
      target = decodeURIComponent(raw.slice(vaultWikilinkHrefPrefix.length));
    } catch {
      // A malformed escape cannot name a document; fall through as unresolved.
    }
    const node = resolveVaultWikilink(target, byPath);
    if (!node) {
      // The tree may still be loading; a later decoration can resolve it.
      if (state.treeState !== "ready") continue;
      link.replaceWith(...link.childNodes);
      continue;
    }
    link.dataset.kvDoc = node.id;
    link.href = docHash({id: node.id, scope: node.scope, project_id: node.project_id});
  }
}

let copyNoticeVersion = 0;

function showCopyNotice(kind: "notice" | "error", message: string): void {
  // Update only the banner and the persistent status region. A full render
  // would replace the focused copy button and drop keyboard focus.
  const stack = document.querySelector<HTMLElement>("main.workspace > .banner-stack");
  if (!stack) return;
  activeContext.announce?.(message);
  const version = ++copyNoticeVersion;
  stack.innerHTML = `<div class="banner ${kind === "error" ? "error" : "success"}">${escapeVaultHTML(message)}</div>`;
  window.setTimeout(() => {
    if (version === copyNoticeVersion && stack.isConnected) stack.innerHTML = "";
  }, 5000);
}

function copyPath(value: string): void {
  const done = () => showCopyNotice("notice", t("kv.copied"));
  if (navigator.clipboard?.writeText) {
    void navigator.clipboard.writeText(value).then(done).catch(() => fallbackCopy(value, done));
    return;
  }
  fallbackCopy(value, done);
}

function fallbackCopy(value: string, done: () => void): void {
  const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  try {
    document.execCommand("copy");
    done();
  } catch {
    showCopyNotice("error", value);
  } finally {
    textarea.remove();
    previous?.focus({preventScroll: true});
  }
}

function bindKeyboard(): void {
  if (keyboardBound) return;
  keyboardBound = true;
  document.addEventListener("keydown", event => {
    if (!document.querySelector(".knowledge-vault")) return;
    const modifier = event.ctrlKey || event.metaKey;
    if (modifier && event.key.toLowerCase() === "k") {
      event.preventDefault();
      event.stopPropagation();
      openSearch();
      return;
    }
    if (event.key === "Escape") {
      if (state.searchOpen) {
        event.preventDefault();
        event.stopPropagation();
        closeSearch();
      } else if (state.previewID) {
        event.preventDefault();
        event.stopPropagation();
        closePreview();
      } else if (state.contextOpen) {
        state.contextOpen = false;
        rerender();
      }
    }
  }, true);
}

function bindHash(): void {
  if (hashBound) return;
  hashBound = true;
  window.addEventListener("hashchange", () => {
    if (!document.querySelector(".knowledge-vault")) return;
    const parsed = parseDocHash(window.location.hash);
    if (!parsed?.id) {
      // A bare #knowledge is the activity page; leaving a document hash and
      // going back must not leave the previous reader on screen.
      if (window.location.hash === "#knowledge" || window.location.hash === "") {
        if (state.doc || state.docState !== "idle" || !isActivitySelected()) {
          selectActivity({mobilePane: "doc"});
        }
      }
      return;
    }
    if (parsed.library) state.libraryID = parsed.library;
    const unchanged = parsed.id === state.selectedID && state.docState === "ready";
    state.tab = "knowledge";
    state.mobilePane = "doc";
    if (unchanged) rerender();
    else void loadDoc(parsed.id);
  });
}

function handlePreviewLink(event: Event): boolean {
  const target = (event.target as HTMLElement | null)?.closest<HTMLElement>("[data-kv-preview]");
  if (!target) return false;
  if (event instanceof MouseEvent && (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0)) return true;
  event.preventDefault();
  openPreview(target.dataset.kvPreview || "");
  return true;
}

function handleDocLink(event: Event): boolean {
  const target = (event.target as HTMLElement | null)?.closest<HTMLElement>("[data-kv-doc]");
  if (!target) return false;
  if (event instanceof MouseEvent && (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0)) return true;
  event.preventDefault();
  const library = target.closest<HTMLElement>("[data-kv-inbox-library]")?.dataset.kvInboxLibrary || "";
  enterDoc(target.dataset.kvDoc || "", library);
  return true;
}

// A wikilink whose target is not in the tree is unwrapped once the tree is
// ready; until then this guard keeps its placeholder hash from rewriting the
// route and dropping the reader on an empty page.
function handleMissingWikilink(event: Event): boolean {
  const anchor = (event.target as HTMLElement | null)?.closest<HTMLAnchorElement>(`a[href^="${vaultWikilinkHrefPrefix}"]`);
  if (!anchor) return false;
  if (event instanceof MouseEvent && (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0)) return true;
  event.preventDefault();
  return true;
}

function focusSelector(element: Element | null): string {
  if (!(element instanceof HTMLElement)) return "";
  if (element.id) return `#${typeof CSS !== "undefined" && CSS.escape ? CSS.escape(element.id) : element.id}`;
  for (const name of ["kvSidebarToggle", "kvToggle", "kvFolderTitle", "kvFeedback", "kvTabButton", "kvDoc", "kvCopy", "kvTag", "kvSkillAuto"]) {
    const value = (element.dataset as Record<string, string | undefined>)[name];
    if (value) return `[data-kv-${name.replace(/[A-Z]/g, letter => `-${letter.toLowerCase()}`) }="${typeof CSS !== "undefined" && CSS.escape ? CSS.escape(value) : value}"]`;
  }
  return "";
}

function renderPreservingFocus(renderPage: () => void): void {
  const dialogWasOpen = state.settingsOpen && Boolean(document.querySelector<HTMLDialogElement>("#kv-settings-dialog")?.open);
  const active = document.activeElement;
  const selector = focusSelector(active);
  const start = active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement ? active.selectionStart : null;
  const end = active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement ? active.selectionEnd : null;
  renderPage();
  // The whole page markup, including the <dialog>, is replaced on every render.
  // Reopen it before restoring focus so in-place status updates do not close
  // the panel under the Owner.
  if (dialogWasOpen) reopenSettingsDialog();
  if (!selector) return;
  const next = document.querySelector<HTMLElement>(selector);
  if (!next) return;
  next.focus({preventScroll: true});
  if ((next instanceof HTMLInputElement || next instanceof HTMLTextAreaElement) && start !== null) {
    try { next.setSelectionRange(start, end ?? start); } catch { /* some input types reject selection ranges */ }
  }
}

export function bindKnowledgeVaultPage(context: KnowledgeVaultContext): void {
  installGitStyles();
  activeContext = {render: () => renderPreservingFocus(context.render), setMessage: context.setMessage, announce: context.announce};
  bindKeyboard();
  bindHash();
  if (!binding) {
    binding = true;
    void loadLibraries().then(ok => {
      if (!ok) return;
      const parsed = parseDocHash(window.location.hash);
      if (parsed?.library && state.libraries.some(item => item.id === parsed.library)) state.libraryID = parsed.library;
      if (parsed?.id) {
        // A doc deep link wins over the default activity selection. Set the id
        // before loadTab so the activity feeds are not fetched for a page that
        // will immediately render the document.
        state.tab = "knowledge";
        state.selectedID = parsed.id;
        state.mobilePane = "doc";
      }
      loadTab(state.tab);
      if (parsed?.id) {
        void loadDoc(parsed.id).then(() => { syncHash(state.doc); decorateDocBody(); });
      }
    });
  } else if (!state.blocked && state.initialized && state.loading === "ready") {
    loadTab(state.tab);
  }

  const root = document.querySelector<HTMLElement>(".knowledge-vault");
  if (!root) return;

  // The blocked landing stays on this route and sends the Owner to the one
  // place that can migrate, import or re-enable the vault.
  root.querySelector<HTMLButtonElement>("[data-kv-open-vault-settings]")?.addEventListener("click", () => {
    window.dispatchEvent(new CustomEvent("aha:open-vault-settings"));
  });

  bindSidebarViewport();
  bindSidebarResizer(root);

  root.querySelectorAll<HTMLElement>("[data-kv-tab-button]").forEach(button => {
    button.addEventListener("click", () => setTab(button.dataset.kvTabButton as VaultTab));
  });

  root.querySelector<HTMLElement>("[data-kv-search-open]")?.addEventListener("click", openSearch);
  root.querySelector<HTMLElement>("[data-kv-search-close]")?.addEventListener("click", closeSearch);
  root.querySelector<HTMLElement>("[data-kv-settings]")?.addEventListener("click", openSettingsDialog);
  root.querySelectorAll<HTMLElement>("[data-kv-settings-close]").forEach(button => {
    button.addEventListener("click", closeSettingsDialog);
  });
  const settingsDialog = root.querySelector<HTMLDialogElement>("#kv-settings-dialog");
  settingsDialog?.addEventListener("cancel", handleSettingsDialogDismissed);
  settingsDialog?.addEventListener("close", handleSettingsDialogDismissed);
  root.querySelector<HTMLButtonElement>("[data-kv-settings-retry]")?.addEventListener("click", () => { void loadVaultSettings(); });
  root.querySelector<HTMLButtonElement>("[data-kv-git-retry]")?.addEventListener("click", () => { void loadVaultGitStatus(); });
  root.querySelector<HTMLButtonElement>("#kv-git-init")?.addEventListener("click", () => { void initializeVaultGit(); });
  root.querySelector<HTMLButtonElement>("#kv-git-remote-save")?.addEventListener("click", () => { void saveVaultGitRemote(); });
  root.querySelector<HTMLButtonElement>("#kv-git-commit")?.addEventListener("click", () => { void commitVaultGit(); });
  root.querySelector<HTMLButtonElement>("#kv-git-pull")?.addEventListener("click", () => { void pullVaultGit(); });
  root.querySelector<HTMLButtonElement>("#kv-git-push")?.addEventListener("click", () => { void pushVaultGit(); });
  root.querySelector<HTMLButtonElement>("#kv-git-sync")?.addEventListener("click", () => { void syncVaultGit(); });
  root.querySelector<HTMLInputElement>("#kv-git-remote-input")?.addEventListener("input", event => {
    state.gitRemoteDraft = (event.target as HTMLInputElement).value;
  });
  root.querySelector<HTMLInputElement>("#kv-git-branch-input")?.addEventListener("input", event => {
    state.gitBranchDraft = (event.target as HTMLInputElement).value;
  });
  root.querySelector<HTMLInputElement>("#kv-git-commit-message")?.addEventListener("input", event => {
    state.gitCommitDraft = (event.target as HTMLInputElement).value;
  });

  const searchInput = root.querySelector<HTMLInputElement>("#kv-search");
  searchInput?.addEventListener("input", () => {
    state.searchQuery = searchInput.value;
    scheduleSearch();
  });
  const searchTag = root.querySelector<HTMLInputElement>("#kv-search-tag");
  searchTag?.addEventListener("input", () => {
    state.searchTag = searchTag.value;
    scheduleSearch();
  });
  const searchScopeSelect = root.querySelector<HTMLSelectElement>("#kv-search-scope");
  searchScopeSelect?.addEventListener("change", () => {
    state.searchScope = (searchScopeSelect.value as SearchScope) || "all";
    performSearch();
  });
  for (const [selector, key] of [["#kv-search-type", "searchType"], ["#kv-search-status", "searchStatus"]] as const) {
    const element = root.querySelector<HTMLSelectElement>(selector);
    element?.addEventListener("change", () => {
      (state as unknown as Record<string, string>)[key] = element.value;
      performSearch();
    });
  }

  root.addEventListener("click", event => {
    if (handlePreviewLink(event)) return;
    if (handleDocLink(event)) return;
    if (handleMissingWikilink(event)) return;
    const target = event.target as HTMLElement | null;
    if (target?.closest("[data-kv-files-toggle]")) {
      state.filesMode = !state.filesMode;
      if (state.filesMode) {
        rerender();
        void loadFiles(state.filesPath);
        void loadOpenInfo();
      } else {
        rerender();
      }
      return;
    }
    const fileRetry = target?.closest("[data-kv-file-retry]");
    if (fileRetry && state.fileRequestPath) {
      void loadFile(state.fileRequestPath);
      return;
    }
    const filesRetry = target?.closest("[data-kv-files-retry]");
    if (filesRetry) {
      void loadFiles(state.filesPath, true);
      return;
    }
    const filesDir = target?.closest<HTMLElement>("[data-kv-files-dir]");
    if (filesDir) {
      const path = filesDir.dataset.kvFilesDir || "";
      state.file = null;
      void loadFiles(path);
      return;
    }
    const filesOpen = target?.closest<HTMLElement>("[data-kv-files-open]");
    if (filesOpen) {
      void loadFile(filesOpen.dataset.kvFilesOpen || "");
      return;
    }
    const reveal = target?.closest<HTMLElement>("[data-kv-reveal]");
    if (reveal) {
      void revealVaultPath(reveal.dataset.kvReveal || "");
      return;
    }
    if (target?.closest("[data-kv-preview-close]")) {
      closePreview();
      return;
    }
    if (target?.closest("[data-kv-preview-tab]")) {
      const tab = target?.closest<HTMLElement>("[data-kv-preview-tab]")?.dataset.kvPreviewTab;
      const next: PreviewTab = tab === "full" ? "full" : "diff";
      if (state.previewTab !== next) {
        state.previewTab = next;
        rerender();
        // The body was replaced, so return focus to the chosen tab for
        // keyboard users instead of dropping it on <body>.
        requestAnimationFrame(() => document.querySelector<HTMLElement>(`[data-kv-preview-tab="${next}"]`)?.focus({preventScroll: true}));
      }
      return;
    }
    if (target?.closest("[data-kv-preview-retry]")) {
      if (state.previewID) void loadPreview(state.previewID);
      return;
    }
    if (target?.closest("[data-kv-activity]")) {
      openActivity();
      return;
    }
    if (target?.closest("[data-kv-back-tree]")) {
      backToList();
      return;
    }
    if (target?.closest("[data-kv-sidebar-toggle]")) {
      toggleSidebar();
      return;
    }
    const treeToggle = target?.closest<HTMLElement>("[data-kv-toggle]");
    if (treeToggle) {
      toggleTreeFolder(treeToggle.dataset.kvToggle || "");
      return;
    }
    // A directory title both opens its index page and flips the branch: the
    // first click expands and loads the index, a second click on the same title
    // collapses it. The chevron above stays toggle-only and never navigates.
    const titleControl = target?.closest<HTMLElement>("[data-kv-title-open], [data-kv-title-toggle]");
    if (titleControl) {
      const openID = titleControl.dataset.kvTitleOpen || "";
      const toggleID = titleControl.dataset.kvTitleToggle || "";
      if (toggleID) {
        if (state.expanded.has(toggleID)) state.expanded.delete(toggleID);
        else state.expanded.add(toggleID);
      }
      if (openID) enterDoc(openID);
      // loadDoc() returns early for an already-loaded document, so render here
      // as well; otherwise a second title click would not visibly collapse.
      rerender();
      return;
    }
    // Folder rows without an index have no title control; clicking their blank
    // area is the same as clicking the folder icon.
    const rowToggle = target?.closest<HTMLElement>("[data-kv-row-toggle]");
    if (rowToggle && !target?.closest("[data-kv-doc]")) {
      toggleTreeFolder(rowToggle.dataset.kvRowToggle || "");
      return;
    }
    const copy = target?.closest<HTMLElement>("[data-kv-copy]");
    if (copy) {
      copyPath(copy.dataset.kvCopy || "");
      return;
    }
    const tag = target?.closest<HTMLElement>("[data-kv-tag]");
    if (tag) {
      state.searchTag = tag.dataset.kvTag || "";
      state.searchOpen = true;
      state.tab = "knowledge";
      rerender();
      performSearch();
      requestAnimationFrame(() => document.querySelector<HTMLInputElement>("#kv-search")?.focus());
      return;
    }
    const feedback = target?.closest<HTMLElement>("[data-kv-feedback]");
    if (feedback) {
      state.feedback = feedback.dataset.kvFeedback === "stale" ? "stale" : "helped";
      rerender();
      return;
    }
    if (target?.closest("[data-kv-back]")) {
      backToActivity();
      return;
    }
    if (target?.closest("[data-kv-context-toggle]")) {
      state.contextOpen = !state.contextOpen;
      rerender();
      return;
    }
    if (target?.closest("[data-kv-recent-unchanged]")) {
      state.recentIncludeUnchanged = !state.recentIncludeUnchanged;
      void loadRecent(true);
      return;
    }
    if (target?.closest("[data-kv-retry]")) {
      if (state.loading === "error") void loadLibraries();
      else if (state.docState === "error" && !isActivitySelected()) void loadDoc(state.selectedID, true);
      else if (state.searchState === "error") performSearch();
      else if (state.tab === "skills") void loadSkills(true);
      else if (isActivitySelected()) {
        // The activity page shows three independent states: the directory
        // tree plus the two feeds. Retry whichever one actually failed.
        if (state.treeState === "error") void loadTree(true);
        if (state.recentState === "error") void loadRecent(true);
        if (state.inboxState === "error") void loadInbox(true);
      } else void loadTree(true);
      return;
    }
    const outline = target?.closest<HTMLElement>("[data-kv-outline]");
    if (outline) {
      event.preventDefault();
      const heading = document.getElementById(outline.dataset.kvOutline || "");
      heading?.scrollIntoView({behavior: "smooth", block: "start"});
      heading?.setAttribute("tabindex", "-1");
      heading?.focus({preventScroll: true});
      return;
    }
  });

  root.addEventListener("change", event => {
    const input = (event.target as HTMLElement | null)?.closest<HTMLInputElement>("[data-kv-skill-auto]");
    if (!input) return;
    void setSkillAutoAttach(input.dataset.kvSkillAuto || "", input.checked);
  });

  if (state.searchOpen) requestAnimationFrame(() => root.querySelector<HTMLInputElement>("#kv-search")?.focus());
  if (state.settingsOpen) requestAnimationFrame(reopenSettingsDialog);
  decorateDocBody();
}

/* Test-only state access ------------------------------------------------ */

export function __getKnowledgeVaultStateForTest(): VaultState {
  return state;
}

export const __loadTreeForTest = loadTree;
export const __loadRecentForTest = loadRecent;
export const __loadInboxForTest = loadInbox;
