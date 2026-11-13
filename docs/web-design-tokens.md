# Web 设计 token（阶段 2）

视觉方向 B「温暖工作台」的基础层。所有 token 定义在 `web/src/styles.css` 的 `:root`，基础组件直接消费这些变量。本轮只改样式与 token，不改 DOM、布局、导航结构、文案、交互行为，也不做暗色模式。

## 颜色

| Token | 值 | 用途 |
| --- | --- | --- |
| `--color-text` | `#241d17` | 正文、标题、主要文字 |
| `--color-text-muted` | `#6a5c4e` | 次要说明、表格表头、占位说明 |
| `--color-text-subtle` | `#6f6152` | 输入占位符、弱提示 |
| `--color-text-inverse` | `#fff8f0` | 深色底上的文字（主按钮、侧栏） |
| `--color-canvas` | `#f6f1e8` | 页面底色 |
| `--color-surface` | `#fffdf8` | 卡片、面板、输入框表面 |
| `--color-surface-raised` | `#ffffff` | 浮层、菜单、弹窗表面 |
| `--color-surface-muted` | `#efe8dc` | 次级块、徽标底、禁用输入 |
| `--color-border` | `#ddd2c3` | 默认描边、分隔线 |
| `--color-border-strong` | `#c4b5a2` | 控件描边、强调分隔 |
| `--color-border-subtle` | `#ebe3d7` | 轻分隔（备用） |
| `--color-overlay` | `rgba(43, 33, 27, .52)` | 对话框遮罩 |
| `--color-accent` | `#7c3b2b` | 主操作、激活态、链接（栗色） |
| `--color-accent-hover` | `#66301f` | 主操作悬停 |
| `--color-accent-active` | `#552719` | 主操作按下 |
| `--color-accent-soft` | `#f3e2da` | 激活底、选中底、主色浅背景 |
| `--color-accent-ink` | `#7c3b2b` | 浅栗色底上的文字 |
| `--color-amber` | `#a35f13` | 琥珀强调（备用） |
| `--color-amber-soft` | `#fdf1dc` | 琥珀浅底 |
| `--color-amber-ink` | `#8a4e0d` | 琥珀浅底上的文字 |
| `--color-success` | `#2f6b45` | 成功态强调 |
| `--color-success-soft` | `#e6f2e9` | 成功态背景 |
| `--color-success-ink` | `#245538` | 成功态背景上的文字 |
| `--color-success-bright` | `#9bd7ae` | 深色导航上的成功/安全提示文字 |
| `--color-warning` | `#8a5a12` | 警告态强调 |
| `--color-warning-soft` | `#fbf0d9` | 警告态背景 |
| `--color-warning-ink` | `#7a4d0c` | 警告态背景上的文字、浅色底上的待审批文字 |
| `--color-warning-bright` | `#f0b26b` | 深色导航上的待审批文字 |
| `--color-danger` | `#a3362d` | 危险态强调、删除操作 |
| `--color-danger-soft` | `#fbe9e7` | 危险态背景 |
| `--color-danger-ink` | `#8f2f27` | 危险态背景上的文字 |
| `--color-info` | `#2b5d8a` | 信息态强调 |
| `--color-info-soft` | `#e7f0f8` | 信息态背景 |
| `--color-info-ink` | `#24507a` | 信息态背景上的文字 |
| `--color-nav` | `#2b211b` | 侧栏、登录品牌区深色底 |
| `--color-nav-text` | `#f2e9dd` | 深色导航上的文字 |
| `--color-nav-muted` | `#c4b6a5` | 深色导航上的次要文字 |
| `--color-nav-active` | `#44362c` | 深色导航激活底 |
| `--color-nav-border` | `#54453a` | 深色导航分隔线 |

## 字体

| Token | 值 | 用途 |
| --- | --- | --- |
| `--font-sans` | `Inter, "Segoe UI", "Microsoft YaHei", sans-serif` | 界面正文 |
| `--font-mono` | `ui-monospace, SFMono-Regular, Menlo, Consolas, monospace` | 任务编号、数据、代码 |
| `--font-size-11` | `11px` | 表头、徽标、辅助标签 |
| `--font-size-12` | `12px` | 表单标签、消息条、说明 |
| `--font-size-13` | `13px` | 正文、空状态 |
| `--font-size-14` | `14px` | 强调正文 |
| `--font-size-16` | `16px` | 区块标题 |
| `--font-size-20` | `20px` | 页面/弹窗标题 |
| `--font-size-26` | `26px` | 登录标题、大数字 |

