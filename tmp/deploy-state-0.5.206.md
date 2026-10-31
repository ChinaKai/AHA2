# 0.5.206 部署状态

- 截图问题：
  - 模型把 `pet_walk_path` 输出成 `<tool_call>...<tool_call_end>` 文本，且 points 用数组 `[[x,y],...]`；原解析只认结构化 tool_calls 和 `{x,y}`，所以只执行了 pet_mood，路径文本还显示出来。
  - 已新增文本工具调用兜底：解析 `<tool_call>name{json}<tool_call_end>`，清洗正文后执行。
  - `PetPoint.UnmarshalJSON` 同时接受 `{"x","y"}` 和 `[x,y]`。
  - `petActionsFromToolCalls` 重新覆盖 `pet_move_to` / `pet_walk_path`。
- 路径残影：
  - `walkPath` 途中每 ~95ms 克隆一个宠物残影，透明度 .35，0.9s 淡出；最多保留 24 个。
  - `.assistant-trail` z-index 9，宠物 10，不挡操作。
- 提交：`ecf498d fix: parse text tool calls and draw fading path trails`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-234958.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-235006`。
- Server `7C28BD84ABCC6B86E3C6943F90CBDEB6AF692F636C03B797880F26947B4BE382`；Tray `F398A453ED282118930ABDC6FB77F2E8FA5EBEDE7D7CF0D6FD2ED8F28CEDA6E9`。
- 进程 2026-10-29 07:50 起；`/healthz` 200；线上含 `assistant-pet-ghost`。
- 测试：文本 `<tool_call>` 解析、数组 points 映射、路径动画残影（9 个 ghost）通过。
