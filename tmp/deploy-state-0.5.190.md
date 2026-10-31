# 0.5.190 部署状态

- 根因修复：
  - `askOwnerAssistant` HTTP handler 只写了 message/reply，漏了 `tool_events`，所以前端永远收不到工具行。已补 `"tool_events": reply.ToolEvents`。
  - 客户端增加 Agent Update 汇总行：“模型执行了 N 个宠物工具”，并归入 update 筛选。
- 输入区对齐：
  - 输入框、发送、筛选统一 42px 高；发送固定 54px 宽、单行不换行；筛选 38px；gap 6px。
  - textarea 从 rows=2 改 rows=1，输入后再自增高。
- 对话框打开宠物可见：
  - 打开面板时改为瞬移 `positionAtDock(false)`，不再走 3 秒、途中被面板挡住。
  - 移动端上方有空间时停在面板上方；没有空间时停在面板内部顶端，不再 `visibility:hidden`。
  - `.assistant-pet.panel-open` z-index 5，保证浮在面板上。
- 提交：`e30bf00 fix: return tool events and keep the pet visible during chat`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-172033.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-172041`。
- Server `F79141804DC54996C5F2F08572A6F5F9AA0C8A3A1C7BC1E4CE757ED7E36DF2E2`；Tray `5D3E7DD9AF4558D0C64EA2D564341837B5EA9A938CC006BF9A280EEB72C15B84`。
- 进程 2026-10-29 01:20:41 起；`/healthz` 200；线上 `assistant.js` 含 `assistant-update-row`、`assistant-filter-popover`、`assistant-tool-row`。
- 390×844 测试：面板 y=214，宠物瞬移 y=140；输入/发送/筛选 bbox 高度均 42。
