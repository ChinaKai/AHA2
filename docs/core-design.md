# AHA2 核心设计

- 状态：Draft
- 日期：2026-09-01
- 目标：定义 AHA2 的产品边界、核心领域模型、主要工作流和第一版范围

## 1. 产品定义

> AHA 是一个以本地项目为边界、以任务为执行核心、通过知识闭环持续成长的个人 AI 工作流系统。

AHA 面向个人长期使用。它不是单纯的 Agent 启动器，也不是聊天 UI，而是围绕项目持续组织任务、执行、证据、记忆和知识的工作系统。

核心价值：

1. 以 Project 组织长期工作。
2. 以 Task 表达用户真实目标。
3. 通过 Codex、Claude 等 Backend 执行任务。
4. 保留可恢复、可追踪的多轮工作状态。
5. 在任务进行过程中持续提炼知识。
6. 让已验证知识指导后续 Turn 和 Task。

核心闭环：

```text
Project
-> Workspace
-> Task
-> Turn
-> Evidence
-> Task Memory
-> Knowledge
-> Next Turn / Next Task
```

## 2. 设计原则

### 2.1 领域概念不能互相代替

必须明确区分：

```text
Project != Workspace
Task != Round
Task != Turn
Agent != Backend
Agent Status != Turn Status
Turn Status != Process Status
Backend Session != Backend Process
Task Memory != Knowledge Base
Git Remote != Remote Workspace
```

### 2.2 控制面与执行面分离

AHA 本地服务是控制面：

- 用户认证与会话
- Project、Task、Turn 管理
- Model、Env Group 管理
- Task Memory 和 Knowledge Base
- 调度、状态和审计
- Web/API

Workspace 是执行面：

- 文件读写
- Git 操作
- 命令执行
- Backend CLI
- Backend Process
- 构建、测试和工具调用

### 2.3 默认拒绝和显式副作用

- 未认证请求默认拒绝。
- Backend 不可用时禁止静默切换。
- Workspace、Backend、Model 或 Env Group 变化必须显式记录。
- 合并、推送、强制重置、删除和部署等高风险操作必须显式确认。
- 所有副作用必须有稳定 Command ID，并支持幂等处理。

### 2.4 知识是有证据的指导，不是聊天归档

- 当前用户要求优先级最高。
- 当前 Workspace 源码和真实执行结果高于历史知识。
- 未验证知识不能直接成为跨任务权威指导。
- 知识必须记录来源、适用范围、证据、版本和置信度。

## 3. 总体架构

```text
Browser
  |
HTTPS
  |
Authentication / Authorization
  |
AHA Control Plane
  |- Application Services
  |- Domain Model
  |- Command Bus
  |- Event Store
  |- Query Projections
  |- Task Memory
  |- Knowledge Base
  |- Model Registry
  `- Audit Log
       |
       | WorkspaceRunner protocol
       |
Execution Plane
  |- Local Runner
  |- WSL Runner
  |- SSH Remote Runner
  `- Future Container Runner
```

推荐代码依赖方向：

```text
Web / API
    |
Application
    |
Domain
    ^
Ports
    ^
Infrastructure
```

Domain 不允许依赖 HTTP、数据库、操作系统路径、Codex 或 Claude。

## 4. 核心领域对象

### 4.1 User

代表 AHA 的使用者。第一版只支持单 Owner，但所有数据保留 `user_id`。

```text
User
|- user_id
|- display_name
|- status
|- created_at
`- last_login_at
```

相关对象：

- Credential
- Session
- Recovery Method
- Audit Event

### 4.2 Project

Project 是逻辑项目身份，不等同于某个可变路径。

```text
Project
|- project_id
|- owner_id
|- name
|- description
|- project_type
|- repository_identity
|- default_workspace_id
|- default_branch
|- created_at
`- updated_at
```

Project 可以来自：

- Git 仓库
- 普通文件夹
- 远程目录
- 未来的其他项目来源

一个 Project 可以关联多个 Workspace。

### 4.3 Workspace

Workspace 是 Project 的一个物理工作目录或 checkout。

