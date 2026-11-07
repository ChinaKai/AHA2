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
	if last, ok := s.petBrainAt[deviceID]; ok && now.Sub(last) < 30*time.Second {
		s.petBrainMu.Unlock()
		return PetBrainResult{}, fmt.Errorf("宠物刚刚动过，稍后再试")
	}
	s.petBrainAt[deviceID] = now
	s.petBrainMu.Unlock()

	assistant, err := s.store.EnsureOwnerAssistant(ctx, now)
	if err != nil {
		return PetBrainResult{}, err
	}
	if !assistant.PetEnabled {
		return PetBrainResult{}, fmt.Errorf("宠物已停用")
	}
	if petQuietNow(assistant, time.Now()) {
		return PetBrainResult{}, fmt.Errorf("现在处于宠物安静时段")
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
	// Every gate above can reject a poll without doing any work. Only a run that
	// actually reaches SubmitMessage may consume the rolling-hour budget, and the
	// check-and-record below must stay atomic so the HTTP entry and the server
	// scheduler cannot both claim the last slot for a device.
	if err := s.reservePetBrainRun(deviceID, now, assistant.PetHourlyLimit); err != nil {
		return PetBrainResult{}, err
	}
	message := petAutonomousMessage(s.petUsesEnglish(ctx), idleMinutes, page, input.Layout)
	if _, err := s.SubmitMessage(ctx, petTaskID, message); err != nil {
		// The turn was never submitted, so give the reserved slot back.
		s.releasePetBrainRun(deviceID, now)
		return PetBrainResult{}, err
	}
	return PetBrainResult{}, nil
}

// reservePetBrainRun prunes the rolling-hour window, enforces the hourly limit
// and records the new run in one critical section. Callers must run it only
// after all other gates passed, immediately before submitting the autonomous
// turn, so rejected polls neither consume the budget nor inflate the count that
// used to starve every later poll.
func (s *Service) reservePetBrainRun(deviceID string, now time.Time, limit int) error {
	s.petBrainMu.Lock()
	defer s.petBrainMu.Unlock()
	if s.petBrainRuns == nil {
		s.petBrainRuns = map[string][]time.Time{}
	}
	cutoff := now.Add(-time.Hour)
	runs := s.petBrainRuns[deviceID][:0]
	for _, at := range s.petBrainRuns[deviceID] {
		if at.After(cutoff) {
			runs = append(runs, at)
		}
	}
	if limit > 0 && len(runs) >= limit {
		s.petBrainRuns[deviceID] = runs
		return fmt.Errorf("宠物这个小时已经够活跃了")
	}
	s.petBrainRuns[deviceID] = append(runs, now)
	return nil
}

// releasePetBrainRun removes a reservation whose submission failed, so a real
// error does not silently eat the device's hourly budget.
func (s *Service) releasePetBrainRun(deviceID string, at time.Time) {
	s.petBrainMu.Lock()
	defer s.petBrainMu.Unlock()
	runs := s.petBrainRuns[deviceID]
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Equal(at) {
			runs = append(runs[:i], runs[i+1:]...)
			break
		}
	}
	s.petBrainRuns[deviceID] = runs
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

func petAutonomousMessage(english bool, idleMinutes int, page string, layout PetLayout) string {
	runtimeState := "Web 前台（你可以使用 pet MCP 控制形象和主人互动）"
	if strings.Contains(page, "后台") {
		runtimeState = "后台运行（不要调用 pet MCP 控制形象；专注推进自主议程）"
	}
	if !english {
		return fmt.Sprintf("[自主行为] 距上次自主复盘约 %d 分钟。运行位置：%s。请按宠物身份和自主议程决定要推进的事项；不要只汇报。想做什么就调用宠物工具，不要写代码。当前页面：%s。\n\n页面布局：\n%s%s", idleMinutes, runtimeState, page, petLayoutPrompt(layout), petWakeActionContract(false))
	}
	if page == "未知页面" {
		page = "unknown page"
	}
	if strings.Contains(page, "后台") {
		page = "background (server scheduler)"
	}
	runtimeState = "web foreground (you may use pet MCP to control the avatar and interact with the owner)"
	if strings.Contains(page, "background") {
		runtimeState = "background run (do not use pet MCP to control the avatar; focus on advancing the agenda)"
	}
	return fmt.Sprintf("[Autonomous behavior] About %d minutes since the last autonomous review. Runtime: %s. Use your identity and agenda to decide what to advance; do not only report. Use pet tools for anything you want to do, and do not write code. Current page: %s.\n\nPage layout:\n%s%s", idleMinutes, runtimeState, page, petLayoutPromptEnglish(layout), petWakeActionContract(true))
}

// petWakeActionContract turns both scheduled and event-driven wake-ups into a
// work decision instead of another status summary. NOOP is the explicit quiet
// outcome, so an idle pet does not repeat the same open-loop list every few
// minutes.
func petWakeActionContract(english bool) string {
	if english {
		return "\n\nThis is a work wake-up, not a status report. Call aha_pet_work_state, read inspection, open_tasks, core/agenda.md and pending approvals. Every wake must leave one verifiable result:\n0) close the oldest pet-created open Task before new work: verify and complete/cancel it, or file one kind=owner_decision approval (payload task_id) for a real blocker; waiting/blocked needs a decision, failed gets one retry then escalation; never leave it silent;\n1) handle an event or pending item;\n2) if the owner must decide, create a kind=owner_decision approval with pet_request_approval and write a waiting item to core/agenda.md or core/loops.md; never only state it in the final answer, and create each decision once;\n3) if nothing can be advanced, pick one low-risk, verifiable AHA experience improvement from recent worklog/loops and create+start a normal Task, or add it to the agenda and start it; do not escape with NOOP;\n4) reply NOOP only after completing an action this turn and when nothing else remains;\n5) final replies: add one compact inspection line from inspection (scope, result, changes, unclosed).\nDo not repeat the open-loop list or only summarize. Approvals/ideas are created once in the approval center; agenda/loops cite them, worklog keeps execution evidence, diary stays personal. Do not copy the same body across layers."
	}
	return "\n\n这是工作唤醒，不是状态汇报。先调用 aha_pet_work_state，读取 inspection、open_tasks、core/agenda.md 和待审批项。每次唤醒必须留下一个可验证结果：\n0) 先关闭最早的未闭环宠物任务：验证后完成/取消，或为真实阻塞提交一次 kind=owner_decision 审批（payload 带 task_id）；waiting/blocked 需要决定，failed 先重试一次再升级；不能静默停留；\n1) 有事件或待办：处理它；\n2) 需要主人决定：用 pet_request_approval 创建 kind=owner_decision 的待审批，并在 core/agenda.md 或 core/loops.md 写 waiting 项；不能只写在最终回复里，同一决策只创建一次；\n3) 没有可推进项：从最近 worklog/loops 选一个低风险、可验证的 AHA 使用体验改进，创建并启动普通 Task，或先写入议程再启动；不要只回 NOOP；\n4) 只有本轮已经完成一个动作且确实没有剩余事项时，才回复 NOOP；\n5) 最终回复附一行巡检摘要（范围、结果、变化、未闭环），数据来自 inspection。\n不要重复列出未闭环清单，不要只汇报。审批/想法只在审批中心创建一次；agenda/loops 只写引用和状态，worklog 只写执行证据，diary 只写个人叙事，不要跨层复制正文。"
}

func (s *Service) petUsesEnglish(ctx context.Context) bool {
	settings, err := s.store.GeneralSettings(ctx)
	return err == nil && settings.Language == domain.LanguageEnglish
}

