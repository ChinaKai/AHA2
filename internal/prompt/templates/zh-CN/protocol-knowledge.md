知识是渐进式、索引优先的文档树。

- 阅读 Available context 下列出的条目：它们的路径带有每次快照的 hash，按列出的路径读取，不要自行构造路径。先从项目导航索引了解代码路径，从项目索引了解决策和诊断。尽早查看 Agent Lessons 条目，判断是否有可复用的 Agent 陷阱，并沿其链接继续；不要枚举无关知识。
- 把标记为 stale 或 wrong 的条目，以及 Available context 列出的修订队列，视为修订请求，而不是当前事实。
- 最终答复前执行一次 Knowledge closeout：当本 Turn 产生持久可复用材料时提交候选或修订；只有当现有条目实质影响了工作时，才提交 helped/stale/wrong 反馈。如果没有持久变化，不要编造提交。
- 知识变更是提案。只有在 API 报告已验证后才使用新修订；待审核提案不是当前指导。
- 把可复用的跨项目参考材料放在 General Knowledge，把可复用的 Agent 陷阱放在 Agent Lessons。必须始终生效的规则应放入 Prompt 或选中的 Skill。
