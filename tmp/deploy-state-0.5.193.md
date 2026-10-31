# 0.5.193 部署状态

- 宠物设置折叠：
  - “身份与准则（系统只读）”“当前提示词（聊天 / 自主思考）”改成 `details`，默认收起。
- 设置显示所有工具：
  - `OwnerAssistantView` 新增 `tools`，从 `assistantPetTools()` 生成，六项：pet_play、pet_move、pet_mood、pet_open_task、pet_sleep、pet_wake。
  - 设置页“可用工具”列出名称和中文说明。
- 宠物被遮：
  - 0.5.192 的 z-index 1 方案在截图里被面板挡住；改回宠物 z-index 5、面板 4。
  - 面板打开时瞬移到面板上方；上方没空间时放面板内顶部。
  - 面板关闭时恢复全屏随机漫游；打开期间不漫游。
- 对话精简（0.5.192 已做）：删除筛选、工具行、Agent Update 行，只留正文 + 模型返回详情。
- 提交：`551f030 feat: fold pet settings and list available tools`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-174048.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-174057`。
- Server `4BBAED640E1CFE77A3CB548EF6112848AC7B6347C3828197ED9A8A8151C85FC4`；Tray `529217A9B7840D160F970383229EE807FF9847DF0F2552CC1F88E63639CEE6D7`。
- 进程 2026-10-29 01:41:02 起；`/healthz` 200；线上含 `pet-tool-item`、`pet-settings-details`、`positionAtDock`。
- 测试：设置 details 2 个且默认关闭；工具列表 6 项；面板 y=214 时宠物 y=140、z-index=5。
