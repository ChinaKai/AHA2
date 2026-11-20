# 提示词四块结构：盘点、lint 与预览方案（task-665）

本文只做结构方案、lint 和预览。**没有修改任何提示词正文，也没有保存生产模板**；
生产 `Build` 的渲染结果保持逐字不变。文中所有“建议顺序”和“建议上限”都是待 Owner
确认的提案，不是已经生效的运行时行为。

> **后续实施（2026-11-19，正文重写）**：本文描述的是 task-665 当时的状态。之后生产模板已按
> 新方案重写并重排，以下几点以代码为准，本文其余部分保留作历史记录：
>
> - 生产顺序改为 `身份 → 做事准则 → 控制协议 → 上下文`：固定规则在前，每轮变化的 Task 数据和
>   当前 Inbox 放最后。这与下文 3.1 的“上下文在控制协议之前”不同，是 Owner 确认过的调整。
> - 正文重写：开头交代 AHA2 是什么、Turn 是什么、产出给谁看；规则带上原因；
>   `channel.external-channel` 按“私聊/群聊 × 是否 `task_route`”条件渲染（新增模板字段
>   `ChannelGroup`、`ChannelTaskRoute`），并加入群聊 @ 的规则。
> - 小节标题写在模板正文里（角色 / 一个 Turn 怎么进行 / 指令从哪里来 / 协作 / 外部渠道 /
>   知识库 / 和 AHA 打交道），不再用模板名拼 `## <Name>`；模板名只是管理页的标签。
> - `channel.*` 从身份块移到做事准则块，渲染在协作策略之后。
> - 控制协议改为“内置 MCP 优先、HTTP Agent API 兜底”，`agent-api.md` 开头加了 MCP 对照表。
> - 块上限调整为 身份 7000 / 做事准则 11500 / 控制协议 2800，各模板预算见
>   `resident_budget_test.go`。
> - 生产顺序已经是建议顺序，所以结构预览的 before/after 不再有 diff，`-check before` 也会通过。

## 1. 四块定义

| 块 | 英文 id | 回答的问题 | 包含的现有模板 |
| --- | --- | --- | --- |
| 身份 | `identity` | 我是谁、在哪个角色/渠道、以什么协作形态工作 | `identity.*`、`role.main`、`role.sub`、`channel.web`、`channel.external-channel` |
| 做事准则 | `principles` | 按什么准则工作：边界、协作、证据与知识纪律 | `core.default`、`policy.single`、`policy.auto`、`protocol.knowledge` |
| 上下文 | `context` | 这一轮的事实：Task/Workspace、可用上下文、Inbox、生成文件 | `section.compact-handoff`、`section.task-workspace`、`section.available-context`、`section.current-inbox`、`section.inbox-batch-content`、`context.*` |
| 控制协议 | `control` | 如何操作系统与交付 | `protocol.agent-api`、`protocol.attachment-delivery`、`resource.*`、运行时追加的回复语言指令 |

块名和顺序写在 `internal/prompt/structure.go` 的 `promptBlockSpecs`，模板到块的映射写在
`promptFragmentPlans`。`language.directive` 是 `BuildEntryModel` 在末尾追加的合成片段
（`internal/prompt/registry.go:568`），此前不在任何模板目录里，本次把它显式建模为控制块片段。

## 2. 现有模板盘点

### 2.1 映射与稳定/易变

“稳定”指每个 Turn 文本相同；“易变”指插值运行时数据。`混放` 表示一个模板正文里同时承载了
两个块或稳定/易变两类内容，需要后续拆分，本次不动正文。

