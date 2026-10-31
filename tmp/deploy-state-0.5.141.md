# 0.5.141 部署状态

- 提交：`0fbeb90 style: make the terminal auto-copy toggle match the console toolbar`
- 变更：把「选中即复制」从原生 checkbox 改为与工具栏其他按钮同高同宽的 toggle 按钮（aria-pressed + active 蓝底 + 状态圆点）
- 候选：Server `aha2 0.5.141` SHA256 `D852E32482734762A1B8A448D83D96D99BFC2E877D65A94F817826CFFADA51EF`；Tray `12A355299DD68874908B5A2EFB3E39AF444B420F6788370384B834E0DA52C84E`
- 结果 JSON：`dist/user-deploy-result-05141-terminal-toggle.json`

## 若被打断

1. 读结果 JSON 确认 Status；健康 200；安装哈希与候选一致。
2. 验证：硬件调试终端工具栏，「选中即复制」应与「连接/断开」同风格，开启时蓝底。
