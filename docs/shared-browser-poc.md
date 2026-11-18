# 共享浏览器 POC（milestone 1）

日期：2026-11-17
范围：AHA2 浏览器 profile 资源、会话生命周期、Task 授权、Browser host、Web 入口。

## 1. 设计落点（v2）

| 对象 | 实现 |
| --- | --- |
| Profile | `browser_profiles`；只存逻辑 ID、名称、`browser_kind=edge`、`host_id`、状态、`last_used_at`。真实用户数据目录由 host 解析，AHA 不保存、不返回。 |
| Session | `browser_sessions`；属于 profile，不归 Task。`starting/ready/auth_required/busy/closed/error`。 |
| Task 独占控制 | `browser_task_grants`；一个 Profile 同时只有一个 active lease，带 `task_id/profile_id/session_id/capabilities/expires_at/revoked_at`。 |
| Tab | `browser_tabs`；属于 session，记录当前持有者和 `host_tab_id` 用于枚举/画面/审计；操作只校验独占 lease 与会话，不再逐 tab 校验 grant。 |
| Host | 独立 Node 进程 `browser-host/host.mjs`，Playwright persistent context + 可见 Edge；仅 loopback + Bearer token。AHA 启动时自动发现并拉起，随机端口 + 每次启动重新生成的短期 token；崩溃自动重启，AHA 停止时先请求 host 关闭会话再清理子进程。 |

同一 profile 单实例有三层约束：

1. host 的用户数据目录 `.aha2-host.lock`：锁文件带随机 owner token，只有 token
   匹配才允许删除；host 内按 profile 串行化 acquire/release；stale 锁的回收通过
   `.reclaim` 文件仲裁，只有回收者能删除，避免两个 host 进程互相删掉活锁；profile
   root 另有单实例锁，第二个 host 直接拒绝启动；
2. host 内存注册表（重复打开直接复用同一窗口）；
3. SQLite 部分唯一索引 `browser_sessions(profile_id) WHERE state <> 'closed'`。

数据库不是唯一依据：host 窗口被 owner 关闭后，AHA 在下次状态读取/操作前通过
`GET /v1/profiles/{id}/session` 对齐，标记 `closed` 并撤销该会话的所有授权。

## 2. API（Owner 会话）

资源页：

- `GET/POST /api/v1/browser/profiles`
- `GET/PATCH/DELETE /api/v1/browser/profiles/{id}`
- `POST /api/v1/browser/profiles/{id}/login-window`：启动/复用可见窗口
- `GET/DELETE /api/v1/browser/profiles/{id}/session`
- `POST /api/v1/browser-sessions/{id}/close`
- `GET /api/v1/browser/host`：host 健康状态

Profile 独占控制（profile_exclusive）：

- `POST/GET /api/v1/tasks/{id}/browser-grants`（别名 `/browser-sessions`）。一个
  Profile 同时只允许一个 active lease，一个 Task 也同时只持有一个 Profile lease。
  获取在 SQLite 事务内完成：同一 Task 对同一 Profile 的重复获取幂等复用原 lease
  （只刷新 capabilities/TTL），不会撤销重建；同一 Task 已持有另一个 Profile 时
  返回 409 `browser_task_profile_limit`，旧 lease 保留，必须先显式释放；其他 Task
  的未过期 lease 返回 409 `browser_profile_in_use`，不会静默抢占。schema v151 把唯一索引从
  `(task_id,profile_id)` 改为 `browser_task_grants(profile_id) WHERE revoked_at=''`，
  迁移会按 Profile 保留最新 lease 并撤销其余旧记录。
- `DELETE /api/v1/browser-grants/{id}`：释放控制。只撤销 lease，不关闭可见页面；
  其他 Task 之后可以获取。释放前必须先安装 released fence；host 不可达/超时时
  返回原错误码且 lease 保持 active，避免在 host 仍可能执行旧操作时虚报释放成功。
  只有 host 明确返回 `browser_session_not_found`（没有执行端）才允许仅撤销 DB。
