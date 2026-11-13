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

## Windows 机器级安装（登录用户运行模式）

GitHub Release 提供机器级安装包 `AHA2-Setup-x64.exe`。安装包需要管理员权限，
程序默认安装到 `Program Files\AHA2`。
应由最终运行 AHA2 的 Windows 登录用户直接双击安装包，让安装器自行请求 UAC；不要从已提权
终端启动，也不要使用另一个管理员账号“运行方式”启动，否则 Windows 无法可靠保留原始用户令牌。
安装向导允许选择数据目录，默认值为：

```text
C:\ProgramData\AHA2
```

数据目录必须位于本机磁盘；SQLite 数据目录不支持映射盘或 UNC 网络共享。安装器从登录计划任务
读取原始登录用户身份，只向该用户授予所选目录的递归修改权限。管理员提权仅用于写入
`Program Files`、设置该目录权限、配置防火墙和清理遗留服务，AHA2 进程本身不以管理员或
`LocalSystem` 身份运行。

安装向导还允许选择 HTTP 端口和访问范围：

- 仅本机：监听 `127.0.0.1`，默认且不创建防火墙规则。
- 局域网：可监听 `0.0.0.0` 或指定网卡 IPv4；可显式选择创建 Windows 防火墙规则。

安装器创建的防火墙规则只适用于 Private profile、TCP 目标端口和本地子网。安装包在
`Program Files\AHA2` 同时安装 `aha2.exe` 和 GUI 子系统的 `aha2-tray.exe`。安装程序通过原始
登录用户进程注册唯一的 `AHA2 User` Windows 登录计划任务；任务使用交互式、最低权限的用户
令牌，在安装完成后以及该用户每次登录时直接启动：

```text
aha2-tray.exe --server "<Program Files>\AHA2\aha2.exe" --listen "<IP>:<端口>" --data-dir "<数据目录>"
```

`aha2-tray.exe` 自身使用 Windows GUI 子系统，不创建控制台窗口；它负责启动、监控和停止
`aha2.exe serve`。计划任务 action 直接指向 tray，不长期依赖 PowerShell、Windows Script Host
或 VBS launcher。PowerShell 仅在安装期间用于注册任务和设置数据目录 ACL。

tray 会把 `aha2.exe serve` 的 stdout/stderr 持久化到 `<数据目录>\logs\aha2-server.log`，
并在其中记录每次启动的命令行、PID、退出码或启动错误；单个文件上限 10 MB，超出后把当前文件
轮转为 `aha2-server.log.1`（最多保留两份）。Go panic、runtime fatal error 和崩溃退出信息因此
不会随隐藏窗口消失。日志只包含进程输出和启动参数，凭据校验拒绝带用户信息、query 或 fragment
的 Agent API URL，因此不会写入请求体或密码。

仅使用本机 Workspace 时，Agent API 地址可留空，AHA2 会使用 loopback 地址。SSH 远程
Workspace 默认通过既有 SSH 连接建立反向隧道，不需要在安装器中填写地址。只有 AHA2 位于
无法建立隧道的反向代理之后时，才需要提供 HTTPS 基址：

```text
--agent-api-url https://<AHA2-host>
```

非 loopback 明文 HTTP 地址无法配置，也不再提供 `--allow-insecure-agent-api`。这不会启用
`--allow-cross-origin`；浏览器 Origin 校验仍保持开启。
该启动参数是 Agent API 地址的初始默认值；安装后可在“高级设置 → 其他设置”中修改排障用的
手动覆盖，普通 Workspace 会显示自动选择结果。特殊 NAT、跳板机、HTTPS 反向代理环境仍可
用该隐藏覆盖排除故障。

此登录用户模式是 Windows 安装器的短期正式默认方案。安装器会停止、禁用并尝试删除旧版本
遗留的 `AHA2` LocalSystem 服务；若 Windows 暂时不能删除服务，保留 Stopped/Disabled 状态也
不会在重启后抢占端口。

升级安装会记住上次的数据目录、访问范围、IP、端口和防火墙选项，也允许重新选择这些值及安装
路径。升级先禁用并结束唯一计划任务，停止所有已安装版本的 `aha2-tray.exe` 与 `aha2.exe`，再以
`Register-ScheduledTask -Force` 覆盖同名任务的程序路径和完整参数，因此不会保留第二个任务或并行
实例。遗留 `AHA2` LocalSystem 服务在任何迁移结果下都保持 Stopped/Disabled；新 tray 健康后安装器
尽力删除服务，暂时删除失败也不会恢复其启动能力。若安装在切换完成前失败，安装器只尝试恢复升级
前的用户任务/tray。卸载会先删除登录任务，再停止 tray/server、删除开始菜单快捷方式、遗留服务及安装器创建的防火墙
规则，但保留所选数据目录，避免误删数据库、Secret Store 和 Setup Token；确认不再需要时应由
管理员另行备份并删除。

