# task-726 review packet：硬件 ADB 拆成 USB 直连 / 无线调试（配对码）/ 网络直连（IP:5555）

- base 43624a4（main）→ 实现提交 3890244，分支 aha/task_bf0fb2bc637d58c78441
- worktree .aha2-worktrees/task_bf0fb2bc637d58c78441（没有 push，没有合并）
- 本包共 6 个文件，每个都不超过 65536 字节。完整 diff 按文件分在 5 个分卷里，分卷 sha256 见文末。各分卷内容都是 `git diff 43624a4 3890244 -- <文件>` 的原样输出。

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
- `go build ./...` → 0
- `go vet ./internal/domain ./internal/hardwareconfig ./internal/hardware ./internal/store ./internal/prompt ./internal/httpapi ./internal/app ./internal/sync ./cmd/...` → 0
- `gofmt -l <改动的 .go 文件>` → 只列出 internal/httpapi/takeover_handlers.go，main 43624a4 上的同一文件本来就会被列出（不是本次引入）
- `go test -race ./internal/hardware/ -count=1` → 0
- `go test ./internal/hardware ./internal/hardwareconfig ./internal/domain -count=1` → 0
- `go test ./internal/store -run 'TestReplaceHardwareGroupsKeepsUSB|TestHardwareResourceADB|TestReplaceHardware' -count=1` → 0
- `go test ./internal/store ./internal/sync -count=1 -timeout 40m` → 0（store 1525.6 秒、sync 706.5 秒）。第一次用默认 10 分钟超时，和其他包并行跑：store 卡在 TestDeleteTaskCascadesMailGraph、sync 卡在 TestNewDeviceRecoversProjectKnowledge…，都是 `panic: test timed out after 10m0s`，没有 `--- FAIL`；单独加长超时重跑的结果见本行
- `go test ./internal/httpapi -run 'Hardware|ADB|Takeover|Remote|AgentHardware' -count=1 -timeout 30m`（在提交 3890244 上）→ 0（240.3 秒）
- `go test ./internal/app -run 'TestPetHardware|TestPetTakeover' -count=1 -v` → 0（11 个测试都是 PASS，包括 TestPetHardwareResourcesADBConnectionForms、TestPetTakeoverKeepsADBConnectionForms，以及原有的 UpdateClearsADBPairing、RejectTargetChangeWithOwnerCredential、ToolRegistrationAndSchema）
- `go test ./internal/prompt -count=1` → 0（全包，90.5 秒）
- web：`npm ci`；`node scripts/build-web.mjs` → 0；`npm test` → 0（619 通过，0 失败；首屏 gzip 实测 273,350，main 是 272,829）
- `tsc --noEmit --strict`（scratch 里装的 typescript 5，仓库没有 tsconfig）：HEAD 223 条，base 226 条。归一化行号后，改动涉及的 5 个文件没有新增错误，反而少了 3 条 main 上原有的 `adb_paired_at` 类型错误（给 HardwareResource 补上了这个字段）
- 浏览器测试（scratch 里的 playwright 1.65.1 + chromium-headless-shell-1255，通过 AHA_PLAYWRIGHT_MODULE/AHA_BROWSER_EXECUTABLE 指定）：
  hardware_resources（本次扩展，并加了环境变量覆盖口）→ 0；hardware_adb_panel（新增）→ 0；
  hardware_add_connect、hardware_copy（这两个测试写死了 .tools 路径，所以复制一份临时文件、只替换启动头再跑，跑完已删除）→ 0；
  phase5_dialog_close_labels（含接管对话框；用 scratch 里的 axe-core）→ 0（20/20）

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
- app（宠物）：三种形式的创建；缺 serial、IP 形 serial、tcp 缺 host、未知形式、pairing_code 都被拒绝；只改描述时 wireless 目标和配对都保留；切到 usb 必须给 serial，切过去后配对清掉；usb 切 tcp；审计里有字段名。接管：USB 不给本机 serial 被拒绝；三种形式各自继承，端口也继承。
- prompt：中英文模板里三种形式的写法，USB 没有空端点。
- Web：资源页浏览器测试（USB 字段可见性、不预选、重复 serial 不可选、没选设备时不能提交、提交内容精确、行里没有配对；tcp 默认 5555 和自定义 5601、切换 usb→ssh→tcp 没有残留；只有 wireless 行有配对按钮；编辑 USB 时 serial 回显，切回 wireless 后不再提交 serial）；新的 Task 面板浏览器测试；build.test 的协议标签断言。

