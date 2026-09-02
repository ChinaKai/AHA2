# AHA2

AHA2 是 AHA 的重新设计版本。

它不是对旧 AHA 的一比一复刻，而是以旧 AHA 的真实使用经验为参考，重新建立清晰的产品边界、领域模型和执行架构。

## 产品定义

> AHA 是一个以本地项目为边界、以任务为执行核心、通过知识闭环持续成长的个人 AI 工作流系统。

核心闭环：

```text
Project
-> Workspace
-> Task
-> Turn
-> Task Memory
-> Knowledge
-> Next Turn / Next Task
```

## 当前阶段

当前只进行产品和架构设计，不实现功能。

核心设计基线见：

- [核心设计](docs/core-design.md)
- [Web Demo](design/web-demo/README.md)
- [Web Demo 总览图](design/web-demo/screenshots/00-overview.png)
- [技术选型](docs/technology.md)
- [第一版实现计划](docs/implementation-v1.md)
- [部署说明](docs/deployment.md)

## 第一版运行状态

AHA2 第一版已部署到：

```text
http://127.0.0.1:8766
```

服务监听 `0.0.0.0:8766`，首次初始化 Token 位于：

```text
C:\Users\toope\AppData\Local\AHA2\setup-token
```

## 基本原则

- 新系统从零实现，不直接迁移旧 AHA 代码。
- 旧 AHA 只作为需求样本、行为参考、测试场景和失败案例。
- 先稳定 Project、Workspace、Task、Turn、Session 和 Knowledge，再增加扩展功能。
- Web 是主要产品形态，所有业务数据和操作默认要求认证。
- 控制面与执行面分离，本地和远程 Workspace 使用统一协议。
- 当前源码和真实执行结果高于历史知识。
- 知识在任务过程中持续增长，不依赖任务结束时一次性总结。
