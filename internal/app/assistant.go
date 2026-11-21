package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/gateway"
	"github.com/ChinaKai/AHA2/internal/proxyconfig"
)

const (
	ownerAssistantID      = "assistant_default"
	assistantHistoryLimit = 12
	assistantTaskLimit    = 40
	assistantAskTimeout   = 60 * time.Second
	// assistantBackendTimeout bounds a CLI-backed chat. Starting a backend and
	// producing one answer is slower than a direct HTTP completion, but a chat
	// must not inherit the full multi-minute Turn budget.
	assistantBackendTimeout = 3 * time.Minute
)

type AssistantModelOption struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	ProviderName string `json:"provider_name,omitempty"`
	WireAPI      string `json:"wire_api"`
}

type PetToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type OwnerAssistantView struct {
	Assistant domain.OwnerAssistant     `json:"assistant"`
	Messages  []domain.AssistantMessage `json:"messages"`
	// Models is the Provider catalog shown by older clients. It is no longer a
	// chat chooser: the unified pet model drives chat and autonomy alike.
	Models         []AssistantModelOption `json:"models"`
	ModelName      string                 `json:"model_name,omitempty"`
	BrainModelName string                 `json:"brain_model_name,omitempty"`
	// Unified pet model readback. The same source/model/account runs the Web
	// assistant chat and the Pet Task; the settings page renders the read-only
	// chat field and the source picker from these fields only.
	PetModelID                string        `json:"pet_model_id,omitempty"`
	PetModelSource            string        `json:"pet_model_source,omitempty"`
	PetWireModel              string        `json:"pet_wire_model,omitempty"`
	PetCodexAccountID         string        `json:"pet_codex_account_id,omitempty"`
	PetModelName              string        `json:"pet_model_name,omitempty"`
	PetAccountName            string        `json:"pet_account_name,omitempty"`
	PetModelLabel             string        `json:"pet_model_label,omitempty"`
	PetModelAvailable         bool          `json:"pet_model_available"`
	PetModelUnavailableReason string        `json:"pet_model_unavailable_reason,omitempty"`
	PetWorkspaceID            string        `json:"pet_workspace_id,omitempty"`
	Tools                     []PetToolInfo `json:"tools"`
	PetLastBrainAt            time.Time     `json:"pet_last_brain_at,omitempty"`
	PetNextBrainAt            time.Time     `json:"pet_next_brain_at,omitempty"`
	PetBrainRuns              int           `json:"pet_brain_runs"`
}

type AssistantToolEvent struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	Arguments string `json:"arguments,omitempty"`
}

type OwnerAssistantReply struct {
	Message    domain.AssistantMessage `json:"message"`
	Reply      domain.AssistantMessage `json:"reply"`
	ToolEvents []AssistantToolEvent    `json:"tool_events,omitempty"`
	Actions    []PetBrainAction        `json:"actions,omitempty"`
}

// OwnerAssistant returns the device-local assistant, its recent conversation,
// and the Provider catalog. There is one model fact: the unified pet model in
// pet_brain_model_id. model_id is a derived compatibility mirror.
func (s *Service) OwnerAssistant(ctx context.Context) (OwnerAssistantView, error) {
	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil {
		return OwnerAssistantView{}, err
	}
	if !assistantCharacterAllowed(assistant.Character) {
		assistant.Character = "gugugaga"
		assistant.UpdatedAt = s.now().UTC()
		if err := s.store.UpdateOwnerAssistant(ctx, assistant); err != nil {
			return OwnerAssistantView{}, err
		}
	}
	if err := s.normalizeAssistantUnifiedModel(ctx, &assistant); err != nil {
		return OwnerAssistantView{}, err
	}
	models, err := s.assistantModelOptions(ctx)
	if err != nil {
		return OwnerAssistantView{}, err
	}
	messages, err := s.store.AssistantMessages(ctx, assistant.ID, 50)
	if err != nil {
		return OwnerAssistantView{}, err
	}
	view := OwnerAssistantView{Assistant: assistant, Messages: messages, Models: models}
	s.fillAssistantModelReadback(ctx, &view)
	// A disabled pet has no runnable Task. This also keeps a just-reset pet
	// from recreating its project while the owner reviews the disabled settings.
	if assistant.PetEnabled {
		if taskID, taskErr := s.ensureOwnerPetTask(ctx, &assistant); taskErr == nil && taskID != "" {
			view.Assistant.PetTaskID = taskID
		}
	}
	view.Tools = petToolInfos()
	view.PetLastBrainAt, view.PetNextBrainAt, view.PetBrainRuns = s.petAutonomyStatus(assistant)
	return view, nil
}

// unifiedAssistantModelID returns the one model ID that drives both the Web
// assistant chat and the Pet Task. pet_brain_model_id is authoritative; the
// legacy chat column is only a fallback for rows written before unification.
func unifiedAssistantModelID(assistant domain.OwnerAssistant) string {
	if id := strings.TrimSpace(assistant.BrainModelID); id != "" {
		return id
	}
	return strings.TrimSpace(assistant.ModelID)
}

