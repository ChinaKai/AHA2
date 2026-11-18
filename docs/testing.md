# 测试与快速验证

AHA2 的 Go 测试分两档：默认全量覆盖，以及供代码评审使用的 `-short` 快速档。快速档不改变默认覆盖面，只跳过少数依赖真实计时器等待的重集成用例。

## 快速档

适用场景：

- 独立评审 Task 的快审和定向验证；
- 本地改动后的快速回归，不想等整包跑完。

不适用：合并前终验、发布门禁、需要全量覆盖的回归。合并前仍应跑默认命令（`go test ./...`），并按平台给足 `-timeout`。

用法：

```bash
scripts/test-review-fast.sh
# 等价于
go test -short ./internal/app ./internal/httpapi ./internal/store -count=1 -timeout 300s
```

脚本会打印实际命令和耗时。`GO_BIN` 可替换 Go 可执行文件，`TEST_TIMEOUT` 可调整超时，额外参数原样传给 `go test`，例如只跑 Pet 相关用例：

```bash
scripts/test-review-fast.sh -run 'Pet'
```

## `-short` 跳过清单

只有下面这些用例会因为 `-short` 跳过。判断标准是串行实测里固定等待占大头：换更快的机器也省不下来，只能靠 `-short` 裁掉。它们都靠真实计时器或真实传输等待推进；其余用例在两种模式下完全一致。

| 包 | 用例 | 串行实测 | 跳过原因 |
| --- | --- | --- | --- |
| internal/app | TestMultiAgentFallbackCoalescesResultsWithinWakeWindow | 3.28s | 等 3s 的多 Agent 唤醒窗口 |
| internal/httpapi | TestDesktopStreamStalledAckTimesOut | 10.33s | 等真实 ACK 超时（12s） |
| internal/httpapi | TestDesktopStreamRevokedOnLogoutStopAndControlChange | 3.64s | 4 次真实 WebSocket 撤销往返 |
| internal/httpapi | TestDesktopStreamAuthExpiryClosesIdleConnection | 2.52s | 等真实 2s 认证 TTL |

默认模式（不加 `-short`）不会跳过任何用例。`-short` 只影响运行期跳过，不改变测试清单，可以用下面的命令核对：

```bash
go test -list '.*' ./internal/app ./internal/httpapi ./internal/store
```

## 关键覆盖

评审最依赖的场景在快速档里仍然真实执行，都不在跳过清单里：

- Pet 工作状态/注意力：`TestPetWorkStateShowsApprovalsAndPriorWork`、`TestPetOpenTaskAttentionReasonSeparatesWaitingKinds`（internal/app）
- Review 关联：`TestPetCodeReviewLinksMigrationAndPersistence`（internal/store）、`TestPetCompletionUsesLatestReview`、`TestPetCodeReviewRejectsSpoofedAndMismatchedLinks`（internal/app）
- Agent 继承幂等：`TestTaskAgentAPIIsolationAndConfigInheritance`（internal/httpapi）
- MCP 身份边界：`TestPetMCPPermissionsEnforcedAtCallTime`（internal/app）、`TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools`（internal/httpapi）
- 只读兼容：`TestPetRequestCodeReviewCreatesReadOnlyHighTask`（internal/app）、`TestRemoteTaskMirrorIsListedAndReadOnly`、`TestSystemManagedResourcesAreReadOnly`（internal/httpapi）

定向验证都带 `-short`，看到 `--- PASS` 才说明用例真的执行了：

```bash
go test -short ./internal/app -count=1 -v \
  -run 'TestPetWorkStateShowsApprovalsAndPriorWork|TestPetOpenTaskAttentionReasonSeparatesWaitingKinds'
go test -short ./internal/store -count=1 -v -run 'TestPetCodeReviewLinksMigrationAndPersistence'
go test -short ./internal/app -count=1 -v \
  -run 'TestPetCompletionUsesLatestReview|TestPetCodeReviewRejectsSpoofedAndMismatchedLinks'
go test -short ./internal/httpapi -count=1 -v -run 'TestTaskAgentAPIIsolationAndConfigInheritance'
go test -short ./internal/app -count=1 -v -run 'TestPetMCPPermissionsEnforcedAtCallTime'
go test -short ./internal/httpapi -count=1 -v -run 'TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools'
go test -short ./internal/app -count=1 -v -run 'TestPetRequestCodeReviewCreatesReadOnlyHighTask'
go test -short ./internal/httpapi -count=1 -v \
  -run 'TestRemoteTaskMirrorIsListedAndReadOnly|TestSystemManagedResourcesAreReadOnly'
```

## 测量记录

2026-11-18 在 Linux builder（32 核，go1.27.4）实测，三个包各自单独执行默认模式：

| 包 | 命令 | 退出码 | 墙钟 |
| --- | --- | --- | --- |
| internal/app | `go test ./internal/app -count=1 -timeout 1800s` | 0 | 114s |
| internal/httpapi | `go test ./internal/httpapi -count=1 -timeout 1800s` | 0 | 96s |
| internal/store | `go test ./internal/store -count=1 -timeout 1800s` | 0 | 78s |

快速档 `go test -short ./internal/app ./internal/httpapi ./internal/store -count=1 -timeout 300s` 同一环境实测退出码 0，墙钟 120s（三包并行：app 119.5s、httpapi 85.4s、store 87.3s）。

`go test -json` 里 `t.Parallel` 用例的 Elapsed 含排队时间：默认并行跑时 store 有 29 个用例显示 ≥10s，但用 `-parallel 1` 复测后最长只有 0.54s。因此跳过清单以串行实测为准：对默认并行跑中最慢的候选（app/store 取 ≥5s、httpapi 取 ≥2s）逐个串行复测，最长为 app 3.28s、httpapi 10.33s、store 0.54s；候选里除上表的 4 个用例外，其余都低于 0.7s。

整包耗时主要来自每个测试新建 SQLite 并重放迁移的固定成本，而不是少数慢用例。Windows 上这项成本明显更高（历史实测 internal/app 约 624s）。`-short` 只能去掉真实计时器等待，消不掉这部分成本；在更慢的机器上跑默认套件或快速档时，可能需要通过 `TEST_TIMEOUT`（脚本）或 `-timeout`（直接调用）加大超时。
