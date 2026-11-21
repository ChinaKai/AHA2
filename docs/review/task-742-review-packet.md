# task-742 review packet：ADB forward 跨 Task rebind 记录失效与隔离回归

本轮基线：`eb0d47a`（main HEAD）。本轮实现：`da50656`。分支 `aha/task_7d9265e0b5a370f99ba4`。未合并 main、未 push、未部署、未操作真实 adb 或硬件。

本轮只改 `internal/httpapi`：`agent_adb_forwards.go`（记录失效语义）、`agent_handlers.go`（forward 变更串行化与注释）、`server.go`（一个互斥字段）、`agent_adb_forwards_test.go`（回归与变异用例）。未改 `--list` 输出、`reverse`、真实执行器或无关文件。

## 结论

- **S2（跨 Task rebind 后旧记录仍可删）已修**：普通 add 成功时，同名 local 在所有其他 binding 下的记录立即失效，新 add 的 binding 成为唯一 owner。A 记账 `tcp:22100`、B 普通 add 同端口（AOSP rebind 到 B）后，A 的 `--remove tcp:22100` 在授权阶段被拒，执行器 0 次；B 仍可正常删除。
- **授权与执行之间的并发窗口已关**：forward add/remove 从归属检查到执行、记账全程串行，避免“remove 已通过检查、B 的 add 插进来 rebind、remove 再执行”的 TOCTOU。
- **S1（变异测试缺口）已补**：新增跨 Task rebind、不同 serial/target、registry key 组成、registry 归属转移、并发串行五类测试。去掉 key 的 taskID、去掉 target、去掉失效循环、去掉串行锁四种变异都会让测试变红（真实断言失败，不是 build failed）。
- **不需要在 remove 前跑 `forward --list` 复核**：所有经 Agent API 的成功 add 都必须经过同一个 `agentADBExec`，而引起 rebind 的那次 add 正是会成功并触发失效的那次；remove 的授权、执行和记账又在同一把锁内完成。因此 Agent API 内部不存在“记录还在、listener 已改绑”的窗口。`--list` 复核只能覆盖 Agent API 之外的改动，本轮按任务要求不扩大 `--list` 的处理与信息暴露。
- 既有行为不变：`--remove-all` 仍无条件拒绝；`--remove` 仍只允许本 Task 通过同一 binding 创建过的 local；`--list`、普通 add、`--no-rebind` add 仍可用；HTTP 与 MCP 共用同一处理函数。

## 问题与根因

task-739 的 L2 评审留下两个问题：

1. **S2 组合路径**。AOSP 的 listener 表是 adb server 级的，key 只有 local 名。`install_listener` 在未带 `--no-rebind` 且同名 listener 已存在时，会把旧 listener 改绑到当前 transport。task-739 的 registry 只按 `(task, group, target)` 记账：
   - Task A 通过绑定 `SA` add `tcp:8080`，registry 记 `SA -> tcp:8080`；
   - Task B 用普通 add 对同一 local 发起 forward，adb 成功 rebind，registry 记 `SB -> tcp:8080`，但 A 的记录没有动；
   - A 再 `--remove tcp:8080`，`allows(SA, tcp:8080)` 仍为真，实际删掉的是 B 的 listener。
2. **S1 测试缺口**。旧测试只覆盖同一 scope 的 add→remove 和跨 group 的拒绝；从 `adbForwardScope` 去掉 `taskID` 或 `target` 后测试仍然全绿，说明“不同 Task 共用同一 group/目标”和“同一 binding 改绑目标”两条隔离语义没有测试锁住。

另外，旧实现先 `allows` 再执行 remove，两步之间没有串行化：即使 add 侧会失效旧记录，一个已经通过检查的 remove 仍可能在并发 add 之后再执行，删掉新 listener。这是同一问题的并发形态，本轮一起关闭。

## 方案

### 1. 成功 add 转移 local 名的所有权

`adbForwardRegistry.add` 在记录当前 scope 之前，先删除其他所有 scope 下的同一个 local 名；只保留一个 owner。生效条件是 add 的 exit code 为 0（handler 原有逻辑），因此：

