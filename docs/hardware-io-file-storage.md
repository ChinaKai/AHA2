# 硬件终端 IO 改存文件（task-711）

## 结论

- 新产生的硬件终端输出（串口、网络、SSH、ADB 的收发记录）不再写入 SQLite，而是写到
  数据目录下的 `hardware-io/`，按流分目录，以 append-only 分段文件保存。
- 升级后服务启动时会一次性清空 SQLite 里的 `hardware_io` 旧行。这是 owner 在
  2026-11-20 的决定：旧终端输出不保留、不迁移。表结构保留为空表，回退到旧版本时
  仍能正常打开数据库。
- 清空只是把数据页标成空闲，`aha2.db` 文件不会自动变小；要缩小文件，需要停服务后
  运行一次 `aha2 db compact`。

## 盘点（基于 main `1cce48c`）

硬件 IO 在 SQLite 里只对应一张表，没有其他表保存终端内容。

| 项目 | 位置 |
| --- | --- |
| 表 `hardware_io`（sequence 是全局 AUTOINCREMENT，同时用作读取游标） | `internal/store/migrations.go:1409` |
| 索引 `idx_hardware_io_stream(task_id,hardware_id,transport,sequence)`，另有 `id UNIQUE` 自动索引 | `internal/store/migrations.go:1421` |
| 唯一写入点：串口/网络每次读到数据（最多 4 KiB）或发送命令时插入一行 | `internal/hardware/manager.go:747`（`readLoop` 在 523 行） |
| 读取：Web 终端轮询（每 600 ms 一次，带游标） | `internal/httpapi/hardware_handlers.go:537` |
| 读取：WebSocket 连接时回放 | `internal/httpapi/hardware_websocket.go:54` |
| 读取：Agent MCP `hardware_terminal_read` 与 `GET /api/v1/agent/hardware/{id}/terminal` | `internal/httpapi/agent_hardware_mcp.go:386`、`internal/httpapi/agent_handlers.go:29` |
| 保留策略：只有数据管理页上的手动预览和清理，默认保留 30 天，不会定时执行 | `internal/store/data_retention.go`（`HardwareIOPurgePlan` / `PurgeHardwareIO`） |
| 删除 Task 时靠外键 `ON DELETE CASCADE` 级联删除 | 同上表定义 |
| 同步：只同步硬件组，不同步终端 IO | `internal/sync/business_graph.go:181` |

生产库不在构建机上，这次没有直接测量生产库。下面的体积明细来自一个按同样结构造出来的
6.8 GB 副本：先用当前二进制建出真实 schema，再灌入 1200 万行终端数据和约 2 GB 的其他
数据。用 `dbstat` 量得：

| 对象 | 大小 |
| --- | --- |
| `hardware_io` 表 | 3223 MiB |
| `idx_hardware_io_stream` | 791 MiB |
| `sqlite_autoindex_hardware_io_1`（`id UNIQUE`） | 529 MiB |
| 其他数据（填充表） | 1958 MiB |
| 空闲页 | 0 |

终端数据连同索引约 4.4 GiB，平均每行约 375 字节，和 owner 描述的“6 GB 里约 4 GB”一致。
要拿生产库的真实明细，可以在**副本**上执行：

```sql
SELECT name, SUM(pgsize)/1048576 AS mib FROM dbstat GROUP BY name ORDER BY mib DESC;
PRAGMA page_count; PRAGMA freelist_count; PRAGMA page_size;
```

## 新的存储方式

```
<data-dir>/hardware-io/
  sequence.json                                  序号保留水位
  v1/<task>/<hardware>/<transport>/
    seg-<first>.active.jsonl                     正在追加的分段
    seg-<first>-<last>-<lastUnix>-<rows>.jsonl.gz  已关闭并压缩的分段
```

- 每行是一条 JSON 记录：序号、ID、方向、数据、编码、来源、时间。数据不是合法 UTF-8 时
  改用 base64 存放，避免 JSON 编码把原始字节替换成 U+FFFD。
- 当前分段超过 4 MiB，或者跨过 UTC 自然日，就关闭并用 gzip 压缩。压缩先写临时文件，
  fsync 后再 rename；崩溃后启动时会删掉残留的临时文件、截掉写了一半的最后一行；
  如果压缩文件已经完整落盘，会删掉对应的旧 active 文件。
- 序号仍然全局递增，接着旧表的最大值（`sqlite_sequence`）往下发，所以升级前客户端
  拿到的游标仍然有效。`sequence.json` 按 4096 个一块预留，并在首次使用时立即记下旧表的
  最大值；即使旧表清空、`sqlite_sequence` 丢失，或者所有文件都被清理掉，序号也不会复用。
