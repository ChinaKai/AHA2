# task-728 review packet diff 1：Go 源码与测试

- 基线 12b9be1 → b285d54
- 内容（按完整 diff 的路径顺序）：`cmd/aha/main.go`、`internal/app/assistant.go`、`internal/app/assistant_unified_model_test.go`、`internal/app/pet.go`、`internal/app/pet_model_source_test.go`、`internal/app/pet_task.go`、`internal/httpapi/assistant_handlers.go`、`internal/httpapi/pet_handlers.go`、`internal/httpapi/pet_model_unified_test.go`、`internal/store/assistant.go`
- 本卷是 `git diff 12b9be1 b285d54 -- <上面文件>` 的原样输出，sha256 `3176997e149ef5d7f68a4a96f3b48bab6cebb29fbb430d96860a0a61484451f9`，63698 字节

~~~~diff
diff --git a/cmd/aha/main.go b/cmd/aha/main.go
index 72249d2..0f5d65a 100644
--- a/cmd/aha/main.go
+++ b/cmd/aha/main.go
@@ -509,6 +509,7 @@ func runControlPlane(ctx context.Context, options serveOptions, ready func()) er
 	defer knowledgeVaultSyncer.Stop()
 	appService.SetBuildInfo(version, webVersion, time.Now().UTC())
 	appService.MigratePetTaskGoal(ctx)
+	appService.MigrateOwnerAssistantUnifiedModel(ctx)
 	appService.StartPetTaskNotifications()
 	appService.SetProxyRuntime(proxyRuntime)
 	appService.SetRunContext(ctx)