- 普通 add 成功并 rebind：旧 owner 的记录在 add 返回后立刻消失，后续 remove 被拒；
- `--no-rebind` add 成功：按 AOSP 语义只可能在同名 listener 不存在时成功，此时其他 scope 的记录本来就已经失效，清理它是正确的；
- add 失败：不进入 registry，旧记录保持（fail closed，不会因为一次失败 add 放行删除）；
- `tcp:0`：仍按 stdout 记录的 resolved 端口记账；adb 为 `tcp:0` 选择空闲端口，不会 rebind 别的 listener，通用失效逻辑对它无害。

失效只看 local 名，不看 remote：AOSP 的 listener 表只按 local 名索引，remote 不同也会 rebind。

### 2. remove 前不跑 `--list` 的理由

`agentADBExec` 是 Agent API 唯一的 ADB 执行入口（`executeADB` / `executeADBTCP` / `executeADBUSB` 只在这里调用）。一次 add 只要能让 adb rebind，它就会成功返回 0，从而走 `add()` 的失效路径；一次 remove 只有在本 scope 仍是 owner 时才会执行。两者都在同一把 `adbForwardMu` 内完成，因此：

- Agent API 内部不存在“remove 已授权 → 别的 add 改绑 → remove 执行”的窗口；
- 记录失效发生在 add 成功之后，不依赖 `--remove` 的返回值，也不是“先删再看返回值”的猜测；
- `forward --list` 只能补充检测 Agent API 之外的 add/remove（owner 手工执行、adb server 重启、其他进程），这类改动本来就可能与任何进程内记录竞争；为覆盖它而解析 `--list` 会扩大本轮范围，也会触及任务明确要求不扩大的 `--list` 信息面。因此保留为残余风险，不在 remove 前增加一次执行。

### 3. forward 变更串行化

`agentADBExec` 在识别出 forward add/remove 后获取 `s.adbForwardMu`，直到 handler 返回才释放。锁覆盖：remove 的 `allows` 检查、参数校验、adb 执行、registry 更新、响应写出。`--list` 不拿锁（只读，不参与授权）。锁的获取顺序固定为 `adbForwardMu -> registry.mu`，registry 内部不会反向获取。

这样做的代价是同一进程内的 forward add/remove 串行执行；forward 是低频操作，远小于跨 Task 误删的代价。锁在 adb 执行期间持有，最长受现有 120 秒超时约束。

## AOSP 依据

沿用 task-738/739 已记录的 AOSP main 只读结论（本轮未重新下载源码，也未在真实 adb 上执行）：

- `client/commandline.cpp`：`--remove <local>` → `killforward:<local>`；普通 add / `--no-rebind` → `forward:...`。
- `adb.cpp` 的 `handle_forward_request`：`killforward` 调 `remove_listener(pieces[0], transport)`。
- `adb_listeners.cpp`：`remove_listener` 只比较 local 名，不比较 transport；`format_listeners` 遍历全部 listener；`install_listener` 在未禁止 rebind 时会替换同名 listener，并在 `tcp:0` 时把 local 名改写成 `tcp:<resolved>`。

这些结论解释了为什么必须按 local 名做全局所有权，而不能只按 scope 记账。

## 测试矩阵

新增/修改的测试都在 `internal/httpapi/agent_adb_forwards_test.go`：

| 场景 | 测试 | 关键断言 |
| --- | --- | --- |
| A 记账 → B 同端口普通 add → A remove | `TestAgentADBForwardRebindInvalidatesPreviousOwner`（HTTP + MCP） | B add 成功；A remove 返回 `adb_forward_remove_not_allowed` 且执行器 0 次；B remove 成功；A 的另一个 local 不受影响 |
| 不同 serial/target 不串 | `TestAgentADBForwardBindingIsolation` | 同一 Task 两个 binding（SERIAL-A/B）互不可删；同一 binding 改绑到 SERIAL-C 后，SERIAL-A 的旧记录不能删 |
| key 组成 | `TestADBForwardScopeSeparatesTaskGroupAndTarget` | task、group、target 任一不同，scope 必须不同 |
| registry 归属转移 | `TestADBForwardRegistryAddTransfersOwnership` | 新 scope add 同名 local 后旧 scope 失去权限；不同 local 不受影响；remove 后自己的记录消失 |
| 并发 add/remove | `TestAgentADBForwardMutationsAreSerialized` | A 的 remove 在 executor 中阻塞时，B 的 add 不得进入 executor；释放后 B 获得所有权 |
| 既有策略 | `TestAgentADBForwardDeletePolicy`、`TestClassifyADBForward`、`TestAgentADBExecParsesCommandBeforeApplyingPolicy`、`TestParseADBCommandSkipsGlobalOptionsAndWaitPrefixes` | 未改动，全部保持通过 |

