# task-743 评审包：task_panels.browser.mjs 技能选择器超时

- 任务：task-743「修复 task_panels.browser.mjs 技能选择器超时（main 既有失败）」
- 基线：`391db60`（main）
- 分支：`aha/task_1286fd66714b593de061`
- 工作区：`/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_1286fd66714b593de061`
- 状态：未合并、未 push、未部署、未运行安装器；未登录真实账号，未读写任何凭据。
- 修复提交：`db94c22`
- 评审包提交：本文件随后单独提交

## 0. 结论

不是产品缺陷，也不是环境/依赖问题，而是**浏览器测试的数据桩过期**。

task-546 已经在 `5c9b37e` 把任务技能选择器切成「只读 vault 技能包，不再合并
legacy 技能表」。`task_panels.browser.mjs` 最后一次更新是 task-469 的
`61b0763`，之后仍从 `/api/v1/skills` 的 legacy 目录取 `skill-a`/`skill-b` 并
断言它们出现在选择器里。选择器按设计只渲染 vault 包，于是
`#task-skills-fold input[value="skill-a"]` 永远不存在，`setChecked` 等满 30s
超时。

同一根因还影响 `task_agent_config.browser.mjs`：它的 Agent 配置对话框技能区
同样只读 vault，但 fixture 没有 `/api/v1/knowledge-vault/skills`，在
`#agent-config-skills-fold input[value="skill-a"]` 处以相同方式超时。本次一并
修正，改动只涉及测试文件，产品代码零改动。

## 1. 只读复现

### 1.1 命令与环境

worktree 内没有 `.tools/web-validation`，按既有做法在 scratch 安装 Playwright，
用测试自带的覆盖口跑**原始测试文件**（未改副本）：

```bash
node scripts/build-web.mjs          # web/build 就绪
AHA_PLAYWRIGHT_MODULE=<scratch>/pw/node_modules/playwright/index.mjs \
AHA_BROWSER_EXECUTABLE=<cache>/chromium_headless_shell-1255/chrome-headless-shell-linux64/chrome-headless-shell \
AHA_BROWSER_SCREENSHOT_DIR=<scratch>/shots \
node web/tests/task_panels.browser.mjs
```

### 1.2 基线结果

```
locator.setChecked: Timeout 30000ms exceeded.
Call log:
  - waiting for locator('#task-skills-fold input[value="skill-a"]')

    at .../web/tests/task_panels.browser.mjs:213:66
```

失败位置与任务描述一致，稳定复现。前面的断言全部通过：
`已选 1`、vault 技能存在且自动勾选、归档技能未泄漏、折叠面板初始高度等。

### 1.3 现场证据

在失败点插入只读 DOM 转储（scratch 副本，不改源测试）后得到：

- `#task-project` 的值是 `project-1`，选项正常（`project-1`/`project-2`），
  排除“项目下拉为空导致技能列表空”的旧问题。
- `#task-skills-fold` 内只有一个技能分组（项目技能），唯一的选项是
  `vault-skill:project:project-1:vault-review`；`skill-a`/`skill-b` 不存在。
- 页面实际请求了两个目录：
  `/api/v1/skills`（legacy）和 `/api/v1/knowledge-vault/skills`（vault）。
  前者返回了 `skill-a`/`skill-b`，但选择器没有使用它。
- 无 pageerror；`#task-dialog` 正常打开，其他字段（proxy、filesystem、
  collaboration、fast 等）均正常。

因此：选择器行为符合当前产品设计，失败点是测试数据与产品数据源不一致。

## 2. 根因

- `web/src/main.ts:1805` 的 `availableTaskSkills`：

  ```ts
  // Skills are vault packages only. ... The legacy skills table is never
  // merged here.
  if (vaultTaskSkillsModule) return availableVaultTaskSkills(projectID, includeIDs);
  ```

  vault 模块加载成功后只返回 `vaultTaskSkillsForProject` 的结果；
  `task_vault_skills.ts:82-93` 按 vault 目录、项目绑定、runtime read/index
  门控过滤。legacy `state.skills` 只在 vault 模块加载失败时作为兜底，正常路径
  不合并。
- 引入该语义的提交是 `5c9b37e`（task-546，2026-11-16），提交说明明确写
  「GET/任务选择器/自动启用只读 vault，legacy 技能表不再被新写入」。
- `web/tests/task_panels.browser.mjs` 最后一次内容更新是 `61b0763`
  （task-469，2026-11-15），早于 `5c9b37e`；`5c9b37e` 没有改这个测试。
- `task_agent_config.browser.mjs` 的 TASK 还带着 `skill_ids: ["skill-b"]`，
  测试同样在 `#agent-config-skills-fold input[value="skill-a"]` 超时，
  fixture 也没有 vault 路由。两者是同一迁移遗漏。

## 3. 改动范围

只改两个测试文件：

### `web/tests/task_panels.browser.mjs`

