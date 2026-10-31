# 0.5.166 部署状态

- 变更：宠物设置入口从右上角头像改到**左侧头像/昵称**。
  - `.assistant-identity` 从 div 改成 button，点左侧头像、昵称、模型名都打开设置。
  - 右侧 `#assistant-character-button` 隐藏，头部只保留左侧身份 + 关闭。
  - 设置弹层内容不变：总开关/形象/四动作/聊天模型/自主思考模型。
- 提交：`9ca3c81 fix: open pet settings from the identity block`；未 push/tag。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-150450.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-150458`。
- Server `6D6AE0A7AD19B769EB63586BC779AF0A05A9D1F42D35C869D600294FAC154C20`；Tray `2D90D0B8B3E82556C342D0FB737F434CAEBBC19BBBE9685801B73945C5740B67`。
- 进程 23:04:58 起；`/healthz` 200。
- 线上 `/assistant.js` 含 `assistant-identity-button`、`openPetSettings`；`/styles.css` 含 `#assistant-character-button { display:none }`。
- 下载验证：390×844 mock 下左侧 identity 按钮存在，右侧头像不可见，点左侧打开设置弹层。
