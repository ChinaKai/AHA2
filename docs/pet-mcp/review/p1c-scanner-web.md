# P1c：覆盖扫描器与 Web 调用点

`c8644634c374d89ec0bcc96ea16fb0a46df4e797..c9aabc1c52e6844933551e0265dab3cbe8b67967`。包含完整扫描脚本、三个防遗漏用例、284 个 Web 字面调用点及使用说明。只读源码扫描，不连接运行服务。注册常量、桌面表路由、嵌套 JS template 均保留；无法解析的新注册写法会报错。动态 HTTP 方法仍明确标为提示，后续要核对业务调用链，不将字面扫描冒充全量语义分析。

调用链：`main` → `routes`/`web_sites` → 与 `coverage.tsv`/`web-call-sites.tsv` 比较；`--complete` 额外拒绝所有未完成状态。完整函数和调用方均在下方新增文件 diff 中。

验证：`TMPDIR="$AHA2_SCRATCH_DIR" PYTHONDONTWRITEBYTECODE=1 python3 scripts/test_pet_mcp_coverage.py` 退出 0，3 tests；`python3 scripts/pet-mcp-coverage.py` 退出 0，425 HTTP operations / 284 Web literal call sites / 411 pending。`--complete` 当前应失败。这里没有把 pending 改成 owner_only 或 N/A，也未声称完整目标已完成。

