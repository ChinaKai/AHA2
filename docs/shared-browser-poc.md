# 共享浏览器 POC（milestone 1）

日期：2026-11-17
范围：AHA2 浏览器 profile 资源、会话生命周期、Task 授权、Browser host、Web 入口。

## 1. 设计落点（v2）

| 对象 | 实现 |
| --- | --- |
| Profile | `browser_profiles`；只存逻辑 ID、名称、`browser_kind=edge`、`host_id`、状态、`last_used_at`。真实用户数据目录由 host 解析，AHA 不保存、不返回。 |
| Session | `browser_sessions`；属于 profile，不归 Task。`starting/ready/auth_required/busy/closed/error`。 |
| Task 授权 | `browser_task_grants`；`task_id/profile_id/session_id/capabilities/expires_at/revoked_at`。 |
| Tab | `browser_tabs`；带 `task_id/grant_id/session_id`，每次操作同时校验授权、会话与标签页归属。 |
| Host | 独立 Node 进程 `browser-host/host.mjs`，Playwright persistent context + 可见 Edge；仅 loopback + Bearer token。 |

同一 profile 单实例有三层约束：

1. host 的用户数据目录 `.aha2-host.lock`：锁文件带随机 owner token，只有 token
   匹配才允许删除；host 内按 profile 串行化 acquire/release，进程崩溃后按 pid
   回收 stale 锁；
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

Task 授权：

- `POST/GET /api/v1/tasks/{id}/browser-grants`（别名 `/browser-sessions`）。创建时
  supersede + insert 在同一个 immediate SQLite 事务内完成；进程内另有 `grantMu`
  串行化创建/撤销。并发创建始终只保留一个 active grant，重复撤销幂等。
- `GET/DELETE /api/v1/browser-grants/{id}`
- `GET /api/v1/tasks/{id}/browser`：Task 详情合并视图（可选的 profile、当前授权、会话、本 Task 标签页）

标签页操作（为后续 Agent API/技能预留，当前只有受控 Owner API）：

- `POST /api/v1/browser-grants/{id}/tabs`
- `POST /api/v1/browser-grants/{id}/tabs/{tab}/navigate`
- `GET /api/v1/browser-grants/{id}/tabs/{tab}/snapshot`
- `DELETE /api/v1/browser-grants/{id}/tabs/{tab}`

能力集合固定为 `open_tab | navigate | snapshot`；未知能力在创建授权时拒绝。

## 3. 失败关闭

- host 未配置/不可达：`browser_host_unavailable`（HTTP 503）；
- Edge 缺失：`browser_edge_unavailable`（503），不自动换浏览器；
- profile 目录被其他进程占用：`browser_profile_locked`（409）；
- 授权过期/撤销：`browser_grant_expired` / `browser_grant_revoked`（403）；
- 会话已关闭或已换代：`browser_session_closed` / `browser_session_changed`（409）；
- 跨 Task 标签页：`browser_tab_forbidden`（403）；
- 授权缺少能力：`browser_capability_denied`（403）；
- 非 http/https 网址：`browser_url_invalid`（400）；
- host 操作超过 60s：`browser_host_timeout`（504），与连接失败
  `browser_host_unavailable` 区分。host 的页面导航是 30s，AHA 客户端超时 60s，
  避免 AHA 先失败而 host 仍在提交导航。

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
标签页 open/navigate/snapshot/close。审计只包含 ID、能力、到期时间、URL 和动作，
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
