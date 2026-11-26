# 宠物 MCP 全覆盖实现记录

完整目标是让宠物有 owner Web 业务操作对应的入口，以及普通 Task 的所有适用能力。当前只完成首批公共门禁、Turn 和硬件实现，整体仍在进行；此目录不能作为完成证明。

基线 `a3ed6cb566680ff079d660685126a2b77a76bad8`，分支 `aha/task_e3d0848747ddc2018605`，工作目录为本 Task 独立 worktree。所有提交保留在此分支，不合并 main、不推送、不部署、不操作生产资源。

## 账本与门禁

三份账本都由 `internal/petcoverage` 生成和校验，它用 Go 自己的语法解析器读源码，不靠文本正则：

- `coverage.tsv`：`internal/` 与 `cmd/` 下所有 `ServeMux` 注册（任意变量名、常量、拼接、局部前缀、字面量路由表都会展开），按监听分 `httpapi`、`mail-download`、`centersync`。每行带源文件、真实 handler、认证边界（由最外层包装推出）和 `v2:` 指纹。
  - 包装与 handler：`boundaries` 里登记的包装不论几个参数都按包装处理（被包装的 handler 默认是最后一个参数；handler 不在最后的包装要在 `wrapperHandlerArgument` 里写明位置，例如 `withWorkspaceTerminalOwner(handler, requireOrigin)`），其余参数必须是可求值的常量（字符串常量，或没有被同名局部变量、常量遮住的 `true`/`false`），一路下钻到真实 handler；参数里任一位置带 handler 的未登记调用当作未审包装报错；记录下来的 handler 名不能是包装名。只有常量参数的工厂（如 `s.savePetWorkbenchDocument("core")`）记为 handler，实参计入指纹。
  - 裸函数名 handler：`mux.HandleFunc(path, workspaceViewerMethodNotAllowed)` 这类写法解析到注册所在包里同名的包级函数（没有接收者），以该函数源码计算指纹，照样判断是否只回写客户端错误。这个名字如果是注册函数里的参数、局部变量或循环变量，或者包里没有这个包级函数（包括包级变量），扫描失败：那是在别处赋值的 handler 值，扫描看不到它是什么。
  - 路由循环：除了按字面结构体表逐行展开（`route.pattern`），`for _, base := range []string{...}` 里用 `base` 拼出的路由串按每个元素各展开一条。只认字面 `[]string`、元素都是可求值常量、循环里不改写 `base`；其余形状照旧报“无法求值的路由串”。
  - 指纹：包装链及其常量参数、每个包装函数自己的源码、工厂调用的实参、handler 源码，一起做 SHA-256。handler、包装参数或包装实现任一变化，已审行在 `-update` 时退回 stale。从旧格式迁移时，只有旧指纹恰好等于"用旧算法重算当前代码"的行才保留结论。
  - 对账：源码里每个 `.Handle(` / `.HandleFunc(` 词法记号都要对上一处解析出的调用，并且要么是路由注册，要么列为非路由调用并写明原因（目前只有 `instancelock` 里两处单参数的 `windows.Handle` 类型转换）。`.Handle`/`.HandleFunc` 出现在非调用位置（`add := mux.Handle` 这类方法值别名，或把 `http.Handle` 当值）、方法表达式 `(*http.ServeMux).Handle(...)`、参数个数不是 2 但带路由串的调用、函数体外的调用、注册到 DefaultServeMux 或非 `http.NewServeMux()`/`*http.ServeMux` 参数的接收者、未登记的注册函数、无法求值的路由串、找不到唯一声明的 handler，扫描都会直接失败。
  - 运行期对照（双向）：`TestPetCoverageScanMatchesLiveMuxes`（httpapi 与 mail-download）和 `TestCoverageScanMatchesLiveCenterMux`（aha-sync）用真实 mux 的 `Handler(r)` 确认扫描出的每个操作都被路由到完全相同的模式；同时读出 mux 实际登记的全部模式（Go 1.22+ 的内部冲突索引，结构变化时直接报错），mux 里有而扫描没有的同样失败。主监听的注册放在 `routeMux()` 里，就是为了这一步能拿到真实 mux。
