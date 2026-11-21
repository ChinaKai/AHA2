# 宠物 MCP 全覆盖实现记录

完整目标是让宠物有 owner Web 业务操作对应的入口，以及普通 Task 的所有适用能力。当前只完成首批公共门禁、Turn 和硬件实现，整体仍在进行；此目录不能作为完成证明。

基线 `a3ed6cb566680ff079d660685126a2b77a76bad8`，分支 `aha/task_e3d0848747ddc2018605`，工作目录为本 Task 独立 worktree。所有提交保留在此分支，不合并 main、不推送、不部署、不操作生产资源。

`coverage.tsv` 逐行记录实际注册的 HTTP 操作、源文件、handler、现有或新增 MCP、权限、安全交接与测试。source 列相对 `internal/httpapi/`。它目前是路由级账本，后续还需展开同一入口的业务子操作，并完成全部映射与效果验证。`web-call-sites.tsv` 保存前端字面 API 调用点与方法提示；方法提示不能代替完整 TypeScript 调用链审查。

```sh
python3 scripts/pet-mcp-coverage.py            # 检查注册与账本一致
python3 scripts/pet-mcp-coverage.py --update   # 更新后逐项审查差异
python3 scripts/pet-mcp-coverage.py --complete # 最终覆盖门禁，当前必须失败
python3 scripts/test_pet_mcp_coverage.py
```

未完成项使用 pending 或 implemented/implemented_unverified。后两个状态只说明已有代码，仍不能通过最终门禁。transport_na 只用于协议、静态资源等传输入口；web_prohibited_na 必须能证明 Web 自身也禁止该操作。受保护功能需要真实申请、状态与 owner 安全输入/授权交接，不能标成 owner_only 后略过。

首批实现中，宠物 core 不再提前退出注入；知识只补指南和反馈，其他知识与 Skill 操作复用宠物已有入口；Workspace、浏览器与硬件按当前绑定、授权和实时查看权限注入。硬件写入另外要求 Main、可写 snapshot、可写资源以及宠物实时 Task 管理权限。普通 Task、只读 Task 和渠道 Turn 的原作用域继续生效。`capabilities` 新增各 MCP 域和硬件写入的实际门禁标志，原 HTTP 能力标志仍表示该 HTTP 面是否存在。

公共 operation adapter 是逐操作固定 handler 和 schema 的适配，不接受任意 URL、方法或 Task 身份，不携带 owner Cookie、CSRF 或 Authorization。复用原 Agent handler 的服务与资源授权；结果只返回指定字段或有界字节，内部错误不回显。凭据、host key 信任和配对暂未新增可执行入口；对应安全交接仍是后续实现范围。

后续仍需完成公共审批/安全输入、硬件完整生命周期、共享控制、托管进程、Task 高级操作、全部 Web 管理域、后端适配与全量回归。浏览器扩展已有未提交草稿，未包含在 P1a 代码提交和该批测试结论中。

review 目录的 packet 按明确提交边界提供完整 diff、关键函数与调用方和验证结果，每份不超过 65536 UTF-8 字节。测试使用 scratch 下的隔离数据库与假执行器；编译、静态检查、fixture 业务测试和真实平台测试分别记录，不能相互替代。