登录用户模式不使用 Windows Service Control Manager。GUI tray 保持 AHA2 控制面进程不可见，
计划任务在 tray 异常退出后以 1 分钟间隔最多
重试 3 次；仍未恢复时，可从开始菜单选择“启动 AHA2”，或在该用户下次登录时自动启动。用户
注销后 AHA2 会随该登录会话结束，不提供未登录状态下的后台服务。

完成页可以选择打开 `http://127.0.0.1:8766`。Release 中的统一 `SHA256SUMS`
可用于校验下载完整性。

## Windows per-user 安装与构建出包

频繁本地开发使用 `AHA2-Setup-User-x64.exe`。它以最低权限安装到当前用户的
`LOCALAPPDATA\Programs\AHA2`，默认数据目录为 `LOCALAPPDATA\AHA2`，只监听 loopback，
继续使用唯一的 `AHA2 User` 登录任务，不创建服务或防火墙规则。安装和后续升级均不请求 UAC。

从已有机器级安装迁移时，可在首次 per-user 安装中显式复用用户已有写权限的数据目录；安装器会
把唯一登录任务切换到用户级二进制。旧机器级文件不会被无权限删除，可在确认新实例健康后单独执行
一次管理员卸载。

### 职责边界

仓库只负责编译出包。远端的构建机把应用代码、Web 资源和飞书插件编译成 Windows 包，产物里
只有可执行文件和清单，不带任何部署逻辑。预检、停止 AHA2、备份运行数据和旧二进制、替换、
回滚、health 检查以及备份保留，全部由目标机器上的本机安装器负责。仓库里的构建脚本不读取
安装目录、不碰运行数据、也不重启任何进程。

旧版本随仓库下发的 operator 部署脚本已移除，不再作为受支持的入口，也不随 tar 包分发；
替代方案是“构建机出包 + 本机安装器部署”。这样部署步骤不再受 Windows PowerShell 编码和
执行策略影响，仓库和安装器的职责也不再混在一起。

### 构建 Windows 包

在构建机上执行：

```bash
./local_build.sh --version 0.5.23
# 等价于
bash scripts/build-windows-package.sh --version 0.5.23 --output-dir dist
```

`local_build.sh` 只是 `scripts/build-windows-package.sh` 的薄包装；两者都只构建，没有部署模式。
`--version` 缺省时依次取 `VERSION` 环境变量、最近的 git tag、`0.0.0`；也可用 `--output-dir`
把包写到仓库外的干净目录，用 `--commit` 覆盖清单里的 commit。

构建产物：

```text
<output-dir>/aha2-windows-amd64.exe
<output-dir>/aha2-tray-windows-amd64.exe
<output-dir>/plugins/feishu/windows-amd64/aha2-channel-feishu.exe
<output-dir>/plugins/feishu/windows-amd64/plugin.json
<output-dir>/release-manifest.json
```

`release-manifest.json` 由构建步骤生成，记录 `version`、`commit`、`built_at`、生成方式
（脚本、命令行、Web/Go/Node 版本）以及每个产物文件的相对路径、大小和 SHA256。它只是产物
清单，不含部署步骤，也不包含自身。构建脚本写清单前会检查三个 PE 文件的 `MZ` 头、飞书
清单里的打包占位符已被真实哈希替换，以及四个输入文件都存在；缺一项就直接失败，不会产出
不完整的包。

Agent API stdio helper 不再需要单独构建或安装：它是一个只用 Node 标准库的单文件脚本，通过
`go:embed` 打在 `aha2.exe` 里，运行时才上传到工作区 `.aha2-context/share/agent-proxy/`。因此
构建链路不产出 `dist/agent-proxy`，安装包也不带按平台的 helper；Windows 上的 AHA2 服务 Linux
工作区时，只要工作区有可用的 node（backend 本身是 node 脚本时由 shebang 保证）。旧版本安装到
`{app}\agent-proxy` 的预编译 helper 会在升级安装时删除。

### PowerShell 脚本编码

会被 Windows PowerShell 5.1 执行的仓库内脚本（`scripts/*.ps1` 与 `installer/windows/*.ps1`）必须保存为
**UTF-8 with BOM**（文件头 `EF BB BF`）。5.1 会把无 BOM 的文件按系统 ANSI 代码页解析；中文字符串
会变成乱码，并可能在第一条语句执行前就抛 ParserError。打包和下载链路（包括 `tar`）必须原样保留
BOM，不得重新编码或裁剪文件头。`scripts/test-windows-installer.ps1` 会检查每个脚本的 BOM 并用
PowerShell Parser 解析；`scripts/ps1_encoding_test.go` 在非 Windows CI 上检查 BOM 和 tar 往返。

