# AHA2 共享浏览器 Host（POC）

AHA2 的交互浏览器必须运行在 owner 的真实登录桌面会话里。Windows 服务默认在
Session 0，不能可靠显示窗口；因此本目录提供的是一个**独立进程**：

- AHA2 控制面只通过 `127.0.0.1` 上的 token API 操作浏览器；
- host 自己管理 Edge 用户数据目录、单实例锁和可见窗口；
- AHA2 数据库只保存逻辑 profile ID，不知道真实目录；
- host 不向 AHA/Task/局域网暴露 CDP 端口；cookie 只在 AHA 已授权的 Task 请求中
  导入/导出，不写日志，profile 路径、CDP 地址和密码管理接口始终不离开 host。
- 页面输入只接受结构化快照里的短期元素 ref 或 viewport 坐标；不接受选择器、
  脚本或任意 URL 参数。文件上传只接受请求体里的字节，下载只会落到受控目录
  并以不透明 ID 返回。

## 前置条件

- Windows 10/11 桌面会话；
- Microsoft Edge 已安装（POC 只支持 `browser_kind=edge`，启动失败不会换
  Chrome/Chromium）；
- Node.js 20+（Windows 安装包已内置 Node.js LTS 运行时，见下文“打包与运行时”）；
  从源码手工运行时只需机器上自备 Node 20+。

AHA 启动时会自动发现 `<安装目录>\browser-host\host.mjs`，随机分配
`127.0.0.1` 端口和短期 token 并拉起 host；用户不再需要配置 URL。

## 自动启动（默认）

正常安装后无需手工启动 host。AHA 负责：

- 优先使用安装目录内置的 `<安装目录>\node\node.exe`；`AHA2_NODE_PATH`、`PATH`、
  `%ProgramFiles%\nodejs`、`%LOCALAPPDATA%\Programs\nodejs` 是覆盖和兜底；
- 每次启动（含崩溃重启）重新生成 256-bit token，token 只通过子进程环境传递，
  不写入磁盘、不进入 AHA 日志；
- host 崩溃后按指数退避自动重启；崩溃/重启预算跨单次 `restart()` 保留，只有
  新 host 稳定运行 60 秒后才重置，达到上限即失败关闭；
- AHA 退出时先请求 host 关闭浏览器会话，再清理子进程；
- 启动失败、缺少 Node、缺少 host 脚本、端口被占用时按稳定错误码失败关闭，
  不阻塞 AHA 主服务。

资源页和 Task 面板不再要求或读取 `AHA2_BROWSER_HOST_URL`。

仅供测试/排障的覆盖项：

```powershell
$env:AHA2_BROWSER_HOST_URL = "http://127.0.0.1:8765"   # 使用已手工启动的 host
$env:AHA2_BROWSER_HOST_TOKEN_FILE = "D:\...\token"      # 与上面的 URL 搭配
$env:AHA2_BROWSER_HOST_ID = "local"
$env:AHA2_BROWSER_HOST_SCRIPT = "D:\...\browser-host\host.mjs"
$env:AHA2_NODE_PATH = "C:\Program Files\nodejs\node.exe"
$env:AHA2_EDGE_PATH = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
```

## 打包与运行时

`scripts/build-windows-package.sh` 会把三部分放进发布目录：

- `dist/browser-host/`：host 源码和 `playwright@1.49.0` 的 Node 依赖，约 12 MB
  （452 个文件，主要是 `playwright-core` 的 JS）；跳过 Playwright 自带浏览器
  下载，host 启动的是本机 Edge。
- `dist/node/node.exe` + `dist/node/LICENSE`：官方 Node.js LTS 运行时
  （默认固定 `22.23.5`，可用 `AHA2_NODE_RUNTIME_VERSION` 改版本）。构建时从
  nodejs.org 下载官方归档，并按该版本的 `SHASUMS256.txt` 校验 SHA-256；
  `AHA2_NODE_RUNTIME_CACHE` 可指向已缓存的官方归档（同样校验 SHASUMS）。
  `AHA2_NODE_EXE`/`AHA2_NODE_LICENSE` 只接受默认固定版本，且 node.exe 必须
  匹配从该官方归档推导的固定 SHA-256（nodejs.org 不单独发布 node.exe 的
  checksum），改一个字节就会失败；非固定版本请走归档路径。缓存入口仍会拉取
  该版本的 `SHASUMS256.txt` 做交叉校验，因此构建机需要能访问 nodejs.org。
  `node.exe` 约 84 MB，安装包压缩后预计增加约 25–30 MB。