- `POST /api/v1/browser/profiles/{id}/transfer`（Owner 会话）：显式把控制权转移给
  `{"task_id":"..."}`。Task Agent 没有这个端点，不能自行抢占。
- `GET /api/v1/tasks/{id}/browser`：Task 详情合并视图；持有 lease 时同步返回该
  Profile 当前全部 http(s) tab；Profile 列表会带占用 Task 的 id/title，供 Owner
  面板显示等待/转移入口。

租约 fencing：每次获取/转移/释放都会通过 host `POST /v1/profiles/{id}/fence`
用 CAS 安装新的 fence（旧值匹配才写入），持久 fence 身份就是 lease ID；所有
open/activate/navigate/close 都带 `<leaseID>|<operationDeadlineMs>`。host 为每个 session 维护读写门：
页面操作拿读槽并持有到副作用完成，fence 切换先立即失效旧 token，再拿写槽等待
在途操作；超时（默认 5s）返回 409 `browser_fence_timeout` 并恢复旧 fence，不能宣称
切换成功。mutation 开始前 host 先确认 token 在调用时仍然有效，并建立仅覆盖本次
`page.close()`/`goto()`/`bringToFront()` 的 operation hold：已获准操作不会在
Playwright 调用中途因原 expiry 到期而失败，也不需要事后重开页面或回滚 URL。
host 的持久 fence 身份固定为 lease ID；每次 mutation 另带
`<leaseID>|<operationDeadline>`，临时 deadline 不参与 fence 身份比较，因此同一
lease 的并发请求不会互相误报 `browser_fence_stale`。
service 用带独立 `hold_id` 的 CAS 把 DB lease expiry 临时延长到当前操作预算：
同一 lease 的并发操作共享同一 hold；结束后只恢复该 `hold_id` 写入的 expiry。
owner 合法续租会清空 hold 身份并获胜，旧 hold 恢复不能覆盖新 TTL。续租失败在调用
Playwright 前返回 409 `browser_lease_hold_failed`；恢复 SQL 失败会记录审计并返回
500 `browser_lease_hold_release_failed`，hold deadline 固定、不会滚动，过期后可被
新的 owner 获取。hold 只保护已经开始的调用；切换后的暂停旧调用仍返回
`browser_fence_stale`，release/transfer 通过写槽等待且不会被无限阻塞。读操作仍
每步重新鉴权。

标签页操作（Owner 与当前独占 Task 共用同一服务层；不再有 per-tab grant 归属）：

- `POST /api/v1/browser-grants/{id}/tabs`：打开新 tab
- `POST /api/v1/browser-grants/{id}/tabs/current`：返回当前活动页（并同步 tab 列表）
- `POST /api/v1/browser-grants/{id}/tabs/{tab}/activate`：切换/激活 tab
- `POST /api/v1/browser-grants/{id}/tabs/{tab}/navigate`
- `GET /api/v1/browser-grants/{id}/tabs/{tab}/snapshot`
- `GET /api/v1/browser-grants/{id}/tabs/{tab}/frame`：只读 JPEG 预览（2–5 fps，
  页面不变时自动降帧）。帧不落盘、不写审计正文，关闭面板即停止拉取。
- `DELETE /api/v1/browser-grants/{id}/tabs/{tab}`：关闭任意 tab（包括 Owner 手动
  打开的页面），origin 仅作展示标签。
- `GET /api/v1/browser-grants/{id}/bookmarks`：读取该 Profile 的 Edge 收藏夹，
  只返回 title/url/folder，不返回文件路径。

`GET /api/v1/browser-grants/{id}/tabs` 和 Task 视图会调用 host
`GET /v1/profiles/{id}/tabs?session_id=`，完整枚举窗口当前的 http(s) 页面
（不再截断到固定条数，页面元数据有界并发读取），再同步进
`browser_tabs`：新发现的页面标记 `origin=owner`，已有 AHA 页面保留 `agent`。
`devtools://`、`edge://`、`chrome://`、`about:` 不会出现。schema v150 的
`browser_tabs(session_id, host_tab_id) WHERE host_tab_id <> ''` 唯一索引保留，
迁移会去重旧行；它只防止同一 host 页面出现重复记录，不阻止当前独占 Task 操作
Owner 手动打开的页面。