### 部署归属与失败处理

部署不再是仓库的一部分。目标机器上的本机安装器按顺序负责：

- 预检：工具链、安装目录可写，以及数据目录和安装目录所在卷的可用空间。
- 停服：停止 `aha2-tray.exe`、`aha2.exe`、飞书插件及其后端子进程，确认退出后再替换文件。
- 备份：备份运行数据（`aha2.db` 与 `aha2.db-wal`；`aha2.db-shm` 是易失缓存，不备份）、
  Secret Store、Setup Token、旧二进制和原登录任务，并在备份后校验大小与 SHA256。
- 替换与回滚：原子替换二进制和数据库；失败时恢复备份，无法验证时保持服务停止并输出人工恢复步骤。
- health 与备份保留：启动后检查 `/healthz`，再按保留策略清理旧备份。

部署过程中的阶段、结果 JSON、日志和人工恢复步骤都由本机安装器生成和保存，不在本仓库内。
旧版本的部署结果 JSON 只作为历史记录，不再由本仓库产出。

## macOS 机器级安装

macOS 标准安装包按架构提供：

```text
AHA2-macos-amd64.pkg
AHA2-macos-arm64.pkg
```

安装包需要管理员权限，将程序安装为 `/usr/local/bin/aha2`，并注册
`/Library/LaunchDaemons/com.aha2.controlplane.plist`。launchd 默认执行：

```text
/usr/local/bin/aha2 serve --listen 127.0.0.1:8766 --data-dir "/Library/Application Support/AHA2"
```

服务配置启用 `RunAtLoad` 和 `KeepAlive`。升级前安装脚本会先从 system launchd domain
卸载旧 job，文件安装完成后再执行 `bootstrap` 与 `kickstart`。持久数据位于
`/Library/Application Support/AHA2`，安装和卸载脚本都不会主动删除该目录。

管理员卸载：

```bash
sudo /usr/local/share/aha2/uninstall.sh
```

卸载脚本会停止并 bootout launchd job，删除程序和 plist，并保留持久数据。

在非 macOS 环境只执行静态验证：

```bash
./scripts/test-macos-packages.sh
```

在 macOS 上分别构建两个架构的包：

```bash
./scripts/build-macos-packages.sh --version v0.1.0 \
  --input-exe ./dist/aha2-darwin-amd64 --arch amd64 --output-dir ./dist
./scripts/build-macos-packages.sh --version v0.1.0 \
  --input-exe ./dist/aha2-darwin-arm64 --arch arm64 --output-dir ./dist
```

如已配置 Developer ID Installer 证书，可额外传入
`--signing-identity`；未提供时正常产出未签名包。脚本不会打印签名凭据。

首次安装后通过管理员终端读取 Owner Setup Token：

```bash
sudo cat "/Library/Application Support/AHA2/setup-token"
```

## Linux 系统安装

Debian/Ubuntu 提供 amd64、arm64 `.deb`，Fedora/RHEL 提供 x86_64、aarch64 `.rpm`。
安装包注册 `aha2.service`，使用专用 `aha2` 系统账号，配置文件位于
`/etc/aha2/aha2.env`，持久数据位于 `/var/lib/aha2`。详细安装、配置与卸载方法见
[`docs/linux-packaging.md`](linux-packaging.md)。

## 首次 Owner 初始化

首次启动会在所选数据目录生成 `setup-token`，用于首次创建 Owner，并在忘记密码时验证本机所有权。它是数据文件，不是程序安装目录中的文件。Windows 机器级安装默认位于：

```text
C:\ProgramData\AHA2\setup-token
```

打开登录页，选择初始化 Owner，输入：

- Setup Token
- Owner 用户名
- 至少 10 位密码

Owner 创建后公开注册自动关闭；Setup Token 只保留用于登录页的本机密码恢复。该文件等同于恢复凭据，应继续限制为管理员和 AHA2 运行用户可读。

如果安装时自定义了数据目录，应以 `AHA2 User` 计划任务参数中的实际 `--data-dir` 为准。密码恢复失败时，不要使用程序安装目录中遗留的同名文件；文件被替换或恢复后，还需重启 AHA2 才会重新加载其内容。

## 数据目录

```text
C:\ProgramData\AHA2
```

默认目录的写权限应只授予：

- 当前 Windows 用户
- `NT AUTHORITY\SYSTEM`
- 本机 Administrators

包含：

- `aha2.db`
- `secrets.json`
- `setup-token`

Secret 值不会通过 API、Web、Event 或日志返回。

## Reverse proxy and embedded WebView compatibility

