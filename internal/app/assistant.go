package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/gateway"
)

const (
	ownerAssistantID      = "assistant_default"
	assistantHistoryLimit = 12
	assistantTaskLimit    = 40
	assistantAskTimeout   = 60 * time.Second
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
	Assistant      domain.OwnerAssistant     `json:"assistant"`
	Messages       []domain.AssistantMessage `json:"messages"`
	Models         []AssistantModelOption    `json:"models"`
	ModelName      string                    `json:"model_name,omitempty"`
	BrainModelName string                    `json:"brain_model_name,omitempty"`
	Tools          []PetToolInfo             `json:"tools"`
	PetLastBrainAt time.Time                 `json:"pet_last_brain_at,omitempty"`
	PetNextBrainAt time.Time                 `json:"pet_next_brain_at,omitempty"`
	PetBrainRuns   int                       `json:"pet_brain_runs"`
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
// and the provider models it may use. It creates the singleton on first use.
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
	models, err := s.assistantModelOptions(ctx)
	if err != nil {
		return OwnerAssistantView{}, err
	}
	if len(models) > 0 {
		selected := false
		for _, model := range models {
			if model.ID == assistant.ModelID {
				selected = true
				break
			}
		}
		if !selected {
			assistant.ModelID = models[0].ID
			assistant.UpdatedAt = s.now().UTC()
			if err := s.store.UpdateOwnerAssistant(ctx, assistant); err != nil {
				return OwnerAssistantView{}, err
			}
		}
	}
	messages, err := s.store.AssistantMessages(ctx, assistant.ID, 50)
	if err != nil {
		return OwnerAssistantView{}, err
	}
	view := OwnerAssistantView{Assistant: assistant, Messages: messages, Models: models}
	for _, model := range models {
		if model.ID == assistant.ModelID {
			view.ModelName = model.DisplayName
		}
		if model.ID == assistant.BrainModelID {
			view.BrainModelName = model.DisplayName
		}
	}
	if view.BrainModelName == "" {
		view.BrainModelName = "跟随聊天模型"
	}
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
	if modelID = strings.TrimSpace(modelID); modelID != "" {
		models, modelErr := s.assistantModelOptions(ctx)
		if modelErr != nil {
			return OwnerAssistantView{}, modelErr
		}
		found := false
		for _, model := range models {
			if model.ID == modelID {
				found = true
				break
			}
		}
		if !found {
			return OwnerAssistantView{}, fmt.Errorf("所选模型不可用于助手，请选择 Provider 模型")
		}
		assistant.ModelID = modelID
	}
	assistant.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateOwnerAssistant(ctx, assistant); err != nil {
		return OwnerAssistantView{}, err
	}
	if state, stateErr := s.OwnerPetState(ctx); stateErr == nil {
		s.publishPetState(state)
	}
	return s.OwnerAssistant(ctx)
}

// AskOwnerAssistant answers one read-only question. It never creates Tasks or
// calls a mutating service method; the model only sees a bounded snapshot.
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
	model, provider, apiKey, err := s.assistantRuntime(ctx, &assistant)
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
	chatOptions := gateway.ChatOptions{
		BaseURL: provider.BaseURL, AnthropicBaseURL: provider.AnthropicBaseURL,
		APIKey: apiKey, AuthStyle: provider.AuthStyle, WireAPI: model.WireAPI,
		Model: wireModel, System: assistantSystemPromptText, Messages: messages,
		MaxTokens: 3000, Timeout: assistantAskTimeout,
	}
	fallbackUsed := false
	chatOptions.ForceTool = wantsPetAction(content)
	toolResult, callErr := gateway.ChatWithTools(ctx, chatOptions, assistantPetTools())
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
	if len(toolResult.ToolCalls) == 0 {
		if calls, cleaned := parseTextToolCalls(toolResult.Text); len(calls) > 0 {
			toolResult.ToolCalls = calls
			toolResult.Text = cleaned
		}
	}
	replyText := toolResult.Text
	if callErr != nil {
		replyText = "我暂时没连上模型：" + truncateAssistantText(callErr.Error(), 240) + "\n\n你可以在助手面板右上角换一个模型，或到模型页检查 Provider。"
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
			if _, err := s.ControlOwnerPet(ctx, PetControlRequest{Actions: []PetBrainAction{action}, Actor: "model"}); err != nil {
				toolEvents = append(toolEvents, AssistantToolEvent{Name: call.Name, Status: "error", Message: err.Error(), Arguments: call.Arguments})
				continue
			}
			replyActions = append(replyActions, action)
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

func (s *Service) assistantRuntime(ctx context.Context, assistant *domain.OwnerAssistant) (domain.Model, domain.Provider, string, error) {
	return s.assistantRuntimeByID(ctx, assistant, assistant.ModelID)
}

func (s *Service) assistantRuntimeByID(ctx context.Context, assistant *domain.OwnerAssistant, requestedModelID string) (domain.Model, domain.Provider, string, error) {
	options, err := s.assistantModelOptions(ctx)
	if err != nil {
		return domain.Model{}, domain.Provider{}, "", err
	}
	if len(options) == 0 {
		return domain.Model{}, domain.Provider{}, "", fmt.Errorf("还没有可用于助手的 Provider 模型，请先在模型页配置 chat 或 anthropic 模型")
	}
	modelID := strings.TrimSpace(requestedModelID)
	if modelID == "" {
		modelID = assistant.ModelID
	}
	eligible := false
	for _, option := range options {
		if option.ID == modelID {
			eligible = true
			break
		}
	}
	if !eligible {
		modelID = assistant.ModelID
		for _, option := range options {
			if option.ID == modelID {
				eligible = true
				break
			}
		}
		if !eligible {
			modelID = options[0].ID
			assistant.ModelID = modelID
			assistant.UpdatedAt = s.now().UTC()
			if err := s.store.UpdateOwnerAssistant(ctx, *assistant); err != nil {
				return domain.Model{}, domain.Provider{}, "", err
			}
		}
	}
	model, err := s.store.Model(ctx, modelID)
	if err != nil {
		return domain.Model{}, domain.Provider{}, "", err
	}
	provider, err := s.store.Provider(ctx, model.ProviderID)
	if err != nil {
		return domain.Model{}, domain.Provider{}, "", fmt.Errorf("Provider 不存在：%w", err)
	}
	apiKey := ""
	if provider.CredentialRef != "" && s.secrets != nil {
		apiKey, _ = s.secrets.Get(provider.CredentialRef)
	}
	if apiKey == "" && provider.CredentialConfigured {
		return domain.Model{}, domain.Provider{}, "", fmt.Errorf("Provider 凭据不可用，请在模型页重新填写")
	}
	return model, provider, apiKey, nil
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

	knowledge, _ := s.store.ListKnowledge(ctx, "", "", []domain.KnowledgeStatus{domain.KnowledgeVerified})
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

func assistantCharacterAllowed(value string) bool {
	switch value {
	case "gugugaga", "deepseek_fish":
		return true
	default:
		return false
	}
}

func hintKey(kind, targetID string) string {
	return kind + "\x00" + targetID
}
