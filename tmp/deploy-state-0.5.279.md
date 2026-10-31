# 0.5.279 部署状态（修正）

- 之前 18:02/18:08 的启动是我手动 `Stop-Process` 后复制 exe、再 Start-Process，未走部署脚本的 detached task，所以需要人工拉起。
- 已按部署技能改用 `scripts/build-and-deploy-windows-user.ps1 -DeployOnly`：
  - 候选 ServerSHA256：`7224F029AB50B652E937689D3642DA9D3017A2ACA8D0CBE9129D2C979680F8A2`
  - 安装后 `aha2.exe` 哈希一致。
  - 更新任务 `AHA2 User` 自动启动，18:12:56 新进程运行。
  - 任务 State=Running，Principal=Interactive/Limited。
  - 结果：`dist/user-deploy-result-20261029-101222.json`，Status=succeeded、Health=ok。
- 无 `panics.log`，18:02/18:08 不是自主行为崩溃，是手动部署重启。
- 以后坚持用部署脚本，不再手工 cp。