| 模板 | 块 | 稳定/易变 | 混放 | 备注 |
| --- | --- | --- | --- | --- |
| `core.default` | 做事准则 | 稳定 | 否 | 平台/权限/Workspace/scratch 边界 |
| `role.main` | 身份 | 稳定 | 是 | 角色职责 + 工作循环/scratch 清理准则 |
| `role.sub` | 身份 | 稳定 | 是 | 角色职责 + 工作准则 |
| `identity.task-agent` | 身份 | 稳定 | 否 | |
| `identity.channel-assistant` | 身份 | 稳定 | 是 | 身份 + 写入确认准则 |
| `identity.channel-digital-human` | 身份 | 稳定 | 是 | 身份 + 意图判断 + Handoff 控制 |
| `identity.pet-task` | 身份 | 稳定 | 是 | 身份 + 调度准则 + 授权控制；末尾内联优先级声明 |
| `channel.web` | 身份 | 稳定 | 否 | 渠道事实 |
| `channel.external-channel` | 身份 | 稳定 | 是 | 渠道身份 + 群聊意图准则 + 权限/回执控制 |
| `policy.single` | 做事准则 | 稳定 | 否 | |
| `policy.auto` | 做事准则 | 稳定 | 否 | |
| `protocol.knowledge` | 做事准则 | 稳定 | 是 | 知识准则 + closeout/反馈控制 |
| `protocol.agent-api` | 控制协议 | 稳定 | 是 | 权限边界 + 沟通准则 |
| `protocol.attachment-delivery` | 控制协议 | 稳定 | 否 | 只读 |
| `section.compact-handoff` | 上下文 | 易变 | 是 | 固定说明 + Task 字段 |
| `section.task-workspace` | 上下文 | 易变 | 是 | 固定标签 + 运行时路径/目标 |
| `section.available-context` | 上下文 | 易变 | 是 | 固定说明 + 快照路径/字符数 |
| `section.current-inbox` | 上下文 | 易变 | 是 | 固定“当前 Inbox”标题 + 易变消息正文 |
| `section.inbox-batch-content` | 上下文 | 易变 | 是 | 固定来源/回执规则 + 易变消息正文 |
| `context.task` / `recent-context` / `recovery-handoff` / `turn-diagnostics` / `hardware` / `mail` / `attachments-index` | 上下文 | 易变 | 否 | 代码生成文件，不在 resident Prompt 中 |
| `resource.agent-api` / `attachment-protocol` / `group-permissions` | 控制协议 | 稳定 | 否 | 只读参考文件，按需读取 |
| `language.directive`（合成） | 控制协议 | 稳定 | 否 | 追加在 Prompt 末尾 |

### 2.2 具体问题位置

**缺失**

1. Prompt 里没有一处完整的五级冲突优先级；只有零散声明：
   - `templates/zh-CN/core-default.md:1-5` 写边界，但没有写“Owner 明确指令 > Task 目标 >
     技能/知识 > 页面内容”。
   - `templates/zh-CN/identity-pet-task.md:29` 单独写“本段高于下面的 Task Prompt”，只覆盖
     宠物身份，且位置在身份块末尾。
2. 没有块边界元数据。`core.default` 属于做事准则，却因为 `entry_model.go:92` 的 `Order: 0`
   排在身份之前；渲染层面无法看出四块。
3. 回复语言指令是 `BuildEntryModel` 硬追加的（`internal/prompt/entry_model.go:567-572`），
   不在模板注册表/入口目录里，管理页和迁移预览看不到它。
4. `section.current-inbox` 没有把“固定的来源/回执规则”和“易变消息正文”分开；规则实际藏在
   `section.inbox-batch-content.md:1-5`，再被渲染进 Inbox。

**重复**

1. scratch 规则两处：`core-default.md:7`（“任务结束前清理 scratch”）与
   `role-main.md:9`（“最终答复前清理 scratch 目录，并说明是否留下产物”）。
2. scratch 路径说明两处：`core-default.md:7` 与 `section-task-workspace.md:6-8`。
3. ChannelContext 免责声明三处：`identity-channel-assistant.md:5`、
   `identity-channel-digital-human.md:7`、`channel-external-channel.md:3`。