### 实跑命令与退出码

环境：本 worktree，`go1.27.4 linux/amd64`。

```text
go build ./...
退出码 0

go vet ./internal/httpapi
退出码 0，无诊断

go test ./internal/httpapi -run 'TestClassifyADBForward|TestADBForwardScopeSeparatesTaskGroupAndTarget|TestADBForwardRegistryAddTransfersOwnership|TestAgentADBForwardDeletePolicy|TestAgentADBForwardRebindInvalidatesPreviousOwner|TestAgentADBForwardMutationsAreSerialized|TestAgentADBForwardBindingIsolation|TestAgentADBExecParsesCommandBeforeApplyingPolicy|TestParseADBCommandSkipsGlobalOptionsAndWaitPrefixes' -count=1 -timeout=15m
退出码 0，ok

go test ./internal/httpapi -run 'TestADBForwardScopeSeparatesTaskGroupAndTarget|TestADBForwardRegistryAddTransfersOwnership|TestAgentADBForwardDeletePolicy|TestAgentADBForwardRebindInvalidatesPreviousOwner|TestAgentADBForwardMutationsAreSerialized|TestAgentADBForwardBindingIsolation' -race -count=1 -timeout=20m
退出码 0，ok

git diff --check
退出码 0
```

说明：本机临时 SQLite 的迁移 fsync 抖动很大，同一组测试观测到 27 秒到 243 秒不等；退出码和断言结果不受影响。

### 变异矩阵

方法：在 Task scratch 目录下复制 `agent_adb_forwards.go` / `agent_handlers.go`，用 `go test -overlay` 替换，worktree 源文件不动。每个变异先用 `diff` 确认替换真的发生（非空且只改目标行），再看测试结果；没有把 `[build failed]` 当作测试抓住。

| 变异 | 改动 | 目标测试 | 结果 |
| --- | --- | --- | --- |
| M1 去掉 taskID | `adbForwardScope` 返回 `groupID + "\x00" + target` | `TestADBForwardScopeSeparatesTaskGroupAndTarget` + `TestAgentADBForwardRebindInvalidatesPreviousOwner` | 退出码 1；`scope for a different task equals...`、A 的 remove `allowed=true` |
| M2 去掉 target | `adbForwardScope` 返回 `taskID + "\x00" + groupID` | `TestADBForwardScopeSeparatesTaskGroupAndTarget` + `TestAgentADBForwardBindingIsolation` | 退出码 1；`scope for a different target equals...`、改绑后的 remove `allowed=true` |
| M3 去掉失效循环 | `if otherScope == scope` 改为 `if otherScope != ""`（全部跳过） | `TestADBForwardRegistryAddTransfersOwnership` + `TestAgentADBForwardRebindInvalidatesPreviousOwner` | 退出码 1；旧 owner 仍可 remove、A 的 remove `allowed=true` |
| M4 去掉串行锁 | 删除 `adbForwardMu.Lock/Unlock` 三行 | `TestAgentADBForwardMutationsAreSerialized` | 退出码 1；`the rebinding add ran while the authorized remove was still in flight` |

四个变异都确认没有 `[build failed]` / `[setup failed]`；恢复源码（不带 overlay）后同一组测试退出码 0。

## 残余风险