Origin host validation is enabled by default. Reverse proxies should preserve the browser-facing
host as the upstream `Host`; in multi-level proxy deployments, the trusted inner proxy may take the
first `X-Forwarded-Host` value supplied by the outer proxy and overwrite upstream `Host` with it.
WebSocket deployments must also forward `Upgrade` and `Connection`.

高级设置中的“校验浏览器请求 Origin”默认开启。可信反向代理确实无法保留外部 Host 时，可先从
本机地址登录，在高级设置中关闭校验；设置会持久化并立即作用于登录、注册、密码恢复、认证写入
和硬件 WebSocket。关闭校验不会关闭 Session 认证或 CSRF Token 校验，但仍应优先修复反向代理的
`Host` / `X-Forwarded-Host` 转发。

`--allow-cross-origin`（或 `AHA2_ALLOW_CROSS_ORIGIN=1`）仍可在启动时强制关闭 Origin 校验，主要用于
受控诊断。启用该启动参数时，高级设置会显示锁定状态；如需重新启用校验，必须移除参数并重启。

## Managed Process

状态：

```powershell
python C:\Users\toope\AppData\Local\AHA\aha managed-process status aha2-v1
```

停止：

```powershell
python C:\Users\toope\AppData\Local\AHA\aha managed-process stop aha2-v1
```

AHA2 的自更新不由仓库脚本负责：构建机只产出 Windows 包和 `release-manifest.json`，目标机器上的
本机安装器负责停止、备份、替换、回滚和 health 检查。仓库不再提供随包下发的部署入口，也不托管
重启自身的 Worker。AHA2 自身的 Agent API 只适合托管普通 Task 进程，不能用于安全地重启 AHA2 自身。

远程 Workspace 的 Agent API 地址由 AHA 自动选择（SSH 优先走反向隧道），通常无需配置。
仅有反向代理等无法自动推断的场景需要给出 HTTPS 基址：

```text
--agent-api-url https://<AHA2-host>
```

非 loopback 的 `http://` URL 不再支持，`--allow-insecure-agent-api` 已移除；旧服务定义中
残留的该参数会被忽略，不会阻止启动。

## 构建产物

Release 只包含标准安装包，不发布裸二进制。`aha2-sync` 是单独部署在 Linux
服务器上的同步中心，不需要安装到每台客户端：

```text
AHA2-Setup-x64.exe
AHA2-Setup-User-x64.exe
aha2_<version>_amd64.deb
aha2_<version>_arm64.deb
aha2-<version>-1.x86_64.rpm
aha2-<version>-1.aarch64.rpm
aha2-sync_<version>_amd64.deb
aha2-sync_<version>_arm64.deb
aha2-sync-<version>-1.x86_64.rpm
aha2-sync-<version>-1.aarch64.rpm
AHA2-macos-amd64.pkg
AHA2-macos-arm64.pkg
SHA256SUMS
```

打包过程仍会生成临时目标平台二进制，但只作为安装包输入，不上传到 Release。

构建机交付给本机安装器的是 Windows 包，由 `scripts/build-windows-package.sh` 生成，包含
`aha2-windows-amd64.exe`、`aha2-tray-windows-amd64.exe`、飞书插件目录和
`release-manifest.json`。包的构建和清单格式见“Windows per-user 安装与构建出包”。

本地仅校验安装器定义、不生成二进制：

```powershell
.\scripts\build-windows-installer.ps1 -ValidateOnly
```

仓库的全平台本地交叉构建会同时生成 Windows server 与 GUI tray；tray 使用
`-H windowsgui`，因此从登录任务启动时不会附加控制台窗口：

```bash
bash scripts/build-all.sh
```

该脚本不再产出预编译的 Agent API stdio helper：helper 是内嵌在 `aha2.exe` 里的 Node 脚本，
安装包不含 `agent-proxy/` 目录。

已有 Windows amd64 控制面和 tray 程序且安装了 Inno Setup 6 时，可构建安装包：

```powershell
.\scripts\build-windows-installer.ps1 `
  -InputExe .\dist\aha2-windows-amd64.exe `
  -InputTrayExe .\dist\aha2-tray-windows-amd64.exe `
  -OutputDir .\dist\installer `
  -Version 0.1.0
```

## 当前边界

- 第一版支持单 Owner。
- Backend 优先支持 Codex，同时保留 Stub 自测 Backend。
- Remote Workspace 通过 SSH 连接目标主机，首次检测会展示并要求确认主机指纹；支持 WSL/Unix 的 `sh` 与 Windows OpenSSH 的 PowerShell 执行环境。
- 当前 WSL 首次 Host Key 使用 `accept-new`，后续严格校验已保存指纹。
- 自动合并、自动推送、多 Agent 和公网 TLS 终止不在第一版范围。
