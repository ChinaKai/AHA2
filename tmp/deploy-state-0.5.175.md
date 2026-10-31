# 0.5.175 部署状态

- 变更：宠物说话改为头顶思考点位置的固定单行。
  - 每页 18 字、气泡固定 22px 高、单行 `nowrap`。
  - 位置按宠物实际 `getBoundingClientRect` 计算：`top = pet.top - 22 - 6`，不再受像素坐标漂移影响。
  - 一页打满清空，从头继续打剩余页；位置不变；最后一页停 3 秒淡出。
  - 隐藏气泡里的昵称，只显示一行正文。
- 提交：`8e7cfc3 fix: keep pet speech in one line at head level`。
- 部署：第一次因 `aha2-tray.exe` 被占用失败回滚；重试 `Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-155913.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-155926`。
- Server `41405FD29D589E48FD7F21937CE3ACC770A487C16BCF677ADA6AB36508C972EE`；Tray `21F506E26666A0423A93637BC0F55BA5C35390A03C525BE6CCF0FBCE213C8EBE`。
- 进程 23:59:26 起；`/healthz` 200；线上 `/assistant.js` 含 `SPEECH_PAGE_CHARS = 18`、`SPEECH_BUBBLE_HEIGHT = 22`。
- 390×844 测试：宠物 y=758 h=58，单行气泡 y=730 h=22，文字 18 字/页；第二页 13 字，位置不变。