```text
Workspace
|- workspace_id
|- project_id
|- name
|- locality             local | remote
|- transport            native | wsl | ssh | container
|- root_path
|- connection_id
|- platform
|- capabilities
|- repository_state
|- health
`- last_detected_at
```

示例：

```text
Project: AHA
|- local-dev       E:\kk-workspace\AHA
|- local-wsl       /home/user/AHA
|- remote-test     server-a:/srv/AHA
`- remote-prod     server-b:/opt/AHA
```

### 4.4 Task

Task 表示用户针对 Project 提出的长期目标。一个 Task 可以经历多轮用户交互和多个 Agent Turn。

```text
Task
|- task_id
|- project_id
|- workspace_id
|- original_request
|- current_goal
|- status
|- target_branch
|- base_commit
|- task_branch
|- task_workspace_id
|- runtime_config_snapshot_id
|- created_at
|- updated_at
`- completed_at
```

Task 状态：

```text
draft
-> preparing
-> active
-> waiting_user
-> completed

preparing / active
-> blocked
-> active

preparing / active / waiting_user
-> failed
-> cancelled
```

一次 Agent 回复成功不等于 Task 完成。

推荐完成流程：

```text
Turn succeeded
-> Task waiting_user
-> User confirms completion
-> Task completed
```

### 4.5 Turn

Turn 是一条输入触发的一次 Agent 执行，必须是独立持久化对象。

```text
Turn
|- turn_id
|- task_id
|- agent_id
|- sequence
|- input_message_id
|- status
|- waiting_reason
|- backend_session_id
|- runtime_config_snapshot_id
|- queued_at
|- started_at
|- finished_at
|- exit_code
`- result_artifact_id
```

Turn 状态：

```text
queued
-> preparing
-> starting
-> running
-> waiting
-> succeeded

queued / preparing / starting / running / waiting
-> failed
-> interrupted
-> blocked
```

等待原因：

```text
waiting_user
waiting_subagents
waiting_host
waiting_approval
waiting_external
waiting_backend
```

排队耗时、启动耗时、执行耗时和等待耗时分别记录，不能压缩成一个模糊的 elapsed。

### 4.6 Agent

Agent 是 Task 内的逻辑执行者。

第一版只实现 Main Agent，但保留扩展模型：

```text
Agent
|- agent_id
|- task_id
|- role
|- status
|- backend
|- model
|- permissions
`- current_turn_id
```

一个 Agent 同时只能有一个 Active Turn。

### 4.7 Backend Provider 与 Installation

Backend Provider 是 Codex、Claude 等逻辑适配器。

Backend Installation 表示某个 Workspace 中实际存在的 CLI：

```text
BackendInstallation
|- installation_id
|- workspace_id
|- backend
|- executable
|- version
|- status
|- capabilities
|- detected_at
`- error
```

检测状态：

```text
unknown
detecting
unavailable
installed
authentication_required
ready
degraded
incompatible
error
```

检测失败不能自动安装，也不能静默切换到其他 Backend。

### 4.8 Model Registry

模型配置由本地 AHA 控制面统一管理，远端 Workspace 不维护独立模型配置。

```text
Model
|- model_id
|- display_name
|- provider_id
|- backend_compatibility
|- wire_model
|- context_window
|- max_output_tokens
|- capabilities
`- default_reasoning_effort
```

### 4.9 Env Group

Env Group 表示一套 Provider 执行环境。

```text
EnvGroup
|- env_group_id
|- provider_id
|- base_url
|- api_type
|- static_environment
|- secret_refs
|- model_bindings
|- revision
`- updated_at
```

本地 AHA 是 Env Group 的权威来源。

远端执行时，AHA 将解析后的环境按允许列表注入 Backend Process。默认不在远端永久写入 `.env`。

认证模式保留扩展：

```text
env_group
remote_login
credential_file
```

### 4.10 Runtime Config Snapshot

Task 创建时保存不可变运行配置快照：

```text
RuntimeConfigSnapshot
|- snapshot_id
|- workspace_id
|- backend
|- backend_version
|- model_id
|- wire_model
|- env_group_id
|- env_group_revision
|- reasoning_effort
|- permissions
`- created_at
```

全局配置变化不能静默改变正在执行的 Task。

### 4.11 Backend Session

Backend Session 是 Agent 与 Provider 的持久会话。

复用身份：

```text
task_id
+ agent_id
+ workspace_id
+ backend
+ model_id
+ env_group_revision
```

关键配置变化后必须生成 handoff summary，再创建新 Session。

```text
BackendSession
|- backend_session_id
|- task_id
|- agent_id
|- backend
|- workspace_id
|- model_id
|- env_group_revision
|- provider_session_id
|- status
|- context_usage
|- created_at
`- last_used_at
```