- 读取语义保持不变：`HardwareIOPage` 的 `after/limit/latest_sequence/has_more` 与原来
  一致。游标已经追平时直接返回，不读文件；当前分段带每 128 条一个的偏移索引，跳过旧行时
  只解析行首的序号，不做完整 JSON 解码。实测在 3 MB 的当前分段上，一次接近末尾的轮询
  约 31 µs。
- 损坏的完整行会被跳过并计数（`corrupt_lines`），不会让读取接口返回 500。
- 删除 Task 时，在数据库事务提交后删除该 Task 的目录；删除项目/Workspace 走同一处理。
  每小时的维护还会清理已不存在的 Task 留下的目录，作为兜底。
- 只有主 Store 持有这个文件存储；`OpenSecondary` 共享同一实例，保证只有一个序号分配器。

### 考虑过的其他方案

| 方案 | 不选的原因 |
| --- | --- |
| 继续放 SQLite，只加自动清理 | 主库仍然承担高频小写入和 WAL 放大，清理后文件也不会变小 |
| 单独一个 SQLite 文件 | 能解决主库体积，但仍是每块输出一次事务，清理同样需要 VACUUM，且多一个需要备份和锁的库 |
| 按流分段的 append-only 文件（采用） | 写入就是一次 append；按整个文件删除即可完成清理；gzip 压缩后体积约为 SQLite 的 1/7 |

## 容量边界

| 项目 | 值 |
| --- | --- |
| 保留期 | 沿用数据管理页的 `hardware_io` 保留天数（默认 30 天），每小时自动执行 |
| 磁盘上限 | 默认 2 GiB，可用环境变量 `AHA2_HARDWARE_IO_MAX_BYTES` 覆盖（最小 64 MiB） |
| 超限行为 | 先删除所有流里最旧的已关闭分段，**不拒绝新的写入**；写入时一旦超过上限，最快 1 分钟内触发一次清理 |
| 单条落盘大小 | 典型终端文本约 50 字节/条（压缩后，含字段开销）；SQLite 约 375 字节/行（含索引） |

单日写入量估算（实测压缩比为 3.6 倍：payload/落盘）：

- 如果生产上的约 1200 万行是两个月左右积累下来的，那就是每天约 20 万行：SQLite 每天增长约
  70 MB，文件方式每天约 10 MB，30 天约 300 MB。这只是推测，上线后以数据管理页显示的
  实际值为准。
- 最坏情况：一个 115200 波特率的串口持续满速输出，每天约 1 GB 原始数据，压缩后约
  280 MB/天。这时 2 GiB 上限大约 7 天就会触发，比保留期更早删掉旧数据。
- 高速网络流（MB/s 级）会更快触发上限；上限检查在写入路径上，最多滞后约 1 分钟。

## 验证记录（构建机，Linux）

- 6.8 GB 副本，用生产同款驱动（modernc sqlite）清空 1200 万行：
  - 默认开着外键直接 `DELETE FROM hardware_io`：4 分 10 秒，WAL 涨到 4 GB；
  - 在独立连接上关闭外键后执行同一语句（SQLite 走 truncate 优化）：3.5 秒，WAL 4.7 MB。
    `hardware_io` 只作为子表引用 `tasks`，没有表引用它，所以关闭外键不会破坏约束。
- 真实二进制端到端：新版本在这个 6.8 GB 库上启动，清空用了 2269 ms，2.6 秒后
  `/healthz` 返回 200；`sequence.json` 记下 12000000。停服后执行
  `aha2 db compact`：20 秒，6.4 GiB → 1.9 GiB，完整性检查 ok，关键表行数一致。
  压缩后重启正常，未再触发清空。
- 回退：用 main 的旧二进制打开清空后的库，`/healthz` 正常。

Windows NTFS 上的清空和压缩耗时没有实测，预计比 Linux 慢。启动期间服务每 5 秒向 SCM
报告一次进度，不会因为启动稍慢而被判定超时。

## 上线步骤

1. 升级安装新版本（照常走官方安装器；安装器会先做备份）。
2. 服务启动时自动清空 `hardware_io`，日志里会有一行 `cleared legacy hardware_io rows`。
3. 停止服务，执行 `aha2 db compact --data-dir <数据目录>`（默认保留一份经过校验的
   快照备份），完成后再启动服务。需要的空闲磁盘约为清空后数据库大小的 2 倍。
4. 在数据管理页的“C. 硬件 I/O”弹窗里确认终端文件的占用和保留设置。

回滚：重新安装旧版本即可，旧版本能打开数据库。升级后写到文件里的终端输出，旧版本看不到；
旧版本运行期间写进 SQLite 的行，下次升级时会再次被清空。
