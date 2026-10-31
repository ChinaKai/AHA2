# 0.5.204 部署状态

- 支持连续路径/圆圈：
  - 新增 `pet_walk_path`：points 2–32 个 0–1 视口坐标，一个工具完成整条路线。
  - 新增 `pet_walk_circle`：中心 x/y、radius、turns、duration_ms；客户端生成圆周点并连续走。
  - 聊天提示词明确：路线/圆圈用一个路径工具完成，不要因为“最多两个工具”拒绝画圈。
  - 工具总数 9：play/move/move_to/walk_path/walk_circle/mood/open_task/sleep/wake。
  - 客户端 `walkPath` rAF 顺序插值，总时长按 duration（默认 6s），结束回 idle；不受 resize 取消。
- 提交：`c45d091 feat: let the pet walk paths and circles`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-233358.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-233407`。
- Server `F52DBBC98788E65FC401BD912741462E59999A41B1AC1924A8345CC7CBB1146C`；Tray `D9121E63D36570FAEBF2AD5B31179C47243DB74073F44B58EF963F8D69338D56`。
- 进程 2026-10-29 07:34 起；`/healthz` 200；线上含 `walk_path`/`walk_circle`。
- 测试：mock walk_circle center .5,.5 radius .18 turns1 duration4s；0.5/1.5/3/4.5s 位置分别变化，绕中心移动。