- `web-call-sites.tsv`：`web/src` 里所有点名 AHA2 API 的字面量：以 `/api/` 开头的字符串/模板、经同源动态基址（只由 `${...}`、`/`、`:` 组成，例如 `${protocol}//${location.host}`）到达 `/api/` 的模板，以及字面量文本里带引号的 `/api/` 路径（例如 `src="/api/..."`）。扫描是真正的 TS 词法：注释、正则字面量（含模板表达式里的正则）跳过，`${...}` 里的嵌套字面量单独计入且不重复。清单不带行号，同一文件里相同的调用合并为 occurrences，挪动代码不会改账本。每个调用点必须能对上一条已注册的 httpapi 路由（方法明确时方法也要一致），对不上就说明有注册逃过了扫描。
- `task-mcp-tools.tsv`：普通 Task 可用的全部内置 MCP 工具（`httpapi.MCPToolCatalog()`，宠物自己的 server 除外），逐个记录宠物 Turn 如何拿到同样能力。

```sh
go run ./cmd/aha-pet-coverage            # 账本与注册/调用点/工具目录一致，且每行证据真实
go run ./cmd/aha-pet-coverage -update    # 重写账本；人工列保留，handler 变了的已审行退回 stale
go run ./cmd/aha-pet-coverage -complete  # 最终门禁：所有行都关闭且证据有效，当前必须失败
go test ./internal/petcoverage ./internal/httpapi -run 'PetCoverage|MCPToolCatalog|HealthProbe'
```

`go test ./internal/httpapi` 里的 `TestPetCoverageLedgersMatchRegistrationsAndEvidence` 做的就是第一条命令：新增、删除或改动路由而不更新账本，测试失败。`go test ./cmd/aha-pet-coverage` 在真实仓库上验证命令本身：检查通过、`-complete` 失败、错误参数退出 2（`-update` 只在合成源码树上测，不在测试里改真实检出）。

状态分两类。开放：`pending`、`implemented`、`implemented_unverified`、`stale`。`stale` 表示该行之前审过，但 handler、源文件、边界或指纹已经变了，旧结论不再成立，必须重新核对后才能改回关闭状态；从 v1 账本迁移时，所有没有指纹的已审行都按这个规则退回了 stale。关闭：`direct`、`equivalent`、`protected_handoff`、`transport_na`、`web_prohibited_na`。关闭行的 mcp、permission、protected_flow、tests 四列都不能为空或 pending，并且：

- tests 只接受 `路径_test.go:TestName`（多个用 `; ` 分隔），文件必须存在且声明了该顶层测试。"existing ... tests"、通配路径这类说法会被拒绝；开放行里写了非 pending 的 tests 也同样核对。
- direct/equivalent/protected_handoff 的 mcp 至少点名一个 `server.tool`，且每个都在内置工具目录里。
- transport_na 的 mcp 必须是 none，且只用于 MCP 协议端点、无认证传输、设备令牌或渠道运行时路由；owner 会话下的业务操作不能标 N/A。
- web_prohibited_na 的 mcp 必须是 none，且 handler 必须是"只回写客户端错误"的拒绝函数（扫描器逐个调用核对）。

受保护功能需要真实申请、状态与 owner 安全输入/授权交接，不能标成 owner_only 后略过；账本也不接受 owner_only 这个状态。

首批实现中，宠物 core 不再提前退出注入；知识只补指南和反馈，其他知识与 Skill 操作复用宠物已有入口；Workspace、浏览器与硬件按当前绑定、授权和实时查看权限注入。硬件写入另外要求 Main、可写 snapshot、可写资源以及宠物实时 Task 管理权限。普通 Task、只读 Task 和渠道 Turn 的原作用域继续生效。`capabilities` 新增各 MCP 域和硬件写入的实际门禁标志，原 HTTP 能力标志仍表示该 HTTP 面是否存在。

公共 operation adapter 是逐操作固定 handler 和 schema 的适配，不接受任意 URL、方法或 Task 身份，不携带 owner Cookie、CSRF 或 Authorization。复用原 Agent handler 的服务与资源授权；结果只返回指定字段或有界字节，内部错误不回显。凭据、host key 信任和配对暂未新增可执行入口；对应安全交接仍是后续实现范围。

后续仍需完成公共审批/安全输入、硬件完整生命周期、共享控制、托管进程、Task 高级操作、全部 Web 管理域、后端适配与全量回归。浏览器扩展已有未提交草稿，未包含在 P1a 代码提交和该批测试结论中。

review 目录的 packet 按明确提交边界提供完整 diff、关键函数与调用方和验证结果，每份不超过 65536 UTF-8 字节。测试使用 scratch 下的隔离数据库与假执行器；编译、静态检查、fixture 业务测试和真实平台测试分别记录，不能相互替代。
