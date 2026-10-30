package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/gateway"
)

type PetLayoutRect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

type PetLayoutElement struct {
	Name string `json:"name"`
	PetLayoutRect
}

type PetLayout struct {
	ViewportWidth  int                `json:"viewport_width"`
	ViewportHeight int                `json:"viewport_height"`
	ScrollY        float64            `json:"scroll_y"`
	Pet            PetLayoutRect      `json:"pet"`
	Panel          *PetLayoutRect     `json:"panel,omitempty"`
	Elements       []PetLayoutElement `json:"elements,omitempty"`
}

type PetBrainContext struct {
	Page        string    `json:"page"`
	IdleSeconds int       `json:"idle_seconds"`
	DeviceID    string    `json:"device_id"`
	Layout      PetLayout `json:"layout"`
}

type PetPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type PetBrainAction struct {
	Type       string     `json:"type"`
	Name       string     `json:"name,omitempty"`
	Zone       string     `json:"zone,omitempty"`
	Task       string     `json:"task,omitempty"`
	X          *float64   `json:"x,omitempty"`
	Y          *float64   `json:"y,omitempty"`
	Points     []PetPoint `json:"points,omitempty"`
	Radius     float64    `json:"radius,omitempty"`
	Turns      float64    `json:"turns,omitempty"`
	DurationMS int        `json:"duration_ms,omitempty"`
	Active     bool       `json:"active,omitempty"`
}

type PetBrainResult struct {
	Say     string           `json:"say"`
	Actions []PetBrainAction `json:"actions"`
}

var petMoods = map[string]bool{"neutral": true, "happy": true, "worried": true, "angry": true, "sad": true, "sleepy": true}
var petActions = map[string]bool{"idle": true, "walk": true, "jump": true, "happy": true, "angry": true, "sleep": true}

func (s *Service) OwnerPetState(ctx context.Context) (domain.PetState, error) {
	state, err := s.store.EnsurePetState(ctx, s.now().UTC())
	if err != nil {
		return domain.PetState{}, err
	}
	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil {
		return domain.PetState{}, err
	}
	state.Character = assistant.Character
	return state, nil
}

func (s *Service) UpdateOwnerPetState(ctx context.Context, patch domain.PetStatePatch) (domain.PetState, error) {
	state, err := s.OwnerPetState(ctx)
	if err != nil {
		return domain.PetState{}, err
	}
	if patch.Mood != nil {
		mood := strings.TrimSpace(strings.ToLower(*patch.Mood))
		if !petMoods[mood] {
			return domain.PetState{}, fmt.Errorf("不支持的宠物心情：%s", mood)
		}
		state.Mood = mood
	}
	if patch.Action != nil {
		action := strings.TrimSpace(strings.ToLower(*patch.Action))
		if !petActions[action] {
			return domain.PetState{}, fmt.Errorf("不支持的宠物动作：%s", action)
		}
		state.Action = action
	}
	if patch.Zone != nil {
		zone := strings.TrimSpace(*patch.Zone)
		if len([]rune(zone)) > 40 {
			return domain.PetState{}, fmt.Errorf("宠物区域名称过长")
		}
		state.Zone = zone
	}
	if patch.X != nil {
		state.X = mathClamp(*patch.X, 0, 1)
	}
	if patch.Y != nil {
		state.Y = mathClamp(*patch.Y, 0, 1)
	}
	if patch.Facing != nil {
		if *patch.Facing < 0 {
			state.Facing = -1
		} else {
			state.Facing = 1
		}
	}
	if patch.Sleeping != nil {
		state.Sleeping = *patch.Sleeping
	}
	if patch.Quiet != nil {
		state.Quiet = *patch.Quiet
	}
	state.UpdatedAt = s.now().UTC()
	saved, err := s.store.SavePetState(ctx, state, patch.Version)
	if err != nil {
		return domain.PetState{}, err
	}
	saved.Character = state.Character
	s.publishPetState(saved)
	return saved, nil
}

func (s *Service) publishPetState(state domain.PetState) {
	s.hub.Publish("", domain.Event{
		ID:            domain.NewID("pet_event"),
		AggregateType: "pet",
		AggregateID:   state.ID,
		Type:          "pet_state",
		Data:          map[string]any{"state": state},
		OccurredAt:    s.now().UTC(),
	})
}

func mathClamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

type PetPromptView struct {
	Chat  string `json:"chat"`
	Brain string `json:"brain"`
}

func (s *Service) OwnerPetPrompts(ctx context.Context) (PetPromptView, error) {
	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil {
		return PetPromptView{}, err
	}
	chat, err := s.assistantSystemPrompt(ctx, "（运行时注入只读快照）", assistantNickname(&assistant), assistant.PetIdentity, assistant.PetPrinciples, PetLayout{})
	if err != nil {
		return PetPromptView{}, err
	}
	return PetPromptView{Chat: chat, Brain: chat}, nil
}

// AskOwnerPetBrain lets the pet decide what to do after a period of owner
// inactivity. It is deliberately a small, read-only decision: the model may
// speak, play an allowed motion, move to a semantic zone, or open a task.
func (s *Service) AskOwnerPetBrain(ctx context.Context, input PetBrainContext) (PetBrainResult, error) {
	deviceID := strings.TrimSpace(input.DeviceID)
	if deviceID == "" {
		deviceID = "default"
	}
	if len(deviceID) > 80 {
		deviceID = deviceID[:80]
	}
	now := s.now().UTC()
	s.petBrainMu.Lock()
	if s.petBrainAt == nil {
		s.petBrainAt = map[string]time.Time{}
	}
	if s.petBrainRuns == nil {
		s.petBrainRuns = map[string][]time.Time{}
	}
	if last, ok := s.petBrainAt[deviceID]; ok && now.Sub(last) < 30*time.Second {
		s.petBrainMu.Unlock()
		return PetBrainResult{}, fmt.Errorf("宠物刚刚动过，稍后再试")
	}
	s.petBrainAt[deviceID] = now
	cutoff := now.Add(-time.Hour)
	runs := s.petBrainRuns[deviceID][:0]
	for _, at := range s.petBrainRuns[deviceID] {
		if at.After(cutoff) {
			runs = append(runs, at)
		}
	}
	s.petBrainRuns[deviceID] = append(runs, now)
	s.petBrainMu.Unlock()

	assistant, err := s.store.EnsureOwnerAssistant(ctx, now)
	if err != nil {
		return PetBrainResult{}, err
	}
	if petQuietNow(assistant, time.Now()) {
		return PetBrainResult{}, fmt.Errorf("现在处于宠物安静时段")
	}
	if assistant.PetHourlyLimit > 0 {
		s.petBrainMu.Lock()
		count := len(s.petBrainRuns[deviceID])
		s.petBrainMu.Unlock()
		if count > assistant.PetHourlyLimit {
			return PetBrainResult{}, fmt.Errorf("宠物这个小时已经够活跃了")
		}
	}
	idleMinutes := input.IdleSeconds / 60
	if idleMinutes < 1 {
		idleMinutes = 1
	}
	page := strings.TrimSpace(input.Page)
	if page == "" {
		page = "未知页面"
	}
	if _, err := s.ensureOwnerPetTask(ctx, &assistant); err != nil {
		return PetBrainResult{}, fmt.Errorf("确保宠物 Task 可用失败：%w", err)
	}
	task, err := s.store.Task(ctx, petTaskID)
	if err != nil {
		return PetBrainResult{}, fmt.Errorf("宠物 Task 不存在")
	}
	if task.Status == domain.TaskDraft {
		return PetBrainResult{}, fmt.Errorf("宠物 Task 尚未启动")
	}
	if task.Status != domain.TaskWaitingUser && task.Status != domain.TaskActive {
		return PetBrainResult{}, fmt.Errorf("宠物 Task 状态为 %s，暂时无法自主行动", task.Status)
	}
	if _, activeErr := s.store.ActiveTurn(ctx, petTaskID); activeErr == nil {
		return PetBrainResult{}, fmt.Errorf("宠物 Task 正在执行，跳过本次自主行为")
	}
	message := fmt.Sprintf("[自主行为] 主人已经 %d 分钟没有互动。请按宠物身份决定是否说话或做动作；想做什么就调用宠物工具，不要写代码。当前页面：%s。\n\n页面布局：\n%s", idleMinutes, page, petLayoutPrompt(input.Layout))
	if _, err := s.SubmitMessage(ctx, petTaskID, message); err != nil {
		return PetBrainResult{}, err
	}
	return PetBrainResult{}, nil
}

