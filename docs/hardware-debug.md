# Hardware Debug

## 目标

AHA2 Hardware V1 为 Task 提供统一的串口和网络终端。设计参考 AHA1，但按
AHA2 的 SQLite、Secret Store、Task 工具面板和 Go Runtime 重新实现，不复制
AHA1 的 Python 子进程与 JSONL control inbox。

## 数据边界

一个 Task 最多保存 16 个同构硬件组：

```text
id
description
mode = off | serial | network | both
serial.device
serial.baudrate
network.host
network.port
network.protocol = telnet | raw | ssh
network.ssh_auth = auto | password | key
username
password -> Secret Store
access = read_only | read_write
```

硬件组和终端 I/O 元数据保存在 SQLite。密码仅以
`hardware/<task-id>/<hardware-id>/credential` 引用保存到 `secrets.json`，
不会进入 Hardware API、Audit、日志、Prompt 或 `hardware.md`。

## Runtime

`internal/hardware.Manager` 按物理端点复用连接：

- Serial key：设备名，例如 `COM3` 或 `/dev/ttyUSB0`。
- Network key：协议、主机和端口。
- 同一端点配置一致时允许多个 Task attachment 共享。
- 最后一个 attachment 断开时关闭真实连接。
- 服务关闭时统一关闭全部连接。

Serial：

- Windows 使用 `CreateFile`、DCB 和 COM timeout。
- Linux 使用原生 TTY、raw mode、关闭软件/硬件流控。
- 串口列表来自 Windows Registry 或 `/dev/tty*`。

Network：

- `raw` 为普通 TCP 字节流。
- `telnet` 处理 IAC negotiation、TTYPE、NAWS 和 BINARY。
- `ssh` 使用 `golang.org/x/crypto/ssh` 建立 `xterm-256color` PTY 和交互 Shell，
  默认端口 22。
- 配置用户名/密码后，只在检测到 `login:`/`username:`/`password:` 时自动发送。
- 密码发送记录固定显示为 `<password>`。
- Agent 可调用显式 `login` API，按设备覆盖提示、换行、唤醒、超时和重试；提示匹配
  使用跨读取块缓冲，Terminal status 返回登录状态。

SSH 支持密码、keyboard-interactive 和用户目录中未加密的
`~/.ssh/id_ed25519`、`id_ecdsa`、`id_rsa`。主机密钥必须匹配
`~/.ssh/known_hosts`。首次连接时 Web 展示主机、算法和 SHA256 指纹；Owner
确认后服务端再次探测并核对指纹，再写入信任库并重试连接。主机密钥发生变化时
仍会拒绝连接，不允许直接覆盖或跳过校验。

SSH 登录方式：

- `auto`：已配置密码时先尝试密码和 keyboard-interactive，再回退本机 Key。
- `password`：仅允许已保存的密码。
- `key`：仅允许用户目录中的未加密私钥。

## 权限

- `read_only`：允许查看配置、连接、历史和 RX，禁止 TX。
- `read_write`：允许文本或 HEX 发送。
- Completed、Failed、Cancelled Task 强制只读。
- Network 仅支持文本；HEX 仅支持 Serial。
- 单次发送上限 64 KiB。

权限同时在 HTTP handler 和 Runtime attachment 两层检查，不能只依赖前端禁用。

## API

```text
GET  /api/v1/hardware/serial-ports
GET  /api/v1/tasks/{task}/hardware
PUT  /api/v1/tasks/{task}/hardware
GET  /api/v1/tasks/{task}/hardware/{hardware}/terminal?transport=serial|network
POST /api/v1/tasks/{task}/hardware/{hardware}/connect?transport=serial|network
GET  /api/v1/tasks/{task}/hardware/{hardware}/host-key?transport=network
POST /api/v1/tasks/{task}/hardware/{hardware}/host-key/trust?transport=network
POST /api/v1/tasks/{task}/hardware/{hardware}/disconnect?transport=serial|network
POST /api/v1/tasks/{task}/hardware/{hardware}/send?transport=serial|network
```

实时终端使用受认证 WebSocket：

```text
GET /api/v1/tasks/{task}/hardware/{hardware}/terminal/ws?transport=serial|network
```

- Binary WebSocket frame：原始终端输入或输出字节。
- Text WebSocket frame：`ready/status/error/resize/close` 控制消息。
- 初次连接先回放最近 1000 条 RX，再按持久化 sequence 接续 live output，避免回放与实时交界重复。
- 输入按约 16ms 合并，单帧上限 64 KiB。

Terminal HTTP GET 继续支持 `after=<sequence>` 增量读取，作为历史查询、状态同步和
WebSocket 不可用时的兜底。

## Prompt Context

配置了至少一个硬件组时，每个 Turn 的 Available Context 增加：

```text
.aha2-context/<task>/<agent>/hardware.md
```

该文件包含硬件组描述、连接方式、权限、用户名和
`password configured: true|false`，不包含密码或 Secret 引用。

Agent Turn 同时获得 `agent-api.md`、`AHA2_AGENT_API_URL` 和仅存于进程环境的
`AHA2_AGENT_API_TOKEN`。Agent 使用 Bearer Token 调用 `/api/v1/agent/hardware`
命名空间；服务端从 capability 绑定 Task，不接受客户端指定 Task ID。Token 不写入
Context、SQLite、Event 或日志，并在 Turn 结束时撤销。该 API 只提供列表、连接、
断开、历史和 TX，不允许 Agent 修改硬件配置。

Agent 还可调用：

```text
POST /api/v1/agent/hardware/{hardware}/login?transport=serial|network
```

## Web

Task 标题栏 Hardware 图标继续使用统一的全尺寸工具面板，终端内核为
xterm.js 6.0.0：

- 桌面：左侧连接配置，右侧终端。
- 移动端：连接配置默认折叠，终端占主视图。
- 支持硬件组增删、Serial/Network 切换、连接/断开、清空显示、
  文本/HEX、CRLF 和增量历史。
- xterm 直接处理 ANSI、光标、键盘、IME、方向键、Ctrl 组合键和窗口 resize；
  Serial/Telnet/SSH 共用同一 WebSocket 协议。
- 黑色 xterm 区域是主输入入口；底部文本/HEX 表单仅用于粘贴、批量命令和原始
  HEX 辅助发送。
- 移动端提供 Enter、Esc、Tab、Ctrl+C、Left、Up、Down、Right 快捷键栏。
- xterm 依赖运行时 inline style 定位 canvas、光标和 IME，因此 CSP 仅对
  `style-src` 允许 `'unsafe-inline'`；`script-src` 仍严格保持 `'self'`。

Go WebSocket 使用固定 vendor 的 `coder/websocket v1.8.14`。xterm.js 和
coder/websocket 的许可证分别保存在 `web/vendor/xterm.LICENSE` 和
`third_party/coder-websocket/LICENSE.txt`。

## V1 不包含

- 串口文件传输
- Armed rules
- 继电器、刷写、复位等板级协议
- 串口 owner takeover
- Agent Hardware WebSocket（Agent 先使用 HTTP 增量历史）

这些能力应在基础连接稳定后通过 Skills 和受控工具逐项加入，不能在通用硬件层
猜测设备协议或默认执行状态变更。
