# 0.5.179 部署状态

- 变更：再慢 10%，并修复前三行没占满。
  - 宠物气泡 34→37ms/字；面板 29→32ms/字。
  - 气泡宽度 258→264px，字号 12→11px，确保一行能容下 23 个中文字符。
  - 3 行视窗 60px；内容文字仍每 23 字强制换行，scrollTop 显示最新三行。
- 提交：`f932a5a fix: fill three speech lines at a slower typing speed`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-161859.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-161907`。
- Server `BD7B445E988831FC24849A9F41C0B539D21E359C18C6005D8C44A4D0710194F9`；Tray `58D9E8F31319562F8F8DFFD42CAEF26ED1AE8BE9AFC15A58E3C624411C6D8E7A`。
- 进程 2026-10-29 00:19:17 起；`/healthz` 200；线上 `assistant.js` 含 `}, 37)`、`}, 32)`、`Math.min(264`。
- 实测：字号 11、行高 18、scrollHeight=54（正好 3 行），前两行各 23 字，不再提前换行。