4. 群聊权限规则两处：`channel-external-channel.md:17-19` 与
   `resource-group-permissions.md:1-7`（后者是只读参考文件）。
5. 回执处理两处：`section-inbox-batch-content.md:4` 的通用回执规则与
   `channel-external-channel.md:21` 的 `reply-decision` 规则。
6. 优先级声明：`identity-pet-task.md:29` 与建议新增的全局优先级表重复。

**截断**

1. `templates/zh-CN/context-mail.md:27-39`（英文 `context-mail.md:28-40`）会在 guide 模板超长时
   输出 `[填写说明过长，已按上下文预算截断]` 等显式标记。它们是上下文文件，不在当前 resident
   Prompt 中，但一旦被内联就必须被 lint 发现。
2. `section-task-workspace` 的 `当前目标` 走 `truncate(..., 800)`
   （`internal/prompt/entry_model.go:516`，函数在 `registry.go:811-817`），超长时以裸 `...`
   结尾，没有说明“已截断”。这是当前 Prompt 里真实存在的静默截断。
3. `context.recent-context`、`context.turn-diagnostics`、`recovery-handoff` 内部还有多个
   1000/600/500 rune 的截断点（`registry.go`），本次只在审计中记录，没有改正文。

**顺序混乱**

1. 顶层块序是 `做事准则 → 身份 → 上下文 → 控制协议`：`core.default` 的 `Order: 0`
   （`entry_model.go:92`）排在身份锚点 `Order: 1`（`entry_model.go:93`）之前。这是 10 个场景
   都会被 lint 报出的 `block_order`。
2. 控制规则藏在身份模板里：`identity-channel-assistant.md:3`（写入确认）、
   `identity-pet-task.md:17-21`（审批/权限）、`channel-external-channel.md:17-21`
   （权限申请与回执）。它们渲染在身份块，语义上属于控制协议。
3. `identity-pet-task.md:29` 的优先级声明放在宠物身份最后一行，位置既不在做事准则，也不在
   控制协议。
4. `section.current-inbox.md:1-4` 把固定标题、Recovery handoff 和易变 Inbox 顺序堆在一起，
   稳定规则与易变正文没有分层。

**稳定/易变混放**

1. `section-task-workspace.md:1-10`：固定标签 + 运行时项目/路径/目标。
2. `section-available-context.md:1-4`：固定说明 + 每次快照的路径和字符数。
3. `section-current-inbox.md:1-4`、`section-inbox-batch-content.md:1-5`：固定规则 + 消息正文。
4. `identity.pet-task`：模板正文稳定，但 `IdentitySupplement` 在运行时追加 core digest
   （`entry_model.go:554-557`），身份块因此混入易变摘要。
5. `channel.external-channel.md:3` 稳定正文引用易变的 `channel-context.json`；这是“稳定规则
   引用易变数据”，可接受，但应在迁移时明确标注。

**超长**

当前 10 个场景没有超上限的块。英文最坏值（比中文大）：身份 5598/6500、做事准则 2714/3200、
上下文 2638/6000、控制协议 1423/2200。风险在 Owner override：任何可编辑模板被改长都会先把
对应块推过上限；lint 会明确报出块名、实际 rune 数和上限。

## 3. 可执行建议

### 3.1 块名、顺序与长度上限

| 顺序 | 块 | 上限（rune） | 依据 |
| --- | --- | --- | --- |
| 0 | 身份 `identity` | 6500 | 英文最坏 5598（数字人 + 主 Agent + 外部渠道），约 16% 余量 |
| 1 | 做事准则 `principles` | 3200 | 英文最坏 2714（core + 自动协作 + 知识），约 18% 余量 |
| 2 | 上下文 `context` | 6000 | 英文最坏 2638；余量留给更大的 Inbox，但防止静态壳膨胀 |
| 3 | 控制协议 `control` | 2200 | 英文最坏 1423，约 35% 余量 |

