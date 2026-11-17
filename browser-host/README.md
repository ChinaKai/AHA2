# AHA2 共享浏览器 Host（POC）

AHA2 的交互浏览器必须运行在 owner 的真实登录桌面会话里。Windows 服务默认在
Session 0，不能可靠显示窗口；因此本目录提供的是一个**独立进程**：

- AHA2 控制面只通过 `127.0.0.1` 上的 token API 操作浏览器；
- host 自己管理 Edge 用户数据目录、单实例锁和可见窗口；
- AHA2 数据库只保存逻辑 profile ID，不知道真实目录；
- host 不向 AHA/Task/局域网暴露 CDP 端口，也不导出 cookie/storage。

## 前置条件

- Windows 10/11 桌面会话；
- Microsoft Edge 已安装（POC 只支持 `browser_kind=edge`，启动失败不会换
  Chrome/Chromium）；
- Node.js 20+；
- 在 `browser-host/` 下安装依赖：

```powershell
cd browser-host
npm install
```

## 启动

```powershell
$env:AHA2_BROWSER_HOST_TOKEN = "<随机长 token>"
$env:AHA2_BROWSER_PROFILE_ROOT = "D:\ProgramData\AHA2\browser-profiles"
$env:AHA2_BROWSER_HOST_ID = "local"
node host.mjs --port 8765
```

同时给 AHA2 进程设置**同一个** token。推荐用只允许 owner 读取的 token 文件，
避免 token 进入 AHA 子进程环境：

```powershell
$env:AHA2_BROWSER_HOST_URL = "http://127.0.0.1:8765"
$env:AHA2_BROWSER_HOST_TOKEN_FILE = "D:\ProgramData\AHA2\browser-host.token"
$env:AHA2_BROWSER_HOST_ID = "local"
```

`AHA2_BROWSER_HOST_TOKEN` 直接传值也可用，但 AHA2 启动后会立即从自身环境删除它，
避免 Task 子进程继承。host 侧同样支持 `--token-file <path>`。token 缺失时 host
拒绝启动；host 只监听 `127.0.0.1`，没有监听地址参数。日志不会打印 token。

可选的 Edge 路径覆盖：

```powershell
$env:AHA2_EDGE_PATH = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
```

profile 根目录建议放在 AHA2 数据目录下的独立子目录，并把 ACL 限制为 owner。
host 为每个 profile 创建 `0700` 子目录和 `.aha2-host.lock` 锁文件。

## HTTP 契约（host 侧）

所有请求（含健康检查）都需要 `Authorization: Bearer <token>`。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/v1/health` | `host_id`、`platform`、`edge_available`、活跃 profile 列表 |
| POST | `/v1/profiles/{id}/session` | 启动或复用持久化 Edge 窗口（`{"browser_kind":"edge"}`） |
| GET | `/v1/profiles/{id}/session` | 当前会话状态；没有则 404 `browser_session_not_found` |
| DELETE | `/v1/profiles/{id}/session` | 关闭窗口并释放目录锁 |
| POST | `/v1/profiles/{id}/tabs` | 打开 Task 标签页（`{"session_id","url"}`） |
| POST | `/v1/profiles/{id}/tabs/{tab}/navigate` | 导航（`{"session_id","url"}`） |
| GET | `/v1/profiles/{id}/tabs/{tab}/snapshot?session_id=` | 返回 `{url,title,text}`，不含 HTML/cookie |
| DELETE | `/v1/profiles/{id}/tabs/{tab}?session_id=` | 关闭标签页 |

错误统一为 `{"error":{"code","message"}}`。重点错误码：

- `browser_host_unavailable` / `browser_host_unauthorized`：host 不在或 token 不符；
- `browser_edge_unavailable`：找不到 Edge；
- `browser_profile_locked`：用户数据目录被另一个进程占用；
- `browser_session_not_found` / `browser_session_changed`：窗口已关闭或会话已换代；
- `browser_tab_not_found`：标签页不存在或已关闭。

## 手工验收（Windows + Edge）

本仓库的 Linux 构建机无法运行真实 Edge，因此以下步骤必须由 owner 在浏览器
主机上手工执行，不能当作已自动验证：

1. 按上面命令启动 host，确认日志只有 `listening on http://127.0.0.1:8765`，
   没有 token；`GET /v1/health` 返回 `edge_available:true`。
2. 启动 AHA2（带相同的 `AHA2_BROWSER_HOST_URL/TOKEN`），在资源页创建
   「工作浏览器」profile。
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
8. 关闭会话后，两个 Task 的授权都变为已撤销，操作返回
   `browser_session_closed` / `browser_grant_revoked`。
9. 停掉 host 再打开登录窗口：AHA 明确返回 `browser_host_unavailable`，
   不会换 profile 或浏览器。

## 已知边界（POC）

- 使用一个共享 loopback token，不是每个 Task 的短期 token；尚未实现 token 轮换。
- 不检测登录过期；`auth_required` 状态与登录检测属于后续里程碑。
- 不做远程 host 注册、不实现 headless、不支持 Chrome/Chromium。
- 标签页快照只返回渲染文本，仍可能包含已授权页面上的账号数据；这是“Task 不使用
  cookie/storage 导出”而不是“Agent 看不到页面内容”。
- profile 删除只删 AHA 逻辑记录，host 用户数据目录会保留，需后续提供受控清理。
