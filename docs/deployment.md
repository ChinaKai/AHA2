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

## 构建产物

`dist/` 包含：

```text
aha2-linux-amd64
aha2-linux-arm64
aha2-windows-amd64.exe
aha2-windows-arm64.exe
aha2-darwin-amd64
aha2-darwin-arm64
```

## 当前边界

- 第一版支持单 Owner。
- Backend 优先支持 Codex，同时保留 Stub 自测 Backend。
- Remote Workspace 使用系统 OpenSSH，目标 WSL/Unix 主机需提前配置 SSH Key。
- 当前 WSL 首次 Host Key 使用 `accept-new`，后续严格校验已保存指纹。
- 自动合并、自动推送、多 Agent 和公网 TLS 终止不在第一版范围。