## 间距

| Token | 值 | 用途 |
| --- | --- | --- |
| `--space-1` | `4px` | 紧凑间隔 |
| `--space-2` | `8px` | 控件内间隔、按钮组 |
| `--space-3` | `12px` | 常规内边距 |
| `--space-4` | `16px` | 区块内边距 |
| `--space-5` | `24px` | 区块间距 |
| `--space-6` | `32px` | 页面级间距 |
| `--space-7` | `48px` | 大区块留白 |

## 圆角与阴影

| Token | 值 | 用途 |
| --- | --- | --- |
| `--radius-control` | `6px` | 按钮、输入、徽标、菜单项 |
| `--radius-card` | `10px` | 面板、卡片、任务卡 |
| `--radius-popover` | `14px` | 对话框、浮层、下拉菜单 |
| `--radius-pill` | `999px` | 胶囊徽标、状态点容器 |
| `--shadow-xs` | `0 1px 2px rgba(59, 42, 26, .08)` | 卡片微阴影 |
| `--shadow-popover` | `0 12px 32px rgba(59, 42, 26, .18)` | 浮层、菜单 |
| `--shadow-dialog` | `0 24px 64px rgba(59, 42, 26, .24)` | 对话框 |

## 焦点与动效

| Token | 值 | 用途 |
| --- | --- | --- |
| `--focus-ring-color` | `#9a5b1f` | 浅色表面的键盘焦点描边颜色 |
| `--focus-ring` | `0 0 0 3px rgba(154, 91, 31, .30)` | 浅色表面的输入控件焦点环 |
| `--focus-ring-nav-color` | `#f0b26b` | 深色导航/登录品牌区的键盘焦点描边颜色 |
| `--focus-ring-nav` | `0 0 0 3px rgba(240, 178, 107, .45)` | 深色表面的焦点环 |
| `--duration-fast` | `120ms` | 悬停、颜色过渡 |
| `--duration-base` | `160ms` | 常规过渡 |
| `--duration-slow` | `180ms` | 浮层/展开过渡 |
| `--ease-standard` | `cubic-bezier(.2, 0, 0, 1)` | 标准缓动 |

`@media (prefers-reduced-motion: reduce)` 把动画和过渡压到 1ms，并关闭平滑滚动。`:focus-visible` 给键盘焦点统一 2px 描边；输入控件用 `--focus-ring` 的 3px 外环。侧栏和登录品牌区是深色表面，焦点色改用 `--focus-ring-nav-color`，避免浅色表面的 `--focus-ring-color` 在深色底上只有 2.91:1。

## 组件映射

| 组件 | 选择器 | 主要 token |
| --- | --- | --- |
| 按钮 primary | `button`, `.primary` | `--color-accent`、`--color-text-inverse`、`--radius-control`；基础 hover 只改 `--button-*` 自定义属性，组件自己声明的背景/文字/描边不会被抢走 |
| 按钮 secondary | `button.secondary` | `--color-surface`、`--color-border-strong`、`--color-text` |
| 按钮 ghost | `button.ghost` | `transparent`、`--color-text-muted`、`--color-surface-muted` |
| 按钮 danger | `button.danger` | `--color-danger`、`--color-danger-soft`、`--color-danger-ink` |
| 输入/选择/文本域 | `input`, `select`, `textarea` | `--color-border-strong`、`--color-surface`、`--radius-control`、`--focus-ring` |
| 表格 | `table`, `th`, `td` | `--color-border`、`--color-text-muted`、`--font-size-11` |
| 卡片/面板 | `.panel`, `.metrics > div`, `.task-card`, `.backend-card`, `.managed-profile-card`, `.channel-instance-card`, `.knowledge-entry-card`, `.skill-card` | `--radius-card`、`--color-surface`、`--shadow-xs` |
| 徽标/状态 | `.status`, `.sync-status-pill`, `.backend-identifier`, `.backend-proto`, `.chips`, `.task-tool-badge` | `--radius-pill`、`--color-*-soft/ink`；待审批徽标用 `--color-warning` 底 + `--color-text-inverse` 字，导航待审批文字用 `--color-warning-ink`（浅底）或 `--color-warning-bright`（深色侧栏） |
| 对话框 | `dialog`, `.dialog-head`, `.dialog-actions` | `--radius-popover`、`--shadow-dialog`、`--color-overlay` |
| 浮层/菜单 | `.task-filter-menu`, `.conversation-filter-menu`, `.slash-command-menu`, `.hardware-title-group-menu`, `.desktop-popover` | `--radius-popover`、`--shadow-popover`、`--color-surface-raised` |
| 消息条 | `.banner`, `.app-hydrating`, `.security-warning`, `.form-error` | 语义 `-soft/-ink`、`--radius-control` |
| 空/载/错状态 | `.loading`, `.boot-loading`, `.task-detail-loading`, `.empty`, `.knowledge-empty`, `.hardware-empty`, `.task-channel-empty`, `.desktop-empty`, `.assistant-empty` | `--color-text-muted`、`--color-canvas`、`--color-surface-muted` |
| 侧栏/导航 | `.sidebar`, `.sidebar nav button`, `.bottom-nav`, `.owner-block` | `--color-nav*`、`--color-accent` |
| 登录页 | `.login-brand`, `.brand-mark`, `.auth-form`, `.auth-link`, `label` | `--color-nav*`、`--color-accent`、`--font-size-26` |