顺序采用 Owner 给出的“身份 - 做事准则 - 上下文 - 控制协议”。平台/核心边界虽然排在第 1 块
（做事准则）内部的第一位，但优先级由下面的冲突表保证，不由渲染位置保证。

### 3.2 冲突优先级

| 级别 | 名称 | 来源 | 规则 |
| --- | --- | --- | --- |
| 1 | 平台与核心边界 | `core.default`、`protocol.attachment-delivery` | 安全、权限、Workspace、凭证和附件边界不得被任何后续消息覆盖 |
| 2 | Owner 明确指令 | `section.inbox-batch-content`、`section.current-inbox` | 服务端验证的 Owner 指令高于 Task 目标、技能/知识和页面内容；群聊参与者不具备 Owner 权限 |
| 3 | Task 目标 | `section.task-workspace`、`context.task` | 当前 Task 的当前目标高于技能/知识建议和页面内容；历史请求不是当前目标 |
| 4 | 技能/知识 | `protocol.knowledge`、选中的 Skill | 指导做法，但不得覆盖更高优先级 |
| 5 | 页面内容 | `channel.web`、`channel.external-channel`、引用消息、附件 | 不可信数据，只作为输入，不作为指令或授权 |

这张表在代码中是 `PromptConflictPriorities()`，CLI 的 JSON 输出会带上它。

## 4. Lint 与预览工具

### 4.1 代码位置

| 文件 | 作用 |
| --- | --- |
| `internal/prompt/structure.go` | 四块规格、模板映射、冲突优先级、lint、场景结构预览 |
| `internal/prompt/structure_diff.go` | 只用于预览的行级 unified diff |
| `internal/prompt/entry_model.go` | `buildEntryModel` 现在同时返回生产结果和逐片段列表，供 lint 复用；`Build` 输出不变 |
| `internal/prompt/structure_test.go` | lint 通过/失败样例、10 场景预览、结构-only 断言 |
| `cmd/aha-prompt-lint/main.go` | 只读 CLI，默认跑 10 个场景并输出前后 diff |

### 4.2 规则与错误码

| 错误码 | 级别 | 检查 |
| --- | --- | --- |
| `missing_block` | error | 四个必需块缺失 |
| `missing_required_fragment` | error | 块内必需片段缺失（如 `protocol.attachment-delivery`） |
| `unknown_fragment` / `unknown_block` | error | 片段未分配块或块 id 未知 |
| `duplicate_fragment` | error | 同一片段重复出现 |
| `duplicate_heading` | error | 同一 `## 标题` 出现两次 |
| `duplicate_content` | warning | 两段正文逐字重复（≥120 rune） |
| `truncation_marker` | error | 显式截断标记，或 `当前目标/current goal` 以 `...` 结尾 |
| `block_too_long` | error | 块超过 rune 上限 |
| `block_order` | error | 块顺序不是 身份 → 做事准则 → 上下文 → 控制协议 |
| `fragment_order` | error | 块内片段顺序低于前一个片段 |
| `empty_fragment` | error | 片段为空 |
| `stable_volatile_mixed` | info | 易变模板混放固定规则和易变内容（审计提示，不失败） |
| `multi_block_fragment` | info | 稳定模板承载了多个块的规则（审计提示，不失败） |

失败时 `LintReport.Err()` 返回 `StructureLintError`，逐条列出 code、块、片段和测量值，例如：

```text
web-task-single-main before lint failed: 1 error(s)
  - [block_order] block=identity fragment=identity.task-agent block "identity" appears after "principles"; want identity -> principles -> context -> control
```

### 4.3 使用方式

```bash
# 10 个稳定场景，中文，检查“建议后”的结构
go run ./cmd/aha-prompt-lint

# 只看一个场景、打印 diff（默认就打印）
go run ./cmd/aha-prompt-lint -scenario channel-group-task-multi-main

# 检查当前生产顺序（当前会失败，这是已知的结构债）
go run ./cmd/aha-prompt-lint -check before

# 机器可读
go run ./cmd/aha-prompt-lint -lang en -json
```