## 缺口与风险
- **没有接真实设备**：USB 枚举、授权弹窗、`adb tcpip` 后的网络直连都只用假 adb 和 fixture 验证过，没有在 Windows 生产宿主机上实测。尤其是 Windows 下 `adb devices -l` 的实际输出（应该没有 `usb:` 属性，解析不依赖它）以及 get-state 的错误文案，需要 owner 用真实设备确认一次。
- 接管表单（main.ts）里 USB 组改为填写本机 serial 的前端分支，没有专门的浏览器测试。只跑了 phase5_dialog_close_labels，它用的接管对话框 fixture 里没有 ADB 组。后端只有宠物接管路径有 ADB 专项测试；HTTP 接管路径（takeover_handlers.go）的改动和宠物路径逐行对称，但只经过原有接管测试回归，没有 ADB 专项用例。
- schema 版本号用的是 v156。如果其他分支先合入并占用了 156，需要顺延。
- 首屏 gzip 预算从 272,896 上调到 273,408（实测 273,350，比 main 的 272,829 多了 JS 480、CSS 41 字节），原因写在 build.test 的注释里。三段较长的新说明文字没有加 en-US 翻译，和硬件页原有的 ADB 说明一样，英文界面下会显示中文。
- wireless 和 tcp 指向同一个 host:port 时共用同一个终端会话键（`network:adb:host:port`），这是有意的：同一台设备不能被两个读写会话同时占用。
- Agent exec 原有的黑名单（connect/pair/devices/kill-server 等）没有扩充。`tcpip`、`usb`、`reconnect` 这类设备级子命令仍然可以对已绑定的设备执行，行为和改动前一样。
- `internal/httpapi/takeover_handlers.go` 在 main 上本来就没有 gofmt，为了不扩大冲突面，这次没有格式化它。
- 与 task-722/723（未合并）的重叠：agent_handlers.go 里的 agentADBExec（我只改了选目标那一段，722 改的是输出截断，两处相邻）、server.go 的 Config 和路由、hardware_handlers.go（函数不同）、manager.go（字段和分支，722 新增的是 DisconnectTaskExcept）、pet.go 的 schema。722 的路由覆盖清单在合并后可能需要登记新的 Owner 路由。已经通过 AHA 进度消息告知宠物。

## 关键调用链（实现提交 3890244；函数体在各 diff 分卷里，这里给链路和守卫）
1. Owner 创建/编辑：`POST|PUT /api/v1/hardware-resources[/{id}]`（withAuth）→ `resourceFromPayload` → `hardwareconfig.NormalizeGroup` → `normalizeADBTarget` → `ADBPairingStillValid` → ADB 资源清除凭据 → `store.UpsertHardwareResource`（写入前 `NormalizedADB`）→ 编辑时执行 `disconnectHardwareResource(existing)`（依赖修好的 `Supports`）。
2. 配对：`POST /api/v1/hardware-resources/{id}/adb/pair` → 检查 mode=network 且 protocol=adb → 检查 `!ADBUSB()`、`!ADBTCP()` → `pairADB(host:pairing_port, code)`；审计里只记连接端点。
3. USB 枚举：`POST /api/v1/hardware/adb/usb-devices`（withAuth）→ `listADBUSBDevices`（默认 `hardware.ListADBUSBDevices`）→ `adb devices -l` → `parseADBDevices`。
4. Agent exec：`POST /api/v1/agent/hardware/{hw}/adb/exec`（withAgentCapability）→ `bindAgentTask` → `hardwareTarget`（`store.HardwareGroup(taskID, hw)`，只查本 Task 的绑定）→ 要求 transport=network 且 protocol=adb → 只读返回 403 → 参数校验（第一个参数不能以 `-` 开头，命中主机级黑名单返回 400）→ `switch`：usb → `executeADBUSB(serial)`；tcp → `executeADBTCP(host:port)`；其他 → `executeADB(host:port)`。
5. 终端：`POST /api/v1/tasks/{id}/hardware/{hw}/connect` 或 Agent connect → `hardwareTarget` → `Manager.Connect` → `Supports` → `sessionTarget`（usb：key=`network:adb-usb:<serial>`，endpoint=`USB <serial>`；serial 不合法直接报错）→ 按顺序判断：openSerial / openSSH / ADBUSB→openADBUSB / ADBTCP→openADBTCP / openADB / 其余 TCP 直连。
6. 宠物：`aha_create|update_hardware_resource` → `decodePetHardwareResourceInput`（拒绝 password、credential_ref、clear_*、pairing_code，包括嵌套在 network 里的）→ `applyTo`（合并 adb_connection 和 adb_serial）→ `validatePetHardwareResourceInput`（usb 要求 adb_serial，其他要求 host）→ `NormalizeGroup` → `ADBPairingStillValid` → 资源有凭据时禁止修改连接目标（目标比较里加了 ADB 两个字段）→ Upsert → 断开旧连接。
7. 接管（HTTP `takeover_handlers.go` 和宠物 `pet_takeover.go` 两条路径对称）：远端是 USB 时要求本机 serial，除非显式改成 wireless；没传 adb_connection 时沿用远端的形式 → `NormalizeGroup`。
8. 读取：`store.HardwareResources`、`HardwareResource`、`HardwareGroups` 读出后都执行 `Network.NormalizedADB()`。

## 分卷
- `task-726-review-packet-diff-1-go-core.md`：38293 字节，sha256 `470b60669383ca7016b8afa2411fcc2d8690a3148ccaa40e04f0987219f9e7c5`；7 个文件
- `task-726-review-packet-diff-2-go-api.md`：26808 字节，sha256 `92231af1b72ddcfb42d59ef8eeabe8b9e2fe5bdc668f52357002aebc55ad4514`；10 个文件
- `task-726-review-packet-diff-3-web.md`：38065 字节，sha256 `4f0208f9f8279f86540e06879b87be0c1caaea926310fd8363d0f65539202dee`；8 个文件
- `task-726-review-packet-diff-4-go-tests.md`：53008 字节，sha256 `f5e7cc909bfb423cc104095ac885f840c8a342401693e894ca0b0b664d2711df`；7 个文件
- `task-726-review-packet-diff-5-browser-tests.md`：21983 字节，sha256 `46a9fd8510bd1fb75c4e1831472198af0b2556c5dd7aa0f28a56ed6b7fc035d5`；2 个文件
