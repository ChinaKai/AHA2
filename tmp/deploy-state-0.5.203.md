# 0.5.203 部署状态

- 时灵时不灵的修复：
  - 模型会传 `duration_ms:800`；客户端原样采用，0.8s 就结束。现在忽略模型 duration，所有工具移动固定 3 秒。
  - 键盘弹起/窗口 resize 会调用 positionAtDock 取消动作；新增 `actionMoveUntil`，动作期间 resize/dock 不再覆盖。
  - 聊天响应 actions + 1.5s 去重继续保留。
- 提交：`98b9055 fix: keep action movement through resize and keyboard`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-231823.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-231831`。
- Server `EDB350EB39A3C6314D3E8867D9A594B48DDBEAF6FB634516EEDFBB55F9159600`；Tray `F6D75F64DF257FDBE310CD4A29859D4E80A5F6CCE0B3E44DED3103D09D935D17`。
- 进程 2026-10-29 07:18 起；`/healthz` 200；线上含 `actionMoveUntil`。
- 测试：mock 响应 action duration=800，发送后 resize 到 390x640；1.2s 时仍在移动，3.3s 后到 (299.5,52.4) 完成，没有被 resize 取消。
