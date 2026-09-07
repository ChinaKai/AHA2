# AHA2

AHA2 是一个以本地项目为边界、以任务为执行核心、通过知识闭环持续成长的个人 AI 工作流系统。

```text
Project → Workspace → Task → Turn → Task Memory → Knowledge → Next Turn / Task
```

## 安装

从 [GitHub Releases](https://github.com/ChinaKai/AHA2/releases) 下载对应安装包，并使用同一 Release 中的 `SHA256SUMS` 校验文件。

### Windows

运行 `AHA2-Setup-x64.exe`。安装包同时安装 `aha2.exe` 控制面与 GUI 子系统的 `aha2-tray.exe`。安装向导可选择程序目录、数据目录、监听 IP、端口、远程 Agent API 地址和局域网防火墙规则；唯一的 `AHA2 User` 登录计划任务会以原登录用户的最低权限直接启动托盘程序，由托盘管理控制面进程，从而继承该用户的 Git、Codex、Claude、WSL 与 SSH 环境。旧版 LocalSystem 服务会保持禁用并尽力删除。

### Debian / Ubuntu

```bash
sudo apt install ./aha2_<version>_amd64.deb
# ARM64 使用 aha2_<version>_arm64.deb
```

### Fedora / RHEL

```bash
sudo dnf install ./aha2-<version>-1.x86_64.rpm
# ARM64 使用 aha2-<version>-1.aarch64.rpm
```

Linux 安装后由 systemd 自动启动。监听地址和数据目录可在 `/etc/aha2/aha2.env` 中修改。

### macOS

按设备架构运行 `AHA2-macos-amd64.pkg` 或 `AHA2-macos-arm64.pkg`。安装需要管理员权限，完成后自动注册并启动 launchd 服务。

### 首次初始化

默认打开 `http://127.0.0.1:8766`，使用数据目录中的一次性 `setup-token` 创建 Owner：

- Windows：安装时选择的数据目录，默认为 `C:\ProgramData\AHA2\setup-token`
- Linux：`sudo cat /var/lib/aha2/setup-token`
- macOS：`sudo cat "/Library/Application Support/AHA2/setup-token"`

完整安装、升级和卸载说明见 [部署文档](docs/deployment.md)。

## 设置同步

先在一台 Linux 服务器安装独立的 Sync Center（不要与每台设备的 AHA2 客户端包混淆）：

```bash
sudo apt install ./aha2-sync_<version>_amd64.deb
# Fedora/RHEL：sudo dnf install ./aha2-sync-<version>-1.x86_64.rpm
```

服务默认监听 `127.0.0.1:8770`。通过 HTTPS 反向代理对外提供服务后，生成 24 小时内有效的一次性注册码：

```bash
sudo -u aha2-sync /usr/bin/aha-sync --db /var/lib/aha2-sync/sync.db \
  --generate-registration-code /var/lib/aha2-sync/registration-code
sudo cat /var/lib/aha2-sync/registration-code
sudo rm /var/lib/aha2-sync/registration-code
```

然后在每台 AHA2 设备中：

1. 打开 Web 的“同步”页面并启用同步。
2. 填写 HTTPS 中心地址、设备名称、同步间隔和一次性注册码。
3. 设置至少 12 位的加密口令；其他设备必须填写相同口令。
4. 保存设置并点击“立即同步”，随后在同一页面检查推送、删除和冲突。

项目、任务、知识库、模型和 Agent 配置会按依赖顺序同步，删除也会传播。Provider、Env Secret、Codex、SSH 和硬件凭据使用共同口令端到端加密；SSH/硬件只作为远端只读镜像，接管到本机时必须重新确认路径、串口或网络端点。每台设备的 Sync Token 始终独立且不会同步。

中心配置、升级、卸载与反向代理说明见 [Sync Center 部署](docs/sync-center.md)。

## 文档

- [核心设计](docs/core-design.md)
- [部署说明](docs/deployment.md)
- [Linux 打包与配置](docs/linux-packaging.md)
- [Sync Center 部署](docs/sync-center.md)
- [Agent API](docs/agent-api.md)
