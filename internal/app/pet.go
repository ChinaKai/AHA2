package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	// NavigationID identifies the persisted navigation request for open_task.
	// Web clients must claim it before navigating, so one action reaches only
	// one session even when several tabs receive the live event.
	NavigationID string `json:"navigation_id,omitempty"`
}

type PetBrainResult struct {
	Say     string           `json:"say"`
	Actions []PetBrainAction `json:"actions"`
	// Navigation* describe the persisted open_task request. They are set only
	// when an action was queued, so callers can report the queued state instead
	// of assuming a fire-and-forget broadcast succeeded.
	NavigationID        string `json:"navigation_id,omitempty"`
	NavigationStatus    string `json:"navigation_status,omitempty"`
	NavigationExpiresAt string `json:"navigation_expires_at,omitempty"`
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
	english := s.petUsesEnglish(ctx)
	message := petAutonomousMessage(english, idleMinutes, page, input.Layout)
	// With no work waiting, a wake may be the pet's own time instead: it is the
	// Owner's partner with interests of its own, not only a worker, and a wake
	// that has nothing to do otherwise just repeats the inspection.
	freeDay := ""
	if assistant.PetFreeDailyTurns > 0 && !s.petHasPendingWork(ctx) {
		day := s.now().Local().Format("2006-01-02")
		if claimed, claimErr := s.store.ClaimPetFreeTurn(ctx, assistant.ID, day); claimErr == nil && claimed {
			freeDay = day
			used := 1
			if assistant.PetFreeDay == day {
				used = assistant.PetFreeUsed + 1
			}
			message = petFreeTimeMessage(english, used, assistant.PetFreeDailyTurns, page, input.Layout)
		}
	}
	if _, err := s.SubmitMessage(ctx, petTaskID, message); err != nil {
		// The turn was never submitted, so give the reserved slots back.
		s.releasePetBrainRun(deviceID, now)
		if freeDay != "" {
			_ = s.store.ReleasePetFreeTurn(ctx, assistant.ID, freeDay)
		}
		return PetBrainResult{}, err
	}
	if freeDay != "" {
		s.recordPetWork(ctx, "free_time", freeDay, "")
	}
	return PetBrainResult{}, nil
}

// petHasPendingWork reports whether a wake has work to do: a pet-created Task
// still open or needing attention, an unresolved background notification, or an
// agenda item the pet marked as its own next step. Items that wait on the Owner
// are not work for the pet; a wake cannot move them. When the state cannot be
// read the answer is "yes", so a failure never turns work time into free time.
func (s *Service) petHasPendingWork(ctx context.Context) bool {
	state, err := s.PetWorkState(ctx)
	if err != nil {
		return true
	}
	if len(state.OpenTasks) > 0 || state.Inspection.AttentionTasks > 0 || len(state.Notifications) > 0 {
		return true
	}
	for _, line := range strings.Split(state.Agenda, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*"))
		if strings.HasPrefix(strings.ToLower(line), "next") {
			return true
		}
	}
	return false
}

// petFreeTimeMessage is the wake the pet gets when the Turn is its own. It
// deliberately asks for no deliverable: the work wake's "every wake must leave
// a verifiable result" is what kept an idle pet re-running the inspection.
func petFreeTimeMessage(english bool, used, limit int, page string, layout PetLayout) string {
	background := strings.Contains(page, "后台")
	if english {
		where := "You are in the Web foreground and may use the avatar tools."
		if background {
			where = "You are running in the background: do not use the avatar tools and do not disturb the Owner."
		}
		return fmt.Sprintf("[Free time %d/%d today] Nothing is waiting on you, so this Turn is yours. %s\n\nSpend it on what interests you: a hobby project, trying something in pet-lab/, reading into a topic, or rethinking your own principles from what the Owner recently corrected. It does not have to produce anything for work, and you do not need to inspect AHA first.\n\nThe hard lines still hold: nothing irreversible, nothing sent outside in the Owner's name, no change to your own permissions. Project source and other projects stay as they are; pet-lab/ and your core files are yours.\n\nAt the end, note in the worklog in a sentence or two what you did and anything you learned; no evidence trail is needed. If you would rather rest, say so and stop.", used, limit, where)
	}
	where := "你在 Web 前台，可以用形象相关的工具。"
	if background {
		where = "你在后台运行：不要用形象相关的工具，也不要打扰 owner。"
	}
	return fmt.Sprintf("[自由时间 今天第 %d/%d 次] 现在没有等你处理的事，这一轮归你自己。%s\n\n做你感兴趣的事：爱好项目、在 pet-lab/ 里试点东西、研究一个想弄明白的问题，或者根据 owner 最近纠正你的地方重新想想自己的准则。不需要为工作产出任何东西，也不用先巡检 AHA。\n\n硬线仍然有效：不做撤不回的事，不以 owner 的名义对外发送，不改自己的权限。项目源码和其他项目保持原样；pet-lab/ 和你的核心文档是你自己的。\n\n结束时在 worklog 里用一两句话记下做了什么、有什么收获，不需要留证据链。如果只想歇着，说一声就结束。", used, limit, where)
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
			if json.Unmarshal([]byte(call.Arguments), &args) == nil && isPetTaskReference(args.Task) {
				action = PetBrainAction{Type: "open_task", Task: strings.TrimSpace(args.Task)}
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
	runtimeState := "Web 前台（你可以使用 pet MCP 控制形象和 owner 互动）"
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
		return "\n\nThis is a work wake-up, not a status report. Call aha_pet_work_state, read inspection, open_tasks, core/agenda.md and pending approvals. Work through these in order:\n0) close the oldest pet-created open Task before new work: verify and complete/cancel it, or file one kind=owner_decision approval (payload task_id) for a real blocker; waiting/blocked needs a decision, failed gets one retry then escalation; never leave it silent;\n1) handle an event or pending item;\n2) if the owner must decide, create a kind=owner_decision approval with pet_request_approval and write a waiting item to core/agenda.md or core/loops.md; never only state it in the final answer, and create each decision once;\n3) if nothing can be advanced, reply NOOP; an improvement worth doing can go on the agenda or become a Task, but do not invent work to avoid NOOP, and do not re-check items that only wait on the Owner;\n4) NOOP is a valid result for a wake with nothing to do;\n5) final replies: add one compact inspection line from inspection (scope, result, changes, unclosed).\nDo not repeat the open-loop list or only summarize. Approvals/ideas are created once in the approval center; agenda/loops cite them, worklog keeps execution evidence. Do not copy the same body across layers."
	}
	return "\n\n这是工作唤醒，不是状态汇报。先调用 aha_pet_work_state，读取 inspection、open_tasks、core/agenda.md 和待审批项。按顺序处理：\n0) 先关闭最早的未闭环宠物任务：验证后完成/取消，或为真实阻塞提交一次 kind=owner_decision 审批（payload 带 task_id）；waiting/blocked 需要决定，failed 先重试一次再升级；不能静默停留；\n1) 有事件或待办：处理它；\n2) 需要 owner 决定：用 pet_request_approval 创建 kind=owner_decision 的待审批，并在 core/agenda.md 或 core/loops.md 写 waiting 项；不能只写在最终回复里，同一决策只创建一次；\n3) 没有可推进项：回复 NOOP；想到值得做的改进可以写进议程或建 Task，但不要为了不回 NOOP 而找事做，也不要把只等 owner 的事项再查一遍；\n4) 没有事可做时，NOOP 就是合格的结果；\n5) 最终回复附一行巡检摘要（范围、结果、变化、未闭环），数据来自 inspection。\n不要重复列出未闭环清单，不要只汇报。审批/想法只在审批中心创建一次；agenda/loops 只写引用和状态，worklog 只写执行证据，不要跨层复制正文。"
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
			if isPetTaskReference(action.Task) {
				result.Actions = append(result.Actions, action)
			}
		}
	}
	return result
}

