# AHA2 技术选型

- 状态：Accepted for v1
- 日期：2026-09-01

## 结论

```text
Control Plane / API / Runner: Go 1.27
Web: TypeScript
Database: SQLite
Realtime: Server-Sent Events
Remote transport: OpenSSH
Backend v1: Codex + Stub
```

## 选择理由

### Go

- 为 Windows、Linux、macOS 生成原生程序。
- Control Plane 与 Remote Runner 共用类型和协议。
- 适合 HTTP、长运行服务、并发 Turn 和子进程管理。
- Web 构建产物可随服务一起发布。
- 第一版禁止引入 CGO 依赖，保持交叉编译能力。

### TypeScript

- Web 是 AHA2 的唯一正式交互形态。
- Project、Task、Turn、Backend 和 Knowledge 等状态需要共享明确类型。
- 桌面端与移动端使用同一套组件和 API Client。

### SQLite

- AHA2 是个人、本地优先的单节点控制面。
- Task、Turn、Session、Knowledge、Command、Event 和 Audit 需要事务一致性。
- 使用 WAL、外键和迁移版本，不使用 ORM 隐式管理 Schema。

### SSE

- 第一版实时链路主要是服务端向浏览器推送 Task/Turn/Event。
- 用户操作继续使用普通 HTTP Command。
- 后续需要双向终端时，再为 Terminal 单独增加 WebSocket。

## 发布目标

```text
linux/amd64
linux/arm64
windows/amd64
windows/arm64
darwin/amd64
darwin/arm64
```

当前开发工具链安装在：

```text
WSL: /mnt/e/kk-workspace/AHA2/.tools/go
```

工具链不属于发布产物。

## 安全边界

- API Key 从当前 AHA 配置导入到 AHA2 Secret Store。
- Secret 不进入普通配置 API、事件、日志和 Artifact。
- Env Group API 只返回 `secret_configured`。
- 所有业务 API、SSE 和静态应用页面都要求登录。
- 唯一匿名入口是最小健康检查和首次 Owner 初始化接口。