func petActionsFromToolCalls(calls []gateway.ChatToolCall, limit int) []PetBrainAction {
	allowedPlay := map[string]bool{"walk": true, "jump": true, "happy": true, "angry": true}
	allowedZone := map[string]bool{"left": true, "right": true, "top": true, "bottom": true, "dock": true}
	allowedMood := map[string]bool{"neutral": true, "happy": true, "worried": true, "angry": true, "sad": true, "sleepy": true}
	actions := make([]PetBrainAction, 0, limit)
	for _, call := range calls {
		if len(actions) >= limit {
			break
		}
		action := PetBrainAction{}
		switch call.Name {
		case "pet_play":
			var args struct {
				Name string `json:"name"`
			}
			if json.Unmarshal([]byte(call.Arguments), &args) == nil && allowedPlay[args.Name] {
				action = PetBrainAction{Type: "play", Name: args.Name}
			}
		case "pet_move":
			var args struct {
				Zone string `json:"zone"`
			}
			if json.Unmarshal([]byte(call.Arguments), &args) == nil && allowedZone[args.Zone] {
				action = PetBrainAction{Type: "move_to", Zone: args.Zone}
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
				}
			}
		case "pet_mood":
			var args struct {
				Mood string `json:"mood"`
			}
			if json.Unmarshal([]byte(call.Arguments), &args) == nil && allowedMood[args.Mood] {
				action = PetBrainAction{Type: "mood", Name: args.Mood}
			}
		case "pet_open_task":
			var args struct {
				Task string `json:"task"`
			}
			if json.Unmarshal([]byte(call.Arguments), &args) == nil && strings.HasPrefix(args.Task, "task-") {
				action = PetBrainAction{Type: "open_task", Task: args.Task}
			}
		case "pet_sleep":
			action = PetBrainAction{Type: "sleep"}
		case "pet_thinking":
			var args struct {
				Active bool `json:"active"`
			}
			if json.Unmarshal([]byte(call.Arguments), &args) == nil {
				action = PetBrainAction{Type: "thinking", Active: args.Active}
			}
		case "pet_wake":
			action = PetBrainAction{Type: "wake"}
		}
		if action.Type != "" {
			actions = append(actions, action)
		}
	}
	return actions
}

func petLayoutPrompt(layout PetLayout) string {
	if layout.ViewportWidth <= 0 || layout.ViewportHeight <= 0 {
		return "页面布局未知；只能用区域移动。"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "视口 %dx%d，滚动 %.0f；宠物矩形 x=%.0f y=%.0f w=%.0f h=%.0f。\n",
		layout.ViewportWidth, layout.ViewportHeight, layout.ScrollY,
		layout.Pet.X, layout.Pet.Y, layout.Pet.W, layout.Pet.H)
	if layout.Panel != nil {
		fmt.Fprintf(&b, "对话面板 x=%.0f y=%.0f w=%.0f h=%.0f。\n",
			layout.Panel.X, layout.Panel.Y, layout.Panel.W, layout.Panel.H)
	}
	for _, element := range layout.Elements {
		fmt.Fprintf(&b, "- %s: x=%.0f y=%.0f w=%.0f h=%.0f\n",
			element.Name, element.X, element.Y, element.W, element.H)
	}
	b.WriteString("移动时用 pet_move_to 的 x/y 视口比例；避免挡住输入框、按钮和对话面板正文。")
	return b.String()
}

func petPersonaPrompt(identity, principles string) string {
	result := ""
	if strings.TrimSpace(identity) != "" {
		result += "\n身份设定：\n" + identity + "\n"
	}
	if strings.TrimSpace(principles) != "" {
		result += "\n做事准则：\n" + principles + "\n"
	}
	return result
}