type PetSettingsInput struct {
	Enabled   bool   `json:"enabled"`
	Character string `json:"character"`
	// ChatModelID is the legacy single-model field. It is only consulted when
	// no source-first pet selection is present, and it then selects the one
	// unified Provider model. A value that disagrees with the pet selection is
	// ignored: the chat model can no longer move independently.
	ChatModelID string `json:"chat_model_id"`
	// BrainModelID is the legacy pet runtime field. New callers select a
	// source-first runtime with the four PetModel* fields; the catalog model ID
	// is then derived server-side and stored in BrainModelID.
	BrainModelID      string   `json:"brain_model_id"`
	PetModelSource    string   `json:"pet_model_source"`
	PetModelID        string   `json:"pet_model_id"`
	PetWireModel      string   `json:"pet_wire_model"`
	PetCodexAccountID string   `json:"pet_codex_account_id"`
	Actions           []string `json:"actions"`
	Identity          string   `json:"identity"`
	Principles        string   `json:"principles"`
	IdleMinutes       int      `json:"idle_minutes"`
	QuietEnabled      *bool    `json:"quiet_enabled"`
	QuietStart        int      `json:"quiet_start"`
	QuietEnd          int      `json:"quiet_end"`
	HourlyLimit       int      `json:"hourly_limit"`
	// FreeDailyTurns is the pet's daily free-time allowance in Turns; nil keeps
	// the stored value, 0 turns free time off.
	FreeDailyTurns *int `json:"free_daily_turns"`

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
	// One model fact: the source-first pet selection below is the only write
	// path. The legacy chat_model_id/brain_model_id inputs are accepted for old
	// clients but map onto the same unified field instead of creating a second
	// chat model. pet_brain_model_id is authoritative; model_id is its mirror.
	petSelectionProvided := strings.TrimSpace(input.PetModelSource) != "" ||
		strings.TrimSpace(input.PetModelID) != "" ||
		strings.TrimSpace(input.PetWireModel) != "" ||
		strings.TrimSpace(input.PetCodexAccountID) != ""
	if petSelectionProvided {
		if s.petSelectionMatchesStored(ctx, assistant, input) {
			// The submitted runtime is already in effect. Re-resolving it would
			// turn a deleted account or model into a hard failure that blocks
			// unrelated settings such as disabling the pet or changing
			// permissions, so keep the stored selection as-is.
		} else {
			model, resolveErr := s.resolvePetRuntimeSelection(ctx,
				input.PetModelSource, input.PetModelID, input.PetWireModel, input.PetCodexAccountID)
			if resolveErr != nil {
				return OwnerAssistantView{}, fmt.Errorf("宠物模型不可用：%w", resolveErr)
			}
			assistant.BrainModelID = model.ID
		}
	} else if brainModelID := strings.TrimSpace(input.BrainModelID); brainModelID != "" {
		if brainModelID == strings.TrimSpace(assistant.BrainModelID) {
			// Legacy field naming the current runtime; nothing to re-resolve.
		} else {
			model, resolveErr := s.resolvePetRuntimeSelection(ctx, "", brainModelID, "", "")
			if resolveErr != nil {
				return OwnerAssistantView{}, fmt.Errorf("宠物模型不可用：%w", resolveErr)
			}
			assistant.BrainModelID = model.ID
		}
	} else if chatModelID := strings.TrimSpace(input.ChatModelID); chatModelID != "" {
		// Compatibility with the old single-model setting: an old client that
		// only sent chat_model_id selects that Provider model for chat and
		// autonomy together.
		if !validModel(chatModelID) {
			return OwnerAssistantView{}, fmt.Errorf("聊天模型不可用，请选择 Provider 模型")
		}
		model, resolveErr := s.resolvePetRuntimeSelection(ctx, domain.ModelSourceProvider, chatModelID, "", "")
		if resolveErr != nil {
			return OwnerAssistantView{}, fmt.Errorf("宠物模型不可用：%w", resolveErr)
		}
		assistant.BrainModelID = model.ID
	}
	if strings.TrimSpace(assistant.BrainModelID) == "" {
		assistant.BrainModelID = strings.TrimSpace(assistant.ModelID)
	}
	assistant.ModelID = strings.TrimSpace(assistant.BrainModelID)
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
	if input.FreeDailyTurns != nil {
		if *input.FreeDailyTurns < 0 || *input.FreeDailyTurns > 48 {
			return OwnerAssistantView{}, fmt.Errorf("每日自由活动需在 0–48 次")
		}
		assistant.PetFreeDailyTurns = *input.FreeDailyTurns
	}
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
	// Keep the derived chat mirror in the same committed value even if an older
	// writer raced this save; a reader must never see two different models.
	if err := s.store.UpdateOwnerAssistantModel(ctx, assistant.ID, assistant.BrainModelID, assistant.UpdatedAt); err != nil {
		return OwnerAssistantView{}, err
	}
	if err := s.store.UpdateOwnerPetBudget(ctx, assistant.ID, assistant.PetIdleMinutes, assistant.PetQuietEnabled, assistant.PetQuietStart, assistant.PetQuietEnd, assistant.PetHourlyLimit, assistant.UpdatedAt); err != nil {
		return OwnerAssistantView{}, err
	}
	if err := s.store.UpdateOwnerPetFreeDailyTurns(ctx, assistant.ID, assistant.PetFreeDailyTurns, assistant.UpdatedAt); err != nil {
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
	if assistant.PetEnabled {
		// A disabled pet has no runnable Task. Skipping the sync on disable
		// means a stale or deleted runtime can never block turning the pet off
		// or changing its permissions.
		if err := s.syncPetTaskRuntime(ctx, assistant.BrainModelID); err != nil {
			return OwnerAssistantView{}, fmt.Errorf("宠物 Task 模型同步失败：%w", err)
		}
	}
	if state, stateErr := s.OwnerPetState(ctx); stateErr == nil {
		s.publishPetState(state)
	}
	return s.OwnerAssistant(ctx)
}

// petSelectionMatchesStored reports whether the submitted pet runtime selection
// already names the stored one. The Web picker sends an empty model_id for
// official and claude_native sources and relies on the server to derive the
// catalog id, so an empty id is matched by deriving the expected id from the
// submitted source/wire/account (or from the stored model row when a field is
// omitted). A provider selection's empty model_id means "automatic" and is a
// real change, never an unchanged value. The legacy brain_model_id field is
// still accepted as an exact id comparison.
func (s *Service) petSelectionMatchesStored(ctx context.Context, assistant domain.OwnerAssistant, input PetSettingsInput) bool {
	currentID := strings.TrimSpace(assistant.BrainModelID)
	submittedID := strings.TrimSpace(input.PetModelID)
	if submittedID == "" {
		if legacyID := strings.TrimSpace(input.BrainModelID); legacyID != "" {
			return legacyID == currentID
		}
	}

	stored, storedErr := s.store.Model(ctx, currentID)
	storedFound := storedErr == nil

	source := strings.TrimSpace(input.PetModelSource)
	if source == "" && storedFound {
		source = strings.TrimSpace(stored.Source)
	}
	if source == "" {
		// Without a source or a stored row there is nothing to derive. An
		// exact id match is still the same selection.
		return submittedID != "" && submittedID == currentID
	}
	normalized, ok := domain.NormalizeModelSource(source)
	if !ok {
		return false
	}
	inherit := func(submitted, storedValue string) string {
		if submitted != "" {
			return submitted
		}
		return strings.TrimSpace(storedValue)
	}
	switch normalized {
	case domain.ModelSourceProvider:
		// Empty model_id is the explicit automatic choice. Provider ids are
		// stored literally, so both sides must agree, not inherit.
		if submittedID == "" || submittedID != currentID {
			return false
		}
		if storedFound {
			storedSource := strings.TrimSpace(stored.Source)
			if normalizedStored, normalizedOK := domain.NormalizeModelSource(storedSource); normalizedOK {
				storedSource = normalizedStored
			}
			if storedSource == "" {
				storedSource = domain.ModelSourceProvider
			}
			if storedSource != domain.ModelSourceProvider {
				return false
			}
		}
		return true
	case domain.ModelSourceOfficial:
		wireModel := strings.TrimSpace(input.PetWireModel)
		accountID := strings.TrimSpace(input.PetCodexAccountID)
		if storedFound {
			wireModel = inherit(wireModel, stored.WireModel)
			accountID = inherit(accountID, stored.CodexAccountID)
		}
		if wireModel == "" || accountID == "" {
			return false
		}
		if submittedID != "" && submittedID != currentID {
			return false
		}
		return domain.OfficialCodexModelID(accountID, wireModel) == currentID
	case domain.ModelSourceClaudeNative:
		wireModel := strings.TrimSpace(input.PetWireModel)
		if storedFound {
			wireModel = inherit(wireModel, stored.WireModel)
		}
		if wireModel == "" {
			return false
		}
		if submittedID != "" && submittedID != currentID {
			return false
		}
		return domain.ClaudeNativeModelID(wireModel) == currentID
	default:
		return false
	}
}

// petCodexAccountForWireModel resolves an account for an official wire model
// with the same rules as the channel runtime: only configured, ready accounts
// count, one candidate is auto-selected, and an ambiguous match must be made
// explicit instead of silently taking the first row.
func (s *Service) petCodexAccountForWireModel(ctx context.Context, wireModel string) (string, error) {
	accounts, err := s.store.ListCodexAccounts(ctx)
	if err != nil {
		return "", err
	}
	candidates := make([]domain.CodexAccount, 0, len(accounts))
	for _, account := range accounts {
		if !account.CredentialConfigured || account.Status != "ready" {
			continue
		}
		if codexAccountOffersWireModel(account, wireModel) {
			candidates = append(candidates, account)
		}
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("没有可用的 Codex 官方账号提供模型 %q", wireModel)
	case 1:
		return candidates[0].ID, nil
	default:
		return "", fmt.Errorf("多个 Codex 官方账号提供模型 %q，请明确选择账号", wireModel)
	}
}

// codexAccountOffersWireModel reports whether one Codex account lists a wire
// model in its probed catalog.
func codexAccountOffersWireModel(account domain.CodexAccount, wireModel string) bool {
	for _, option := range account.AvailableModels {
		if option.WireModel == wireModel {
			return true
		}
	}
	return false
}

// resolvePetRuntimeSelection turns the pet settings selection into a catalog
// model. An empty source is inferred from model_id; an official selection
// without an explicit account is resolved only when exactly one configured
// account offers that wire model.
func (s *Service) resolvePetRuntimeSelection(ctx context.Context, source, modelID, wireModel, accountID string) (domain.Model, error) {
	normalized, ok := domain.NormalizeModelSource(source)
	if !ok {
		return domain.Model{}, fmt.Errorf("model_source 必须是 provider、official 或 claude_native")
	}
	modelID = strings.TrimSpace(modelID)
	wireModel = strings.TrimSpace(wireModel)
	accountID = strings.TrimSpace(accountID)
	if modelID != "" {
		model, err := s.store.Model(ctx, modelID)
		if err != nil {
			return domain.Model{}, fmt.Errorf("所选模型不存在或已删除")
		}
		modelSource := strings.TrimSpace(model.Source)
		if modelSource == "" {
			modelSource = domain.ModelSourceProvider
		}
		if normalized != "" && normalized != modelSource {
			return domain.Model{}, fmt.Errorf("模型 %q 属于模型源 %s，不能通过 model_source=%s 选择", modelID, modelSource, normalized)
		}
		if normalized == "" {
			normalized = modelSource
		}
		if wireModel == "" {
			wireModel = model.WireModel
		}
		if accountID == "" {
			accountID = model.CodexAccountID
		}
	}
	if normalized == "" {
		normalized = domain.ModelSourceProvider
	}
	if normalized == domain.ModelSourceProvider && modelID == "" {
		return domain.Model{}, fmt.Errorf("请选择 Provider 宠物运行时模型")
	}
	if normalized == domain.ModelSourceOfficial && wireModel == "" {
		return domain.Model{}, fmt.Errorf("请选择 Codex 官方宠物运行时模型")
	}
	if normalized == domain.ModelSourceOfficial && accountID == "" && wireModel != "" {
		resolved, err := s.petCodexAccountForWireModel(ctx, wireModel)
		if err != nil {
			return domain.Model{}, err
		}
		accountID = resolved
	}
	if normalized == domain.ModelSourceOfficial {
		// Validate the official account here so every failure reaches the
		// settings page as business Chinese instead of a storage error.
		account, err := s.store.CodexAccount(ctx, accountID)
		if err != nil {
			return domain.Model{}, fmt.Errorf("所选 Codex 官方账号已删除，请重新选择账号")
		}
		if !account.CredentialConfigured {
			return domain.Model{}, fmt.Errorf("所选 Codex 官方账号尚未配置凭据，请重新选择账号")
		}
		if status := strings.TrimSpace(account.Status); status != "" && status != "ready" {
			return domain.Model{}, fmt.Errorf("所选 Codex 官方账号当前不可用（%s），请重新选择账号", status)
		}
		if !codexAccountOffersWireModel(account, wireModel) {
			return domain.Model{}, fmt.Errorf("所选模型不在该 Codex 官方账号的可用模型列表中")
		}
	}
	model, _, accountID, err := s.resolveRuntimeSelection(ctx, runtimeSelectionInput{
		WorkspaceID: petWorkspaceID, ModelSource: normalized, ModelID: modelID,
		WireModel: wireModel, CodexAccountID: accountID,
	})
	if err != nil {
		return domain.Model{}, petRuntimeSelectionError(normalized, err)
	}
	if model.Source == "" {
		model.Source = normalized
	}
	if accountID != "" && model.CodexAccountID == "" {
		model.CodexAccountID = accountID
	}
	return model, nil
}

// petRuntimeSelectionError replaces storage-level not-found errors with the
// business wording the settings page can show. sql: no rows must never reach
// the owner.
func petRuntimeSelectionError(source string, err error) error {
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if source == domain.ModelSourceOfficial {
		return fmt.Errorf("所选 Codex 官方账号已删除，请重新选择账号")
	}
	return fmt.Errorf("所选模型已删除，请重新选择模型")
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
			// Accept the internal id and the human-facing code; a background
			// Turn usually sees the code in open_tasks while the Web UI routes
			// by the internal id.
			task, err := s.resolvePetOpenTask(ctx, action.Task)
			if err != nil {
				return PetBrainResult{}, err
			}
			action.Task = task.ID
			result.Actions = append(result.Actions, action)
		case "sleep", "wake", "thinking":
			result.Actions = append(result.Actions, action)
		}
	}
	if result.Say == "" && len(result.Actions) == 0 {
		return PetBrainResult{}, fmt.Errorf("没有可执行的宠物动作")
	}
	// Persist open_task before publishing. A connected Web client still gets
	// the live event, but a background Turn with no client attached now leaves
	// a durable, single-use navigation request instead of losing the action.
	for i := range result.Actions {
		if result.Actions[i].Type != "open_task" {
			continue
		}
		queued, err := s.QueuePetNavigation(ctx, result.Actions[i].Task)
		if err != nil {
			return PetBrainResult{}, fmt.Errorf("排队打开 Task 失败：%w", err)
		}
		result.Actions[i].NavigationID = queued.ID
		result.NavigationID = queued.ID
		result.NavigationStatus = queued.Status
		result.NavigationExpiresAt = queued.ExpiresAt.Format(time.RFC3339)
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
	case "aha_overview", "aha_pet_work_state", "aha_pet_permissions", "aha_request_permission_change", "aha_capabilities", "pet_request_approval", "pet_list_approvals", "pet_submit_idea", "aha_ack_knowledge_sync_notification", "aha_recover_knowledge_sync":
		return true
	case "pet_say", "pet_play", "pet_move_to", "pet_walk_path", "pet_mood", "pet_open_task", "pet_thinking", "pet_sleep", "pet_wake",
		"pet_update_core", "pet_write_worklog":
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
		"aha_delete_mail_resource", "aha_task_mail", "aha_update_task_mail",
		"aha_list_mcp_servers", "aha_test_mcp_server", "aha_list_task_mcp_servers",
		"aha_set_task_mcp_servers", "aha_request_mcp_server_create",
		"aha_request_mcp_server_update", "aha_request_mcp_server_delete":
		return assistant.PetAllowManageSystem
	case "aha_list_projects":
		return assistant.PetAllowViewTasks || assistant.PetAllowCreateTasks || assistant.PetAllowCreateWorkspaces
	case "aha_create_task", "aha_start_task", "aha_takeover_remote_task", "aha_complete_task", "aha_finalize_task_worktree", "aha_request_code_review", "aha_delete_task", "aha_reopen_task", "aha_interrupt_task", "aha_update_task", "aha_update_task_agent", "aha_upload_task_attachment", "aha_delete_task_attachment", "aha_copy_task_attachment", "aha_update_task_hardware", "aha_bind_task_channel", "aha_unbind_task_channel", "aha_retire_remote_task_mirror", "aha_compact_task_session", "aha_reset_task_session":
		return assistant.PetAllowCreateTasks
	case "aha_create_project", "aha_create_workspace", "aha_takeover_remote_workspace", "aha_update_project", "aha_delete_project", "aha_update_workspace", "aha_delete_workspace", "aha_detect_workspace", "aha_retire_remote_workspace_mirror":
		return assistant.PetAllowCreateWorkspaces
	case "aha_send_task_message":
		return assistant.PetAllowMessageTasks
	case "aha_create_provider", "aha_update_provider", "aha_delete_provider", "aha_detect_models",
		"aha_create_model", "aha_update_model", "aha_delete_model", "aha_delete_models",
		"aha_create_env_group", "aha_update_env_group", "aha_delete_env_group", "aha_restore_previous_credential":
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
		{Name: "aha_pet_work_state", Description: "开始工作前查看：巡检快照、所有未关闭的宠物任务、审批、未处理的知识库自动同步异常（notifications）和最近 Task 进展。默认只返回最近 8 条审批/工作动态且不含 last_turn_result（响应控制在 16KB 内，history_truncated 表示默认预算压掉了历史）；需要完整历史时传 history_limit（最多 50）和 include_turn_results=true，单任务详情用 aha_task_detail/aha_task_turns", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"history_limit": map[string]any{
					"type": "integer", "minimum": 0, "maximum": 50,
					"description": "审批与工作动态各返回的最大条数，默认 8；0 表示不返回这两个历史列表",
				},
				"include_turn_results": map[string]any{
					"type":        "boolean",
					"description": "是否返回 last_turn_result，默认 false；true 时默认返回完整结果，可用 turn_result_chars 截断",
				},
				"turn_result_chars": map[string]any{
					"type": "integer", "minimum": 0, "maximum": 20000,
					"description": "include_turn_results=true 时 last_turn_result 的最大字符数；0 或缺省表示不截断",
				},
			},
		}},
		{Name: "aha_ack_knowledge_sync_notification", Description: "把已看过的知识库自动同步通知标记为已处理（只改通知状态，不会恢复自动同步，也不解决 Git 分叉）；真正修复后仍需手动 pull/push/sync 或等自动同步成功", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"ids": map[string]any{
					"type": "array", "minItems": 1, "items": map[string]any{"type": "string"},
					"description": "aha_pet_work_state 返回的 notifications[].id",
				},
			}, "required": []string{"ids"},
		}},
		{Name: "aha_recover_knowledge_sync", Description: "在确认 Git 分叉或阻塞已经处理完后，清除知识库自动同步的暂停状态并唤醒下一次同步；不会开启已关闭的自动同步，不执行 Git 操作，也不会直接关闭通知（只有真正同步成功后通知才关闭）。未暂停时返回 no-op 和当前状态", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_pet_permissions", Description: "只读查看当前宠物权限开关", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_capabilities", Description: "按当前权限返回可用工具目录；需要知道有哪些命令时调用，不要凭记忆猜", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_request_permission_change", Description: "提交权限变更审批；owner 批准后才生效，宠物不能自行改权限", Parameters: map[string]any{
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
		{Name: "aha_skill_detail", Description: "只读查看 Skill 说明和文件名；include_files=true 时同时返回包内文本文件的内容", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"skill_id":      map[string]any{"type": "string"},
				"include_files": map[string]any{"type": "boolean", "description": "要修改包内文件前先读取它们"},
			}, "required": []string{"skill_id"},
		}},
		{Name: "aha_create_skill", Description: "创建 Skill：默认项目级（需 project_id）；scope=global 创建对所有项目生效的全局 Skill", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"scope":        map[string]any{"type": "string", "enum": []string{"project", "global"}},
				"project_id":   map[string]any{"type": "string", "description": "项目级 Skill 必填"},
				"name":         map[string]any{"type": "string"},
				"description":  map[string]any{"type": "string"},
				"instructions": map[string]any{"type": "string"},
				"status":       map[string]any{"type": "string", "enum": []string{"active", "draft", "disabled"}},
				"enabled":      map[string]any{"type": "boolean"},
				"files": map[string]any{
					"type":        "array",
					"description": "完整的包文件列表（必须包含 SKILL.md）；传了就整体替换包内文本文件，不传则只改 SKILL.md 的字段、其余文件保持不变",
					"items": map[string]any{
						"type": "object", "properties": map[string]any{
							"path":    map[string]any{"type": "string"},
							"content": map[string]any{"type": "string"},
						}, "required": []string{"path", "content"},
					},
				},
			}, "required": []string{"name", "instructions"},
		}},
		{Name: "aha_update_skill", Description: "修改项目级或全局 Skill；未提供的字段保持不变。归档（archive）Skill 只读", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"skill_id":     map[string]any{"type": "string"},
				"name":         map[string]any{"type": "string"},
				"description":  map[string]any{"type": "string"},
				"instructions": map[string]any{"type": "string"},
				"status":       map[string]any{"type": "string", "enum": []string{"active", "draft", "disabled"}},
				"enabled":      map[string]any{"type": "boolean"},
				"files": map[string]any{
					"type":        "array",
					"description": "完整的包文件列表（必须包含 SKILL.md）；传了就整体替换包内文本文件，不传则只改 SKILL.md 的字段、其余文件保持不变",
					"items": map[string]any{
						"type": "object", "properties": map[string]any{
							"path":    map[string]any{"type": "string"},
							"content": map[string]any{"type": "string"},
						}, "required": []string{"path", "content"},
					},
				},
			}, "required": []string{"skill_id"},
		}},
		{Name: "aha_delete_skill", Description: "提交删除项目级或全局 Skill 的审批；owner 批准后才删除", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"skill_id": map[string]any{"type": "string"},
				"reason":   map[string]any{"type": "string", "description": "为什么需要删除；必填"},
			}, "required": []string{"skill_id", "reason"},
		}},
		{Name: "aha_delete_knowledge", Description: "提交删除知识条目审批（支持 project/global）；owner 批准后才删除，空 AHA1 目录会自动清理", Parameters: map[string]any{
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
		{Name: "aha_update_prompt_template", Description: "提交 Prompt 模板修改审批；owner 批准后才写入，身份/安全/协议模板不允许修改", Parameters: map[string]any{
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
		{Name: "aha_send_channel_message", Description: "提交对外渠道发送审批；owner 批准后才发送到已绑定的群聊渠道", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":                   map[string]any{"type": "string"},
				"message":                   map[string]any{"type": "string"},
				"reason":                    map[string]any{"type": "string", "description": "为什么需要对外发送；至少 8 个字符"},
				"mention_identity_link_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"attachment_ids":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "required": []string{"task_id", "message", "reason"},
		}},
		{Name: "aha_set_channel_enabled", Description: "提交启用/停用渠道审批；owner 批准后才执行", Parameters: map[string]any{
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
		{Name: "aha_update_security_settings", Description: "提交安全设置修改审批；owner 批准后立即应用 access_scope 和 Origin 校验", Parameters: map[string]any{
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
		{Name: "aha_delete_provider", Description: "提交删除 Provider 审批；owner 批准后才删除，且不能有模型引用", Parameters: map[string]any{
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
		{Name: "aha_delete_model", Description: "提交删除模型审批；owner 批准后才删除", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"model_id": map[string]any{"type": "string"},
				"reason":   map[string]any{"type": "string", "description": "为什么删除；至少 8 个字符"},
			}, "required": []string{"model_id", "reason"},
		}},
		{Name: "aha_delete_models", Description: "提交批量删除模型审批：只生成一张审批，owner 批准后在同一个事务里删除全部；任一模型已不存在时整批不删除", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"model_ids": map[string]any{
					"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": 100,
					"description": "要删除的模型 ID 列表",
				},
				"reason": map[string]any{"type": "string", "description": "为什么删除；至少 8 个字符"},
			}, "required": []string{"model_ids", "reason"},
		}},
		{Name: "aha_create_env_group", Description: "创建 Env Group；environment 值不会在结果中返回", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"name":        map[string]any{"type": "string"},
				"provider_id": map[string]any{"type": "string"},
				"backend":     map[string]any{"type": "string"},
				"environment": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			}, "required": []string{"name", "provider_id", "backend"},
		}},
		{Name: "aha_update_env_group", Description: "修改 Env Group；environment 会整体替换，被替换的旧值保留一代，可用 aha_restore_previous_credential 恢复", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"env_group_id": map[string]any{"type": "string"},
				"name":         map[string]any{"type": "string"},
				"provider_id":  map[string]any{"type": "string"},
				"backend":      map[string]any{"type": "string"},
				"environment":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"reason":       map[string]any{"type": "string", "description": "修改 environment 等环境变量时必填；至少 8 个字符"},
			}, "required": []string{"env_group_id"},
		}},
		{Name: "aha_restore_previous_credential", Description: "撤销上一次覆盖：把 Provider 的 API Key 或 Env Group 的 environment 恢复成被覆盖前的值（只保留一代；恢复本身也可再撤销）。不返回任何凭据值", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"target": map[string]any{"type": "string", "enum": []string{"provider", "env_group"}},
				"id":     map[string]any{"type": "string", "description": "Provider ID 或 Env Group ID"},
			}, "required": []string{"target", "id"},
		}},
		{Name: "aha_delete_env_group", Description: "提交删除 Env Group 审批；owner 批准后才删除", Parameters: map[string]any{
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
				"model_source":     map[string]any{"type": "string", "enum": []string{"provider", "official", "claude_native"}, "description": "可选；模型来源。省略时按模型目录推断，托管模型会自动落到对应来源；显式填写与目录冲突时报错"},
				"wire_model":       map[string]any{"type": "string", "description": "可选；托管模型的底层模型名。通常可省略，目录模型会自带"},
				"codex_account_id": map[string]any{"type": "string", "description": "可选；official 模型使用的 Codex 账号 ID。省略时仅当恰好一个就绪账号提供该模型才自动选择"},
				"reasoning_effort": map[string]any{"type": "string", "enum": []string{"low", "medium", "high", "xhigh", "max"}, "description": "可选；默认 high"},
				"filesystem":       map[string]any{"type": "string", "enum": []string{"read-only", "workspace-write", "danger-full-access"}, "description": "可选；默认完全访问"},
				"approval":         map[string]any{"type": "string", "enum": []string{"never", "auto"}, "description": "可选；默认 never"},
				"isolation":        map[string]any{"type": "string", "enum": []string{"worktree", "inplace"}, "description": "可选；git 项目默认 worktree；只读或非代码任务可显式用 inplace；非 git 项目强制 inplace"},
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
							"host":           map[string]any{"type": "string"},
							"port":           map[string]any{"type": "integer"},
							"protocol":       map[string]any{"type": "string", "enum": []string{"telnet", "raw", "ssh", "adb"}},
							"ssh_auth":       map[string]any{"type": "string", "enum": []string{"auto", "password", "key"}},
							"adb_connection": map[string]any{"type": "string", "enum": []string{"wireless", "tcp", "usb"}},
							"adb_serial":     map[string]any{"type": "string"},
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
		{Name: "aha_delete_project", Description: "提交删除项目审批；owner 批准后才会删除项目及其本地任务和 Workspace", Parameters: map[string]any{
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
		{Name: "aha_delete_workspace", Description: "提交删除 Workspace 审批；owner 批准后才会删除 Workspace 及其本地任务", Parameters: map[string]any{
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
		{Name: "aha_copy_task_attachment", Description: "把同一 Task 内的一个附件克隆成新的未绑定附件（复用同一文件内容，不重新上传）；返回新附件 ID，可随后用 aha_send_task_message 发送。原附件记录及其消息绑定不受影响", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":       map[string]any{"type": "string"},
				"attachment_id": map[string]any{"type": "string"},
			}, "required": []string{"task_id", "attachment_id"},
		}},
		{Name: "aha_hardware_resources", Description: "只读列出可绑定硬件资源（不返回凭据）", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_delete_hardware_resource", Description: "提交删除硬件资源审批；owner 批准后才删除", Parameters: map[string]any{
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
					"type": "object", "description": "mode=network 时 host 必填（ADB USB 直连除外，改为 adb_serial 必填）；port 不传时按协议使用默认端口",
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
						"adb_connection": map[string]any{"type": "string", "enum": []string{
							domain.HardwareADBConnectionWireless, domain.HardwareADBConnectionTCP, domain.HardwareADBConnectionUSB,
						}, "description": "仅 protocol=adb：wireless=Android 无线调试（host+调试连接端口，配对码由 Owner 在 Web 资源页配对）；tcp=网络直连（显式 host+port，默认 5555，不配对、不做 mDNS 端口发现，设备需已开启网络 ADB）；usb=USB 直连（只用 adb_serial，不用 host/port/配对码）。不传时旧资源按 wireless"},
						"adb_serial": map[string]any{"type": "string", "description": "仅 adb_connection=usb：AHA 宿主机上 USB 设备的序列号（Owner 在 Web 资源页从检测到的设备中选择）；不接受 IP:端口、mDNS 名或模拟器"},
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
					"type": "object", "description": "未提供字段保持原值；mode=network 时 host 必填（ADB USB 直连除外，改为 adb_serial 必填）",
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
						"adb_connection": map[string]any{"type": "string", "enum": []string{
							domain.HardwareADBConnectionWireless, domain.HardwareADBConnectionTCP, domain.HardwareADBConnectionUSB,
						}, "description": "仅 protocol=adb：wireless=Android 无线调试（host+调试连接端口，配对码由 Owner 在 Web 资源页配对）；tcp=网络直连（显式 host+port，默认 5555，不配对、不做 mDNS 端口发现，设备需已开启网络 ADB）；usb=USB 直连（只用 adb_serial，不用 host/port/配对码）。不传时旧资源按 wireless"},
						"adb_serial": map[string]any{"type": "string", "description": "仅 adb_connection=usb：AHA 宿主机上 USB 设备的序列号（Owner 在 Web 资源页从检测到的设备中选择）；不接受 IP:端口、mDNS 名或模拟器"},
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
		{Name: "aha_delete_mail_resource", Description: "提交删除邮件账号/模板/绑定的审批；owner 批准后才执行。模板按现有规则归档，历史版本保留", Parameters: map[string]any{
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
		{Name: "aha_complete_task", Description: "把 Task 标记为完成；宠物自行判断是否需要评审，必须显式申报 review_decision；跳过评审必须说明理由。评审 Task 自身无需递归评审，但最新成功结果仍必须有最终 verdict", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":         map[string]any{"type": "string"},
				"review_decision": map[string]any{"type": "string", "enum": []string{"required", "skipped"}, "description": "必填：required 需要独立评审；skipped 跳过评审并说明理由"},
				"review_reason":   map[string]any{"type": "string", "description": "skipped 时必填且非空，建议至少 8 字符"},
				"review_task_id":  map[string]any{"type": "string", "description": "required 时必填（评审 Task 自身豁免）：已关联源 Task、已完成且最新结果为 Verdict: APPROVE 的独立 Review Task ID（high/xhigh/max 推理、不同模型、approval=never、inplace；评审不得修改源码）"},
			}, "required": []string{"task_id", "review_decision"},
		}},
		{Name: "aha_finalize_task_worktree", Description: "仅用于已完成的 worktree Task：检查提交、合并回目标分支、删除临时 worktree 和分支；有未提交改动或冲突时保留并报错。返回 merge_strategy（fast_forward/merge_commit/none）、source_head、target_head_before、target_head_after 与 commits，可直接引用目标分支的新 HEAD", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
			}, "required": []string{"task_id"},
		}},
		{Name: "aha_request_code_review", Description: "为源 Task 创建独立的不同模型 Review Task，创建时一次性应用权限/推理/Skill（运行中的 Turn 不会热更新）；默认 read-only、high、approval=never、inplace，推理至少 high；评审可运行 git diff、测试和构建但不得修改、提交、合并或部署源码；由宠物自行判断是否需要评审，完成时必须申报决定", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":          map[string]any{"type": "string"},
				"review_model_id":  map[string]any{"type": "string", "description": "评审模型，必须与源 Task 模型不同；用 aha_model_catalog 选择更强的模型"},
				"focus":            map[string]any{"type": "string", "description": "可选：额外关注的审查重点"},
				"filesystem":       map[string]any{"type": "string", "enum": []string{"read-only", "workspace-write", "danger-full-access"}, "description": "可选；创建时生效，默认 read-only"},
				"reasoning_effort": map[string]any{"type": "string", "enum": []string{"low", "medium", "high", "xhigh", "max"}, "description": "可选；创建时生效，默认 high"},
				"skill_ids":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "可选；创建时生效。为空时自动附加项目或全局可用的 tiered-code-review Skill；找不到时 Review Task 照常创建并在 warnings 中说明"},
			}, "required": []string{"task_id", "review_model_id"},
		}},
		{Name: "aha_delete_task", Description: "提交删除 Task 审批；owner 批准后才会删除", Parameters: map[string]any{
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
		{Name: "aha_update_task_agent", Description: "修改 Task main Agent 的模型源、模型、推理强度、文件权限、审批方式或 Fast 模式。model_source=provider 用 model_id 选 Env 模型；model_source=claude_native 需要 wire_model；model_source=official 需要 wire_model 和 codex_account_id（或目录里的 official model_id）。不传 model_source 时按 model_id 所属模型源推断；模型切换无法确定来源会返回错误，不会假装成功。调用必须携带宠物 Task 自己的 Turn capability", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
				"model_source": map[string]any{
					"type": "string", "enum": []string{"provider", "official", "claude_native", "env", "codex_official"},
					"description": "provider/env=Env Provider 模型；official/codex_official=Codex 官方账号；claude_native=Claude Code 官方账号",
				},
				"model_id":         map[string]any{"type": "string", "description": "模型目录 ID；provider 必填，也可用于从目录推断 official/claude_native"},
				"wire_model":       map[string]any{"type": "string", "description": "官方账号的实际模型名；official 和 claude_native 必填（可用 model_id 代替）"},
				"codex_account_id": map[string]any{"type": "string", "description": "official 模型源使用的 Codex 官方账号；省略时选择提供该模型的已配置账号"},
				"backend":          map[string]any{"type": "string"},
				"reasoning_effort": map[string]any{"type": "string"},
				"filesystem":       map[string]any{"type": "string", "enum": []string{"read-only", "workspace-write", "danger-full-access"}},
				"approval":         map[string]any{"type": "string", "enum": []string{"never", "auto"}},
				"fast_mode":        map[string]any{"type": "boolean"},
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
		{Name: "aha_list_mcp_servers", Description: "只读列出 MCP server 的 id、name、transport、enabled、builtin、health 和工具数量；只返回 env/header 键名，绝不返回值。", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "aha_test_mcp_server", Description: "对指定 MCP 做只读连通与 tools/list 探测，返回 health、工具名和脱敏后的错误；不修改配置。", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"server_id": map[string]any{"type": "string"},
			}, "required": []string{"server_id"},
		}},
		{Name: "aha_list_task_mcp_servers", Description: "只读查看某个 Task 绑定的 MCP server ID 与健康摘要；不返回值。", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
			}, "required": []string{"task_id"},
		}},
		{Name: "aha_set_task_mcp_servers", Description: "设置 Task 绑定的 MCP server ID，只允许已存在且 enabled 的非自动注入 server。宠物可直接设置 task_pet；其他 Task 会创建 Owner 审批。", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"task_id":    map[string]any{"type": "string"},
				"server_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"reason":     map[string]any{"type": "string", "description": "设置其他 Task 时必填；至少 8 个字符"},
			}, "required": []string{"task_id", "server_ids"},
		}},
		{Name: "aha_request_mcp_server_create", Description: "提交新增外部 MCP 的 Owner 审批；只创建审批，不保存。payload 只记录 env/header 键名，不接收 secret 值；stdio 禁止 shell 包装命令。外部 MCP 会以 AHA 服务账号权限执行，批准前务必核对来源。", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"name":        map[string]any{"type": "string"},
				"server_id":   map[string]any{"type": "string", "description": "可选；缺省时自动生成"},
				"description": map[string]any{"type": "string"},
				"transport":   map[string]any{"type": "string", "enum": []string{"http", "stdio"}},
				"command":     map[string]any{"type": "string", "description": "stdio 可执行文件；不能是 shell、cmd/.bat/.ps1 或脚本宿主"},
				"args":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"url":         map[string]any{"type": "string", "description": "http(s) URL；不能内嵌凭据或查询参数"},
				"env_keys":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "只填键名；值由 owner 在 Web 或 Secret Store 填写"},
				"header_keys": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "只填键名；值由 owner 在 Web 或 Secret Store 填写"},
				"enabled":     map[string]any{"type": "boolean"},
				"reason":      map[string]any{"type": "string", "description": "为什么需要新增；至少 8 个字符"},
			}, "required": []string{"name", "transport", "reason"},
		}},
		{Name: "aha_request_mcp_server_update", Description: "提交修改外部 MCP 的 Owner 审批；只创建审批，不改配置。secret 值不进入审批，由 owner 或 Secret Store 保留。内置 MCP 拒绝修改。", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"server_id":   map[string]any{"type": "string"},
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"transport":   map[string]any{"type": "string", "enum": []string{"http", "stdio"}},
				"command":     map[string]any{"type": "string"},
				"args":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"url":         map[string]any{"type": "string"},
				"env_keys":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"header_keys": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"enabled":     map[string]any{"type": "boolean"},
				"reason":      map[string]any{"type": "string", "description": "为什么修改；至少 8 个字符"},
			}, "required": []string{"server_id", "reason"},
		}},
		{Name: "aha_request_mcp_server_delete", Description: "提交删除外部 MCP 的 Owner 审批；只创建审批，不改配置。内置 MCP 拒绝删除。", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"server_id": map[string]any{"type": "string"},
				"reason":    map[string]any{"type": "string", "description": "为什么删除；至少 8 个字符"},
			}, "required": []string{"server_id", "reason"},
		}},
		{Name: "pet_watch_task", Description: "关注一个 Task 的状态变化；完成/失败/阻塞/等待时会主动通知", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "pet_unwatch_task", Description: "取消关注一个 Task 的状态变化", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}}, "required": []string{"task_id"},
		}},
		{Name: "pet_submit_idea", Description: "提交一个想法审批；owner 同意后再实现，不自动执行或授权", Parameters: map[string]any{
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
		{Name: "pet_request_approval", Description: "向 owner 申请一次需要授权的操作；只创建审批，不执行操作；任务相关审批在 payload 中带 task_id", Parameters: map[string]any{
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

// IsPetTaskID reports whether taskID is the system Pet Task. The pet MCP
// endpoint authenticates every call against this identity; loopback alone is
// never treated as a caller.
func IsPetTaskID(taskID string) bool {
	return strings.TrimSpace(taskID) == petTaskID
}

// PetMCPTool dispatches the pet MCP surface without a caller identity. It is
// for in-process callers and tests; the HTTP endpoint must use
// PetMCPToolForTask so every tool verifies the agent capability belongs to the
// pet Task.
func (s *Service) PetMCPTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	return s.petMCPTool(ctx, "", name, args)
}

// PetMCPToolForTask dispatches one pet MCP tool on behalf of the authenticated
// taskID. The pet MCP client sends its Turn capability as a bearer token.
// Every tool, including the read-only ones, requires the pet Task's capability
// and is rejected before dispatch for any other caller: the endpoint is
// loopback-reachable by other local and tunnelled processes, so the source
// address is not an identity.
func (s *Service) PetMCPToolForTask(ctx context.Context, taskID, name string, args json.RawMessage) (string, error) {
	taskID = strings.TrimSpace(taskID)
	name = strings.TrimSpace(strings.ToLower(name))
	if taskID != petTaskID {
		// The rejection carries the stable code the MCP client and tests expect.
		return "", codedErrorf(
			ErrCodePetIdentityRequired, http.StatusForbidden,
			"只有携带宠物 Task capability 的调用可以访问宠物 MCP 工具",
		)
	}
	return s.petMCPTool(ctx, taskID, name, args)
}

func (s *Service) petMCPTool(ctx context.Context, callerTaskID, name string, args json.RawMessage) (string, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	if isAhaPetTool(name) {
		assistant, err := s.petAssistant(ctx)
		if err != nil {
			return "", err
		}
		if !petToolAllowed(assistant, name) {
			return "", codedErrorf(ErrCodePetPermissionDenied, http.StatusForbidden, "owner 未开启该工具权限")
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
		var input struct {
			HistoryLimit       *int `json:"history_limit"`
			IncludeTurnResults bool `json:"include_turn_results"`
			TurnResultChars    int  `json:"turn_result_chars"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &input); err != nil {
				return "", fmt.Errorf("invalid work state request")
			}
		}
		opts := defaultPetWorkStateOptions()
		if input.HistoryLimit != nil {
			opts.HistoryLimit = *input.HistoryLimit
		}
		opts.IncludeTurnResults = input.IncludeTurnResults
		opts.TurnResultChars = input.TurnResultChars
		state, err := s.PetWorkStateWithOptions(ctx, opts)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(state)
		return string(data), nil
	case "aha_ack_knowledge_sync_notification":
		// Notifications describe background sync state for the whole install,
		// so only a call carrying the pet Task's own Turn capability may clear
		// them. The HTTP endpoint is loopback-only but that alone does not
		// identify the caller.
		if callerTaskID != petTaskID {
			return "", fmt.Errorf("只有宠物 Task 可以处理知识库自动同步通知")
		}
		var input struct {
			IDs []string `json:"ids"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &input); err != nil {
				return "", fmt.Errorf("invalid notification acknowledgement request")
			}
		}
		acknowledged, err := s.AcknowledgeKnowledgeSyncNotifications(ctx, input.IDs)
		if err != nil {
			return "", err
		}
		remaining, _ := s.KnowledgeSyncNotifications(ctx)
		data, _ := json.Marshal(map[string]any{
			"ok": true, "acknowledged": acknowledged, "remaining": remaining,
			"note": "只标记通知已处理，不会恢复自动同步，也不解决 Git 分叉；确认分叉已解决后仍需手动 pull/push/sync。",
		})
		return string(data), nil
	case "aha_recover_knowledge_sync":
		// Recovery changes install-wide background sync state, so it is gated
		// exactly like the ack tool: only the pet Task's own Turn capability.
		if callerTaskID != petTaskID {
			return "", fmt.Errorf("只有宠物 Task 可以恢复知识库自动同步")
		}
		result, err := s.RecoverKnowledgeSyncAutoSync(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(result)
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
	case "aha_list_mcp_servers":
		view, err := s.PetListMCPServers(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_test_mcp_server":
		var input struct {
			ServerID string `json:"server_id"`
		}
		if err := decodePetMCPAdminJSON(args, &input); err != nil {
			return "", err
		}
		result, err := s.PetTestMCPServer(ctx, input.ServerID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(result)
		return string(data), nil
	case "aha_list_task_mcp_servers":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := decodePetMCPAdminJSON(args, &input); err != nil {
			return "", err
		}
		view, err := s.PetListTaskMCPServers(ctx, input.TaskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_set_task_mcp_servers":
		var input PetSetTaskMCPServersInput
		if err := decodePetMCPAdminJSON(args, &input); err != nil {
			return "", err
		}
		view, err := s.PetSetTaskMCPServers(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(view)
		return string(data), nil
	case "aha_request_mcp_server_create":
		var input PetMCPServerCreateInput
		if err := decodePetMCPAdminJSON(args, &input); err != nil {
			return "", err
		}
		approval, err := s.PetRequestMCPServerCreate(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_request_mcp_server_update":
		var input PetMCPServerUpdateInput
		if err := decodePetMCPAdminJSON(args, &input); err != nil {
			return "", err
		}
		approval, err := s.PetRequestMCPServerUpdate(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
		return string(data), nil
	case "aha_request_mcp_server_delete":
		var input PetMCPServerDeleteInput
		if err := decodePetMCPAdminJSON(args, &input); err != nil {
			return "", err
		}
		approval, err := s.PetRequestMCPServerDelete(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(approval)
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
			SkillID      string `json:"skill_id"`
			IncludeFiles bool   `json:"include_files"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid skill detail request")
		}
		detail, err := s.PetSkillDetailWithFiles(ctx, input.SkillID, input.IncludeFiles)
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
	case "aha_delete_models":
		var input struct {
			ModelIDs []string `json:"model_ids"`
			Reason   string   `json:"reason"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid delete models request")
		}
		approval, err := s.PetRequestDeleteModels(ctx, input.ModelIDs, input.Reason)
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
	case "aha_restore_previous_credential":
		var input struct {
			Target string `json:"target"`
			ID     string `json:"id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid restore previous credential request")
		}
		result, err := s.PetRestorePreviousCredential(ctx, input.Target, input.ID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(result)
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
	case "aha_copy_task_attachment":
		var input PetCopyAttachmentInput
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid copy attachment request")
		}
		item, err := s.PetCopyTaskAttachment(ctx, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(item)
		return string(data), nil
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
			TaskID         string `json:"task_id"`
			ReviewDecision string `json:"review_decision"`
			ReviewReason   string `json:"review_reason"`
			ReviewTaskID   string `json:"review_task_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid complete task request")
		}
		task, err := s.PetCompleteTaskWithDecision(ctx, input.TaskID, input.ReviewDecision, input.ReviewReason, input.ReviewTaskID)
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
		result, err := s.PetApplyTaskAgent(ctx, callerTaskID, input)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(struct {
			domain.TaskAgent
			OK      bool   `json:"ok"`
			Status  string `json:"status"`
			Changed bool   `json:"changed"`
		}{TaskAgent: result.Agent, OK: true, Status: result.Status, Changed: result.Changed})
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
		// MCP management kinds are reserved for the dedicated tools below. They
		// create server-side titles/summaries/risk and enforce manage_system;
		// letting the generic entry forge them would bypass both.
		if isPetMCPApprovalKind(input.Kind) {
			return "", fmt.Errorf("MCP 管理审批只允许通过专用工具 aha_request_mcp_server_* 或 aha_set_task_mcp_servers 提交")
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
		// Update approvals can contain command/args/URL fragments. The pet only
		// needs the decision and the server-side summary, so do not echo the
		// stored payload back. This also contains legacy approvals created
		// before update payloads were reduced to a field diff.
		for index := range items {
			if strings.EqualFold(strings.TrimSpace(items[index].Kind), petMCPServerUpdateKind) {
				items[index].Payload = "{}"
			}
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
	if result.NavigationID != "" {
		taskID := ""
		for _, action := range result.Actions {
			if action.Type == "open_task" {
				taskID = action.Task
				break
			}
		}
		data, _ := json.Marshal(map[string]any{
			"ok":            true,
			"status":        result.NavigationStatus,
			"navigation_id": result.NavigationID,
			"task_id":       taskID,
			"expires_at":    result.NavigationExpiresAt,
			"message":       "已排队：Web 前端连接后会打开一次，不要重复调用",
		})
		return string(data), nil
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
		{Name: "pet_open_task", Description: "在网页中打开一个 Task（只读导航）；task 可传 Task ID 或 task-编号，后台调用会排队到前端连接后只执行一次", Parameters: map[string]any{
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
