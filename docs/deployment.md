# AHA2 第一版部署

- 部署日期：2026-09-01
- 监听地址：`0.0.0.0:8766`
- Managed Process：`aha2-v1`
- 版本：`0.1.0`
- 当前 Windows 运行产物：`dist/aha2-windows-amd64-v0.1.0-r3.exe`

## 访问

本机：

```text
http://127.0.0.1:8766
```

局域网：

```text
http://<Windows 主机 IP>:8766
```

公网部署必须在 AHA2 前增加 HTTPS 反向代理，不应直接公开明文 HTTP。

## Windows 机器级安装

GitHub Release 同时提供便携版 `aha2-windows-amd64.exe` 与机器级安装包
`AHA2-Setup-x64.exe`。安装包需要管理员权限，安装到 `Program Files\AHA2`，并创建：

```text
C:\ProgramData\AHA2
```

安装程序将 `AHA2` 注册为 delayed-auto Windows 服务，服务命令固定为：

```text
aha2.exe service run --listen 127.0.0.1:8766 --data-dir C:\ProgramData\AHA2
```

服务异常退出时由 Windows Service Control Manager 依次在 5 秒、15 秒和 60 秒后重启。
升级安装会先停止服务，替换程序后重新配置并启动服务。卸载会停止并删除服务，但默认保留
`C:\ProgramData\AHA2`，避免误删数据库、Secret Store 和 Setup Token；确认不再需要时应由管理员另行备份并删除。

完成页可以选择打开 `http://127.0.0.1:8766`。安装包旁的
`AHA2-Setup-x64.exe.sha256` 可用于校验下载完整性。

## 首次 Owner 初始化

首次启动生成一次性 Setup Token：

```text
C:\Users\toope\AppData\Local\AHA2\setup-token
```

打开登录页，选择初始化 Owner，输入：

- Setup Token
- Owner 用户名
- 至少 10 位密码

Owner 创建后公开注册自动关闭，Setup Token 不再有效。

## 数据目录

```text
C:\Users\toope\AppData\Local\AHA2
```

该目录位于 NTFS，只授权：

- 当前 Windows 用户
- `NT AUTHORITY\SYSTEM`

包含：

- `aha2.db`
- `secrets.json`
- `setup-token`

Secret 值不会通过 API、Web、Event 或日志返回。

## Reverse proxy and embedded WebView compatibility

Origin host validation is enabled by default. When a reverse proxy or embedded
WebView cannot preserve a same-origin `Host` header, start AHA2 with:

```text
--allow-cross-origin
```

The equivalent environment variable is `AHA2_ALLOW_CROSS_ORIGIN=1`. This
disables Origin host validation for login, registration, and authenticated
write requests. Authenticated write requests still require the session CSRF
token.

## Managed Process

状态：

```powershell
python C:\Users\toope\AppData\Local\AHA\aha managed-process status aha2-v1
```

停止：

```powershell
python C:\Users\toope\AppData\Local\AHA\aha managed-process stop aha2-v1
```

本地 Agent 构建并部署时使用选中的 `aha2-local-build` Skill：先构建 staging 二进制，
再由宿主 AHA `managed-process` 启动独立部署 Worker；Worker 停止服务、备份运行数据与旧二进制、替换并重启，最后
检查 `http://127.0.0.1:8766/healthz`；失败时恢复旧二进制。AHA2 自身的 Agent API
只适合托管普通 Task 进程，不能用于安全地重启 AHA2 自身。

远程 Workspace 使用 Agent API 时还需在启动参数中配置可达基址：

```text
--agent-api-url https://<AHA2-host>
```

非 loopback 的 `http://` URL 默认拒绝启动。只允许在受信开发网络中临时追加：

```text
--allow-insecure-agent-api
```

## 构建产物

`dist/` 包含：

```text
aha2-linux-amd64
aha2-linux-arm64
aha2-windows-amd64.exe
aha2-windows-arm64.exe
aha2-darwin-amd64
aha2-darwin-arm64
AHA2-Setup-x64.exe
AHA2-Setup-x64.exe.sha256
```

本地仅校验安装器定义、不生成二进制：

```powershell
.\scripts\build-windows-installer.ps1 -ValidateOnly
```

已有 Windows amd64 主程序且安装了 Inno Setup 6 时，可构建安装包：

```powershell
.\scripts\build-windows-installer.ps1 `
  -InputExe .\dist\aha2-windows-amd64.exe `
  -OutputDir .\dist\installer `
  -Version 0.1.0
```

## 当前边界

- 第一版支持单 Owner。
- Backend 优先支持 Codex，同时保留 Stub 自测 Backend。
- Remote Workspace 使用系统 OpenSSH，目标 WSL/Unix 主机需提前配置 SSH Key。
- 当前 WSL 首次 Host Key 使用 `accept-new`，后续严格校验已保存指纹。
- 自动合并、自动推送、多 Agent 和公网 TLS 终止不在第一版范围。
