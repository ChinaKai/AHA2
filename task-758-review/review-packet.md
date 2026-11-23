# task-758：AHA2 未手动配置时默认关闭出站代理

## 结论

已完成实现并提交，等待 L2 独立评审。核心改动只有三处行为：

1. 后端省略 `mode` 时归一化为 `off`（直连），不再归一化为 `external`。
2. 只允许显式 `external` 注入 `HTTP_PROXY` / `HTTPS_PROXY`、使用保存的代理地址；`off`、空模式、`managed_hysteria2` 都直连。
3. 追加数据迁移 v157：只把“可证明仍是 schema v18 初始种子”的 `proxy_settings` 行改成 `off`；任何有用户写入痕迹或证据不足的行原样保留。

Web 初始状态、渲染回退和表单回退同步改为 `off`。显式 `external`、`off`、`managed_hysteria2` 以及地址、`NO_PROXY`、受管订阅能力均保持原样。

建议 L2 重点看：v157 的种子判定是否足够保守、`updated_at` 证据链是否接受、以及“保留历史地址但模式为 off”的产品取舍。

## 审查对象

- 基线（base）：`bd54b6b1f0e8b6e78e98eb70d27ef9604bf03b7e`（AHA2-BUILDER-SERVER main）。
- 产品提交（code head）：`1c00af559e15a4272c78edee658125233ffc3ffc`。
- 分支：`aha/task_cfcdfef9780fb26c640e`。
- Worktree：`/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_cfcdfef9780fb26c640e`。
- 本评审包由后续独立 docs 提交交付，不包含在产品提交中。最终 delivery head 见最终答复。
- 未 push、未 merge、未部署，未修改生产代理设置，未操作真实网络代理或正在运行的实例。

复现评审范围：

```bash
git diff bd54b6b1f0e8b6e78e98eb70d27ef9604bf03b7e 1c00af559e15a4272c78edee658125233ffc3ffc
```

下面“完整 diff”内嵌同一范围的 `git diff` 全文。

## 生产变更

### 1. `internal/proxyconfig/proxy.go`

- `Normalize`：空 `mode` 从 `"external"` 改为 `"off"`。这是所有 HTTP 请求、Turn 环境、Codex 账号路径共享的兼容入口，旧调用方省略 `mode` 时不再意外启用代理。
- `ApplyEnvironment`：只有 `Mode == "external"` 才写入 `HTTP_PROXY` / `HTTPS_PROXY`；`off`、空模式、`managed_hysteria2` 一律清空这两个变量。`NO_PROXY` 仍按原逻辑写入/删除。
- `proxyFunc`：只有 `Mode == "external"` 才读取保存的地址并返回代理；`off` 即使数据库里还留着历史 `127.0.0.1:7897` 也返回 `nil`（直连）。这覆盖了 `codexaccount` 等直接调用 `proxyconfig.Client` 的调用方，避免它们绕过 `ApplyEnvironment` 继续使用旧地址。
- 受管代理路径不变：`outboundproxy.Controller.ApplyEnvironment` 在 `managed_hysteria2` 下仍把本地 bridge 以 `Mode: "external"` 传入，因此受管代理仍会正确注入 bridge 地址。

### 2. `internal/store/migrations.go`：追加 schema v157

v157 不改表、不加列，只做一次数据判定并记录版本。判定 `proxy_settings` 行是否是“从未手动配置”的 v18 种子：

- `mode == "external"`；
- `http_proxy == https_proxy == "http://127.0.0.1:7897"`；
- `no_proxy == "localhost,127.0.0.1,::1"`；
- `managed_profile_id`、`managed_node_id`、`managed_subscription_at` 为空，`managed_refresh_interval_minutes == 1440`；
- `updated_at` 与 `schema_migrations` 中 v18 的 `applied_at` 都可解析，且 `updated_at <= applied_at`，两者相差不超过 1 分钟。

全部满足才执行：

```sql
UPDATE proxy_settings SET mode='off' WHERE id=1 AND mode='external';
```

不满足任何一条即保留原行。迁移本身只记录版本，不修改 `updated_at`；因此即使 v157 被重跑，判定结果也不变，且不会覆盖之后保存的用户设置。

### 3. Web

- `web/src/main.ts`：首屏状态 `proxySettings.mode` 从 `external` 改为 `off`。
- `web/src/proxy_settings.ts`：`renderProxySettings` 和表单读取的缺省值从 `external` 改为 `off`。
- 历史地址仍保留在状态里（`http_proxy` / `https_proxy` 仍是 7897 预置值），但 `off` 下外部地址字段隐藏、服务端不注入代理。这样与新库服务端返回值（`mode=off` + 同样的历史地址）一致，也保留“以后切到 external 时地址已在”的原有便利。
- 占位符 `placeholder="http://127.0.0.1:7897"` 未改：它只在用户选择 `external` 时可见，属于提示而非生效默认值。

### 4. 文档

`docs/implementation-v1.md` 的“共享代理”段落改为：默认关闭、直连、不注入；只有显式选择 external 才使用保存地址（历史预置值 7897）。

## 迁移审计：可判定与不可判定的边界

### 持久化历史

- `proxy_settings` 由 schema v18 创建，并在同一次迁移中插入唯一一行：

  ```sql
  INSERT INTO proxy_settings(id,http_proxy,https_proxy,no_proxy,updated_at)
  VALUES(1,'http://127.0.0.1:7897','http://127.0.0.1:7897','localhost,127.0.0.1,::1',
         strftime('%Y-%m-%dT%H:%M:%fZ','now'));
  ```

  紧接着 `migrate` 用同样的 `strftime(...,'now')` 写入 `schema_migrations(18, applied_at)`。两条语句相邻，`updated_at <= applied_at` 只可能相差毫秒级。
- schema v53 给 `proxy_settings` 增加 `mode TEXT NOT NULL DEFAULT 'external'`，对已有行回填 `external`；v54 增加 `managed_profile_id`。这些 ALTER 不会改 `updated_at`。
- 我们核对了历史：`git log --all -S"127.0.0.1:7897" -- internal/store/migrations.go` 只有 v18 引入时一次；`-S"INSERT INTO proxy_settings"` 也只有 v18（另有一个 worktree 保留提交，内容相同）；没有任何 `UPDATE proxy_settings SET ...` 的历史迁移。
- 当前树的非测试写入点只有 `Store.UpdateProxySettings`。它从 v18 引入时起就遵守：`UpdatedAt` 为零则填 `time.Now().UTC()`，否则写调用方给的值；HTTP 保存、受管配置导入/激活/删除等调用方都会在写入前设置当前时间。

### 为什么时间戳可以当证据

任何一次普通保存（哪怕保存的值与旧默认完全相同）都会把 `updated_at` 推到 v18 `applied_at` 之后。因此：

- `updated_at <= applied_at(v18)` + 值完全等于种子 → 只能是 v18 插入的原始行，可以安全改成 `off`。
- `updated_at > applied_at(v18)` → 至少发生过一次写入，保留。
- 值不等于种子（自定义 external、off、managed、非默认 NO_PROXY/刷新间隔）→ 保留。
- `updated_at` 为空/不可解析、v18 `applied_at` 缺失/不可解析、或两者相差超过 1 分钟 → 证据不足，保留。

### 需要说明的两个“不推进时间戳”的写入

审计发现有两个写入路径会把已存在的时间戳原样写回：

1. `Controller.DeleteProfile` 删除一个非活动配置时，会无条件调用 `UpdateProxySettings`，但只有 `activeDeleted` 分支才设置 `UpdatedAt`。若原行仍是 v18 种子，这次写回内容与种子相同，时间戳不变；v157 会把它当作未配置并改为 `off`。这符合语义：用户只是添加又删除了一个未启用的配置，从未选择过全局代理模式。
2. `Controller.loadProfiles` 迁移旧版单配置 secret 时，会更新 `managed_profile_id` 但不推进 `updated_at`。这类行带有非空受管字段，v157 的值检查会直接保留，不会误判为种子。

