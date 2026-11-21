# task-726 review packet：硬件 ADB 拆成 USB 直连 / 无线调试（配对码）/ 网络直连（IP:5555）

- base 43624a4（main）→ 最终实现提交 336d47b，分支 aha/task_bf0fb2bc637d58c78441
- 实现由两个提交组成：3890244 是功能实现；336d47b 补了接管相关测试，并在 build.test 里写明预算决定。42b2480 是上一版评审材料，已被本包取代。3890244 → 336d47b 之间生产代码没有任何变化（`git diff 3890244 336d47b` 去掉测试和文档后是 0 字节）
- worktree .aha2-worktrees/task_bf0fb2bc637d58c78441（没有 push，没有合并）
- 本包共 6 个文件，每个都不超过 65536 字节。完整 diff 按文件分在 5 个分卷里，分卷 sha256 见文末。各分卷内容都是 `git diff 43624a4 336d47b -- <文件>` 的原样输出（不含 docs/review 下的评审材料本身）。5 个分卷拼起来，和 `git diff 43624a4 336d47b -- . ':!docs/review'` 按文件逐字节一致。

## 需求来源
- owner 15:41:41 确认“USB 直连”，15:55:29 追加“传统 IP:5555 免配对这个也需要”。最终范围是三种 ADB 连接形式，都在硬件资源页原来的“协议”下拉里选择。
- 不做的事：不向设备自动下发 `adb tcpip 5555`，不代替设备授权，不迁移或改写真实库里的配置，不 push，不部署。

## 设计
1. **数据模型**：`network.protocol` 仍然是 `adb`，新增两个字段：`network.adb_connection`（`wireless` / `tcp` / `usb`）和 `network.adb_serial`（只给 USB 用）。没有新增协议值，所以现有的“是不是 ADB”判断（Agent exec 入口、凭据清除、登录状态）继续有效。
   - `adb_connection` 为空（v156 之前的行、旧客户端、旧同步对端）一律按 `wireless` 解释（`domain.NormalizedADB`）。已配对的手机不会被当成 tcp 或 USB，host、port、adb_paired_at 都不动。
   - 存储层 schema v156 只做 `ALTER TABLE hardware_resources ADD COLUMN adb_connection/adb_serial TEXT NOT NULL DEFAULT ''`，可重入，不重写已有行。测试会断言旧行库里存的值仍是 `''`，读出来是 wireless。
2. **校验**（`hardwareconfig.normalizeADBTarget`，HTTP 资源 CRUD、Task 级兼容 PUT、接管、宠物 MCP 共用）：
   - wireless：保留 host 和调试连接端口（不传时默认 5555，和原来一样），清掉 serial。
   - tcp：host 必填，端口默认 5555、可以改；清掉 serial。
   - usb：清掉 host 和 port（旧客户端可能还会带上），serial 必填。`domain.ValidADBUSBSerial` 规则：`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`，并拒绝 `emulator-*`、含 `._adb` 的名字和以 `._tcp` 结尾的 mDNS 名。规则里不允许 `:`，所以 `host:port`、`model:` / `usb:` 这类 `-s` 限定符和以 `-` 开头的值都进不来。
   - 其他协议会清掉这两个 ADB 字段；未知的 adb_connection 值直接拒绝。
3. **配对状态**（`hardwareconfig.ADBPairingStillValid`，HTTP 和宠物共用）：只有“改之前是 wireless、改之后也是 wireless、mode=network、host 不变”时才保留 adb_paired_at。切到 tcp、usb、其他协议或其他 host 都会清掉。配对接口对 usb 和 tcp 资源返回 400 `adb_pair_unavailable`，不会调用 PairADB。
4. **执行目标**（Agent `adb/exec` 和终端 `Manager.Connect` 都只从本 Task 已绑定资源取目标）：
   - usb → `ExecuteADBUSB(serial)` / `openADBUSB(serial)`：先用 `adb -s <serial> get-state` 查状态（device、unauthorized 等待 Owner 在设备上确认、offline、missing、ambiguous、no_permissions 各有提示），再执行 `adb -s <serial> …`。不调用 connect、pair、mdns，也不调用 kill-server。
   - tcp → `ExecuteADBTCP(host:port)` / `openADBTCP`：`connectADBDirect` 只用配置的 host:port 做 connect、get-state 后执行，不做 mDNS 查询，所以不会被换成别的端口或设备。遇到 unauthorized 只给提示，不重启 adb server。
   - wireless → 原来的 `ExecuteADB` / `openADB`，保留 mDNS 端口修正和遇到 unauthorized 时重启一次 adb server 的行为，没有改动。
   - 现有 `ExecuteADB` / `PairADB` / `openADB` 的签名都没变，另外加了 `ExecuteADBUSB`、`ExecuteADBTCP`、`ListADBUSBDevices` 这几个 Config 钩子和 Manager 的 `openADBUSB`、`openADBTCP`。这样 task-722 分支里新增的那些 `ExecuteADB` 测试桩，合并后照样能编译。