### 4.12 Backend Process

Backend Process 是执行某次 Turn 的操作系统进程。

```text
BackendProcess
|- process_id
|- turn_id
|- workspace_id
|- backend_session_id
|- native_pid
|- status
|- started_at
|- stopped_at
`- exit_code
```

Process 状态不能直接作为 Agent 或 Turn 状态。

### 4.13 Task Memory

Task Memory 是当前 Task 的持久工作记忆：

- 当前目标
- 需求变化
- 用户决策
- 已确认事实
- 已排除方向
- 当前进度
- 验证结果
- 未完成事项
- Knowledge Candidate

Task Memory 必须跨 Turn、上下文压缩、Backend 切换和 AHA 重启保留。

### 4.14 Knowledge Entry

知识至少存在两个正式作用域：

```text
global
project
```

另有 Task Memory 作为尚未正式提升的任务作用域记忆。

知识类型：

```text
navigation
practice
decision
solution
constraint
worklog
```

统一数据模型：

```text
KnowledgeEntry
|- knowledge_id
|- scope                  global | project
|- project_id
|- type
|- title
|- body
|- status
|- applicability
|- branch_scope
|- source_task_ids
|- source_turn_ids
|- source_files
|- evidence
|- confidence
|- created_at
|- updated_at
`- last_verified_at
```

知识状态：

```text
observed
-> candidate
-> verified
-> stale
-> deprecated
```

当前 Task 产生的 Candidate 可以立即作为 Task Memory 使用，但未验证 Candidate 默认不能指导其他 Task。

### 4.15 Artifact

Artifact 是 Prompt、结果、日志、命令输出、附件、图片、Diff、Summary 等产物。

```text
Artifact
|- artifact_id
|- task_id
|- turn_id
|- type
|- storage
|- path_or_ref
|- content_type
|- checksum
|- size
`- created_at
```

### 4.16 Command、Event 与 Audit

Command 表示希望发生的操作，Event 表示已经发生的事实。

```text
Command
|- command_id
|- user_id
|- type
|- target
|- payload
|- status
`- idempotency_key

Event
|- event_id
|- aggregate_type
|- aggregate_id
|- type
|- data
|- sequence
`- occurred_at
```

Audit Event 记录安全和高风险操作，不能与普通 UI 日志混为一体。

## 5. Project、Workspace 与 Git

### 5.1 Git 项目隔离

修改型 Task 默认使用独立 branch 和 worktree：

```text
Primary Workspace
|- user working tree
`- Task Worktrees
   |- aha/task-001
   |- aha/task-002
   `- product-b/aha/task-003
```

规则：

- AHA 不自动切换用户 Primary Workspace 的分支。
- 不同写 Task 不共享同一个 worktree。
- 同一 Task 的所有 Turn 使用同一个 Task Workspace。
- 分析型只读 Task 可以不创建分支。
- Task 完成不等于分支自动合并。
- 合并、推送、强制更新和删除必须显式执行。

### 5.2 分支作用域知识

未合并分支上的知识不能立即成为整个 Project 的权威知识。

```text
Task Candidate
-> Branch-scoped Knowledge
-> Merge Validation
-> Project Shared Knowledge
```

分支被删除、回退或实现被撤销时，对应知识需要标记 stale 或 deprecated。

### 5.3 Task Workspace 迁移

Task 创建后默认固定 Workspace。

切换 Workspace 必须执行显式迁移：

```text
Freeze current Turn
-> Capture Git and dirty state
-> Persist Task Memory
-> Generate Session Handoff
-> Prepare target Workspace
-> Transfer or reconstruct source state
-> Create new Runtime Snapshot
-> Resume Task
```

禁止因切换 Backend 而隐式切换 Workspace。

## 6. Workspace Runner

业务层不能直接使用本地文件路径操作 Workspace，必须通过统一 Runner：

```text
WorkspaceRunner
|- inspect()
|- health()
|- stat()
|- list_files()
|- read_file()
|- write_file()
|- run_command()
|- start_process()
|- stop_process()
|- git_status()
|- create_branch()
|- create_worktree()
|- collect_artifact()
`- cleanup()
```