- `release-manifest.json`：覆盖 `browser-host/` 与 `node/` 下的全部文件。

安装器把它们复制到 `{app}\browser-host` 和 `{app}\node`，升级时先删除这两个
目录再整体替换，避免旧依赖残留。AHA 启动时优先使用 `{app}\node\node.exe`，
所以干净的 Windows 机器不需要系统 Node/npm，也不需要用户手工安装依赖；
`AHA2_NODE_PATH` 仍可覆盖。`node.exe` 来自官方发布包：SHA-256 来自
nodejs.org；构建机有 `osslsigncode`（或 Windows `signtool`）时会真正执行
Authenticode 验签并检查 OpenJS Foundation 签名者，失败即构建失败；没有验签
工具时明确输出“未验证”，不会把证书列举当成验签结果。`AHA2_OSSLSIGNCODE`
可指定验签工具，`AHA2_NODE_SIGNATURE_POLICY=require` 可要求没有验签工具时
直接失败。安装包本身的 Authenticode 签名/升级仍由 Windows 发布流程负责。
若内置运行时缺失或损坏，资源页显示 `browser_host_node_missing` 并失败关闭。

## 手工启动（排障）

```powershell
$env:AHA2_BROWSER_HOST_TOKEN = "<随机长 token>"
$env:AHA2_BROWSER_PROFILE_ROOT = "D:\ProgramData\AHA2\browser-profiles"
$env:AHA2_BROWSER_HOST_ID = "local"
node host.mjs --port 8765 --data-dir "D:\ProgramData\AHA2\browser-profiles"
```

同时给 AHA2 进程设置**同一个** token 和上面的
`AHA2_BROWSER_HOST_URL`。`AHA2_BROWSER_HOST_TOKEN` 直接传值也可用，但 AHA2
启动后会立即从自身环境删除它，避免 Task 子进程继承。host 侧同样支持
`--token-file <path>`。token 缺失时 host 拒绝启动；host 只监听 `127.0.0.1`，
没有监听地址参数。日志不会打印 token。

可选的 Edge 路径覆盖：

```powershell
$env:AHA2_EDGE_PATH = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
```

profile 根目录建议放在 AHA2 数据目录下的独立子目录，并把 ACL 限制为 owner。
host 为每个 profile 创建 `0700` 子目录和 `.aha2-host.lock` 锁文件。锁文件带随机
owner token，只有 token 匹配时释放才会删除；同 profile 的 acquire/release 在
host 内串行化。崩溃遗留的 stale 锁通过 `.reclaim` 文件仲裁回收，两个 host 进程
不会互相删除刚创建的活锁；profile root 另有 `.aha2-host.instance` 单实例锁，
第二个 host 会拒绝启动。

AHA 侧业务请求客户端超时 60s，覆盖 host 的 30s 页面导航期限；启动就绪探针
单独使用 5s 客户端（每次探测上下文 2s），不会把慢导航误判为宿主超时。导航
失败会关闭刚创建的 page，不会留下 AHA 无法管理的孤立页面。

## HTTP 契约（host 侧）

