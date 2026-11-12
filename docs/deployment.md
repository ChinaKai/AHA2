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

## Windows per-user 开发安装与自动部署

频繁本地开发使用 `AHA2-Setup-User-x64.exe`。它以最低权限安装到当前用户的
`LOCALAPPDATA\Programs\AHA2`，默认数据目录为 `LOCALAPPDATA\AHA2`，只监听 loopback，
继续使用唯一的 `AHA2 User` 登录任务，不创建服务或防火墙规则。安装和后续升级均不请求 UAC。

从已有机器级安装迁移时，可在首次 per-user 安装中显式复用用户已有写权限的数据目录；安装器会
把唯一登录任务切换到用户级二进制。旧机器级文件不会被无权限删除，可在确认新实例健康后单独执行
一次管理员卸载。后续开发部署使用 `scripts/deploy-windows-user.ps1`，脚本会备份运行数据和旧用户级
二进制、构建 per-user 安装包、升级、重启并核对健康与哈希。

需要从源码完成完整验证、构建、打包、升级和重启时，使用单一入口，避免手工重复探测工具链或重复执行测试：

```powershell
powershell.exe -ExecutionPolicy Bypass -File scripts\build-and-deploy-windows-user.ps1 `
  -Version 0.5.19 `
  -DataDir D:\ProgramData\AHA2 `
  -Listen 0.0.0.0:8766 `
  -HealthURL http://127.0.0.1:8766/healthz
```

脚本只复用仓库现有的 WSL Go 工具链，自动读取真实发行版名称并预检磁盘空间；Web 与 Go 验证在同一轮各执行一次。已有已验证候选时传入 `-DeployOnly`，只执行安装、重启、健康和哈希校验。

### 部署失败路径与结果字段

`deploy-windows-user.ps1` 在停止 AHA2、复制备份之前先做磁盘空间预检，按每个涉及的卷分别检查：
数据目录所在卷需要 `aha2.db`、`aha2.db-wal`、`aha2.db-shm` 当前大小的 2 倍（备份复制加 WAL 增长和
临时文件余量）再加 1GiB 安全余量；安装目录所在卷预留 200MB；临时目录（安装包暂存）所在卷预留 300MB，
但临时目录只在安装器分支检查，`-UpdateExistingInstallInPlace` 在目标目录旁暂存，不要求临时卷空间。
同一卷上的需求合并计算，任何一卷不足都会直接失败。结果 JSON 记录 `Stage=preflight`、
`Volume`/`FreeBytes`/`RequiredBytes`（数据卷）和 `Volumes`（每个卷的角色、可用与需求），
**不会停止 AHA2，也不会复制或替换任何文件**；外层 `build-and-deploy-windows-user.ps1` 和
`local_build.sh` 会按非 0 退出码原样报错。空间足够时逐卷记录 Free/Required 日志；后续流程与之前一致。

在替换任何已安装文件或数据库前，还必须先“验证已停”：重新枚举
`aha2.exe`、`aha2-tray.exe`、`aha2-channel-feishu.exe` 及其全部后端子进程，确认进程消失后
再以独占方式打开 `aha2.exe`、`aha2.db`、WAL/SHM 等文件做探测；失败会按 `-StopTimeoutSeconds`
（默认 45 秒）重试，超时则拒绝继续修改，而不是边跑边复制。进程枚举或进程身份查询失败时按“未验证/
仍存活”处理，绝不把 WMI/CIM 错误当成“没有进程”。备份只在这个验证通过后执行，
并对每个复制结果校验大小与 SHA256。失败回滚同样先验证停止，再用“同目录临时文件 + 哈希校验 +
`[IO.File]::Replace` 原子替换”恢复二进制、`aha2.db`/`aha2.db-wal` 以及备份中的
`secrets.json`/`setup-token`；部署前不存在的文件（例如首次安装的 Feishu 插件与清单）在回滚时删除。
数据库恢复前**无条件**删除失败版本可能留下的 `aha2.db-wal` 和 `aha2.db-shm`（即使备份里没有 WAL），
再按备份内容原子恢复，最后对数据库执行 `PRAGMA quick_check`；只有返回 `ok` 才会重新启动服务。
回滚无法验证时保持 AHA2 停止、保留备份，并在结果中输出人工恢复步骤（同样要求先移走
`aha2.db`/`aha2.db-wal`/`aha2.db-shm` 再复制备份）。

结果 JSON 中的诊断字段：

- `Stage`：失败时最后执行的阶段（`preflight`/`stop`/`backup`/`install`/`health`/`rollback`）；成功时为 `health`。
- `Volume`/`FreeBytes`/`RequiredBytes`：数据卷及其可用/需求字节；`Volumes`：每个涉及的卷（data/install/temp）的角色、可用与需求明细。预检失败时都写入结果 JSON。
- `FailedStage`：最初触发失败的阶段；`LockedFiles`：无法独占、无法原子替换或恢复后校验不通过的文件。
- `BackupSHA256`/`BackupSizes`/`BackupVerified`：备份校验结果；`StopVerified`/`StopAttempts`：停止验证结果与重试次数。
- `RollbackVerified`：回滚的二进制与数据库是否已原子恢复且 `quick_check` 通过。
- `rollback_health`：回滚与恢复后启动的完整结论；`ManualRecovery`：需要人工恢复时的步骤与备份校验值。

结果文件是诊断产物：写入是 best-effort，原子替换失败（目标被锁、文件系统不支持等）时会退回直接写入，
仍失败则只记录告警，不会因为写结果失败而中断或回滚一次已经成功的部署。

`-SelfTest` 只检查纯函数、恢复计划和临时目录中的原子替换，不读取或修改安装目录、进程、计划任务和数据库，
可在任意 PowerShell 7 上运行；`scripts/test-windows-installer.ps1` 也会在 Windows 上执行它。

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

本地 Agent 构建并部署时使用选中的 `aha2-local-build` Skill：先构建 staging 二进制，
再由宿主 AHA `managed-process` 启动独立部署 Worker；Worker 停止服务、备份运行数据与旧二进制、替换并重启，最后
检查 `http://127.0.0.1:8766/healthz`；失败时恢复旧二进制。AHA2 自身的 Agent API
只适合托管普通 Task 进程，不能用于安全地重启 AHA2 自身。

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

本地仅校验安装器定义、不生成二进制：

```powershell
.\scripts\build-windows-installer.ps1 -ValidateOnly
```

仓库的全平台本地交叉构建会同时生成 Windows server 与 GUI tray；tray 使用
`-H windowsgui`，因此从登录任务启动时不会附加控制台窗口：

```bash
bash scripts/build-all.sh
```

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