第一版实现：

- Local Native Runner
- SSH Remote Runner

后续实现：

- WSL Runner
- Container Runner
- Persistent Remote Agent Runner

远端第一版支持两种模式：

```text
attach
managed_clone
```

第一版不实现本地与远端目录的自动双向同步。

## 7. Backend 检测

Backend 检测属于 Workspace Capability Discovery。

流程：

```text
Connect Workspace
-> Detect platform and shell
-> Locate backend executable
-> Read version
-> Check adapter compatibility
-> Detect runtime capabilities
-> Resolve local Env Group
-> Validate selected model
-> Optional explicit smoke test
```

检测分层：

1. 静态安装检测
2. 版本和协议能力检测
3. 认证配置验证
4. 模型连通性验证
5. 用户明确触发的真实 Smoke Test

检测结果按 `workspace_id + backend` 缓存，并具有 TTL 和显式刷新能力。

## 8. 认证与授权

AHA2 的产品形态是公网可访问的 Web。

默认规则：

> 除登录和最小健康检查外，所有页面、API、WebSocket、Artifact 和 Workspace 操作均要求认证。

第一版采用单 Owner：

```text
First Start
-> One-time Setup Token
-> Create Owner
-> Configure Credential
-> Disable public registration
-> Login only
```

会话使用服务端持久化 Session 和安全 Cookie。

禁止：

- 在 URL 中传长期 Token
- 将 Session Token 保存到 localStorage
- 未校验 Origin 的 WebSocket
- 允许匿名读取 Project、Task、KB、版本和路径

高风险操作即使已经登录仍需二次确认，必要时重新认证：

- 任意终端和 Shell
- 删除 Workspace
- Git 强制重置和强制推送
- 合并、发布和部署
- 修改密钥、Env Group 和远程连接
- 重启和升级 AHA

## 9. 完整工作流

### 9.1 系统初始化

```text
Register Owner / Login
-> Configure Provider
-> Configure Env Group
-> Configure Model Registry
-> Register Workspace
```

### 9.2 Workspace 准备

```text
Configure Local / Remote Workspace
-> Detect connection
-> Detect platform
-> Detect Git
-> Detect Backend installations
-> Cache capabilities and health
```

### 9.3 创建 Task

```text
Enter task information
-> Select Project
-> Select Workspace
-> Select target branch
-> Select Backend
-> Select Model
-> Select Env Group
-> Validate runtime configuration
-> Create Runtime Config Snapshot
-> Create Task
-> Prepare task branch and worktree
```

### 9.4 Prompt Pack

Prompt Pack 使用稳定结构：

```text
1. AHA system policy
2. Agent identity and permissions
3. Project / Workspace / Git context
4. Original task request
5. Current task goal
6. Task Memory
7. Relevant Global KB
8. Relevant Project KB
9. Current user message
10. Artifact references
11. Output, memory and knowledge feedback protocol
```

### 9.5 Turn 循环

```text
Receive user message
-> Persist message
-> Update current goal
-> Create Turn
-> Load Task Memory
-> Retrieve Global KB
-> Retrieve Project KB
-> Filter applicable knowledge
-> Build Prompt Pack
-> Resolve or reuse Backend Session
-> Inject Env Group into Workspace process
-> Start Agent Turn
-> Stream events and tool actions
-> Persist reply and Artifacts
-> Update Task Memory
-> Upsert Knowledge Candidates
-> Set Task waiting_user
-> Wait for next user message
```

知识更新发生在 Turn 内的重要节点和 Turn checkpoint，而不是只在 Task 完成时执行。

### 9.6 多轮 Session 复用

复用条件：

```text
same task
same agent
same workspace
same backend
compatible model
same env group revision
resumable provider session
```

Session 不可恢复或关键配置变化时：

```text
Persist Task Memory
-> Generate Handoff Summary
-> Archive old Session
-> Create new Session
-> Continue Task
```

### 9.7 Task 结束

```text
Agent provides result
-> Verify goal and evidence
-> User confirms completion
-> Save Task Result
-> Final Task Memory checkpoint
-> Reconcile Knowledge Candidates
-> Record branch and dirty state
-> User chooses merge / keep / delete branch
-> Archive Backend Session
-> Complete Task
```