// normalizeAssistantUnifiedModel makes the stored row self-consistent. It
// never picks a different model: an unavailable stored model stays stored so
// chat and settings report a real error instead of silently switching.
//
// The repair is a compare-and-swap against the values this caller read. A save
// that lands in between changes one of the two columns, so the update matches
// zero rows and this function reloads the fresh row instead of writing the old
// model back over the Owner's choice.
func (s *Service) normalizeAssistantUnifiedModel(ctx context.Context, assistant *domain.OwnerAssistant) error {
	if assistant == nil {
		return nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		observedModelID := strings.TrimSpace(assistant.ModelID)
		observedBrainID := strings.TrimSpace(assistant.BrainModelID)
		unified := observedBrainID
		if unified == "" {
			unified = observedModelID
		}
		if unified == "" {
			// First use only: seed the unified field from the chat-capable
			// Provider catalog. This is setup, not a fallback after a failed
			// selection.
			models, err := s.assistantModelOptions(ctx)
			if err != nil {
				return err
			}
			if len(models) > 0 {
				unified = models[0].ID
			}
		}
		if unified == observedModelID && unified == observedBrainID {
			return nil
		}
		updatedAt := s.now().UTC()
		applied, err := s.store.NormalizeOwnerAssistantModel(
			ctx, assistant.ID, unified, observedModelID, observedBrainID, updatedAt,
		)
		if err != nil {
			return err
		}
		if applied {
			assistant.ModelID = unified
			assistant.BrainModelID = unified
			assistant.UpdatedAt = updatedAt
			return nil
		}
		fresh, err := s.store.OwnerAssistant(ctx, assistant.ID)
		if err != nil {
			return err
		}
		*assistant = fresh
	}
	return nil
}

// fillAssistantModelReadback describes the unified model exactly once, so the
// read-only chat field and the source picker can never disagree.
func (s *Service) fillAssistantModelReadback(ctx context.Context, view *OwnerAssistantView) {
	view.PetWorkspaceID = petWorkspaceID
	modelID := unifiedAssistantModelID(view.Assistant)
	view.PetModelID = modelID
	if modelID == "" {
		view.PetModelSource = domain.ModelSourceProvider
		view.PetModelLabel = "未配置模型"
		view.BrainModelName = "未配置模型"
		view.PetModelAvailable = false
		view.PetModelUnavailableReason = "还没有选择宠物模型"
		return
	}
	model, err := s.store.Model(ctx, modelID)
	if err != nil {
		view.PetModelLabel = modelID + "（当前配置不可用）"
		view.BrainModelName = modelID
		view.PetModelAvailable = false
		view.PetModelUnavailableReason = "所选模型已删除，请在模型来源里重新选择"
		return
	}
	source := strings.TrimSpace(model.Source)
	if source == "" {
		source = domain.ModelSourceProvider
	}
	view.PetModelSource = source
	view.PetWireModel = model.WireModel
	view.PetCodexAccountID = model.CodexAccountID
	view.PetModelName = model.DisplayName
	view.ModelName = model.DisplayName
	view.BrainModelName = model.DisplayName
	if accountID := strings.TrimSpace(model.CodexAccountID); accountID != "" {
		if account, accountErr := s.store.CodexAccount(ctx, accountID); accountErr == nil {
			view.PetAccountName = strings.TrimSpace(account.Label)
			if view.PetAccountName == "" {
				view.PetAccountName = account.Email
			}
		}
	}
	view.PetModelLabel = assistantModelLabel(source, model, view.PetAccountName)
	view.PetModelAvailable, view.PetModelUnavailableReason = s.assistantModelAvailability(ctx, model, source)
}

// assistantModelLabel is the readable source + model/account description shown
// by the read-only chat field.
func assistantModelLabel(source string, model domain.Model, accountName string) string {
	name := strings.TrimSpace(model.DisplayName)
	if name == "" {
		name = strings.TrimSpace(model.WireModel)
	}
	switch source {
	case domain.ModelSourceOfficial:
		account := strings.TrimSpace(accountName)
		if account == "" {
			account = strings.TrimSpace(model.CodexAccountID)
		}
		return "Codex 官方账号 · " + emptyAs(account, "未知账号") + " / " + emptyAs(name, model.ID)
	case domain.ModelSourceClaudeNative:
		return "Claude native · " + emptyAs(name, model.WireModel)
	default:
		provider := strings.TrimSpace(model.ProviderName)
		return "Provider 模型 · " + emptyAs(provider, "未知 Provider") + " / " + emptyAs(name, model.ID)
	}
}

// assistantModelAvailability is a cheap readback check. It never mutates the
// selection; callers show the reason and the runtime fails explicitly. Provider
// and Claude native use the same predicate as the chat runtime, so the settings
// page cannot mark a model unavailable while chat would accept it (or the
// reverse).
func (s *Service) assistantModelAvailability(ctx context.Context, model domain.Model, source string) (bool, string) {
	if strings.TrimSpace(model.ProviderID) == "" {
		return false, "模型缺少 Provider 配置"
	}
	switch source {
	case domain.ModelSourceOfficial:
		account, err := s.store.CodexAccount(ctx, model.CodexAccountID)
		if err != nil {
			return false, "所选 Codex 官方账号已删除，请重新选择账号"
		}
		if !account.CredentialConfigured {
			return false, "所选 Codex 官方账号尚未配置凭据"
		}
		if status := strings.TrimSpace(account.Status); status != "" && status != "ready" {
			return false, "所选 Codex 官方账号当前不可用（" + status + "）"
		}
		for _, option := range account.AvailableModels {
			if option.WireModel == model.WireModel {
				return true, ""
			}
		}
		return false, "所选模型不在该 Codex 官方账号的可用模型列表中"
	case domain.ModelSourceClaudeNative:
		return s.claudeNativeAvailability(ctx)
	default:
		provider, err := s.store.Provider(ctx, model.ProviderID)
		if err != nil {
			return false, "模型 Provider 已删除"
		}
		if provider.CredentialConfigured && s.providerAPIKey(provider) == "" {
			return false, "Provider 凭据不可用，请在模型页重新填写"
		}
		return true, ""
	}
}

// providerAPIKey is the single credential lookup used by both chat and the
// availability readback.
func (s *Service) providerAPIKey(provider domain.Provider) string {
	if provider.CredentialRef == "" || s.secrets == nil {
		return ""
	}
	apiKey, _ := s.secrets.Get(provider.CredentialRef)
	return apiKey
}