## 可访问性

正文和背景的对比度（WCAG AA 要求 4.5:1，大字号 3:1）：

| 前景 | 背景 | 对比度 |
| --- | --- | --- |
| `--color-text` | `--color-canvas` | 14.78:1 |
| `--color-text` | `--color-surface` | 16.35:1 |
| `--color-text-muted` | `--color-canvas` | 5.74:1 |
| `--color-text-muted` | `--color-surface` | 6.35:1 |
| `--color-text-subtle` | `--color-canvas` | 5.32:1 |
| `--color-text-inverse` | `--color-accent` | 7.92:1 |
| `--color-accent-ink` | `--color-accent-soft` | 6.63:1 |
| `--color-success-ink` | `--color-success-soft` | 7.50:1 |
| `--color-warning-ink` | `--color-warning-soft` | 6.42:1 |
| `--color-warning-ink` | `--color-surface` | 7.14:1 |
| `--color-warning-bright` | `--color-nav` | 8.45:1 |
| `--color-warning-bright` | `--color-nav-active` | 6.24:1 |
| `--color-text-inverse` | `--color-warning` | 5.61:1 |
| `--focus-ring-nav-color` | `--color-nav` | 8.45:1 |
| `--focus-ring-nav-color` | `--color-nav-active` | 6.24:1 |
| `--color-danger-ink` | `--color-danger-soft` | 6.88:1 |
| `--color-info-ink` | `--color-info-soft` | 7.28:1 |
| `--color-amber-ink` | `--color-amber-soft` | 5.92:1 |
| `--color-nav-text` | `--color-nav` | 13.08:1 |
| `--color-nav-muted` | `--color-nav` | 7.92:1 |
| `--color-success-bright` | `--color-nav` | 9.52:1 |

状态不只靠颜色：`.status` 同时有文字和圆点，`.banner` 额外用 3px 内阴影竖条区分；`.form-error`、`.security-warning` 也带文字和边框。键盘焦点用 `:focus-visible` 统一描边，输入控件另有 3px 焦点环。`prefers-reduced-motion` 生效时动画与过渡降到 1ms。

## 兼容别名

模块里尚未迁移的旧变量保留为别名，避免一次性改整份 233KB 样式：

`--ink` → `--color-text`，`--text` → `--color-text`，`--muted` → `--color-text-muted`，`--line` → `--color-border`，`--line-strong` → `--color-border-strong`，`--surface` → `--color-surface`，`--surface-soft` → `--color-surface-muted`，`--nav` → `--color-nav`，`--blue` → `--color-accent`，`--green` → `--color-success`，`--amber` → `--color-warning`，`--red`/`--danger` → `--color-danger`。

## 验证

```bash
node scripts/build-web.mjs
node --test web/tests/*.test.mjs
go test ./internal/httpapi -count=1
```

`web/tests/design_tokens.test.mjs` 检查核心 token、关键选择器是否使用 `var(--...)`、颜色对比度、焦点环、reduced-motion，以及 select 的 4px 左内边距（避免 Windows/Edge 原生选项弹层文字错位回归）。