func parsePetBrain(text string) PetBrainResult {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return PetBrainResult{}
	}
	var raw struct {
		Say     string           `json:"say"`
		Actions []PetBrainAction `json:"actions"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return PetBrainResult{}
	}
	result := PetBrainResult{Say: strings.TrimSpace(raw.Say)}
	if len([]rune(result.Say)) > 120 {
		result.Say = string([]rune(result.Say)[:120])
	}
	allowedPlay := map[string]bool{"walk": true, "jump": true, "happy": true, "angry": true}
	allowedZone := map[string]bool{"left": true, "right": true, "top": true, "bottom": true, "dock": true}
	for _, action := range raw.Actions {
		if len(result.Actions) >= 2 {
			break
		}
		action.Type = strings.TrimSpace(strings.ToLower(action.Type))
		switch action.Type {
		case "play":
			if allowedPlay[action.Name] {
				result.Actions = append(result.Actions, action)
			}
		case "move_to":
			if allowedZone[action.Zone] {
				result.Actions = append(result.Actions, action)
			} else if action.X != nil && action.Y != nil && *action.X >= 0 && *action.X <= 1 && *action.Y >= 0 && *action.Y <= 1 {
				result.Actions = append(result.Actions, action)
			}
		case "open_task":
			if strings.HasPrefix(action.Task, "task-") {
				result.Actions = append(result.Actions, action)
			}
		}
	}
	return result
}

type PetSettingsInput struct {
	Enabled      bool     `json:"enabled"`
	Character    string   `json:"character"`
	ChatModelID  string   `json:"chat_model_id"`
	BrainModelID string   `json:"brain_model_id"`
	Actions      []string `json:"actions"`
	Identity     string   `json:"identity"`
	Principles   string   `json:"principles"`
	IdleMinutes  int      `json:"idle_minutes"`
	QuietEnabled *bool    `json:"quiet_enabled"`
	QuietStart   int      `json:"quiet_start"`
	QuietEnd     int      `json:"quiet_end"`
	HourlyLimit  int      `json:"hourly_limit"`
}

func (s *Service) UpdateOwnerPetSettings(ctx context.Context, input PetSettingsInput) (OwnerAssistantView, error) {
	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil {
		return OwnerAssistantView{}, err
	}
	if character := strings.TrimSpace(input.Character); character != "" {
		if !assistantCharacterAllowed(character) {
			return OwnerAssistantView{}, fmt.Errorf("不支持的形象：%s", character)
		}
		assistant.Character = character
	}
	models, err := s.assistantModelOptions(ctx)
	if err != nil {
		return OwnerAssistantView{}, err
	}
	validModel := func(id string) bool {
		for _, model := range models {
			if model.ID == id {
				return true
			}
		}
		return false
	}
	if chatModelID := strings.TrimSpace(input.ChatModelID); chatModelID != "" {
		if !validModel(chatModelID) {
			return OwnerAssistantView{}, fmt.Errorf("宠物模型不可用")
		}
		assistant.ModelID = chatModelID
	}
	// Chat and autonomous behavior share one model. The old brain_model_id
	// field is kept in the stored row for compatibility but mirrors model_id.
	assistant.BrainModelID = assistant.ModelID
	allowed := map[string]bool{"walk": true, "jump": true, "happy": true, "angry": true}
	actions := []string{}
	seen := map[string]bool{}
	for _, action := range input.Actions {
		action = strings.TrimSpace(strings.ToLower(action))
		if allowed[action] && !seen[action] {
			seen[action] = true
			actions = append(actions, action)
		}
	}
	if len(actions) == 0 {
		return OwnerAssistantView{}, fmt.Errorf("至少保留一个动作")
	}
	assistant.PetActions = actions
	assistant.PetEnabled = input.Enabled
	idleMinutes := input.IdleMinutes
	if idleMinutes == 0 {
		idleMinutes = 5
	}
	quietStart := input.QuietStart
	quietEnd := input.QuietEnd
	hourlyLimit := input.HourlyLimit
	if idleMinutes < 1 || idleMinutes > 120 {
		return OwnerAssistantView{}, fmt.Errorf("空闲触发时间需在 1–120 分钟")
	}
	if quietStart < 0 || quietStart > 23 || quietEnd < 0 || quietEnd > 23 {
		return OwnerAssistantView{}, fmt.Errorf("免打扰时间需在 0–23 点")
	}
	if hourlyLimit < 0 || hourlyLimit > 60 {
		return OwnerAssistantView{}, fmt.Errorf("每小时上限需在 0–60 次")
	}
	assistant.PetIdleMinutes = idleMinutes
	if input.QuietEnabled != nil {
		assistant.PetQuietEnabled = *input.QuietEnabled
	}
	assistant.PetQuietStart = quietStart
	assistant.PetQuietEnd = quietEnd
	assistant.PetHourlyLimit = hourlyLimit
	// Identity/principles are system-managed for now; user-supplied values
	// are ignored until the write permission is explicitly enabled.
	assistant.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateOwnerAssistant(ctx, assistant); err != nil {
		return OwnerAssistantView{}, err
	}
	if err := s.store.UpdateOwnerPetSettings(ctx, assistant.ID, assistant.PetEnabled, assistant.BrainModelID, assistant.PetActions, assistant.UpdatedAt); err != nil {
		return OwnerAssistantView{}, err
	}
	if err := s.store.UpdateOwnerPetBudget(ctx, assistant.ID, assistant.PetIdleMinutes, assistant.PetQuietEnabled, assistant.PetQuietStart, assistant.PetQuietEnd, assistant.PetHourlyLimit, assistant.UpdatedAt); err != nil {
		return OwnerAssistantView{}, err
	}
	if err := s.store.UpdateOwnerPetIdentity(ctx, assistant.ID, assistant.PetIdentity, assistant.PetPrinciples, assistant.UpdatedAt); err != nil {
		return OwnerAssistantView{}, err
	}
	if err := s.syncPetTaskRuntime(ctx, assistant.ModelID); err != nil {
		return OwnerAssistantView{}, fmt.Errorf("宠物 Task 模型同步失败：%w", err)
	}
	if state, stateErr := s.OwnerPetState(ctx); stateErr == nil {
		s.publishPetState(state)
	}
	return s.OwnerAssistant(ctx)
}

func (s *Service) OwnerPetActivity(ctx context.Context, limit int) ([]domain.PetEvent, error) {
	return s.store.ListPetEvents(ctx, limit)
}

type PetControlRequest struct {
	Say     string           `json:"say"`
	Actions []PetBrainAction `json:"actions"`
	Actor   string           `json:"actor"`
}

// ControlOwnerPet is the owner/model-facing pet control API. It validates
// intent-level actions and broadcasts them to connected Web clients; it never
// mutates tasks or exposes general AHA operations.
func (s *Service) ControlOwnerPet(ctx context.Context, input PetControlRequest) (PetBrainResult, error) {
	allowedPlay := map[string]bool{"walk": true, "jump": true, "happy": true, "angry": true}
	allowedZone := map[string]bool{"left": true, "right": true, "top": true, "bottom": true, "dock": true}
	allowedMood := map[string]bool{"neutral": true, "happy": true, "worried": true, "angry": true, "sad": true, "sleepy": true}
	result := PetBrainResult{Say: strings.TrimSpace(input.Say)}
	if len([]rune(result.Say)) > 120 {
		return PetBrainResult{}, fmt.Errorf("宠物说话不能超过 120 个字符")
	}
	for _, action := range input.Actions {
		if len(result.Actions) >= 3 {
			break
		}
		action.Type = strings.TrimSpace(strings.ToLower(action.Type))
		action.Name = strings.TrimSpace(strings.ToLower(action.Name))
		action.Zone = strings.TrimSpace(strings.ToLower(action.Zone))
		action.Task = strings.TrimSpace(action.Task)
		switch action.Type {
		case "play":
			if allowedPlay[action.Name] {
				result.Actions = append(result.Actions, action)
			}
		case "move_to":
			if allowedZone[action.Zone] {
				result.Actions = append(result.Actions, action)
			} else if action.X != nil && action.Y != nil && *action.X >= 0 && *action.X <= 1 && *action.Y >= 0 && *action.Y <= 1 {
				result.Actions = append(result.Actions, action)
			}
		case "walk_path":
			if len(action.Points) >= 2 && len(action.Points) <= 32 {
				valid := true
				for _, point := range action.Points {
					if point.X < 0 || point.X > 1 || point.Y < 0 || point.Y > 1 {
						valid = false
						break
					}
				}
				if valid {
					result.Actions = append(result.Actions, action)
				}
			}
		case "mood":
			if allowedMood[action.Name] {
				result.Actions = append(result.Actions, action)
			}
		case "open_task":
			if strings.HasPrefix(action.Task, "task-") {
				if _, err := s.store.Task(ctx, action.Task); err == nil {
					result.Actions = append(result.Actions, action)
				}
			}
		case "sleep", "wake", "thinking":
			result.Actions = append(result.Actions, action)
		}
	}
	if result.Say == "" && len(result.Actions) == 0 {
		return PetBrainResult{}, fmt.Errorf("没有可执行的宠物动作")
	}
	s.hub.Publish("", domain.Event{
		ID: domain.NewID("pet_event"), AggregateType: "pet", AggregateID: "owner_pet",
		Type: "pet_action", OccurredAt: s.now().UTC(),
		Data: map[string]any{"say": result.Say, "actions": result.Actions},
	})
	actor := strings.TrimSpace(input.Actor)
	if actor == "" {
		actor = "api"
	}
	detail, _ := json.Marshal(map[string]any{"say": result.Say, "actions": result.Actions})
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: actor, Kind: "control",
		Detail: string(detail), Status: "acted",
	})
	return result, nil
}

func (s *Service) PetMCPTools() []gateway.ChatTool {
	return assistantPetTools()
}

// ControlOwnerPetTool maps one MCP tool call to a validated pet action.
func (s *Service) ControlOwnerPetTool(ctx context.Context, name string, args json.RawMessage) (PetBrainResult, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	actions := petActionsFromToolCalls([]gateway.ChatToolCall{{Name: name, Arguments: string(args)}}, 1)
	if len(actions) == 0 {
		return PetBrainResult{}, fmt.Errorf("pet tool %q rejected its arguments", name)
	}
	return s.ControlOwnerPet(ctx, PetControlRequest{Actions: actions, Actor: "mcp"})
}

func petToolInfos() []PetToolInfo {
	tools := assistantPetTools()
	infos := make([]PetToolInfo, 0, len(tools))
	for _, tool := range tools {
		infos = append(infos, PetToolInfo{Name: tool.Name, Description: tool.Description})
	}
	return infos
}

func assistantPetTools() []gateway.ChatTool {
	return []gateway.ChatTool{
		{Name: "pet_play", Description: "播放宠物动作", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "enum": []string{"walk", "jump", "happy", "angry"}}},
			"required": []string{"name"},
		}},
		{Name: "pet_move_to", Description: "把宠物移动到页面任意位置，x/y 为 0–1 的视口比例，表示宠物中心", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"x":           map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				"y":           map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				"duration_ms": map[string]any{"type": "integer", "minimum": 300, "maximum": 10000},
			},
			"required": []string{"x", "y"},
		}},
		{Name: "pet_walk_path", Description: "沿一组屏幕坐标连续行走，可用来画任意路径；points 为 0–1 视口比例数组", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"points": map[string]any{"type": "array", "minItems": 2, "maxItems": 128, "items": map[string]any{
					"type": "object", "properties": map[string]any{
						"x": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
						"y": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					}, "required": []string{"x", "y"},
				}},
				"duration_ms": map[string]any{"type": "integer", "minimum": 1000, "maximum": 30000},
			}, "required": []string{"points"},
		}},
		{Name: "pet_mood", Description: "设置宠物心情", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"mood": map[string]any{"type": "string", "enum": []string{"neutral", "happy", "worried", "angry", "sad", "sleepy"}}},
			"required": []string{"mood"},
		}},
		{Name: "pet_open_task", Description: "在网页中打开一个 Task（只读导航）", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task": map[string]any{"type": "string"}}, "required": []string{"task"},
		}},
		{Name: "pet_thinking", Description: "控制宠物头顶的思考问号（true 显示 / false 隐藏）", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"active": map[string]any{"type": "boolean"}}, "required": []string{"active"},
		}},
		{Name: "pet_sleep", Description: "让宠物睡觉", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		{Name: "pet_wake", Description: "唤醒宠物", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
	}
}

func petQuietNow(assistant domain.OwnerAssistant, now time.Time) bool {
	if !assistant.PetQuietEnabled {
		return false
	}
	start, end := assistant.PetQuietStart, assistant.PetQuietEnd
	if start == end {
		return false
	}
	hour := now.Hour()
	if start < end {
		return hour >= start && hour < end
	}
	return hour >= start || hour < end
}
