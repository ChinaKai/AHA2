# 0.5.165 部署状态

- 宠物设置收口第一版：
  - `owner_assistants` 新增 `pet_enabled`、`pet_brain_model_id`、`pet_actions`（migration v86）。
  - `PUT /api/v1/pet/settings`：总开关、形象、聊天模型、自主思考模型、动作白名单；保存后发布 pet_state。
  - 高级设置新增“宠物”卡片；点宠物对话框左上角头像也进同一设置弹层。
  - 设置页含：启用开关、形象卡片、走/跳/喜/怒勾选、聊天模型、自主思考模型。
  - 关闭宠物：隐藏宠物和面板、停止漫游/心跳（visible=false），设置弹层仍可从高级设置打开。
  - 聊天模型与自主思考模型分开；brain 调用 `assistantRuntimeByID(..., BrainModelID)`，空则跟随聊天模型。
  - 头部隐藏“动”按钮和模型下拉，头像改为打开设置。
- 提交：`51a7c57 feat: add a single pet settings surface`；未 push/tag。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-145348.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-145356`。
- Server `A5BCC2859A2A1C417E64A85A09B0D6F928EF822E244C51C9B4A2D311A8709163`；Tray `5C4E8B19293877BF600F739DDFDD25DF34263197F97C5DC35D38DCA631BA545A`。
- 进程 22:54:02 起；`/healthz` 200；`PUT /api/v1/pet/settings` 未登录 401。
- 线上 `/assistant.js` 含 `openPetSettings`、`updatePetSettings`、`pet-enabled`、`applyPetEnabled`。
- 测试：Pet settings HTTP 测试通过；App/Store/HTTP Pet/Assistant 通过；Web 133 pass；mock 验证打开设置、取消启用后宠物隐藏、保存请求体正确。
- 未做：身份/准则、活动流、日记、免打扰/每小时上限、事件驱动、`pet:control` token；动作勾选目前只影响设置 UI，模型脑提示词/解析尚未按白名单过滤。
