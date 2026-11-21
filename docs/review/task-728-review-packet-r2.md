# task-728 review packet r2：M1/M3 修复 + M2 按 owner 指示改为完全访问

- 后续修订：本包是 r2（源码 `2a8f1e5`）；M4 与测试补强见 `task-728-review-packet-r3.md`（源码 `ee276b2`）。

- 上一版：实现 `b285d54`，材料 `1e88bce`（r1，评审结论 REQUEST_CHANGES：M1/M2/M3 + S）
- 本版：实现 `2a8f1e5`，分支 `aha/task_07933ff5b7797feffa4c`，worktree `.aha2-worktrees/task_07933ff5b7797feffa4c`
- 没有 push、没有 merge、没有 complete/finalize；没有另开评审 Task
- 本包覆盖 `b285d54..2a8f1e5` 的源码 delta（不含 `docs/review`），48,107 字节，sha256 `a8ffbe7e74653ed693206884ddbc808eb5ebae7e10009dc5a48e7b2e4c856a07`（整包见文末字节数）
- 说明：`b285d54..2a8f1e5` 里还有一版 r1 材料提交 `1e88bce`，只改 `docs/review`。去掉 `docs/review` 后，本包 delta 与 `git diff 1e88bce 2a8f1e5 -- . ':!docs/review'` 逐字节相同（已用 `cmp` 核对）。还原方法：取 `~~~~diff` 与 `~~~~` 之间的内容。

## 逐条处理

### M1（必须修）：读时归一化覆盖 owner 新保存的模型
- 原子保存：`Store.UpdateOwnerPetSettings` 现在一条 SQL 同时写 `model_id` 和 `pet_brain_model_id`（连同 `pet_enabled`/`pet_actions`/`updated_at`）。保存路径不再先写聊天列、再写宠物列，因此不会出现 `(model_id=B, brain=A)` 的中间态。
- 名称/形象路径不再碰模型：`Store.UpdateOwnerAssistant` 改成只写 `name`/`character`/`updated_at`；`Service.UpdateOwnerAssistant` 不再调用归一化或 `UpdateOwnerAssistantModel`，只做 profile 更新。旧客户端发 `model_id` 时仍然只做“是否等于统一模型”的校验，不写库。
- 读时归一化加并发条件：新增 `Store.NormalizeOwnerAssistantModel`，`UPDATE ... WHERE id=? AND model_id=<读到的值> AND pet_brain_model_id=<读到的值>`。条件不匹配（说明期间有保存）时不再写，而是重新读整行，再基于新值判断。`normalizeAssistantUnifiedModel` 最多重试 3 次，成功后把新行写回调用者的快照。
- 回归测试：
  - `TestNormalizeOwnerAssistantModelDoesNotOverwriteConcurrentSave`：按评审的复现路径构造撕裂读 `(B,A)`，随后保存完成 `(B,B)`；归一化必须走 CAS 失败并重读，最终库和返回值都是 `(B,B)`。
  - `TestUpdateOwnerAssistantDoesNotWriteModel`：模型保存为 B 后只改名字/形象，库里两列仍是 B。
  - `TestConcurrentPetModelSavesAndReadsStayUnified`：2 个写者交替保存 A/B、2 个读者并发读取；结束后两列相等且是 A/B 之一。
  - `TestMigrateOwnerAssistantUnifiedModelSeedsOnce`：启动迁移第一次播种，第二次不再写（`updated_at` 不变）。
  - 旧字段冲突矩阵：`TestPetSettingsLegacyBrainModelAndConflictMatrix`（`brain_model_id` 兼容映射；source-first 选择同时压过 `brain_model_id` 与 `chat_model_id`）；`TestPetSettingsLegacyChatModelMapsToUnifiedModel`、`TestPetSettingsPetSelectionWinsOverChatModelID` 保留。

### M2（方向变更，按 owner 明确指示）：聊天与自主行为使用同一套完全访问权限
- owner 在 2026-11-21 18:58 追加要求：宠物模型（包括聊天）的权限应为完全访问，不是只读；不要按评审建议做“强制无工具模式”；M2 的继承 MCP/命令执行风险按 owner 选择接受，在交付里说明边界和残余风险，但不要把功能关掉。
- 实现：
  - 新增 `petRuntimePolicy`，从宠物 Task 的 `RuntimeConfigSnapshot` 读取 `Filesystem`/`Approval`（`parsePermissionsJSON`）、`ReasoningEffort`、`FastMode`、`ProxyEnabled`、`StreamIdleTimeoutMS`、`StreamMaxRetries`；没有宠物 Task 时使用与创建宠物 Task 相同的默认值（`danger-full-access`、`never`、`high`）。
  - `askAssistantBackend` 把这份策略同时放进 `ExecutionRequest.Filesystem/Approval` 和新建的 `RuntimeConfigSnapshot`，不再硬编码 `read-only`；CLI 聊天因此和自主 Task 一样拿到 `--dangerously-skip-permissions` / `danger-full-access`，审批仍是 `never`。
  - 提示词不再要求“不要调用工具”，改为“优先直接回答；需要时可以使用工作区和工具”。
- 测试：
  - `TestAssistantChatUsesUnifiedClaudeNativeModel` 断言聊天的 `Filesystem`/`Approval` 与宠物 Task 快照解析结果完全相等，并且等于 `danger-full-access`/`never`；`ReasoningEffort`/`FastMode`/`StreamIdleTimeoutMS`/`StreamMaxRetries` 也逐项与快照相等。
  - `TestAssistantChatUsesPetEnvGroupAndProxyPolicy` 断言聊天使用同一 Env Group、同一代理开关（宠物 Task 打开代理后聊天环境出现 `HTTP_PROXY`）。
- 残余风险（owner 已接受）：CLI 聊天继承宿主 CLI 的 MCP/配置，且完全访问权限意味着聊天模型可以执行命令、读写工作区；这次没有为聊天单独收紧。真实账号下的工具行为仍未实测。

### M3（必须修）：Codex 聊天刷新后的凭据回写
- `askAssistantBackend` 在 `PrepareProfile` 成功后注册 defer：无论 Execute 成功、失败还是超时，都用 `context.Background()` 在账号锁内调用 `SyncProfile(accountID, workspace, profileDir)`，与正常 Turn 的收尾一致。下一次 `PrepareProfile` 从账号密文里写出的就是刷新后的 `auth.json`。
- 回归测试 `TestAssistantChatPersistsRefreshedCodexCredential`：假执行器读取 `CODEX_HOME/auth.json` 并写回新的 access token；第一次聊天后断言账号密文已含新 token；第二次聊天断言 `PrepareProfile` 写出的就是新 token（记录到两次执行器看到的 token 分别是 initial / refreshed）。该测试特意停用宠物，避免宠物 Task 的 Turn 共用同一个执行器干扰计数。

### S：建议补强
- 可用性判断与运行时共用规则：
  - Provider：新增 `providerAPIKey`，设置页和 `assistantRuntime` 都用“凭据已配置但取不到”才判不可用；从未配置凭据的 Provider 不再被设置页误标红。`TestProviderAvailabilityMatchesChatRuntime` 覆盖两种情况。
  - Claude native：读取 Workspace 的 `capabilities.claude` 探测（优先宠物 Workspace）。`not_logged_in` 判不可用并给出登录提示；`logged_in` 可用；`unknown`/无探测保持可用，让 CLI 报真实鉴权错误。`TestClaudeNativeAvailabilityUsesWorkspaceProbe` 覆盖无探测、未登录、已登录。
  - 聊天入口在调用 backend 前执行同一可用性判断；官方账号不可用时不会启动执行器。`TestAssistantChatUnavailableOfficialAccountDoesNotRun` 断言回复含“不可用”且执行器 0 次调用。