- **Agent API 之外的 forward 改动**：owner 在宿主手工 add/remove、adb server 重启或别的进程改绑同名 local，进程内记录无法感知。极端情况下旧记录可能允许一次 remove，删掉外部新建的 listener。彻底消除需要在 remove 前执行 `forward --list` 并按 serial 复核；本轮按任务要求未扩大 `--list` 处理，保留为已知风险。
- **跨进程并发**：`adbForwardMu` 只串行化本进程的 Agent API 调用；本进程与外部 adb 操作之间没有排序保证。
- **进程重启**：记录丢失，重启前创建的 forward 不能再通过 Agent API 删除（fail closed）。
- **`tcp:0` 输出格式**：仍依赖 adb 在 stdout 打印裸端口号；解析不到就不记账，之后 remove 被拒（fail closed）。
- **`forward --list` 仍会列出其他设备**：task-738 已记录的行为，本轮未改，也未借它做授权。
- **未做真实 adb / 设备验证**：Builder 上没有 adb；rebind、remove 的结论来自 AOSP main 源码和注入执行器的 HTTP/MCP 测试，不构成实机协议验证。生产宿主是 Windows，也未验证。
- **未逐版本核对旧 adb**：部署用的 platform-tools 若对 `--remove` / `--no-rebind` 有额外语法，仍由现有 fail-closed 分类器拦截，但未逐版本测试。

## 改动文件

```text
internal/httpapi/agent_adb_forwards.go
internal/httpapi/agent_adb_forwards_test.go
internal/httpapi/agent_handlers.go
internal/httpapi/server.go
```

## 本轮完整 diff（eb0d47a → da50656）

~~~~diff
diff --git a/internal/httpapi/agent_adb_forwards.go b/internal/httpapi/agent_adb_forwards.go
index f4dfdef..76f79ad 100644
--- a/internal/httpapi/agent_adb_forwards.go
+++ b/internal/httpapi/agent_adb_forwards.go
@@ -42,16 +42,25 @@ func registeredADBForwardLocal(local string, result hardware.ADBExecResult) stri
 }
 
 // adbForwardRegistry records the forward local names this AHA process created
-// through the Agent ADB executor, scoped to one Task hardware binding. adb
-// forward --remove compares only the local name and not the device transport,
-// so without this record one Task could delete another device's forward just
-// by knowing (or reading from --list) the port name.
+// through the Agent ADB executor, and which Task hardware binding currently
+// owns each name. adb keeps one host-wide listener table keyed only by the
+// local name: forward --remove compares that name and not the device
+// transport, and a successful plain add rebinds an existing same-name
+// listener to the caller's transport. Without this record one Task could
+// delete another device's forward just by knowing (or reading from --list)
+// the port name; with a per-scope record only, a rebind-permitting add would
+// also leave the previous owner's name removable against the new listener.
+//
+// A successful add therefore takes ownership of its local name away from every
+// other binding. That invalidation happens on the add path itself, not from
+// the later --remove exit code, so a rebind can never leave an older record
+// authorized. A successful remove drops the name from its own scope.
 //
 // The registry is deliberately in-process. After a restart previously created
 // forwards are no longer removable through the Agent API until they are