除这两处外，没有发现自动更新代理元数据却不推进时间戳的路径。

### 不可判定边界

以下情况无法无损区分“从未配置”和“显式保存了旧默认值”，实现选择保留现有配置：

- 手动保存恰好与 v18 `applied_at` 落在同一时钟粒度内（理论上可能，实际上 Web UI 在启动迁移完成前不可用；现有 Linux/Windows 时钟精度下也基本不可能）。
- 外部工具直接改写了 `updated_at` 或 `schema_migrations.applied_at`，且把时间改到与种子同分钟内。超过 1 分钟的改写会被 1 分钟保护拒绝。
- 数据库从非 AHA2 持久化格式导入，但保留了 `proxy_settings` 行、丢失或伪造了 v18 迁移记账。此时若时间戳看起来像种子，会被判为种子；除此之外的情况都保留。

### 无法判定时的最小处理方案

- 保留原 `mode` 与全部字段，不做任何猜测性改写；这些实例升级后仍按原配置运行。
- 迁移记录 v157，后续打开不会再次评估，避免某次重跑把用户后来改的设置翻回去。
- 这些实例在 Web 上仍是原来的模式（external/off/managed），owner 可以一次点击改成 `off`；不需要数据库手术。
- 不新增审计表、不新增“是否配置过”列；如果以后需要彻底消除歧义，可在后续迁移中增加显式的 `source` / `configured_at` 列，从新版本开始记录来源。这次不做，避免扩大迁移面。

## 相关生命周期函数与调用方

后端：

- `proxyconfig.Normalize`：HTTP PUT/test、Turn 环境、Codex 账号、受管 controller 共用的归一化入口。
- `proxyconfig.ApplyEnvironment`：`internal/app/service.go`（Turn 环境）、`internal/app/assistant.go`（助手聊天）、`internal/outboundproxy/controller.go`（受管 bridge）调用。
- `proxyconfig.Client` / `proxyFunc`：`internal/codexaccount/manager.go`、`internal/codexaccount/usage.go`、`internal/httpapi/proxy_handlers.go` 的测试请求、`outboundproxy.ClientFor` 的 external 分支。
- `Store.ProxySettings` / `Store.UpdateProxySettings`：唯一代理设置读写入口。
- `internal/httpapi/proxy_handlers.go`：
  - `GET /api/v1/settings/proxy`：返回数据库当前值（新库为 `mode=off`）。
  - `PUT /api/v1/settings/proxy`：先 `Normalize`，再经 `outboundproxy.UpdateSettings`（存在时）或 store 保存。省略 mode 的旧请求现在保存为 `off`。
  - `POST /api/v1/settings/proxy/test`：off 时构造直连 client 测试。
  - 受管订阅导入/刷新/激活/删除：模式与地址语义未改。
- `internal/outboundproxy/controller.go`：
  - `ClientFor`：`off` 返回 direct client；`external` 走 `proxyconfig.Client`；`managed_hysteria2` 走受管 dialer。
  - `ServiceClient`：非受管一律 direct（保持原注释和原行为）。
  - `ApplyEnvironment`：非受管交给 `proxyconfig.ApplyEnvironment`；受管把 bridge 以 external 注入。
- `internal/app/service.go` / `assistant.go`：只有 Runtime Config Snapshot 的 `proxy_enabled` 为真才应用代理；off 下 `ApplyEnvironment` 清空 HTTP/HTTPS 代理变量。
- `internal/codexaccount/*`：`proxy_enabled` 为真时通过 controller 或 `proxyconfig.Client` 取 client；off 下直连。

前端：

- `web/src/main.ts` 初始 `state.proxySettings`。
- `web/src/proxy_settings.ts` 的 `renderProxySettings`、`formSettings`、保存/测试按钮。
- `web/tests/proxy_defaults.test.mjs` 验证空 mode 渲染为 off、explicit external 仍正常、首屏状态为 off。

## 测试证据

以下命令均在本 Task worktree 内执行；临时数据库都在 `t.TempDir()`，HTTP 端点都是本机 `httptest`，没有连接真实代理或运行中的 AHA2 实例。

### 1. 代理配置单元测试

```bash
go test ./internal/proxyconfig -count=1 -v
```

结果：`ok github.com/ChinaKai/AHA2/internal/proxyconfig 0.005s`，7 个测试全部 PASS，包括：

- `TestNormalizeDefaultsOmittedModeToOff`：空 mode → off；显式 off/external/managed 不变。
- `TestOffModeDoesNotInjectProxyEnvironment`：off 清空已存在的 `HTTP_PROXY`/`HTTPS_PROXY`，保留 `NO_PROXY`；external 正常注入。
- `TestOffModeClientConnectsDirectlyEvenWithStoredAddress`：本机 origin + 本机假代理，off 且保存了假代理地址时请求直达 origin，假代理命中 0 次。
- `TestExternalModeClientUsesStoredProxy`：external 时假代理命中 1 次。

### 2. 迁移定向测试

```bash
go test ./internal/store -run 'ProxyDefault|TestMigrationV8Backfills' -count=1 -v
```

结果：`ok github.com/ChinaKai/AHA2/internal/store 41.850s`，全部 PASS。覆盖：

- `TestProxyDefaultOffOnFreshDatabase`：全新库 `mode=off`，历史地址仍在；按 Turn 路径依次执行 `ProxySettings` → `Normalize` → `ApplyEnvironment`，确认即使环境里预置了旧 `HTTP_PROXY`/`HTTPS_PROXY` 也会被清空；显式改 external 后重开不被覆盖；重复 `migrate` 成功。
- `TestProxyDefaultOffMigrationCoversLegacyModeColumnPath`：把真实库退回 v52 形状（重建 v18 表、删除 v53/v54/v157 记账），重开时真实执行 v53 添加 `mode default 'external'`，再由 v157 改为 off；`updated_at` 保持 v18 时间。
- `TestProxyDefaultOffMigrationPreservesExplicitSettings`：显式保存旧默认 external、自定义 external、off、managed 四种情况全部逐字段保留（含 `updated_at`）。
- `TestProxyDefaultOffMigrationRerunKeepsLaterUserSave`：用户保存 external 后删除 v157 记账并重开，迁移重跑仍保留用户值。
- `TestProxyDefaultOffMigrationPreservesUnprovableRows`：空时间戳、时间戳远早于迁移记账、种子时间戳但值被改、v18 `applied_at` 不可读、种子时间戳但带旧受管元数据，五种不可判定行全部保留。
- `TestProxyDefaultOffMigrationPreservesRowWithoutSeedMarker`：v18 记账缺失时直接调用迁移函数，保留原行并记录 v157。

### 3. HTTP API

```bash
go test ./internal/httpapi -run 'TestProxySettingsAPI|TestManagedProxySubscriptionAPIKeepsSecretsOutOfResponses' -count=1 -v
```

结果：两个测试全部 PASS（`ok ... 6.706s`）：

- 新库 GET 返回 `mode=off` + 历史地址；
- 旧式 PUT（省略 mode）成功保存为 `mode=off`，地址更新照常；
- 显式 PUT `external` 正常保存；
- 显式 PUT `managed_hysteria2` 正常保存（无 controller 时直接落库）；
- 导入受管配置不会意外激活，模式仍为 off。

### 4. Codex 账号代理路径

```bash
go test ./internal/codexaccount -run TestOAuthTokenExchangeUsesConfiguredProxy -count=1 -v
```

结果：PASS（`ok ... 0.040s`）。测试 fixture 显式设置 `Mode: "external"`，证明显式代理仍照常使用。另一个默认 fixture 也改为显式 external，避免测试依赖旧默认。

