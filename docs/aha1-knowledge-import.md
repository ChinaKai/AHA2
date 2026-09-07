# AHA1 知识库迁移

AHA2 提供离线命令 `import-aha1-knowledge`，用于把 AHA1 的 `.aha/knowledge` 目录迁移为 AHA2 的树形 Knowledge、Knowledge Proposal、Project 与完整文本 Skill 包。导入器默认只执行 dry-run，不修改目标数据。

## 迁移规则

- `general` 保留为全局知识；`personal`、`capture` 与 Personal pending note 进入独立的 `AHA1 Personal Archive` Project，不会提升为全局知识。Capture 保持 `candidate`。
- `projects/<project-key>` 优先按规范化 Git remote 匹配现有 AHA2 Project，也可用 `--project-map` 显式指定。无法匹配时创建“待绑定项目知识库”；它不出现在普通 Project 列表，也不会把项目知识降级为全局知识。
- AHA1 路径生成稳定 ID；目录变成普通父文档，旧 `index.md` 变成 `overview`，AHA2 的系统根首页不会被替换。
- JSON 与简单 YAML frontmatter 都会被解析，正文不保留旧 frontmatter；旧元数据、来源路径和来源哈希记录在 Evidence 中。
- `.pending` 中的候选保持 pending proposal。
- `task_worklog` 默认不导入。重复运行新版本导入器时，会幂等删除此前导入的工作记录及其空目录，并为删除写入同步 tombstone，避免审批列表和其他设备重新出现。
- 与标题相同的首个 H1 会移除，避免 AHA2 物化文档重复显示标题；可解析的 Markdown 文档链接（包括 AHA1 以 navigation 根为基准的旧链接）会按新 slug 重写。无法解析的 WikiLink 会转成带失效说明的文本；绝对本地链接仍保留并进入 dry-run 报告。
- Skill 迁移 `SKILL.md`、`scripts/`、`assets/`、`agents/` 等 UTF-8 文本文件；`.pyc` 和含 NUL 的编译产物不会迁移。
- 导入前会清除密码、账号、Token、API key、Authorization、Cookie、私钥及常见凭据格式，报告只显示脱敏次数，不输出原值。
- 正文引用的 PNG/SVG/JPEG/GIF/WebP 会以严格 MIME、大小和 Base64 校验后的 `data:` 图片嵌入 Knowledge body，从而随 Knowledge 同步；未被正文引用的唯一图片内容会生成候选资产文档，物理重复副本按 SHA-256 合并。无法解析的图片引用仍会逐项报告。

## Dry-run

先对实际 AHA2 数据目录运行只读预检。命令会创建并删除临时 SQLite 快照，不迁移目标 schema，也不修改目标数据库。

```powershell
aha2.exe import-aha1-knowledge `
  --source "E:\AHA\.aha\knowledge" `
  --data-dir "C:\ProgramData\AHA2"
```

如果 legacy project key 应归入已有 AHA2 Project，可重复传入映射：

```powershell
--project-map "legacy-project-key=project_xxx"
```

报告中的 `knowledge_create` 包含为保持原目录层级而合成的父文档，因此会大于源 Markdown 数量。

## 正式导入

正式导入前停止使用目标数据目录的 AHA2 进程。确认停止后执行：

```powershell
aha2.exe import-aha1-knowledge `
  --source "E:\AHA\.aha\knowledge" `
  --data-dir "C:\ProgramData\AHA2" `
  --apply `
  --confirm-service-stopped
```

命令在写入前通过 SQLite 一致性快照备份 `aha2.db`，并备份现有 `knowledge/skills`。备份保存到数据目录的 `backups/pre-aha1-knowledge-import-*`。

导入完成后会验证每个 scope/project 恰有一个根节点、父节点完整、无环、兄弟 slug 唯一且 slug 合法；随后重新 dry-run。只有第二次检查为零新增、零修改时，结果才返回 `idempotent: true`。

待绑定项目知识库可在 Web“知识库 → 待绑定”中直接浏览。创建正式 Project 后选择“绑定”；绑定只新增关系，不改知识 ID、层级或正文。解绑或删除正式 Project 时，知识库会回到待绑定区并保留全部内容。一个 Project 可以绑定多个项目知识库。

若中途失败，不要删除备份。修复报告的问题后可重复运行；稳定 ID 和来源哈希保证已成功写入的内容不会重复创建。
