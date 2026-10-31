# 0.5.173 部署状态

- 变更：说话气泡按实际高度定位，避免长文遮住宠物。
  - `positionBubble` 读取 `bubble.offsetHeight`，`top = petY - height - 10`。
  - 上方空间不足才放下方，并对底部做 clamp。
  - 打字过程中每增一行会重新定位，气泡始终在宠物上方。
- 提交：`8a6194a fix: place speech above the pet by its measured height`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-155002.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-155010`。
- Server `23A096B98EDF2644D67B1F3B1DEA37FA7B739D2D867057D0ACC7D5F729E3A095`；Tray `B8C9D994838D1F2570AD2952FD8F75039B83AE9FD7F4708F43B16FA664671FB6`。
- 进程 23:50:15 起；`/healthz` 200。
- 390×844 长文测试：宠物 y=758 h=58，气泡 y=651 h=97，重叠 false。
