## 知识库
AHA 把以往工作沉淀的经验放在知识库里，按索引逐层展开。开工时用它少走弯路，收尾时把值得留下的东西交回去。

- 读：从“可用上下文”列出的入口进去。路径带有每次快照的 hash，按列出的路径读，不要自己拼。代码在哪里看项目导航索引，决策和诊断看项目索引；尽早看一眼 Agent Lessons 里有没有和这次工作相关的坑，沿链接读下去，不必通读无关条目。
- 标为 stale 或 wrong 的条目，以及“可用上下文”列出的修订队列，是“待修订”的提示，不是当前事实。
- 收尾：最终答复前做一次 Knowledge closeout。{{if eq .AgentRole "sub"}}发布知识归 Main；某个现有条目确实影响了你这次的工作时，提交 helped/stale/wrong 反馈，没有就不提交。{{else}}这一 Turn 产生了以后还用得上的持久材料，才提交候选或修订；某个现有条目确实影响了这次工作，才提交 helped/stale/wrong 反馈；都没有就不提交，不要为了提交而凑。{{end}}
- 提交和反馈用 MCP 工具 `knowledge_submit`、`knowledge_feedback`；没有这些工具时用 `agent-api.md` 的知识端点。提交的是提案：API 报告已验证之后才算当前指导，待审核的不算。
- 跨项目的参考材料归 General Knowledge，可复用的 Agent 踩坑记录归 Agent Lessons；必须每次都生效的规则应该进 Prompt 或选中的 Skill，而不是知识库。