host 收藏夹读取 `<profile>/Default/Bookmarks`，只解析
`bookmark_bar/other/synced` 下的 title/url/folder，最多 500 条、深度 8；文件不存在
时返回空列表。读取前用 lstat/realpath 校验 profile 根、Default 目录和 Bookmarks
本体：symlink、junction/reparse point、目录链接或 `..` 越界一律失败关闭，不返回
任何收藏内容。打开前记录文件 dev/ino/size/mtime，打开后用已打开句柄 fstat、
父目录再次 lstat 和二次 realpath 对比实际对象身份；检查后替换父目录的 TOCTOU
也会失败关闭。响应和日志都不含 profile 目录。

Task Agent HTTP API（Bearer capability 绑定当前 Task/Turn；每个 grant 路由先校验
`grant.TaskID == claims.TaskID`，跨 Task 返回 `browser_grant_forbidden`）：

- `GET /api/v1/agent/browser`
- `GET|POST /api/v1/agent/browser/grants/{grant}/tabs`
- `POST /api/v1/agent/browser/grants/{grant}/tabs/current`
- `POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/activate`
- `POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/navigate`
- `GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/snapshot`
- `GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/frame`
- `DELETE /api/v1/agent/browser/grants/{grant}/tabs/{tab}`
- `GET /api/v1/agent/browser/grants/{grant}/bookmarks`
- `DELETE /api/v1/agent/browser/grants/{grant}`：Task 主动释放自己的 lease

Agent 只有只读 snapshot/frame/bookmarks 与 open/activate/close/navigate 控制动作；
不提供 CDP、页内点击/输入/滚动、cookie/storage、profile 路径或下载接口。lease
撤销、过期、转移或不属于本 Task 时所有操作失败关闭。

能力集合固定为 `open_tab | navigate | snapshot`；未知能力在创建授权时拒绝。

运行时前置：安装包携带 `browser-host/`（含 `playwright` Node 依赖，约 12 MB）和
`node/node.exe`（官方 Node.js LTS，默认 22.23.5，约 84 MB；安装包压缩后约增加
25–30 MB），安装到 `{app}\browser-host` 与 `{app}\node`。AHA 优先使用内置
`node.exe`，干净的 Windows 机器不需要系统 Node/npm；`AHA2_NODE_PATH` 仍是覆盖项。
构建时官方归档按 SHASUMS256.txt 校验 SHA-256；`AHA2_NODE_EXE` 缓存入口必须匹配
从该版本官方归档推导的固定 node.exe SHA-256；有 osslsigncode/signtool 时执行
真实 Authenticode 验签，没有验签工具时显式标记未验证（可用
`AHA2_NODE_SIGNATURE_POLICY=require` 改为失败）。内置运行时缺失/损坏时资源页
返回 `browser_host_node_missing` 并失败关闭，不会退回公开端口或 CDP。

## 3. 失败关闭
- host 未配置/不可达：`browser_host_unavailable`（HTTP 503）；释放时同样失败并保留 lease；
- Edge 缺失：`browser_edge_unavailable`（503），不自动换浏览器；
- profile 目录被其他进程占用：`browser_profile_locked`（409）；
- 授权过期/撤销：`browser_grant_expired` / `browser_grant_revoked`（403）；
- 会话已关闭或已换代：`browser_session_closed` / `browser_session_changed`（409）；
- 同一 Profile 已被其他 Task 独占控制：`browser_profile_in_use`（409）；
- 同一 Task 已持有另一个 Profile lease：`browser_task_profile_limit`（409）；
- 控制权在操作开始前已转移/释放：`browser_fence_stale`（409）；
- lease 过期后的新操作：`browser_fence_expired`（409）；
- 无法建立 lease hold/续租：`browser_lease_hold_failed`（409），操作在 Playwright 调用前失败；
- hold 恢复失败：`browser_lease_hold_release_failed`（500），操作已完成但临时期限未能恢复；deadline 固定有界；
- fence 切换等待在途操作超时：`browser_fence_timeout`（409），旧 lease/fence 保留；
- 跨 Profile 的 tab：`browser_tab_forbidden`（403）；
- 授权缺少能力：`browser_capability_denied`（403）；
- 非 http/https 网址：`browser_url_invalid`（400）；
- 没有可见 http/https 页面：`browser_no_current_page`（404），面板显示空状态；
- Agent capability 指向其他 Task 的 grant：`browser_grant_forbidden`（403）；
- host 业务操作超过 60s：`browser_host_timeout`（504），与连接失败
  `browser_host_unavailable` 区分。host 的页面导航是 30s，AHA 业务客户端超时
  60s，避免 AHA 先失败而 host 仍在提交导航；启动就绪探针单独使用 5s 客户端
  （每次探测上下文 2s），只约束启动期。