- 顺带修了一个 typed-nil：`NewService` 之前把 nil `*secrets.FileStore` 装进 `SecretResolver` 接口，接口非 nil，读取 Provider 凭据时会 panic。现在显式保持 nil 接口；`TestPetMCPUpdateTaskAgentSwitchesModelSource` 等用例不再触发。
- 其余 S 断言：旧字段冲突矩阵、启动迁移幂等、并发保存/读取、Env Group/代理一致性都在上面的测试列表里。

## 执行的命令与退出码（全部实跑）

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `go build ./...` | 0 | 通过 |
| `go vet ./internal/app ./internal/store ./internal/httpapi ./cmd/aha` | 0 | 通过 |
| app 全包：`go test -c` 后 `env -u GOTMPDIR TMPDIR=/tmp/task-728-app app.test -test.count=1 -test.timeout=25m` | 0 | 0 FAIL |
| httpapi 全包：同法，`TMPDIR=/tmp/task-728-http` | 0 | 0 FAIL |
| store 全包：同法，`TMPDIR=/tmp/task-728-store` | 0 | 0 FAIL |
| `npm test`（web） | 0 | 620/620 |
| `node web/tests/pet_model_readonly.browser.mjs`（Playwright + Chromium） | 0 | 只读框、保存 payload、保存后两处一致 |

定向新增/修改用例：`TestNormalizeOwnerAssistantModelDoesNotOverwriteConcurrentSave`、`TestUpdateOwnerAssistantDoesNotWriteModel`、`TestMigrateOwnerAssistantUnifiedModelSeedsOnce`、`TestConcurrentPetModelSavesAndReadsStayUnified`、`TestPetSettingsLegacyBrainModelAndConflictMatrix`、`TestAssistantChatUsesUnifiedClaudeNativeModel`、`TestAssistantChatUsesPetEnvGroupAndProxyPolicy`、`TestAssistantChatPersistsRefreshedCodexCredential`、`TestAssistantChatUnavailableOfficialAccountDoesNotRun`、`TestProviderAvailabilityMatchesChatRuntime`、`TestClaudeNativeAvailabilityUsesWorkspaceProbe`、`TestUnifiedAssistantModelIDPrefersPetRuntime`。

## 未验证缺口

- 仍没有真实 Claude Code 登录或 Codex 官方账号，未对真实 CLI 发起聊天；M2 的完全访问、继承 MCP、工具执行行为与 M3 的真实 token 刷新都只由假执行器和 argv/请求断言覆盖。
- 浏览器验证仍是 Playwright + API 夹具，不是真实 Go 服务。
- 评审在只读沙箱里未能启动 Go 测试；以上全包结果是本机 builder 实跑。
- Claude 的 `danger-full-access` 走既有 `--dangerously-skip-permissions` 映射；Codex 走 `--sandbox danger-full-access`。真实账号下的行为需要 owner 验证。

## 本版 delta（b285d54..2a8f1e5，排除 docs/review）