// claudeNativeAvailability reads the Workspace probe the picker already uses.
// With no probe on the execution host there is no evidence either way, so it
// stays available and the CLI reports a real authentication error instead of
// the settings page inventing one.
func (s *Service) claudeNativeAvailability(ctx context.Context) (bool, string) {
	workspaces, err := s.store.ListWorkspaces(ctx, "")
	if err != nil {
		return true, ""
	}
	// The Pet Workspace is where chat actually runs. If it has its own probe,
	// that probe is authoritative even if it belongs to a remote host.
	for _, workspace := range workspaces {
		if workspace.ID != petWorkspaceID {
			continue
		}
		if ok, available, reason := claudeProbeAvailability(workspace); ok {
			return available, reason
		}
	}
	// Only local native Workspaces share the default Claude login with the
	// local Pet Workspace. A remote Workspace's not_logged_in must never block
	// a working local account, and list order must not decide it either.
	loggedIn, notLoggedIn := false, false
	for _, workspace := range workspaces {
		if workspace.ID == petWorkspaceID || !claudeNativeHostWorkspace(workspace) {
			continue
		}
		ok, available, _ := claudeProbeAvailability(workspace)
		if !ok {
			continue
		}
		if available {
			loggedIn = true
		} else {
			notLoggedIn = true
		}
	}
	if loggedIn {
		return true, ""
	}
	if notLoggedIn {
		return false, "尚未登录 Claude Code，请先执行 claude auth login"
	}
	return true, ""
}

// claudeNativeHostWorkspace reports whether a Workspace describes the login of
// the machine that runs the local pet chat: a local native Workspace. WSL and
// SSH Workspaces have their own logged-in account and are not evidence about
// this host.
func claudeNativeHostWorkspace(workspace domain.Workspace) bool {
	if workspace.Locality != "" && workspace.Locality != "local" {
		return false
	}
	return workspace.Transport == "native"
}

// claudeProbeAvailability reads one Workspace's Claude probe. ok=false means
// the Workspace has no successful probe and contributes no evidence.
func claudeProbeAvailability(workspace domain.Workspace) (ok, available bool, reason string) {
	probe, found := workspace.Capabilities["claude"].(map[string]any)
	if !found {
		return false, true, ""
	}
	if status, _ := probe["status"].(string); status != "ready" {
		return false, true, ""
	}
	switch authStatus, _ := probe["auth_status"].(string); authStatus {
	case "not_logged_in":
		return true, false, "尚未登录 Claude Code，请先执行 claude auth login"
	case "logged_in":
		return true, true, ""
	default:
		// "unknown" is not evidence of a failure. Keeping it available lets the
		// CLI report the real authentication result.
		return true, true, ""
	}
}

func (s *Service) UpdateOwnerAssistant(ctx context.Context, modelID, name, character string) (OwnerAssistantView, error) {
	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil {
		return OwnerAssistantView{}, err
	}
	if name = strings.TrimSpace(name); name != "" {
		if len([]rune(name)) > 40 {
			return OwnerAssistantView{}, fmt.Errorf("助手名称不能超过 40 个字符")
		}
		assistant.Name = name
	}
	if character = strings.TrimSpace(character); character != "" {
		if !assistantCharacterAllowed(character) {
			return OwnerAssistantView{}, fmt.Errorf("不支持的形象：%s", character)
		}
		assistant.Character = character
	}
	if modelID = strings.TrimSpace(modelID); modelID != "" && modelID != unifiedAssistantModelID(assistant) {
		// The chat model is no longer independently settable. Keeping the old
		// model_id field accepted-but-inert would let two clients disagree about
		// which model runs; the source picker is the only write path.
		return OwnerAssistantView{}, fmt.Errorf("聊天模型跟随宠物模型，请在宠物设置的「模型来源 / 模型账号」中修改")
	}
	assistant.UpdatedAt = s.now().UTC()
	// Profile only: this path must not write the model columns, or a stale read
	// here would roll back a model save that completed after the read.
	if err := s.store.UpdateOwnerAssistant(ctx, assistant); err != nil {
		return OwnerAssistantView{}, err
	}
	if state, stateErr := s.OwnerPetState(ctx); stateErr == nil {
		s.publishPetState(state)
	}
	return s.OwnerAssistant(ctx)
}

// AskOwnerAssistant answers one question without creating a Task. The model
// sees a bounded snapshot; a CLI-backed source runs with the same permission
// policy as the Pet Task.
func (s *Service) AskOwnerAssistant(ctx context.Context, content string) (OwnerAssistantReply, error) {
	return s.AskOwnerAssistantWithLayout(ctx, content, PetLayout{})
}