### 5. Turn 环境路径

```bash
go test ./internal/app -run '^TestTaskMultiTurnFlow$' -count=1 -timeout 600s -v
```

结果：PASS（`ok ... 53.407s`）。该测试覆盖 Turn 环境注入；因为默认已改为 off，测试在创建 Task 前显式保存 external 7897，继续验证显式代理注入路径。

### 6. 受管代理 controller

```bash
go test ./internal/outboundproxy -count=1
```

结果：`ok github.com/ChinaKai/AHA2/internal/outboundproxy 0.020s`。

### 7. Web 构建与单元测试

```bash
node scripts/build-web.mjs
node --test tests/proxy_defaults.test.mjs
```

结果：构建成功；新测试 3/3 PASS：

- 空 mode 渲染为 off（`<option value="off" selected>`，external 字段隐藏）；
- 显式 external 仍选中并显示字段；
- `main.ts` 初始状态为 `{mode: "off"}`。

完整 Web 套件：

```bash
npm test
```

结果：641 个测试，639 PASS，2 FAIL。两个失败都在 `tests/model_desktop_layout.test.mjs`，读的是本任务未修改的 `web/src/styles.css`：测试用 `mediaBlocks(css, "min-width: 761px")` 取第一个匹配块，而 CSS 里第一个是 `@media (min-width: 761px) and (max-height: 799px)`（第 285 行），目标规则在后面的纯 `@media (min-width: 761px)`（第 1608 行）。`git diff --name-only bd54b6b -- web/src/styles.css` 为空，失败与本变更无关，是既有基线问题。本任务没有改动该测试或样式。

### 8. 全量受影响 Go 包

```bash
go test -p 1 -count=1 -timeout 40m ./internal/proxyconfig ./internal/store ./internal/codexaccount ./internal/httpapi ./internal/app
```

未完成。本机 cgroup CPU 配额较低，串行跑到 `internal/store` 时，测试 `TestCreateAgentTurnRejectsTerminalRound` 在其独立临时库的 SQLite 写入上等待超过 7 分钟没有进展；此前一次并行跑 5 个包也在 `go test` 默认 600s 超时中止于 `internal/app`。把该测试单独运行时 37.99s 通过，说明是整套并行测试在本机资源配额下的争用，而不是单个测试死锁。该环境现象没有在基线上专门复现，因此这里只把它记为缺口，不作为门禁证据；本次评审依据是上面的定向命令，它们覆盖了全部改动路径。

补充静态检查均通过：

```bash
go build ./...
go vet ./internal/proxyconfig ./internal/store ./internal/httpapi ./internal/codexaccount ./internal/app ./internal/outboundproxy
```

两条命令退出码均为 0。

## 缺口

- 没有连接任何真实代理、真实网络或运行中的 AHA2 实例；所有出站测试都是本机 `httptest`。
- 没有在真实历史数据库副本上运行 v157。迁移测试覆盖了 v52→v157 的真实列变更路径，以及 v18 种子的时间戳状态，但没有 owner 机器上的历史库样本。
- 没有 L2 独立评审（本任务要求后续进行）。
- 未 push、未 merge、未部署；未改生产代理设置。
- Web 完整套件有 2 个既有失败，原因见上。

## 完整 diff

以下为 `git diff bd54b6b1f0e8b6e78e98eb70d27ef9604bf03b7e 1c00af559e15a4272c78edee658125233ffc3ffc` 的完整输出。

```diff
diff --git a/docs/implementation-v1.md b/docs/implementation-v1.md
index fa5651f..c7bcfef 100644
--- a/docs/implementation-v1.md
+++ b/docs/implementation-v1.md
@@ -160,7 +160,7 @@ POST   /api/v1/knowledge/{id}/verify
 
 ## 共享代理
 
-- 全局代理设置只保存一份 `HTTP_PROXY`、`HTTPS_PROXY` 和 `NO_PROXY`，默认指向 `http://127.0.0.1:7897`。
+- 全局代理设置只保存一份 `HTTP_PROXY`、`HTTPS_PROXY` 和 `NO_PROXY`。默认模式为关闭：从未手动配置过代理的新库和旧库都直连，不注入 `HTTP_PROXY` / `HTTPS_PROXY`；只有明确选择“现有 HTTP / HTTPS 代理”时才使用保存的地址（历史预置值为 `http://127.0.0.1:7897`）。
 - Task/Agent 的 `proxy_enabled` 固化在不可变 Runtime Config Snapshot 中；开启时才向 Codex 或 Claude Backend 注入共享代理环境变量。
 - Codex Account 的 `proxy_enabled` 独立控制 OAuth Token 交换，并随账号保存，浏览器打开授权链接是否使用代理仍由浏览器或系统代理决定。
 - 一期仅接受不含凭据的 `http://`、`https://` 代理 URL，不解析订阅或实现代理协议。
diff --git a/internal/app/service_test.go b/internal/app/service_test.go
index 2df99a0..a167571 100644
--- a/internal/app/service_test.go
+++ b/internal/app/service_test.go
@@ -872,6 +872,14 @@ func TestTaskMultiTurnFlow(t *testing.T) {
 	service.SetKnowledgeVaultRuntimeWrite(true)
 	capabilities := agentapi.NewCapabilities()
 	service.SetAgentAPI(capabilities, "https://aha.example.test")
+	// This test covers environment injection, so it opts into the external
+	// proxy explicitly; the default is now off.
+	if _, err := database.UpdateProxySettings(ctx, domain.ProxySettings{
+		Mode: "external", HTTPProxy: "http://127.0.0.1:7897", HTTPSProxy: "http://127.0.0.1:7897",
+		NoProxy: "localhost,127.0.0.1,::1", ManagedRefreshIntervalMins: 1440, UpdatedAt: now,
+	}); err != nil {
+		t.Fatal(err)
+	}
 	task, err := service.CreateTask(ctx, CreateTaskInput{
 		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "test", Request: "run test",
 		ModelID: model.ID, ProxyEnabled: true, SkillIDs: []string{selectedSkillID},
diff --git a/internal/codexaccount/manager_test.go b/internal/codexaccount/manager_test.go
index b551a41..1aa88f8 100644
--- a/internal/codexaccount/manager_test.go
+++ b/internal/codexaccount/manager_test.go
@@ -50,7 +50,7 @@ func (f *fakeAccountStore) ProxySettings(context.Context) (domain.ProxySettings,
 		return f.proxy, nil
 	}
 	return domain.ProxySettings{
-		HTTPProxy: "http://127.0.0.1:7897", HTTPSProxy: "http://127.0.0.1:7897",
+		Mode: "external", HTTPProxy: "http://127.0.0.1:7897", HTTPSProxy: "http://127.0.0.1:7897",
 		NoProxy: "localhost,127.0.0.1,::1",
 	}, nil
 }
@@ -94,7 +94,7 @@ func TestOAuthTokenExchangeUsesConfiguredProxy(t *testing.T) {
 	defer proxyServer.Close()
 	store := &fakeAccountStore{
 		items: map[string]domain.CodexAccount{},
-		proxy: domain.ProxySettings{HTTPProxy: proxyServer.URL, HTTPSProxy: proxyServer.URL},
+		proxy: domain.ProxySettings{Mode: "external", HTTPProxy: proxyServer.URL, HTTPSProxy: proxyServer.URL},
 	}
 	manager := New(context.Background(), store, &fakeSecretStore{items: map[string]string{}}, "", "")
 	manager.tokenURL = "http://auth.openai.invalid/oauth/token"
diff --git a/internal/httpapi/server_test.go b/internal/httpapi/server_test.go
index 4095964..36bcf73 100644
--- a/internal/httpapi/server_test.go
+++ b/internal/httpapi/server_test.go
@@ -116,16 +116,40 @@ func TestProxySettingsAPI(t *testing.T) {
 	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/proxy", nil, "")
 	var payload map[string]any
 	decodeResponse(t, response, &payload)
-	if response.StatusCode != http.StatusOK || payload["proxy"].(map[string]any)["http_proxy"] != "http://127.0.0.1:7897" {
+	proxy := payload["proxy"].(map[string]any)
+	if response.StatusCode != http.StatusOK || proxy["mode"] != "off" || proxy["http_proxy"] != "http://127.0.0.1:7897" {
 		t.Fatalf("unexpected default proxy: status=%d payload=%v", response.StatusCode, payload)
 	}
+	// Older callers omitted mode. They must keep working, with the new safe
+	// default of a direct connection.
 	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/proxy", map[string]any{
 		"http_proxy": "http://192.168.1.2:7897", "https_proxy": "http://192.168.1.2:7897",
 		"no_proxy": "localhost,127.0.0.1",
 	}, csrf)
 	decodeResponse(t, response, &payload)
-	if response.StatusCode != http.StatusOK || payload["proxy"].(map[string]any)["https_proxy"] != "http://192.168.1.2:7897" {
-		t.Fatalf("proxy update failed: status=%d payload=%v", response.StatusCode, payload)
+	proxy = payload["proxy"].(map[string]any)
+	if response.StatusCode != http.StatusOK || proxy["mode"] != "off" || proxy["https_proxy"] != "http://192.168.1.2:7897" {
+		t.Fatalf("omitted-mode proxy update failed: status=%d payload=%v", response.StatusCode, payload)
+	}
+	// An explicit external or managed choice still takes effect unchanged.
+	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/proxy", map[string]any{
+		"mode": "external", "http_proxy": "http://198.51.100.5:3128",
+		"https_proxy": "http://198.51.100.5:3128", "no_proxy": "localhost",
+	}, csrf)
+	decodeResponse(t, response, &payload)
+	proxy = payload["proxy"].(map[string]any)
+	if response.StatusCode != http.StatusOK || proxy["mode"] != "external" ||
+		proxy["http_proxy"] != "http://198.51.100.5:3128" {
+		t.Fatalf("explicit external proxy update failed: status=%d payload=%v", response.StatusCode, payload)
+	}
+	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/proxy", map[string]any{
+		"mode": "managed_hysteria2", "managed_refresh_interval_minutes": 120,
+	}, csrf)
+	decodeResponse(t, response, &payload)
+	proxy = payload["proxy"].(map[string]any)
+	if response.StatusCode != http.StatusOK || proxy["mode"] != "managed_hysteria2" ||
+		proxy["managed_refresh_interval_minutes"] != float64(120) {
+		t.Fatalf("explicit managed proxy update failed: status=%d payload=%v", response.StatusCode, payload)
 	}
 }
 