~~~~diff
diff --git a/internal/app/assistant.go b/internal/app/assistant.go
index b27cbfb..fc2d13c 100644
--- a/internal/app/assistant.go
+++ b/internal/app/assistant.go
@@ -131,31 +131,57 @@ func unifiedAssistantModelID(assistant domain.OwnerAssistant) string {
 // normalizeAssistantUnifiedModel makes the stored row self-consistent. It
 // never picks a different model: an unavailable stored model stays stored so
 // chat and settings report a real error instead of silently switching.
+//
+// The repair is a compare-and-swap against the values this caller read. A save
+// that lands in between changes one of the two columns, so the update matches
+// zero rows and this function reloads the fresh row instead of writing the old
+// model back over the Owner's choice.
 func (s *Service) normalizeAssistantUnifiedModel(ctx context.Context, assistant *domain.OwnerAssistant) error {
 	if assistant == nil {
 		return nil
 	}
-	unified := unifiedAssistantModelID(*assistant)
-	changed := unified != strings.TrimSpace(assistant.BrainModelID) || unified != strings.TrimSpace(assistant.ModelID)
-	if unified == "" {
-		// First use only: seed the unified field from the chat-capable Provider
-		// catalog. This is setup, not a fallback after a failed selection.
-		models, err := s.assistantModelOptions(ctx)
+	for attempt := 0; attempt < 3; attempt++ {
+		observedModelID := strings.TrimSpace(assistant.ModelID)
+		observedBrainID := strings.TrimSpace(assistant.BrainModelID)
+		unified := observedBrainID
+		if unified == "" {
+			unified = observedModelID
+		}
+		if unified == "" {
+			// First use only: seed the unified field from the chat-capable
+			// Provider catalog. This is setup, not a fallback after a failed
+			// selection.
+			models, err := s.assistantModelOptions(ctx)
+			if err != nil {
+				return err
+			}
+			if len(models) > 0 {
+				unified = models[0].ID
+			}
+		}
+		if unified == observedModelID && unified == observedBrainID {
+			return nil
+		}
+		updatedAt := s.now().UTC()
+		applied, err := s.store.NormalizeOwnerAssistantModel(
+			ctx, assistant.ID, unified, observedModelID, observedBrainID, updatedAt,
+		)
 		if err != nil {
 			return err
 		}
-		if len(models) > 0 {
-			unified = models[0].ID
-			changed = true
+		if applied {
+			assistant.ModelID = unified
+			assistant.BrainModelID = unified
+			assistant.UpdatedAt = updatedAt
+			return nil
 		}
+		fresh, err := s.store.OwnerAssistant(ctx, assistant.ID)
+		if err != nil {
+			return err
+		}
+		*assistant = fresh
 	}
-	assistant.BrainModelID = unified
-	assistant.ModelID = unified
-	if !changed {
-		return nil
-	}
-	assistant.UpdatedAt = s.now().UTC()
-	return s.store.UpdateOwnerAssistantModel(ctx, assistant.ID, unified, assistant.UpdatedAt)
+	return nil
 }
 
 // fillAssistantModelReadback describes the unified model exactly once, so the
@@ -225,7 +251,10 @@ func assistantModelLabel(source string, model domain.Model, accountName string)
 }
 
 // assistantModelAvailability is a cheap readback check. It never mutates the
-// selection; callers show the reason and the runtime fails explicitly.
+// selection; callers show the reason and the runtime fails explicitly. Provider
+// and Claude native use the same predicate as the chat runtime, so the settings
+// page cannot mark a model unavailable while chat would accept it (or the
+// reverse).
 func (s *Service) assistantModelAvailability(ctx context.Context, model domain.Model, source string) (bool, string) {
 	if strings.TrimSpace(model.ProviderID) == "" {
 		return false, "模型缺少 Provider 配置"
@@ -249,29 +278,78 @@ func (s *Service) assistantModelAvailability(ctx context.Context, model domain.M
 		}
 		return false, "所选模型不在该 Codex 官方账号的可用模型列表中"
 	case domain.ModelSourceClaudeNative:
-		// Login state belongs to the Workspace probe; the settings picker shows
-		// that detail. The runtime reports a real authentication error.
-		return true, ""
+		return s.claudeNativeAvailability(ctx)
 	default:
 		provider, err := s.store.Provider(ctx, model.ProviderID)
 		if err != nil {
 			return false, "模型 Provider 已删除"
 		}
-		if !provider.CredentialConfigured {
+		if provider.CredentialConfigured && s.providerAPIKey(provider) == "" {
 			return false, "Provider 凭据不可用，请在模型页重新填写"
 		}
 		return true, ""
 	}
 }
 
+// providerAPIKey is the single credential lookup used by both chat and the
+// availability readback.
+func (s *Service) providerAPIKey(provider domain.Provider) string {
+	if provider.CredentialRef == "" || s.secrets == nil {
+		return ""
+	}
+	apiKey, _ := s.secrets.Get(provider.CredentialRef)
+	return apiKey
+}
+
+// claudeNativeAvailability reads the Workspace probe the picker already uses.
+// With no probe there is no evidence either way, so it stays available and the
+// CLI reports a real authentication error instead of the settings page
+// inventing one.
+func (s *Service) claudeNativeAvailability(ctx context.Context) (bool, string) {
+	workspaces, err := s.store.ListWorkspaces(ctx, "")
+	if err != nil {
+		return true, ""
+	}
+	ordered := make([]domain.Workspace, 0, len(workspaces))
+	for _, workspace := range workspaces {
+		if workspace.ID == petWorkspaceID {
+			ordered = append(ordered, workspace)
+		}
+	}
+	for _, workspace := range workspaces {
+		if workspace.ID != petWorkspaceID {
+			ordered = append(ordered, workspace)
+		}
+	}
+	for _, workspace := range ordered {
+		probe, ok := workspace.Capabilities["claude"].(map[string]any)
+		if !ok {
+			continue
+		}
+		status, _ := probe["status"].(string)
+		if status != "ready" {
+			continue
+		}
+		switch authStatus, _ := probe["auth_status"].(string); authStatus {
+		case "not_logged_in":
+			return false, "尚未登录 Claude Code，请先执行 claude auth login"
+		case "logged_in":
+			return true, ""
+		default:
+			// "unknown" is not evidence of a failure. Keeping it available lets
+			// the CLI report the real authentication result instead of the
+			// settings page blocking a login it could not verify.
+			return true, ""
+		}
+	}
+	return true, ""
+}
+
 func (s *Service) UpdateOwnerAssistant(ctx context.Context, modelID, name, character string) (OwnerAssistantView, error) {
 	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
 	if err != nil {
 		return OwnerAssistantView{}, err
 	}
-	if err := s.normalizeAssistantUnifiedModel(ctx, &assistant); err != nil {
-		return OwnerAssistantView{}, err
-	}
 	if name = strings.TrimSpace(name); name != "" {
 		if len([]rune(name)) > 40 {
 			return OwnerAssistantView{}, fmt.Errorf("助手名称不能超过 40 个字符")
@@ -291,20 +369,20 @@ func (s *Service) UpdateOwnerAssistant(ctx context.Context, modelID, name, chara
 		return OwnerAssistantView{}, fmt.Errorf("聊天模型跟随宠物模型，请在宠物设置的「模型来源 / 模型账号」中修改")
 	}
 	assistant.UpdatedAt = s.now().UTC()
+	// Profile only: this path must not write the model columns, or a stale read
+	// here would roll back a model save that completed after the read.
 	if err := s.store.UpdateOwnerAssistant(ctx, assistant); err != nil {
 		return OwnerAssistantView{}, err
 	}
-	if err := s.store.UpdateOwnerAssistantModel(ctx, assistant.ID, unifiedAssistantModelID(assistant), assistant.UpdatedAt); err != nil {
-		return OwnerAssistantView{}, err
-	}
 	if state, stateErr := s.OwnerPetState(ctx); stateErr == nil {
 		s.publishPetState(state)
 	}
 	return s.OwnerAssistant(ctx)
 }
 
-// AskOwnerAssistant answers one read-only question. It never creates Tasks or
-// calls a mutating service method; the model only sees a bounded snapshot.
+// AskOwnerAssistant answers one question without creating a Task. The model
+// sees a bounded snapshot; a CLI-backed source runs with the same permission
+// policy as the Pet Task.
 func (s *Service) AskOwnerAssistant(ctx context.Context, content string) (OwnerAssistantReply, error) {
 	return s.AskOwnerAssistantWithLayout(ctx, content, PetLayout{})
 }
@@ -676,10 +754,7 @@ func (s *Service) assistantRuntime(ctx context.Context, assistant *domain.OwnerA
 		if err != nil {
 			return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型的 Provider 不存在：%w", err)
 		}
-		apiKey := ""
-		if provider.CredentialRef != "" && s.secrets != nil {
-			apiKey, _ = s.secrets.Get(provider.CredentialRef)
-		}
+		apiKey := s.providerAPIKey(provider)
 		if apiKey == "" && provider.CredentialConfigured {
 			return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("Provider 凭据不可用，请在模型页重新填写")
 		}
@@ -690,26 +765,50 @@ func (s *Service) assistantRuntime(ctx context.Context, assistant *domain.OwnerA
 }
 
 // askAssistantThroughBackend runs the selected Claude native or Codex official
-// model through the same backend adapter the Pet Task executes, so both paths
-// resolve the identical catalog row, account and credentials.
+// model through the same backend adapter the Pet Task executes. The model,
+// account, credentials, workspace and permission policy all come from the same
+// pet runtime resolution; nothing here downgrades chat to a different mode.
 func (s *Service) askAssistantBackend(ctx context.Context, model domain.Model, source, systemPrompt string, history []domain.AssistantMessage, content string) (string, error) {
 	if s.executor == nil {
 		return "", fmt.Errorf("聊天后端不可用")
 	}
-	assistantWorkspace, envGroup, environment, err := s.assistantBackendEnvironment(ctx, model, source)
+	if ok, reason := s.assistantModelAvailability(ctx, model, source); !ok {
+		// The settings page and the chat runtime use one availability decision.
+		// An explicitly unavailable account or login fails here with the same
+		// wording instead of spawning a backend that can only fail later.
+		return "", fmt.Errorf("%s", reason)
+	}
+	policy := s.petRuntimePolicy(ctx)
+	assistantWorkspace, envGroup, environment, profileDir, err := s.assistantBackendEnvironment(ctx, model, source, policy)
 	if err != nil {
 		return "", err
 	}
+	if source == domain.ModelSourceOfficial && profileDir != "" && s.codex != nil {
+		// The normal Turn writes a refreshed auth.json back to the account after
+		// every run. Chat must do the same, on success or failure: otherwise the
+		// next PrepareProfile restores the stale credential over the refreshed
+		// one and later chat/autonomy calls authenticate with an expired token.
+		defer func() {
+			unlock := s.codex.LockAccount(model.CodexAccountID)
+			defer unlock()
+			s.codex.SyncProfile(context.Background(), model.CodexAccountID, assistantWorkspace, profileDir)
+		}()
+	}
 	prompt := assistantBackendPrompt(systemPrompt, history, content)
 	snapshot := domain.RuntimeConfigSnapshot{
-		WorkspaceID:      assistantWorkspace.ID,
-		Backend:          model.Backend,
-		ModelID:          model.ID,
-		WireModel:        model.WireModel,
-		EnvGroupID:       envGroup.ID,
-		EnvGroupRevision: envGroup.Revision,
-		CodexAccountID:   model.CodexAccountID,
-		ReasoningEffort:  model.DefaultEffort,
+		WorkspaceID:         assistantWorkspace.ID,
+		Backend:             model.Backend,
+		ModelID:             model.ID,
+		WireModel:           model.WireModel,
+		EnvGroupID:          envGroup.ID,
+		EnvGroupRevision:    envGroup.Revision,
+		CodexAccountID:      model.CodexAccountID,
+		ProxyEnabled:        policy.ProxyEnabled,
+		ReasoningEffort:     policy.ReasoningEffort,
+		FastMode:            policy.FastMode,
+		StreamIdleTimeoutMS: policy.StreamIdleTimeoutMS,
+		StreamMaxRetries:    policy.StreamMaxRetries,
+		PermissionsJSON:     permissionsJSON(policy.Filesystem, policy.Approval),
 	}
 	timeout := assistantBackendTimeout
 	runCtx, cancel := context.WithTimeout(ctx, timeout)
@@ -723,8 +822,8 @@ func (s *Service) askAssistantBackend(ctx context.Context, model domain.Model, s
 		Environment:      environment,
 		Prompt:           prompt,
 		BackendSessionID: "assistant_chat",
-		Filesystem:       "read-only",
-		Approval:         "never",
+		Filesystem:       policy.Filesystem,
+		Approval:         policy.Approval,
 	}, func(ExecutionEvent) {})
 	if err != nil {
 		return "", err
@@ -736,17 +835,54 @@ func (s *Service) askAssistantBackend(ctx context.Context, model domain.Model, s
 	return reply, nil
 }
 
+// petRuntimePolicy is the Pet Task's permission and runtime policy. Chat and
+// autonomy both read it from the same runtime snapshot; if the pet Task has not
+// been created yet, the same creation defaults are used.
+type petRuntimePolicy struct {
+	Filesystem          string
+	Approval            string
+	ReasoningEffort     string
+	FastMode            bool
+	ProxyEnabled        bool
+	StreamIdleTimeoutMS int
+	StreamMaxRetries    int
+}
+
+func (s *Service) petRuntimePolicy(ctx context.Context) petRuntimePolicy {
+	policy := petRuntimePolicy{
+		Filesystem: petTaskFilesystem, Approval: "never", ReasoningEffort: petTaskReasoningEffort,
+	}
+	agent, err := s.store.TaskAgent(ctx, petTaskID, "main")
+	if err != nil {
+		return policy
+	}
+	snapshot, err := s.store.RuntimeSnapshot(ctx, agent.RuntimeConfigSnapshotID)
+	if err != nil {
+		return policy
+	}
+	filesystem, approval := parsePermissionsJSON(snapshot.PermissionsJSON)
+	policy.Filesystem = filesystem
+	policy.Approval = approval
+	if effort := strings.TrimSpace(snapshot.ReasoningEffort); effort != "" {
+		policy.ReasoningEffort = effort
+	}
+	policy.FastMode = snapshot.FastMode
+	policy.ProxyEnabled = snapshot.ProxyEnabled
+	policy.StreamIdleTimeoutMS = snapshot.StreamIdleTimeoutMS
+	policy.StreamMaxRetries = snapshot.StreamMaxRetries
+	return policy
+}
+
 // assistantBackendEnvironment builds the runtime environment for the one-shot
-// backend chat. It mirrors the Turn path: env group values and secret refs,
-// proxy settings, and the Codex official profile directory.
-func (s *Service) assistantBackendEnvironment(ctx context.Context, model domain.Model, source string) (domain.Workspace, domain.EnvGroup, map[string]string, error) {
+// backend chat from the same model row, Env Group and account the Pet Task uses.
+func (s *Service) assistantBackendEnvironment(ctx context.Context, model domain.Model, source string, policy petRuntimePolicy) (domain.Workspace, domain.EnvGroup, map[string]string, string, error) {
 	assistantWorkspace, err := s.ensurePetWorkspace(ctx)
 	if err != nil {
-		return domain.Workspace{}, domain.EnvGroup{}, nil, fmt.Errorf("准备宠物 Workspace 失败：%w", err)
+		return domain.Workspace{}, domain.EnvGroup{}, nil, "", fmt.Errorf("准备宠物 Workspace 失败：%w", err)
 	}
 	envGroup, err := s.store.EnvGroup(ctx, model.DefaultEnvGroupID)
 	if err != nil {
-		return domain.Workspace{}, domain.EnvGroup{}, nil, fmt.Errorf("宠物模型的 Env Group 不可用：%w", err)
+		return domain.Workspace{}, domain.EnvGroup{}, nil, "", fmt.Errorf("宠物模型的 Env Group 不可用：%w", err)
 	}
 	environment := make(map[string]string, len(envGroup.Environment)+len(envGroup.SecretRefs))
 	for key, value := range envGroup.Environment {
@@ -760,12 +896,11 @@ func (s *Service) assistantBackendEnvironment(ctx context.Context, model domain.
 		}
 	}
 	// Mirror the Pet Task's proxy decision: the same turn path applies store
-	// proxy settings only when the snapshot asked for them, so chat must not
-	// silently route through a proxy the autonomy path does not use.
-	if s.petTaskProxyEnabled(ctx) {
+	// proxy settings only when the snapshot asked for them.
+	if policy.ProxyEnabled {
 		if s.proxyRuntime != nil {
 			if settingsErr := s.proxyRuntime.ApplyEnvironment(ctx, environment); settingsErr != nil {
-				return domain.Workspace{}, domain.EnvGroup{}, nil, fmt.Errorf("应用代理设置失败：%w", settingsErr)
+				return domain.Workspace{}, domain.EnvGroup{}, nil, "", fmt.Errorf("应用代理设置失败：%w", settingsErr)
 			}
 		} else if settings, settingsErr := s.store.ProxySettings(ctx); settingsErr == nil {
 			if normalized, normalizeErr := proxyconfig.Normalize(settings); normalizeErr == nil {
@@ -773,34 +908,20 @@ func (s *Service) assistantBackendEnvironment(ctx context.Context, model domain.
 			}
 		}
 	}
+	profileDir := ""
 	if source == domain.ModelSourceOfficial {
 		if s.codex == nil {
-			return domain.Workspace{}, domain.EnvGroup{}, nil, fmt.Errorf("Codex 官方账号运行时不可用")
+			return domain.Workspace{}, domain.EnvGroup{}, nil, "", fmt.Errorf("Codex 官方账号运行时不可用")
 		}
 		unlock := s.codex.LockAccount(model.CodexAccountID)
-		profileDir, profileErr := s.codex.PrepareProfile(ctx, model.CodexAccountID, assistantWorkspace, assistantWorkspace.RootPath, "assistant_chat")
+		profileDir, err = s.codex.PrepareProfile(ctx, model.CodexAccountID, assistantWorkspace, assistantWorkspace.RootPath, "assistant_chat")
 		unlock()
-		if profileErr != nil {
-			return domain.Workspace{}, domain.EnvGroup{}, nil, fmt.Errorf("准备 Codex 官方账号失败：%w", profileErr)
+		if err != nil {
+			return domain.Workspace{}, domain.EnvGroup{}, nil, "", fmt.Errorf("准备 Codex 官方账号失败：%w", err)
 		}
 		environment["CODEX_HOME"] = profileDir
 	}
-	return assistantWorkspace, envGroup, environment, nil
-}
-
-// petTaskProxyEnabled reports the Pet Task runtime's proxy switch. Without a
-// pet Task there is no autonomy runtime to match, so chat keeps the direct
-// environment.
-func (s *Service) petTaskProxyEnabled(ctx context.Context) bool {
-	agent, err := s.store.TaskAgent(ctx, petTaskID, "main")
-	if err != nil {
-		return false
-	}
-	snapshot, err := s.store.RuntimeSnapshot(ctx, agent.RuntimeConfigSnapshotID)
-	if err != nil {
-		return false
-	}
-	return snapshot.ProxyEnabled
+	return assistantWorkspace, envGroup, environment, profileDir, nil
 }
 
 // assistantBackendPrompt renders the single prompt string a CLI backend takes.
@@ -808,7 +929,7 @@ func (s *Service) petTaskProxyEnabled(ctx context.Context) bool {
 // these backends have no function-calling parameter in this path.
 func assistantBackendPrompt(systemPrompt string, history []domain.AssistantMessage, content string) string {
 	var builder strings.Builder
-	builder.WriteString("本轮是 Web 里的助手聊天：直接回答主人下面的问题，不要巡检 AHA、不要调用工具、不要创建 Task。\n\n")
+	builder.WriteString("本轮是 Web 里的助手聊天：优先直接回答主人。需要时可以使用工作区和工具，但不要主动巡检 AHA，也不要为了聊天创建 Task。\n\n")
 	builder.WriteString(strings.TrimSpace(systemPrompt))
 	builder.WriteString("\n\n## 当前对话\n")
 	for _, item := range history {
diff --git a/internal/app/assistant_unified_model_test.go b/internal/app/assistant_unified_model_test.go
index bcbabb2..dba1d51 100644
--- a/internal/app/assistant_unified_model_test.go
+++ b/internal/app/assistant_unified_model_test.go
@@ -2,11 +2,14 @@ package app
 
 import (
 	"context"
+	"encoding/json"
+	"os"
 	"path/filepath"
 	"strings"
 	"sync"
 	"testing"
 
+	"github.com/ChinaKai/AHA2/internal/codexaccount"
 	"github.com/ChinaKai/AHA2/internal/domain"
 	"github.com/ChinaKai/AHA2/internal/secrets"
 	"github.com/ChinaKai/AHA2/internal/store"
@@ -41,6 +44,49 @@ func (executor *recordingExecutor) lastRequest(t *testing.T) ExecutionRequest {
 	return executor.requests[len(executor.requests)-1]
 }
 
+// refreshingCodexAuthExecutor simulates Codex refreshing its OAuth token
+// during a chat: it reads the profile written by PrepareProfile, records the
+// access token it saw, then writes a refreshed token back to auth.json.
+type refreshingCodexAuthExecutor struct {
+	mu             sync.Mutex
+	seenTokens     []string
+	refreshedToken string
+}
+
+func (executor *refreshingCodexAuthExecutor) Execute(_ context.Context, request ExecutionRequest, _ func(ExecutionEvent)) (ExecutionResult, error) {
+	executor.mu.Lock()
+	defer executor.mu.Unlock()
+	home := strings.TrimSpace(request.Environment["CODEX_HOME"])
+	path := filepath.Join(home, "auth.json")
+	data, err := os.ReadFile(path)
+	if err != nil {
+		return ExecutionResult{}, err
+	}
+	var raw map[string]any
+	if err := json.Unmarshal(data, &raw); err != nil {
+		return ExecutionResult{}, err
+	}
+	tokens, _ := raw["tokens"].(map[string]any)
+	token, _ := tokens["access_token"].(string)
+	executor.seenTokens = append(executor.seenTokens, token)
+	tokens["access_token"] = executor.refreshedToken
+	tokens["refresh_token"] = "refreshed-refresh-token"
+	updated, err := json.Marshal(raw)
+	if err != nil {
+		return ExecutionResult{}, err
+	}
+	if err := os.WriteFile(path, updated, 0o600); err != nil {
+		return ExecutionResult{}, err
+	}
+	return ExecutionResult{Reply: "官方账号回答"}, nil
+}
+
+func (executor *refreshingCodexAuthExecutor) tokens() []string {
+	executor.mu.Lock()
+	defer executor.mu.Unlock()
+	return append([]string(nil), executor.seenTokens...)
+}
+
 func secondProviderModel(t *testing.T, fixture *petTaskFixture) domain.Model {
 	t.Helper()
 	model := domain.Model{
@@ -63,53 +109,100 @@ func storedAssistant(t *testing.T, fixture *petTaskFixture) domain.OwnerAssistan
 	return assistant
 }
 
-// Legacy rows could hold a chat model and a pet runtime model that disagreed.
-// On read the pet runtime wins and the chat column becomes its mirror.
-func TestOwnerAssistantNormalizesLegacyDivergentModels(t *testing.T) {
+// The legacy precedence is a pure decision: the pet runtime column wins and
+// the chat column is only a fallback for rows written before unification.
+func TestUnifiedAssistantModelIDPrefersPetRuntime(t *testing.T) {
+	assistant := domain.OwnerAssistant{ModelID: "chat-only", BrainModelID: "pet-runtime"}
+	if got := unifiedAssistantModelID(assistant); got != "pet-runtime" {
+		t.Fatalf("unified model=%q, want pet-runtime", got)
+	}
+	assistant.BrainModelID = ""
+	if got := unifiedAssistantModelID(assistant); got != "chat-only" {
+		t.Fatalf("chat-only fallback=%q", got)
+	}
+}
+
+// M1 regression: the reviewer reproduced a torn read (new chat model, old pet
+// model) while a save was in flight, then the read-time repair wrote the old
+// pet model back over the completed save. The compare-and-swap now sees that
+// the row moved and reloads it instead.
+func TestNormalizeOwnerAssistantModelDoesNotOverwriteConcurrentSave(t *testing.T) {
 	fixture := newPetTaskFixture(t)
 	second := secondProviderModel(t, fixture)
-	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, fixture.service.now().UTC())
-	if err != nil {
+	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
+		domain.ModelSourceProvider, fixture.model.ID, "", "",
+	)); err != nil {
 		t.Fatal(err)
 	}
-	assistant.ModelID = second.ID
-	if err := fixture.database.UpdateOwnerAssistant(fixture.ctx, assistant); err != nil {
-		t.Fatal(err)
+	stale := storedAssistant(t, fixture)
+	if stale.BrainModelID != fixture.model.ID || stale.ModelID != fixture.model.ID {
+		t.Fatalf("initial model=(brain=%q chat=%q)", stale.BrainModelID, stale.ModelID)
 	}
-	if err := fixture.database.UpdateOwnerPetSettings(fixture.ctx, assistant.ID, true, fixture.model.ID, []string{"walk"}, fixture.service.now().UTC()); err != nil {
+	// The read that normalization is about to repair saw the new chat column
+	// before the pet column was updated.
+	stale.ModelID = second.ID
+
+	// Another request completes the save after this snapshot was read.
+	if err := fixture.database.UpdateOwnerPetSettings(fixture.ctx, stale.ID, true, second.ID, []string{"walk"}, fixture.service.now().UTC()); err != nil {
 		t.Fatal(err)
 	}
-
-	view, err := fixture.service.OwnerAssistant(fixture.ctx)
-	if err != nil {
+	if afterSave := storedAssistant(t, fixture); afterSave.BrainModelID != second.ID || afterSave.ModelID != second.ID {
+		t.Fatalf("direct save did not write both columns: brain=%q chat=%q", afterSave.BrainModelID, afterSave.ModelID)
+	}
+	if err := fixture.service.normalizeAssistantUnifiedModel(fixture.ctx, &stale); err != nil {
 		t.Fatal(err)
 	}
-	if view.Assistant.BrainModelID != fixture.model.ID || view.Assistant.ModelID != fixture.model.ID {
-		t.Fatalf("legacy divergence was not normalized: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
+	stored := storedAssistant(t, fixture)
+	if stored.BrainModelID != second.ID || stored.ModelID != second.ID {
+		t.Fatalf("stale normalization rolled the save back: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
 	}
-	if stored := storedAssistant(t, fixture); stored.BrainModelID != fixture.model.ID || stored.ModelID != fixture.model.ID {
-		t.Fatalf("normalization was not persisted: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
+	if stale.BrainModelID != second.ID || stale.ModelID != second.ID {
+		t.Fatalf("normalization did not reload the saved row: brain=%q chat=%q", stale.BrainModelID, stale.ModelID)
 	}
 }
 
-// A legacy row with only the old chat field maps it into the unified field.
-func TestOwnerAssistantNormalizesLegacyChatOnlyRow(t *testing.T) {
+// The name/appearance path must not write the model columns at all. A model
+// save that lands between its read and write stays in effect.
+func TestUpdateOwnerAssistantDoesNotWriteModel(t *testing.T) {
 	fixture := newPetTaskFixture(t)
 	second := secondProviderModel(t, fixture)
-	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, fixture.service.now().UTC())
-	if err != nil {
+	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
+		domain.ModelSourceProvider, second.ID, "", "",
+	)); err != nil {
 		t.Fatal(err)
 	}
-	assistant.ModelID = second.ID
-	if err := fixture.database.UpdateOwnerAssistant(fixture.ctx, assistant); err != nil {
+	if _, err := fixture.service.UpdateOwnerAssistant(fixture.ctx, "", "新名字", "deepseek_fish"); err != nil {
 		t.Fatal(err)
 	}
-	view, err := fixture.service.OwnerAssistant(fixture.ctx)
-	if err != nil {
+	stored := storedAssistant(t, fixture)
+	if stored.BrainModelID != second.ID || stored.ModelID != second.ID {
+		t.Fatalf("profile update changed the model: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
+	}
+	if stored.Name != "新名字" || stored.Character != "deepseek_fish" {
+		t.Fatalf("profile update was not saved: name=%q character=%q", stored.Name, stored.Character)
+	}
+}
+
+// The startup migration is idempotent: it seeds an empty row once and leaves
+// later runs untouched.
+func TestMigrateOwnerAssistantUnifiedModelSeedsOnce(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	if _, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, fixture.service.now().UTC()); err != nil {
 		t.Fatal(err)
 	}
-	if view.Assistant.BrainModelID != second.ID || view.Assistant.ModelID != second.ID {
-		t.Fatalf("chat-only legacy row was not unified: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
+	fixture.service.MigrateOwnerAssistantUnifiedModel(fixture.ctx)
+	stored := storedAssistant(t, fixture)
+	if stored.BrainModelID != fixture.model.ID || stored.ModelID != fixture.model.ID {
+		t.Fatalf("migration seeded model=(brain=%q chat=%q)", stored.BrainModelID, stored.ModelID)
+	}
+	firstUpdated := stored.UpdatedAt
+	fixture.service.MigrateOwnerAssistantUnifiedModel(fixture.ctx)
+	stored = storedAssistant(t, fixture)
+	if !stored.UpdatedAt.Equal(firstUpdated) {
+		t.Fatalf("second migration wrote again: updated_at %s -> %s", firstUpdated, stored.UpdatedAt)
+	}
+	if stored.BrainModelID != fixture.model.ID || stored.ModelID != fixture.model.ID {
+		t.Fatalf("second migration changed the model: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
 	}
 }
 
@@ -208,8 +301,31 @@ func TestAssistantChatUsesUnifiedClaudeNativeModel(t *testing.T) {
 	if !strings.Contains(request.Prompt, "陪我聊聊") || !strings.Contains(request.Prompt, "<tool_call>") {
 		t.Fatalf("chat prompt is missing the user message or the text tool protocol:\n%s", request.Prompt)
 	}
-	if request.Filesystem != "read-only" {
-		t.Fatalf("chat backend ran with filesystem=%q", request.Filesystem)
+	// Chat and autonomy share one permission policy. The owner chose full
+	// access for the pet, and chat must not be downgraded to read-only.
+	agent, err := fixture.database.TaskAgent(fixture.ctx, petTaskID, "main")
+	if err != nil {
+		t.Fatal(err)
+	}
+	petSnapshot, err := fixture.database.RuntimeSnapshot(fixture.ctx, agent.RuntimeConfigSnapshotID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	petFilesystem, petApproval := parsePermissionsJSON(petSnapshot.PermissionsJSON)
+	if request.Filesystem != petFilesystem || request.Approval != petApproval {
+		t.Fatalf("chat permissions=%s/%s, pet task=%s/%s", request.Filesystem, request.Approval, petFilesystem, petApproval)
+	}
+	if request.Filesystem != petTaskFilesystem || request.Approval != "never" {
+		t.Fatalf("chat permissions=%s/%s, want full access with approval never", request.Filesystem, request.Approval)
+	}
+	if request.Snapshot.ReasoningEffort != petSnapshot.ReasoningEffort ||
+		request.Snapshot.FastMode != petSnapshot.FastMode ||
+		request.Snapshot.StreamIdleTimeoutMS != petSnapshot.StreamIdleTimeoutMS ||
+		request.Snapshot.StreamMaxRetries != petSnapshot.StreamMaxRetries {
+		t.Fatalf("chat runtime policy diverged from the pet task: chat=%#v pet=%#v", request.Snapshot, petSnapshot)
+	}
+	if request.Snapshot.EnvGroupID != request.EnvGroup.ID || request.Snapshot.CodexAccountID != request.Model.CodexAccountID {
+		t.Fatalf("chat snapshot does not share the resolved runtime: %#v", request.Snapshot)
 	}
 }
 
@@ -305,6 +421,304 @@ func TestAssistantChatDeletedUnifiedModelDoesNotFallBack(t *testing.T) {
 	}
 }
 
+// M3 regression: Codex refreshes its OAuth token during a chat. The refreshed
+// auth.json must be written back to the account, and the next chat must prepare
+// its profile from that refreshed value instead of the original one.
+func TestAssistantChatPersistsRefreshedCodexCredential(t *testing.T) {
+	executor := &refreshingCodexAuthExecutor{refreshedToken: "refreshed-access-token"}
+	fixture := newPetTaskFixtureWithExecutor(t, executor)
+	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "codex-secrets.json"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	credentialRef := "codex/account/refresh"
+	const initialAuth = `{"tokens":{"access_token":"initial-access-token","refresh_token":"initial-refresh-token"}}`
+	if err := secretStore.PutMany(map[string]string{credentialRef: initialAuth}); err != nil {
+		t.Fatal(err)
+	}
+	fixture.service.SetCodexAccountManager(codexaccount.New(fixture.ctx, fixture.database, secretStore, "", "codex"))
+	now := fixture.service.now().UTC()
+	account := domain.CodexAccount{
+		ID: "account-refresh", Label: "Refresh Account", Status: "ready", CredentialConfigured: true,
+		CredentialRef:   credentialRef,
+		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-refresh", DisplayName: "GPT Refresh"}},
+		CreatedAt:       now, UpdatedAt: now,
+	}
+	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
+		t.Fatal(err)
+	}
+	// The fixture uses one executor for both chat and the Pet Task. Disable the
+	// pet so only the two chat calls below reach it; the credential refresh is
+	// still exercised through the chat path.
+	disabled := petSettingsWithModel(domain.ModelSourceOfficial, "", "gpt-refresh", account.ID)
+	disabled.Enabled = false
+	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, disabled); err != nil {
+		t.Fatal(err)
+	}
+
+	if _, err := fixture.service.AskOwnerAssistant(fixture.ctx, "第一次"); err != nil {
+		t.Fatal(err)
+	}
+	stored, ok := secretStore.Get(credentialRef)
+	if !ok || !strings.Contains(stored, "refreshed-access-token") {
+		t.Fatalf("refreshed credential was not written back: %q", stored)
+	}
+
+	if _, err := fixture.service.AskOwnerAssistant(fixture.ctx, "第二次"); err != nil {
+		t.Fatal(err)
+	}
+	tokens := executor.tokens()
+	if len(tokens) != 2 {
+		t.Fatalf("chat runs=%d, want 2", len(tokens))
+	}
+	if tokens[0] != "initial-access-token" {
+		t.Fatalf("first chat token=%q, want the stored initial token", tokens[0])
+	}
+	if tokens[1] != "refreshed-access-token" {
+		t.Fatalf("second chat token=%q, want the refreshed token", tokens[1])
+	}
+}
+
+// Concurrent saves and reads must never leave the two model columns disagreeing
+// or resurrect a model that was already replaced.
+func TestConcurrentPetModelSavesAndReadsStayUnified(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	second := secondProviderModel(t, fixture)
+	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
+		domain.ModelSourceProvider, fixture.model.ID, "", "",
+	)); err != nil {
+		t.Fatal(err)
+	}
+	models := []string{fixture.model.ID, second.ID}
+	var wg sync.WaitGroup
+	errs := make(chan error, 16)
+	for writer := 0; writer < 2; writer++ {
+		wg.Add(1)
+		go func(index int) {
+			defer wg.Done()
+			for attempt := 0; attempt < 8; attempt++ {
+				input := petSettingsWithModel(domain.ModelSourceProvider, models[(index+attempt)%2], "", "")
+				if _, err := fixture.service.UpdateOwnerPetSettings(context.Background(), input); err != nil {
+					errs <- err
+					return
+				}
+			}
+		}(writer)
+	}
+	for reader := 0; reader < 2; reader++ {
+		wg.Add(1)
+		go func() {
+			defer wg.Done()
+			for attempt := 0; attempt < 16; attempt++ {
+				if _, err := fixture.service.OwnerAssistant(context.Background()); err != nil {
+					errs <- err
+					return
+				}
+			}
+		}()
+	}
+	wg.Wait()
+	close(errs)
+	for err := range errs {
+		t.Fatalf("concurrent operation failed: %v", err)
+	}
+	stored := storedAssistant(t, fixture)
+	if stored.ModelID != stored.BrainModelID {
+		t.Fatalf("concurrent operations left a torn model: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
+	}
+	if stored.BrainModelID != fixture.model.ID && stored.BrainModelID != second.ID {
+		t.Fatalf("concurrent operations left an unexpected model: %q", stored.BrainModelID)
+	}
+}
+
+// An official account that becomes unavailable after selection must fail chat
+// with its reason before the backend is called; it never falls back.
+func TestAssistantChatUnavailableOfficialAccountDoesNotRun(t *testing.T) {
+	executor := &recordingExecutor{reply: "should not run"}
+	fixture := newPetTaskFixtureWithExecutor(t, executor)
+	now := fixture.service.now().UTC()
+	account := domain.CodexAccount{
+		ID: "account-expires", Label: "Expires", Status: "ready", CredentialConfigured: true,
+		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-expires", DisplayName: "GPT Expires"}},
+		CreatedAt:       now, UpdatedAt: now,
+	}
+	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
+		domain.ModelSourceOfficial, "", "gpt-expires", account.ID,
+	)); err != nil {
+		t.Fatal(err)
+	}
+	account.Status = "expired"
+	account.UpdatedAt = fixture.service.now().UTC()
+	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
+		t.Fatal(err)
+	}
+	reply, err := fixture.service.AskOwnerAssistant(fixture.ctx, "还在吗")
+	if err != nil {
+		t.Fatal(err)
+	}
+	if !strings.Contains(reply.Reply.Content, "不可用") {
+		t.Fatalf("unavailable account reply=%q", reply.Reply.Content)
+	}
+	executor.mu.Lock()
+	runs := len(executor.requests)
+	executor.mu.Unlock()
+	if runs != 0 {
+		t.Fatalf("backend ran %d times for an unavailable account", runs)
+	}
+}
+
+// Chat reads the same Env Group and proxy switch as the Pet Task runtime.
+func TestAssistantChatUsesPetEnvGroupAndProxyPolicy(t *testing.T) {
+	executor := &recordingExecutor{reply: "ok"}
+	fixture := newPetTaskFixtureWithExecutor(t, executor)
+	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
+		domain.ModelSourceClaudeNative, "", "claude-sonnet-4-5", "",
+	)); err != nil {
+		t.Fatal(err)
+	}
+	envGroup, err := fixture.database.EnvGroup(fixture.ctx, domain.ClaudeNativeEnvGroupID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if _, err := fixture.database.UpdateProxySettings(fixture.ctx, domain.ProxySettings{
+		Mode: "external", HTTPProxy: "http://127.0.0.1:9", HTTPSProxy: "http://127.0.0.1:9",
+		ManagedRefreshIntervalMins: 1440,
+	}); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := fixture.service.UpdateAgentConfig(fixture.ctx, petTaskID, "main", UpdateAgentConfigInput{
+		ProxyEnabled: boolPointer(true),
+	}); err != nil {
+		t.Fatal(err)
+	}
+	// Runtime selection upserts the native Env Group, so add the marker after
+	// the last operation that resolves it.
+	envGroup, err = fixture.database.EnvGroup(fixture.ctx, domain.ClaudeNativeEnvGroupID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	envGroup.Environment["AHA_CHAT_TEST"] = "env-value"
+	if err := fixture.database.UpsertEnvGroup(fixture.ctx, envGroup); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := fixture.service.AskOwnerAssistant(fixture.ctx, "环境检查"); err != nil {
+		t.Fatal(err)
+	}
+	request := executor.lastRequest(t)
+	if request.EnvGroup.ID != envGroup.ID || request.Snapshot.EnvGroupID != envGroup.ID {
+		t.Fatalf("chat env group=%q snapshot=%q, want %q", request.EnvGroup.ID, request.Snapshot.EnvGroupID, envGroup.ID)
+	}
+	if request.Environment["AHA_CHAT_TEST"] != "env-value" {
+		t.Fatalf("chat did not inherit the Env Group environment: %#v", request.Environment)
+	}
+	if request.Environment["HTTP_PROXY"] != "http://127.0.0.1:9" {
+		t.Fatalf("chat did not follow the pet task proxy switch: HTTP_PROXY=%q", request.Environment["HTTP_PROXY"])
+	}
+	if !request.Snapshot.ProxyEnabled {
+		t.Fatalf("chat snapshot proxy switch=%t, want true", request.Snapshot.ProxyEnabled)
+	}
+}
+
+// The settings availability flag and the chat runtime must agree. A Provider
+// with no configured credential is not marked unavailable; one whose credential
+// was configured but is missing from the secret store fails in both places.
+func TestProviderAvailabilityMatchesChatRuntime(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	provider, err := fixture.database.Provider(fixture.ctx, fixture.provider.ID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	model, err := fixture.database.Model(fixture.ctx, fixture.model.ID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	// The fixture's provider says a credential is configured but the secret
+	// store is empty.
+	if ok, reason := fixture.service.assistantModelAvailability(fixture.ctx, model, domain.ModelSourceProvider); ok || !strings.Contains(reason, "凭据") {
+		t.Fatalf("missing configured credential availability=%t reason=%q", ok, reason)
+	}
+	if _, _, _, _, err := fixture.service.assistantRuntime(fixture.ctx, &domain.OwnerAssistant{
+		ModelID: fixture.model.ID, BrainModelID: fixture.model.ID,
+	}); err == nil || !strings.Contains(err.Error(), "凭据") {
+		t.Fatalf("chat runtime accepted a missing configured credential: %v", err)
+	}
+
+	// A provider that never configured a credential is usable with an empty
+	// key, and the settings page must not call it unavailable.
+	provider.CredentialConfigured = false
+	provider.CredentialRef = ""
+	if err := fixture.database.UpsertProvider(fixture.ctx, provider); err != nil {
+		t.Fatal(err)
+	}
+	if ok, reason := fixture.service.assistantModelAvailability(fixture.ctx, model, domain.ModelSourceProvider); !ok {
+		t.Fatalf("no-credential provider marked unavailable: %s", reason)
+	}
+	if _, _, _, _, err := fixture.service.assistantRuntime(fixture.ctx, &domain.OwnerAssistant{
+		ModelID: fixture.model.ID, BrainModelID: fixture.model.ID,
+	}); err != nil {
+		t.Fatalf("chat runtime rejected a no-credential provider: %v", err)
+	}
+}
+
+// Claude native availability follows the Workspace probe when there is one, and
+// stays unknown (available) when no probe exists so the CLI reports the real
+// authentication result.
+func TestClaudeNativeAvailabilityUsesWorkspaceProbe(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	if ok, reason := fixture.service.claudeNativeAvailability(fixture.ctx); !ok {
+		t.Fatalf("no probe should stay available, got %q", reason)
+	}
+	workspace, err := fixture.service.ensurePetWorkspace(fixture.ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	workspace.Capabilities = map[string]any{"claude": map[string]any{"status": "ready", "auth_status": "not_logged_in"}}
+	if err := fixture.database.UpdateWorkspaceDetection(fixture.ctx, workspace); err != nil {
+		t.Fatal(err)
+	}
+	if ok, reason := fixture.service.claudeNativeAvailability(fixture.ctx); ok || !strings.Contains(reason, "尚未登录") {
+		t.Fatalf("not-logged-in probe availability=%t reason=%q", ok, reason)
+	}
+	workspace.Capabilities = map[string]any{"claude": map[string]any{"status": "ready", "auth_status": "logged_in"}}
+	if err := fixture.database.UpdateWorkspaceDetection(fixture.ctx, workspace); err != nil {
+		t.Fatal(err)
+	}
+	if ok, reason := fixture.service.claudeNativeAvailability(fixture.ctx); !ok {
+		t.Fatalf("logged-in probe marked unavailable: %s", reason)
+	}
+}
+
+// Legacy brain_model_id is a compatibility input, and a source-first pet
+// selection always wins over any legacy field in the same request.
+func TestPetSettingsLegacyBrainModelAndConflictMatrix(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	second := secondProviderModel(t, fixture)
+	input := petSettingsWithModel("", "", "", "")
+	input.ChatModelID = ""
+	input.BrainModelID = second.ID
+	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, input)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if view.Assistant.BrainModelID != second.ID || view.Assistant.ModelID != second.ID {
+		t.Fatalf("legacy brain_model_id did not map: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
+	}
+
+	conflict := petSettingsWithModel(domain.ModelSourceProvider, fixture.model.ID, "", "")
+	conflict.BrainModelID = second.ID
+	conflict.ChatModelID = second.ID
+	view, err = fixture.service.UpdateOwnerPetSettings(fixture.ctx, conflict)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if view.Assistant.BrainModelID != fixture.model.ID || view.Assistant.ModelID != fixture.model.ID {
+		t.Fatalf("legacy fields overrode the source-first selection: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
+	}
+}
+
 // The read-only chat label describes source and account, so the Web field can
 // show exactly what the picker represents.
 func TestOwnerAssistantModelLabelIncludesSourceAndAccount(t *testing.T) {
@@ -329,6 +743,15 @@ func TestOwnerAssistantModelLabelIncludesSourceAndAccount(t *testing.T) {
 		t.Fatalf("official label=%q available=%t reason=%q", view.PetModelLabel, view.PetModelAvailable, view.PetModelUnavailableReason)
 	}
 
+	// The fixture's provider claims a configured credential without storing one.
+	// Mark it as the supported no-credential Provider case so the label and
+	// availability assertions describe the provider itself.
+	provider := fixture.provider
+	provider.CredentialConfigured = false
+	provider.CredentialRef = ""
+	if err := fixture.database.UpsertProvider(fixture.ctx, provider); err != nil {
+		t.Fatal(err)
+	}
 	providerView, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
 		domain.ModelSourceProvider, fixture.model.ID, "", "",
 	))
diff --git a/internal/app/pet.go b/internal/app/pet.go
index 377c278..80c44cc 100644
--- a/internal/app/pet.go
+++ b/internal/app/pet.go
@@ -766,11 +766,6 @@ func (s *Service) UpdateOwnerPetSettings(ctx context.Context, input PetSettingsI
 	if err := s.store.UpdateOwnerPetSettings(ctx, assistant.ID, assistant.PetEnabled, assistant.BrainModelID, assistant.PetActions, assistant.UpdatedAt); err != nil {
 		return OwnerAssistantView{}, err
 	}
-	// Keep the derived chat mirror in the same committed value even if an older
-	// writer raced this save; a reader must never see two different models.
-	if err := s.store.UpdateOwnerAssistantModel(ctx, assistant.ID, assistant.BrainModelID, assistant.UpdatedAt); err != nil {
-		return OwnerAssistantView{}, err
-	}
 	if err := s.store.UpdateOwnerPetBudget(ctx, assistant.ID, assistant.PetIdleMinutes, assistant.PetQuietEnabled, assistant.PetQuietStart, assistant.PetQuietEnd, assistant.PetHourlyLimit, assistant.UpdatedAt); err != nil {
 		return OwnerAssistantView{}, err
 	}
diff --git a/internal/app/service.go b/internal/app/service.go
index f61df55..992fbb5 100644
--- a/internal/app/service.go
+++ b/internal/app/service.go
@@ -495,9 +495,15 @@ type CreateTaskInput struct {
 }
 
 func NewService(database *store.Store, secretStore *secrets.FileStore, executor Executor) *Service {
+	// Keep a missing secret store as a nil interface. Assigning the typed nil
+	// pointer would make s.secrets != nil true and panic on Get.
+	var secretResolver SecretResolver
+	if secretStore != nil {
+		secretResolver = secretStore
+	}
 	return &Service{
 		store:                database,
-		secrets:              secretStore,
+		secrets:              secretResolver,
 		executor:             executor,
 		hub:                  NewEventHub(),
 		now:                  time.Now,
diff --git a/internal/store/assistant.go b/internal/store/assistant.go
index b08f7f2..56dfb92 100644
--- a/internal/store/assistant.go
+++ b/internal/store/assistant.go
@@ -52,9 +52,12 @@ func (s *Store) OwnerAssistant(ctx context.Context, id string) (domain.OwnerAssi
 }
 
 func (s *Store) UpdateOwnerAssistant(ctx context.Context, item domain.OwnerAssistant) error {
+	// Profile-only update. The model columns are written by
+	// UpdateOwnerAssistantModel or UpdateOwnerPetSettings so a stale profile
+	// read can never overwrite a model another request just saved.
 	_, err := s.db.ExecContext(ctx, `
-		UPDATE owner_assistants SET name=?,model_id=?,character=?,updated_at=? WHERE id=?`,
-		item.Name, item.ModelID, item.Character, timeString(item.UpdatedAt), item.ID)
+		UPDATE owner_assistants SET name=?,character=?,updated_at=? WHERE id=?`,
+		item.Name, item.Character, timeString(item.UpdatedAt), item.ID)
 	return err
 }
 
@@ -68,6 +71,25 @@ func (s *Store) UpdateOwnerAssistantModel(ctx context.Context, id, modelID strin
 	return err
 }
 
+// NormalizeOwnerAssistantModel applies a read-time repair only when the row is
+// still exactly what the caller read. A concurrent save that moved either
+// column makes the condition fail, so normalization can never overwrite a
+// model the Owner just chose. The boolean reports whether the row was updated.
+func (s *Store) NormalizeOwnerAssistantModel(ctx context.Context, id, modelID, expectModelID, expectBrainModelID string, updatedAt time.Time) (bool, error) {
+	result, err := s.db.ExecContext(ctx, `
+		UPDATE owner_assistants SET model_id=?,pet_brain_model_id=?,updated_at=?
+		WHERE id=? AND model_id=? AND pet_brain_model_id=?`,
+		modelID, modelID, timeString(updatedAt), id, expectModelID, expectBrainModelID)
+	if err != nil {
+		return false, err
+	}
+	affected, err := result.RowsAffected()
+	if err != nil {
+		return false, err
+	}
+	return affected > 0, nil
+}
+
 func splitPetActions(value string) []string {
 	result := []string{}
 	for _, item := range strings.Split(value, ",") {
@@ -87,9 +109,12 @@ func (s *Store) UpdateOwnerPetSettings(ctx context.Context, id string, enabled b
 	if strings.TrimSpace(value) == "" {
 		value = ""
 	}
+	// The save writes both model columns in the same statement. Splitting them
+	// into separate updates used to expose a transient (new chat model, old pet
+	// model) row that a concurrent read could normalize back over the new choice.
 	_, err := s.db.ExecContext(ctx, `
-		UPDATE owner_assistants SET pet_enabled=?,pet_brain_model_id=?,pet_actions=?,updated_at=? WHERE id=?`,
-		petBoolInt(enabled), brainModelID, value, timeString(updatedAt), id)
+		UPDATE owner_assistants SET model_id=?,pet_brain_model_id=?,pet_enabled=?,pet_actions=?,updated_at=? WHERE id=?`,
+		brainModelID, brainModelID, petBoolInt(enabled), value, timeString(updatedAt), id)
 	return err
 }
 
~~~~

- delta 字节数：48107
- delta sha256：a8ffbe7e74653ed693206884ddbc808eb5ebae7e10009dc5a48e7b2e4c856a07