func petLayoutPromptEnglish(layout PetLayout) string {
	if layout.ViewportWidth <= 0 || layout.ViewportHeight <= 0 {
		return "Page layout unavailable; only zone movement is allowed."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Viewport %dx%d, scroll %.0f; pet rect x=%.0f y=%.0f w=%.0f h=%.0f.\n",
		layout.ViewportWidth, layout.ViewportHeight, layout.ScrollY,
		layout.Pet.X, layout.Pet.Y, layout.Pet.W, layout.Pet.H)
	if layout.Panel != nil {
		fmt.Fprintf(&b, "Chat panel x=%.0f y=%.0f w=%.0f h=%.0f.\n",
			layout.Panel.X, layout.Panel.Y, layout.Panel.W, layout.Panel.H)
	}
	for _, element := range layout.Elements {
		fmt.Fprintf(&b, "- %s: x=%.0f y=%.0f w=%.0f h=%.0f\n",
			element.Name, element.X, element.Y, element.W, element.H)
	}
	b.WriteString("When moving, use pet_move_to x/y viewport fractions; avoid covering inputs, buttons, and chat panel text.")
	return b.String()
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

	AllowViewTasks        *bool `json:"allow_view_tasks"`
	AllowCreateTasks      *bool `json:"allow_create_tasks"`
	AllowCreateWorkspaces *bool `json:"allow_create_workspaces"`
	AllowMessageTasks     *bool `json:"allow_message_tasks"`
	AllowManageModels     *bool `json:"allow_manage_models"`
	AllowManageKnowledge  *bool `json:"allow_manage_knowledge"`
	AllowManageChannels   *bool `json:"allow_manage_channels"`
	AllowManageSystem     *bool `json:"allow_manage_system"`
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
	if input.AllowViewTasks != nil {
		assistant.PetAllowViewTasks = *input.AllowViewTasks
	}
	if input.AllowCreateTasks != nil {
		assistant.PetAllowCreateTasks = *input.AllowCreateTasks
	}
	if input.AllowCreateWorkspaces != nil {
		assistant.PetAllowCreateWorkspaces = *input.AllowCreateWorkspaces
	}
	if input.AllowMessageTasks != nil {
		assistant.PetAllowMessageTasks = *input.AllowMessageTasks
	}
	if input.AllowManageModels != nil {
		assistant.PetAllowManageModels = *input.AllowManageModels
	}
	if input.AllowManageKnowledge != nil {
		assistant.PetAllowManageKnowledge = *input.AllowManageKnowledge
	}
	if input.AllowManageChannels != nil {
		assistant.PetAllowManageChannels = *input.AllowManageChannels
	}
	if input.AllowManageSystem != nil {
		assistant.PetAllowManageSystem = *input.AllowManageSystem
	}
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
	if err := s.store.UpdateOwnerPetPermissions(ctx, assistant.ID,
		assistant.PetAllowViewTasks, assistant.PetAllowCreateTasks,
		assistant.PetAllowCreateWorkspaces, assistant.PetAllowMessageTasks,
		assistant.PetAllowManageModels, assistant.PetAllowManageKnowledge, assistant.PetAllowManageChannels, assistant.PetAllowManageSystem, assistant.UpdatedAt); err != nil {
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

func (s *Service) PetMCPTools(ctx context.Context) []gateway.ChatTool {
	// Tools are always advertised so a permission change takes effect without a
	// backend-session reset; PetMCPTool enforces the switch on every call.
	return allPetMCPTools()
}

func allPetMCPTools() []gateway.ChatTool {
	tools := append([]gateway.ChatTool(nil), assistantPetTools()...)
	return append(tools, ahaPetTools()...)
}

func petToolAllowed(assistant domain.OwnerAssistant, name string) bool {
	switch name {
	case "aha_overview", "aha_pet_work_state", "aha_pet_permissions", "aha_request_permission_change", "aha_capabilities", "pet_request_approval", "pet_list_approvals", "pet_submit_idea":
		return true
	case "pet_say", "pet_play", "pet_move_to", "pet_walk_path", "pet_mood", "pet_open_task", "pet_thinking", "pet_sleep", "pet_wake",
		"pet_update_core", "pet_write_diary", "pet_write_worklog":
		return true
	case "aha_task_detail", "aha_task_conversation", "aha_task_turns", "aha_model_catalog", "aha_list_task_attachments", "aha_hardware_resources", "aha_task_hardware", "aha_task_channel_route", "aha_list_remote_tasks", "pet_watch_task", "pet_unwatch_task":
		return assistant.PetAllowViewTasks
	case "aha_list_knowledge", "aha_knowledge_detail", "aha_skill_detail", "aha_list_prompt_templates":
		return assistant.PetAllowViewTasks || assistant.PetAllowManageKnowledge
	case "aha_list_skills":
		// create_tasks may attach Skill IDs to aha_create_task/aha_update_task, so
		// it must be able to discover them. Mutating Skill tools stay behind
		// manage_knowledge.
		return assistant.PetAllowViewTasks || assistant.PetAllowManageKnowledge || assistant.PetAllowCreateTasks
	case "aha_submit_knowledge", "aha_create_skill", "aha_update_skill", "aha_delete_skill", "aha_delete_knowledge", "aha_prompt_template_detail", "aha_update_prompt_template":
		return assistant.PetAllowManageKnowledge
	case "aha_list_channels", "aha_channel_detail", "aha_list_channel_destinations", "aha_channel_contacts":
		return assistant.PetAllowViewTasks || assistant.PetAllowManageChannels
	case "aha_send_channel_message", "aha_set_channel_enabled", "aha_reset_channel_binding", "aha_archive_channel", "aha_purge_channel":
		return assistant.PetAllowManageChannels
	case "aha_system_settings":
		return assistant.PetAllowViewTasks || assistant.PetAllowManageSystem
	case "aha_update_security_settings", "aha_delete_hardware_resource",
		"aha_create_hardware_resource", "aha_update_hardware_resource",
		"aha_mail_resources", "aha_create_mail_account", "aha_update_mail_account",
		"aha_create_mail_template", "aha_update_mail_template",
		"aha_create_mail_binding", "aha_update_mail_binding",
		"aha_delete_mail_resource", "aha_task_mail", "aha_update_task_mail":
		return assistant.PetAllowManageSystem
	case "aha_list_projects":
		return assistant.PetAllowViewTasks || assistant.PetAllowCreateTasks || assistant.PetAllowCreateWorkspaces
	case "aha_create_task", "aha_start_task", "aha_takeover_remote_task", "aha_complete_task", "aha_finalize_task_worktree", "aha_request_code_review", "aha_delete_task", "aha_reopen_task", "aha_interrupt_task", "aha_update_task", "aha_update_task_agent", "aha_upload_task_attachment", "aha_delete_task_attachment", "aha_update_task_hardware", "aha_bind_task_channel", "aha_unbind_task_channel", "aha_retire_remote_task_mirror", "aha_compact_task_session", "aha_reset_task_session":
		return assistant.PetAllowCreateTasks
	case "aha_create_project", "aha_create_workspace", "aha_takeover_remote_workspace", "aha_update_project", "aha_delete_project", "aha_update_workspace", "aha_delete_workspace", "aha_detect_workspace", "aha_retire_remote_workspace_mirror":
		return assistant.PetAllowCreateWorkspaces
	case "aha_send_task_message":
		return assistant.PetAllowMessageTasks
	case "aha_create_provider", "aha_update_provider", "aha_delete_provider", "aha_detect_models",
		"aha_create_model", "aha_update_model", "aha_delete_model",
		"aha_create_env_group", "aha_update_env_group", "aha_delete_env_group":
		return assistant.PetAllowManageModels
	default:
		return false
	}
}

func isAhaPetTool(name string) bool {
	for _, tool := range ahaPetTools() {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func ahaPetTools() []gateway.ChatTool {
	return []gateway.ChatTool{
		{Name: "aha_overview", Description: "只读查看 AHA2 项目、任务、渠道的汇总和需要关注的任务", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_pet_work_state", Description: "开始工作前查看：巡检快照、所有未关闭的宠物任务、审批和最近 Task 进展", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_pet_permissions", Description: "只读查看当前宠物权限开关", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_capabilities", Description: "按当前权限返回可用工具目录；需要知道有哪些命令时调用，不要凭记忆猜", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_request_permission_change", Description: "提交权限变更审批；主人批准后才生效，宠物不能自行改权限", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"permission": map[string]any{"type": "string", "enum": []string{
					"view_tasks", "create_tasks", "create_workspaces", "message_tasks",
					"manage_models", "manage_knowledge", "manage_channels", "manage_system",
				}},
				"enabled": map[string]any{"type": "boolean"},
				"reason":  map[string]any{"type": "string", "description": "为什么需要这项权限；必填"},
			}, "required": []string{"permission", "enabled", "reason"},
		}},
		{Name: "aha_task_detail", Description: "只读查看一个 Task 的状态、目标和最近一次 Turn 的错误/结果", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
			}, "required": []string{"task_id"},
		}},
		{Name: "aha_list_projects", Description: "只读列出项目和 Workspace，用于选择创建 Task 的位置", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_list_remote_tasks", Description: "只读列出其他设备的 Task 镜像，可接管到本机", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"project_id": map[string]any{"type": "string"},
			},
		}},
		{Name: "aha_list_knowledge", Description: "只读列出知识条目和待审知识候选；scope 为 project 时需要 project_id", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"scope":      map[string]any{"type": "string", "enum": []string{"project", "global"}},
				"project_id": map[string]any{"type": "string"},
				"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			},
		}},
		{Name: "aha_knowledge_detail", Description: "只读查看知识条目正文", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"knowledge_id": map[string]any{"type": "string"}}, "required": []string{"knowledge_id"},
		}},
		{Name: "aha_submit_knowledge", Description: "提交知识候选或修订；scope 可为 project 或 global，是否直接发布取决于知识审核设置，自动审核开启时会直接发布", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"scope":      map[string]any{"type": "string", "enum": []string{"project", "global"}},
				"project_id": map[string]any{"type": "string", "description": "scope=project 时必填"},
				"candidates": map[string]any{"type": "array", "items": map[string]any{
					"type": "object", "properties": map[string]any{
						"entry_id":        map[string]any{"type": "string", "description": "可选；修订已有条目时提供"},
						"base_revision":   map[string]any{"type": "integer"},
						"parent_id":       map[string]any{"type": "string"},
						"slug":            map[string]any{"type": "string"},
						"sort_order":      map[string]any{"type": "integer"},
						"is_index":        map[string]any{"type": "boolean"},
						"type":            map[string]any{"type": "string", "enum": []string{"practice", "diagnostic"}},
						"title":           map[string]any{"type": "string"},
						"body":            map[string]any{"type": "string"},
						"confidence":      map[string]any{"type": "number", "minimum": 0, "maximum": 1},
						"product_line_id": map[string]any{"type": "string"},
						"reason":          map[string]any{"type": "string", "description": "为什么提交或修订这条知识"},
					}, "required": []string{"title", "body"},
				}},
			}, "required": []string{"candidates"},
		}},
		{Name: "aha_list_skills", Description: "只读列出 Skill；不返回包内文件内容", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"scope":      map[string]any{"type": "string", "enum": []string{"project", "global"}},
				"project_id": map[string]any{"type": "string"},
				"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			},
		}},
		{Name: "aha_skill_detail", Description: "只读查看 Skill 说明和文件名", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"skill_id": map[string]any{"type": "string"}}, "required": []string{"skill_id"},
		}},
		{Name: "aha_create_skill", Description: "创建项目级 Skill", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"project_id":   map[string]any{"type": "string"},
				"name":         map[string]any{"type": "string"},
				"description":  map[string]any{"type": "string"},
				"instructions": map[string]any{"type": "string"},
				"status":       map[string]any{"type": "string", "enum": []string{"active", "draft", "disabled"}},
				"enabled":      map[string]any{"type": "boolean"},
			}, "required": []string{"project_id", "name", "instructions"},
		}},
		{Name: "aha_update_skill", Description: "修改项目级 Skill；未提供的字段保持不变", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"skill_id":     map[string]any{"type": "string"},
				"name":         map[string]any{"type": "string"},
				"description":  map[string]any{"type": "string"},
				"instructions": map[string]any{"type": "string"},
				"status":       map[string]any{"type": "string", "enum": []string{"active", "draft", "disabled"}},
				"enabled":      map[string]any{"type": "boolean"},
			}, "required": []string{"skill_id"},
		}},
		{Name: "aha_delete_skill", Description: "提交删除 Skill 审批；主人批准后才删除", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"skill_id": map[string]any{"type": "string"},
				"reason":   map[string]any{"type": "string", "description": "为什么需要删除；必填"},
			}, "required": []string{"skill_id", "reason"},
		}},
		{Name: "aha_delete_knowledge", Description: "提交删除知识条目审批（支持 project/global）；主人批准后才删除，空 AHA1 目录会自动清理", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"knowledge_id": map[string]any{"type": "string"},
				"reason":       map[string]any{"type": "string", "description": "为什么需要删除；必填"},
			}, "required": []string{"knowledge_id", "reason"},
		}},
		{Name: "aha_list_prompt_templates", Description: "只读列出 Prompt 模板元数据", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_prompt_template_detail", Description: "查看 Prompt 模板内容；受保护的身份/安全/协议模板只读", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"template_id": map[string]any{"type": "string"}}, "required": []string{"template_id"},
		}},
		{Name: "aha_update_prompt_template", Description: "提交 Prompt 模板修改审批；主人批准后才写入，身份/安全/协议模板不允许修改", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"template_id": map[string]any{"type": "string"},
				"content":     map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string", "description": "为什么需要修改；必填"},
			}, "required": []string{"template_id", "content", "reason"},
		}},
		{Name: "aha_list_channels", Description: "只读列出渠道实例和健康状态（不返回凭据）", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_channel_detail", Description: "只读查看渠道实例和端点（不返回凭据）", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"instance_id": map[string]any{"type": "string"}}, "required": []string{"instance_id"},
		}},
		{Name: "aha_list_channel_destinations", Description: "只读列出可用渠道目的地和当前 Task 路由", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_channel_contacts", Description: "只读查看渠道目的地的联系人", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"conversation_id": map[string]any{"type": "string"}}, "required": []string{"conversation_id"},
		}},
		{Name: "aha_send_channel_message", Description: "提交对外渠道发送审批；主人批准后才发送到已绑定的群聊渠道", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":                   map[string]any{"type": "string"},
				"message":                   map[string]any{"type": "string"},
				"reason":                    map[string]any{"type": "string", "description": "为什么需要对外发送；至少 8 个字符"},
				"mention_identity_link_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"attachment_ids":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "required": []string{"task_id", "message", "reason"},
		}},
		{Name: "aha_set_channel_enabled", Description: "提交启用/停用渠道审批；主人批准后才执行", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"instance_id": map[string]any{"type": "string"},
				"enabled":     map[string]any{"type": "boolean"},
			}, "required": []string{"instance_id", "enabled"},
		}},
		{Name: "aha_reset_channel_binding", Description: "提交重置渠道绑定审批；批准后清理绑定和凭据", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"instance_id": map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string", "description": "为什么执行该渠道操作；至少 8 个字符"},
			}, "required": []string{"instance_id", "reason"},
		}},
		{Name: "aha_archive_channel", Description: "提交归档渠道审批；批准后停止运行时并归档", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"instance_id": map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string", "description": "为什么执行该渠道操作；至少 8 个字符"},
			}, "required": []string{"instance_id", "reason"},
		}},
		{Name: "aha_purge_channel", Description: "提交永久清理渠道审批；只能清理已归档渠道，批准后不可恢复", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"instance_id": map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string", "description": "为什么执行该渠道操作；至少 8 个字符"},
			}, "required": []string{"instance_id", "reason"},
		}},
		{Name: "aha_system_settings", Description: "只读查看系统安全设置和代理设置（代理凭据会脱敏）", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_update_security_settings", Description: "提交安全设置修改审批；主人批准后立即应用 access_scope 和 Origin 校验", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"validate_origin": map[string]any{"type": "boolean"},
				"access_scope":    map[string]any{"type": "string", "enum": []string{"local", "lan"}},
				"reason":          map[string]any{"type": "string", "description": "为什么修改安全设置；至少 8 个字符"},
			}, "required": []string{"validate_origin", "reason"},
		}},
		{Name: "aha_model_catalog", Description: "只读列出模型、Provider 和 Env Group（凭据值不会返回）", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_create_provider", Description: "创建 LLM Provider；api_key 可选且不会在结果中返回", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"name":               map[string]any{"type": "string"},
				"base_url":           map[string]any{"type": "string"},
				"anthropic_base_url": map[string]any{"type": "string"},
				"auth_style":         map[string]any{"type": "string"},
				"api_key":            map[string]any{"type": "string"},
			}, "required": []string{"name", "base_url"},
		}},
		{Name: "aha_update_provider", Description: "修改 Provider；api_key 留空则保留原凭据", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"provider_id":        map[string]any{"type": "string"},
				"name":               map[string]any{"type": "string"},
				"base_url":           map[string]any{"type": "string"},
				"anthropic_base_url": map[string]any{"type": "string"},
				"auth_style":         map[string]any{"type": "string"},
				"api_key":            map[string]any{"type": "string"},
				"reason":             map[string]any{"type": "string", "description": "修改 api_key 等凭据时必填；至少 8 个字符"},
			}, "required": []string{"provider_id", "name", "base_url"},
		}},
		{Name: "aha_delete_provider", Description: "提交删除 Provider 审批；主人批准后才删除，且不能有模型引用", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"provider_id": map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string", "description": "为什么删除；至少 8 个字符"},
			}, "required": []string{"provider_id", "reason"},
		}},
		{Name: "aha_detect_models", Description: "调用 Provider 的 /models 检测可用模型（不会返回凭据）", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"provider_id": map[string]any{"type": "string"}}, "required": []string{"provider_id"},
		}},
		{Name: "aha_create_model", Description: "创建 Provider 模型", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"display_name":             map[string]any{"type": "string"},
				"provider_id":              map[string]any{"type": "string"},
				"backend":                  map[string]any{"type": "string"},
				"wire_model":               map[string]any{"type": "string"},
				"wire_api":                 map[string]any{"type": "string"},
				"context_window":           map[string]any{"type": "integer"},
				"max_output_tokens":        map[string]any{"type": "integer"},
				"auto_compact_window":      map[string]any{"type": "integer"},
				"default_reasoning_effort": map[string]any{"type": "string"},
				"default_env_group_id":     map[string]any{"type": "string"},
			}, "required": []string{"display_name", "provider_id", "backend", "wire_model"},
		}},
		{Name: "aha_update_model", Description: "修改模型字段；未提供的字段保持不变", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"model_id":                 map[string]any{"type": "string"},
				"display_name":             map[string]any{"type": "string"},
				"provider_id":              map[string]any{"type": "string"},
				"backend":                  map[string]any{"type": "string"},
				"wire_model":               map[string]any{"type": "string"},
				"wire_api":                 map[string]any{"type": "string"},
				"context_window":           map[string]any{"type": "integer"},
				"max_output_tokens":        map[string]any{"type": "integer"},
				"auto_compact_window":      map[string]any{"type": "integer"},
				"default_reasoning_effort": map[string]any{"type": "string"},
				"default_env_group_id":     map[string]any{"type": "string"},
			}, "required": []string{"model_id"},
		}},
		{Name: "aha_delete_model", Description: "提交删除模型审批；主人批准后才删除", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"model_id": map[string]any{"type": "string"},
				"reason":   map[string]any{"type": "string", "description": "为什么删除；至少 8 个字符"},
			}, "required": []string{"model_id", "reason"},
		}},
		{Name: "aha_create_env_group", Description: "创建 Env Group；environment 值不会在结果中返回", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"name":        map[string]any{"type": "string"},
				"provider_id": map[string]any{"type": "string"},
				"backend":     map[string]any{"type": "string"},
				"environment": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			}, "required": []string{"name", "provider_id", "backend"},
		}},
		{Name: "aha_update_env_group", Description: "修改 Env Group；environment 会整体替换", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"env_group_id": map[string]any{"type": "string"},
				"name":         map[string]any{"type": "string"},
				"provider_id":  map[string]any{"type": "string"},
				"backend":      map[string]any{"type": "string"},
				"environment":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"reason":       map[string]any{"type": "string", "description": "修改 environment 等环境变量时必填；至少 8 个字符"},
			}, "required": []string{"env_group_id"},
		}},
		{Name: "aha_delete_env_group", Description: "提交删除 Env Group 审批；主人批准后才删除", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"env_group_id": map[string]any{"type": "string"},
				"reason":       map[string]any{"type": "string", "description": "为什么删除；至少 8 个字符"},
			}, "required": []string{"env_group_id", "reason"},
		}},
		{Name: "aha_create_task", Description: "在指定项目/Workspace 创建普通 AHA Task，可选择立即启动、用 skill_ids 选择 Skill；git 项目默认独立 worktree，high 推理和完全访问", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"project_id":       map[string]any{"type": "string"},
				"workspace_id":     map[string]any{"type": "string"},
				"title":            map[string]any{"type": "string"},
				"request":          map[string]any{"type": "string"},
				"model_id":         map[string]any{"type": "string", "description": "可选；默认使用宠物当前模型；可按难度选择更强模型"},
				"reasoning_effort": map[string]any{"type": "string", "enum": []string{"low", "medium", "high", "xhigh", "max"}, "description": "可选；默认 high"},
				"filesystem":       map[string]any{"type": "string", "enum": []string{"read-only", "workspace-write", "danger-full-access"}, "description": "可选；默认完全访问"},
				"approval":         map[string]any{"type": "string", "enum": []string{"never", "auto"}, "description": "可选；默认 never"},
				"isolation":        map[string]any{"type": "string", "enum": []string{"worktree", "inplace"}, "description": "可选；git 项目默认 worktree；只读或非代码任务可显式用 inplace；非 git 项目强制 inplace"},
				"stream_input":     map[string]any{"type": "boolean", "description": "Claude（stream-json）或 Codex（app-server turn/steer）：实验性 Turn 内 live inbox 投递；默认关闭"},
				"start":            map[string]any{"type": "boolean", "description": "默认 true"},
				"skill_ids":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "可选；该项目或全局可用的已启用 Skill ID（可用 aha_list_skills 查询）；项目自动挂载的 Skill 仍会合并；任一 ID 不可用则创建失败"},
			}, "required": []string{"project_id", "workspace_id", "title", "request"},
		}},
		{Name: "aha_start_task", Description: "启动一个草稿 Task", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
			}, "required": []string{"task_id"},
		}},
		{Name: "aha_takeover_remote_task", Description: "把其他设备的只读 Task 接管为本机 Task；必须选择同项目的本机 Workspace，并逐项确认硬件组", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":            map[string]any{"type": "string"},
				"workspace_id":       map[string]any{"type": "string"},
				"title":              map[string]any{"type": "string"},
				"request":            map[string]any{"type": "string"},
				"backend":            map[string]any{"type": "string"},
				"model_source":       map[string]any{"type": "string"},
				"model_id":           map[string]any{"type": "string"},
				"wire_model":         map[string]any{"type": "string"},
				"codex_account_id":   map[string]any{"type": "string"},
				"reasoning_effort":   map[string]any{"type": "string"},
				"filesystem":         map[string]any{"type": "string", "enum": []string{"read-only", "workspace-write", "danger-full-access"}},
				"approval":           map[string]any{"type": "string", "enum": []string{"never", "auto"}},
				"proxy_enabled":      map[string]any{"type": "boolean"},
				"fast_mode":          map[string]any{"type": "boolean"},
				"stream_input":       map[string]any{"type": "boolean", "description": "Claude（stream-json）或 Codex（app-server turn/steer）：实验性 Turn 内 live inbox 投递；默认关闭"},
				"collaboration_mode": map[string]any{"type": "string", "enum": []string{"single", "auto"}},
				"max_agents":         map[string]any{"type": "integer", "minimum": 1, "maximum": 3},
				"groups": map[string]any{"type": "array", "items": map[string]any{
					"type": "object", "properties": map[string]any{
						"id":          map[string]any{"type": "string"},
						"description": map[string]any{"type": "string"},
						"mode":        map[string]any{"type": "string", "enum": []string{"off", "serial", "network", "both"}},
						"serial": map[string]any{"type": "object", "properties": map[string]any{
							"device":   map[string]any{"type": "string"},
							"baudrate": map[string]any{"type": "integer"},
						}},
						"network": map[string]any{"type": "object", "properties": map[string]any{
							"host":     map[string]any{"type": "string"},
							"port":     map[string]any{"type": "integer"},
							"protocol": map[string]any{"type": "string", "enum": []string{"telnet", "raw", "ssh", "adb"}},
							"ssh_auth": map[string]any{"type": "string", "enum": []string{"auto", "password", "key"}},
						}},
						"username":                map[string]any{"type": "string"},
						"password":                map[string]any{"type": "string"},
						"reuse_remote_credential": map[string]any{"type": "boolean"},
					}, "required": []string{"id"},
				}},
			}, "required": []string{"task_id", "workspace_id"},
		}},
		{Name: "aha_create_project", Description: "创建 AHA 项目", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"name":                map[string]any{"type": "string"},
				"description":         map[string]any{"type": "string"},
				"project_type":        map[string]any{"type": "string", "enum": []string{"folder", "git"}},
				"repository_identity": map[string]any{"type": "string"},
				"default_branch":      map[string]any{"type": "string"},
				"knowledge_policy":    map[string]any{"type": "string", "enum": []string{"enabled", "disabled"}},
			}, "required": []string{"name"},
		}},
		{Name: "aha_create_workspace", Description: "创建本地 Workspace；需要项目已存在，native 路径必须已存在", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"project_id":   map[string]any{"type": "string"},
				"name":         map[string]any{"type": "string"},
				"transport":    map[string]any{"type": "string", "enum": []string{"native", "wsl", "ssh"}},
				"root_path":    map[string]any{"type": "string"},
				"distro":       map[string]any{"type": "string"},
				"ssh_host":     map[string]any{"type": "string"},
				"ssh_user":     map[string]any{"type": "string"},
				"ssh_port":     map[string]any{"type": "integer"},
				"ssh_auth":     map[string]any{"type": "string", "enum": []string{"auto", "password", "key"}},
				"ssh_password": map[string]any{"type": "string"},
			}, "required": []string{"project_id", "name", "root_path"},
		}},
		{Name: "aha_takeover_remote_workspace", Description: "把其他设备的只读 Workspace 接管为本机 Workspace；需要明确本机 root_path 和 transport", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"workspace_id":            map[string]any{"type": "string"},
				"name":                    map[string]any{"type": "string"},
				"locality":                map[string]any{"type": "string"},
				"transport":               map[string]any{"type": "string", "enum": []string{"native", "wsl", "ssh"}},
				"root_path":               map[string]any{"type": "string"},
				"distro":                  map[string]any{"type": "string"},
				"ssh_host":                map[string]any{"type": "string"},
				"ssh_user":                map[string]any{"type": "string"},
				"ssh_port":                map[string]any{"type": "integer"},
				"ssh_auth":                map[string]any{"type": "string", "enum": []string{"auto", "password", "key"}},
				"ssh_password":            map[string]any{"type": "string"},
				"reuse_remote_credential": map[string]any{"type": "boolean"},
			}, "required": []string{"workspace_id", "transport", "root_path"},
		}},
		{Name: "aha_update_project", Description: "修改 AHA 项目名称、描述、类型、仓库标识或知识策略", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"project_id":          map[string]any{"type": "string"},
				"name":                map[string]any{"type": "string"},
				"description":         map[string]any{"type": "string"},
				"project_type":        map[string]any{"type": "string", "enum": []string{"folder", "git"}},
				"repository_identity": map[string]any{"type": "string"},
				"default_branch":      map[string]any{"type": "string"},
				"knowledge_policy":    map[string]any{"type": "string", "enum": []string{"enabled", "disabled"}},
			}, "required": []string{"project_id"},
		}},
		{Name: "aha_delete_project", Description: "提交删除项目审批；主人批准后才会删除项目及其本地任务和 Workspace", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"project_id": map[string]any{"type": "string"},
				"reason":     map[string]any{"type": "string", "description": "为什么删除；至少 8 个字符"},
			}, "required": []string{"project_id", "reason"},
		}},
		{Name: "aha_update_workspace", Description: "修改 Workspace 名称、路径、传输方式或发行版", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"workspace_id":       map[string]any{"type": "string"},
				"name":               map[string]any{"type": "string"},
				"root_path":          map[string]any{"type": "string"},
				"transport":          map[string]any{"type": "string", "enum": []string{"native", "wsl", "ssh"}},
				"distro":             map[string]any{"type": "string"},
				"ssh_host":           map[string]any{"type": "string"},
				"ssh_user":           map[string]any{"type": "string"},
				"ssh_port":           map[string]any{"type": "integer"},
				"ssh_auth":           map[string]any{"type": "string", "enum": []string{"auto", "password", "key"}},
				"ssh_password":       map[string]any{"type": "string"},
				"clear_ssh_password": map[string]any{"type": "boolean"},
			}, "required": []string{"workspace_id"},
		}},
		{Name: "aha_delete_workspace", Description: "提交删除 Workspace 审批；主人批准后才会删除 Workspace 及其本地任务", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"workspace_id": map[string]any{"type": "string"},
				"reason":       map[string]any{"type": "string", "description": "为什么删除；至少 8 个字符"},
			}, "required": []string{"workspace_id", "reason"},
		}},
		{Name: "aha_detect_workspace", Description: "重新检测 Workspace 环境和健康状态", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"workspace_id": map[string]any{"type": "string"}}, "required": []string{"workspace_id"},
		}},
		{Name: "aha_retire_remote_task_mirror", Description: "移除其他设备的只读 Task 镜像", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "aha_retire_remote_workspace_mirror", Description: "移除其他设备的只读 Workspace 镜像", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"workspace_id": map[string]any{"type": "string"}}, "required": []string{"workspace_id"},
		}},
		{Name: "aha_send_task_message", Description: "给另一个 AHA Task 发内部消息，对话框显示发送者为 pet；不会发送到外部渠道", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":        map[string]any{"type": "string"},
				"content":        map[string]any{"type": "string"},
				"attachment_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "required": []string{"task_id"},
		}},
		{Name: "aha_list_task_attachments", Description: "列出 Task 附件元数据，不返回文件内容", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "aha_upload_task_attachment", Description: "以 base64 上传 Task 附件（存储上限 200MiB；MCP 实际受模型输出上限约束）", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":        map[string]any{"type": "string"},
				"name":           map[string]any{"type": "string"},
				"media_type":     map[string]any{"type": "string"},
				"content_base64": map[string]any{"type": "string"},
			}, "required": []string{"task_id", "content_base64"},
		}},
		{Name: "aha_delete_task_attachment", Description: "删除尚未绑定到消息的 Task 附件", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":       map[string]any{"type": "string"},
				"attachment_id": map[string]any{"type": "string"},
			}, "required": []string{"task_id", "attachment_id"},
		}},
		{Name: "aha_hardware_resources", Description: "只读列出可绑定硬件资源（不返回凭据）", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_delete_hardware_resource", Description: "提交删除硬件资源审批；主人批准后才删除", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"resource_id": map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string", "description": "为什么删除；至少 8 个字符"},
			}, "required": []string{"resource_id", "reason"},
		}},
		{Name: "aha_create_hardware_resource", Description: "新建硬件资源（mode 只支持 serial 或 network）。凭据只能由 Owner 在 Web/Secret Store 配置；本工具不接受 password、credential_ref 或 clear_secret", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"id":          map[string]any{"type": "string", "description": "可选；不传则由服务端生成"},
				"description": map[string]any{"type": "string", "description": "可选；为空时使用资源 ID"},
				"mode": map[string]any{"type": "string", "enum": []string{
					domain.HardwareModeSerial, domain.HardwareModeNetwork,
				}, "description": "必填；只支持串口或网络二选一"},
				"access": map[string]any{"type": "string", "enum": []string{
					domain.HardwareAccessReadOnly, domain.HardwareAccessReadWrite,
				}, "description": "可选；默认 read_write"},
				"serial": map[string]any{
					"type": "object", "description": "mode=serial 时必需 device 非空且 baudrate>0",
					"properties": map[string]any{
						"device":   map[string]any{"type": "string"},
						"baudrate": map[string]any{"type": "integer", "minimum": 1},
					},
				},
				"network": map[string]any{
					"type": "object", "description": "mode=network 时 host 必填；port 不传时按协议使用默认端口",
					"properties": map[string]any{
						"host": map[string]any{"type": "string"},
						"port": map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
						"protocol": map[string]any{"type": "string", "enum": []string{
							domain.HardwareProtocolTelnet, domain.HardwareProtocolRaw,
							domain.HardwareProtocolSSH, domain.HardwareProtocolADB,
						}},
						"ssh_auth": map[string]any{"type": "string", "enum": []string{
							domain.HardwareSSHAuthAuto, domain.HardwareSSHAuthPassword, domain.HardwareSSHAuthKey,
						}},
					},
				},
				"username": map[string]any{"type": "string", "description": "SSH 用户名；ADB 资源会忽略"},
			}, "required": []string{"mode"},
		}},
		{Name: "aha_update_hardware_resource", Description: "修改硬件资源；只更新传入字段，未提供字段保持原值。凭据只能由 Owner 在 Web/Secret Store 配置；本工具不接受 password、credential_ref 或 clear_secret", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"resource_id": map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"mode": map[string]any{"type": "string", "enum": []string{
					domain.HardwareModeSerial, domain.HardwareModeNetwork,
				}},
				"access": map[string]any{"type": "string", "enum": []string{
					domain.HardwareAccessReadOnly, domain.HardwareAccessReadWrite,
				}},
				"serial": map[string]any{
					"type": "object", "description": "未提供字段保持原值；mode=serial 时 device 非空且 baudrate>0",
					"properties": map[string]any{
						"device":   map[string]any{"type": "string"},
						"baudrate": map[string]any{"type": "integer", "minimum": 1},
					},
				},
				"network": map[string]any{
					"type": "object", "description": "未提供字段保持原值；mode=network 时 host 必填",
					"properties": map[string]any{
						"host": map[string]any{"type": "string"},
						"port": map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
						"protocol": map[string]any{"type": "string", "enum": []string{
							domain.HardwareProtocolTelnet, domain.HardwareProtocolRaw,
							domain.HardwareProtocolSSH, domain.HardwareProtocolADB,
						}},
						"ssh_auth": map[string]any{"type": "string", "enum": []string{
							domain.HardwareSSHAuthAuto, domain.HardwareSSHAuthPassword, domain.HardwareSSHAuthKey,
						}},
					},
				},
				"username": map[string]any{"type": "string", "description": "SSH 用户名；ADB 资源会忽略"},
			}, "required": []string{"resource_id"},
		}},
		{Name: "aha_task_hardware", Description: "只读查看 Task 的硬件绑定和硬件组", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "aha_update_task_hardware", Description: "整体替换 Task 的硬件资源绑定", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":      map[string]any{"type": "string"},
				"resource_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "required": []string{"task_id"},
		}},
		{Name: "aha_mail_resources", Description: "只读列出邮箱账号、模板和绑定；账号只返回非敏感字段与 password_configured，绝不返回 password、授权码或 credential_ref。可选 task_id 同时返回该 Task 的有效绑定", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "可选；返回该 Task 的有效邮件绑定"},
			},
		}},
		{Name: "aha_create_mail_account", Description: "新建邮箱账号；凭据只能由 Owner 在 Web/Secret Store 配置。本工具不接受 password、auth_code、credential_ref、clear_secret；新账号始终为未配置凭据、unverified", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"id":            map[string]any{"type": "string", "description": "可选；不传则由服务端生成"},
				"name":          map[string]any{"type": "string"},
				"from_address":  map[string]any{"type": "string"},
				"display_name":  map[string]any{"type": "string"},
				"provider":      map[string]any{"type": "string", "enum": []string{domain.MailProviderNeteaseQiye}},
				"smtp_host":     map[string]any{"type": "string", "enum": []string{domain.MailDefaultSMTPHost, domain.MailBackupSMTPHost}},
				"smtp_port":     map[string]any{"type": "integer", "enum": []int{domain.MailDefaultSMTPPort}},
				"smtp_security": map[string]any{"type": "string", "enum": []string{domain.MailSMTPSecuritySSL}},
				"smtp_username": map[string]any{"type": "string"},
				"aliases":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "required": []string{"name", "from_address"},
		}},
		{Name: "aha_update_mail_account", Description: "修改邮箱账号非敏感字段；只更新传入字段。凭据只能由 Owner 配置。若账号已有凭据，禁止修改 SMTP 目标、TLS 或发件地址，否则返回 mail_credential_owner_only 且不写库", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"account_id":    map[string]any{"type": "string"},
				"name":          map[string]any{"type": "string"},
				"from_address":  map[string]any{"type": "string"},
				"display_name":  map[string]any{"type": "string"},
				"provider":      map[string]any{"type": "string", "enum": []string{domain.MailProviderNeteaseQiye}},
				"smtp_host":     map[string]any{"type": "string", "enum": []string{domain.MailDefaultSMTPHost, domain.MailBackupSMTPHost}},
				"smtp_port":     map[string]any{"type": "integer", "enum": []int{domain.MailDefaultSMTPPort}},
				"smtp_security": map[string]any{"type": "string", "enum": []string{domain.MailSMTPSecuritySSL}},
				"smtp_username": map[string]any{"type": "string"},
				"aliases":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "required": []string{"account_id"},
		}},
		{Name: "aha_create_mail_template", Description: "新建邮件模板（正文允许，不是凭据）；默认 guide 模式，模板内容仅作参考示例；模板版本不可变，创建即 version 1", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"id":                 map[string]any{"type": "string", "description": "可选；不传则由服务端生成"},
				"scope":              map[string]any{"type": "string", "enum": []string{string(domain.MailTemplateScopeGlobal), string(domain.MailTemplateScopeProject)}},
				"project_id":         map[string]any{"type": "string", "description": "scope=project 时必填"},
				"name":               map[string]any{"type": "string"},
				"description":        map[string]any{"type": "string", "description": "guide 模式下作为填写说明/检查清单"},
				"mode":               map[string]any{"type": "string", "enum": []string{string(domain.MailTemplateModeGuide), string(domain.MailTemplateModeRender)}, "description": "默认 guide；render 保留旧的变量替换语义"},
				"subject_template":   map[string]any{"type": "string"},
				"body_text_template": map[string]any{"type": "string"},
				"body_html_template": map[string]any{"type": "string", "description": "正文至少提供 text 或 html 之一"},
				"default_to":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"default_cc":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"required_variables": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "required": []string{"name", "subject_template"},
		}},
		{Name: "aha_update_mail_template", Description: "修改邮件模板；模板版本不可变，更新会生成新版本。scope/project_id 不可修改；expected_version 不传时使用当前版本", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"template_id":        map[string]any{"type": "string"},
				"expected_version":   map[string]any{"type": "integer", "minimum": 1},
				"name":               map[string]any{"type": "string"},
				"description":        map[string]any{"type": "string"},
				"mode":               map[string]any{"type": "string", "enum": []string{string(domain.MailTemplateModeGuide), string(domain.MailTemplateModeRender)}},
				"subject_template":   map[string]any{"type": "string"},
				"body_text_template": map[string]any{"type": "string"},
				"body_html_template": map[string]any{"type": "string"},
				"default_to":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"default_cc":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"required_variables": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "required": []string{"template_id"},
		}},
		{Name: "aha_create_mail_binding", Description: "创建 project/workspace/task 级邮件绑定；作用域和账号必须存在。一期只支持 approval_mode=always 且不允许自动发送", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"scope_type":          map[string]any{"type": "string", "enum": []string{string(domain.MailBindingScopeProject), string(domain.MailBindingScopeWorkspace), string(domain.MailBindingScopeTask)}},
				"scope_id":            map[string]any{"type": "string"},
				"account_id":          map[string]any{"type": "string"},
				"default_template_id": map[string]any{"type": "string"},
				"approval_mode":       map[string]any{"type": "string", "enum": []string{string(domain.MailApprovalAlways)}},
				"allowed_recipients":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"enabled":             map[string]any{"type": "boolean"},
			}, "required": []string{"scope_type", "scope_id", "account_id"},
		}},
		{Name: "aha_update_mail_binding", Description: "修改邮件绑定；绑定变更直接执行。删除绑定请使用 aha_delete_mail_resource 并等待审批", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"binding_id":          map[string]any{"type": "string"},
				"account_id":          map[string]any{"type": "string"},
				"default_template_id": map[string]any{"type": "string"},
				"approval_mode":       map[string]any{"type": "string", "enum": []string{string(domain.MailApprovalAlways)}},
				"allowed_recipients":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"enabled":             map[string]any{"type": "boolean"},
			}, "required": []string{"binding_id"},
		}},
		{Name: "aha_delete_mail_resource", Description: "提交删除邮件账号/模板/绑定的审批；主人批准后才执行。模板按现有规则归档，历史版本保留", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"kind":        map[string]any{"type": "string", "enum": []string{"account", "template", "binding"}},
				"resource_id": map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string", "description": "为什么删除；至少 8 个字符"},
			}, "required": []string{"kind", "resource_id", "reason"},
		}},
		{Name: "aha_task_mail", Description: "只读查看 Task 级邮件绑定及生效绑定（Task → Workspace → Project）", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "aha_update_task_mail", Description: "更新或创建 Task 级邮件绑定；不删除其他绑定。删除绑定请走审批工具", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":             map[string]any{"type": "string"},
				"account_id":          map[string]any{"type": "string", "description": "创建时必填；更新已有绑定时可省略"},
				"binding_id":          map[string]any{"type": "string", "description": "可选；指定时更新该 Task 级绑定"},
				"default_template_id": map[string]any{"type": "string"},
				"approval_mode":       map[string]any{"type": "string", "enum": []string{string(domain.MailApprovalAlways)}},
				"allowed_recipients":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"enabled":             map[string]any{"type": "boolean"},
			}, "required": []string{"task_id"},
		}},
		{Name: "aha_task_channel_route", Description: "只读查看 Task 当前渠道路由", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "aha_bind_task_channel", Description: "把 Task 绑定到指定渠道会话", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":         map[string]any{"type": "string"},
				"instance_id":     map[string]any{"type": "string"},
				"conversation_id": map[string]any{"type": "string"},
			}, "required": []string{"task_id", "instance_id", "conversation_id"},
		}},
		{Name: "aha_unbind_task_channel", Description: "解除 Task 的渠道路由", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":  map[string]any{"type": "string"},
				"route_id": map[string]any{"type": "string"},
			}, "required": []string{"task_id", "route_id"},
		}},
		{Name: "aha_complete_task", Description: "把 Task 标记为完成；写权限任务必须提供高级模型 Review Task", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":        map[string]any{"type": "string"},
				"review_task_id": map[string]any{"type": "string", "description": "写权限任务必填：已通过 Verdict: APPROVE 的只读高推理 Review Task ID"},
			}, "required": []string{"task_id"},
		}},
		{Name: "aha_finalize_task_worktree", Description: "仅用于已完成的 worktree Task：检查提交、合并回目标分支、删除临时 worktree 和分支；有未提交改动或冲突时保留并报错", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
			}, "required": []string{"task_id"},
		}},
		{Name: "aha_request_code_review", Description: "为源 Task 创建独立、只读、高推理的高级模型 Review Task；写权限任务完成前必须使用", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":         map[string]any{"type": "string"},
				"review_model_id": map[string]any{"type": "string", "description": "评审模型，必须与源 Task 模型不同；用 aha_model_catalog 选择更强的模型"},
				"focus":           map[string]any{"type": "string", "description": "可选：额外关注的审查重点"},
			}, "required": []string{"task_id", "review_model_id"},
		}},
		{Name: "aha_delete_task", Description: "提交删除 Task 审批；主人批准后才会删除", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
				"reason":  map[string]any{"type": "string", "description": "为什么删除；至少 8 个字符"},
			}, "required": []string{"task_id", "reason"},
		}},
		{Name: "aha_reopen_task", Description: "重新打开已完成/失败/阻塞/取消的 Task", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "aha_interrupt_task", Description: "中断 Task 当前执行中的 Turn 或 Round", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "aha_update_task", Description: "修改 Task 标题、Current Goal、协作模式、Max Agents 或所选 Skill", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":            map[string]any{"type": "string"},
				"title":              map[string]any{"type": "string"},
				"current_goal":       map[string]any{"type": "string"},
				"collaboration_mode": map[string]any{"type": "string", "enum": []string{"single", "auto"}},
				"max_agents":         map[string]any{"type": "integer", "minimum": 1, "maximum": 3},
				"skill_ids":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "可选；整体替换 Task 的 Skill 选择（该项目或全局可用的已启用 Skill ID），空数组表示清空；不传则保持不变；任一 ID 不可用则报错且不做任何修改"},
			}, "required": []string{"task_id"},
		}},
		{Name: "aha_update_task_agent", Description: "修改 Task main Agent 的模型、推理强度、文件权限、审批方式或 Claude stream-json 输入开关", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":          map[string]any{"type": "string"},
				"model_id":         map[string]any{"type": "string"},
				"reasoning_effort": map[string]any{"type": "string"},
				"filesystem":       map[string]any{"type": "string", "enum": []string{"read-only", "workspace-write", "danger-full-access"}},
				"approval":         map[string]any{"type": "string", "enum": []string{"never", "auto"}},
				"fast_mode":        map[string]any{"type": "boolean"},
				"stream_input":     map[string]any{"type": "boolean", "description": "Claude（stream-json）或 Codex（app-server turn/steer）：实验性 Turn 内 live inbox 投递；下一 Turn 生效；其他 Backend 忽略"},
			}, "required": []string{"task_id"},
		}},
		{Name: "aha_compact_task_session", Description: "压缩并重置 Task main Agent 的 Backend Session", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "aha_reset_task_session", Description: "重置 Task main Agent 的 Backend Session", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "aha_task_conversation", Description: "只读读取 Task 的会话记录", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}, "required": []string{"task_id"},
		}},
		{Name: "aha_task_turns", Description: "只读读取 Task 的 Turn 状态、结果和错误", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			}, "required": []string{"task_id"},
		}},
		{Name: "pet_watch_task", Description: "关注一个 Task 的状态变化；完成/失败/阻塞/等待时会主动通知", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "pet_unwatch_task", Description: "取消关注一个 Task 的状态变化", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "pet_submit_idea", Description: "提交一个想法审批；主人同意后再实现，不自动执行或授权", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"title":              map[string]any{"type": "string"},
				"summary":            map[string]any{"type": "string"},
				"reason":             map[string]any{"type": "string", "description": "为什么提出这个想法；必填"},
				"plan":               map[string]any{"type": "string"},
				"expected_outcome":   map[string]any{"type": "string"},
				"risk":               map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
				"permissions_needed": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "required": []string{"title", "summary", "reason"},
		}},
		{Name: "pet_request_approval", Description: "向主人申请一次需要授权的操作；只创建审批，不执行操作；任务相关审批在 payload 中带 task_id", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"kind":    map[string]any{"type": "string", "description": "操作类型，例如 external_channel_send、delete_task、owner_decision"},
				"title":   map[string]any{"type": "string"},
				"summary": map[string]any{"type": "string"},
				"reason":  map[string]any{"type": "string", "description": "为什么需要这次审批；高风险审批必填"},
				"risk":    map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
				"payload": map[string]any{"type": "object", "description": "可选的操作参数；任务相关审批带 task_id"},
			}, "required": []string{"kind", "title", "summary"},
		}},
		{Name: "pet_list_approvals", Description: "查看自己的审批记录", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			},
		}},
	}
}

