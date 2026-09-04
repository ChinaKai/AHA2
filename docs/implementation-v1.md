# AHA2 第一版实现计划

- 日期：2026-09-01
- 目标端口：`0.0.0.0:8766`

## 实现状态

第一版核心闭环已实现并通过自测：

- Go Control Plane、SQLite、Migration
- Owner 初始化、Argon2id、Session、CSRF
- Project、Local/SSH Workspace
- Workspace 与 Codex 检测
- Model Registry、Env Group（Web 主动配置，不依赖旧 AHA 导入）
- Git task branch/worktree
- Task、Turn、Message、Backend Session
- Codex 新 Session 与 Resume
- Stub Backend
- Task Memory、Prompt Pack、Knowledge Candidate
- SSE 实时事件
- TypeScript 桌面端与移动端 Web
- Windows Managed Process 部署
- 同一 Task 只允许一个活跃 Turn，执行中拒绝并发消息和完成操作
- 登录失败限速
- 创建 Task 后自动启动首个 Turn
- 嵌套普通目录不会误识别父级 Git 仓库
- 缺失静态资源返回 404，CSP 不允许 inline script/style

## 模块

```text
cmd/aha
  进程入口、配置、迁移、服务启动

internal/domain
  Project、Workspace、Task、Turn、Session、Knowledge 等实体和状态机

internal/app
  用例编排、Prompt Pack、Turn 生命周期、Knowledge checkpoint

internal/store
  SQLite Schema、Migration、Repository、事务

internal/auth
  Owner 初始化、密码哈希、Session、CSRF

internal/httpapi
  REST API、认证中间件、SSE、静态 Web

internal/workspace
  Local Runner、SSH Runner、Git 和 Backend 检测

internal/backend
  Backend Adapter、Codex、Stub、Session 复用和中断

internal/configimport
  旧 AHA config 导入工具（Web 已不再提供入口；保留包用于离线迁移）

internal/secrets
  本地受限 Secret Store

internal/prompt
  Global KB、Project KB、Task Memory 与当前输入的 Prompt Pack

internal/webassets
  Web 构建产物

web
  TypeScript 响应式 Web
```

## 第一版业务闭环

```text
Owner 初始化 / 登录
-> 主动配置 Model 与 Env Group
-> 创建 Project
-> 创建 Local / SSH Workspace
-> Backend 检测
-> 创建 Task
-> 自动创建并执行首个 Turn
-> 创建独立 Task Turn
-> 注入 Task Memory 与 KB
-> 调用 Codex / Stub
-> 持久化事件和回复
-> 复用 Backend Session
-> 多轮调用
-> 增量 Knowledge Candidate
-> 用户完成 Task
```

## API v1

```text
GET    /healthz

GET    /api/v1/auth/status
POST   /api/v1/auth/register
POST   /api/v1/auth/login
POST   /api/v1/auth/logout

GET    /api/v1/projects
POST   /api/v1/projects

GET    /api/v1/workspaces
POST   /api/v1/workspaces
POST   /api/v1/workspaces/{id}/detect

GET    /api/v1/models
POST   /api/v1/models
GET    /api/v1/env-groups
POST   /api/v1/env-groups

GET    /api/v1/tasks
POST   /api/v1/tasks
GET    /api/v1/tasks/{id}
POST   /api/v1/tasks/{id}/messages
POST   /api/v1/tasks/{id}/complete

POST   /api/v1/turns/{id}/interrupt
GET    /api/v1/tasks/{id}/events

GET    /api/v1/knowledge
POST   /api/v1/knowledge
POST   /api/v1/knowledge/{id}/verify
```

## 验收标准

### 自动化

- 领域状态机测试。
- SQLite Repository 和 Migration 测试。
- Owner 注册、登录、Session、CSRF 测试。
- Local/SSH Runner 参数与超时测试。
- AHA 配置导入工具脱敏测试（保留，但不再接入 Web）。
- Codex JSONL 解析和 Session 复用测试。
- API 未登录拒绝测试。
- Task 多轮 Stub Backend 端到端测试。
- Web TypeScript 构建测试。

### 浏览器

- 1536x1024 桌面视口。
- 390x844 移动视口。
- 登录、项目、配置、创建任务、Task 对话、Knowledge 流程。
- 页面无横向溢出和核心控件遮挡。

### 部署

- 监听 `0.0.0.0:8766`。
- `/healthz` 正常。
- 未登录访问业务 API 返回 `401`。
- 重启服务后 Project、Task、Turn、Session 和 Knowledge 仍可读取。
- Codex Backend 使用 Model 默认 Env Group，API Key 不出现在日志或 API。

## 共享代理

- 全局代理设置只保存一份 `HTTP_PROXY`、`HTTPS_PROXY` 和 `NO_PROXY`，默认指向 `http://127.0.0.1:7897`。
- Task/Agent 的 `proxy_enabled` 固化在不可变 Runtime Config Snapshot 中；开启时才向 Codex 或 Claude Backend 注入共享代理环境变量。
- Codex Account 的 `proxy_enabled` 独立控制 OAuth Token 交换，并随账号保存，浏览器打开授权链接是否使用代理仍由浏览器或系统代理决定。
- 一期仅接受不含凭据的 `http://`、`https://` 代理 URL，不解析订阅或实现代理协议。