func (s *Service) AskOwnerAssistantWithLayout(ctx context.Context, content string, layout PetLayout) (OwnerAssistantReply, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return OwnerAssistantReply{}, fmt.Errorf("消息不能为空")
	}
	if len([]rune(content)) > 4000 {
		return OwnerAssistantReply{}, fmt.Errorf("消息不能超过 4000 个字符")
	}
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()

	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil {
		return OwnerAssistantReply{}, err
	}
	if err := s.normalizeAssistantUnifiedModel(ctx, &assistant); err != nil {
		return OwnerAssistantReply{}, err
	}
	model, provider, apiKey, source, err := s.assistantRuntime(ctx, &assistant)
	if err != nil {
		return OwnerAssistantReply{}, err
	}
	history, err := s.store.AssistantMessages(ctx, assistant.ID, assistantHistoryLimit)
	if err != nil {
		return OwnerAssistantReply{}, err
	}
	now := s.now().UTC()
	userMessage := domain.AssistantMessage{
		ID: domain.NewID("assistant_message"), AssistantID: assistant.ID,
		Role: domain.AssistantRoleUser, Content: content, CreatedAt: now,
	}
	if err := s.store.AppendAssistantMessage(ctx, userMessage); err != nil {
		return OwnerAssistantReply{}, err
	}
	snapshot, err := s.assistantSnapshot(ctx)
	if err != nil {
		return OwnerAssistantReply{}, err
	}
	messages := make([]gateway.ChatMessage, 0, len(history)+1)
	for _, item := range history {
		messages = append(messages, gateway.ChatMessage{
			Role: item.Role, Content: truncateAssistantText(item.Content, 800),
		})
	}
	messages = append(messages, gateway.ChatMessage{Role: "user", Content: content})
	wireModel := strings.TrimSpace(model.WireModel)
	if wireModel == "" {
		wireModel = model.ID
	}
	assistantSystemPromptText, promptErr := s.assistantSystemPrompt(ctx, snapshot, assistantNickname(&assistant), assistant.PetIdentity, assistant.PetPrinciples, layout)
	if promptErr != nil {
		return OwnerAssistantReply{}, promptErr
	}
	var toolResult gateway.ChatToolResponse
	fallbackUsed := false
	var callErr error
	if source == domain.ModelSourceOfficial || source == domain.ModelSourceClaudeNative {
		// The selected source has no Provider HTTP endpoint. Run the same
		// backend the Pet Task runs, with the same catalog model and account,
		// so chat and autonomy cannot diverge.
		toolResult.Text, callErr = s.askAssistantBackend(ctx, model, source, assistantSystemPromptText, history, content)
	} else {
		chatOptions := gateway.ChatOptions{
			BaseURL: provider.BaseURL, AnthropicBaseURL: provider.AnthropicBaseURL,
			APIKey: apiKey, AuthStyle: provider.AuthStyle, WireAPI: model.WireAPI,
			Model: wireModel, System: assistantSystemPromptText, Messages: messages,
			MaxTokens: 3000, Timeout: assistantAskTimeout,
		}
		chatOptions.ForceTool = wantsPetAction(content)
		toolResult, callErr = gateway.ChatWithTools(ctx, chatOptions, assistantPetTools())
		if callErr != nil {
			firstErr := callErr
			// Retry with tools but without a forced choice; some Anthropic-compatible
			// gateways reject tool_choice=any but accept normal tool use.
			autoOptions := chatOptions
			autoOptions.ForceTool = false
			if retried, retryErr := gateway.ChatWithTools(ctx, autoOptions, assistantPetTools()); retryErr == nil {
				toolResult = retried
				callErr = nil
				fallbackUsed = true
			} else if fallback, fallbackErr := gateway.Chat(ctx, autoOptions); fallbackErr == nil {
				toolResult = gateway.ChatToolResponse{Text: fallback}
				callErr = nil
				fallbackUsed = true
			} else {
				callErr = fmt.Errorf("%v; fallback: %v", firstErr, fallbackErr)
			}
		}
	}
	if len(toolResult.ToolCalls) == 0 {
		if calls, cleaned := parseTextToolCalls(toolResult.Text); len(calls) > 0 {
			toolResult.ToolCalls = calls
			toolResult.Text = cleaned
		}
	}
	replyText := toolResult.Text
	if callErr != nil {
		replyText = "我暂时没连上模型：" + truncateAssistantText(callErr.Error(), 240) + "\n\n请在宠物设置的「模型来源 / 模型账号」检查当前模型；不可用来源不会自动切换。"
	}
	toolEvents := make([]AssistantToolEvent, 0, len(toolResult.ToolCalls)+1)
	replyActions := make([]PetBrainAction, 0, 3)
	if callErr == nil && len(toolResult.ToolCalls) > 0 {
		for _, call := range toolResult.ToolCalls {
			if len(toolEvents) >= 3 {
				break
			}
			action := PetBrainAction{}
			detail := ""
			switch call.Name {
			case "pet_play":
				var args struct {
					Name string `json:"name"`
				}
				if json.Unmarshal([]byte(call.Arguments), &args) == nil && strings.TrimSpace(args.Name) != "" {
					action = PetBrainAction{Type: "play", Name: args.Name}
					detail = args.Name
				}
			case "pet_move":
				var args struct {
					Zone string `json:"zone"`
				}
				if json.Unmarshal([]byte(call.Arguments), &args) == nil && strings.TrimSpace(args.Zone) != "" {
					action = PetBrainAction{Type: "move_to", Zone: args.Zone}
					detail = args.Zone
				}
			case "pet_move_to":
				var args struct {
					X          float64 `json:"x"`
					Y          float64 `json:"y"`
					DurationMS int     `json:"duration_ms"`
				}
				if json.Unmarshal([]byte(call.Arguments), &args) == nil && args.X >= 0 && args.X <= 1 && args.Y >= 0 && args.Y <= 1 {
					x, y := args.X, args.Y
					action = PetBrainAction{Type: "move_to", X: &x, Y: &y, DurationMS: args.DurationMS}
					detail = "arbitrary"
				}
			case "pet_walk_path":
				var args struct {
					Points     []PetPoint `json:"points"`
					DurationMS int        `json:"duration_ms"`
				}
				if json.Unmarshal([]byte(call.Arguments), &args) == nil && len(args.Points) >= 2 && len(args.Points) <= 128 {
					valid := true
					for _, point := range args.Points {
						if point.X < 0 || point.X > 1 || point.Y < 0 || point.Y > 1 {
							valid = false
							break
						}
					}
					if valid {
						action = PetBrainAction{Type: "walk_path", Points: args.Points, DurationMS: args.DurationMS}
						detail = "path"
					}
				}
			case "pet_mood":
				var args struct {
					Mood string `json:"mood"`
				}
				if json.Unmarshal([]byte(call.Arguments), &args) == nil && strings.TrimSpace(args.Mood) != "" {
					action = PetBrainAction{Type: "mood", Name: args.Mood}
					detail = args.Mood
				}
			case "pet_open_task":
				var args struct {
					Task string `json:"task"`
				}
				if json.Unmarshal([]byte(call.Arguments), &args) == nil && strings.TrimSpace(args.Task) != "" {
					action = PetBrainAction{Type: "open_task", Task: args.Task}
					detail = args.Task
				}
			case "pet_sleep":
				action = PetBrainAction{Type: "sleep"}
				detail = "sleep"
			case "pet_wake":
				action = PetBrainAction{Type: "wake"}
				detail = "wake"
			}
			if action.Type == "" {
				toolEvents = append(toolEvents, AssistantToolEvent{Name: call.Name, Status: "error", Message: "参数未通过校验", Arguments: call.Arguments})
				continue
			}
			controlled, err := s.ControlOwnerPet(ctx, PetControlRequest{Actions: []PetBrainAction{action}, Actor: "model"})
			if err != nil {
				toolEvents = append(toolEvents, AssistantToolEvent{Name: call.Name, Status: "error", Message: err.Error(), Arguments: call.Arguments})
				continue
			}
			// Use the normalized actions: open_task carries its navigation id and
			// a code may have been resolved to the internal Task id.
			replyActions = append(replyActions, controlled.Actions...)
			toolEvents = append(toolEvents, AssistantToolEvent{Name: call.Name, Status: "ok", Message: detail, Arguments: call.Arguments})
		}
		if strings.TrimSpace(replyText) == "" {
			replyText = "好。"
		}
	} else if callErr == nil && len(toolResult.ToolCalls) == 0 && len(toolEvents) == 0 && toolResult.Text == "" {
		// no-op: ordinary text reply
	}
	if callErr == nil && len(toolResult.ToolCalls) == 0 && toolResult.Text != "" && fallbackUsed {
		toolEvents = append(toolEvents, AssistantToolEvent{Name: "chat", Status: "warning", Message: "工具调用不可用，已回退普通对话"})
	}
	reply := domain.AssistantMessage{
		ID: domain.NewID("assistant_message"), AssistantID: assistant.ID,
		Role: domain.AssistantRoleAssistant, Content: replyText, CreatedAt: s.now().UTC(),
	}
	if err := s.store.AppendAssistantMessage(ctx, reply); err != nil {
		return OwnerAssistantReply{}, err
	}
	return OwnerAssistantReply{Message: userMessage, Reply: reply, ToolEvents: toolEvents, Actions: replyActions}, nil
}

