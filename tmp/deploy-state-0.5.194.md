# 0.5.194 部署状态

- 设置“暂无可用工具”根因：
  - `GET /api/v1/assistant` handler 只写了 assistant/messages/models/model_name，漏了 `view.Tools`；设置页永远拿不到。
  - 已补 `tools`；HTTP 测试断言 6 个工具。
- 设置折叠（0.5.193）：
  - 身份与准则、当前提示词均 details 默认收起。
  - 可用工具列出 6 项：pet_play、pet_move、pet_mood、pet_open_task、pet_sleep、pet_wake。
- 宠物仍被遮：
  - 0.5.193 CSS z-index 可能被浏览器旧样式缓存；0.5.194 在 JS 里强制 `pet.style.zIndex=10`、`panel.style.zIndex=4`、`bubble=11`。
  - 面板打开仍瞬移到面板上方；空间不足放面板内顶部。
- 提交：`5f7bff7 fix: return pet tools and force the pet above chat`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-174651.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-174700`。
- Server `83CABD1CE70DB65D289E1B88B89BD46B8783DC3B4BF486734F64714270B67F2E`；Tray `2E3FD8F9CF4E85CEA5B99516E3D0477A7F2700325DFB42D983D3AFBA1D8B2A1B`。
- 进程 2026-10-29 01:47:07 起；`/healthz` 200；`assistant.js?x=` 含 `zIndex`。
- 注意：无 query 的直接 curl 可能拿到旧的中间缓存；线上带 query 已验证 3 处 zIndex。
