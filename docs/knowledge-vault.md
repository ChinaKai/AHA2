# 文件知识库

文件知识库（knowledge vault）把知识以 Markdown 文件保存为正本，SQLite 只保存可重建的索引。Web 知识页只读；编辑在 VSCode 或 Obsidian 中完成，然后重建索引。

## 启用与关闭

在「高级设置 → 其他设置 → 文件知识库」中切换：

- 开关运行态生效，不需要重启 AHA2。
- 首次切换后，选择写入 `knowledge_vault_settings` 表；此后以数据库设置为准，`AHA2_KNOWLEDGE_VAULT_ENABLED` / `--knowledge-vault-enabled` 只作为从未在 Web 切换过时的启动默认值。
- 关闭时 7 个 `/api/v1/knowledge-vault/*` 数据接口返回 404，旧知识页与旧 API 不受影响。

vault 和索引路径仍由 `AHA2_KNOWLEDGE_VAULT_PATH`、`AHA2_KNOWLEDGE_VAULT_INDEX`（或对应 CLI 参数）决定；默认位于 `<data-dir>/knowledge-vault` 与 `<data-dir>/knowledge-vault-index.db`。

## 示例知识库（体验数据，可删除）

新 vault 可以在面板里点「初始化示例知识库」创建，也可以运行：

```sh
aha2 knowledge-vault init --samples --commit=false
```

示例是**体验数据**，用于快速看到新知识页的效果；它们不会导入真实旧知识，也不会修改旧知识库。固定生成 4 个文件：

- `global/general/示例/欢迎使用.md`
- `global/general/示例/编辑与同步.md`
- `global/general/示例/写作约定.md`
- `global/agent-lessons/示例-Agent经验.md`

示例只会写进尚未初始化（没有 `.aha-vault.json`）的 vault；已有 vault 调用 `--samples` 或初始化接口会跳过或返回 409，不会覆盖任何文件。验证满意后可以直接删除 `global/general/示例/` 和 `global/agent-lessons/示例-Agent经验.md`，再点「重建索引」。

## 编辑与重建

当前没有文件监听。用 VSCode 或 Obsidian 打开 vault 目录，保存后到「高级设置 → 其他设置 → 文件知识库」点「重建索引」，或在服务器上运行：

```sh
aha2 knowledge-vault rebuild-index
aha2 knowledge-vault search <关键词>
```

索引只服务于新知识页；旧知识表和旧知识页保持原样。正式迁移旧知识、删除旧知识库不在本功能范围内。