// OwnerAssistantHints derives short, dismissible nudges from live state. It is
// deterministic: the pet only moves when there is a real failed/waiting task or
// a degraded channel.
func (s *Service) OwnerAssistantHints(ctx context.Context) ([]domain.AssistantHint, error) {
	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil {
		return nil, err
	}
	dismissals, err := s.store.AssistantHintDismissals(ctx, assistant.ID, s.now().UTC())
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	tasks, err := s.store.ListTasks(ctx, "")
	if err != nil {
		return nil, err
	}
	hints := []domain.AssistantHint{}
	for _, task := range tasks {
		if now.Sub(task.UpdatedAt) > 7*24*time.Hour {
			continue
		}
		label := strings.TrimSpace(task.Code)
		if label == "" {
			label = task.Title
		}
		switch task.Status {
		case domain.TaskFailed:
			hints = append(hints, domain.AssistantHint{
				Kind: "task_failed", TargetType: "task", TargetID: task.ID,
				Title: label + " 失败了", Body: assistantTaskSummary(task), Priority: 100,
			})
		case domain.TaskWaitingUser:
			hints = append(hints, domain.AssistantHint{
				Kind: "task_waiting", TargetType: "task", TargetID: task.ID,
				Title: label + " 在等你", Body: assistantTaskSummary(task), Priority: 80,
			})
		case domain.TaskBlocked:
			hints = append(hints, domain.AssistantHint{
				Kind: "task_blocked", TargetType: "task", TargetID: task.ID,
				Title: label + " 被阻塞了", Body: assistantTaskSummary(task), Priority: 70,
			})
		}
	}
	channels, err := s.store.ChannelInstances(ctx, "")
	if err == nil {
		for _, instance := range channels {
			if instance.Retired || (instance.Status != "degraded" && instance.Status != "error") {
				continue
			}
			hints = append(hints, domain.AssistantHint{
				Kind: "channel_degraded", TargetType: "channel", TargetID: instance.ID,
				Title: "渠道「" + instance.Name + "」异常", Body: "当前状态：" + instance.Status, Priority: 90,
			})
		}
	}
	filtered := hints[:0]
	for _, hint := range hints {
		if _, dismissed := dismissals[hintKey(hint.Kind, hint.TargetID)]; dismissed {
			continue
		}
		filtered = append(filtered, hint)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Priority != filtered[j].Priority {
			return filtered[i].Priority > filtered[j].Priority
		}
		return filtered[i].Title < filtered[j].Title
	})
	if len(filtered) > 5 {
		filtered = filtered[:5]
	}
	return filtered, nil
}

func (s *Service) DismissOwnerAssistantHint(ctx context.Context, kind, targetID string) error {
	kind, targetID = strings.TrimSpace(kind), strings.TrimSpace(targetID)
	if kind == "" || targetID == "" {
		return fmt.Errorf("kind and target_id are required")
	}
	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil {
		return err
	}
	return s.store.DismissAssistantHint(ctx, assistant.ID, kind, targetID, s.now().UTC().Add(10*time.Minute))
}