5. **USB 枚举**：新增 `POST /api/v1/hardware/adb/usb-devices`，只注册在 `withAuth`（Owner 会话 + 同源 + CSRF）下，不注册给 Agent 能力路由，宠物 MCP 也不开放。它只执行 `adb devices -l`；网络 transport（`host:port`）、mDNS 名和模拟器只计数（`other_count`），不列出来。重复 serial 的设备和不合规的 serial 都标成 `selectable:false`。设备字段当外部数据处理：去掉控制字符并截断长度；失败时只返回固定文案，不回显 adb 的原始输出。
6. **UI**：协议下拉里有 `ADB（无线调试 / 配对码）`、`ADB（网络直连 / IP:端口，免配对）`、`ADB（USB 直连）` 三项。前端用 `adb` / `adb_tcp` / `adb_usb` 区分，提交时换算成 `protocol=adb` + `adb_connection`。
   - USB：只显示设备下拉、刷新按钮和说明，隐藏地址、端口、配对说明和登录字段。下拉从不预先选中设备，必须手动选一台；编辑时已保存的 serial 即使当前没检测到，也会保留为一个选项。
   - 网络直连：显示地址和端口（默认 5555），有单独的说明。
   - 无线：端口标签改成“调试连接端口”，说明里写明配对端口和连接端口不是同一个。
   - 资源行：只有 wireless 显示配对按钮和“已配对/未配对”。Task 面板显示 `ADB（USB 直连） <serial>` 或 `ADB（网络直连） host:port`；USB 组没有 serial 时“连接”按钮不可用，也不会显示空的 `:0`。接管表单里 USB 组改成填写本机 serial。
7. **宠物 MCP**：`aha_create/update_hardware_resource` 的 schema 新增 `adb_connection`（enum）和 `adb_serial`，update 仍是“只改传入字段”。`pairing_code` 列为 Owner 专属字段，宠物传了会报 `hardware_credential_owner_only`，不会被静默丢掉。修改审计里有 `network.adb_connection` / `network.adb_serial` 两个字段名。
8. **接管和同步**：HardwareGroup 的 JSON 自带新字段。接管时如果远端是 USB，必须提供本机 serial（远端 serial 属于另一台机器）。没传 adb_connection 时沿用远端的连接形式，所以 USB、tcp、wireless 不会互相串。旧对端同步过来的数据没有这个字段，按 wireless 处理。
9. **Prompt**：USB 组显示“ADB USB 直连，设备序列号 X”，不再打印空的 host:port；tcp 和 wireless 在协议后面标出连接形式。

## 顺带修复的问题（都在本需求的链路上）
- `HardwareResource.Supports(network)` 原来只看 host。USB 资源没有 host，如果不改，编辑或删除 USB 资源时不会断开已有终端。现在两个 Supports 都按连接形式判断。由 store 测试发现。
- Task 级兼容 PUT 把 mode=both 拆成串口和网络两个资源时，原来也只看 host，USB 那一半会被丢掉。已修，有 store 测试。

## 命令与退出码（都在本 worktree 执行）
在提交 336d47b 上执行（本版新增或重跑）：
- `go vet ./internal/httpapi` → 0；`gofmt -l internal/httpapi/takeover_adb_test.go` → 没有输出
- `go test ./internal/httpapi -run 'TestTaskTakeoverKeepsADBConnectionForms|TestRemoteConfigurationIsReadOnlyUntilExplicitTakeover' -count=1 -v` → 0（两个都是 PASS）
- web：`node scripts/build-web.mjs` → 0；`npm test` → 0（619 通过，0 失败；包含首屏预算断言，实测 273,350 ≤ 273,408）
- 浏览器测试（scratch 里的 playwright 1.65.1 + chromium-headless-shell-1255，通过 AHA_PLAYWRIGHT_MODULE/AHA_BROWSER_EXECUTABLE 指定），都是 0：
  task_takeover_adb（新增）、hardware_resources、hardware_adb_panel、phase5_dialog_close_labels（20/20，axe-core 装在 scratch）、
  hardware_add_connect、hardware_copy（这两个测试写死了 .tools 路径，所以复制一份临时文件、只替换启动头再跑，跑完已删除）

