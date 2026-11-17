# 离线数据库压缩

AHA2 不在服务运行期间执行 VACUUM。数据管理页只展示离线步骤和最近一次结果；真正的压缩由 CLI 在 AHA2 停止后执行。

## 命令

```sh
aha2 db compact --data-dir <数据目录> [--backup-dir <备份根目录>] [--json] [--keep-backup=true|false]
```

- `--data-dir`：AHA2 数据目录，默认取 `AHA2_DATA_DIR` 或 `.data`。
- `--backup-dir`：时间戳备份目录的父目录，默认 `<数据目录>/backups`。每次创建新的 `offline-compact-<UTC 时间>` 子目录，不覆盖旧备份。
- `--json`：stdout 输出机器可读结果，进度写 stderr。
- `--keep-backup`：默认 `true`，成功后也保留备份；只有显式 `--keep-backup=false` 才会在全部校验通过后删除备份。
- 退出码：`0` 成功，`1` 失败，`2` 用法错误，`3` 拒绝（服务在运行、数据库被锁、磁盘不足）。

命令会获取 `<数据目录>/aha2.instance.lock`。`aha2 serve` 在启动早期持有同一把锁直到退出，因此服务还在运行时命令会直接拒绝；随后命令在已锁连接上取得 SQLite 独占锁，再检查磁盘、创建备份、执行 VACUUM、`PRAGMA integrity_check` 和关键表行数对比。

## 备份格式

备份目录里的 `aha2.db` 是用 `VACUUM INTO` 在已锁连接上生成的一致快照，已包含提交到 WAL 的内容，因此不再单独复制 `aha2.db-wal` / `aha2.db-shm`。命令会记录大小和 SHA256，并用只读连接校验页数、`quick_check` 与关键表行数；`manifest.json` 记录文件哈希。

恢复时先停止 AHA2，然后用备份中的 `aha2.db` 覆盖数据目录里的同名文件，并删除残留的 `aha2.db-wal`、`aha2.db-shm`，最后重新启动服务。

## Windows 大库验证

Windows 的 `LockFileEx` 在大于 1 GiB 的数据库上可能在 PENDING_BYTE 处让另开句柄的普通读取返回 `ERROR_LOCK_VIOLATION`。实现因此改用已锁连接上的 `VACUUM INTO`，但 Linux 单测不能替代 Windows 实机验证。

在 Windows 构建机、预留至少约 4 GiB 临时空间后执行：

```powershell
$env:AHA2_WINDOWS_LARGE_DB_TEST = "1"
go test ./internal/store -run TestOfflineCompactionLargeDatabaseWindows -v -timeout 30m
```

该测试会创建一个 >1 GiB 的临时数据库，停止使用后执行离线压缩，并断言备份快照也大于 1 GiB、完整性和行数校验通过。默认不运行，必须显式设置环境变量。