func (s *Service) assistantModelOptions(ctx context.Context) ([]AssistantModelOption, error) {
	models, err := s.store.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	options := []AssistantModelOption{}
	for _, model := range models {
		if model.Source != "" && model.Source != domain.ModelSourceProvider {
			continue
		}
		if model.ProviderID == "" {
			continue
		}
		wire := strings.TrimSpace(model.WireAPI)
		if wire == "" {
			wire = "chat"
		}
		if !gateway.SupportsChat(wire) {
			continue
		}
		options = append(options, AssistantModelOption{
			ID: model.ID, DisplayName: model.DisplayName, ProviderName: model.ProviderName, WireAPI: wire,
		})
	}
	sort.SliceStable(options, func(i, j int) bool {
		left := options[i].ProviderName + "/" + options[i].DisplayName
		right := options[j].ProviderName + "/" + options[j].DisplayName
		return left < right
	})
	return options, nil
}

// assistantRuntime resolves the one unified model. It returns the model, the
// Provider runtime values for the direct HTTP chat path, and the canonical
// source. It never substitutes a different model: an unavailable selection is
// an error the owner must see and fix.
func (s *Service) assistantRuntime(ctx context.Context, assistant *domain.OwnerAssistant) (domain.Model, domain.Provider, string, string, error) {
	modelID := unifiedAssistantModelID(*assistant)
	if modelID == "" {
		return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("还没有配置宠物模型，请在宠物设置的「模型来源 / 模型账号」中选择")
	}
	model, err := s.store.Model(ctx, modelID)
	if err != nil {
		return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型已删除或不存在，请在宠物设置的「模型来源 / 模型账号」中重新选择")
	}
	source := strings.TrimSpace(model.Source)
	if source == "" {
		source = domain.ModelSourceProvider
	}
	switch source {
	case domain.ModelSourceOfficial, domain.ModelSourceClaudeNative:
		if model.Backend != "codex" && model.Backend != "claude" {
			return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型的 Backend %q 不支持聊天", model.Backend)
		}
		return model, domain.Provider{}, "", source, nil
	case domain.ModelSourceProvider:
		if strings.TrimSpace(model.ProviderID) == "" {
			return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型缺少 Provider 配置，请重新选择")
		}
		wire := strings.TrimSpace(model.WireAPI)
		if wire == "" {
			wire = "chat"
		}
		if !gateway.SupportsChat(wire) {
			return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型协议 %q 不支持聊天", model.WireAPI)
		}
		provider, err := s.store.Provider(ctx, model.ProviderID)
		if err != nil {
			return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型的 Provider 不存在：%w", err)
		}
		apiKey := s.providerAPIKey(provider)
		if apiKey == "" && provider.CredentialConfigured {
			return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("Provider 凭据不可用，请在模型页重新填写")
		}
		return model, provider, apiKey, source, nil
	default:
		return domain.Model{}, domain.Provider{}, "", "", fmt.Errorf("宠物模型来源 %q 不支持聊天", source)
	}
}

// askAssistantThroughBackend runs the selected Claude native or Codex official
// model through the same backend adapter the Pet Task executes. The model,
// account, credentials, workspace and permission policy all come from the same
// pet runtime resolution; nothing here downgrades chat to a different mode.
func (s *Service) askAssistantBackend(ctx context.Context, model domain.Model, source, systemPrompt string, history []domain.AssistantMessage, content string) (string, error) {
	if s.executor == nil {
		return "", fmt.Errorf("聊天后端不可用")
	}
	if ok, reason := s.assistantModelAvailability(ctx, model, source); !ok {
		// The settings page and the chat runtime use one availability decision.
		// An explicitly unavailable account or login fails here with the same
		// wording instead of spawning a backend that can only fail later.
		return "", fmt.Errorf("%s", reason)
	}
	policy := s.petRuntimePolicy(ctx)
	assistantWorkspace, envGroup, environment, profileDir, err := s.assistantBackendEnvironment(ctx, model, source, policy)
	if err != nil {
		return "", err
	}
	if source == domain.ModelSourceOfficial && profileDir != "" && s.codex != nil {
		// The normal Turn writes a refreshed auth.json back to the account after
		// every run. Chat must do the same, on success or failure: otherwise the
		// next PrepareProfile restores the stale credential over the refreshed
		// one and later chat/autonomy calls authenticate with an expired token.
		defer func() {
			unlock := s.codex.LockAccount(model.CodexAccountID)
			defer unlock()
			s.codex.SyncProfile(context.Background(), model.CodexAccountID, assistantWorkspace, profileDir)
		}()
	}
	prompt := assistantBackendPrompt(systemPrompt, history, content)
	snapshot := domain.RuntimeConfigSnapshot{
		WorkspaceID:         assistantWorkspace.ID,
		Backend:             model.Backend,
		ModelID:             model.ID,
		WireModel:           model.WireModel,
		EnvGroupID:          envGroup.ID,
		EnvGroupRevision:    envGroup.Revision,
		CodexAccountID:      model.CodexAccountID,
		ProxyEnabled:        policy.ProxyEnabled,
		ReasoningEffort:     policy.ReasoningEffort,
		FastMode:            policy.FastMode,
		StreamIdleTimeoutMS: policy.StreamIdleTimeoutMS,
		StreamMaxRetries:    policy.StreamMaxRetries,
		PermissionsJSON:     permissionsJSON(policy.Filesystem, policy.Approval),
	}
	timeout := assistantBackendTimeout
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := s.executor.Execute(runCtx, ExecutionRequest{
		Task:             domain.Task{TaskWorkspacePath: assistantWorkspace.RootPath},
		Workspace:        assistantWorkspace,
		Snapshot:         snapshot,
		Model:            model,
		EnvGroup:         envGroup,
		Environment:      environment,
		Prompt:           prompt,
		BackendSessionID: "assistant_chat",
		Filesystem:       policy.Filesystem,
		Approval:         policy.Approval,
	}, func(ExecutionEvent) {})
	if err != nil {
		return "", err
	}
	reply := strings.TrimSpace(result.Reply)
	if reply == "" {
		return "", fmt.Errorf("模型没有返回内容")
	}
	return reply, nil
}

