# 0.5.142 部署状态

- 提交：`6bb8ebc style: make the CRLF option a console toggle too`
- 变更：发送行的 CRLF 从原生 checkbox 改为 `.terminal-toggle` 按钮（与「选中即复制」同款），状态读 aria-pressed，HEX 模式禁用；`.hardware-toggle` 保持给资源列表开关用
- 候选：Server `aha2 0.5.142` SHA256 `F985464F79E0C390844B8D37EF5A61EF976E7AF1A36CF6665821033BC60A289F`；Tray `E9EA761E98459AE8A4C8A7688E612B23A9AB2D3ED35F3CC3206B846C90FFDA89`
- 结果 JSON：`dist/user-deploy-result-05142-crlf-toggle.json`

## 若被打断

1. 读结果 JSON；健康 200；安装哈希与候选一致。
2. 验证：调试终端发送行 CRLF 应为与工具栏同风格的按钮，HEX 模式下变灰不可点；发送行为不变（文本模式带 \r\n）。
