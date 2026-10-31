# 0.5.146 部署状态

- 提交：`5f750ea feat: let a group post go out without mentioning anyone`
- 变更：群发 outreach 的 mention 列表改为可选（空数组=纯群发不 @）；>5 仍 400；填写时仍校验必须是本会话联系人。Prompt 同步说明「纯群发传空数组，别随机 @ 人」。
- 背景：Owner 从 Web 让 Agent 发消息/附件到群里时，旧逻辑强制 1–5 个 @，导致随机 @ 到无关成员
- 候选：Server `aha2 0.5.146` SHA256 `4F110B01D6B3EECD3D51D08473447AA17F8917A0FC1D9A79ECBAB70ED52C080C`；Tray `D28B354A5C45C5D0939B55056EB91FCEF6406AEB8F7525787BBE0A18CC99CC30`
- 结果 JSON：`dist/user-deploy-result-05146-plain-group-post.json`

## 若被打断

1. 读结果 JSON；健康 200；安装哈希与候选一致。
2. 验证：让 Agent 往群发一条消息（mention 空数组），群里应只出现消息本身、没有任何 @。
