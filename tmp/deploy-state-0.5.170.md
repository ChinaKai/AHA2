# 0.5.170 部署状态

- 提示词只读 + 聊天思考/打字：
  - 设置页新增“当前提示词”：聊天/自主思考两段，系统管理、只读；`GET /api/v1/pet/prompts` 返回当前生成的 prompt（快照用占位符）。
  - 身份/准则输入框改为 readonly，并标注“系统管理，当前只读；后续再开放编辑”。
  - 聊天发送时：消息区立即出现三点 typing 行，面板模型名下方也有三点；回复到达后按 24ms/字打字输出。
  - 宠物说话的透明打字气泡、头旁思考点保留；`0.5.169` 的活动记录/身份注入仍在。
- 提交：`f54c02b feat: show read-only pet prompts and type chat replies`；未 push/tag。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-153422.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-153430`。
- Server `0309A7AB49D038E27D6CD1BE4A3D308896C7E94284423A3C4F1AF5DCC03D0E0D`；Tray `410AF3A61105FB23EC1DBFF4843CD525462E149B16FAFD66BAC99F417EFF1741`。
- 进程 23:34:36 起；`/healthz` 200；`/api/v1/pet/prompts` 未登录 401。
- 线上 `/assistant.js` 含 `pet-prompt-chat`、`assistant-typing`、`assistant-panel-thinking`、`typewriterMessage`。
- 已知未做：身份/准则/提示词的服务端写权限尚未显式拒绝（UI 已只读）；日记、免打扰、每小时上限、pet:control token 未做。