-// created again; that is stricter, not permissive. A forward removed outside
-// this path (owner on the host, adb server restart) can leave a stale entry
-// for a reused port; see the review packet for that residual risk.
+// created again; that is stricter, not permissive. A forward added or removed
+// outside this path (owner on the host, adb server restart) can leave a stale
+// entry for a reused port; see the review packet for that residual risk.
 type adbForwardRegistry struct {
 	mu      sync.Mutex
 	byScope map[string]map[string]struct{}
@@ -67,6 +76,19 @@ func (registry *adbForwardRegistry) add(scope, local string) {
 	}
 	registry.mu.Lock()
 	defer registry.mu.Unlock()
+	// An add that reaches the registry succeeded, and adb may have rebound an
+	// existing listener for this local name to the current transport whatever
+	// form was used (--no-rebind succeeds only when nothing was bound). The
+	// previous owner's record no longer describes a listener it can remove.
+	for otherScope, locals := range registry.byScope {
+		if otherScope == scope {
+			continue
+		}
+		delete(locals, local)
+		if len(locals) == 0 {
+			delete(registry.byScope, otherScope)
+		}
+	}
 	locals := registry.byScope[scope]
 	if locals == nil {
 		locals = map[string]struct{}{}
diff --git a/internal/httpapi/agent_adb_forwards_test.go b/internal/httpapi/agent_adb_forwards_test.go
index c74f20b..f47059e 100644
--- a/internal/httpapi/agent_adb_forwards_test.go
+++ b/internal/httpapi/agent_adb_forwards_test.go
@@ -3,9 +3,11 @@ package httpapi
 import (
 	"context"
 	"encoding/json"
+	"fmt"
 	"net/http"
 	"net/http/httptest"
 	"strings"
+	"sync"
 	"sync/atomic"
 	"testing"
 	"time"
@@ -219,3 +221,261 @@ func TestAgentADBForwardDeletePolicy(t *testing.T) {
 	expectAgentADBExec(t, server.URL, token, tcp, []string{"forward", "--remove", "tcp:22005"}, false, &executions, false, "adb_forward_remove_not_allowed")
 	expectAgentADBExec(t, server.URL, token, usb, []string{"forward", "--remove", "tcp:22005"}, false, &executions, true, "")
 }
+
+// adbForwardTestUSBGroup builds a read-write USB ADB binding. The serial is the
+// configured target and therefore part of the registry scope.
+func adbForwardTestUSBGroup(id, serial string) domain.HardwareGroup {
+	now := time.Now().UTC()
+	return domain.HardwareGroup{
+		ID: id,
+		Network: domain.HardwareNetworkConfig{
+			Protocol:      domain.HardwareProtocolADB,
+			ADBConnection: domain.HardwareADBConnectionUSB,
+			ADBSerial:     serial,
+		},
+		Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadWrite,
+		CreatedAt: now, UpdatedAt: now,
+	}
+}
+
+// newADBForwardTestTask creates a Task with the given bindings plus a running
+// turn token, which is what the Agent ADB endpoint authenticates against.
+func newADBForwardTestTask(t *testing.T, h *coreMCPHarness, groups []domain.HardwareGroup, batchID string) (domain.Task, string) {
+	t.Helper()
+	task := createHardwareAPITask(t, h.database)
+	if err := h.database.ReplaceHardwareGroups(h.ctx, task.ID, groups); err != nil {
+		t.Fatal(err)
+	}
+	turn := createCoreTurn(t, h.database, task, "main", batchID)
+	return task, h.issue(task.ID, "main", turn.ID)
+}
+
+// newADBForwardTestServer wires the Agent ADB executors to a fake adb that
+// reports success unless the joined argv has an explicit result. Counted calls
+// let each assertion tell "policy refused before execution" from "adb ran".
+func newADBForwardTestServer(t *testing.T, h *coreMCPHarness, executions *atomic.Int32, results map[string]hardware.ADBExecResult) *httptest.Server {
+	t.Helper()
+	executor := func(_ context.Context, _ string, args []string, _ []byte, _ time.Duration) (hardware.ADBExecResult, error) {
+		executions.Add(1)
+		if result, ok := results[strings.Join(args, " ")]; ok {
+			return result, nil
+		}
+		return hardware.ADBExecResult{Stdout: []byte("ok")}, nil
+	}
+	server := httptest.NewServer(New(Config{
+		Store: h.database, App: h.service, AgentCapabilities: h.caps,
+		ExecuteADB: executor, ExecuteADBTCP: executor, ExecuteADBUSB: executor,
+	}).Handler())
+	t.Cleanup(server.Close)
+	return server
+}
+
+// TestADBForwardScopeSeparatesTaskGroupAndTarget pins the registry key itself.
+// Each component is a separate isolation boundary, so a key that silently
+// drops one of them must make this test fail rather than merely weaken a
+// behavior test elsewhere.
+func TestADBForwardScopeSeparatesTaskGroupAndTarget(t *testing.T) {
+	base := adbForwardScope("task-a", "usb", "SERIAL-A")
+	for _, tc := range []struct {
+		name  string
+		scope string
+	}{
+		{"task", adbForwardScope("task-b", "usb", "SERIAL-A")},
+		{"group", adbForwardScope("task-a", "usb-copy", "SERIAL-A")},
+		{"target", adbForwardScope("task-a", "usb", "SERIAL-B")},
+	} {
+		if tc.scope == base {
+			t.Fatalf("scope for a different %s equals the base scope %q", tc.name, base)
+		}
+	}
+}
+
+// TestADBForwardRegistryAddTransfersOwnership covers the rebind rule at the
+// registry boundary: one host-wide listener table keyed by local name, so a
+// successful add by another scope must take that local name away. The
+// unrelated local name proves the transfer is not a wholesale clear.
+func TestADBForwardRegistryAddTransfersOwnership(t *testing.T) {
+	registry := newADBForwardRegistry()
+	scopeA := adbForwardScope("task-a", "usb", "SERIAL-A")
+	scopeB := adbForwardScope("task-b", "usb", "SERIAL-B")
+
+	registry.add(scopeA, "tcp:8080")
+	registry.add(scopeA, "tcp:8081")
+	registry.add(scopeB, "tcp:8080")
+
+	if registry.allows(scopeA, "tcp:8080") {
+		t.Fatal("the previous owner can still remove a local name that was rebound")
+	}
+	if !registry.allows(scopeB, "tcp:8080") {
+		t.Fatal("the binding that just added the local name cannot remove it")
+	}
+	if !registry.allows(scopeA, "tcp:8081") {
+		t.Fatal("an unrelated local name was dropped by another scope's add")
+	}
+
+	registry.remove(scopeB, "tcp:8080")
+	if registry.allows(scopeB, "tcp:8080") {
+		t.Fatal("a successful remove left its own record in place")
+	}
+}
+
+// TestAgentADBForwardRebindInvalidatesPreviousOwner is the S2 regression: Task
+// A records tcp:22100, Task B plain-adds the same local name (AOSP rebinds the
+// listener to B), and A's later remove must not reach adb. Both transports are
+// exercised because they share agentADBExec but have separate error mapping.
+func TestAgentADBForwardRebindInvalidatesPreviousOwner(t *testing.T) {
+	h := newCoreMCPHarness(t)
+	var executions atomic.Int32
+	server := newADBForwardTestServer(t, h, &executions, nil)
+
+	// Same group ID, connection and target; only the Task differs, which is
+	// exactly the cross-Task collision from the review.
+	_, tokenA := newADBForwardTestTask(t, h, []domain.HardwareGroup{adbForwardTestUSBGroup("usb", "SHARED-SERIAL")}, "batch-forward-rebind-a")
+	_, tokenB := newADBForwardTestTask(t, h, []domain.HardwareGroup{adbForwardTestUSBGroup("usb", "SHARED-SERIAL")}, "batch-forward-rebind-b")
+	groupA, groupB := adbForwardTestUSBGroup("usb", "SHARED-SERIAL"), adbForwardTestUSBGroup("usb", "SHARED-SERIAL")
+
+	for _, mcp := range []bool{false, true} {
+		name, basePort := "http", 22100
+		if mcp {
+			name, basePort = "mcp", 22110
+		}
+		t.Run(name, func(t *testing.T) {
+			local := fmt.Sprintf("tcp:%d", basePort)
+			other := fmt.Sprintf("tcp:%d", basePort+1)
+			// A creates the listener plus an unrelated one of its own.
+			expectAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", local, "tcp:9000"}, mcp, &executions, true, "")
+			expectAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", other, "tcp:9001"}, mcp, &executions, true, "")
+			// B plain-adds the same local name. adb reports success after
+			// rebinding the existing listener to B's transport.
+			expectAgentADBExec(t, server.URL, tokenB, groupB, []string{"forward", local, "tcp:9000"}, mcp, &executions, true, "")
+			// A's record is stale: allowing this remove would delete B's
+			// listener, so it must be refused before the executor runs.
+			expectAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", "--remove", local}, mcp, &executions, false, "adb_forward_remove_not_allowed")
+			// B owns the rebound listener and can still clean it up.
+			expectAgentADBExec(t, server.URL, tokenB, groupB, []string{"forward", "--remove", local}, mcp, &executions, true, "")
+			// Invalidation is per local name: A's other listener is untouched.
+			expectAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", "--remove", other}, mcp, &executions, true, "")
+		})
+	}
+}
+
+// TestAgentADBForwardMutationsAreSerialized closes the authorize-then-execute
+// race: A's remove passes the ownership check and enters the executor, then B
+// plain-adds the same local name. Without serialization B's add would rebind
+// and invalidate A's record while A's already-authorized remove was still in
+// flight, so the remove would delete B's listener. The blocking executor makes
+// that interleaving observable: B's add must not reach the executor until A's
+// remove has returned.
+func TestAgentADBForwardMutationsAreSerialized(t *testing.T) {
+	h := newCoreMCPHarness(t)
+	_, tokenA := newADBForwardTestTask(t, h, []domain.HardwareGroup{adbForwardTestUSBGroup("usb", "SERIAL-A")}, "batch-forward-serialized-a")
+	_, tokenB := newADBForwardTestTask(t, h, []domain.HardwareGroup{adbForwardTestUSBGroup("usb", "SERIAL-B")}, "batch-forward-serialized-b")
+	groupA, groupB := adbForwardTestUSBGroup("usb", "SERIAL-A"), adbForwardTestUSBGroup("usb", "SERIAL-B")
+
+	var removeStateMu sync.Mutex
+	firstRemove := true
+	removeStarted := make(chan struct{})
+	releaseRemove := make(chan struct{})
+	calls := make(chan []string, 8)
+	executor := func(_ context.Context, _ string, args []string, _ []byte, _ time.Duration) (hardware.ADBExecResult, error) {
+		calls <- append([]string(nil), args...)
+		if len(args) == 3 && args[0] == "forward" && args[1] == "--remove" {
+			removeStateMu.Lock()
+			block := firstRemove
+			firstRemove = false
+			removeStateMu.Unlock()
+			if block {
+				close(removeStarted)
+				<-releaseRemove
+			}
+		}
+		return hardware.ADBExecResult{Stdout: []byte("ok")}, nil
+	}
+	server := httptest.NewServer(New(Config{
+		Store: h.database, App: h.service, AgentCapabilities: h.caps,
+		ExecuteADB: executor, ExecuteADBTCP: executor, ExecuteADBUSB: executor,
+	}).Handler())
+	t.Cleanup(server.Close)
+
+	local := "tcp:22130"
+	if outcome := callAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", local, "tcp:9000"}, false); !outcome.allowed {
+		t.Fatalf("setup add was refused: %+v", outcome)
+	}
+	<-calls // drain the setup add call
+
+	removeDone := make(chan adbExecOutcome, 1)
+	go func() {
+		removeDone <- callAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", "--remove", local}, false)
+	}()
+	select {
+	case <-removeStarted:
+	case <-time.After(5 * time.Second):
+		t.Fatal("the authorized remove never reached the executor")
+	}
+	<-calls // drain the remove call
+
+	addDone := make(chan adbExecOutcome, 1)
+	go func() {
+		addDone <- callAgentADBExec(t, server.URL, tokenB, groupB, []string{"forward", local, "tcp:9000"}, false)
+	}()
+	select {
+	case interleaved := <-calls:
+		close(releaseRemove)
+		t.Fatalf("the rebinding add ran while the authorized remove was still in flight: %v", interleaved)
+	case <-time.After(500 * time.Millisecond):
+	}
+	close(releaseRemove)
+	select {
+	case <-calls: // B's add, now serialized after A's remove
+	case <-time.After(5 * time.Second):
+		t.Fatal("the rebinding add never reached the executor after the remove finished")
+	}
+	if outcome := <-removeDone; !outcome.allowed {
+		t.Fatalf("the owning remove was refused: %+v", outcome)
+	}
+	if outcome := <-addDone; !outcome.allowed {
+		t.Fatalf("the rebinding add was refused: %+v", outcome)
+	}
+
+	// B owns the name after the serialized sequence; A's record is gone.
+	if outcome := callAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", "--remove", local}, false); outcome.allowed || outcome.code != "adb_forward_remove_not_allowed" {
+		t.Fatalf("stale owner remove outcome=%+v, want adb_forward_remove_not_allowed", outcome)
+	}
+	if outcome := callAgentADBExec(t, server.URL, tokenB, groupB, []string{"forward", "--remove", local}, false); !outcome.allowed {
+		t.Fatalf("rebinding owner remove was refused: %+v", outcome)
+	}
+}
+
+// TestAgentADBForwardBindingIsolation covers the other half of "records must
+// not cross": two bindings on one Task with different serials, and the same
+// binding re-pointed at another serial (the configured target is part of the
+// scope). A key that drops target or group would let these removes through.
+func TestAgentADBForwardBindingIsolation(t *testing.T) {
+	h := newCoreMCPHarness(t)
+	var executions atomic.Int32
+	server := newADBForwardTestServer(t, h, &executions, nil)
+
+	task, token := newADBForwardTestTask(t, h, []domain.HardwareGroup{
+		adbForwardTestUSBGroup("usb-a", "SERIAL-A"),
+		adbForwardTestUSBGroup("usb-b", "SERIAL-B"),
+	}, "batch-forward-binding-isolation")
+	groupA, groupB := adbForwardTestUSBGroup("usb-a", "SERIAL-A"), adbForwardTestUSBGroup("usb-b", "SERIAL-B")
+
+	// A local name recorded through one serial is not removable through a
+	// sibling binding on the same Task.
+	expectAgentADBExec(t, server.URL, token, groupA, []string{"forward", "tcp:22120", "tcp:8080"}, false, &executions, true, "")
+	expectAgentADBExec(t, server.URL, token, groupB, []string{"forward", "--remove", "tcp:22120"}, false, &executions, false, "adb_forward_remove_not_allowed")
+	expectAgentADBExec(t, server.URL, token, groupA, []string{"forward", "--remove", "tcp:22120"}, false, &executions, true, "")
+
+	// Re-point the same binding at another serial: the old record was made
+	// against SERIAL-A and must not authorize a remove through SERIAL-C.
+	expectAgentADBExec(t, server.URL, token, groupA, []string{"forward", "tcp:22121", "tcp:8080"}, false, &executions, true, "")
+	repointed := adbForwardTestUSBGroup("usb-a", "SERIAL-C")
+	if err := h.database.ReplaceHardwareGroups(h.ctx, task.ID, []domain.HardwareGroup{repointed}); err != nil {
+		t.Fatal(err)
+	}
+	expectAgentADBExec(t, server.URL, token, repointed, []string{"forward", "--remove", "tcp:22121"}, false, &executions, false, "adb_forward_remove_not_allowed")
+	// The new target owns its own names normally.
+	expectAgentADBExec(t, server.URL, token, repointed, []string{"forward", "tcp:22122", "tcp:8080"}, false, &executions, true, "")
+	expectAgentADBExec(t, server.URL, token, repointed, []string{"forward", "--remove", "tcp:22122"}, false, &executions, true, "")
+}
diff --git a/internal/httpapi/agent_handlers.go b/internal/httpapi/agent_handlers.go
index 50e52de..5cc0d16 100644
--- a/internal/httpapi/agent_handlers.go
+++ b/internal/httpapi/agent_handlers.go
@@ -290,6 +290,14 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
 			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_forward_remove_all_not_allowed", "message": "forward --remove-all 会清除同一 ADB server 上所有设备的转发，禁止使用"})
 			return
 		}