Task 可以重新打开。原 Session 可恢复时继续使用，否则根据 Task Memory 和 Handoff Summary 创建新 Session。

## 10. 状态恢复与并发

### 10.1 持久化优先

所有外部副作用必须遵守：

```text
Persist Command
-> Commit state
-> Execute side effect
-> Persist Event and result
```

Web 服务重启后根据持久化状态恢复，不依赖内存中的 worker 状态。

### 10.2 并发规则

- 一个 Agent 同时只能有一个 Active Turn。
- 同一 Task 默认串行处理用户 Turn。
- 执行中收到新用户消息时，只能显式排队或中断当前 Turn。
- 不同 Task 使用独立 Workspace 时可以并行。
- 多 Agent 编排不属于第一版实现范围。

## 11. 查询与 UI

UI 不根据零散事件猜测核心状态。

后端维护明确的查询投影：

- Project Summary
- Workspace Health
- Backend Availability
- Task Detail
- Current Turn
- Session State
- Task Memory
- Knowledge Candidates
- Git State
- Audit History

Turn 卡片直接读取持久化 Turn Projection：

```text
phase
result
queued_duration
startup_duration
execution_duration
waiting_duration
waiting_reason
backend
model
session
```

## 12. 第一版范围

第一版必须实现：

- 单 Owner 注册和登录
- Project
- Local Workspace
- SSH Remote Workspace
- Git 检测
- 独立 Task branch/worktree
- Backend Adapter
- Codex Backend
- Stub Backend
- Model Registry
- Env Group
- Backend 检测
- Task、Turn 和 Session
- Task Memory
- Global / Project Knowledge
- Prompt Pack
- 增量 Knowledge Candidate
- Event、Artifact 和 Audit
- Web UI
- 服务重启恢复

第一版暂不实现：

- 多用户协作
- 多 Agent 编排
- 飞书和微信
- Hardware
- Browser Bridge
- 自动双向文件同步
- 自动部署
- 自动合并和推送
- 复杂插件市场
- Windows Tray 和开机启动

## 13. 信任顺序

Agent 执行时的信息优先级：

```text
Current user request
> Current Workspace source and command results
> Current Task Memory
> Current branch Project Knowledge
> Shared Project Knowledge
> Global AHA Knowledge
> General model knowledge
```

知识是可质疑、可更新、可废弃的指导，不是不可覆盖的系统真理。

## 14. 待定设计决策

以下事项进入实现前需要继续确认：

1. Task 是否必须由用户明确 Complete，还是允许规则驱动自动完成。
2. Project Knowledge Candidate 的自动验证条件。
3. Global Knowledge 的提升权限和审核方式。
4. 普通非 Git Workspace 的并发隔离方式。
5. SSH Remote Runner 的凭据管理和连接复用。
6. Artifact 默认拉回本地还是允许只保留远程引用。
7. Task Workspace 的默认保留期限。
8. Backend Session 的压缩、归档和过期策略。
9. Passkey、密码和外部 OIDC 的第一版优先级。
10. Event Store 与业务数据库采用同库还是分离存储。

## 15. 下一步

设计阶段下一步输出：

1. 领域关系图。
2. 各对象状态机。
3. Command / Event 清单。
4. Workspace Runner 接口。
5. Backend Adapter 接口。
6. 数据库 Schema。
7. Web 页面信息架构。
8. 第一阶段可执行开发计划。

## 16. Web 一级导航一致性

项目、任务、知识库、模型、提示词以及后续新增的渠道等一级模块，必须共用同一个应用导航壳层：

- 桌面端始终保留左侧主导航。
- 移动端始终保留底部主导航。
- 一级模块的页面函数只返回模块内容，不得自行创建或替换全局导航。
- 全局 `shell()` 只能由 Web 主入口统一调用，避免模块页面覆盖侧栏、Owner 状态和移动导航。
- 新增一级模块时，必须同时注册桌面导航、移动导航和主视图映射，并验证切换前后导航 DOM 保持同一结构。
- 登录页属于认证边界，可以不使用应用导航壳层；进入应用后的业务模块不得例外。

提示词模块是普通一级业务模块，只负责查看、编辑和恢复当前 AHA2 使用的提示词模板。Prompt 的运行时选择和组装属于内部执行逻辑，不在 Web 中提供路由、有效 Prompt 或诊断管理页面。
