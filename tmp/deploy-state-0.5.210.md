# 0.5.210 部署状态

- 残影不显示：
  - 原因是 `prefers-reduced-motion: reduce` 时直接 return，且样式依赖 CSS 缓存。
  - 已去掉 reduce-motion 跳过；残影的 position/size/opacity/transition/transform 全部 inline 设置，不依赖缓存 CSS。
  - 每只按剩余路程淡出，动作结束清空。
- Responses 协议报 `responses response has no text`：
  - 增加 loose 递归解析：从任意嵌套的 `function_call`/`tool_call`/`tool_use` 提取 name/arguments，并递归找 `output_text`/`text`。
  - 解析失败时错误里附带 raw 前 500 字，便于定位 Provider 包裹格式。
- 提交：`33635d4 fix: parse wrapped Responses and harden path ghosts`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-002900.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261029-002908`。
- Server `84B6B4EC016944D86E103348B1156D37D5A78768E0970B218EABDD5F5938618D`；Tray `280AE1CE212766EC32CA5DAB0910ACF8573F992F8DA03309F70C1AA1669B7249`。
- 进程 2026-10-29 08:29 起；`/healthz` 200；线上含 `ghost.style.opacity`。
- 测试：mock 3s 路径，1.2s 有 12 个残影 opacity 0.12 起，3.4s 清空。