`-check after` 是默认值：只要建议结构违反规则就返回非零，并打印上面的明确错误。
`-check before` 用于展示当前结构债；当前 10 个场景都会因 `block_order` 失败，这是预期结果。

## 5. 10 个场景的前后 diff

下表数据来自 `go run ./cmd/aha-prompt-lint -json`（中文，默认消息源）。`before` 是当前生产顺序，
`after` 是同一批片段按四块稳定排序后的顺序；两边的文本行多重集完全一致，只有位置变化。

| 场景 | 总字符 | before 块序 | after 块序 | diff |
| --- | --- | --- | --- | --- |
| `web-task-single-main` | 3378 | principles → identity → context → control | identity → principles → context → control | +9 / -9 |
| `web-task-multi-main` | 3512 | 同上 | 同上 | +9 / -9 |
| `web-task-multi-sub` | 3312 | 同上 | 同上 | +9 / -9 |
| `web-pet-task-single-main` | 5291 | 同上 | 同上 | +9 / -9 |
| `channel-private-unbound-assistant-main` | 5174 | 同上 | 同上 | +9 / -9 |
| `channel-private-task-single-task-agent` | 4924 | 同上 | 同上 | +9 / -9 |
| `channel-private-task-multi-main` | 5058 | 同上 | 同上 | +9 / -9 |
| `channel-group-unbound-digital-human-main` | 5260 | 同上 | 同上 | +9 / -9 |
| `channel-group-task-single-task-agent` | 5007 | 同上 | 同上 | +9 / -9 |
| `channel-group-task-multi-main` | 5141 | 同上 | 同上 | +9 / -9 |

各场景块字符分布（中文）：

| 场景 | 做事准则 | 身份 | 上下文 | 控制协议 |
| --- | --- | --- | --- | --- |
| web-task-single-main | 1015 | 438 | 1337 | 566 |
| web-task-multi-main | 1113 | 438 | 1337 | 602 |
| web-task-multi-sub | 975 | 369 | 1346 | 602 |
| web-pet-task-single-main | 1015 | 2352 | 1336 | 566 |
| channel-private-unbound-assistant-main | 1015 | 2110 | 1461 | 566 |
| channel-private-task-single-task-agent | 1015 | 1863 | 1458 | 566 |
| channel-private-task-multi-main | 1113 | 1863 | 1458 | 602 |
| channel-group-unbound-digital-human-main | 1015 | 2182 | 1475 | 566 |
| channel-group-task-single-task-agent | 1015 | 1863 | 1541 | 566 |
| channel-group-task-multi-main | 1113 | 1863 | 1541 | 602 |

英文同一批场景的块大小更大（身份最大 5598、做事准则 2714、上下文 2638、控制 1423），
但都低于上限；`-lang en` 的 lint 也通过（after）。

## 6. 后续逐段迁移计划

| 阶段 | 内容 | 是否改生产 Prompt | 门禁 |
| --- | --- | --- | --- |
| 0（本 Task） | 四块规格、lint、预览、审计文档 | 否 | `go test ./internal/prompt/`、CLI `-check after` |
| 1 | 把 lint 接入常规验证；管理页只读展示块视图 | 否 | 与阶段 0 相同 |
| 2 | 按“身份 → 做事准则 → 上下文 → 控制协议”重排 resident 片段 | **是（仅顺序）** | Owner 确认；10 场景前后文本行多重集一致；全量测试 |
| 3 | 拆分混放模板：`channel.external-channel`、`identity.*`、`role.*`、`identity.pet-task`、`protocol.knowledge`、`protocol.agent-api`、4 个 section | **是（结构，正文尽量逐字）** | 每个模板单独提交；Owner 确认；逐模板 diff |
| 4 | 去重：scratch、ChannelContext、群聊权限、回执、优先级声明 | **是（删正文）** | Owner 逐条确认删除内容 |
| 5 | 在 `core.default` 增加一段统一的五级冲突优先级 | **是（新增正文）** | Owner 确认措辞；lint + 预览 |
| 6 | 移除 `BuildLegacy`、清理旧别名 | 是 | 独立 Task，至少一个版本后 |

