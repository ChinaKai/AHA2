# AHA2

AHA2 是一个以本地项目为边界、以任务为执行核心、通过知识闭环持续成长的个人 AI 工作流系统。

```text
Project → Workspace → Task → Turn → Task Memory → Knowledge → Next Turn / Task
```

## 安装

从 [GitHub Releases](https://github.com/ChinaKai/AHA2/releases) 下载对应安装包，并使用同一 Release 中的 `SHA256SUMS` 校验文件。

### Windows

运行 `AHA2-Setup-x64.exe`。安装向导可选择程序目录、数据目录、监听 IP、端口和局域网防火墙规则，安装后自动注册并启动 Windows 服务。

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

同步需要一个可访问的 AHA Sync Center、该中心生成的一次性注册码，以及所有设备一致的加密口令。

1. 打开 Web 的“同步”页面并启用同步。
2. 填写 HTTPS 中心地址、设备名称、同步间隔和一次性注册码。
3. 设置至少 12 位的加密口令；其他设备必须填写相同口令。
4. 按需勾选要同步的 Provider、Env Secret 和 Codex 账号凭据。
5. 保存设置并点击“立即同步”，随后在同一页面检查推送、拉取状态和冲突。

项目、任务、知识库、模型和 Agent 配置会按依赖顺序同步。设备访问凭据由注册流程生成并仅保存在本机；敏感凭据只同步显式选中的条目，并使用上述口令端到端加密。

## 文档

- [核心设计](docs/core-design.md)
- [部署说明](docs/deployment.md)
- [Linux 打包与配置](docs/linux-packaging.md)
- [Agent API](docs/agent-api.md)
