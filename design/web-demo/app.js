(() => {
  const app = document.getElementById("app");
  const params = new URLSearchParams(window.location.search);
  const screen = params.get("screen") || "projects";

  const icon = (name, cls = "") => `<i data-lucide="${name}"${cls ? ` class="${cls}"` : ""}></i>`;

  const navItems = [
    ["projects", "folder-kanban", "项目", "3"],
    ["tasks", "list-checks", "任务", "7"],
    ["knowledge", "book-open-text", "知识库", "24"],
    ["settings", "settings-2", "设置", ""]
  ];

  function sidebar(active) {
    return `
      <aside class="sidebar">
        <div class="brand">
          <span class="brand-mark">A</span>
          <div><strong>AHA</strong><small>个人 AI 工作流</small></div>
        </div>
        <nav class="nav-group">
          <div class="nav-label">Workspace</div>
          ${navItems.map(([id, name, label, count]) => `
            <a class="nav-item ${active === id ? "active" : ""}" href="?screen=${id}">
              ${icon(name)}<span>${label}</span>${count ? `<span class="nav-count">${count}</span>` : ""}
            </a>
          `).join("")}
          <div class="nav-label">快捷入口</div>
          <a class="nav-item" href="?screen=new-task">${icon("circle-plus")}<span>创建任务</span></a>
          <a class="nav-item" href="?screen=task">${icon("activity")}<span>当前执行</span><span class="nav-count">1</span></a>
        </nav>
        <div class="sidebar-bottom">
          <div class="owner">
            <span class="avatar">KK</span>
            <div><strong>Owner</strong><small>已安全登录</small></div>
            ${icon("chevrons-up-down")}
          </div>
        </div>
      </aside>
    `;
  }

  function topbar(section, detail = "") {
    return `
      <header class="topbar">
        <div class="crumb"><strong>${section}</strong>${detail ? `<i>/</i><span>${detail}</span>` : ""}</div>
        <div class="top-search">${icon("search")}<span>搜索项目、任务和知识</span></div>
        <button class="icon-btn bordered" aria-label="通知">${icon("bell")}</button>
        <button class="icon-btn bordered" aria-label="帮助">${icon("circle-help")}</button>
      </header>
    `;
  }

  function shell(active, section, detail, content, pageClass = "") {
    return `
      <div class="app-shell">
        ${sidebar(active)}
        <section class="workspace-shell">
          ${topbar(section, detail)}
          <main class="main"><div class="page ${pageClass}">${content}</div></main>
        </section>
      </div>
    `;
  }

  function loginView() {
    return `
      <div class="login-page">
        <aside class="login-brand">
          <div class="login-logo"><span class="brand-mark">A</span><strong>AHA</strong></div>
          <p>以项目为基础，以任务为核心，通过知识闭环持续成长的个人 AI 工作流。</p>
          <div class="login-security">${icon("shield-check")}<span>安全连接 · 所有操作均需认证</span></div>
        </aside>
        <main class="login-main">
          <form class="login-form">
            <h1>登录</h1>
            <p>进入你的项目、任务与知识工作区</p>
            <button class="button primary passkey" type="button">${icon("key-round")}使用 Passkey 登录</button>
            <div class="divider">或使用密码</div>
            <div class="field"><label>账号</label><input class="input" value="owner" readonly></div>
            <div class="field" style="margin-top:12px"><label>密码</label><input class="input" type="password" value="1234567890" readonly></div>
            <div class="login-options"><label><input type="checkbox" checked>保持登录</label><span>忘记密码</span></div>
            <button class="button" style="width:100%;min-height:40px" type="button">登录</button>
            <div class="login-foot">首次使用？<strong>初始化 Owner</strong></div>
          </form>
        </main>
      </div>
    `;
  }

  function projectRow(name, path, workspaces, branch, state, taskCount) {
    return `
      <div class="project-row">
        <div class="project-title">
          <span class="project-icon">${icon("folder-git-2")}</span>
          <div><strong>${name}</strong><small>${path}</small></div>
        </div>
        <div class="workspace-pills">${workspaces.map(([i, label]) => `<span class="workspace-tag">${icon(i)}${label}</span>`).join("")}</div>
        <div><span class="status ${state === "健康" ? "good" : "warn"}">${state}</span><small class="row-sub">Backend 可用</small></div>
        <div><strong class="row-title">${branch}</strong><small class="row-sub">${taskCount} 个任务</small></div>
        <button class="icon-btn">${icon("ellipsis")}</button>
      </div>
    `;
  }

  function projectsView() {
    const content = `
      <div class="page-head">
        <div><h1>所有项目</h1><p>管理本地与远程 Workspace、任务和项目知识。</p></div>
        <div class="head-actions"><button class="button">${icon("refresh-cw")}刷新状态</button><button class="button primary">${icon("plus")}新建项目</button></div>
      </div>
      <section class="metrics">
        <div class="metric"><div class="metric-top"><span>项目</span>${icon("folder-kanban")}</div><strong>3</strong><small>2 个 Git 仓库</small></div>
        <div class="metric"><div class="metric-top"><span>活动任务</span>${icon("activity")}</div><strong>4</strong><small>1 个正在执行</small></div>
        <div class="metric"><div class="metric-top"><span>Workspace</span>${icon("monitor-cog")}</div><strong>5</strong><small>3 本地 · 2 远程</small></div>
        <div class="metric"><div class="metric-top"><span>已验证知识</span>${icon("badge-check")}</div><strong>24</strong><small>今日新增 2 条</small></div>
      </section>
      <div class="projects-layout">
        <section class="panel">
          <div class="panel-head"><h3>项目</h3><small>按最近活动排序</small></div>
          ${projectRow("AHA2", "E:\\\\kk-workspace\\\\AHA2", [["monitor", "本地开发"], ["server", "远程测试"]], "main", "健康", 4)}
          ${projectRow("XIAOMIWATCH", "E:\\\\kk-workspace\\\\XIAOMIWATCH", [["monitor", "Windows"], ["container", "构建容器"]], "feature/wearpush", "健康", 2)}
          ${projectRow("Home Lab", "server-home:/srv/home-lab", [["server", "远程主机"]], "main", "需关注", 1)}
        </section>
        <aside class="panel">
          <div class="panel-head"><h3>最近任务</h3><small>查看全部</small></div>
          <div class="compact-list">
            <div class="compact-row">${icon("loader-circle")}<div><strong class="row-title">设计 AHA2 核心架构</strong><small class="row-sub">AHA2 · Main Agent</small></div><span class="status info">执行中</span></div>
            <div class="compact-row">${icon("message-circle")}<div><strong class="row-title">排查消息重复问题</strong><small class="row-sub">AHA · 14 分钟前</small></div><span class="status warn">等待输入</span></div>
            <div class="compact-row">${icon("circle-check-big")}<div><strong class="row-title">WearPush 远程部署</strong><small class="row-sub">XIAOMIWATCH · 1 小时前</small></div><span class="status good">已完成</span></div>
          </div>
          <div class="panel-head" style="border-top:1px solid var(--line)"><h3>今日活动</h3><small>2026/09/01</small></div>
          <div class="compact-list">
            <div class="compact-row">${icon("book-plus")}<div><strong class="row-title">新增项目知识</strong><small class="row-sub">Workspace Runner 设计</small></div><span class="activity-time">16:38</span></div>
            <div class="compact-row">${icon("git-branch")}<div><strong class="row-title">创建任务分支</strong><small class="row-sub">aha/task-041</small></div><span class="activity-time">15:54</span></div>
          </div>
        </aside>
      </div>
    `;
    return shell("projects", "项目", "", content);
  }

  function settingsView() {
    const content = `
      <div class="page-head">
        <div><h1>模型与 Workspace</h1><p>模型由本地 AHA 管理，执行环境通过 Env Group 注入目标 Workspace。</p></div>
        <div class="head-actions"><button class="button">${icon("history")}配置历史</button><button class="button primary">${icon("save")}保存配置</button></div>
      </div>
      <section class="panel settings-grid">
        <nav class="settings-nav">
          <button>${icon("user-round")}账号与安全</button>
          <button class="active">${icon("brain-circuit")}模型与 Provider</button>
          <button>${icon("key-square")}Env Groups</button>
          <button>${icon("monitor-cog")}Workspaces</button>
          <button>${icon("bot")}Backend 检测</button>
          <button>${icon("shield-check")}权限与审计</button>
        </nav>
        <div class="settings-content">
          <div class="section-head"><div><h3>Model Registry</h3><p>AHA 控制面中的统一模型目录。</p></div><button class="button">${icon("plus")}添加模型</button></div>
          <table class="config-table">
            <thead><tr><th>模型</th><th>Provider</th><th>兼容 Backend</th><th>上下文</th><th>Env Group</th><th>状态</th><th></th></tr></thead>
            <tbody>
              <tr><td><strong>GPT-5.6 Sol</strong><small>env:hualai-claw-gpt-5.6-sol</small></td><td>OpenAI Compatible</td><td>Codex</td><td>1M</td><td>hualai-prod · r7</td><td><span class="status good">Ready</span></td><td>${icon("ellipsis")}</td></tr>
              <tr><td><strong>DeepSeek V4 Flash</strong><small>env:hualai-deepseek-v4-flash</small></td><td>OpenAI Compatible</td><td>Claude</td><td>1M</td><td>hualai-prod · r7</td><td><span class="status good">Ready</span></td><td>${icon("ellipsis")}</td></tr>
              <tr><td><strong>Codex Default</strong><small>official:codex-default</small></td><td>OpenAI</td><td>Codex</td><td>200K</td><td>remote-login</td><td><span class="status warn">需远端登录</span></td><td>${icon("ellipsis")}</td></tr>
            </tbody>
          </table>
          <div class="settings-band">
            <div class="section-head"><div><h3>Workspace Backend 检测</h3><p>检测结果属于 Workspace，不作为模型配置来源。</p></div><button class="button">${icon("scan-search")}全部检测</button></div>
            <div style="display:grid;gap:9px;margin-top:12px">
              <div class="detect-strip"><div><strong>本地开发 · Windows</strong><small>E:\\kk-workspace\\AHA2</small></div><div><strong>Codex 1.18</strong><small>已安装</small></div><span class="status good">Ready</span><button class="button">${icon("refresh-cw")}重新检测</button></div>
              <div class="detect-strip"><div><strong>远程测试 · Ubuntu 24.04</strong><small>dev@server-a:/srv/AHA2</small></div><div><strong>Claude 2.4</strong><small>已安装</small></div><span class="status good">Ready</span><button class="button">${icon("refresh-cw")}重新检测</button></div>
              <div class="detect-strip"><div><strong>远程生产 · Debian 13</strong><small>deploy@server-b:/opt/AHA2</small></div><div><strong>Codex</strong><small>未发现</small></div><span class="status bad">Unavailable</span><button class="button">${icon("terminal")}查看详情</button></div>
            </div>
          </div>
        </div>
      </section>
    `;
    return shell("settings", "设置", "模型与 Workspace", content);
  }

  function newTaskView() {
    const content = `
      <div class="page-head">
        <div><h1>创建任务</h1><p>选择执行环境并冻结本次任务的 Runtime Config Snapshot。</p></div>
        <div class="segmented"><span class="active">任务信息</span><span>运行配置</span><span>确认</span></div>
      </div>
      <div class="task-create-layout">
        <section class="panel">
          <div class="form-section">
            <h3>任务目标</h3><p>描述需要 Agent 在项目中完成的工作。</p>
            <div class="form-grid">
              <div class="field full"><label>任务标题</label><input class="input" value="实现 AHA2 Task 与 Turn 核心模型" readonly></div>
              <div class="field full"><label>需求描述</label><textarea class="textarea" readonly>根据核心设计文档，实现 Task、Turn、BackendSession 的领域模型与状态机，并提供持久化和单元测试。暂不实现 Web API。</textarea></div>
              <div class="field"><label>项目</label><select class="select"><option>AHA2</option></select></div>
              <div class="field"><label>Workspace</label><select class="select"><option>本地开发 · Windows</option></select></div>
            </div>
          </div>
          <div class="form-section">
            <h3>Git 隔离</h3><p>修改型任务默认使用独立分支和 worktree。</p>
            <div class="form-grid">
              <div class="field"><label>任务类型</label><select class="select"><option>修改项目文件</option></select></div>
              <div class="field"><label>目标分支</label><select class="select"><option>main</option></select></div>
              <div class="field"><label>任务分支</label><input class="input" value="aha/task-001-domain-model" readonly></div>
              <div class="field"><label>Worktree</label><input class="input" value=".aha/worktrees/task-001" readonly></div>
            </div>
          </div>
          <div class="form-section">
            <h3>Agent Runtime</h3><p>模型来自本地 Registry，Env Group 在启动进程时注入。</p>
            <div class="form-grid">
              <div class="field"><label>Backend</label><select class="select"><option>Codex · Ready</option></select></div>
              <div class="field"><label>模型</label><select class="select"><option>GPT-5.6 Sol · 1M</option></select></div>
              <div class="field"><label>Env Group</label><select class="select"><option>hualai-prod · revision 7</option></select></div>
              <div class="field"><label>推理强度</label><select class="select"><option>High</option></select></div>
            </div>
          </div>
        </section>
        <aside class="panel runtime-summary">
          <div class="panel-head"><h3>创建前检查</h3><span class="status good">可以创建</span></div>
          <div class="summary-block">
            <h4>执行位置</h4>
            <div class="summary-line"><span>Project</span><strong>AHA2</strong></div>
            <div class="summary-line"><span>Workspace</span><strong>本地开发</strong></div>
            <div class="summary-line"><span>Platform</span><code>Windows 11</code></div>
            <div class="summary-line"><span>Base commit</span><code>580c4d8</code></div>
          </div>
          <div class="summary-block">
            <h4>Runtime Snapshot</h4>
            <div class="summary-line"><span>Backend</span><strong>Codex 1.18</strong></div>
            <div class="summary-line"><span>Model</span><strong>GPT-5.6 Sol</strong></div>
            <div class="summary-line"><span>Env Group</span><strong>hualai-prod r7</strong></div>
          </div>
          <div class="summary-block">
            <h4>检查项</h4>
            <div class="check-row"><span class="check">${icon("check")}</span>Workspace 连接正常</div>
            <div class="check-row"><span class="check">${icon("check")}</span>Git 工作区干净</div>
            <div class="check-row"><span class="check">${icon("check")}</span>Backend 检测通过</div>
            <div class="check-row"><span class="check">${icon("check")}</span>Env Group 可解析</div>
            <div class="check-row"><span class="check">${icon("check")}</span>Project KB 已建立索引</div>
          </div>
          <div class="summary-block"><button class="button primary" style="width:100%;min-height:40px">${icon("rocket")}创建并启动任务</button></div>
        </aside>
      </div>
    `;
    return shell("tasks", "任务", "创建任务", content);
  }

  function taskView() {
    const content = `
      <div class="task-page">
        <header class="task-head">
          <div>
            <h1>实现 Task 与 Turn 核心模型</h1>
            <div class="task-meta"><span class="status info">执行中</span><span class="status">task-001</span><span class="status">aha/task-001-domain-model</span></div>
          </div>
          <div class="head-actions"><button class="button">${icon("pause")}中断 Turn</button><button class="icon-btn bordered">${icon("ellipsis")}</button></div>
        </header>
        <div class="task-body">
          <aside class="task-rail">
            <div class="task-rail-section">
              <h4>执行上下文</h4>
              <div class="rail-line">${icon("folder-git-2")}<div><strong>AHA2</strong>E:\\kk-workspace\\AHA2</div></div>
              <div class="rail-line">${icon("git-branch")}<div><strong>任务分支</strong>aha/task-001-domain-model</div></div>
              <div class="rail-line">${icon("bot")}<div><strong>Codex</strong>GPT-5.6 Sol · High</div></div>
              <div class="rail-line">${icon("link")}<div><strong>Session</strong>ses_98f1 · reused</div></div>
            </div>
            <div class="task-rail-section">
              <h4>任务阶段</h4>
              <div class="rail-line">${icon("circle-check-big")}<div><strong>需求确认</strong>已完成</div></div>
              <div class="rail-line">${icon("circle-check-big")}<div><strong>领域建模</strong>已完成</div></div>
              <div class="rail-line">${icon("loader-circle")}<div><strong>持久化实现</strong>执行中</div></div>
              <div class="rail-line">${icon("circle")}<div><strong>测试验证</strong>待开始</div></div>
            </div>
            <div class="task-rail-section">
              <h4>Task Memory</h4>
              <div class="rail-line">${icon("target")}<div><strong>当前目标</strong>完成状态机持久化</div></div>
              <div class="rail-line">${icon("badge-check")}<div><strong>已确认</strong>Turn 是独立实体</div></div>
              <div class="rail-line">${icon("ban")}<div><strong>已排除</strong>前端事件推导状态</div></div>
            </div>
          </aside>
          <section class="conversation">
            <div class="turn-strip">
              <span class="turn-indicator">${icon("loader-circle")}</span>
              <div><strong>Turn 4 · Agent 正在执行</strong><small>准备 1.2s · 启动 3.8s · 执行 02:14 · 当前调用 shell_command</small></div>
              <div class="turn-phases"><span class="done"></span><span class="done"></span><span class="active"></span><span></span></div>
            </div>
            <div class="messages">
              <div class="message user"><div class="message-head"><span>Owner</span><span>16:42</span></div><div class="message-body">状态机需要保证服务重启后能够恢复，并且 Turn 状态不能依赖前端根据事件猜测。</div></div>
              <div class="message"><div class="message-head"><span>Main Agent · Codex</span><span>16:42</span></div><div class="message-body">我会把 Turn 作为持久化聚合，命令执行遵循“先持久化、再执行副作用、最后记录事件”的顺序。先检查现有 Schema 和迁移入口。</div></div>
              <div class="tool-event">${icon("terminal")}<div><strong>shell_command</strong><span>rg -n \"class Turn|turn_status\" src tests</span></div><span class="status good">exit 0</span></div>
              <div class="tool-event">${icon("file-search")}<div><strong>read_file</strong><span>src/aha/domain/task.py · 214 lines</span></div><span>完成</span></div>
              <div class="message"><div class="message-head"><span>Main Agent · 进度更新</span><span>16:44</span></div><div class="message-body">已确认当前没有独立 Turn 表。我正在新增领域实体、状态转换校验和幂等 Command 记录，随后补恢复测试。</div></div>
              <div class="tool-event">${icon("terminal")}<div><strong>shell_command</strong><span>python -m pytest tests/test_turn_lifecycle.py</span></div><span class="status warn">running</span></div>
            </div>
            <div class="composer">
              <div class="composer-box"><button class="icon-btn">${icon("paperclip")}</button><span class="composer-placeholder">Agent 执行中，新消息将排队到下一 Turn</span><button class="icon-btn bordered">${icon("arrow-up")}</button></div>
            </div>
          </section>
          <aside class="inspector">
            <div class="inspector-tabs"><span class="active">上下文</span><span>产物</span><span>审计</span></div>
            <div class="inspector-block">
              <h4>KB 注入 · 4 条</h4>
              <div class="kb-hit"><strong>Project · 领域状态规范</strong><small>Task、Turn、Process 状态必须分离，UI 读取后端 Projection。</small></div>
              <div class="kb-hit"><strong>Global · 副作用幂等</strong><small>远程命令和 Git 操作必须使用稳定 Command ID。</small></div>
              <div class="kb-hit"><strong>Project · 测试约定</strong><small>状态转换使用 table-driven 单元测试。</small></div>
            </div>
            <div class="inspector-block">
              <h4>Prompt Pack</h4>
              <div class="summary-line"><span>系统规则</span><strong>3.8K</strong></div>
              <div class="summary-line"><span>Task Memory</span><strong>2.1K</strong></div>
              <div class="summary-line"><span>Project KB</span><strong>4.6K</strong></div>
              <div class="summary-line"><span>Global KB</span><strong>1.2K</strong></div>
              <div class="summary-line"><span>当前输入</span><strong>96</strong></div>
            </div>
            <div class="inspector-block">
              <h4>Knowledge Candidate</h4>
              <div class="kb-hit" style="border-left-color:#d97706"><strong>Turn 恢复边界</strong><small>observed · 来源 Turn 4 · 等待测试验证</small></div>
            </div>
          </aside>
        </div>
      </div>
    `;
    return shell("tasks", "任务", "task-001", content, "task-page-wrap");
  }

  function knowledgeView() {
    const content = `
      <div class="page-head">
        <div><h1>知识库</h1><p>Global 记录通用 Agent 工作经验，Project 记录项目专用认知。</p></div>
        <div class="head-actions"><div class="segmented"><span class="active">Project</span><span>Global</span></div><button class="button">${icon("plus")}新建知识</button></div>
      </div>
      <section class="panel knowledge-layout">
        <aside class="knowledge-tree">
          <div class="tree-search"><div class="top-search" style="width:100%;margin:0">${icon("search")}<span>搜索知识</span></div></div>
          <div class="tree-group">
            <div class="tree-label">AHA2</div>
            <div class="tree-item">${icon("folder")}Navigation</div>
            <div class="tree-item active">${icon("file-text")}核心领域模型</div>
            <div class="tree-item">${icon("file-text")}Workspace Runner</div>
            <div class="tree-item">${icon("folder")}Practices</div>
            <div class="tree-item">${icon("file-text")}Git 任务隔离</div>
            <div class="tree-item">${icon("file-text")}状态恢复规范</div>
            <div class="tree-item">${icon("folder")}Decisions</div>
            <div class="tree-item">${icon("file-text")}控制面与执行面</div>
          </div>
          <div class="tree-group">
            <div class="tree-label">状态</div>
            <div class="tree-item">${icon("circle-dashed")}Candidate <span style="margin-left:auto">5</span></div>
            <div class="tree-item">${icon("badge-check")}Verified <span style="margin-left:auto">24</span></div>
            <div class="tree-item">${icon("triangle-alert")}Stale <span style="margin-left:auto">2</span></div>
          </div>
        </aside>
        <article class="knowledge-editor">
          <div class="doc-meta"><span class="status good">Verified</span><span>Project Knowledge</span><span>·</span><span>最后验证 2026/09/01</span></div>
          <h1>核心领域模型</h1>
          <p>Task 表达用户的长期目标；Turn 表示一次输入触发的 Agent 执行。两者必须独立持久化，不能通过前端事件临时推导。</p>
          <h2>状态边界</h2>
          <ul>
            <li><code>Task.status</code> 描述目标整体生命周期。</li>
            <li><code>Turn.status</code> 描述单次 Agent 调用的阶段和结果。</li>
            <li><code>BackendProcess.status</code> 只描述操作系统进程。</li>
            <li>UI 读取后端 Projection，不自行拼装权威状态。</li>
          </ul>
          <h2>恢复原则</h2>
          <p>所有外部副作用遵循：持久化 Command → 提交状态 → 执行副作用 → 写入 Event 和结果。AHA 重启后根据持久化状态恢复。</p>
          <div class="evidence-box">
            <div class="evidence-item"><strong>来源任务</strong><small>task-001 · Turn 2/4</small></div>
            <div class="evidence-item"><strong>源码证据</strong><small>domain/task.py · store/turns.py</small></div>
            <div class="evidence-item"><strong>验证</strong><small>18 lifecycle tests passed</small></div>
          </div>
        </article>
        <aside class="growth-panel">
          <div class="panel-head"><h3>知识增长</h3><small>实时更新</small></div>
          <div class="growth-stat"><div><strong>5</strong><small>待验证 Candidate</small></div><div><strong>2</strong><small>本任务新增</small></div></div>
          <div class="candidate">
            <div class="candidate-head"><strong>Turn 恢复边界</strong><span class="status warn">Candidate</span></div>
            <p>执行中的远程进程失联后，应区分不可验证与确认停止。</p>
            <div class="candidate-actions"><button class="mini-btn">查看证据</button><button class="mini-btn">提升</button></div>
          </div>
          <div class="candidate">
            <div class="candidate-head"><strong>Env Group 快照</strong><span class="status warn">Candidate</span></div>
            <p>Task 必须绑定 Env Group revision，防止配置漂移。</p>
            <div class="candidate-actions"><button class="mini-btn">合并到现有</button><button class="mini-btn">验证</button></div>
          </div>
          <div class="candidate">
            <div class="candidate-head"><strong>Git Worktree 清理</strong><span class="status bad">Stale</span></div>
            <p>旧保留期限与当前分支策略冲突，需要重新确认。</p>
            <div class="candidate-actions"><button class="mini-btn">修订</button><button class="mini-btn">废弃</button></div>
          </div>
        </aside>
      </section>
    `;
    return shell("knowledge", "知识库", "AHA2", content);
  }

  const views = {
    login: loginView,
    projects: projectsView,
    settings: settingsView,
    "new-task": newTaskView,
    task: taskView,
    knowledge: knowledgeView,
    tasks: taskView
  };

  app.innerHTML = (views[screen] || projectsView)();
  window.requestAnimationFrame(() => {
    if (window.lucide) window.lucide.createIcons();
    document.documentElement.dataset.ready = "true";
  });
})();