diff --git a/internal/app/assistant.go b/internal/app/assistant.go
index c02f34e..b27cbfb 100644
--- a/internal/app/assistant.go
+++ b/internal/app/assistant.go
@@ -11,6 +11,7 @@ import (
 
 	"github.com/ChinaKai/AHA2/internal/domain"
 	"github.com/ChinaKai/AHA2/internal/gateway"
+	"github.com/ChinaKai/AHA2/internal/proxyconfig"
 )
 
 const (
@@ -18,6 +19,10 @@ const (
 	assistantHistoryLimit = 12
 	assistantTaskLimit    = 40
 	assistantAskTimeout   = 60 * time.Second
+	// assistantBackendTimeout bounds a CLI-backed chat. Starting a backend and
+	// producing one answer is slower than a direct HTTP completion, but a chat
+	// must not inherit the full multi-minute Turn budget.
+	assistantBackendTimeout = 3 * time.Minute
 )
 
 type AssistantModelOption struct {
@@ -33,23 +38,30 @@ type PetToolInfo struct {
 }
 
 type OwnerAssistantView struct {
-	Assistant      domain.OwnerAssistant     `json:"assistant"`
-	Messages       []domain.AssistantMessage `json:"messages"`
-	Models         []AssistantModelOption    `json:"models"`
-	ModelName      string                    `json:"model_name,omitempty"`
-	BrainModelName string                    `json:"brain_model_name,omitempty"`
-	// Pet runtime readback. Models above are the Provider-only chat choices;
-	// these fields describe the source/model/account the Pet Task actually runs.
-	PetModelID        string        `json:"pet_model_id,omitempty"`
-	PetModelSource    string        `json:"pet_model_source,omitempty"`
-	PetWireModel      string        `json:"pet_wire_model,omitempty"`
-	PetCodexAccountID string        `json:"pet_codex_account_id,omitempty"`
-	PetModelName      string        `json:"pet_model_name,omitempty"`
-	PetWorkspaceID    string        `json:"pet_workspace_id,omitempty"`
-	Tools             []PetToolInfo `json:"tools"`
-	PetLastBrainAt    time.Time     `json:"pet_last_brain_at,omitempty"`
-	PetNextBrainAt    time.Time     `json:"pet_next_brain_at,omitempty"`
-	PetBrainRuns      int           `json:"pet_brain_runs"`
+	Assistant domain.OwnerAssistant     `json:"assistant"`
+	Messages  []domain.AssistantMessage `json:"messages"`
+	// Models is the Provider catalog shown by older clients. It is no longer a
+	// chat chooser: the unified pet model drives chat and autonomy alike.
+	Models         []AssistantModelOption `json:"models"`
+	ModelName      string                 `json:"model_name,omitempty"`
+	BrainModelName string                 `json:"brain_model_name,omitempty"`
+	// Unified pet model readback. The same source/model/account runs the Web
+	// assistant chat and the Pet Task; the settings page renders the read-only
+	// chat field and the source picker from these fields only.
+	PetModelID                string        `json:"pet_model_id,omitempty"`
+	PetModelSource            string        `json:"pet_model_source,omitempty"`
+	PetWireModel              string        `json:"pet_wire_model,omitempty"`
+	PetCodexAccountID         string        `json:"pet_codex_account_id,omitempty"`
+	PetModelName              string        `json:"pet_model_name,omitempty"`
+	PetAccountName            string        `json:"pet_account_name,omitempty"`
+	PetModelLabel             string        `json:"pet_model_label,omitempty"`
+	PetModelAvailable         bool          `json:"pet_model_available"`
+	PetModelUnavailableReason string        `json:"pet_model_unavailable_reason,omitempty"`
+	PetWorkspaceID            string        `json:"pet_workspace_id,omitempty"`
+	Tools                     []PetToolInfo `json:"tools"`
+	PetLastBrainAt            time.Time     `json:"pet_last_brain_at,omitempty"`
+	PetNextBrainAt            time.Time     `json:"pet_next_brain_at,omitempty"`
+	PetBrainRuns              int           `json:"pet_brain_runs"`
 }
 
 type AssistantToolEvent struct {
@@ -67,7 +79,8 @@ type OwnerAssistantReply struct {
 }
 
 // OwnerAssistant returns the device-local assistant, its recent conversation,
-// and the provider models it may use. It creates the singleton on first use.
+// and the Provider catalog. There is one model fact: the unified pet model in
+// pet_brain_model_id. model_id is a derived compatibility mirror.
 func (s *Service) OwnerAssistant(ctx context.Context) (OwnerAssistantView, error) {
 	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
 	if err != nil {
@@ -80,71 +93,175 @@ func (s *Service) OwnerAssistant(ctx context.Context) (OwnerAssistantView, error
 			return OwnerAssistantView{}, err
 		}
 	}
+	if err := s.normalizeAssistantUnifiedModel(ctx, &assistant); err != nil {
+		return OwnerAssistantView{}, err
+	}
 	models, err := s.assistantModelOptions(ctx)
 	if err != nil {
 		return OwnerAssistantView{}, err
 	}
-	if len(models) > 0 {
-		selected := false
-		for _, model := range models {
-			if model.ID == assistant.ModelID {
-				selected = true
-				break
-			}
-		}
-		if !selected {
-			assistant.ModelID = models[0].ID
-			assistant.UpdatedAt = s.now().UTC()
-			if err := s.store.UpdateOwnerAssistant(ctx, assistant); err != nil {
-				return OwnerAssistantView{}, err
-			}
-		}
-	}
 	messages, err := s.store.AssistantMessages(ctx, assistant.ID, 50)
 	if err != nil {
 		return OwnerAssistantView{}, err
 	}
 	view := OwnerAssistantView{Assistant: assistant, Messages: messages, Models: models}
-	for _, model := range models {
-		if model.ID == assistant.ModelID {
-			view.ModelName = model.DisplayName
+	s.fillAssistantModelReadback(ctx, &view)
+	// A disabled pet has no runnable Task. This also keeps a just-reset pet
+	// from recreating its project while the owner reviews the disabled settings.
+	if assistant.PetEnabled {
+		if taskID, taskErr := s.ensureOwnerPetTask(ctx, &assistant); taskErr == nil && taskID != "" {
+			view.Assistant.PetTaskID = taskID
 		}
 	}
-	petModelID := strings.TrimSpace(assistant.BrainModelID)
-	if petModelID == "" {
-		petModelID = assistant.ModelID
+	view.Tools = petToolInfos()
+	view.PetLastBrainAt, view.PetNextBrainAt, view.PetBrainRuns = s.petAutonomyStatus(assistant)
+	return view, nil
+}
+
+// unifiedAssistantModelID returns the one model ID that drives both the Web
+// assistant chat and the Pet Task. pet_brain_model_id is authoritative; the
+// legacy chat column is only a fallback for rows written before unification.
+func unifiedAssistantModelID(assistant domain.OwnerAssistant) string {
+	if id := strings.TrimSpace(assistant.BrainModelID); id != "" {
+		return id
+	}
+	return strings.TrimSpace(assistant.ModelID)
+}
+
+// normalizeAssistantUnifiedModel makes the stored row self-consistent. It
+// never picks a different model: an unavailable stored model stays stored so
+// chat and settings report a real error instead of silently switching.
+func (s *Service) normalizeAssistantUnifiedModel(ctx context.Context, assistant *domain.OwnerAssistant) error {
+	if assistant == nil {
+		return nil
 	}
-	view.PetModelID = petModelID
+	unified := unifiedAssistantModelID(*assistant)
+	changed := unified != strings.TrimSpace(assistant.BrainModelID) || unified != strings.TrimSpace(assistant.ModelID)
+	if unified == "" {
+		// First use only: seed the unified field from the chat-capable Provider
+		// catalog. This is setup, not a fallback after a failed selection.
+		models, err := s.assistantModelOptions(ctx)
+		if err != nil {
+			return err
+		}
+		if len(models) > 0 {
+			unified = models[0].ID
+			changed = true
+		}
+	}
+	assistant.BrainModelID = unified
+	assistant.ModelID = unified
+	if !changed {
+		return nil
+	}
+	assistant.UpdatedAt = s.now().UTC()
+	return s.store.UpdateOwnerAssistantModel(ctx, assistant.ID, unified, assistant.UpdatedAt)
+}
+
+// fillAssistantModelReadback describes the unified model exactly once, so the
+// read-only chat field and the source picker can never disagree.
+func (s *Service) fillAssistantModelReadback(ctx context.Context, view *OwnerAssistantView) {
 	view.PetWorkspaceID = petWorkspaceID
-	if petModelID != "" {
-		if model, modelErr := s.store.Model(ctx, petModelID); modelErr == nil {
-			source := strings.TrimSpace(model.Source)
-			if source == "" {
-				source = domain.ModelSourceProvider
+	modelID := unifiedAssistantModelID(view.Assistant)
+	view.PetModelID = modelID
+	if modelID == "" {
+		view.PetModelSource = domain.ModelSourceProvider
+		view.PetModelLabel = "未配置模型"
+		view.BrainModelName = "未配置模型"
+		view.PetModelAvailable = false
+		view.PetModelUnavailableReason = "还没有选择宠物模型"
+		return
+	}
+	model, err := s.store.Model(ctx, modelID)
+	if err != nil {
+		view.PetModelLabel = modelID + "（当前配置不可用）"
+		view.BrainModelName = modelID
+		view.PetModelAvailable = false
+		view.PetModelUnavailableReason = "所选模型已删除，请在模型来源里重新选择"
+		return
+	}
+	source := strings.TrimSpace(model.Source)
+	if source == "" {
+		source = domain.ModelSourceProvider
+	}
+	view.PetModelSource = source
+	view.PetWireModel = model.WireModel
+	view.PetCodexAccountID = model.CodexAccountID
+	view.PetModelName = model.DisplayName
+	view.ModelName = model.DisplayName
+	view.BrainModelName = model.DisplayName
+	if accountID := strings.TrimSpace(model.CodexAccountID); accountID != "" {
+		if account, accountErr := s.store.CodexAccount(ctx, accountID); accountErr == nil {
+			view.PetAccountName = strings.TrimSpace(account.Label)
+			if view.PetAccountName == "" {
+				view.PetAccountName = account.Email
 			}
-			view.PetModelSource = source
-			view.PetWireModel = model.WireModel
-			view.PetCodexAccountID = model.CodexAccountID
-			view.PetModelName = model.DisplayName
 		}
 	}
-	if view.PetModelSource == "" {
-		view.PetModelSource = domain.ModelSourceProvider
+	view.PetModelLabel = assistantModelLabel(source, model, view.PetAccountName)
+	view.PetModelAvailable, view.PetModelUnavailableReason = s.assistantModelAvailability(ctx, model, source)
+}
+
+// assistantModelLabel is the readable source + model/account description shown
+// by the read-only chat field.
+func assistantModelLabel(source string, model domain.Model, accountName string) string {
+	name := strings.TrimSpace(model.DisplayName)
+	if name == "" {
+		name = strings.TrimSpace(model.WireModel)
+	}
+	switch source {
+	case domain.ModelSourceOfficial:
+		account := strings.TrimSpace(accountName)
+		if account == "" {
+			account = strings.TrimSpace(model.CodexAccountID)
+		}
+		return "Codex 官方账号 · " + emptyAs(account, "未知账号") + " / " + emptyAs(name, model.ID)
+	case domain.ModelSourceClaudeNative:
+		return "Claude native · " + emptyAs(name, model.WireModel)
+	default:
+		provider := strings.TrimSpace(model.ProviderName)
+		return "Provider 模型 · " + emptyAs(provider, "未知 Provider") + " / " + emptyAs(name, model.ID)
 	}
-	view.BrainModelName = view.PetModelName
-	if view.BrainModelName == "" {
-		view.BrainModelName = "跟随聊天模型"
+}
+
+// assistantModelAvailability is a cheap readback check. It never mutates the
+// selection; callers show the reason and the runtime fails explicitly.
+func (s *Service) assistantModelAvailability(ctx context.Context, model domain.Model, source string) (bool, string) {
+	if strings.TrimSpace(model.ProviderID) == "" {
+		return false, "模型缺少 Provider 配置"
 	}
-	// A disabled pet has no runnable Task. This also keeps a just-reset pet
-	// from recreating its project while the owner reviews the disabled settings.
-	if assistant.PetEnabled {
-		if taskID, taskErr := s.ensureOwnerPetTask(ctx, &assistant); taskErr == nil && taskID != "" {
-			view.Assistant.PetTaskID = taskID
+	switch source {
+	case domain.ModelSourceOfficial:
+		account, err := s.store.CodexAccount(ctx, model.CodexAccountID)
+		if err != nil {
+			return false, "所选 Codex 官方账号已删除，请重新选择账号"
+		}
+		if !account.CredentialConfigured {
+			return false, "所选 Codex 官方账号尚未配置凭据"
+		}
+		if status := strings.TrimSpace(account.Status); status != "" && status != "ready" {
+			return false, "所选 Codex 官方账号当前不可用（" + status + "）"
+		}
+		for _, option := range account.AvailableModels {
+			if option.WireModel == model.WireModel {
+				return true, ""
+			}
+		}
+		return false, "所选模型不在该 Codex 官方账号的可用模型列表中"
+	case domain.ModelSourceClaudeNative:
+		// Login state belongs to the Workspace probe; the settings picker shows
+		// that detail. The runtime reports a real authentication error.
+		return true, ""
+	default:
+		provider, err := s.store.Provider(ctx, model.ProviderID)
+		if err != nil {
+			return false, "模型 Provider 已删除"
+		}
+		if !provider.CredentialConfigured {
+			return false, "Provider 凭据不可用，请在模型页重新填写"
 		}
+		return true, ""
 	}
-	view.Tools = petToolInfos()
-	view.PetLastBrainAt, view.PetNextBrainAt, view.PetBrainRuns = s.petAutonomyStatus(assistant)
-	return view, nil
 }
 
 func (s *Service) UpdateOwnerAssistant(ctx context.Context, modelID, name, character string) (OwnerAssistantView, error) {
@@ -152,6 +269,9 @@ func (s *Service) UpdateOwnerAssistant(ctx context.Context, modelID, name, chara
 	if err != nil {
 		return OwnerAssistantView{}, err
 	}
+	if err := s.normalizeAssistantUnifiedModel(ctx, &assistant); err != nil {
+		return OwnerAssistantView{}, err
+	}
 	if name = strings.TrimSpace(name); name != "" {
 		if len([]rune(name)) > 40 {
 			return OwnerAssistantView{}, fmt.Errorf("助手名称不能超过 40 个字符")
@@ -164,27 +284,19 @@ func (s *Service) UpdateOwnerAssistant(ctx context.Context, modelID, name, chara
 		}
 		assistant.Character = character
 	}
-	if modelID = strings.TrimSpace(modelID); modelID != "" {
-		models, modelErr := s.assistantModelOptions(ctx)
-		if modelErr != nil {
-			return OwnerAssistantView{}, modelErr
-		}
-		found := false
-		for _, model := range models {
-			if model.ID == modelID {
-				found = true
-				break
-			}
-		}
-		if !found {
-			return OwnerAssistantView{}, fmt.Errorf("所选模型不可用于助手，请选择 Provider 模型")
-		}
-		assistant.ModelID = modelID
+	if modelID = strings.TrimSpace(modelID); modelID != "" && modelID != unifiedAssistantModelID(assistant) {
+		// The chat model is no longer independently settable. Keeping the old
+		// model_id field accepted-but-inert would let two clients disagree about
+		// which model runs; the source picker is the only write path.
+		return OwnerAssistantView{}, fmt.Errorf("聊天模型跟随宠物模型，请在宠物设置的「模型来源 / 模型账号」中修改")
 	}
 	assistant.UpdatedAt = s.now().UTC()
 	if err := s.store.UpdateOwnerAssistant(ctx, assistant); err != nil {
 		return OwnerAssistantView{}, err
 	}
+	if err := s.store.UpdateOwnerAssistantModel(ctx, assistant.ID, unifiedAssistantModelID(assistant), assistant.UpdatedAt); err != nil {
+		return OwnerAssistantView{}, err
+	}
 	if state, stateErr := s.OwnerPetState(ctx); stateErr == nil {
 		s.publishPetState(state)
 	}
@@ -212,7 +324,10 @@ func (s *Service) AskOwnerAssistantWithLayout(ctx context.Context, content strin
 	if err != nil {
 		return OwnerAssistantReply{}, err
 	}
-	model, provider, apiKey, err := s.assistantRuntime(ctx, &assistant)
+	if err := s.normalizeAssistantUnifiedModel(ctx, &assistant); err != nil {
+		return OwnerAssistantReply{}, err
+	}
+	model, provider, apiKey, source, err := s.assistantRuntime(ctx, &assistant)
 	if err != nil {
 		return OwnerAssistantReply{}, err
 	}
@@ -247,31 +362,40 @@ func (s *Service) AskOwnerAssistantWithLayout(ctx context.Context, content strin
 	if promptErr != nil {
 		return OwnerAssistantReply{}, promptErr
 	}
-	chatOptions := gateway.ChatOptions{
-		BaseURL: provider.BaseURL, AnthropicBaseURL: provider.AnthropicBaseURL,
-		APIKey: apiKey, AuthStyle: provider.AuthStyle, WireAPI: model.WireAPI,
-		Model: wireModel, System: assistantSystemPromptText, Messages: messages,
-		MaxTokens: 3000, Timeout: assistantAskTimeout,
-	}
+	var toolResult gateway.ChatToolResponse
 	fallbackUsed := false
-	chatOptions.ForceTool = wantsPetAction(content)
-	toolResult, callErr := gateway.ChatWithTools(ctx, chatOptions, assistantPetTools())
-	if callErr != nil {
-		firstErr := callErr
-		// Retry with tools but without a forced choice; some Anthropic-compatible
-		// gateways reject tool_choice=any but accept normal tool use.
-		autoOptions := chatOptions
-		autoOptions.ForceTool = false
-		if retried, retryErr := gateway.ChatWithTools(ctx, autoOptions, assistantPetTools()); retryErr == nil {
-			toolResult = retried
-			callErr = nil
-			fallbackUsed = true
-		} else if fallback, fallbackErr := gateway.Chat(ctx, autoOptions); fallbackErr == nil {
-			toolResult = gateway.ChatToolResponse{Text: fallback}
-			callErr = nil
-			fallbackUsed = true
-		} else {
-			callErr = fmt.Errorf("%v; fallback: %v", firstErr, fallbackErr)
+	var callErr error
+	if source == domain.ModelSourceOfficial || source == domain.ModelSourceClaudeNative {
+		// The selected source has no Provider HTTP endpoint. Run the same
+		// backend the Pet Task runs, with the same catalog model and account,
+		// so chat and autonomy cannot diverge.
+		toolResult.Text, callErr = s.askAssistantBackend(ctx, model, source, assistantSystemPromptText, history, content)
+	} else {
+		chatOptions := gateway.ChatOptions{
+			BaseURL: provider.BaseURL, AnthropicBaseURL: provider.AnthropicBaseURL,
+			APIKey: apiKey, AuthStyle: provider.AuthStyle, WireAPI: model.WireAPI,
+			Model: wireModel, System: assistantSystemPromptText, Messages: messages,
+			MaxTokens: 3000, Timeout: assistantAskTimeout,
+		}
+		chatOptions.ForceTool = wantsPetAction(content)
+		toolResult, callErr = gateway.ChatWithTools(ctx, chatOptions, assistantPetTools())
+		if callErr != nil {
+			firstErr := callErr
+			// Retry with tools but without a forced choice; some Anthropic-compatible
+			// gateways reject tool_choice=any but accept normal tool use.
+			autoOptions := chatOptions
+			autoOptions.ForceTool = false
+			if retried, retryErr := gateway.ChatWithTools(ctx, autoOptions, assistantPetTools()); retryErr == nil {
+				toolResult = retried
+				callErr = nil
+				fallbackUsed = true
+			} else if fallback, fallbackErr := gateway.Chat(ctx, autoOptions); fallbackErr == nil {
+				toolResult = gateway.ChatToolResponse{Text: fallback}
+				callErr = nil
+				fallbackUsed = true
+			} else {
+				callErr = fmt.Errorf("%v; fallback: %v", firstErr, fallbackErr)
+			}
 		}
 	}
 	if len(toolResult.ToolCalls) == 0 {
@@ -282,7 +406,7 @@ func (s *Service) AskOwnerAssistantWithLayout(ctx context.Context, content strin
 	}
 	replyText := toolResult.Text
 	if callErr != nil {
-		replyText = "我暂时没连上模型：" + truncateAssistantText(callErr.Error(), 240) + "\n\n你可以在助手面板右上角换一个模型，或到模型页检查 Provider。"
+		replyText = "我暂时没连上模型：" + truncateAssistantText(callErr.Error(), 240) + "\n\n请在宠物设置的「模型来源 / 模型账号」检查当前模型；不可用来源不会自动切换。"
 	}
 	toolEvents := make([]AssistantToolEvent, 0, len(toolResult.ToolCalls)+1)
 	replyActions := make([]PetBrainAction, 0, 3)
@@ -514,62 +638,193 @@ func (s *Service) assistantModelOptions(ctx context.Context) ([]AssistantModelOp
 	return options, nil
 }
 
-func (s *Service) assistantRuntime(ctx context.Context, assistant *domain.OwnerAssistant) (domain.Model, domain.Provider, string, error) {
-	return s.assistantRuntimeByID(ctx, assistant, assistant.ModelID)
+// assistantRuntime resolves the one unified model. It returns the model, the
+// Provider runtime values for the direct HTTP chat path, and the canonical
+// source. It never substitutes a different model: an unavailable selection is
+// an error the owner must see and fix.
+func (s *Service) assistantRuntime(ctx context.Context, assistant *domain.OwnerAssistant) (domain.Model, domain.Provider, string, string, error) {
+	modelID := unifiedAssistantModelID(*assistant)
+	if modelID == "" {
+		return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("还没有配置宠物模型，请在宠物设置的「模型来源 / 模型账号」中选择")
+	}
+	model, err := s.store.Model(ctx, modelID)
+	if err != nil {
+		return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型已删除或不存在，请在宠物设置的「模型来源 / 模型账号」中重新选择")
+	}
+	source := strings.TrimSpace(model.Source)
+	if source == "" {
+		source = domain.ModelSourceProvider
+	}
+	switch source {
+	case domain.ModelSourceOfficial, domain.ModelSourceClaudeNative:
+		if model.Backend != "codex" && model.Backend != "claude" {
+			return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型的 Backend %q 不支持聊天", model.Backend)
+		}
+		return model, domain.Provider{}, "", source, nil
+	case domain.ModelSourceProvider:
+		if strings.TrimSpace(model.ProviderID) == "" {
+			return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型缺少 Provider 配置，请重新选择")
+		}
+		wire := strings.TrimSpace(model.WireAPI)
+		if wire == "" {
+			wire = "chat"
+		}
+		if !gateway.SupportsChat(wire) {
+			return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型协议 %q 不支持聊天", model.WireAPI)
+		}
+		provider, err := s.store.Provider(ctx, model.ProviderID)
+		if err != nil {
+			return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型的 Provider 不存在：%w", err)
+		}
+		apiKey := ""
+		if provider.CredentialRef != "" && s.secrets != nil {
+			apiKey, _ = s.secrets.Get(provider.CredentialRef)
+		}
+		if apiKey == "" && provider.CredentialConfigured {
+			return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("Provider 凭据不可用，请在模型页重新填写")
+		}
+		return model, provider, apiKey, source, nil
+	default:
+		return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型来源 %q 不支持聊天", source)
+	}
+}
+
+// askAssistantThroughBackend runs the selected Claude native or Codex official
+// model through the same backend adapter the Pet Task executes, so both paths
+// resolve the identical catalog row, account and credentials.
+func (s *Service) askAssistantBackend(ctx context.Context, model domain.Model, source, systemPrompt string, history []domain.AssistantMessage, content string) (string, error) {
+	if s.executor == nil {
+		return "", fmt.Errorf("聊天后端不可用")
+	}
+	assistantWorkspace, envGroup, environment, err := s.assistantBackendEnvironment(ctx, model, source)
+	if err != nil {
+		return "", err
+	}
+	prompt := assistantBackendPrompt(systemPrompt, history, content)
+	snapshot := domain.RuntimeConfigSnapshot{
+		WorkspaceID:      assistantWorkspace.ID,
+		Backend:          model.Backend,
+		ModelID:          model.ID,
+		WireModel:        model.WireModel,
+		EnvGroupID:       envGroup.ID,
+		EnvGroupRevision: envGroup.Revision,
+		CodexAccountID:   model.CodexAccountID,
+		ReasoningEffort:  model.DefaultEffort,
+	}
+	timeout := assistantBackendTimeout
+	runCtx, cancel := context.WithTimeout(ctx, timeout)
+	defer cancel()
+	result, err := s.executor.Execute(runCtx, ExecutionRequest{
+		Task:             domain.Task{TaskWorkspacePath: assistantWorkspace.RootPath},
+		Workspace:        assistantWorkspace,
+		Snapshot:         snapshot,
+		Model:            model,
+		EnvGroup:         envGroup,
+		Environment:      environment,
+		Prompt:           prompt,
+		BackendSessionID: "assistant_chat",
+		Filesystem:       "read-only",
+		Approval:         "never",
+	}, func(ExecutionEvent) {})
+	if err != nil {
+		return "", err
+	}
+	reply := strings.TrimSpace(result.Reply)
+	if reply == "" {
+		return "", fmt.Errorf("模型没有返回内容")
+	}
+	return reply, nil
 }
 
-func (s *Service) assistantRuntimeByID(ctx context.Context, assistant *domain.OwnerAssistant, requestedModelID string) (domain.Model, domain.Provider, string, error) {
-	options, err := s.assistantModelOptions(ctx)
+// assistantBackendEnvironment builds the runtime environment for the one-shot
+// backend chat. It mirrors the Turn path: env group values and secret refs,
+// proxy settings, and the Codex official profile directory.
+func (s *Service) assistantBackendEnvironment(ctx context.Context, model domain.Model, source string) (domain.Workspace, domain.EnvGroup, map[string]string, error) {
+	assistantWorkspace, err := s.ensurePetWorkspace(ctx)
 	if err != nil {
-		return domain.Model{}, domain.Provider{}, "", err
+		return domain.Workspace{}, domain.EnvGroup{}, nil, fmt.Errorf("准备宠物 Workspace 失败：%w", err)
 	}
-	if len(options) == 0 {
-		return domain.Model{}, domain.Provider{}, "", fmt.Errorf("还没有可用于助手的 Provider 模型，请先在模型页配置 chat 或 anthropic 模型")
+	envGroup, err := s.store.EnvGroup(ctx, model.DefaultEnvGroupID)
+	if err != nil {
+		return domain.Workspace{}, domain.EnvGroup{}, nil, fmt.Errorf("宠物模型的 Env Group 不可用：%w", err)
 	}
-	modelID := strings.TrimSpace(requestedModelID)
-	if modelID == "" {
-		modelID = assistant.ModelID
+	environment := make(map[string]string, len(envGroup.Environment)+len(envGroup.SecretRefs))
+	for key, value := range envGroup.Environment {
+		environment[key] = value
 	}
-	eligible := false
-	for _, option := range options {
-		if option.ID == modelID {
-			eligible = true
-			break
+	if s.secrets != nil {
+		for name, secretRef := range envGroup.SecretRefs {
+			if value, ok := s.secrets.Get(secretRef); ok {
+				environment[name] = value
+			}
 		}
 	}
-	if !eligible {
-		modelID = assistant.ModelID
-		for _, option := range options {
-			if option.ID == modelID {
-				eligible = true
-				break
+	// Mirror the Pet Task's proxy decision: the same turn path applies store
+	// proxy settings only when the snapshot asked for them, so chat must not
+	// silently route through a proxy the autonomy path does not use.
+	if s.petTaskProxyEnabled(ctx) {
+		if s.proxyRuntime != nil {
+			if settingsErr := s.proxyRuntime.ApplyEnvironment(ctx, environment); settingsErr != nil {
+				return domain.Workspace{}, domain.EnvGroup{}, nil, fmt.Errorf("应用代理设置失败：%w", settingsErr)
 			}
-		}
-		if !eligible {
-			modelID = options[0].ID
-			assistant.ModelID = modelID
-			assistant.UpdatedAt = s.now().UTC()
-			if err := s.store.UpdateOwnerAssistant(ctx, *assistant); err != nil {
-				return domain.Model{}, domain.Provider{}, "", err
+		} else if settings, settingsErr := s.store.ProxySettings(ctx); settingsErr == nil {
+			if normalized, normalizeErr := proxyconfig.Normalize(settings); normalizeErr == nil {
+				proxyconfig.ApplyEnvironment(environment, normalized)
 			}
 		}
 	}
-	model, err := s.store.Model(ctx, modelID)
+	if source == domain.ModelSourceOfficial {
+		if s.codex == nil {
+			return domain.Workspace{}, domain.EnvGroup{}, nil, fmt.Errorf("Codex 官方账号运行时不可用")
+		}
+		unlock := s.codex.LockAccount(model.CodexAccountID)
+		profileDir, profileErr := s.codex.PrepareProfile(ctx, model.CodexAccountID, assistantWorkspace, assistantWorkspace.RootPath, "assistant_chat")
+		unlock()
+		if profileErr != nil {
+			return domain.Workspace{}, domain.EnvGroup{}, nil, fmt.Errorf("准备 Codex 官方账号失败：%w", profileErr)
+		}
+		environment["CODEX_HOME"] = profileDir
+	}
+	return assistantWorkspace, envGroup, environment, nil
+}
+
+// petTaskProxyEnabled reports the Pet Task runtime's proxy switch. Without a
+// pet Task there is no autonomy runtime to match, so chat keeps the direct
+// environment.
+func (s *Service) petTaskProxyEnabled(ctx context.Context) bool {
+	agent, err := s.store.TaskAgent(ctx, petTaskID, "main")
 	if err != nil {
-		return domain.Model{}, domain.Provider{}, "", err
+		return false
 	}
-	provider, err := s.store.Provider(ctx, model.ProviderID)
+	snapshot, err := s.store.RuntimeSnapshot(ctx, agent.RuntimeConfigSnapshotID)
 	if err != nil {
-		return domain.Model{}, domain.Provider{}, "", fmt.Errorf("Provider 不存在：%w", err)
+		return false
 	}
-	apiKey := ""
-	if provider.CredentialRef != "" && s.secrets != nil {
-		apiKey, _ = s.secrets.Get(provider.CredentialRef)
+	return snapshot.ProxyEnabled
+}
+
+// assistantBackendPrompt renders the single prompt string a CLI backend takes.
+// The text tool-call block is the only way a CLI chat can move the pet, because
+// these backends have no function-calling parameter in this path.
+func assistantBackendPrompt(systemPrompt string, history []domain.AssistantMessage, content string) string {
+	var builder strings.Builder
+	builder.WriteString("本轮是 Web 里的助手聊天：直接回答主人下面的问题，不要巡检 AHA、不要调用工具、不要创建 Task。\n\n")
+	builder.WriteString(strings.TrimSpace(systemPrompt))
+	builder.WriteString("\n\n## 当前对话\n")
+	for _, item := range history {
+		role := "主人"
+		if item.Role == domain.AssistantRoleAssistant {
+			role = "你"
+		}
+		builder.WriteString(role + "：" + truncateAssistantText(item.Content, 800) + "\n")
 	}
-	if apiKey == "" && provider.CredentialConfigured {
-		return domain.Model{}, domain.Provider{}, "", fmt.Errorf("Provider 凭据不可用，请在模型页重新填写")
+	builder.WriteString("主人：" + content + "\n\n请直接回复主人。")
+	builder.WriteString("\n\n需要做动作时，在回复末尾按下面的格式输出（可多条）。动作是可选的，不要为了动作编造理由：\n")
+	for _, tool := range assistantPetTools() {
+		parameters, _ := json.Marshal(tool.Parameters)
+		fmt.Fprintf(&builder, "- <tool_call>%s %s</tool_call_end>\n", tool.Name, parameters)
 	}
-	return model, provider, apiKey, nil
+	return builder.String()
 }
 
 func (s *Service) assistantSnapshot(ctx context.Context) (string, error) {
diff --git a/internal/app/assistant_unified_model_test.go b/internal/app/assistant_unified_model_test.go
new file mode 100644
index 0000000..bcbabb2
--- /dev/null
+++ b/internal/app/assistant_unified_model_test.go
@@ -0,0 +1,342 @@
+package app
+
+import (
+	"context"
+	"path/filepath"
+	"strings"
+	"sync"
+	"testing"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/secrets"
+	"github.com/ChinaKai/AHA2/internal/store"
+)
+
+// recordingExecutor captures the one-shot backend chat request so the test can
+// assert chat and the Pet Task resolve the same catalog row.
+type recordingExecutor struct {
+	mu       sync.Mutex
+	requests []ExecutionRequest
+	reply    string
+	err      error
+}
+
+func (executor *recordingExecutor) Execute(_ context.Context, request ExecutionRequest, _ func(ExecutionEvent)) (ExecutionResult, error) {
+	executor.mu.Lock()
+	defer executor.mu.Unlock()
+	executor.requests = append(executor.requests, request)
+	if executor.err != nil {
+		return ExecutionResult{}, executor.err
+	}
+	return ExecutionResult{Reply: executor.reply}, nil
+}
+
+func (executor *recordingExecutor) lastRequest(t *testing.T) ExecutionRequest {
+	t.Helper()
+	executor.mu.Lock()
+	defer executor.mu.Unlock()
+	if len(executor.requests) == 0 {
+		t.Fatal("backend chat did not call the executor")
+	}
+	return executor.requests[len(executor.requests)-1]
+}
+
+func secondProviderModel(t *testing.T, fixture *petTaskFixture) domain.Model {
+	t.Helper()
+	model := domain.Model{
+		ID: "model-pet-second", DisplayName: "Second Pet Model", ProviderID: fixture.provider.ID,
+		Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "pet-second", WireAPI: "chat",
+		DefaultEnvGroupID: fixture.modelEnv.ID, CreatedAt: fixture.createdAt, UpdatedAt: fixture.createdAt,
+	}
+	if err := fixture.database.UpsertModel(fixture.ctx, model); err != nil {
+		t.Fatal(err)
+	}
+	return model
+}
+
+func storedAssistant(t *testing.T, fixture *petTaskFixture) domain.OwnerAssistant {
+	t.Helper()
+	assistant, err := fixture.database.OwnerAssistant(fixture.ctx, ownerAssistantID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	return assistant
+}
+
+// Legacy rows could hold a chat model and a pet runtime model that disagreed.
+// On read the pet runtime wins and the chat column becomes its mirror.
+func TestOwnerAssistantNormalizesLegacyDivergentModels(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	second := secondProviderModel(t, fixture)
+	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, fixture.service.now().UTC())
+	if err != nil {
+		t.Fatal(err)
+	}
+	assistant.ModelID = second.ID
+	if err := fixture.database.UpdateOwnerAssistant(fixture.ctx, assistant); err != nil {
+		t.Fatal(err)
+	}
+	if err := fixture.database.UpdateOwnerPetSettings(fixture.ctx, assistant.ID, true, fixture.model.ID, []string{"walk"}, fixture.service.now().UTC()); err != nil {
+		t.Fatal(err)
+	}
+
+	view, err := fixture.service.OwnerAssistant(fixture.ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if view.Assistant.BrainModelID != fixture.model.ID || view.Assistant.ModelID != fixture.model.ID {
+		t.Fatalf("legacy divergence was not normalized: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
+	}
+	if stored := storedAssistant(t, fixture); stored.BrainModelID != fixture.model.ID || stored.ModelID != fixture.model.ID {
+		t.Fatalf("normalization was not persisted: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
+	}
+}
+
+// A legacy row with only the old chat field maps it into the unified field.
+func TestOwnerAssistantNormalizesLegacyChatOnlyRow(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	second := secondProviderModel(t, fixture)
+	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, fixture.service.now().UTC())
+	if err != nil {
+		t.Fatal(err)
+	}
+	assistant.ModelID = second.ID
+	if err := fixture.database.UpdateOwnerAssistant(fixture.ctx, assistant); err != nil {
+		t.Fatal(err)
+	}
+	view, err := fixture.service.OwnerAssistant(fixture.ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if view.Assistant.BrainModelID != second.ID || view.Assistant.ModelID != second.ID {
+		t.Fatalf("chat-only legacy row was not unified: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
+	}
+}
+
+// The old assistant endpoint used to switch the chat model on its own. It must
+// refuse a different model instead of creating a second source of truth.
+func TestUpdateOwnerAssistantRejectsIndependentChatModel(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	second := secondProviderModel(t, fixture)
+	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
+		domain.ModelSourceProvider, fixture.model.ID, "", "",
+	)); err != nil {
+		t.Fatal(err)
+	}
+	_, err := fixture.service.UpdateOwnerAssistant(fixture.ctx, second.ID, "", "")
+	if err == nil || !strings.Contains(err.Error(), "宠物模型") {
+		t.Fatalf("independent chat model change was accepted: %v", err)
+	}
+	stored := storedAssistant(t, fixture)
+	if stored.BrainModelID != fixture.model.ID || stored.ModelID != fixture.model.ID {
+		t.Fatalf("rejected change mutated the model: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
+	}
+	// Naming and appearance changes still work with no model in the payload.
+	if _, err := fixture.service.UpdateOwnerAssistant(fixture.ctx, "", "小A", "deepseek_fish"); err != nil {
+		t.Fatalf("non-model assistant update failed: %v", err)
+	}
+}
+
+// A legacy settings client that only sends chat_model_id selects the unified
+// Provider model; it is a compatibility mapping, not a second chat model.
+func TestPetSettingsLegacyChatModelMapsToUnifiedModel(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	second := secondProviderModel(t, fixture)
+	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel("", "", "", ""))
+	if err != nil {
+		t.Fatal(err)
+	}
+	if view.Assistant.BrainModelID != fixture.model.ID {
+		t.Fatalf("empty selection did not keep the unified model: %q", view.Assistant.BrainModelID)
+	}
+
+	input := petSettingsWithModel("", "", "", "")
+	input.ChatModelID = second.ID
+	view, err = fixture.service.UpdateOwnerPetSettings(fixture.ctx, input)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if view.Assistant.BrainModelID != second.ID || view.Assistant.ModelID != second.ID {
+		t.Fatalf("legacy chat_model_id did not map to the unified model: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
+	}
+}
+
+// A hostile/old client sending both fields must not let chat_model_id override
+// the source-first pet selection.
+func TestPetSettingsPetSelectionWinsOverChatModelID(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	second := secondProviderModel(t, fixture)
+	input := petSettingsWithModel(domain.ModelSourceProvider, fixture.model.ID, "", "")
+	input.ChatModelID = second.ID
+	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, input)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if view.Assistant.BrainModelID != fixture.model.ID || view.Assistant.ModelID != fixture.model.ID {
+		t.Fatalf("chat_model_id overrode the pet selection: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
+	}
+}
+
+// Chat with a non-Provider source must use the same catalog row and backend the
+// Pet Task runs, not silently fall back to a Provider model.
+func TestAssistantChatUsesUnifiedClaudeNativeModel(t *testing.T) {
+	executor := &recordingExecutor{reply: "claude 的回答"}
+	fixture := newPetTaskFixtureWithExecutor(t, executor)
+	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
+		domain.ModelSourceClaudeNative, "", "claude-sonnet-4-5", "",
+	))
+	if err != nil {
+		t.Fatal(err)
+	}
+	wantModelID := domain.ClaudeNativeModelID("claude-sonnet-4-5")
+	if view.Assistant.BrainModelID != wantModelID || view.Assistant.ModelID != wantModelID {
+		t.Fatalf("unified model=(brain=%q chat=%q), want %q", view.Assistant.BrainModelID, view.Assistant.ModelID, wantModelID)
+	}
+
+	reply, err := fixture.service.AskOwnerAssistant(fixture.ctx, "陪我聊聊")
+	if err != nil {
+		t.Fatal(err)
+	}
+	if reply.Reply.Content != "claude 的回答" {
+		t.Fatalf("reply=%q", reply.Reply.Content)
+	}
+	request := executor.lastRequest(t)
+	if request.Snapshot.ModelID != wantModelID || request.Snapshot.Backend != "claude" ||
+		request.Model.ID != wantModelID || request.Model.Source != domain.ModelSourceClaudeNative {
+		t.Fatalf("chat backend request did not use the unified model: snapshot=%#v model=%#v", request.Snapshot, request.Model)
+	}
+	if !strings.Contains(request.Prompt, "陪我聊聊") || !strings.Contains(request.Prompt, "<tool_call>") {
+		t.Fatalf("chat prompt is missing the user message or the text tool protocol:\n%s", request.Prompt)
+	}
+	if request.Filesystem != "read-only" {
+		t.Fatalf("chat backend ran with filesystem=%q", request.Filesystem)
+	}
+}
+
+// The unified model and the Pet Task runtime snapshot must survive a service
+// restart, and chat after the restart must resolve the same row.
+func TestUnifiedModelSurvivesRestart(t *testing.T) {
+	executor := &recordingExecutor{reply: "重启后回答"}
+	fixture := newPetTaskFixtureWithExecutor(t, executor)
+	wantModelID := domain.ClaudeNativeModelID("claude-sonnet-4-5")
+	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
+		domain.ModelSourceClaudeNative, "", "claude-sonnet-4-5", "",
+	)); err != nil {
+		t.Fatal(err)
+	}
+	// The save path returns the assistant view, which creates the Pet Task.
+	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
+		t.Fatal(err)
+	}
+
+	reopened, err := store.Open(fixture.ctx, fixture.databasePath)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer reopened.Close()
+	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	restarted := NewService(reopened, secretStore, executor)
+	defer func() { _ = restarted.Shutdown(context.Background()) }()
+
+	view, err := restarted.OwnerAssistant(fixture.ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if view.Assistant.BrainModelID != wantModelID || view.Assistant.ModelID != wantModelID ||
+		view.PetModelSource != domain.ModelSourceClaudeNative {
+		t.Fatalf("model after restart=(brain=%q chat=%q source=%q)", view.Assistant.BrainModelID, view.Assistant.ModelID, view.PetModelSource)
+	}
+	agent, err := reopened.TaskAgent(fixture.ctx, petTaskID, "main")
+	if err != nil {
+		t.Fatal(err)
+	}
+	snapshot, err := reopened.RuntimeSnapshot(fixture.ctx, agent.RuntimeConfigSnapshotID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if snapshot.ModelID != wantModelID || snapshot.Backend != "claude" {
+		t.Fatalf("pet task runtime after restart=%#v", snapshot)
+	}
+
+	reply, err := restarted.AskOwnerAssistant(fixture.ctx, "重启后还在吗")
+	if err != nil {
+		t.Fatal(err)
+	}
+	if reply.Reply.Content != "重启后回答" {
+		t.Fatalf("reply after restart=%q", reply.Reply.Content)
+	}
+	if request := executor.lastRequest(t); request.Snapshot.ModelID != wantModelID || request.Model.ID != wantModelID {
+		t.Fatalf("chat after restart used %q/%q, want %q", request.Snapshot.ModelID, request.Model.ID, wantModelID)
+	}
+}
+
+// A deleted unified model is an explicit error. It must never be replaced with
+// a different catalog row.
+func TestAssistantChatDeletedUnifiedModelDoesNotFallBack(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	second := secondProviderModel(t, fixture)
+	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
+		domain.ModelSourceProvider, second.ID, "", "",
+	)); err != nil {
+		t.Fatal(err)
+	}
+	if err := fixture.database.DeleteModel(fixture.ctx, second.ID); err != nil {
+		t.Fatal(err)
+	}
+	_, err := fixture.service.AskOwnerAssistant(fixture.ctx, "你好")
+	if err == nil || !strings.Contains(err.Error(), "已删除") {
+		t.Fatalf("deleted unified model was not an explicit error: %v", err)
+	}
+	if messages, messageErr := fixture.database.AssistantMessages(fixture.ctx, ownerAssistantID, 10); messageErr != nil || len(messages) != 0 {
+		t.Fatalf("failed chat persisted messages: %#v err=%v", messages, messageErr)
+	}
+	view, viewErr := fixture.service.OwnerAssistant(fixture.ctx)
+	if viewErr != nil {
+		t.Fatal(viewErr)
+	}
+	if view.Assistant.BrainModelID != second.ID || view.Assistant.ModelID != second.ID {
+		t.Fatalf("read fell back to another model: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
+	}
+	if view.PetModelAvailable || !strings.Contains(view.PetModelUnavailableReason, "删除") {
+		t.Fatalf("unavailable readback=%q available=%t", view.PetModelUnavailableReason, view.PetModelAvailable)
+	}
+}
+
+// The read-only chat label describes source and account, so the Web field can
+// show exactly what the picker represents.
+func TestOwnerAssistantModelLabelIncludesSourceAndAccount(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	now := fixture.service.now().UTC()
+	account := domain.CodexAccount{
+		ID: "account-pet-label", Label: "Pet Plus", Status: "ready", CredentialConfigured: true,
+		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-label", DisplayName: "GPT Label"}},
+		CreatedAt:       now, UpdatedAt: now,
+	}
+	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
+		t.Fatal(err)
+	}
+	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
+		domain.ModelSourceOfficial, "", "gpt-label", account.ID,
+	))
+	if err != nil {
+		t.Fatal(err)
+	}
+	if !view.PetModelAvailable || !strings.Contains(view.PetModelLabel, "Codex 官方账号") ||
+		!strings.Contains(view.PetModelLabel, "Pet Plus") || !strings.Contains(view.PetModelLabel, "GPT Label") {
+		t.Fatalf("official label=%q available=%t reason=%q", view.PetModelLabel, view.PetModelAvailable, view.PetModelUnavailableReason)
+	}
+
+	providerView, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
+		domain.ModelSourceProvider, fixture.model.ID, "", "",
+	))
+	if err != nil {
+		t.Fatal(err)
+	}
+	if !providerView.PetModelAvailable || !strings.Contains(providerView.PetModelLabel, "Provider 模型") ||
+		!strings.Contains(providerView.PetModelLabel, fixture.model.DisplayName) {
+		t.Fatalf("provider label=%q available=%t", providerView.PetModelLabel, providerView.PetModelAvailable)
+	}
+}
diff --git a/internal/app/pet.go b/internal/app/pet.go
index c536056..377c278 100644
--- a/internal/app/pet.go
+++ b/internal/app/pet.go
@@ -580,8 +580,12 @@ func parsePetBrain(text string) PetBrainResult {
 }
 
 type PetSettingsInput struct {
-	Enabled     bool   `json:"enabled"`
-	Character   string `json:"character"`
+	Enabled   bool   `json:"enabled"`
+	Character string `json:"character"`
+	// ChatModelID is the legacy single-model field. It is only consulted when
+	// no source-first pet selection is present, and it then selects the one
+	// unified Provider model. A value that disagrees with the pet selection is
+	// ignored: the chat model can no longer move independently.
 	ChatModelID string `json:"chat_model_id"`
 	// BrainModelID is the legacy pet runtime field. New callers select a
 	// source-first runtime with the four PetModel* fields; the catalog model ID
@@ -636,15 +640,10 @@ func (s *Service) UpdateOwnerPetSettings(ctx context.Context, input PetSettingsI
 		}
 		return false
 	}
-	if chatModelID := strings.TrimSpace(input.ChatModelID); chatModelID != "" {
-		if !validModel(chatModelID) {
-			return OwnerAssistantView{}, fmt.Errorf("聊天模型不可用，请选择 Provider 模型")
-		}
-		assistant.ModelID = chatModelID
-	}
-	// The pet runtime may use any supported source. It is stored in the legacy
-	// brain_model_id column so old rows and readers keep working, while the chat
-	// panel keeps its Provider-only model_id.
+	// One model fact: the source-first pet selection below is the only write
+	// path. The legacy chat_model_id/brain_model_id inputs are accepted for old
+	// clients but map onto the same unified field instead of creating a second
+	// chat model. pet_brain_model_id is authoritative; model_id is its mirror.
 	petSelectionProvided := strings.TrimSpace(input.PetModelSource) != "" ||
 		strings.TrimSpace(input.PetModelID) != "" ||
 		strings.TrimSpace(input.PetWireModel) != "" ||
@@ -674,13 +673,22 @@ func (s *Service) UpdateOwnerPetSettings(ctx context.Context, input PetSettingsI
 			assistant.BrainModelID = model.ID
 		}
 	} else if chatModelID := strings.TrimSpace(input.ChatModelID); chatModelID != "" {
-		// Compatibility with the old single-model setting: chat and autonomy
-		// shared one Provider model.
-		assistant.BrainModelID = assistant.ModelID
+		// Compatibility with the old single-model setting: an old client that
+		// only sent chat_model_id selects that Provider model for chat and
+		// autonomy together.
+		if !validModel(chatModelID) {
+			return OwnerAssistantView{}, fmt.Errorf("聊天模型不可用，请选择 Provider 模型")
+		}
+		model, resolveErr := s.resolvePetRuntimeSelection(ctx, domain.ModelSourceProvider, chatModelID, "", "")
+		if resolveErr != nil {
+			return OwnerAssistantView{}, fmt.Errorf("宠物模型不可用：%w", resolveErr)
+		}
+		assistant.BrainModelID = model.ID
 	}
 	if strings.TrimSpace(assistant.BrainModelID) == "" {
-		assistant.BrainModelID = assistant.ModelID
+		assistant.BrainModelID = strings.TrimSpace(assistant.ModelID)
 	}
+	assistant.ModelID = strings.TrimSpace(assistant.BrainModelID)
 	allowed := map[string]bool{"walk": true, "jump": true, "happy": true, "angry": true}
 	actions := []string{}
 	seen := map[string]bool{}
@@ -758,6 +766,11 @@ func (s *Service) UpdateOwnerPetSettings(ctx context.Context, input PetSettingsI
 	if err := s.store.UpdateOwnerPetSettings(ctx, assistant.ID, assistant.PetEnabled, assistant.BrainModelID, assistant.PetActions, assistant.UpdatedAt); err != nil {
 		return OwnerAssistantView{}, err
 	}
+	// Keep the derived chat mirror in the same committed value even if an older
+	// writer raced this save; a reader must never see two different models.
+	if err := s.store.UpdateOwnerAssistantModel(ctx, assistant.ID, assistant.BrainModelID, assistant.UpdatedAt); err != nil {
+		return OwnerAssistantView{}, err
+	}
 	if err := s.store.UpdateOwnerPetBudget(ctx, assistant.ID, assistant.PetIdleMinutes, assistant.PetQuietEnabled, assistant.PetQuietStart, assistant.PetQuietEnd, assistant.PetHourlyLimit, assistant.UpdatedAt); err != nil {
 		return OwnerAssistantView{}, err
 	}
diff --git a/internal/app/pet_model_source_test.go b/internal/app/pet_model_source_test.go
index 468d096..562e08b 100644
--- a/internal/app/pet_model_source_test.go
+++ b/internal/app/pet_model_source_test.go
@@ -47,11 +47,11 @@ func TestPetModelSettingsSelectOfficialAccountAndReadBack(t *testing.T) {
 		t.Fatal(err)
 	}
 	wantModelID := domain.OfficialCodexModelID(account.ID, "gpt-pet-official")
-	if view.Assistant.BrainModelID != wantModelID {
-		t.Fatalf("pet runtime model=%q, want %q", view.Assistant.BrainModelID, wantModelID)
+	if view.Assistant.BrainModelID != wantModelID || view.Assistant.ModelID != wantModelID {
+		t.Fatalf("unified model=(brain=%q chat=%q), want %q", view.Assistant.BrainModelID, view.Assistant.ModelID, wantModelID)
 	}
-	if view.Assistant.ModelID != fixture.model.ID {
-		t.Fatalf("chat model changed to %q; it must stay on the Provider chat model", view.Assistant.ModelID)
+	if view.Assistant.ModelID != wantModelID {
+		t.Fatalf("chat model=%q; it must mirror the unified pet model %q", view.Assistant.ModelID, wantModelID)
 	}
 	if view.PetModelSource != domain.ModelSourceOfficial || view.PetWireModel != "gpt-pet-official" ||
 		view.PetCodexAccountID != account.ID || view.PetModelName != "GPT Pet Official" {
@@ -76,8 +76,8 @@ func TestPetModelSettingsSelectClaudeNative(t *testing.T) {
 		t.Fatal(err)
 	}
 	wantModelID := domain.ClaudeNativeModelID("claude-sonnet-4-5")
-	if view.Assistant.BrainModelID != wantModelID {
-		t.Fatalf("pet runtime model=%q, want %q", view.Assistant.BrainModelID, wantModelID)
+	if view.Assistant.BrainModelID != wantModelID || view.Assistant.ModelID != wantModelID {
+		t.Fatalf("unified model=(brain=%q chat=%q), want %q", view.Assistant.BrainModelID, view.Assistant.ModelID, wantModelID)
 	}
 	if view.PetModelSource != domain.ModelSourceClaudeNative || view.PetWireModel != "claude-sonnet-4-5" || view.PetCodexAccountID != "" {
 		t.Fatalf("pet model readback=%#v", view)
@@ -100,7 +100,7 @@ func TestPetModelSettingsProviderDoesNotRegress(t *testing.T) {
 	if err != nil {
 		t.Fatal(err)
 	}
-	if view.Assistant.BrainModelID != fixture.model.ID || view.PetModelSource != domain.ModelSourceProvider ||
+	if view.Assistant.BrainModelID != fixture.model.ID || view.Assistant.ModelID != fixture.model.ID || view.PetModelSource != domain.ModelSourceProvider ||
 		view.PetWireModel != fixture.model.WireModel {
 		t.Fatalf("provider pet selection=%#v", view)
 	}
diff --git a/internal/app/pet_task.go b/internal/app/pet_task.go
index ac80900..d7e42c9 100644
--- a/internal/app/pet_task.go
+++ b/internal/app/pet_task.go
@@ -168,6 +168,40 @@ func (s *Service) storeModelForPetRuntime(ctx context.Context, modelID string) (
 	return model, true
 }
 
+// ensurePetWorkspace creates the system pet project and its local writable
+// workspace on first use. Chat with a Claude native or Codex official model runs
+// in this workspace through the backend CLI, so it must be available even when
+// the pet Task itself has not been created or the pet is disabled.
+func (s *Service) ensurePetWorkspace(ctx context.Context) (domain.Workspace, error) {
+	if workspace, err := s.store.Workspace(ctx, petWorkspaceID); err == nil {
+		return workspace, nil
+	}
+	now := s.now().UTC()
+	project, projectErr := s.store.Project(ctx, petProjectID)
+	if projectErr != nil {
+		project = domain.Project{
+			ID: petProjectID, Name: "宠物", Description: "系统管理的宠物 Task 项目",
+			ProjectType: "system", KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now,
+		}
+		if err := s.store.CreateProject(ctx, project); err != nil {
+			return domain.Workspace{}, err
+		}
+	}
+	root := filepath.Join(s.store.DataDir(), "pet-workspace")
+	if err := os.MkdirAll(root, 0o700); err != nil {
+		return domain.Workspace{}, err
+	}
+	workspace := domain.Workspace{
+		ID: petWorkspaceID, ProjectID: project.ID, Name: "pet-workspace",
+		Locality: "local", Transport: "native", RootPath: root, Health: "ready",
+		AgentAPIMode: "embedded", CreatedAt: now, UpdatedAt: now,
+	}
+	if err := s.store.CreateWorkspace(ctx, workspace); err != nil {
+		return domain.Workspace{}, err
+	}
+	return workspace, nil
+}
+
 // ensureOwnerPetTask creates the visible system Pet Task and its dedicated
 // writable workspace on first use. The pet is a normal Task so it can use the
 // normal backend conversation, while pet tools come from the MCP endpoint.
@@ -231,30 +265,8 @@ func (s *Service) ensureOwnerPetTask(ctx context.Context, assistant *domain.Owne
 		s.ensurePetTaskStarted(ctx, existing)
 		return petTaskID, nil
 	}
-	now := s.now().UTC()
-	project, projectErr := s.store.Project(ctx, petProjectID)
-	if projectErr != nil {
-		project = domain.Project{
-			ID: petProjectID, Name: "宠物", Description: "系统管理的宠物 Task 项目",
-			ProjectType: "system", KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now,
-		}
-		if err := s.store.CreateProject(ctx, project); err != nil {
-			return "", err
-		}
-	}
-	if _, err := s.store.Workspace(ctx, petWorkspaceID); err != nil {
-		root := filepath.Join(s.store.DataDir(), "pet-workspace")
-		if err := os.MkdirAll(root, 0o700); err != nil {
-			return "", err
-		}
-		workspace := domain.Workspace{
-			ID: petWorkspaceID, ProjectID: project.ID, Name: "pet-workspace",
-			Locality: "local", Transport: "native", RootPath: root, Health: "ready",
-			AgentAPIMode: "embedded", CreatedAt: now, UpdatedAt: now,
-		}
-		if err := s.store.CreateWorkspace(ctx, workspace); err != nil {
-			return "", err
-		}
+	if _, err := s.ensurePetWorkspace(ctx); err != nil {
+		return "", err
 	}
 	petRuntime, petRuntimeFound := s.storeModelForPetRuntime(ctx, petModelID)
 	modelSource := petRuntime.Source
@@ -347,6 +359,24 @@ func (s *Service) migratePetTaskGoal(ctx context.Context, task domain.Task) doma
 	return task
 }
 
+// MigrateOwnerAssistantUnifiedModel applies the single-model migration once at
+// server start. pet_brain_model_id becomes authoritative and model_id a derived
+// mirror, so a legacy row where the chat model and pet model diverged stops
+// presenting two different models.
+func (s *Service) MigrateOwnerAssistantUnifiedModel(ctx context.Context) {
+	if s == nil || s.store == nil {
+		return
+	}
+	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
+	if err != nil {
+		log.Printf("migrate owner assistant unified model: %v", err)
+		return
+	}
+	if err := s.normalizeAssistantUnifiedModel(ctx, &assistant); err != nil {
+		log.Printf("migrate owner assistant unified model: %v", err)
+	}
+}
+
 // MigratePetTaskGoal applies the Pet Task goal migration once at server start
 // so an upgraded installation does not keep the onboarding goal until the
 // assistant page happens to load.
diff --git a/internal/httpapi/assistant_handlers.go b/internal/httpapi/assistant_handlers.go
index d468fd1..379fee1 100644
--- a/internal/httpapi/assistant_handlers.go
+++ b/internal/httpapi/assistant_handlers.go
@@ -17,8 +17,11 @@ func (s *Server) ownerAssistant(writer http.ResponseWriter, request *http.Reques
 		"models": view.Models, "model_name": view.ModelName, "brain_model_name": view.BrainModelName,
 		"pet_model_id": view.PetModelID, "pet_model_source": view.PetModelSource,
 		"pet_wire_model": view.PetWireModel, "pet_codex_account_id": view.PetCodexAccountID,
-		"pet_model_name": view.PetModelName, "pet_workspace_id": view.PetWorkspaceID,
-		"tools": view.Tools,
+		"pet_model_name": view.PetModelName, "pet_account_name": view.PetAccountName,
+		"pet_model_label": view.PetModelLabel, "pet_model_available": view.PetModelAvailable,
+		"pet_model_unavailable_reason": view.PetModelUnavailableReason,
+		"pet_workspace_id":             view.PetWorkspaceID,
+		"tools":                        view.Tools,
 	})
 }
 
diff --git a/internal/httpapi/pet_handlers.go b/internal/httpapi/pet_handlers.go
index 3b0daed..2fa1875 100644
--- a/internal/httpapi/pet_handlers.go
+++ b/internal/httpapi/pet_handlers.go
@@ -95,7 +95,10 @@ func (s *Server) updateOwnerPetSettings(writer http.ResponseWriter, request *htt
 		"model_name": view.ModelName, "brain_model_name": view.BrainModelName,
 		"pet_model_id": view.PetModelID, "pet_model_source": view.PetModelSource,
 		"pet_wire_model": view.PetWireModel, "pet_codex_account_id": view.PetCodexAccountID,
-		"pet_model_name": view.PetModelName, "pet_workspace_id": view.PetWorkspaceID,
+		"pet_model_name": view.PetModelName, "pet_account_name": view.PetAccountName,
+		"pet_model_label": view.PetModelLabel, "pet_model_available": view.PetModelAvailable,
+		"pet_model_unavailable_reason": view.PetModelUnavailableReason,
+		"pet_workspace_id":             view.PetWorkspaceID,
 	})
 }
 
diff --git a/internal/httpapi/pet_model_unified_test.go b/internal/httpapi/pet_model_unified_test.go
new file mode 100644
index 0000000..7b7bdd4
--- /dev/null
+++ b/internal/httpapi/pet_model_unified_test.go
@@ -0,0 +1,118 @@
+package httpapi
+
+import (
+	"context"
+	"net/http"
+	"net/http/cookiejar"
+	"net/http/httptest"
+	"path/filepath"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/app"
+	"github.com/ChinaKai/AHA2/internal/auth"
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/secrets"
+	"github.com/ChinaKai/AHA2/internal/store"
+)
+
+// The settings API has one writable model. A hostile or old client that sends
+// a different chat_model_id must still get the source-first selection, and the
+// legacy assistant endpoint must refuse an independent chat model.
+func TestOwnerPetSettingsAPIUnifiesChatAndPetModel(t *testing.T) {
+	t.Parallel()
+	ctx := context.Background()
+	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	now := time.Now().UTC()
+	provider := domain.Provider{
+		ID: "provider-unified-api", Name: "Unified API", BaseURL: "https://example.test/v1",
+		AuthStyle: "bearer", CredentialRef: "provider/unified/credential", CredentialConfigured: true,
+		CreatedAt: now, UpdatedAt: now,
+	}
+	envGroup := domain.EnvGroup{
+		ID: "env-unified-api", Name: "Unified", ProviderID: provider.ID, Backend: "codex", Revision: 1,
+		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
+	}
+	modelA := domain.Model{
+		ID: "model-unified-a", DisplayName: "Unified A", ProviderID: provider.ID,
+		Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "unified-a", WireAPI: "chat",
+		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
+	}
+	modelB := domain.Model{
+		ID: "model-unified-b", DisplayName: "Unified B", ProviderID: provider.ID,
+		Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "unified-b", WireAPI: "chat",
+		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
+	}
+	for _, operation := range []func() error{
+		func() error { return database.UpsertProvider(ctx, provider) },
+		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
+		func() error { return database.UpsertModel(ctx, modelA) },
+		func() error { return database.UpsertModel(ctx, modelB) },
+	} {
+		if err := operation(); err != nil {
+			t.Fatal(err)
+		}
+	}
+	authService := auth.NewService(database, "setup-test", time.Hour)
+	appService := app.NewService(database, secretStore, app.StubExecutor{})
+	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
+	defer server.Close()
+	jar, _ := cookiejar.New(nil)
+	client := &http.Client{Jar: jar}
+	csrf := registerOwner(t, client, server.URL)
+
+	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/pet/settings", map[string]any{
+		"enabled": true, "character": "gugugaga", "actions": []string{"walk"},
+		"idle_minutes": 5, "hourly_limit": 6, "quiet_start": 23, "quiet_end": 8,
+		"pet_model_source": "provider", "pet_model_id": modelA.ID,
+		"chat_model_id": modelB.ID,
+	}, csrf)
+	var saved struct {
+		Assistant struct {
+			ModelID      string `json:"model_id"`
+			BrainModelID string `json:"brain_model_id"`
+		} `json:"assistant"`
+		PetModelID    string `json:"pet_model_id"`
+		PetModelLabel string `json:"pet_model_label"`
+	}
+	decodeResponse(t, response, &saved)
+	if response.StatusCode != http.StatusOK {
+		t.Fatalf("save settings=%d %#v", response.StatusCode, saved)
+	}
+	if saved.Assistant.BrainModelID != modelA.ID || saved.Assistant.ModelID != modelA.ID || saved.PetModelID != modelA.ID {
+		t.Fatalf("chat_model_id overrode the unified model: %#v", saved)
+	}
+	if saved.PetModelLabel == "" {
+		t.Fatalf("read-only chat label is empty: %#v", saved)
+	}
+
+	// A fresh read must return the same single model.
+	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assistant", nil, "")
+	var read struct {
+		Assistant struct {
+			ModelID      string `json:"model_id"`
+			BrainModelID string `json:"brain_model_id"`
+		} `json:"assistant"`
+		PetModelID string `json:"pet_model_id"`
+	}
+	decodeResponse(t, response, &read)
+	if response.StatusCode != http.StatusOK || read.Assistant.ModelID != modelA.ID ||
+		read.Assistant.BrainModelID != modelA.ID || read.PetModelID != modelA.ID {
+		t.Fatalf("readback=%d %#v", response.StatusCode, read)
+	}
+
+	// The legacy assistant endpoint cannot move the chat model alone.
+	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/assistant", map[string]any{"model_id": modelB.ID}, csrf)
+	if response.StatusCode != http.StatusBadRequest {
+		t.Fatalf("independent chat model change=%d", response.StatusCode)
+	}
+	response.Body.Close()
+}
diff --git a/internal/store/assistant.go b/internal/store/assistant.go
index 5a30945..b08f7f2 100644
--- a/internal/store/assistant.go
+++ b/internal/store/assistant.go
@@ -58,6 +58,16 @@ func (s *Store) UpdateOwnerAssistant(ctx context.Context, item domain.OwnerAssis
 	return err
 }
 
+// UpdateOwnerAssistantModel writes the unified model in one statement. The pet
+// runtime column remains the source of truth and model_id is the derived
+// compatibility mirror, so a reader can never observe them disagreeing.
+func (s *Store) UpdateOwnerAssistantModel(ctx context.Context, id, modelID string, updatedAt time.Time) error {
+	_, err := s.db.ExecContext(ctx, `
+		UPDATE owner_assistants SET model_id=?,pet_brain_model_id=?,updated_at=? WHERE id=?`,
+		modelID, modelID, timeString(updatedAt), id)
+	return err
+}
+
 func splitPetActions(value string) []string {
 	result := []string{}
 	for _, item := range strings.Split(value, ",") {
~~~~