在提交 3890244 上执行（336d47b 的生产代码和它完全一样，这些结果仍然适用）：
- `go build ./...` → 0
- `go vet ./internal/domain ./internal/hardwareconfig ./internal/hardware ./internal/store ./internal/prompt ./internal/httpapi ./internal/app ./internal/sync ./cmd/...` → 0
- `gofmt -l <改动的 .go 文件>` → 只列出 internal/httpapi/takeover_handlers.go；main 43624a4 上的同一文件本来就会被列出，不是本次引入
- `go test -race ./internal/hardware/ -count=1` → 0
- `go test ./internal/hardware ./internal/hardwareconfig ./internal/domain -count=1` → 0
- `go test ./internal/store ./internal/sync -count=1 -timeout 40m` → 0（store 1525.6 秒，sync 706.5 秒）。第一次用默认的 10 分钟超时，又和其他包并行跑，两个包都报 `panic: test timed out after 10m0s`，但没有任何 `--- FAIL`，所以单独加长超时重跑
- `go test ./internal/httpapi -run 'Hardware|ADB|Takeover|Remote|AgentHardware' -count=1 -timeout 30m` → 0（240.3 秒）
- `go test ./internal/app -run 'TestPetHardware|TestPetTakeover' -count=1 -v` → 0（11 个测试都是 PASS）
- `go test ./internal/prompt -count=1` → 0（全包，90.5 秒）
- `tsc --noEmit --strict`（typescript 5 装在 scratch，仓库没有 tsconfig）：HEAD 223 条，base 226 条。改动涉及的 5 个文件没有新增类型错误，反而少了 3 条 main 上原有的 `adb_paired_at` 错误

## 变异验证（在 scratch 里用 go test -overlay，源码没改）
- tcp 执行改回 `connectADB`（带 mDNS）→ TestExecuteADBTCPUsesExplicitEndpointWithoutMDNSOrPairing 和 TestExecuteADBTCPCustomPortAndUnauthorizedDoesNotRestartServer 都 FAIL。
- Manager 的 USB 分支改成 `openADB(endpoint)` → TestManagerRoutesEachADBConnectionFormToItsOpener FAIL。
- `HardwareResource.Supports` 修复前，store 测试 FAIL（实际踩到，不是人为构造的）。
- 宠物传 pairing_code 原来是静默丢弃 → 新测试 FAIL，所以加了拒绝。