```diff
diff --git a/docs/pet-mcp/README.md b/docs/pet-mcp/README.md
new file mode 100644
index 0000000..25c95ee
--- /dev/null
+++ b/docs/pet-mcp/README.md
@@ -0,0 +1,24 @@
+# 宠物 MCP 全覆盖实现记录
+
+完整目标是让宠物有 owner Web 业务操作对应的入口，以及普通 Task 的所有适用能力。当前只完成首批公共门禁、Turn 和硬件实现，整体仍在进行；此目录不能作为完成证明。
+
+基线 `a3ed6cb566680ff079d660685126a2b77a76bad8`，分支 `aha/task_e3d0848747ddc2018605`，工作目录为本 Task 独立 worktree。所有提交保留在此分支，不合并 main、不推送、不部署、不操作生产资源。
+
+`coverage.tsv` 逐行记录实际注册的 HTTP 操作、源文件、handler、现有或新增 MCP、权限、安全交接与测试。source 列相对 `internal/httpapi/`。它目前是路由级账本，后续还需展开同一入口的业务子操作，并完成全部映射与效果验证。`web-call-sites.tsv` 保存前端字面 API 调用点与方法提示；方法提示不能代替完整 TypeScript 调用链审查。
+
+```sh
+python3 scripts/pet-mcp-coverage.py            # 检查注册与账本一致
+python3 scripts/pet-mcp-coverage.py --update   # 更新后逐项审查差异
+python3 scripts/pet-mcp-coverage.py --complete # 最终覆盖门禁，当前必须失败
+python3 scripts/test_pet_mcp_coverage.py
+```
+
+未完成项使用 pending 或 implemented/implemented_unverified。后两个状态只说明已有代码，仍不能通过最终门禁。transport_na 只用于协议、静态资源等传输入口；web_prohibited_na 必须能证明 Web 自身也禁止该操作。受保护功能需要真实申请、状态与 owner 安全输入/授权交接，不能标成 owner_only 后略过。
+
+首批实现中，宠物 core 不再提前退出注入；知识只补指南和反馈，其他知识与 Skill 操作复用宠物已有入口；Workspace、浏览器与硬件按当前绑定、授权和实时查看权限注入。硬件写入另外要求 Main、可写 snapshot、可写资源以及宠物实时 Task 管理权限。普通 Task、只读 Task 和渠道 Turn 的原作用域继续生效。`capabilities` 新增各 MCP 域和硬件写入的实际门禁标志，原 HTTP 能力标志仍表示该 HTTP 面是否存在。
+
+公共 operation adapter 是逐操作固定 handler 和 schema 的适配，不接受任意 URL、方法或 Task 身份，不携带 owner Cookie、CSRF 或 Authorization。复用原 Agent handler 的服务与资源授权；结果只返回指定字段或有界字节，内部错误不回显。凭据、host key 信任和配对暂未新增可执行入口；对应安全交接仍是后续实现范围。
+
+后续仍需完成公共审批/安全输入、硬件完整生命周期、共享控制、托管进程、Task 高级操作、全部 Web 管理域、后端适配与全量回归。浏览器扩展已有未提交草稿，未包含在 P1a 代码提交和该批测试结论中。
+
+review 目录的 packet 按明确提交边界提供完整 diff、关键函数与调用方和验证结果，每份不超过 65536 UTF-8 字节。测试使用 scratch 下的隔离数据库与假执行器；编译、静态检查、fixture 业务测试和真实平台测试分别记录，不能相互替代。
diff --git a/docs/pet-mcp/web-call-sites.tsv b/docs/pet-mcp/web-call-sites.tsv
new file mode 100644
index 0000000..6ec150f
--- /dev/null
+++ b/docs/pet-mcp/web-call-sites.tsv
@@ -0,0 +1,285 @@
+source	line	method_hint	expression
+web/src/api.ts	236	GET/see-source	/api/v1/auth/status
+web/src/api.ts	240	POST	/api/v1/auth/register
+web/src/api.ts	244	POST	/api/v1/auth/login
+web/src/api.ts	248	POST	/api/v1/auth/recover
+web/src/api.ts	252	POST	/api/v1/auth/password
+web/src/api.ts	256	POST	/api/v1/auth/logout
+web/src/api.ts	260	GET/see-source	/api/v1/system
+web/src/api.ts	264	GET/see-source	/api/v1/system/data
+web/src/api.ts	268	POST	/api/v1/system/data/cleanup
+web/src/api.ts	274	GET/see-source	/api/v1/system/data/backups
+web/src/api.ts	278	PUT	/api/v1/system/data/conversation/retention
+web/src/api.ts	282	POST	/api/v1/system/data/conversation/preview
+web/src/api.ts	286	POST	/api/v1/system/data/conversation/purge
+web/src/api.ts	290	GET/see-source	/api/v1/system/data/conversation/purge/progress
+web/src/api.ts	294	GET/see-source	/api/v1/system/data/database/offline-compaction
+web/src/api.ts	298	GET/see-source	"/api/v1/system/data/database${refresh ? ""?refresh=1"" : """"}"
+web/src/api.ts	302	PUT	/api/v1/system/data/hardware-io/retention
+web/src/api.ts	306	POST	/api/v1/system/data/hardware-io/preview
+web/src/api.ts	310	POST	/api/v1/system/data/hardware-io/purge
+web/src/api.ts	314	GET/see-source	/api/v1/system/data/hardware-io/purge/progress
+web/src/api.ts	318	GET/see-source	/api/v1/settings/proxy
+web/src/api.ts	322	PUT	/api/v1/settings/proxy
+web/src/api.ts	326	POST	/api/v1/settings/proxy/test
+web/src/api.ts	330	POST	/api/v1/settings/proxy/subscription
+web/src/api.ts	334	POST	/api/v1/settings/proxy/subscription/refresh
+web/src/api.ts	338	POST	/api/v1/settings/proxy/profiles/${encodeURIComponent(id)}/refresh
+web/src/api.ts	342	PATCH	/api/v1/settings/proxy/profiles/${encodeURIComponent(id)}
+web/src/api.ts	346	POST	/api/v1/settings/proxy/profiles/${encodeURIComponent(id)}/activate
+web/src/api.ts	350	DELETE	/api/v1/settings/proxy/profiles/${encodeURIComponent(id)}
+web/src/api.ts	354	POST	/api/v1/settings/proxy/profiles/${encodeURIComponent(profileID)}/nodes/${encodeURIComponent(nodeID)}/test
+web/src/api.ts	358	GET/see-source	/api/v1/settings/security
+web/src/api.ts	362	PUT	/api/v1/settings/security
+web/src/api.ts	366	GET/see-source	/api/v1/settings/network
+web/src/api.ts	370	PUT	/api/v1/settings/network
+web/src/api.ts	374	GET/see-source	/api/v1/settings/agent-api
+web/src/api.ts	378	PUT	/api/v1/settings/agent-api
+web/src/api.ts	382	GET/see-source	/api/v1/settings/backend
+web/src/api.ts	386	GET/see-source	/api/v1/settings/general
+web/src/api.ts	390	PUT	/api/v1/settings/general
+web/src/api.ts	394	PUT	/api/v1/settings/backend
+web/src/api.ts	398	GET/see-source	/api/v1/settings/sync
+web/src/api.ts	402	PUT	/api/v1/settings/sync
+web/src/api.ts	406	GET/see-source	/api/v1/settings/sync/status
+web/src/api.ts	410	GET/see-source	/api/v1/channel-providers
+web/src/api.ts	414	PATCH	/api/v1/channel-plugins/${encodeURIComponent(id)}
+web/src/api.ts	422	GET/see-source	/api/v1/channel-instances
+web/src/api.ts	426	GET/see-source	/api/v1/channel-instances/${encodeURIComponent(id)}
+web/src/api.ts	431	POST	/api/v1/channel-instances
+web/src/api.ts	439	PATCH	/api/v1/channel-instances/${encodeURIComponent(id)}
+web/src/api.ts	447	PATCH	/api/v1/channel-instances/${encodeURIComponent(id)}
+web/src/api.ts	451	POST	/api/v1/channel-instances/${encodeURIComponent(id)}/reset-binding
+web/src/api.ts	457	POST	/api/v1/channel-instances/${encodeURIComponent(id)}/archive
+web/src/api.ts	463	GET/see-source	/api/v1/channel-instances/${encodeURIComponent(id)}/purge-preview
+web/src/api.ts	467	POST	/api/v1/channel-instances/${encodeURIComponent(id)}/purge
+web/src/api.ts	473	PUT	/api/v1/channel-instances/${encodeURIComponent(id)}/credentials
+web/src/api.ts	481	GET/see-source	/api/v1/channel-instances/${encodeURIComponent(id)}/onboarding-options
+web/src/api.ts	486	POST	/api/v1/channel-instances/${encodeURIComponent(id)}/runtime-menus/reset
+web/src/api.ts	493	POST	/api/v1/channel-instances/${encodeURIComponent(id)}/onboarding-sessions
+web/src/api.ts	499	GET/see-source	/api/v1/channel-onboarding-sessions/${encodeURIComponent(id)}
+web/src/api.ts	503	POST	/api/v1/channel-onboarding-sessions/${encodeURIComponent(id)}/cancel
+web/src/api.ts	507	GET/see-source	/api/v1/channel-instances/${encodeURIComponent(id)}/conversations
+web/src/api.ts	511	GET/see-source	/api/v1/channel-instances/${encodeURIComponent(id)}/handoffs
+web/src/api.ts	515	GET/see-source	/api/v1/channel-instances/${encodeURIComponent(id)}/deliveries
+web/src/api.ts	519	GET/see-source	/api/v1/channel-deliveries/${encodeURIComponent(id)}/attempts
+web/src/api.ts	523	POST	/api/v1/channel-deliveries/${encodeURIComponent(id)}/replay
+web/src/api.ts	527	POST	/api/v1/channel-deliveries/${encodeURIComponent(id)}/skip
+web/src/api.ts	531	GET/see-source	/api/v1/channel-instances/${encodeURIComponent(id)}/knowledge-policy
+web/src/api.ts	535	PUT	/api/v1/channel-instances/${encodeURIComponent(id)}/knowledge-policy
+web/src/api.ts	539	GET/see-source	/api/v1/settings/sync/preview
+web/src/api.ts	543	POST	/api/v1/settings/sync/run
+web/src/api.ts	547	GET/see-source	/api/v1/settings/sync/conflicts
+web/src/api.ts	551	POST	/api/v1/settings/sync/conflicts/${encodeURIComponent(id)}/resolve
+web/src/api.ts	559	GET/see-source	"/api/v1/projects${query.size ? `?${query}` : """"}"
+web/src/api.ts	563	POST	/api/v1/projects
+web/src/api.ts	567	DELETE	/api/v1/projects/${encodeURIComponent(id)}
+web/src/api.ts	571	PUT	/api/v1/projects/${encodeURIComponent(id)}
+web/src/api.ts	576	GET/see-source	/api/v1/workspaces${query}
+web/src/api.ts	580	POST	/api/v1/workspaces
+web/src/api.ts	584	POST	/api/v1/workspaces/${id}/detect
+web/src/api.ts	588	GET/see-source	/api/v1/workspaces/${encodeURIComponent(id)}/host-key
+web/src/api.ts	592	POST	/api/v1/workspaces/${encodeURIComponent(id)}/host-key/trust
+web/src/api.ts	598	DELETE	/api/v1/workspaces/${encodeURIComponent(id)}
+web/src/api.ts	602	DELETE	/api/v1/workspaces/${encodeURIComponent(id)}/remote-mirror
+web/src/api.ts	606	PUT	/api/v1/workspaces/${encodeURIComponent(id)}
+web/src/api.ts	610	POST	/api/v1/workspaces/${encodeURIComponent(id)}/takeover
+web/src/api.ts	614	GET/see-source	/api/v1/models
+web/src/api.ts	618	POST	/api/v1/models
+web/src/api.ts	622	GET/see-source	/api/v1/env-groups
+web/src/api.ts	626	POST	/api/v1/env-groups
+web/src/api.ts	630	POST	/api/v1/providers/detect-models
+web/src/api.ts	634	POST	/api/v1/providers/${encodeURIComponent(providerID)}/model-probes
+web/src/api.ts	640	POST	/api/v1/providers/add-models
+web/src/api.ts	644	GET/see-source	/api/v1/providers
+web/src/api.ts	648	POST	/api/v1/providers
+web/src/api.ts	652	DELETE	/api/v1/providers/${encodeURIComponent(id)}
+web/src/api.ts	656	PUT	/api/v1/providers/${encodeURIComponent(id)}
+web/src/api.ts	660	DELETE	/api/v1/models/${encodeURIComponent(id)}
+web/src/api.ts	664	POST	/api/v1/models/batch-delete
+web/src/api.ts	668	PUT	/api/v1/models/${encodeURIComponent(id)}
+web/src/api.ts	672	POST	/api/v1/models/${encodeURIComponent(id)}/health-check
+web/src/api.ts	676	GET/see-source	/api/v1/codex-accounts
+web/src/api.ts	680	POST	/api/v1/codex-accounts/import-local
+web/src/api.ts	684	POST	/api/v1/codex-accounts/login
+web/src/api.ts	690	POST	/api/v1/codex-accounts/login/${encodeURIComponent(id)}/callback
+web/src/api.ts	696	DELETE	/api/v1/codex-accounts/${encodeURIComponent(id)}
+web/src/api.ts	700	POST	/api/v1/codex-accounts/${encodeURIComponent(id)}/refresh
+web/src/api.ts	722	GET/see-source	"/api/v1/tasks${query.size ? `?${query}` : """"}"
+web/src/api.ts	726	GET/see-source	/api/v1/task-clone-sources
+web/src/api.ts	730	GET/see-source	/api/v1/pet/approvals/pending-count
+web/src/api.ts	738	GET/see-source	"/api/v1/pet/workbench${suffix ? `?${suffix}` : """"}"
+web/src/api.ts	742	PUT	/api/v1/pet/workbench/${encodeURIComponent(section)}/${encodeURIComponent(name)}
+web/src/api.ts	748	POST	/api/v1/pet/watches/${encodeURIComponent(taskID)}
+web/src/api.ts	752	DELETE	/api/v1/pet/watches/${encodeURIComponent(taskID)}
+web/src/api.ts	756	PATCH	/api/v1/pet/watches/${encodeURIComponent(taskID)}
+web/src/api.ts	764	POST	/api/v1/pet/approvals/${encodeURIComponent(id)}/resolve
+web/src/api.ts	770	GET/see-source	/api/v1/mcp-servers
+web/src/api.ts	774	GET/see-source	/api/v1/mcp-servers/${encodeURIComponent(id)}
+web/src/api.ts	778	POST	/api/v1/mcp-servers/test
+web/src/api.ts	782	POST	/api/v1/mcp-servers
+web/src/api.ts	786	DELETE	/api/v1/mcp-servers/${encodeURIComponent(id)}
+web/src/api.ts	790	PUT	/api/v1/tasks/${encodeURIComponent(id)}/mcp-servers
+web/src/api.ts	794	POST	/api/v1/tasks
+web/src/api.ts	798	POST	/api/v1/tasks/${encodeURIComponent(id)}/start
+web/src/api.ts	802	GET/see-source	/api/v1/tasks/${id}
+web/src/api.ts	806	POST	/api/v1/tasks/${encodeURIComponent(taskID)}/permission-requests/${encodeURIComponent(requestID)}/resolve
+web/src/api.ts	820	GET/see-source	/api/v1/tasks/${id}/conversation?${query}
+web/src/api.ts	833	GET/see-source	/api/v1/tasks/${id}/agents/${encodeURIComponent(agentID)}/conversation?${query}
+web/src/api.ts	837	GET/see-source	/api/v1/tasks/${id}/context
+web/src/api.ts	841	GET/see-source	/api/v1/tasks/${id}/agents/${encodeURIComponent(agentID)}/context
+web/src/api.ts	845	GET/see-source	/api/v1/tasks/${id}/agents
+web/src/api.ts	849	DELETE	/api/v1/tasks/${encodeURIComponent(id)}
+web/src/api.ts	853	DELETE	/api/v1/tasks/${encodeURIComponent(id)}/remote-mirror
+web/src/api.ts	857	POST	/api/v1/tasks/${encodeURIComponent(id)}/takeover
+web/src/api.ts	861	GET/see-source	/api/v1/tasks/${encodeURIComponent(id)}/channel-routes
+web/src/api.ts	865	POST	/api/v1/tasks/${encodeURIComponent(id)}/channel-routes
+web/src/api.ts	871	DELETE	/api/v1/tasks/${encodeURIComponent(id)}/channel-routes/${encodeURIComponent(routeID)}
+web/src/api.ts	875	POST	/api/v1/tasks/${encodeURIComponent(id)}/channel-members:refresh
+web/src/api.ts	879	PUT	/api/v1/tasks/${encodeURIComponent(id)}/channel-contacts
+web/src/api.ts	885	POST	/api/v1/tasks/${taskID}/messages
+web/src/api.ts	889	POST	/api/v1/tasks/${taskID}/agents/${encodeURIComponent(agentID)}/messages
+web/src/api.ts	898	POST	/api/v1/tasks/${encodeURIComponent(taskID)}/attachments
+web/src/api.ts	902	DELETE	/api/v1/tasks/${encodeURIComponent(taskID)}/attachments/${encodeURIComponent(attachmentID)}
+web/src/api.ts	906	POST	/api/v1/tasks/${id}/complete
+web/src/api.ts	910	POST	/api/v1/tasks/${id}/reopen
+web/src/api.ts	914	PATCH	/api/v1/tasks/${encodeURIComponent(id)}
+web/src/api.ts	918	PATCH	/api/v1/tasks/${encodeURIComponent(id)}
+web/src/api.ts	922	PATCH	/api/v1/tasks/${id}/agents/${encodeURIComponent(agentID)}
+web/src/api.ts	929	POST	/api/v1/tasks/${id}/agents/${encodeURIComponent(agentID)}/session/compact
+web/src/api.ts	935	POST	/api/v1/tasks/${id}/agents/${encodeURIComponent(agentID)}/session/reset
+web/src/api.ts	941	GET/see-source	/api/v1/hardware/serial-ports
+web/src/api.ts	945	GET/see-source	/api/v1/hardware-resources
+web/src/api.ts	949	POST	/api/v1/hardware-resources
+web/src/api.ts	955	PUT	/api/v1/hardware-resources/${encodeURIComponent(id)}
+web/src/api.ts	961	DELETE	/api/v1/hardware-resources/${encodeURIComponent(id)}
+web/src/api.ts	967	GET/see-source	/api/v1/browser/profiles
+web/src/api.ts	971	POST	/api/v1/browser/profiles
+web/src/api.ts	975	PATCH	/api/v1/browser/profiles/${encodeURIComponent(id)}
+web/src/api.ts	981	DELETE	/api/v1/browser/profiles/${encodeURIComponent(id)}?confirm=1
+web/src/api.ts	985	POST	/api/v1/browser/profiles/${encodeURIComponent(id)}/login-window
+web/src/api.ts	991	DELETE	/api/v1/browser/profiles/${encodeURIComponent(id)}/session
+web/src/api.ts	995	GET/see-source	/api/v1/browser/host
+web/src/api.ts	999	GET/see-source	/api/v1/tasks/${encodeURIComponent(taskID)}/browser
+web/src/api.ts	1006	GET	/api/v1/browser-grants/${encodeURIComponent(grantID)}/tabs/${encodeURIComponent(tabID)}/frame
+web/src/api.ts	1021	POST	/api/v1/tasks/${encodeURIComponent(taskID)}/browser-sessions
+web/src/api.ts	1027	POST	/api/v1/browser-grants/${encodeURIComponent(grantID)}/tabs
+web/src/api.ts	1033	DELETE	/api/v1/browser-grants/${encodeURIComponent(grantID)}/tabs/${encodeURIComponent(tabID)}
+web/src/api.ts	1039	POST	/api/v1/browser-grants/${encodeURIComponent(grantID)}/tabs/${encodeURIComponent(tabID)}/activate
+web/src/api.ts	1045	GET/see-source	/api/v1/browser-grants/${encodeURIComponent(grantID)}/bookmarks
+web/src/api.ts	1049	DELETE	/api/v1/browser-grants/${encodeURIComponent(grantID)}
+web/src/api.ts	1053	POST	/api/v1/browser/profiles/${encodeURIComponent(profileID)}/transfer
+web/src/api.ts	1059	POST	/api/v1/hardware-resources/${encodeURIComponent(id)}/adb/pair
+web/src/api.ts	1065	PUT	/api/v1/tasks/${id}/hardware
+web/src/api.ts	1071	PUT	/api/v1/tasks/${encodeURIComponent(id)}/hardware-bindings
+web/src/api.ts	1084	GET/see-source	/api/v1/tasks/${taskID}/hardware/${encodeURIComponent(hardwareID)}/terminal?${query}
+web/src/api.ts	1092	POST	/api/v1/tasks/${taskID}/hardware/${encodeURIComponent(hardwareID)}/connect?transport=${transport}
+web/src/api.ts	1098	GET/see-source	/api/v1/tasks/${taskID}/hardware/${encodeURIComponent(hardwareID)}/host-key?transport=network
+web/src/api.ts	1102	POST	/api/v1/tasks/${taskID}/hardware/${encodeURIComponent(hardwareID)}/host-key/trust?transport=network
+web/src/api.ts	1112	POST	/api/v1/tasks/${taskID}/hardware/${encodeURIComponent(hardwareID)}/disconnect?transport=${transport}
+web/src/api.ts	1124	POST	/api/v1/tasks/${taskID}/hardware/${encodeURIComponent(hardwareID)}/send?transport=${transport}
+web/src/api.ts	1130	POST	/api/v1/turns/${id}/interrupt
+web/src/api.ts	1134	POST	/api/v1/rounds/${id}/interrupt
+web/src/api.ts	1146	GET/see-source	/api/v1/knowledge?${query}
+web/src/api.ts	1156	GET/see-source	/api/v1/knowledge/proposals?${query}
+web/src/api.ts	1160	GET/see-source	/api/v1/knowledge/${encodeURIComponent(id)}
+web/src/api.ts	1164	GET/see-source	/api/v1/knowledge-vault/libraries
+web/src/api.ts	1171	GET/see-source	/api/v1/knowledge-vault/tree?${query}
+web/src/api.ts	1176	GET/see-source	/api/v1/channel-instances/${encodeURIComponent(instanceID)}/knowledge-bindings${query}
+web/src/api.ts	1180	PUT	/api/v1/channel-instances/${encodeURIComponent(instanceID)}/knowledge-bindings
+web/src/api.ts	1187	PUT	/api/v1/channel-instances/${encodeURIComponent(instanceID)}/knowledge-bindings/group
+web/src/api.ts	1195	GET/see-source	/api/v1/knowledge-vault/files${query}
+web/src/api.ts	1199	GET/see-source	/api/v1/knowledge-vault/file?path=${encodeURIComponent(path)}
+web/src/api.ts	1203	GET/see-source	/api/v1/knowledge-vault/file/download?path=${encodeURIComponent(path)}
+web/src/api.ts	1207	GET/see-source	/api/v1/knowledge-vault/open-info
+web/src/api.ts	1211	POST	/api/v1/knowledge-vault/reveal?path=${encodeURIComponent(path)}
+web/src/api.ts	1215	GET/see-source	/api/v1/knowledge-vault/doc/${encodeURIComponent(id)}
+web/src/api.ts	1219	GET/see-source	/api/v1/knowledge-vault/doc/${encodeURIComponent(id)}/diff
+web/src/api.ts	1232	GET/see-source	/api/v1/knowledge-vault/search?${query}
+web/src/api.ts	1240	GET/see-source	/api/v1/knowledge-vault/recent?${query}
+web/src/api.ts	1246	GET/see-source	/api/v1/knowledge-vault/inbox?${query}
+web/src/api.ts	1251	GET/see-source	/api/v1/knowledge-vault/skills${query}
+web/src/api.ts	1258	PUT	/api/v1/knowledge-vault/skills/auto-attach
+web/src/api.ts	1267	GET/see-source	/api/v1/settings/knowledge-vault
+web/src/api.ts	1271	PUT	/api/v1/settings/knowledge-vault
+web/src/api.ts	1275	PUT	/api/v1/settings/knowledge-vault
+web/src/api.ts	1279	POST	/api/v1/settings/knowledge-vault/init
+web/src/api.ts	1283	POST	/api/v1/settings/knowledge-vault/rebuild
+web/src/api.ts	1287	GET/see-source	/api/v1/knowledge/libraries
+web/src/api.ts	1294	GET/see-source	/api/v1/knowledge/indexes
+web/src/api.ts	1298	POST	/api/v1/knowledge/libraries/${encodeURIComponent(id)}/bind
+web/src/api.ts	1302	POST	/api/v1/knowledge/libraries/${encodeURIComponent(id)}/unbind
+web/src/api.ts	1306	DELETE	/api/v1/knowledge/libraries/${encodeURIComponent(id)}
+web/src/api.ts	1310	POST	/api/v1/projects/${encodeURIComponent(projectID)}/knowledge/detach
+web/src/api.ts	1314	DELETE	/api/v1/projects/${encodeURIComponent(projectID)}/knowledge
+web/src/api.ts	1318	POST	/api/v1/knowledge/proposals/${encodeURIComponent(id)}/approve
+web/src/api.ts	1322	POST	/api/v1/knowledge/proposals/${encodeURIComponent(id)}/reject
+web/src/api.ts	1326	PUT	/api/v1/settings/knowledge-review
+web/src/api.ts	1330	POST	/api/v1/knowledge/proposals/batch
+web/src/api.ts	1334	POST	/api/v1/knowledge
+web/src/api.ts	1338	PUT	/api/v1/knowledge/${encodeURIComponent(id)}
+web/src/api.ts	1342	DELETE	/api/v1/knowledge/${encodeURIComponent(id)}
+web/src/api.ts	1346	POST	/api/v1/knowledge/${id}/verify
+web/src/api.ts	1350	POST	/api/v1/knowledge/${encodeURIComponent(id)}/feedback
+web/src/api.ts	1354	GET/see-source	/api/v1/projects/${encodeURIComponent(projectID)}/product-lines
+web/src/api.ts	1358	POST	/api/v1/projects/${encodeURIComponent(projectID)}/product-lines
+web/src/api.ts	1362	DELETE	/api/v1/projects/${encodeURIComponent(projectID)}/product-lines/${encodeURIComponent(id)}
+web/src/api.ts	1369	GET/see-source	/api/v1/skills?${query}
+web/src/api.ts	1373	GET/see-source	/api/v1/prompts/templates
+web/src/api.ts	1377	GET/see-source	/api/v1/prompts/entries
+web/src/api.ts	1381	GET/see-source	/api/v1/prompts/entries/${encodeURIComponent(entryID)}/anchors/${encodeURIComponent(anchor)}
+web/src/api.ts	1385	GET/see-source	/api/v1/prompts/migration-preview
+web/src/api.ts	1392	GET/see-source	/api/v1/prompts/scenario-preview?${query.toString()}
+web/src/api.ts	1396	PUT	/api/v1/prompts/templates/${encodeURIComponent(id)}
+web/src/api.ts	1402	POST	/api/v1/prompts/templates/${encodeURIComponent(id)}/reset
+web/src/api.ts	1406	GET/see-source	/api/v1/assistant
+web/src/api.ts	1410	PUT	/api/v1/assistant
+web/src/api.ts	1413	GET/see-source	/api/v1/pet
+web/src/api.ts	1417	PUT	/api/v1/pet
+web/src/api.ts	1420	POST	/api/v1/pet/control
+web/src/api.ts	1424	POST	/api/v1/pet/navigation/claim
+web/src/api.ts	1428	POST	/api/v1/pet/brain
+web/src/api.ts	1432	GET/see-source	/api/v1/pet/prompts
+web/src/api.ts	1436	GET/see-source	/api/v1/pet/activity?limit=${limit}
+web/src/api.ts	1440	PUT	/api/v1/pet/settings
+web/src/api.ts	1444	POST	/api/v1/pet/reset
+web/src/api.ts	1448	POST	/api/v1/assistant/messages
+web/src/api.ts	1452	GET/see-source	/api/v1/assistant/hints
+web/src/api.ts	1456	POST	/api/v1/assistant/hints/dismiss
+web/src/api.ts	1462	GET/see-source	/api/v1/mail-accounts
+web/src/api.ts	1466	POST	/api/v1/mail-accounts
+web/src/api.ts	1470	PUT	/api/v1/mail-accounts/${encodeURIComponent(id)}
+web/src/api.ts	1474	DELETE	/api/v1/mail-accounts/${encodeURIComponent(id)}
+web/src/api.ts	1478	POST	/api/v1/mail-accounts/${encodeURIComponent(id)}/test
+web/src/api.ts	1487	GET/see-source	/api/v1/mail-templates${suffix}
+web/src/api.ts	1491	POST	/api/v1/mail-templates
+web/src/api.ts	1495	PUT	/api/v1/mail-templates/${encodeURIComponent(id)}
+web/src/api.ts	1499	DELETE	/api/v1/mail-templates/${encodeURIComponent(id)}
+web/src/api.ts	1503	DELETE	/api/v1/mail-templates/${encodeURIComponent(id)}?purge=1
+web/src/api.ts	1507	GET/see-source	/api/v1/mail-templates/${encodeURIComponent(id)}/versions
+web/src/api.ts	1511	POST	/api/v1/mail-templates/${encodeURIComponent(id)}/preview
+web/src/api.ts	1515	POST	/api/v1/mail-templates/preview
+web/src/api.ts	1520	GET/see-source	/api/v1/mail-bindings?${query}
+web/src/api.ts	1524	POST	/api/v1/mail-bindings
+web/src/api.ts	1528	PUT	/api/v1/mail-bindings/${encodeURIComponent(id)}
+web/src/api.ts	1532	DELETE	/api/v1/mail-bindings/${encodeURIComponent(id)}
+web/src/api.ts	1542	GET/see-source	/api/v1/mail-messages${suffix}
+web/src/api.ts	1546	POST	/api/v1/mail-messages
+web/src/api.ts	1550	GET/see-source	/api/v1/mail-messages/${encodeURIComponent(id)}
+web/src/api.ts	1554	POST	/api/v1/mail-messages/${encodeURIComponent(id)}/approve
+web/src/api.ts	1560	POST	/api/v1/mail-messages/${encodeURIComponent(id)}/reject
+web/src/api.ts	1566	POST	/api/v1/mail-messages/${encodeURIComponent(id)}/cancel
+web/src/api.ts	1570	POST	/api/v1/mail-messages/${encodeURIComponent(id)}/retry
+web/src/api.ts	1576	GET/see-source	/api/v1/mail-messages/${encodeURIComponent(id)}/attempts
+web/src/api.ts	1580	GET/see-source	/api/v1/mail-messages/${encodeURIComponent(id)}/download-links
+web/src/api.ts	1584	POST	/api/v1/mail-messages/${encodeURIComponent(messageID)}/download-links/${encodeURIComponent(attachmentID)}/revoke
+web/src/api.ts	1588	GET/see-source	/api/v1/tasks/${encodeURIComponent(taskID)}/mail
+web/src/channels.ts	764	GET/see-source	/api/v1/channel-onboarding-sessions/${encodeURIComponent(item.id)}/qr
+web/src/conversation_ui.ts	35	GET/see-source	/api/v1/tasks/${encodeURIComponent(item.task_id)}/attachments/${encodeURIComponent(id)}
+web/src/desktop_panel.ts	1135	GET/see-source	/api/v1/tasks/${encodeURIComponent(state.taskID)}/desktop/stream
+web/src/desktop_panel.ts	1249	POST	/api/v1/tasks/${encodeURIComponent(binding.state.taskID)}/desktop${suffix}
+web/src/knowledge_vault.ts	2254	GET/see-source	/api/v1/knowledge-vault/git/${action}
+web/src/knowledge_vault_settings.ts	307	POST	/api/v1/settings/knowledge-vault/${action}
+web/src/main.ts	5778	GET/see-source	/api/v1/tasks/${encodeURIComponent(item.task_id)}/attachments/${encodeURIComponent(item.id)}
+web/src/main.ts	8844	GET/see-source	/api/v1/tasks/${taskID}/events?after=${after}
+web/src/main.ts	9017	GET/see-source	/api/v1/events
diff --git a/scripts/pet-mcp-coverage.py b/scripts/pet-mcp-coverage.py
new file mode 100644
index 0000000..b790e65
--- /dev/null
+++ b/scripts/pet-mcp-coverage.py
@@ -0,0 +1,158 @@
+#!/usr/bin/env python3
+"""Inventory registered HTTP operations and literal Web call sites.
+
+No runtime database, credentials or network are used. The checked-in ledger is
+deliberately explicit about unfinished operations; --complete is the release
+gate and rejects every pending row. Registration syntax outside the supported
+forms fails closed rather than silently disappearing from the inventory.
+"""
+import argparse
+import csv
+import io
+import re
+from pathlib import Path
+
+ROOT = Path(__file__).resolve().parents[1]
+LEDGER = ROOT / "docs/pet-mcp/coverage.tsv"
+WEB = ROOT / "docs/pet-mcp/web-call-sites.tsv"
+
+
+def routes():
+    rows = {}
+    constants = {}
+    for source_path in (ROOT / "internal/httpapi").glob("*.go"):
+        if not source_path.name.endswith("_test.go"):
+            constants.update(re.findall(r'\b(\w+)\s*=\s*"([^"\n]+)"', source_path.read_text()))
+    for path in sorted((ROOT / "internal/httpapi").glob("*.go")):
+        if path.name.endswith("_test.go"):
+            continue
+        source = path.read_text()
+        for match in re.finditer(r'mux\.Handle(?:Func)?\(([^\n]+)', source):
+            line = match.group(1)
+            symbol = re.match(r'(\w+)\s*,',line)
+            if symbol and symbol.group(1) in constants:
+                line = '"' + constants[symbol.group(1)] + '",' + line[symbol.end():]
+            literal = re.match(r'"([^"\n]+)"\s*,', line)
+            if literal:
+                pattern = literal.group(1)
+                names = re.findall(r'\bs\.([A-Za-z0-9_]+)', line)
+                handler = names[-1] if names else "inline"
+                boundary = "agent_turn" if "withAgentCapability" in line else "owner_session" if "withAuth" in line else "transport_auth"
+                add_route(rows, pattern, path, handler, boundary)
+            elif path.name == "desktop_handlers.go" and line.startswith("route.pattern,"):
+                # Both finite desktop tables are expanded below. Any third
+                # dynamic registration must be taught to this scanner.
+                continue
+            else:
+                raise ValueError(f"unsupported registration: {path.name}: {line}")
+        if path.name == "desktop_handlers.go":
+            if len(re.findall(r'mux\.Handle\(route.pattern,', source)) != 2:
+                raise ValueError("desktop route tables changed; review scanner")
+            for expression, handler in re.findall(r'\{([^\n]+?),\s*s\.(\w+)\},', source):
+                if not re.match(r'"(?:GET|POST|DELETE|PUT|PATCH) ', expression):
+                    continue
+                expression = expression.replace('prefix', '"/api/v1/tasks/{id}/desktop"')
+                parts = re.findall(r'"([^"\n]*)"', expression)
+                remainder = re.sub(r'"[^"\n]*"|[+\s]', '', expression)
+                if remainder:
+                    raise ValueError(f"unsupported desktop pattern: {expression}")
+                pattern = ''.join(parts)
+                add_route(rows, pattern, path, handler, "agent_turn" if "/agent/" in pattern else "owner_session")
+    return rows
+
+
+def add_route(rows, pattern, path, handler, boundary):
+    # Static resources and methodless fallback are still accounted for.
+    method, sep, route = pattern.partition(' ')
+    if not sep:
+        method, route = "ANY", pattern
+    key = f"{method} {route}"
+    if key in rows:
+        raise ValueError(f"duplicate route: {key}")
+    rows[key] = {"operation": key, "source": path.name, "handler": handler, "boundary": boundary}
+
+
+def js_literal_end(source, start):
+    quote = source[start]
+    index = start + 1
+    while index < len(source):
+        char = source[index]
+        if char == '\\':
+            index += 2
+            continue
+        if char == quote:
+            return index + 1
+        if quote == '`' and source.startswith('${', index):
+            index += 2
+            depth = 1
+            while index < len(source) and depth:
+                char = source[index]
+                if char in "\"'`":
+                    index = js_literal_end(source, index)
+                    continue
+                depth += (char == '{') - (char == '}')
+                index += 1
+            if depth:
+                raise ValueError('unterminated Web template expression')
+            continue
+        index += 1
+    raise ValueError('unterminated Web literal')
+
+
+def web_sites():
+    result = []
+    for path in sorted((ROOT / "web/src").rglob("*.ts")):
+        source = path.read_text()
+        # Includes API.request/cache wrappers, raw fetch, downloads and streams.
+        for match in re.finditer(r'["\x27`]\/api\/', source):
+            end = js_literal_end(source, match.start())
+            value = source[match.start() + 1:end - 1]
+            tail = source[end:source.find(';', end) + 1]
+            method = re.search(r'method:\s*["\x27](GET|POST|PUT|PATCH|DELETE)["\x27]', tail)
+            result.append({"source": str(path.relative_to(ROOT)), "line": source.count('\n', 0, match.start()) + 1,
+                           "method_hint": method.group(1) if method else "GET/see-source", "expression": value})
+    return result
+
+
+def encode(fields, rows):
+    output = io.StringIO()
+    writer = csv.DictWriter(output, fields, delimiter='\t', lineterminator='\n')
+    writer.writeheader()
+    writer.writerows(rows)
+    return output.getvalue()
+
+
+def main():
+    parser = argparse.ArgumentParser(description=__doc__)
+    parser.add_argument('--update', action='store_true')
+    parser.add_argument('--complete', action='store_true')
+    args = parser.parse_args()
+    actual = routes()
+    fields = ["operation", "source", "handler", "boundary", "mcp", "permission", "protected_flow", "tests", "status"]
+    previous = {}
+    if LEDGER.exists():
+        previous = {row['operation']: row for row in csv.DictReader(LEDGER.open(), delimiter='\t')}
+    rows = []
+    for key, row in sorted(actual.items()):
+        entry = {**row, **{field: previous.get(key, {}).get(field, "pending") for field in fields[4:]}}
+        rows.append(entry)
+    ledger = encode(fields, rows)
+    sites = web_sites()
+    web = encode(["source", "line", "method_hint", "expression"], sites)
+    if args.update:
+        LEDGER.parent.mkdir(parents=True, exist_ok=True)
+        LEDGER.write_text(ledger)
+        WEB.write_text(web)
+    else:
+        if not LEDGER.exists() or LEDGER.read_text() != ledger:
+            raise SystemExit("HTTP coverage ledger differs from registration; run --update and review every new/changed row")
+        if not WEB.exists() or WEB.read_text() != web:
+            raise SystemExit("Web call-site inventory differs; run --update and review")
+    pending = [row for row in rows if row['status'] not in {'direct', 'equivalent', 'protected_handoff', 'transport_na', 'web_prohibited_na'} or any(row[key] == 'pending' for key in fields[4:])]
+    print(f"HTTP operations: {len(rows)}; Web literal call sites: {len(sites)}; pending operations: {len(pending)}")
+    if args.complete and pending:
+        raise SystemExit("coverage is incomplete")
+
+
+if __name__ == '__main__':
+    main()
diff --git a/scripts/test_pet_mcp_coverage.py b/scripts/test_pet_mcp_coverage.py
new file mode 100644
index 0000000..008a35e
--- /dev/null
+++ b/scripts/test_pet_mcp_coverage.py
@@ -0,0 +1,66 @@
+#!/usr/bin/env python3
+import importlib.util
+import tempfile
+import unittest
+from pathlib import Path
+
+spec = importlib.util.spec_from_file_location("pet_mcp_coverage", Path(__file__).with_name("pet-mcp-coverage.py"))
+coverage = importlib.util.module_from_spec(spec)
+spec.loader.exec_module(coverage)
+
+
+class CoverageInventoryTest(unittest.TestCase):
+    def setUp(self):
+        self.temp = tempfile.TemporaryDirectory()
+        self.addCleanup(self.temp.cleanup)
+        coverage.ROOT = Path(self.temp.name)
+        self.http = coverage.ROOT / "internal/httpapi"
+        self.http.mkdir(parents=True)
+        self.web = coverage.ROOT / "web/src"
+        self.web.mkdir(parents=True)
+
+    def test_literal_constant_and_desktop_tables_are_traced(self):
+        (self.http / "server.go").write_text('''
+const waitRoute = "POST /api/v1/agent/turn/inbox/wait"
+mux.Handle("GET /api/v1/system", s.withAuth(http.HandlerFunc(s.systemInfo)))
+mux.Handle(waitRoute, s.withAgentCapability(http.HandlerFunc(s.waitAgentInbox)))
+mux.HandleFunc("/", s.serveWeb)
+''')
+        (self.http / "desktop_handlers.go").write_text('''
+{"GET " + prefix + "/observation", s.desktopObserve},
+mux.Handle(route.pattern, s.withAuth(route.handler))
+{"GET /api/v1/agent/desktop", s.desktopStatus},
+mux.Handle(route.pattern, s.withAgentCapability(route.handler))
+''')
+        rows = coverage.routes()
+        self.assertEqual(len(rows), 5)
+        self.assertEqual(rows["GET /api/v1/system"]["handler"], "systemInfo")
+        self.assertEqual(rows["POST /api/v1/agent/turn/inbox/wait"]["boundary"], "agent_turn")
+        self.assertIn("GET /api/v1/tasks/{id}/desktop/observation", rows)
+        self.assertIn("ANY /", rows)
+
+    def test_unknown_registration_and_duplicate_fail_closed(self):
+        path = self.http / "server.go"
+        path.write_text('mux.Handle(dynamicRoute, s.handler)')
+        with self.assertRaisesRegex(ValueError, "unsupported registration"):
+            coverage.routes()
+        path.write_text('mux.Handle("GET /same", s.a)\nmux.Handle("GET /same", s.b)')
+        with self.assertRaisesRegex(ValueError, "duplicate route"):
+            coverage.routes()
+
+    def test_web_templates_keep_source_and_method_hint(self):
+        (self.web / "api.ts").write_text('''
+return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, {method: "PATCH", body: "{}"});
+fetch("/api/v1/system");
+return this.request(`/api/v1/projects${query.size ? `?${query}` : ""}`);
+''')
+        sites = coverage.web_sites()
+        self.assertEqual(len(sites), 3)
+        self.assertEqual(sites[0]["method_hint"], "PATCH")
+        self.assertEqual(sites[0]["line"], 2)
+        self.assertIn("${encodeURIComponent(id)}", sites[0]["expression"])
+        self.assertEqual(sites[2]["expression"], '/api/v1/projects${query.size ? `?${query}` : ""}')
+
+
+if __name__ == '__main__':
+    unittest.main()
```