- 崩溃/重启预算跨单次重启保留，只有新 host 稳定运行 60s 后才重置；达到上限
  （默认 5 次）返回 `browser_host_start_failed`，不会无限重启。

host 返回的原始 Playwright 诊断只留在 host 本地日志（替换 profile 根目录和 Edge
路径）；AHA 对 host 错误只使用稳定文案和错误码，不持久化、不返回任意 host 文本。
导航失败时 host 关闭刚创建的 page，避免出现 AHA 无记录但 owner 能看到的孤立页面。

任何失败都不会回退到其他 profile、其他浏览器或静默新建会话。

## 4. 删除语义

资源页沿用现有 owner 确认式删除：Web 先 `window.confirm`，服务端要求
`DELETE ...?confirm=1`，否则返回 `browser_profile_delete_confirmation_required`。
有活跃会话或有效 Task 授权时返回 `browser_profile_in_use`，必须先关闭会话/撤销授权。
删除只移除 AHA 逻辑 Profile，host 用户数据目录不被 AHA 删除，避免误删登录态。

## 5. 审计

`audit_events` 记录 profile 创建/重命名/删除、会话启动/关闭、授权创建/撤销，以及
标签页 open/activate/close/navigate/snapshot、收藏夹读取、lease 获取/释放/转移。
审计记录 profile/session、task、动作、tab id、URL 和结果；Agent actor 形如
`agent:<agent>@<turn>`，因此能追溯到 turn。审计只包含 ID、能力、到期时间、URL 和动作，
不写页面正文、cookie、storage 或 profile 路径。

## 6. 威胁边界与未覆盖风险

边界：

- host 只监听 `127.0.0.1`，所有请求校验 Bearer token；AHA 不向 Web/Task 返回
  host token、CDP 地址或 profile 目录。
- Task 只能操作自己授权内的 profile/session 和 `task_id + grant_id + session_id`
  全部匹配的标签页。
- 登录只在 owner 可见窗口完成；AHA 不保存网站账号密码，不实现自动登录。
- snapshot 只返回渲染文本，不返回 HTML、cookie 或 storage。

未覆盖/残留风险：

- `snapshot` 文本可能包含已授权页面的账号数据；这是业务授权结果，不是
  cookie/storage 导出，但需要技能侧约束。
- URL 校验只限制 `http/https`，不限制目标网段；拿到授权的 Task 可以让宿主访问
  内网地址。POC 默认 Task 与 owner 属于同一信任域，后续需要按技能收紧域名策略。
- POC 使用一个静态 loopback token，不是每 Task 短期 token；token 泄漏可控制整个
  host。后续需要 token 轮换/按 session 发放。
- AHA 与 host 若以同一操作系统用户运行，Task 仍可通过其他本地手段读取 profile
  目录；隐藏 CDP 不等价于 OS 级隔离。
- 登录过期检测、`auth_required` 自动流程、Task 完成自动关闭标签页、远程 host
  注册、headless、Chrome/Chromium、多平台 host 均为后续里程碑。
- profile 删除后 host 目录保留，目前没有受控清理命令。

## 7. 手工验收

真实 Edge 无法在 Linux 构建机运行，因此自动化使用 fake host 覆盖契约；Windows
手工步骤见 `browser-host/README.md` 的「手工验收」。