## 覆盖
- hardware（假 adb 脚本，每次调用都记日志）：`adb devices -l` 解析（USB、unauthorized、带说明的 no permissions、IP:5555、mDNS、模拟器、重复 serial、Windows 下没有 usb: 属性）；USB 执行只有 `-s <serial>` 调用，没有 connect/pair/mdns/kill-server；missing、ambiguous、offline、unauthorized、no_permissions 五种状态的提示，以及这些状态下不会执行命令；授权等待成功；非法 serial 根本不会调用 adb；USB 终端；tcp 默认 5555、自定义 5601，mDNS 宣告了别的端口也不会被替换；tcp 遇到 unauthorized 不 kill-server；Manager 三种形式分别走对应 opener，状态里的 endpoint 正确，非法 USB 组不会连接。
- hardwareconfig：三种形式的归一化、默认值和互斥（旧形状、wireless 默认、tcp 默认和自定义端口、tcp 缺 host、usb 清掉 host 和 port、usb 缺 serial、9 种非法 serial、未知形式、ssh 清掉 ADB 字段）；配对保留和失效矩阵。
- store：v156 重开库可重入，旧行仍按 wireless 读出且 adb_paired_at 保留，tcp、usb、ssh 往返；mode=both 拆分时保留 USB。
- httpapi：同一个 Task 绑定 legacy、tcp 5555、tcp 5601、usb，Agent exec 各自走到对应执行器和目标；只读 USB 403、其他 Task 的 USB 404、devices/connect/`-s` 注入/pair/kill-server 都是 400，且都不会调用执行器，也不会调用 PairADB。Owner 侧：三种形式的创建（旧客户端不带字段时是 wireless）、非法 serial 400、usb 和 tcp 拒绝配对且不调用 PairADB、wireless 配对回归、同 host 编辑保留配对、切到 tcp 清掉配对、tcp 切 usb；枚举接口 Owner 能用、没有 CSRF 403、Agent token 401、adb 缺失时返回固定文案 409。原有的 TestHardwareADBPair… 和 TestAgentADBExecUses… 照常通过。
- 接管（本版补充）：`internal/httpapi/takeover_adb_test.go` 测的是 Web 接管接口。远端有 USB、tcp 5601、旧 wireless 三个组：不给本机 serial 返回 400 `local_hardware_fields_required`，提示里写了“USB 设备序列号”；serial 是 IP:端口形式返回 400；成功接管后 USB 用的是本机 serial，tcp 和 wireless 的连接形式和端口都继承下来。`web/tests/task_takeover_adb.browser.mjs` 测的是接管表单：USB 组只有一个必填的本机 serial 输入框，没有地址和端口；tcp 和 wireless 的隐藏字段分别带 tcp 和 wireless；没填 serial 时浏览器拦下提交；提交内容精确到每个字段。
- app（宠物）：三种形式的创建；缺 serial、IP 形 serial、tcp 缺 host、未知形式、pairing_code 都被拒绝；只改描述时 wireless 目标和配对都保留；切到 usb 必须给 serial，切过去后配对清掉；usb 切 tcp；审计里有字段名。接管：USB 不给本机 serial 被拒绝；三种形式各自继承，端口也继承。
- prompt：中英文模板里三种形式的写法，USB 没有空端点。
- Web：资源页浏览器测试（USB 字段可见性、不预选、重复 serial 不可选、没选设备时不能提交、提交内容精确、行里没有配对；tcp 默认 5555 和自定义 5601、切换 usb→ssh→tcp 没有残留；只有 wireless 行有配对按钮；编辑 USB 时 serial 回显，切回 wireless 后不再提交 serial）；新的 Task 面板浏览器测试；build.test 的协议标签断言。

