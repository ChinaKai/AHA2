# Sync Center 部署

Sync Center 是多台 AHA2 设备之间的同步服务，只需要部署一份。Release 为 Linux amd64/arm64 提供独立的 DEB 和 RPM，不发布裸二进制。

## 安装

Debian / Ubuntu：

```bash
sudo apt install ./aha2-sync_<version>_amd64.deb
# ARM64 使用 aha2-sync_<version>_arm64.deb
```

Fedora / RHEL：

```bash
sudo dnf install ./aha2-sync-<version>-1.x86_64.rpm
# ARM64 使用 aha2-sync-<version>-1.aarch64.rpm
```

安装包会创建 `aha2-sync` 系统账号，注册并启动 `aha2-sync.service`。默认配置为：

```text
监听：127.0.0.1:8770
配置：/etc/aha2-sync/aha2-sync.env
数据库：/var/lib/aha2-sync/sync.db
```

可用 `systemctl status aha2-sync.service` 检查服务。修改配置后执行：

```bash
sudo systemctl restart aha2-sync.service
```

## HTTPS

远程 AHA2 客户端只接受 HTTPS 的 Sync Center 地址。保持 Sync Center 监听 loopback，并使用 Nginx、Caddy 或其他反向代理将 HTTPS 请求转发到 `http://127.0.0.1:8770`。TLS 证书和公网或局域网防火墙由服务器管理员配置。

## 注册设备

在服务器上生成一次性注册码。注册码默认 24 小时有效，生成新码会使此前未使用的注册码失效：

```bash
sudo -u aha2-sync /usr/bin/aha-sync --db /var/lib/aha2-sync/sync.db \
  --generate-registration-code /var/lib/aha2-sync/registration-code
sudo cat /var/lib/aha2-sync/registration-code
sudo rm /var/lib/aha2-sync/registration-code
```

不要把注册码写入日志、脚本或版本库。在 AHA2 Web 的“同步”页面填写：

1. Sync Center 的 HTTPS 地址和设备名称。
2. 尚未注册时填写一次性注册码。
3. 至少 12 位的加密口令；所有设备必须使用同一口令。

保存后点击“立即同步”。注册码只用于首次换取设备凭据；设备 Sync Token 保存在本机且不会跨设备。Provider、Env Secret、Codex、SSH 和硬件凭据会使用共同口令端到端加密；SSH/硬件只作为远端只读镜像，必须显式接管并重新绑定本机路径、串口或网络端点后才能使用。

## 升级与卸载

直接用新版本包升级即可，安装脚本会重启服务。管理员修改过的配置会保留，数据库不会被覆盖。

```bash
sudo apt remove aha2-sync
# 或 sudo dnf remove aha2-sync
```

卸载会停止并禁用服务，但保留 `/var/lib/aha2-sync` 和系统账号。确认不再需要同步数据后，由管理员备份并手动删除数据目录。