// petRuntimePolicy is the Pet Task's permission and runtime policy. Chat and
// autonomy both read it from the same runtime snapshot; if the pet Task has not
// been created yet, the same creation defaults are used.
type petRuntimePolicy struct {
	Filesystem          string
	Approval            string
	ReasoningEffort     string
	FastMode            bool
	ProxyEnabled        bool
	StreamIdleTimeoutMS int
	StreamMaxRetries    int
}

func (s *Service) petRuntimePolicy(ctx context.Context) petRuntimePolicy {
	policy := petRuntimePolicy{
		Filesystem: petTaskFilesystem, Approval: "never", ReasoningEffort: petTaskReasoningEffort,
	}
	agent, err := s.store.TaskAgent(ctx, petTaskID, "main")
	if err != nil {
		return policy
	}
	snapshot, err := s.store.RuntimeSnapshot(ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		return policy
	}
	filesystem, approval := parsePermissionsJSON(snapshot.PermissionsJSON)
	policy.Filesystem = filesystem
	policy.Approval = approval
	if effort := strings.TrimSpace(snapshot.ReasoningEffort); effort != "" {
		policy.ReasoningEffort = effort
	}
	policy.FastMode = snapshot.FastMode
	policy.ProxyEnabled = snapshot.ProxyEnabled
	policy.StreamIdleTimeoutMS = snapshot.StreamIdleTimeoutMS
	policy.StreamMaxRetries = snapshot.StreamMaxRetries
	return policy
}

// assistantBackendEnvironment builds the runtime environment for the one-shot
// backend chat from the same model row, Env Group and account the Pet Task uses.
func (s *Service) assistantBackendEnvironment(ctx context.Context, model domain.Model, source string, policy petRuntimePolicy) (domain.Workspace, domain.EnvGroup, map[string]string, string, error) {
	assistantWorkspace, err := s.ensurePetWorkspace(ctx)
	if err != nil {
		return domain.Workspace{}, domain.EnvGroup{}, nil, "", fmt.Errorf("准备宠物 Workspace 失败：%w", err)
	}
	envGroup, err := s.store.EnvGroup(ctx, model.DefaultEnvGroupID)
	if err != nil {
		return domain.Workspace{}, domain.EnvGroup{}, nil, "", fmt.Errorf("宠物模型的 Env Group 不可用：%w", err)
	}
	environment := make(map[string]string, len(envGroup.Environment)+len(envGroup.SecretRefs))
	for key, value := range envGroup.Environment {
		environment[key] = value
	}
	if s.secrets != nil {
		for name, secretRef := range envGroup.SecretRefs {
			if value, ok := s.secrets.Get(secretRef); ok {
				environment[name] = value
			}
		}
	}
	// Mirror the Pet Task's proxy decision: the same turn path applies store
	// proxy settings only when the snapshot asked for them.
	if policy.ProxyEnabled {
		if s.proxyRuntime != nil {
			if settingsErr := s.proxyRuntime.ApplyEnvironment(ctx, environment); settingsErr != nil {
				return domain.Workspace{}, domain.EnvGroup{}, nil, "", fmt.Errorf("应用代理设置失败：%w", settingsErr)
			}
		} else if settings, settingsErr := s.store.ProxySettings(ctx); settingsErr == nil {
			if normalized, normalizeErr := proxyconfig.Normalize(settings); normalizeErr == nil {
				proxyconfig.ApplyEnvironment(environment, normalized)
			}
		}
	}
	profileDir := ""
	if source == domain.ModelSourceOfficial {
		if s.codex == nil {
			return domain.Workspace{}, domain.EnvGroup{}, nil, "", fmt.Errorf("Codex 官方账号运行时不可用")
		}
		unlock := s.codex.LockAccount(model.CodexAccountID)
		profileDir, err = s.codex.PrepareProfile(ctx, model.CodexAccountID, assistantWorkspace, assistantWorkspace.RootPath, "assistant_chat")
		unlock()
		if err != nil {
			return domain.Workspace{}, domain.EnvGroup{}, nil, "", fmt.Errorf("准备 Codex 官方账号失败：%w", err)
		}
		environment["CODEX_HOME"] = profileDir
	}
	return assistantWorkspace, envGroup, environment, profileDir, nil
}

// assistantBackendPrompt renders the single prompt string a CLI backend takes.
// The text tool-call block is the only way a CLI chat can move the pet, because
// these backends have no function-calling parameter in this path.
func assistantBackendPrompt(systemPrompt string, history []domain.AssistantMessage, content string) string {
	var builder strings.Builder
	builder.WriteString("本轮是 Web 里的助手聊天：优先直接回答主人。需要时可以使用工作区和工具，但不要主动巡检 AHA，也不要为了聊天创建 Task。\n\n")
	builder.WriteString(strings.TrimSpace(systemPrompt))
	builder.WriteString("\n\n## 当前对话\n")
	for _, item := range history {
		role := "主人"
		if item.Role == domain.AssistantRoleAssistant {
			role = "你"
		}
		builder.WriteString(role + "：" + truncateAssistantText(item.Content, 800) + "\n")
	}
	builder.WriteString("主人：" + content + "\n\n请直接回复主人。")
	builder.WriteString("\n\n需要做动作时，在回复末尾按下面的格式输出（可多条）。动作是可选的，不要为了动作编造理由：\n")
	for _, tool := range assistantPetTools() {
		parameters, _ := json.Marshal(tool.Parameters)
		fmt.Fprintf(&builder, "- <tool_call>%s %s</tool_call_end>\n", tool.Name, parameters)
	}
	return builder.String()
}

