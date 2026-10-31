# 0.5.167 部署状态

- 变更：
  - 宠物设置去掉“动作”区；四个动作全量开放给模型，不再由 Owner 勾选。
  - 保存设置时客户端固定提交 walk/jump/happy/angry。
  - 聊天提示词修正：昵称随形象（咕咕嘎嘎/大肥鱼），不再自称“小A”；动作只列 walk/jump/happy/angry，不再列已删除的 run/crouch/turn/sad/joy。
  - 保留设置里的总开关、形象、聊天模型、自主思考模型。
- 提交：`3d9af32 fix: expose all pet actions to the model`；未 push/tag。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-151004.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-151012`。
- Server `A0F8695B18439569B0CE1A5871E8B6D4EA469EA967F356E8145BD0DDFDD9012F`；Tray `92DCFFE88C9E7B390BCC168D6E8C77E4A9C4373E9EEC4C044B08F03C5C34B4CE`。
- 进程 23:10:23 起；首次 host-check 撞到启动窗口（State=Ready/health 失败），8 秒后恢复 Running，连续 3 次 /healthz 200。
- 线上 `/assistant.js` 不含 `pet-action-options`；含 `assistant-identity-button`。
- 未做：身份/准则、活动流、日记、免打扰/每小时上限、事件驱动、pet:control token。
