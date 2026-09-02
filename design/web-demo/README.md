# AHA2 Web Demo

静态 Web 原型通过查询参数切换页面：

```text
index.html?screen=login
index.html?screen=projects
index.html?screen=settings
index.html?screen=new-task
index.html?screen=task
index.html?screen=knowledge
```

页面范围：

1. 登录
2. 项目主页
3. 模型、Env Group、Workspace 与 Backend 检测
4. 创建任务
5. Task 多轮执行
6. Global / Project 知识库

导出的图片位于 `screenshots/`。

## 截图

- `screenshots/01-login.png`
- `screenshots/02-projects.png`
- `screenshots/03-settings.png`
- `screenshots/04-new-task.png`
- `screenshots/05-task.png`
- `screenshots/06-knowledge.png`
- `screenshots/00-overview.png`

截图尺寸为 `1536x1024`。页面使用同一套浅色运营工具视觉系统，重点表达：

- 公网 Web 的认证入口
- Project 与 Local/Remote Workspace
- 本地 Model Registry、Env Group 与 Workspace Backend 检测
- Git branch/worktree 隔离
- Task、Turn、Session、Task Memory 和 KB 注入
- Global / Project Knowledge 的持续增长