// PetMCPTool dispatches the pet MCP surface. Pet action tools still go through
// the validated ControlOwnerPet path; AHA read tools and approval requests are
// handled here.
func (s *Service) PetMCPTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	if isAhaPetTool(name) {
		assistant, err := s.petAssistant(ctx)
		if err != nil {
			return "", err
		}
		if !petToolAllowed(assistant, name) {
			return "", fmt.Errorf("主人未开启该工具权限")
		}
	}
	switch name {
	case "aha_overview":
		overview, err := s.PetAHAOverview(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(overview)
		return string(data), nil
	case "aha_pet_work_state":
		state, err := s.PetWorkState(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(state)
		return string(data), nil
	case "aha_pet_permissions":
		permissions, err := s.PetPermissions(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(permissions)
		return string(data), nil
	case "aha_capabilities":
		capabilities, err := s.PetCapabilities(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(capabilities)
		return string(data), nil
	case "aha_request_permission_change":
		var input PetPermissionChangeInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid permission change request")
		}
		approval, err := s.PetRequestPermissionChange(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_task_detail":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid task_id")
		}
		detail, err := s.PetTaskDetail(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(detail)
		return string(data), nil
	case "aha_list_projects":
		projects, err := s.PetProjectCatalog(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(projects)
		return string(data), nil
	case "aha_list_remote_tasks":
		var input struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid list remote tasks request")
		}
		tasks, err := s.PetRemoteTaskMirrors(ctx, input.ProjectID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(tasks)
		return string(data), nil
	case "aha_list_knowledge":
		var input struct {
			Scope     string `json:"scope"`
			ProjectID string `json:"project_id"`
			Limit     int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid list knowledge request")
		}
		view, err := s.PetListKnowledge(ctx, input.Scope, input.ProjectID, input.Limit)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_knowledge_detail":
		var input struct {
			KnowledgeID string `json:"knowledge_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid knowledge detail request")
		}
		detail, err := s.PetKnowledgeDetail(ctx, input.KnowledgeID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(detail)
		return string(data), nil
	case "aha_submit_knowledge":
		var input struct {
			Scope      string               `json:"scope"`
			ProjectID  string               `json:"project_id"`
			Candidates []KnowledgeCandidate `json:"candidates"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid submit knowledge request")
		}
		result, err := s.PetSubmitKnowledge(ctx, input.Scope, input.ProjectID, input.Candidates)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(result)
		return string(data), nil
	case "aha_list_skills":
		var input struct {
			Scope     string `json:"scope"`
			ProjectID string `json:"project_id"`
			Limit     int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid list skills request")
		}
		items, err := s.PetListSkills(ctx, input.Scope, input.ProjectID, input.Limit)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(items)
		return string(data), nil
	case "aha_skill_detail":
		var input struct {
			SkillID string `json:"skill_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid skill detail request")
		}
		detail, err := s.PetSkillDetail(ctx, input.SkillID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(detail)
		return string(data), nil
	case "aha_create_skill":
		var input PetSkillInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid create skill request")
		}
		detail, err := s.PetCreateSkill(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(detail)
		return string(data), nil
	case "aha_update_skill":
		var input PetSkillInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid update skill request")
		}
		detail, err := s.PetUpdateSkill(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(detail)
		return string(data), nil
	case "aha_delete_skill":
		var input struct {
			SkillID string `json:"skill_id"`
			Reason  string `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid delete skill request")
		}
		approval, err := s.PetDeleteSkill(ctx, input.SkillID, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_delete_knowledge":
		var input struct {
			KnowledgeID string `json:"knowledge_id"`
			Reason      string `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid delete knowledge request")
		}
		approval, err := s.PetDeleteKnowledge(ctx, input.KnowledgeID, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_list_prompt_templates":
		items, err := s.PetListPromptTemplates(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(items)
		return string(data), nil
	case "aha_prompt_template_detail":
		var input struct {
			TemplateID string `json:"template_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid prompt template detail request")
		}
		detail, err := s.PetPromptTemplateDetail(ctx, input.TemplateID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(detail)
		return string(data), nil
	case "aha_update_prompt_template":
		var input struct {
			TemplateID string `json:"template_id"`
			Content    string `json:"content"`
			Reason     string `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid update prompt template request")
		}
		approval, err := s.PetRequestPromptUpdate(ctx, input.TemplateID, input.Content, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_list_channels":
		items, err := s.PetListChannels(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(items)
		return string(data), nil
	case "aha_channel_detail":
		var input struct {
			InstanceID string `json:"instance_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid channel detail request")
		}
		detail, err := s.PetChannelDetail(ctx, input.InstanceID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(detail)
		return string(data), nil
	case "aha_list_channel_destinations":
		items, err := s.PetListChannelDestinations(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(items)
		return string(data), nil
	case "aha_channel_contacts":
		var input struct {
			ConversationID string `json:"conversation_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid channel contacts request")
		}
		view, err := s.PetChannelContacts(ctx, input.ConversationID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_send_channel_message":
		var input PetChannelSendInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid channel send request")
		}
		approval, err := s.PetRequestChannelSend(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_set_channel_enabled":
		var input struct {
			InstanceID string `json:"instance_id"`
			Enabled    bool   `json:"enabled"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid set channel enabled request")
		}
		approval, err := s.PetRequestChannelEnabled(ctx, input.InstanceID, input.Enabled)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_reset_channel_binding", "aha_archive_channel", "aha_purge_channel":
		var input struct {
			InstanceID string `json:"instance_id"`
			Reason     string `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid channel lifecycle request")
		}
		action := strings.TrimPrefix(name, "aha_")
		action = strings.TrimSuffix(action, "_channel_binding")
		action = strings.TrimSuffix(action, "_channel")
		approval, err := s.PetRequestChannelLifecycle(ctx, input.InstanceID, action, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_system_settings":
		view, err := s.PetSystemSettings(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_update_security_settings":
		var input struct {
			ValidateOrigin bool   `json:"validate_origin"`
			AccessScope    string `json:"access_scope"`
			Reason         string `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid update security settings request")
		}
		approval, err := s.PetRequestSecuritySettingsUpdate(ctx, input.ValidateOrigin, input.AccessScope, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_model_catalog":
		catalog, err := s.PetModelCatalog(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(catalog)
		return string(data), nil
	case "aha_create_provider":
		var input PetProviderInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid create provider request")
		}
		provider, err := s.PetCreateProvider(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(provider)
		return string(data), nil
	case "aha_update_provider":
		var input PetProviderInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid update provider request")
		}
		provider, err := s.PetUpdateProvider(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(provider)
		return string(data), nil
	case "aha_delete_provider":
		var input struct {
			ProviderID string `json:"provider_id"`
			Reason     string `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid delete provider request")
		}
		approval, err := s.PetRequestDeleteProvider(ctx, input.ProviderID, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_detect_models":
		var input struct {
			ProviderID string `json:"provider_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid detect models request")
		}
		result, err := s.PetDetectModels(ctx, input.ProviderID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(result)
		return string(data), nil
	case "aha_create_model":
		var input PetModelInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid create model request")
		}
		model, err := s.PetCreateModel(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(model)
		return string(data), nil
	case "aha_update_model":
		var input PetModelInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid update model request")
		}
		model, err := s.PetUpdateModel(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(model)
		return string(data), nil
	case "aha_delete_model":
		var input struct {
			ModelID string `json:"model_id"`
			Reason  string `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid delete model request")
		}
		approval, err := s.PetRequestDeleteModel(ctx, input.ModelID, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_create_env_group":
		var input PetEnvGroupInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid create env group request")
		}
		group, err := s.PetCreateEnvGroup(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(group)
		return string(data), nil
	case "aha_update_env_group":
		var input PetEnvGroupInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid update env group request")
		}
		group, err := s.PetUpdateEnvGroup(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(group)
		return string(data), nil
	case "aha_delete_env_group":
		var input struct {
			EnvGroupID string `json:"env_group_id"`
			Reason     string `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid delete env group request")
		}
		approval, err := s.PetRequestDeleteEnvGroup(ctx, input.EnvGroupID, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_create_task":
		var input PetCreateTaskInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid create task request")
		}
		task, err := s.PetCreateTask(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(task)
		return string(data), nil
	case "aha_start_task":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid start task request")
		}
		task, err := s.PetStartTask(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(task)
		return string(data), nil
	case "aha_takeover_remote_task":
		var input PetTakeoverTaskInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid takeover remote task request")
		}
		task, groups, err := s.PetTakeoverRemoteTask(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(map[string]any{"task": task, "hardware": groups})
		return string(data), nil
	case "aha_create_project":
		var input PetCreateProjectInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid create project request")
		}
		project, err := s.PetCreateProject(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(project)
		return string(data), nil
	case "aha_create_workspace":
		var input PetCreateWorkspaceInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid create workspace request")
		}
		workspace, err := s.PetCreateWorkspace(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(workspace)
		return string(data), nil
	case "aha_takeover_remote_workspace":
		var input PetTakeoverWorkspaceInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid takeover remote workspace request")
		}
		workspace, err := s.PetTakeoverRemoteWorkspace(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(workspace)
		return string(data), nil
	case "aha_update_project":
		var input PetUpdateProjectInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid update project request")
		}
		project, err := s.PetUpdateProject(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(project)
		return string(data), nil
	case "aha_delete_project":
		var input struct {
			ProjectID string `json:"project_id"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid delete project request")
		}
		approval, err := s.PetDeleteProject(ctx, input.ProjectID, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_update_workspace":
		var input PetUpdateWorkspaceInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid update workspace request")
		}
		workspace, err := s.PetUpdateWorkspace(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(workspace)
		return string(data), nil
	case "aha_delete_workspace":
		var input struct {
			WorkspaceID string `json:"workspace_id"`
			Reason      string `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid delete workspace request")
		}
		approval, err := s.PetDeleteWorkspace(ctx, input.WorkspaceID, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_detect_workspace":
		var input struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid detect workspace request")
		}
		workspace, err := s.PetDetectWorkspace(ctx, input.WorkspaceID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(workspace)
		return string(data), nil
	case "aha_retire_remote_task_mirror":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid retire remote task request")
		}
		result, err := s.PetRetireRemoteTaskMirror(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(result)
		return string(data), nil
	case "aha_retire_remote_workspace_mirror":
		var input struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid retire remote workspace request")
		}
		result, err := s.PetRetireRemoteWorkspaceMirror(ctx, input.WorkspaceID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(result)
		return string(data), nil
	case "aha_send_task_message":
		var input PetSendTaskMessageInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid task message request")
		}
		turn, err := s.PetSendTaskMessage(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(turn)
		return string(data), nil
	case "aha_list_task_attachments":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid list attachments request")
		}
		items, err := s.PetTaskAttachments(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(items)
		return string(data), nil
	case "aha_upload_task_attachment":
		var input PetUploadAttachmentInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid upload attachment request")
		}
		item, err := s.PetUploadTaskAttachment(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(item)
		return string(data), nil
	case "aha_delete_task_attachment":
		var input struct {
			TaskID       string `json:"task_id"`
			AttachmentID string `json:"attachment_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid delete attachment request")
		}
		if err := s.PetDeleteTaskAttachment(ctx, input.TaskID, input.AttachmentID); err != nil {
			return "", err
		}
		return `{"ok":true}`, nil
	case "aha_delete_hardware_resource":
		var input struct {
			ResourceID string `json:"resource_id"`
			Reason     string `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid delete hardware resource request")
		}
		approval, err := s.PetRequestDeleteHardwareResource(ctx, input.ResourceID, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_create_hardware_resource":
		input, err := decodePetHardwareResourceInput(args)
		if err != nil {
			return "", err
		}
		view, err := s.PetCreateHardwareResource(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_update_hardware_resource":
		input, err := decodePetHardwareResourceInput(args)
		if err != nil {
			return "", err
		}
		view, err := s.PetUpdateHardwareResource(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_hardware_resources":
		items, err := s.PetHardwareResources(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(items)
		return string(data), nil
	case "aha_task_hardware":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid task hardware request")
		}
		view, err := s.PetTaskHardware(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_update_task_hardware":
		var input PetUpdateTaskHardwareInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid update task hardware request")
		}
		view, err := s.PetUpdateTaskHardware(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_mail_resources":
		var input PetMailResourcesInput
		if err := decodePetMailArgs(args, &input); err != nil {
			return "", err
		}
		view, err := s.PetMailResources(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_create_mail_account":
		var input PetMailAccountInput
		if err := decodePetMailArgs(args, &input); err != nil {
			return "", err
		}
		view, err := s.PetCreateMailAccount(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_update_mail_account":
		var input PetMailAccountInput
		if err := decodePetMailArgs(args, &input); err != nil {
			return "", err
		}
		view, err := s.PetUpdateMailAccount(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_create_mail_template":
		var input PetMailTemplateInput
		if err := decodePetMailArgs(args, &input); err != nil {
			return "", err
		}
		item, err := s.PetCreateMailTemplate(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(item)
		return string(data), nil
	case "aha_update_mail_template":
		var input PetMailTemplateInput
		if err := decodePetMailArgs(args, &input); err != nil {
			return "", err
		}
		item, err := s.PetUpdateMailTemplate(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(item)
		return string(data), nil
	case "aha_create_mail_binding":
		var input PetMailBindingInput
		if err := decodePetMailArgs(args, &input); err != nil {
			return "", err
		}
		item, err := s.PetCreateMailBinding(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(item)
		return string(data), nil
	case "aha_update_mail_binding":
		var input PetMailBindingInput
		if err := decodePetMailArgs(args, &input); err != nil {
			return "", err
		}
		item, err := s.PetUpdateMailBinding(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(item)
		return string(data), nil
	case "aha_delete_mail_resource":
		var input PetDeleteMailResourceInput
		if err := decodePetMailArgs(args, &input); err != nil {
			return "", err
		}
		approval, err := s.PetRequestDeleteMailResource(ctx, input.Kind, input.ResourceID, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_task_mail":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := decodePetMailArgs(args, &input); err != nil {
			return "", err
		}
		view, err := s.PetTaskMail(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_update_task_mail":
		var input PetUpdateTaskMailInput
		if err := decodePetMailArgs(args, &input); err != nil {
			return "", err
		}
		view, err := s.PetUpdateTaskMail(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_task_channel_route":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid task channel route request")
		}
		route, err := s.PetTaskChannelRoute(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(route)
		return string(data), nil
	case "aha_bind_task_channel":
		var input PetBindTaskChannelInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid bind task channel request")
		}
		route, err := s.PetBindTaskChannel(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(route)
		return string(data), nil
	case "aha_unbind_task_channel":
		var input struct {
			TaskID  string `json:"task_id"`
			RouteID string `json:"route_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid unbind task channel request")
		}
		if err := s.PetUnbindTaskChannel(ctx, input.TaskID, input.RouteID); err != nil {
			return "", err
		}
		return `{"ok":true}`, nil
	case "aha_complete_task":
		var input struct {
			TaskID       string `json:"task_id"`
			ReviewTaskID string `json:"review_task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid complete task request")
		}
		task, err := s.PetCompleteTaskWithReview(ctx, input.TaskID, input.ReviewTaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(task)
		return string(data), nil
	case "aha_finalize_task_worktree":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid finalize task worktree request")
		}
		result, err := s.PetFinalizeTaskWorktree(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(result)
		return string(data), nil
	case "aha_request_code_review":
		var input PetRequestCodeReviewInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid code review request")
		}
		task, err := s.PetRequestCodeReview(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(task)
		return string(data), nil
	case "aha_delete_task":
		var input struct {
			TaskID string `json:"task_id"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid delete task request")
		}
		approval, err := s.PetDeleteTask(ctx, input.TaskID, input.Reason)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_reopen_task":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid reopen task request")
		}
		task, err := s.PetReopenTask(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(task)
		return string(data), nil
	case "aha_interrupt_task":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid interrupt task request")
		}
		task, err := s.PetInterruptTask(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(task)
		return string(data), nil
	case "aha_update_task":
		var input PetUpdateTaskInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid update task request")
		}
		task, err := s.PetUpdateTask(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(task)
		return string(data), nil
	case "aha_update_task_agent":
		var input PetUpdateTaskAgentInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid update task agent request")
		}
		agent, err := s.PetUpdateTaskAgent(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(agent)
		return string(data), nil
	case "aha_compact_task_session":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid compact session request")
		}
		session, err := s.PetCompactTaskSession(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(session)
		return string(data), nil
	case "aha_reset_task_session":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid reset session request")
		}
		session, err := s.PetResetTaskSession(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(session)
		return string(data), nil
	case "aha_task_conversation":
		var input struct {
			TaskID string `json:"task_id"`
			Limit  int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid task conversation request")
		}
		items, err := s.PetTaskConversation(ctx, input.TaskID, input.Limit)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(items)
		return string(data), nil
	case "aha_task_turns":
		var input struct {
			TaskID string `json:"task_id"`
			Limit  int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid task turns request")
		}
		items, err := s.PetTaskTurns(ctx, input.TaskID, input.Limit)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(items)
		return string(data), nil
	case "pet_update_core":
		var input struct {
			Document string `json:"document"`
			Content  string `json:"content"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid update core request")
		}
		if err := s.PetUpdateCore(ctx, input.Document, input.Content); err != nil {
			return "", err
		}
		return `{"ok":true}`, nil
	case "pet_write_diary":
		var input struct {
			Date    string `json:"date"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid write diary request")
		}
		if err := s.PetWriteDiary(ctx, input.Date, input.Content); err != nil {
			return "", err
		}
		return `{"ok":true}`, nil
	case "pet_write_worklog":
		var input struct {
			Date    string `json:"date"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid write worklog request")
		}
		if err := s.PetWriteWorklog(ctx, input.Date, input.Content); err != nil {
			return "", err
		}
		return `{"ok":true}`, nil
	case "pet_submit_idea":
		var input PetIdeaInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid idea request")
		}
		approval, err := s.PetSubmitIdea(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "pet_request_approval":
		var input struct {
			Kind    string          `json:"kind"`
			Title   string          `json:"title"`
			Summary string          `json:"summary"`
			Reason  string          `json:"reason"`
			Risk    string          `json:"risk"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid approval request")
		}
		if strings.EqualFold(strings.TrimSpace(input.Risk), "high") {
			reason, reasonErr := requirePetApprovalReason(input.Reason)
			if reasonErr != nil {
				return "", reasonErr
			}
			input.Reason = reason
		}
		payload := "{}"
		if len(input.Payload) > 0 && string(input.Payload) != "null" {
			payload = string(input.Payload)
		}
		item, err := s.CreatePetApproval(ctx, PetApprovalInput{
			Kind: input.Kind, Title: input.Title, Summary: input.Summary, Reason: input.Reason,
			Risk: input.Risk, Payload: payload,
		})
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(item)
		return string(data), nil
	case "pet_list_approvals":
		var input struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(args, &input)
		items, err := s.PetApprovals(ctx, input.Limit)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(items)
		return string(data), nil
	case "pet_watch_task":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid watch task request")
		}
		watch, err := s.PetWatchTask(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(watch)
		return string(data), nil
	case "pet_unwatch_task":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid unwatch task request")
		}
		if err := s.PetUnwatchTask(ctx, input.TaskID); err != nil {
			return "", err
		}
		return `{"ok":true}`, nil
	}
	result, err := s.ControlOwnerPetTool(ctx, name, args)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(result.Actions)
	return "ok " + string(data), nil
}

// ControlOwnerPetTool maps one MCP tool call to a validated pet action.
func (s *Service) ControlOwnerPetTool(ctx context.Context, name string, args json.RawMessage) (PetBrainResult, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "pet_say" {
		var input struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &input); err != nil || strings.TrimSpace(input.Text) == "" {
			return PetBrainResult{}, fmt.Errorf("pet_say 需要非空 text")
		}
		return s.ControlOwnerPet(ctx, PetControlRequest{Say: strings.TrimSpace(input.Text), Actor: "mcp"})
	}
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
		{Name: "pet_say", Description: "在 Web 前台显示一句说话气泡；不想说话就不要调用", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"text": map[string]any{"type": "string", "description": "不超过 120 个字符"},
			}, "required": []string{"text"},
		}},
		{Name: "pet_update_core", Description: "写入宠物核心文档 identity、principles、loops 或 agenda（运行时为只读，必须用此工具写；loops 记录未闭环事项，agenda 记录可自主推进事项）", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"document": map[string]any{"type": "string", "enum": []string{"identity", "principles", "loops", "agenda"}},
				"content":  map[string]any{"type": "string"},
			}, "required": []string{"document", "content"},
		}},
		{Name: "pet_write_diary", Description: "写入宠物日记 diary/YYYY-MM-DD.md", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"date":    map[string]any{"type": "string", "description": "可选，YYYY-MM-DD"},
				"content": map[string]any{"type": "string"},
			}, "required": []string{"content"},
		}},
		{Name: "pet_write_worklog", Description: "写入宠物工作记录 worklog/YYYY-MM-DD.md", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"date":    map[string]any{"type": "string", "description": "可选，YYYY-MM-DD"},
				"content": map[string]any{"type": "string"},
			}, "required": []string{"content"},
		}},
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