- 在 `/api/v1/knowledge-vault/skills` 桩中增补两个可选项：
  - `vault-skill:global:alpha`（global，项目已绑定）
  - `vault-skill:project:project-1:beta`（project-1）
  - 原有 `vault-review`（auto_attach）和 archive 负例保持不变。
- 断言从 legacy id 改为 vault id：
  - 勾选 `alpha` + `beta` 后仍要求 `已选 3`；
  - `offered` 仍做**精确集合**断言：alpha、beta、vault-review；
  - 提交 payload 仍必须是 `["vault-skill:global:alpha",
    "vault-skill:project:project-1:beta"]`；
  - 取消勾选 auto-attach 的 `vault-review` 后仍要求 `已选 2`。
- 保留 legacy `/api/v1/skills` 桩（含 `skill-a`/`skill-b`），并新增显式断言：
  它们不得出现在选择器里。

### `web/tests/task_agent_config.browser.mjs`

- TASK 的 `skill_ids` 改为 `vault-skill:project:project-1:beta`。
- 新增 `/api/v1/knowledge-vault/skills` 桩（global alpha + project beta）。
- 打开技能区后先断言可选集合精确等于两个 vault id，再勾选 alpha、取消 beta，
  最终 PATCH 的 `skill_ids` 仍必须精确等于 `["vault-skill:global:alpha"]`。
- legacy `/api/v1/skills` 桩同样保留，用于证明不会被混入。

### 断言强度

没有降低断言强度：选择器选项集合、折叠计数、提交 payload、归档技能排除、
Agent 配置保存内容都从 legacy id 一对一换成了 vault id，并额外增加
「legacy 不得混入」的正向防护。没有删除任何失败路径。

## 4. 取舍

- **不在产品代码里把 legacy 技能合回来**：那会推翻 task-546 的设计，重新启用
  已经退役的技能表，属于反向修复。
- **不删掉这两个浏览器用例**：它们覆盖的创建面板/Agent 配置技能选择、自动启用、
  手工取消、提交 payload 和归档排除都是真实行为，删掉会失去回归保护。
- **只改测试数据与断言 id**：产品代码路径与运行时行为完全未动，因此不需要前端
  功能验收。
- **范围控制在两个受同一迁移影响的文件**：没有顺手改任务面板的其他逻辑。

## 5. 实际命令与结果

环境：Linux builder，Node 24.21.0；Playwright 1.65.1 +
`chromium_headless_shell-1255`（headless，不是真实桌面浏览器）。

| 命令 | 结果 |
| --- | --- |
| `node scripts/build-web.mjs` | exit 0；产出 `web/build` 与 `web/dist`（`app.dfa892e06178.js`、`styles.eceebbd144d8.css`、22 个 route chunk） |
| `cd web && npm test` | 637 pass，0 fail，exit 0 |
| `node web/tests/task_panels.browser.mjs` | pass，连续 3 次全部 exit 0，输出 `task panels verified in the built Web bundle` |
| `node web/tests/task_agent_config.browser.mjs` | pass，exit 0 |
| `node web/tests/knowledge_vault.browser.mjs` | pass，exit 0（含 axe 检查；技能页与 vault 目录未受影响） |
| `node web/tests/task_permissions.browser.mjs` | pass，exit 0 |
| `node web/tests/narrow_viewport_layout.browser.mjs` | pass，exit 0 |

修复前基线见 §1；修复后 `task_panels.browser.mjs` 与
`task_agent_config.browser.mjs` 均实际通过，不是只靠改选择器等它变绿。

## 6. 未覆盖风险

- 只跑了受技能选择器影响的浏览器用例（创建任务面板、Agent 配置、知识库技能页、
  任务权限、窄视口），没有在本 Turn 内跑全部 48 个 `.browser.mjs`；其余用例
  与本改动无数据或选择器交集。
- 浏览器验证是 headless Chromium，不是真实 Windows Edge；本次没有产品代码改动，
  真实浏览器差异不影响本修复的结论。
- 测试依赖 vault 懒加载模块在打开对话框前完成（页面 `networkidle` 且既有断言
  已经依赖这一点）。本次没有改变加载时序，也没有引入新的等待。
- `task_panels.browser.mjs` 仍要求 `web/build` 存在；这是所有同类浏览器测试的
  既有前提，本任务按要求先执行 `node scripts/build-web.mjs`。

## 7. 是否需要 Owner 实机验收

不需要。改动只涉及测试 fixture 与断言 id，产品运行时行为零改动；headless
浏览器回归已覆盖选择器的渲染、自动启用、手工取消和提交 payload。按正常发布
流程验收即可。

## 8. 现场与后续

- 所有改动只在上述 worktree 的分支上，未合并、未 push、未部署。
- 未接触真实账号、凭据、Cookie；浏览器只访问本地 fixture 服务。
- `web/node_modules`、`web/build`、`web/dist`、`.aha2-context` 均为 gitignore，
  不在提交里；scratch 中的 Playwright 与日志不进入仓库。
- 建议评审重点：确认「任务选择器只读 vault」是 task-546 的有意行为，而不是
  应该恢复 legacy 合并；本修复按前者处理。