func (s *Service) assistantSnapshot(ctx context.Context) (string, error) {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return "", err
	}
	workspaces, _ := s.store.ListWorkspaces(ctx, "")
	workspaceNames := make(map[string]string, len(workspaces))
	for _, workspace := range workspaces {
		workspaceNames[workspace.ID] = workspace.Name
	}
	projectNames := make(map[string]string, len(projects))
	var builder strings.Builder
	builder.WriteString("当前时间：" + s.now().UTC().Format(time.RFC3339) + "\n\n项目：\n")
	projectCount := 0
	for _, project := range projects {
		if project.ProjectType == "knowledge" {
			continue
		}
		projectNames[project.ID] = project.Name
		if projectCount >= 12 {
			continue
		}
		fmt.Fprintf(&builder, "- %s（%s）\n", project.Name, emptyAs(project.ProjectType, "folder"))
		projectCount++
	}
	if projectCount == 0 {
		builder.WriteString("- 暂无项目\n")
	}

	tasks, _ := s.store.ListTasks(ctx, "")
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt) })
	builder.WriteString("\n任务（按最近更新，最多 40 个）：\n")
	taskCount := 0
	for _, task := range tasks {
		if taskCount >= assistantTaskLimit {
			break
		}
		label := strings.TrimSpace(task.Code)
		if label == "" {
			label = "task"
		}
		project := projectNames[task.ProjectID]
		workspace := workspaceNames[task.WorkspaceID]
		readOnly := ""
		if task.ReadOnly {
			readOnly = " · 只读镜像"
		}
		fmt.Fprintf(&builder, "- [%s] %s | 状态 %s | 项目 %s | Workspace %s%s\n  目标：%s\n",
			label, truncateAssistantText(task.Title, 100), task.Status,
			emptyAs(project, "未知"), emptyAs(workspace, "未知"), readOnly,
			truncateAssistantText(task.CurrentGoal, 180))
		taskCount++
	}
	if taskCount == 0 {
		builder.WriteString("- 暂无任务\n")
	}

	// The assistant snapshot is global, not bound to a Project. When global
	// knowledge reads the vault, list it there; a vault read failure leaves the
	// section empty with a warning instead of silently showing legacy titles.
	knowledge := []domain.KnowledgeEntry{}
	if s.vaultGlobalReadEnabled() {
		items, ok := s.vaultGlobalKnowledge(ctx)
		if !ok {
			log.Printf("assistant snapshot: vault global knowledge is unavailable; omitting knowledge titles")
		}
		for _, entry := range items {
			if entry.Status == domain.KnowledgeVerified {
				knowledge = append(knowledge, entry)
			}
		}
	} else {
		knowledge, _ = s.store.ListKnowledge(ctx, "", "", []domain.KnowledgeStatus{domain.KnowledgeVerified})
	}
	if len(knowledge) > 0 {
		builder.WriteString("\n已验证知识（标题，最多 25 条）：\n")
		limit := len(knowledge)
		if limit > 25 {
			limit = 25
		}
		for _, entry := range knowledge[:limit] {
			fmt.Fprintf(&builder, "- %s\n", truncateAssistantText(entry.Title, 100))
		}
	}

	models, _ := s.assistantModelOptions(ctx)
	if len(models) > 0 {
		builder.WriteString("\n可用助手模型：\n")
		for _, model := range models {
			fmt.Fprintf(&builder, "- %s / %s\n", emptyAs(model.ProviderName, "Provider"), model.DisplayName)
		}
	}

	hardware, _ := s.store.HardwareResources(ctx)
	if len(hardware) > 0 {
		builder.WriteString("\n硬件资源：\n")
		for index, resource := range hardware {
			if index >= 12 {
				break
			}
			fmt.Fprintf(&builder, "- %s（%s，%s）\n", truncateAssistantText(resource.Description, 80), resource.Mode, resource.Access)
		}
	}
	channels, _ := s.store.ChannelInstances(ctx, "")
	if len(channels) > 0 {
		builder.WriteString("\n渠道实例：\n")
		for index, instance := range channels {
			if index >= 10 {
				break
			}
			fmt.Fprintf(&builder, "- %s：%s\n", truncateAssistantText(instance.Name, 60), instance.Status)
		}
	}
	return builder.String(), nil
}

func (s *Service) assistantSystemPrompt(ctx context.Context, snapshot string, nickname string, identity string, principles string, layout PetLayout) (string, error) {
	return s.renderPetAssistantPrompt(ctx, nickname, identity, principles, snapshot, layout)
}

func wantsPetAction(content string) bool {
	value := strings.ToLower(strings.TrimSpace(content))
	for _, keyword := range []string{"走", "跑", "跳", "左", "右", "上", "下", "开心", "高兴", "生气", "怒", "难过", "哭", "睡", "醒", "打开", "task-", "表情", "动作", "过来", "dock"} {
		if strings.Contains(value, keyword) {
			return true
		}
	}
	return false
}

func assistantNickname(assistant *domain.OwnerAssistant) string {
	if assistant != nil && assistant.Character == "deepseek_fish" {
		return "大肥鱼"
	}
	return "咕咕嘎嘎"
}

func assistantTaskSummary(task domain.Task) string {
	goal := strings.TrimSpace(task.CurrentGoal)
	if goal == "" {
		goal = strings.TrimSpace(task.Title)
	}
	return truncateAssistantText(goal, 140)
}

func truncateAssistantText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// assistantCharacterNone is the persisted "no appearance" choice. It only
// hides the Web avatar; every pet capability keeps running under this value.
const assistantCharacterNone = "none"

func assistantCharacterAllowed(value string) bool {
	switch value {
	case "gugugaga", "deepseek_fish", assistantCharacterNone:
		return true
	default:
		return false
	}
}

func hintKey(kind, targetID string) string {
	return kind + "\x00" + targetID
}