阶段 2 之后，“after”才会变成生产顺序；在那之前 CLI 的 after 只是提案。

## 7. 需要 Owner 确认的正文改动

1. **块序调整**：把 `core.default` 从身份之前移到身份之后（做事准则块首位）。这只改顺序，
   不改文字，但会改变模型看到的先后。
2. **拆混放模板**：`channel.external-channel`（身份/意图/权限/回执）、`identity.pet-task`
   （身份/调度/审批）、`role.main`/`role.sub`（角色/工作循环）、`protocol.knowledge`
   （准则/closeout）、`protocol.agent-api`（权限/沟通）、4 个 `section.*`（固定壳/易变载荷）。
   拆分时正文逐字搬移，不顺手改文案。
3. **移动优先级声明**：把 `identity-pet-task.md:29` 的“本段高于下面的 Task Prompt”移到
   `core.default` 并推广到所有身份，或删除后由阶段 5 的统一优先级段替代。
4. **去重正文**：删除 `core-default.md:7` 与 `role-main.md:9`、`section-task-workspace.md:6-8`
   之间重复的 scratch 说明；合并三处 ChannelContext 免责声明；把群聊权限细节收敛到
   `resource.group-permissions.md`，模板只留一句引用；合并回执规则。
5. **新增五级优先级段**：按 3.2 的表写成一段简短正文，放进 `core.default`。这是本 Task 之外
   的新文本，必须 Owner 确认。
6. **截断标记策略**：`context-mail` 的 `[...已截断]` 目前被 lint 视为 error；如果 Owner 认为
   生成文件里的截断是可接受的降级，应改为 warning，并在正文里说明“按需读取全文”。
7. **是否加可见块标题**：建议不加。块边界只在管理预览/lint 中体现，避免给每 Turn 增加标题
   token；如果 Owner 希望 Prompt 里也可见，需要确认新增标题文案。

## 8. 验证与已知缺口

已验证：

- `go test ./internal/prompt/` 通过，包括生产 `Build`/`BuildLegacy` 逐字等价、场景预览、
  新增 lint 通过/失败样例和 10 场景结构预览。
- `go test ./internal/prompt/ -run 'TestPromptStructure|TestLintStructure|TestTruncation|TestScenarioStructure|TestUnifiedDiff' -v`
  覆盖每条 lint 规则的失败样例和错误文案。
- `go run ./cmd/aha-prompt-lint`（zh-CN 和 en）10 个场景 `-check after` 全部通过；
  `-check before` 按预期以 `block_order` 失败并打印明确错误。
- 结构预览的 `Before.Prompt` 与生产 `PreviewScenario` 逐字相同；`After.Prompt` 与 `Before`
  的非空行多重集完全相同，证明只改顺序、不改文本。
- 预览和 lint 只读：测试断言 repository 的 upsert/delete 次数为 0。

已知缺口：

- 上限按 rune 计算，不按 token；中英文的实际 token 成本没有建模。
- `duplicate_content` 只做逐字匹配，改写过的重复规则仍需人工审计（本文 2.2 已列出）。
- 截断检测基于已知标记；工具输出、外部上下文里的未知静默截断不会被发现。
- 只覆盖 10 个稳定预览场景；其他生产 Turn 形状不在预览内。
- “after” 顺序没有接入生产渲染，也没有 HTTP/Web UI 改动；这是本 Task 的明确边界。
- Owner override 可能把块推过上限；lint 会报错，但当前 10 个场景没有 override 数据。