## 缺口与风险
- **没有接真实设备**：USB 枚举、授权弹窗、`adb tcpip` 后的网络直连都只用假 adb 和 fixture 验证过，没有在 Windows 生产宿主机上实测。尤其是 Windows 下 `adb devices -l` 的实际输出（应该没有 `usb:` 属性，解析不依赖它）以及 get-state 的错误文案，需要 owner 用真实设备确认一次。
- schema 版本号用 v156。核对时 main 43624a4 最高是 v155；task-722/723 分支（aha/task_e3d0848747ddc2018605，tip cc25be4）也是最高 v155，没有 store 迁移改动；其他 aha/* 分支都没有领先 main 的 schema 改动。合并时的顺延规则：谁后合并谁顺延。如果 main 在本分支合并前已经有了 v156，就把本分支 migrations.go 末尾这段（两个 ADD COLUMN 加一行 INSERT schema_migrations）整体改成“main 当前最高版本 + 1”，同时改 `internal/store/hardware_adb_test.go` 里查 `version=156` 的断言。这段迁移按列名判断，可以重复执行，不依赖和其他版本的先后顺序，所以顺延只改版本号，不改逻辑。
- 首屏 gzip 预算：owner 侧分身已批准从 272,896 上调到 273,408（实测 273,350，比 main 的 272,829 多了 JS 480、CSS 41 字节），批准理由写在 build.test 的注释里。三段较长的新说明不做 en-US 翻译：owner 侧分身决定放到以后的“硬件页说明文案 i18n 统一清理”里一起做，不在本次评审范围。
- wireless 和 tcp 指向同一个 host:port 时共用同一个终端会话键（`network:adb:host:port`），这是有意的：同一台设备不能被两个读写会话同时占用。
- Agent exec 原有的黑名单（connect/pair/devices/kill-server 等）没有扩充。`tcpip`、`usb`、`reconnect` 这类设备级子命令仍然可以对已绑定的设备执行，行为和改动前一样。
- `internal/httpapi/takeover_handlers.go` 在 main 上本来就没有 gofmt，为了不扩大冲突面，这次没有格式化它。
- 与 task-722/723（未合并）的重叠：agent_handlers.go 里的 agentADBExec（我只改了选目标那一段，722 改的是输出截断，两处相邻）、server.go 的 Config 和路由、hardware_handlers.go（函数不同）、manager.go（字段和分支，722 新增的是 DisconnectTaskExcept）、pet.go 的 schema。722 的路由覆盖清单在合并后可能需要登记新的 Owner 路由。已经通过 AHA 进度消息告知宠物。

## 关键调用链（实现提交 336d47b；函数体在各 diff 分卷里，这里给链路和守卫）
1. Owner 创建/编辑：`POST|PUT /api/v1/hardware-resources[/{id}]`（withAuth）→ `resourceFromPayload` → `hardwareconfig.NormalizeGroup` → `normalizeADBTarget` → `ADBPairingStillValid` → ADB 资源清除凭据 → `store.UpsertHardwareResource`（写入前 `NormalizedADB`）→ 编辑时执行 `disconnectHardwareResource(existing)`（依赖修好的 `Supports`）。
2. 配对：`POST /api/v1/hardware-resources/{id}/adb/pair` → 检查 mode=network 且 protocol=adb → 检查 `!ADBUSB()`、`!ADBTCP()` → `pairADB(host:pairing_port, code)`；审计里只记连接端点。
3. USB 枚举：`POST /api/v1/hardware/adb/usb-devices`（withAuth）→ `listADBUSBDevices`（默认 `hardware.ListADBUSBDevices`）→ `adb devices -l` → `parseADBDevices`。
4. Agent exec：`POST /api/v1/agent/hardware/{hw}/adb/exec`（withAgentCapability）→ `bindAgentTask` → `hardwareTarget`（`store.HardwareGroup(taskID, hw)`，只查本 Task 的绑定）→ 要求 transport=network 且 protocol=adb → 只读返回 403 → 参数校验（第一个参数不能以 `-` 开头，命中主机级黑名单返回 400）→ `switch`：usb → `executeADBUSB(serial)`；tcp → `executeADBTCP(host:port)`；其他 → `executeADB(host:port)`。
5. 终端：`POST /api/v1/tasks/{id}/hardware/{hw}/connect` 或 Agent connect → `hardwareTarget` → `Manager.Connect` → `Supports` → `sessionTarget`（usb：key=`network:adb-usb:<serial>`，endpoint=`USB <serial>`；serial 不合法直接报错）→ 按顺序判断：openSerial / openSSH / ADBUSB→openADBUSB / ADBTCP→openADBTCP / openADB / 其余 TCP 直连。
6. 宠物：`aha_create|update_hardware_resource` → `decodePetHardwareResourceInput`（拒绝 password、credential_ref、clear_*、pairing_code，包括嵌套在 network 里的）→ `applyTo`（合并 adb_connection 和 adb_serial）→ `validatePetHardwareResourceInput`（usb 要求 adb_serial，其他要求 host）→ `NormalizeGroup` → `ADBPairingStillValid` → 资源有凭据时禁止修改连接目标（目标比较里加了 ADB 两个字段）→ Upsert → 断开旧连接。
7. 接管（HTTP `takeover_handlers.go` 和宠物 `pet_takeover.go` 两条路径对称）：远端是 USB 时要求本机 serial，除非显式改成 wireless；没传 adb_connection 时沿用远端的形式 → `NormalizeGroup`。
8. 读取：`store.HardwareResources`、`HardwareResource`、`HardwareGroups` 读出后都执行 `Network.NormalizedADB()`。

## 分卷
- `task-726-review-packet-diff-1-go-core.md`：38293 字节，sha256 `280eeff6558c251a8c50dec5ad722756f691b205c76bf9e7529593b87ddbe070`；7 个文件
- `task-726-review-packet-diff-2-go-api.md`：26808 字节，sha256 `c890f758e0542cecbe252655c8bd51fa8d2daae0b188820b3f81ed25bc727618`；10 个文件
- `task-726-review-packet-diff-3-web.md`：38175 字节，sha256 `92ebd4ebe5479a43bfbd289e1cce4b34adc12dfc26a294749fae5a265cd29142`；8 个文件
- `task-726-review-packet-diff-4-go-tests.md`：59659 字节，sha256 `521313cd2d7599887a922fc05ab47954e37ffad745363940c4dafd368745c4e9`；8 个文件
- `task-726-review-packet-diff-5-browser-tests.md`：33498 字节，sha256 `e7f96076766ed9996582eb2df6770559ba17b6a263e623372734c0b0586d93fe`；3 个文件