@@ -196,7 +220,7 @@ func TestManagedProxySubscriptionAPIKeepsSecretsOutOfResponses(t *testing.T) {
 	}
 	profileID := profiles[0].(map[string]any)["id"].(string)
 	proxy := payload["proxy"].(map[string]any)
-	if proxy["mode"] != "external" || proxy["managed_profile_id"] != nil {
+	if proxy["mode"] != "off" || proxy["managed_profile_id"] != nil {
 		t.Fatalf("new profile unexpectedly became active: %#v", proxy)
 	}
 	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/proxy/profiles/"+profileID+"/activate", map[string]any{}, csrf)
diff --git a/internal/proxyconfig/proxy.go b/internal/proxyconfig/proxy.go
index 7aa3bfe..10b999c 100644
--- a/internal/proxyconfig/proxy.go
+++ b/internal/proxyconfig/proxy.go
@@ -14,7 +14,10 @@ import (
 func Normalize(input domain.ProxySettings) (domain.ProxySettings, error) {
 	input.Mode = strings.TrimSpace(input.Mode)
 	if input.Mode == "" {
-		input.Mode = "external"
+		// The historical default was "external" with a local 127.0.0.1:7897
+		// preset. An omitted mode now means the owner never asked for a proxy,
+		// so the safe default is a direct connection.
+		input.Mode = "off"
 	}
 	if input.Mode != "off" && input.Mode != "external" && input.Mode != "managed_hysteria2" {
 		return domain.ProxySettings{}, fmt.Errorf("unsupported proxy mode")
@@ -44,7 +47,10 @@ func Normalize(input domain.ProxySettings) (domain.ProxySettings, error) {
 }
 
 func ApplyEnvironment(environment map[string]string, settings domain.ProxySettings) {
-	if settings.Mode == "off" {
+	// Only an explicit external proxy may inject HTTP_PROXY/HTTPS_PROXY. Off,
+	// an omitted mode, and managed mode (which reaches this point through a
+	// loopback bridge with Mode "external") must never leak a stored address.
+	if settings.Mode != "external" {
 		settings.HTTPProxy = ""
 		settings.HTTPSProxy = ""
 	}
@@ -75,6 +81,12 @@ func Client(base *http.Client, settings domain.ProxySettings) (*http.Client, err
 
 func proxyFunc(settings domain.ProxySettings) func(*http.Request) (*url.URL, error) {
 	return func(request *http.Request) (*url.URL, error) {
+		// Off means direct even if a historical address is still stored. This
+		// keeps callers that build a client straight from the stored row (for
+		// example the Codex account path) from using the old 127.0.0.1 preset.
+		if settings.Mode != "external" {
+			return nil, nil
+		}
 		if bypassProxy(request.URL.Hostname(), request.URL.Port(), settings.NoProxy) {
 			return nil, nil
 		}
diff --git a/internal/proxyconfig/proxy_test.go b/internal/proxyconfig/proxy_test.go
index 5b4616f..c1b3093 100644
--- a/internal/proxyconfig/proxy_test.go
+++ b/internal/proxyconfig/proxy_test.go
@@ -1,7 +1,11 @@
 package proxyconfig
 
 import (
+	"io"
 	"net/http"
+	"net/http/httptest"
+	"strings"
+	"sync/atomic"
 	"testing"
 
 	"github.com/ChinaKai/AHA2/internal/domain"
@@ -9,6 +13,7 @@ import (
 
 func TestProxySelectionHonorsSchemeAndNoProxy(t *testing.T) {
 	settings := domain.ProxySettings{
+		Mode:      "external",
 		HTTPProxy: "http://127.0.0.1:7897", HTTPSProxy: "http://127.0.0.1:7898",
 		NoProxy: "localhost,.internal.example,10.0.0.0/8",
 	}
@@ -55,3 +60,107 @@ func TestNormalizeModesAndManagedDefaults(t *testing.T) {
 		t.Fatal("expected unsupported mode to fail")
 	}
 }
+
+func TestNormalizeDefaultsOmittedModeToOff(t *testing.T) {
+	settings, err := Normalize(domain.ProxySettings{
+		HTTPProxy: "http://127.0.0.1:7897", HTTPSProxy: "http://127.0.0.1:7897",
+	})
+	if err != nil {
+		t.Fatal(err)
+	}
+	if settings.Mode != "off" {
+		t.Fatalf("omitted mode normalized to %q, want off", settings.Mode)
+	}
+	for _, mode := range []string{"off", "external", "managed_hysteria2"} {
+		normalized, err := Normalize(domain.ProxySettings{Mode: mode})
+		if err != nil || normalized.Mode != mode {
+			t.Fatalf("explicit mode %q changed: %#v %v", mode, normalized, err)
+		}
+	}
+}
+
+func TestOffModeDoesNotInjectProxyEnvironment(t *testing.T) {
+	environment := map[string]string{
+		"HTTP_PROXY": "http://legacy.example:7897", "HTTPS_PROXY": "http://legacy.example:7897",
+		"NO_PROXY": "old.example", "KEEP": "1",
+	}
+	ApplyEnvironment(environment, domain.ProxySettings{
+		Mode: "off", HTTPProxy: "http://stored.example:7897", HTTPSProxy: "http://stored.example:7897",
+		NoProxy: "localhost,127.0.0.1,::1",
+	})
+	if _, ok := environment["HTTP_PROXY"]; ok {
+		t.Fatalf("off mode kept HTTP_PROXY: %#v", environment)
+	}
+	if _, ok := environment["HTTPS_PROXY"]; ok {
+		t.Fatalf("off mode kept HTTPS_PROXY: %#v", environment)
+	}
+	if environment["NO_PROXY"] != "localhost,127.0.0.1,::1" || environment["KEEP"] != "1" {
+		t.Fatalf("unexpected environment after off mode: %#v", environment)
+	}
+
+	external := map[string]string{}
+	ApplyEnvironment(external, domain.ProxySettings{
+		Mode: "external", HTTPProxy: "http://stored.example:7897", HTTPSProxy: "http://stored.example:8443",
+		NoProxy: "localhost",
+	})
+	if external["HTTP_PROXY"] != "http://stored.example:7897" || external["HTTPS_PROXY"] != "http://stored.example:8443" {
+		t.Fatalf("external mode did not inject its addresses: %#v", external)
+	}
+}
+
+func TestOffModeClientConnectsDirectlyEvenWithStoredAddress(t *testing.T) {
+	var proxyHits atomic.Int32
+	proxyServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
+		proxyHits.Add(1)
+		http.Error(writer, "proxy must not be used", http.StatusBadGateway)
+	}))
+	defer proxyServer.Close()
+	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
+		_, _ = io.WriteString(writer, "origin")
+	}))
+	defer origin.Close()
+
+	client, err := Client(nil, domain.ProxySettings{
+		Mode: "off", HTTPProxy: proxyServer.URL, HTTPSProxy: proxyServer.URL,
+	})
+	if err != nil {
+		t.Fatal(err)
+	}
+	response, err := client.Get(origin.URL)
+	if err != nil {
+		t.Fatal(err)
+	}
+	body, _ := io.ReadAll(response.Body)
+	response.Body.Close()
+	if string(body) != "origin" || proxyHits.Load() != 0 {
+		t.Fatalf("off mode did not go direct: body=%q proxy_hits=%d", strings.TrimSpace(string(body)), proxyHits.Load())
+	}
+}
+
+func TestExternalModeClientUsesStoredProxy(t *testing.T) {
+	var proxyHits atomic.Int32
+	proxyServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
+		proxyHits.Add(1)
+		if request.URL.Host != "origin.invalid" {
+			t.Errorf("proxy received %q, want origin.invalid", request.URL.String())
+		}
+		_, _ = io.WriteString(writer, "proxied")
+	}))
+	defer proxyServer.Close()
+
+	client, err := Client(nil, domain.ProxySettings{
+		Mode: "external", HTTPProxy: proxyServer.URL, HTTPSProxy: proxyServer.URL,
+	})
+	if err != nil {
+		t.Fatal(err)
+	}
+	response, err := client.Get("http://origin.invalid/resource")
+	if err != nil {
+		t.Fatal(err)
+	}
+	body, _ := io.ReadAll(response.Body)
+	response.Body.Close()
+	if string(body) != "proxied" || proxyHits.Load() != 1 {
+		t.Fatalf("external mode did not use its proxy: body=%q hits=%d", strings.TrimSpace(string(body)), proxyHits.Load())
+	}
+}
diff --git a/internal/store/migrations.go b/internal/store/migrations.go
index 729f8fe..e843f46 100644
--- a/internal/store/migrations.go
+++ b/internal/store/migrations.go
@@ -2,6 +2,8 @@ package store
 
 import (
 	"context"
+	"database/sql"
+	"errors"
 	"fmt"
 	"strings"
 	"time"
@@ -4321,9 +4323,116 @@ func (s *Store) migrate(ctx context.Context) error {
 		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(156, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
 		return fmt.Errorf("record schema v156: %w", err)
 	}
+	if err := s.migrateProxyDefaultOff(ctx); err != nil {
+		return err
+	}
+	return nil
+}
+
+// proxyDefaultOffVersion is the append-only migration that makes direct
+// connection the effective default. It deliberately only rewrites a row that
+// provably still holds the schema v18 seed.
+const proxyDefaultOffVersion = 157
+
+const (
+	legacyProxyAddress = "http://127.0.0.1:7897"
+	legacyProxyNoProxy = "localhost,127.0.0.1,::1"
+)
+
+// migrateProxyDefaultOff turns the historical external preset into an opt-in
+// setting. Schema v18 inserted proxy_settings and recorded its applied_at in
+// the same migration run; every later write, including a save of values equal
+// to the old default, goes through UpdateProxySettings and always advances
+// updated_at. A row whose updated_at is at or before v18's applied_at is
+// therefore still the untouched seed, not an owner choice.
+//
+// The migration is conservative by design: a missing migration marker, an
+// unreadable timestamp, a later timestamp, or values that differ from the seed
+// all mean "cannot prove it was never configured", so the row is preserved.
+// Because the version row is written even when the row is preserved, reopening
+// the database never re-evaluates (and never overwrites) a later user choice.
+func (s *Store) migrateProxyDefaultOff(ctx context.Context) error {
+	var recorded int
+	if err := s.db.QueryRowContext(ctx,
+		`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, proxyDefaultOffVersion).Scan(&recorded); err != nil {
+		return fmt.Errorf("read proxy default migration state: %w", err)
+	}
+	if recorded > 0 {
+		return nil
+	}
+	var mode, httpProxy, httpsProxy, noProxy string
+	var managedProfileID, managedNodeID, managedSubscriptionAt, updatedAt string
+	var refreshMinutes int
+	err := s.db.QueryRowContext(ctx, `
+		SELECT mode,http_proxy,https_proxy,no_proxy,managed_profile_id,managed_node_id,
+		       managed_refresh_interval_minutes,managed_subscription_at,updated_at
+		FROM proxy_settings WHERE id=1`).Scan(
+		&mode, &httpProxy, &httpsProxy, &noProxy, &managedProfileID, &managedNodeID,
+		&refreshMinutes, &managedSubscriptionAt, &updatedAt,
+	)
+	if err != nil && !errors.Is(err, sql.ErrNoRows) {
+		return fmt.Errorf("read proxy settings for default migration: %w", err)
+	}
+	if err == nil {
+		var appliedAt string
+		appliedErr := s.db.QueryRowContext(ctx,
+			`SELECT applied_at FROM schema_migrations WHERE version=18`).Scan(&appliedAt)
+		if appliedErr != nil && !errors.Is(appliedErr, sql.ErrNoRows) {
+			return fmt.Errorf("read proxy seed migration time: %w", appliedErr)
+		}
+		if appliedErr == nil && proxyRowIsMigrationSeed(
+			mode, httpProxy, httpsProxy, noProxy, managedProfileID, managedNodeID,
+			managedSubscriptionAt, updatedAt, appliedAt, refreshMinutes,
+		) {
+			// updated_at is intentionally left untouched: it records when the
+			// row was created, not this migration. A rerun therefore reaches
+			// the same conclusion and remains idempotent.
+			if _, err := s.db.ExecContext(ctx,
+				`UPDATE proxy_settings SET mode='off' WHERE id=1 AND mode='external'`); err != nil {
+				return fmt.Errorf("default proxy to off: %w", err)
+			}
+		}
+	}
+	if _, err := s.db.ExecContext(ctx,
+		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
+		proxyDefaultOffVersion,
+	); err != nil {
+		return fmt.Errorf("record schema v%d: %w", proxyDefaultOffVersion, err)
+	}
 	return nil
 }
 
+func proxyRowIsMigrationSeed(
+	mode, httpProxy, httpsProxy, noProxy, managedProfileID, managedNodeID,
+	managedSubscriptionAt, updatedAt, appliedAt string, refreshMinutes int,
+) bool {
+	if mode != "external" ||
+		httpProxy != legacyProxyAddress || httpsProxy != legacyProxyAddress ||
+		noProxy != legacyProxyNoProxy ||
+		managedProfileID != "" || managedNodeID != "" || managedSubscriptionAt != "" ||
+		refreshMinutes != 1440 {
+		return false
+	}
+	updated, err := time.Parse(time.RFC3339Nano, updatedAt)
+	if err != nil {
+		return false
+	}
+	applied, err := time.Parse(time.RFC3339Nano, appliedAt)
+	if err != nil {
+		return false
+	}
+	// v18 writes the seed row before it records applied_at, so a seed has
+	// updated_at <= applied_at. A save through UpdateProxySettings always
+	// moves updated_at forward, even when it stores the same old addresses.
+	if updated.After(applied) {
+		return false
+	}
+	// A seed is written by the migration immediately before applied_at. A
+	// larger gap means the timestamps were rewritten by something we cannot
+	// reason about, so keep the row.
+	return applied.Sub(updated) <= time.Minute
+}
+
 // migrateKnowledgeModelA adds the project knowledge-space state and the
 // channel-to-space bindings that replace per-entry channel grants. The column
 // check keeps the migration reentrant when a test or repair recreated the
diff --git a/internal/store/proxy_default_migration_test.go b/internal/store/proxy_default_migration_test.go
new file mode 100644
index 0000000..a8ff7ba
--- /dev/null
+++ b/internal/store/proxy_default_migration_test.go
@@ -0,0 +1,414 @@
+package store
+
+import (
+	"context"
+	"database/sql"
+	"path/filepath"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/proxyconfig"
+)
+
+const testLegacyProxyTime = "2025-01-02T03:04:05.678Z"
+
+func TestProxyDefaultOffOnFreshDatabase(t *testing.T) {
+	t.Parallel()
+	ctx := context.Background()
+	path := filepath.Join(t.TempDir(), "aha2.db")
+	database, err := Open(ctx, path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	settings, err := database.ProxySettings(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if settings.Mode != "off" {
+		t.Fatalf("fresh database proxy mode = %q, want off", settings.Mode)
+	}
+	if settings.HTTPProxy != legacyProxyAddress || settings.HTTPSProxy != legacyProxyAddress {
+		t.Fatalf("fresh database lost the historical address seed: %#v", settings)
+	}
+	// Follow the Turn path's composition: read the stored row, normalize it,
+	// then apply it to the environment. A fresh database must leave no
+	// HTTP_PROXY/HTTPS_PROXY behind even though the inert addresses remain.
+	normalized, err := proxyconfig.Normalize(settings)
+	if err != nil {
+		t.Fatal(err)
+	}
+	environment := map[string]string{
+		"HTTP_PROXY": "http://stale.example:7897", "HTTPS_PROXY": "http://stale.example:7897",
+	}
+	proxyconfig.ApplyEnvironment(environment, normalized)
+	if _, ok := environment["HTTP_PROXY"]; ok {
+		t.Fatalf("fresh database injected HTTP_PROXY: %#v", environment)
+	}
+	if _, ok := environment["HTTPS_PROXY"]; ok {
+		t.Fatalf("fresh database injected HTTPS_PROXY: %#v", environment)
+	}
+	assertProxyDefaultOffVersion(t, ctx, database)
+
+	// Reopening is a no-op: the version row prevents the migration from
+	// re-evaluating (and overwriting) whatever the owner set later.
+	if _, err := database.UpdateProxySettings(ctx, domain.ProxySettings{
+		Mode: "external", HTTPProxy: "http://192.0.2.10:3128", HTTPSProxy: "http://192.0.2.10:3128",
+		NoProxy: "localhost", ManagedRefreshIntervalMins: 1440, UpdatedAt: time.Now().UTC(),
+	}); err != nil {
+		t.Fatal(err)
+	}
+	if err := database.Close(); err != nil {
+		t.Fatal(err)
+	}
+	database, err = Open(ctx, path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	settings, err = database.ProxySettings(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if settings.Mode != "external" || settings.HTTPProxy != "http://192.0.2.10:3128" {
+		t.Fatalf("reopen overwrote an explicit proxy setting: %#v", settings)
+	}
+	if err := database.migrate(ctx); err != nil {
+		t.Fatalf("repeat migration failed: %v", err)
+	}
+}
+
+// TestProxyDefaultOffMigrationCoversLegacyModeColumnPath rewinds a real
+// database to the shape it had before schema v53 added proxy_settings.mode.
+// Opening it then exercises the actual historical path: v53 adds the mode
+// column with its 'external' default on the untouched v18 seed row, and v157
+// turns that seed off.
+func TestProxyDefaultOffMigrationCoversLegacyModeColumnPath(t *testing.T) {
+	t.Parallel()
+	ctx := context.Background()
+	path := filepath.Join(t.TempDir(), "aha2.db")
+	database, err := Open(ctx, path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if err := database.Close(); err != nil {
+		t.Fatal(err)
+	}
+
+	raw, err := sql.Open("sqlite", path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	for _, statement := range []string{
+		`DELETE FROM schema_migrations WHERE version IN (53,54,157)`,
+		`UPDATE schema_migrations SET applied_at='` + testLegacyProxyTime + `' WHERE version=18`,
+		`CREATE TABLE proxy_settings_legacy (
+			id INTEGER PRIMARY KEY CHECK (id = 1),
+			http_proxy TEXT NOT NULL DEFAULT '',
+			https_proxy TEXT NOT NULL DEFAULT '',
+			no_proxy TEXT NOT NULL DEFAULT '',
+			updated_at TEXT NOT NULL DEFAULT ''
+		)`,
+		`INSERT INTO proxy_settings_legacy(id,http_proxy,https_proxy,no_proxy,updated_at)
+		 VALUES(1,'` + legacyProxyAddress + `','` + legacyProxyAddress + `','` + legacyProxyNoProxy + `','` + testLegacyProxyTime + `')`,
+		`DROP TABLE proxy_settings`,
+		`ALTER TABLE proxy_settings_legacy RENAME TO proxy_settings`,
+	} {
+		if _, err := raw.ExecContext(ctx, statement); err != nil {
+			raw.Close()
+			t.Fatalf("prepare legacy database: %v", err)
+		}
+	}
+	if err := raw.Close(); err != nil {
+		t.Fatal(err)
+	}
+
+	database, err = Open(ctx, path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	settings, err := database.ProxySettings(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if settings.Mode != "off" {
+		t.Fatalf("legacy v18+v53 path proxy mode = %q, want off", settings.Mode)
+	}
+	if !settings.UpdatedAt.Equal(mustParseProxyTestTime(t, testLegacyProxyTime)) {
+		t.Fatalf("migration rewrote the seed timestamp: %v", settings.UpdatedAt)
+	}
+	assertProxyDefaultOffVersion(t, ctx, database)
+}
+
+func TestProxyDefaultOffMigrationPreservesExplicitSettings(t *testing.T) {
+	t.Parallel()
+	ctx := context.Background()
+	later := "2026-02-03T04:05:06.789123456Z"
+	cases := []struct {
+		name     string
+		settings domain.ProxySettings
+	}{
+		{
+			name: "explicit save of the old default",
+			settings: domain.ProxySettings{
+				Mode: "external", HTTPProxy: legacyProxyAddress, HTTPSProxy: legacyProxyAddress,
+				NoProxy: legacyProxyNoProxy, ManagedRefreshIntervalMins: 1440,
+			},
+		},
+		{
+			name: "custom external",
+			settings: domain.ProxySettings{
+				Mode: "external", HTTPProxy: "http://198.51.100.7:8080", HTTPSProxy: "https://proxy.example:8443",
+				NoProxy: "localhost,.corp.example", ManagedRefreshIntervalMins: 1440,
+			},
+		},
+		{
+			name: "off",
+			settings: domain.ProxySettings{
+				Mode: "off", HTTPProxy: "http://198.51.100.7:8080", HTTPSProxy: "http://198.51.100.7:8080",
+				NoProxy: "localhost", ManagedRefreshIntervalMins: 1440,
+			},
+		},
+		{
+			name: "managed",
+			settings: domain.ProxySettings{
+				Mode: "managed_hysteria2", HTTPProxy: legacyProxyAddress, HTTPSProxy: legacyProxyAddress,
+				NoProxy: legacyProxyNoProxy, ManagedProfileID: "profile-1", ManagedNodeID: "node-2",
+				ManagedRefreshIntervalMins: 120, ManagedSubscriptionAt: mustParseProxyTestTime(t, later),
+			},
+		},
+	}
+	for _, testCase := range cases {
+		t.Run(testCase.name, func(t *testing.T) {
+			database, path := prepareProxyUpgradeRow(t, ctx, testCase.settings, later)
+			if err := database.Close(); err != nil {
+				t.Fatal(err)
+			}
+			database, err := Open(ctx, path)
+			if err != nil {
+				t.Fatal(err)
+			}
+			defer database.Close()
+			stored, err := database.ProxySettings(ctx)
+			if err != nil {
+				t.Fatal(err)
+			}
+			if stored.Mode != testCase.settings.Mode ||
+				stored.HTTPProxy != testCase.settings.HTTPProxy ||
+				stored.HTTPSProxy != testCase.settings.HTTPSProxy ||
+				stored.NoProxy != testCase.settings.NoProxy ||
+				stored.ManagedProfileID != testCase.settings.ManagedProfileID ||
+				stored.ManagedNodeID != testCase.settings.ManagedNodeID ||
+				stored.ManagedRefreshIntervalMins != testCase.settings.ManagedRefreshIntervalMins ||
+				!stored.ManagedSubscriptionAt.Equal(testCase.settings.ManagedSubscriptionAt) ||
+				!stored.UpdatedAt.Equal(mustParseProxyTestTime(t, later)) {
+				t.Fatalf("migration changed an explicit setting:\n got %#v\nwant %#v", stored, testCase.settings)
+			}
+			assertProxyDefaultOffVersion(t, ctx, database)
+		})
+	}
+}
+
+func TestProxyDefaultOffMigrationRerunKeepsLaterUserSave(t *testing.T) {
+	t.Parallel()
+	ctx := context.Background()
+	path := filepath.Join(t.TempDir(), "aha2.db")
+	database, err := Open(ctx, path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if _, err := database.UpdateProxySettings(ctx, domain.ProxySettings{
+		Mode: "external", HTTPProxy: "http://203.0.113.9:8080", HTTPSProxy: "http://203.0.113.9:8080",
+		NoProxy: "localhost", ManagedRefreshIntervalMins: 1440,
+	}); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, proxyDefaultOffVersion); err != nil {
+		t.Fatal(err)
+	}
+	if err := database.Close(); err != nil {
+		t.Fatal(err)
+	}
+	database, err = Open(ctx, path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	settings, err := database.ProxySettings(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if settings.Mode != "external" || settings.HTTPProxy != "http://203.0.113.9:8080" {
+		t.Fatalf("rerun overwrote a later user save: %#v", settings)
+	}
+	assertProxyDefaultOffVersion(t, ctx, database)
+}
+
+func TestProxyDefaultOffMigrationPreservesUnprovableRows(t *testing.T) {
+	t.Parallel()
+	ctx := context.Background()
+	cases := []struct {
+		name   string
+		mutate func(t *testing.T, database *Store)
+	}{
+		{
+			name: "empty timestamp",
+			mutate: func(t *testing.T, database *Store) {
+				execProxyTestSQL(t, database, `UPDATE proxy_settings SET updated_at='' WHERE id=1`)
+			},
+		},
+		{
+			name: "timestamp far before migration record",
+			mutate: func(t *testing.T, database *Store) {
+				execProxyTestSQL(t, database,
+					`UPDATE proxy_settings SET updated_at='2024-12-31T00:00:00.000Z' WHERE id=1`)
+			},
+		},
+		{
+			name: "custom values under a seed timestamp",
+			mutate: func(t *testing.T, database *Store) {
+				execProxyTestSQL(t, database,
+					`UPDATE proxy_settings SET http_proxy='http://198.51.100.7:8080' WHERE id=1`)
+			},
+		},
+		{
+			name: "unreadable seed migration time",
+			mutate: func(t *testing.T, database *Store) {
+				execProxyTestSQL(t, database,
+					`UPDATE schema_migrations SET applied_at='' WHERE version=18`)
+			},
+		},
+		{
+			// The legacy managed-profile import path writes
+			// managed_profile_id without advancing updated_at. A non-empty
+			// managed field means the row is not the untouched seed.
+			name: "managed metadata under a seed timestamp",
+			mutate: func(t *testing.T, database *Store) {
+				execProxyTestSQL(t, database,
+					`UPDATE proxy_settings SET managed_profile_id='legacy-profile' WHERE id=1`)
+			},
+		},
+	}
+	for _, testCase := range cases {
+		t.Run(testCase.name, func(t *testing.T) {
+			path := filepath.Join(t.TempDir(), "aha2.db")
+			database, err := Open(ctx, path)
+			if err != nil {
+				t.Fatal(err)
+			}
+			if _, err := database.db.ExecContext(ctx,
+				`UPDATE proxy_settings SET mode='external' WHERE id=1`); err != nil {
+				t.Fatal(err)
+			}
+			testCase.mutate(t, database)
+			if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, proxyDefaultOffVersion); err != nil {
+				t.Fatal(err)
+			}
+			if err := database.Close(); err != nil {
+				t.Fatal(err)
+			}
+			database, err = Open(ctx, path)
+			if err != nil {
+				t.Fatal(err)
+			}
+			defer database.Close()
+			settings, err := database.ProxySettings(ctx)
+			if err != nil {
+				t.Fatal(err)
+			}
+			if settings.Mode != "external" {
+				t.Fatalf("unprovable row was rewritten to %q", settings.Mode)
+			}
+			assertProxyDefaultOffVersion(t, ctx, database)
+		})
+	}
+}
+
+func TestProxyDefaultOffMigrationPreservesRowWithoutSeedMarker(t *testing.T) {
+	t.Parallel()
+	ctx := context.Background()
+	path := filepath.Join(t.TempDir(), "aha2.db")
+	database, err := Open(ctx, path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	execProxyTestSQL(t, database,
+		`UPDATE proxy_settings SET mode='external',updated_at='`+testLegacyProxyTime+`' WHERE id=1`)
+	if _, err := database.db.ExecContext(ctx,
+		`DELETE FROM schema_migrations WHERE version IN (18,?)`, proxyDefaultOffVersion); err != nil {
+		t.Fatal(err)
+	}
+	if err := database.migrateProxyDefaultOff(ctx); err != nil {
+		t.Fatal(err)
+	}
+	settings, err := database.ProxySettings(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if settings.Mode != "external" {
+		t.Fatalf("row without a v18 marker was rewritten to %q", settings.Mode)
+	}
+	assertProxyDefaultOffVersion(t, ctx, database)
+}
+
+func prepareProxyUpgradeRow(
+	t *testing.T, ctx context.Context, settings domain.ProxySettings, updatedAt string,
+) (*Store, string) {
+	t.Helper()
+	path := filepath.Join(t.TempDir(), "aha2.db")
+	database, err := Open(ctx, path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if _, err := database.db.ExecContext(ctx, `
+		UPDATE proxy_settings
+		SET mode=?, http_proxy=?, https_proxy=?, no_proxy=?, managed_profile_id=?,
+		    managed_node_id=?, managed_refresh_interval_minutes=?, managed_subscription_at=?, updated_at=?
+		WHERE id=1`,
+		settings.Mode, settings.HTTPProxy, settings.HTTPSProxy, settings.NoProxy,
+		settings.ManagedProfileID, settings.ManagedNodeID, settings.ManagedRefreshIntervalMins,
+		timeString(settings.ManagedSubscriptionAt), updatedAt,
+	); err != nil {
+		t.Fatal(err)
+	}
+	// Put the seed record in the past so an explicit save can be strictly
+	// later than it, exactly like an upgraded installation.
+	if _, err := database.db.ExecContext(ctx,
+		`UPDATE schema_migrations SET applied_at=? WHERE version=18`, testLegacyProxyTime); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, proxyDefaultOffVersion); err != nil {
+		t.Fatal(err)
+	}
+	return database, path
+}
+
+func execProxyTestSQL(t *testing.T, database *Store, statement string) {
+	t.Helper()
+	if _, err := database.db.ExecContext(context.Background(), statement); err != nil {
+		t.Fatal(err)
+	}
+}
+
+func assertProxyDefaultOffVersion(t *testing.T, ctx context.Context, database *Store) {
+	t.Helper()
+	var count int
+	if err := database.db.QueryRowContext(ctx,
+		`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, proxyDefaultOffVersion).Scan(&count); err != nil {
+		t.Fatal(err)
+	}
+	if count != 1 {
+		t.Fatalf("schema v%d recorded %d times, want 1", proxyDefaultOffVersion, count)
+	}
+}
+
+func mustParseProxyTestTime(t *testing.T, value string) time.Time {
+	t.Helper()
+	parsed, err := time.Parse(time.RFC3339Nano, value)
+	if err != nil {
+		t.Fatal(err)
+	}
+	return parsed
+}
diff --git a/internal/store/store_test.go b/internal/store/store_test.go
index b7fc9b0..b0b26b8 100644
--- a/internal/store/store_test.go
+++ b/internal/store/store_test.go
@@ -111,7 +111,7 @@ func TestMigrationV8BackfillsRoundsAndConversation(t *testing.T) {
 		t.Fatalf("legacy runtime snapshot must preserve Codex defaults: %#v", snapshot)
 	}
 	proxy, err := database.ProxySettings(ctx)
-	if err != nil || proxy.Mode != "external" || proxy.HTTPProxy != "http://127.0.0.1:7897" || proxy.HTTPSProxy != proxy.HTTPProxy || proxy.NoProxy == "" || proxy.ManagedRefreshIntervalMins != 1440 {
+	if err != nil || proxy.Mode != "off" || proxy.HTTPProxy != "http://127.0.0.1:7897" || proxy.HTTPSProxy != proxy.HTTPProxy || proxy.NoProxy == "" || proxy.ManagedRefreshIntervalMins != 1440 {
 		t.Fatalf("proxy migration defaults are invalid: %#v %v", proxy, err)
 	}
 	account := domain.CodexAccount{
diff --git a/web/src/main.ts b/web/src/main.ts
index b72d5bc..452e041 100644
--- a/web/src/main.ts
+++ b/web/src/main.ts
@@ -675,7 +675,7 @@ const state: State = {
   notice: "",
   renderPending: false,
   system: {os: "windows", arch: "", wsl_available: false, wsl_distros: [], version: "dev", web_version: "", started_at: ""},
-  proxySettings: {mode: "external", http_proxy: "http://127.0.0.1:7897", https_proxy: "http://127.0.0.1:7897", no_proxy: "localhost,127.0.0.1,::1", managed_refresh_interval_minutes: 1440},
+  proxySettings: {mode: "off", http_proxy: "http://127.0.0.1:7897", https_proxy: "http://127.0.0.1:7897", no_proxy: "localhost,127.0.0.1,::1", managed_refresh_interval_minutes: 1440},
   managedProxy: {configured: false, profiles: [], url_configured: false, nodes: [], unsupported_count: 0, unsupported_types: [], status: "idle"},
   securitySettings: {validate_origin: true, access_scope: "lan", startup_override: false},
   networkSettings: {listen_address: "", startup_listen_address: "", restart_required: false},
diff --git a/web/src/proxy_settings.ts b/web/src/proxy_settings.ts
index e06127e..558bd46 100644
--- a/web/src/proxy_settings.ts
+++ b/web/src/proxy_settings.ts
@@ -26,7 +26,7 @@ function formatUpdatedAt(value?: string): string {
 }
 
 function formSettings(base: ProxySettings): ProxySettings {
-  const mode = document.querySelector<HTMLSelectElement>("#proxy-mode")?.value || "external";
+  const mode = document.querySelector<HTMLSelectElement>("#proxy-mode")?.value || "off";
   return {
     ...base,
     mode: mode as ProxySettings["mode"],
@@ -100,7 +100,7 @@ export function renderProxySettings(settings: ProxySettings, managed: ManagedPro
       <section class="panel proxy-settings-panel"><div class="empty">正在读取当前代理设置…</div></section>
     </section>`;
   }
-  const mode = settings.mode || "external";
+  const mode = settings.mode || "off";
   const profiles = managed.profiles || [];
   const activeProfile = profiles.find(profile => profile.active);
   const activeNode = activeProfile?.nodes.find(node => node.id === activeProfile.active_node_id);
diff --git a/web/tests/proxy_defaults.test.mjs b/web/tests/proxy_defaults.test.mjs
new file mode 100644
index 0000000..c4a5b5c
--- /dev/null
+++ b/web/tests/proxy_defaults.test.mjs
@@ -0,0 +1,49 @@
+import test from "node:test";
+import assert from "node:assert/strict";
+import {readFile} from "node:fs/promises";
+import {resolve} from "node:path";
+import {pathToFileURL} from "node:url";
+
+const root = resolve(import.meta.dirname, "..");
+const {renderProxySettings} = await import(pathToFileURL(resolve(root, "build", "proxy_settings.js")));
+
+const managed = {
+  configured: false,
+  profiles: [],
+  url_configured: false,
+  nodes: [],
+  unsupported_count: 0,
+  unsupported_types: [],
+  status: "idle",
+};
+
+function settings(overrides = {}) {
+  return {
+    mode: "off",
+    http_proxy: "http://127.0.0.1:7897",
+    https_proxy: "http://127.0.0.1:7897",
+    no_proxy: "localhost,127.0.0.1,::1",
+    managed_refresh_interval_minutes: 1440,
+    ...overrides,
+  };
+}
+
+test("an omitted proxy mode renders as off, matching the server default", () => {
+  const html = renderProxySettings(settings({mode: ""}), managed);
+  assert.match(html, /<option value="off" selected>关闭代理<\/option>/);
+  assert.doesNotMatch(html, /<option value="external" selected>/);
+  assert.match(html, /id="proxy-external-fields" class="proxy-mode-fields hidden"/);
+});
+
+test("an explicit external mode still renders its fields", () => {
+  const html = renderProxySettings(settings({mode: "external"}), managed);
+  assert.match(html, /<option value="external" selected>现有 HTTP \/ HTTPS 代理<\/option>/);
+  assert.doesNotMatch(html, /<option value="off" selected>/);
+  assert.match(html, /id="proxy-external-fields" class="proxy-mode-fields"/);
+});
+
+test("the initial web state starts on off before the first settings load", async () => {
+  const source = await readFile(resolve(root, "src", "main.ts"), "utf8");
+  assert.match(source, /proxySettings:\s*\{mode:\s*"off"/);
+  assert.doesNotMatch(source, /proxySettings:\s*\{mode:\s*"external"/);
+});
```