所有请求（含健康检查）都需要 `Authorization: Bearer <token>`。持久 fence
身份是 lease ID；mutation 传入的 `<leaseID>|<operationDeadline>` 中只有前段参与
身份比较，后段是本次操作的临时 deadline。deadline 必须是有限正整数，且不超过
host 当前时间 + 5 分钟 + 30 秒偏差；裸 ID、NaN/Infinity、多分隔符、负值、零、
溢出或超上限均返回 4xx 且不执行页面操作。host 不会把调用方声明的 deadline 当作
无限授权。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/v1/health` | `host_id`、`platform`、`edge_available`、活跃 profile 列表 |
| POST | `/v1/shutdown` | 关闭所有浏览器会话并退出（AHA 停止时调用） |
| POST | `/v1/profiles/{id}/session` | 启动或复用持久化 Edge 窗口（`{"browser_kind":"edge"}`） |
| POST | `/v1/profiles/{id}/fence` | CAS 安装租约 fence（`{"session_id","expected_fence","fence"}`；切换会等待在途页面操作） |
| GET | `/v1/profiles/{id}/session` | 当前会话状态；没有则 404 `browser_session_not_found` |
| DELETE | `/v1/profiles/{id}/session` | 关闭窗口并释放目录锁 |
| GET | `/v1/profiles/{id}/tabs?session_id=` | 列出窗口内当前 http/https 页面（`id/url/title/active`），供独占 Task 枚举 |
| POST | `/v1/profiles/{id}/tabs` | 打开 Task 标签页（`{"session_id","url"}`） |
| POST | `/v1/profiles/{id}/tabs/{tab}/activate` | 切换/激活标签页（`bringToFront`） |
| POST | `/v1/profiles/{id}/tabs/{tab}/navigate` | 导航（`{"session_id","url"}`） |
| GET | `/v1/profiles/{id}/tabs/{tab}/snapshot?session_id=` | 返回 `{url,title,text,elements}`；elements 只含 role/name/bounds/ref/actions，不含输入值、HTML 或 cookie |
| POST | `/v1/profiles/{id}/tabs/{tab}/actions` | 页面输入（`click`/`double_click`/`hover`/`type`/`key`/`scroll`），目标为快照 ref 或 viewport 坐标 |
| POST | `/v1/profiles/{id}/tabs/{tab}/uploads` | 把请求体中的 base64 文件交给 file input 或文件选择框；不读宿主路径 |
| GET | `/v1/profiles/{id}/tabs/{tab}/frame?session_id=` | 返回只读 JPEG 画面；200ms 内复用上一帧 |
| DELETE | `/v1/profiles/{id}/tabs/{tab}?session_id=` | 关闭标签页 |
| GET | `/v1/profiles/{id}/bookmarks?session_id=` | 读取 Edge 收藏夹，只返回 title/url/folder，不含路径 |
| GET | `/v1/profiles/{id}/downloads?session_id=` | 列出本会话已捕获的下载（id/filename/size/created_at） |
| GET | `/v1/profiles/{id}/downloads/{download}?session_id=` | 以原始字节返回一个下载；文件名经响应头传递 |
| DELETE | `/v1/profiles/{id}/downloads?session_id=` | 清空本会话下载（释放租约/关闭会话时自动调用） |
| GET | `/v1/profiles/{id}/cookies?session_id=&url=\|domain=` | 按 URL 或 domain 导出 cookie；必须限定范围 |
| POST | `/v1/profiles/{id}/cookies` | 导入经过校验的 cookie；响应只返回数量 |

错误统一为 `{"error":{"code","message"}}`。重点错误码：

- `browser_host_unavailable` / `browser_host_timeout` / `browser_host_unauthorized`：
  host 不在、请求超时或 token 不符；
- `browser_edge_unavailable`：找不到 Edge；
- `browser_profile_locked`：用户数据目录被另一个进程占用；
- `browser_session_not_found` / `browser_session_changed`：窗口已关闭或会话已换代；
- `browser_tab_not_found`：标签页不存在或已关闭；
- `browser_element_not_found` / `browser_element_stale`：快照 ref 不在当前页面，
  或来自更早的快照/导航；重新获取 snapshot 后再操作；
- `browser_action_invalid` / `browser_upload_invalid`：动作/文件参数非法；
- `browser_download_not_found`：下载 ID 不存在、已过期或不属于本会话；
- `browser_cookie_invalid`：cookie 的 domain/path/sameSite/值校验失败；
- `browser_tab_list_failed`：读取当前页面列表失败（页面已崩溃或上下文不可用）；
- `browser_activation_failed`：切换前台页面失败；
- `browser_bookmarks_failed`：收藏夹文件不可读、是 symlink/reparse point 或格式异常；
- `browser_fence_stale`：控制租约在操作开始前已变化，拒绝旧 token；
- `browser_fence_invalid`：mutation 的 lease/deadline 格式非法或超过硬上限（400）；
- `browser_fence_expired`：租约已过截止时间，拒绝新操作；
- `browser_lease_hold_failed`：无法为已开始的调用建立 lease hold 或续租失败，
  在 Playwright 调用前拒绝，页面不变；
- `browser_lease_hold_release_failed`：服务端 hold 恢复失败（仅由 AHA 产生），
  操作已完成但临时期限保留，deadline 固定有界；
- `browser_fence_timeout`：fence 切换等待在途操作超时，旧 fence 保留。

## 自动化测试

```powershell
cd browser-host
npm test
```

覆盖锁并发/过期回收/token 保护、导航失败页面清理、fence 切换/过期、
lease hold 与底层 close/navigate/open 调用窗口；真实 Edge 仍需手工验收。

## 手工验收（Windows + Edge）

本仓库的 Linux 构建机无法运行真实 Edge，因此以下步骤必须由 owner 在浏览器
主机上手工执行，不能当作已自动验证：

1. 在一台**没有系统 Node/npm** 的干净 Windows 机器上安装；不设置
   `AHA2_BROWSER_HOST_URL`、`AHA2_NODE_PATH`，正常启动 AHA2。确认：
   - `<安装目录>\node\node.exe --version` 可用（内置运行时）；
   - 日志出现 `browser host started`，资源页宿主横幅显示可用且 Edge 可用，
     页面不再提示配置 URL。
   如果这台机器上也装了系统 Node，请临时从 PATH 移除，以证明只靠内置运行时
   即可启动。
2. 在资源页创建「工作浏览器」profile。
3. 点「打开登录窗口」：宿主桌面出现可见 Edge 窗口；登录任意测试站点。
4. 正常关闭窗口（不是强杀进程）；刷新资源页，状态回到「未初始化」。
5. 再次打开登录窗口并访问该站点，确认仍是登录态（persistent context 生效）。
6. 在 Task 详情 → 共享控制 → 控制方式选「内置浏览器」，选择该 profile，
   点「启动 / 附加会话」。确认：
   - 状态变为「可用」；
   - 显示器下拉被隐藏；
   - Task 标签页列表为空且属于当前 Task；
   - 授权到期时间约为 30 分钟后。
7. 用同一个 profile 给第二个 Task 建立授权，确认复用了同一个可见窗口，
   且两个 Task 的标签页互不可见/不可操作。
8. 在面板中打开/选择标签页，确认只读画面出现且 URL/标题正确；切回显示模式或
   关闭面板后，网络请求停止（Network 面板无新的 frame 请求）。
9. 关闭会话后，两个 Task 的授权都变为已撤销，操作返回
   `browser_session_closed` / `browser_grant_revoked`。
10. 区分两种模式验证宿主不可用/重启：
    - 自动启动模式（未设置 `AHA2_BROWSER_HOST_URL`）：强杀 host 进程
      （`taskkill /F /PID <node PID>`），AHA 应记录重启并使用新端口恢复可用；
      如果反复“就绪后立即崩溃”，达到重启预算后资源页显示
      `browser_host_start_failed`，不会无限重启。
    - 手工 host 模式（设置了 `AHA2_BROWSER_HOST_URL`）：停掉手工 host 再打开
      登录窗口，AHA 明确返回 `browser_host_unavailable`，不会换 profile 或浏览器。

## 已知边界（POC）

- host 使用每次启动重新生成的共享 loopback token，不是每个 Task 的短期 token；
  AHA 进程内不做 token 轮换（崩溃重启会换新 token）。
- 只读画面是授权标签页的页面 viewport（不含浏览器外壳），不是整窗桌面截屏；
  也不提供 CDP 预览通道。需要整窗画面时另走共享控制桌面链路。
- 不检测登录过期；`auth_required` 状态与登录检测属于后续里程碑。
- 不做远程 host 注册、不实现 headless、不支持 Chrome/Chromium。
- 标签页快照的渲染文本与元素名称仍可能包含已授权页面上的账号数据；这是业务
  授权结果，不是 storage 导出。cookie 只经显式、受 Task 能力约束的 API 传输，
  不写审计、不写日志。
- 页面输入与文件上传都在 Owner 可见的 Edge 窗口中发生；host 不读取 Edge 已保存
  密码，也不提供密码管理器接口。
- 下载文件保存在 `<profile-root>/.downloads/` 的会话目录，TTL 30 分钟、最多
  20 个、单个 10 MiB、累计 50 MiB。Chromium 的 `downloadsPath` 指向受控目录，
  host 在下载进行中轮询原始 artifact 大小，超限立即 cancel 并删除 artifact；
  释放租约或关闭会话即清理。
- profile 删除只删 AHA 逻辑记录，host 用户数据目录会保留，需后续提供受控清理。