+		// Serialize forward mutations from authorization through the registry
+		// update: a concurrent add can rebind (and invalidate) the same local
+		// name, so a remove that passed the ownership check must not execute
+		// after such an add interleaves.
+		if action == adbForwardAdd || action == adbForwardRemove {
+			s.adbForwardMu.Lock()
+			defer s.adbForwardMu.Unlock()
+		}
 		if action == adbForwardRemove && !s.adbForwards.allows(forwardScope, local) {
 			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_forward_remove_not_allowed", "message": "只能删除本 Task 通过该硬件资源创建的转发端口"})
 			return
@@ -365,7 +373,9 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
 	}
 	// A forward only enters or leaves the registry when adb reported success:
 	// a failed add created nothing to remove, and a failed remove leaves the
-	// record in place (fail closed). tcp:0 is stored under the port adb picked,
+	// record in place (fail closed). A successful add also invalidates the same
+	// local name under every other binding, because adb may have rebound that
+	// listener to this transport. tcp:0 is stored under the port adb picked,
 	// because the literal tcp:0 cannot be removed later.
 	if result.ExitCode == 0 {
 		switch forwardAction {
diff --git a/internal/httpapi/server.go b/internal/httpapi/server.go
index 9b0f986..4e64fdb 100644
--- a/internal/httpapi/server.go
+++ b/internal/httpapi/server.go
@@ -129,6 +129,7 @@ type Server struct {
 	executeADBTCP            func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
 	listADBUSBDevices        func(context.Context) (hardware.ADBUSBDeviceList, error)
 	adbForwards              *adbForwardRegistry
+	adbForwardMu             sync.Mutex // serializes forward add/remove from authorization through the registry update
 	version                  string
 	webVersion               string
 	startedAt                time.Time
~~~~
