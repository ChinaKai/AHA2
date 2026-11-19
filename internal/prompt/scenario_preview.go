package prompt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// The scenario preview is a read-only rendering aid for the prompt admin page.
// It builds a complete Prompt Snapshot through the production builder using
// synthetic demo data; it never reads or writes Turn/Task state, never sends a
// channel message, and never calls a model.
const (
	DefaultScenarioTestMessage = "这是一条测试消息"
	// MaxScenarioTestMessageRunes keeps the GET query small enough for the
	// reverse proxy request line. The endpoint reports a clear error instead of
	// truncating an overlong message.
	MaxScenarioTestMessageRunes = 500
)

var (
	// ErrUnknownScenario is returned when the requested scenario ID is not one
	// of the stable preview shapes.
	ErrUnknownScenario = errors.New("unknown prompt preview scenario")
	// ErrScenarioMessageTooLong is returned when the test message exceeds the
	// query budget. No partial snapshot is ever returned.
	ErrScenarioMessageTooLong = errors.New("prompt preview message too long")
	// ErrInvalidMessageSource rejects a source other than owner/pet instead of
	// silently defaulting.
	ErrInvalidMessageSource = errors.New("invalid prompt preview message source")
)

// ScenarioSpec is the stable metadata exposed to the prompt admin page.
type ScenarioSpec struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	MessageSourceSwitch bool   `json:"message_source_switch"`
}

// ScenarioContextFile is a small layer summary of the rendered Context files.
// It lets the page show what the demo Turn would materialize without shipping
// the file bodies through the preview response.
type ScenarioContextFile struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Chars int    `json:"chars"`
}

// ScenarioPreview is the complete read-only demo snapshot.
type ScenarioPreview struct {
	Scenario      string                `json:"scenario"`
	ScenarioName  string                `json:"scenario_name"`
	Prompt        string                `json:"prompt"`
	PromptChars   int                   `json:"prompt_chars"`
	Demo          bool                  `json:"demo"`
	TestMessage   string                `json:"test_message"`
	MessageSource string                `json:"message_source"`
	ContextFiles  []ScenarioContextFile `json:"context_files"`
}

const (
	ScenarioWebTaskSingleMain              = "web-task-single-main"
	ScenarioWebTaskMultiMain               = "web-task-multi-main"
	ScenarioWebTaskMultiSub                = "web-task-multi-sub"
	ScenarioWebPetTaskSingleMain           = "web-pet-task-single-main"
	ScenarioChannelPrivateUnboundAssistant = "channel-private-unbound-assistant-main"
	ScenarioChannelPrivateTaskSingle       = "channel-private-task-single-task-agent"
	ScenarioChannelPrivateTaskMulti        = "channel-private-task-multi-main"
	ScenarioChannelGroupUnboundDigital     = "channel-group-unbound-digital-human-main"
	ScenarioChannelGroupTaskSingle         = "channel-group-task-single-task-agent"
	ScenarioChannelGroupTaskMulti          = "channel-group-task-multi-main"
)

var scenarioSpecs = []ScenarioSpec{
	{
		ID: ScenarioWebTaskSingleMain, Name: "Web 普通 Task，单 Agent，主 Agent",
		Description: "Web Owner 消息，普通 Task，单 Agent 主 Agent。", MessageSourceSwitch: true,
	},
	{
		ID: ScenarioWebTaskMultiMain, Name: "Web 普通 Task，多 Agent，主 Agent（自动协作）",
		Description: "Web Owner 消息，多 Agent 自动协作下的主 Agent。", MessageSourceSwitch: true,
	},
	{
		ID: ScenarioWebTaskMultiSub, Name: "Web 普通 Task，多 Agent，子 Agent（无协作策略段）",
		Description: "多 Agent 自动协作下的子 Agent；不包含协作策略段。", MessageSourceSwitch: true,
	},
	{
		ID: ScenarioWebPetTaskSingleMain, Name: "Web 宠物 Task，单 Agent，主 Agent",
		Description: "宠物 Task 的主 Agent，使用宠物身份模板。", MessageSourceSwitch: true,
	},
	{
		ID: ScenarioChannelPrivateUnboundAssistant, Name: "渠道私聊，未绑定 Task，渠道助手，单 Agent，主 Agent",
		Description: "渠道私聊未绑定 Task，渠道助手身份处理消息。",
	},
	{
		ID: ScenarioChannelPrivateTaskSingle, Name: "渠道私聊，已绑定 Task，单 Agent，任务 Agent",
		Description: "渠道私聊 task_route，单 Agent 任务 Agent。",
	},
	{
		ID: ScenarioChannelPrivateTaskMulti, Name: "渠道私聊，已绑定 Task，多 Agent，主 Agent",
		Description: "渠道私聊 task_route，多 Agent 自动协作主 Agent。",
	},
	{
		ID: ScenarioChannelGroupUnboundDigital, Name: "渠道群聊，未绑定 Task，群聊电子人，单 Agent，主 Agent，群聊规则",
		Description: "渠道群聊未绑定 Task，群聊电子人身份与群聊规则。",
	},
	{
		ID: ScenarioChannelGroupTaskSingle, Name: "渠道群聊，已绑定 Task，单 Agent，任务 Agent，群聊规则",
		Description: "渠道群聊 task_route，单 Agent 任务 Agent 与群聊规则。",
	},
	{
		ID: ScenarioChannelGroupTaskMulti, Name: "渠道群聊，已绑定 Task，多 Agent，主 Agent，群聊规则",
		Description: "渠道群聊 task_route，多 Agent 自动协作主 Agent 与群聊规则。",
	},
}

// ScenarioCatalog returns a copy so callers cannot mutate the stable list.
func ScenarioCatalog() []ScenarioSpec {
	return append([]ScenarioSpec(nil), scenarioSpecs...)
}

func scenarioSpecByID(id string) (ScenarioSpec, bool) {
	for _, spec := range scenarioSpecs {
		if spec.ID == id {
			return spec, true
		}
	}
	return ScenarioSpec{}, false
}

type scenarioShape struct {
	petTask       bool
	collaboration string
	agentRole     string
	agentID       string
	channel       string // "", "private", "group"
	channelBound  bool
}

func scenarioShapeFor(id string) (scenarioShape, bool) {
	switch id {
	case ScenarioWebTaskSingleMain:
		return scenarioShape{collaboration: "single", agentRole: "main", agentID: "main"}, true
	case ScenarioWebTaskMultiMain:
		return scenarioShape{collaboration: "auto", agentRole: "main", agentID: "main"}, true
	case ScenarioWebTaskMultiSub:
		return scenarioShape{collaboration: "auto", agentRole: "sub", agentID: "sub-001"}, true
	case ScenarioWebPetTaskSingleMain:
		return scenarioShape{petTask: true, collaboration: "single", agentRole: "main", agentID: "main"}, true
	case ScenarioChannelPrivateUnboundAssistant:
		return scenarioShape{channel: "private"}, true
	case ScenarioChannelPrivateTaskSingle:
		return scenarioShape{channel: "private", channelBound: true, collaboration: "single", agentRole: "main", agentID: "main"}, true
	case ScenarioChannelPrivateTaskMulti:
		return scenarioShape{channel: "private", channelBound: true, collaboration: "auto", agentRole: "main", agentID: "main"}, true
	case ScenarioChannelGroupUnboundDigital:
		return scenarioShape{channel: "group"}, true
	case ScenarioChannelGroupTaskSingle:
		return scenarioShape{channel: "group", channelBound: true, collaboration: "single", agentRole: "main", agentID: "main"}, true
	case ScenarioChannelGroupTaskMulti:
		return scenarioShape{channel: "group", channelBound: true, collaboration: "auto", agentRole: "main", agentID: "main"}, true
	default:
		return scenarioShape{}, false
	}
}

// PreviewScenario renders one complete demo Prompt Snapshot through Build.
// The caller may pass an empty message to use the documented default. Unknown
// scenarios and overlong messages fail before any rendering so no partial
// snapshot can leak into the response.
func (engine *Engine) PreviewScenario(ctx context.Context, scenarioID, message, source, language string) (ScenarioPreview, error) {
	spec, ok := scenarioSpecByID(strings.TrimSpace(scenarioID))
	if !ok {
		return ScenarioPreview{}, fmt.Errorf("%w: %s", ErrUnknownScenario, scenarioID)
	}
	shape, _ := scenarioShapeFor(spec.ID)
	message = strings.TrimSpace(message)
	if message == "" {
		message = DefaultScenarioTestMessage
	}
	if len([]rune(message)) > MaxScenarioTestMessageRunes {
		return ScenarioPreview{}, fmt.Errorf("%w: max %d runes", ErrScenarioMessageTooLong, MaxScenarioTestMessageRunes)
	}
	source = strings.TrimSpace(strings.ToLower(source))
	if source == "" {
		source = "owner"
	}
	if source != "owner" && source != "pet" {
		return ScenarioPreview{}, fmt.Errorf("%w: %s", ErrInvalidMessageSource, source)
	}

	input, effectiveSource, err := engine.scenarioBuildInput(ctx, shape, message, source, language)
	if err != nil {
		return ScenarioPreview{}, err
	}
	result, err := engine.Build(ctx, input)
	if err != nil {
		return ScenarioPreview{}, err
	}
	files := make([]ScenarioContextFile, 0, len(result.ContextManifest)+len(result.SharedManifest))
	for _, item := range append(append([]ContextResource(nil), result.ContextManifest...), result.SharedManifest...) {
		files = append(files, ScenarioContextFile{ID: item.ID, Path: item.Path, Chars: item.Chars})
	}
	return ScenarioPreview{
		Scenario:      spec.ID,
		ScenarioName:  spec.Name,
		Prompt:        result.EffectivePrompt,
		PromptChars:   len([]rune(result.EffectivePrompt)),
		Demo:          true,
		TestMessage:   message,
		MessageSource: effectiveSource,
		ContextFiles:  files,
	}, nil
}

func (engine *Engine) scenarioBuildInput(
	ctx context.Context,
	shape scenarioShape,
	message, source, language string,
) (BuildInput, string, error) {
	now := time.Date(2026, 11, 19, 19, 0, 0, 0, time.Local)
	taskID := "task-demo"
	taskCode := "task-901"
	taskTitle := "演示任务：修复登录超时"
	originalRequest := "演示用的历史请求：定位并修复登录偶发超时，保留现有行为。"
	currentGoal := "定位登录超时的根因，给出可验证的最小修复，并报告测试结果。"
	projectName := "AHA2 演示项目"
	workspaceName := "AHA2 演示工作区"
	if shape.petTask {
		taskID = "task_pet"
		taskCode = "task-pet"
		taskTitle = "宠物工作台：整理本周项目进度"
	}
	if shape.channel != "" && !shape.channelBound {
		// The channel host Task owns the conversation when no business Task is
		// routed; it is still a Task, not an unbound prompt shape.
		taskCode = "task-channel-host"
		taskTitle = "渠道会话宿主任务"
		originalRequest = "渠道会话未绑定业务 Task，由宿主 Task 接收并处理渠道消息。"
		currentGoal = "以当前渠道身份回复这条消息，并保持渠道边界。"
		projectName = "AHA2 渠道宿主项目"
		workspaceName = "AHA2 渠道宿主工作区"
	}
	collaboration := shape.collaboration
	if collaboration == "" {
		collaboration = "single"
	}
	maxAgents := 1
	if collaboration == "auto" {
		maxAgents = 3
	}
	project := domain.Project{ID: "project-demo", Name: projectName, Description: "用于场景预览的合成项目。"}
	workspace := domain.Workspace{
		ID: "workspace-demo", ProjectID: project.ID, Name: workspaceName,
		Locality: "local", Transport: "native", RootPath: "/demo/aha2", Health: "ok",
	}
	task := domain.Task{
		ID: taskID, Code: taskCode, ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: taskTitle, OriginalRequest: originalRequest,
		CurrentGoal: currentGoal,
		Status:      domain.TaskActive, Isolation: "worktree", TaskWorkspacePath: workspace.RootPath,
		TaskBranch: "aha/task-demo", CollaborationMode: collaboration, MaxAgents: maxAgents,
		KnowledgePolicy: "project",
	}
	agent := domain.TaskAgent{
		TaskID: task.ID, AgentID: shape.agentID, Role: shape.agentRole, Status: "active",
		Title: "Main Agent",
	}
	if shape.agentRole == "sub" {
		agent.Title = "子 Agent"
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID: "snapshot-demo", WorkspaceID: workspace.ID, Backend: "codex",
		ModelID: "gpt-5-codex", WireModel: "gpt-5-codex", EnvGroupRevision: 1,
	}

	channelContext := demoChannelContext(shape, task.ID)

	item := InboxTemplateItem{
		Sequence: 1, SourceKind: "owner_message", SourceAgentID: "owner",
		CreatedAt: FormatPromptTime(now), Content: message,
	}
	effectiveSource := source
	switch {
	case shape.channel == "private":
		item.SourceKind = "channel_message"
		item.SourceAgentID = "Owner"
		item.ChannelSender = "Owner"
		item.ChannelSenderRole = "owner"
		item.ChannelSenderIsOwner = true
		item.ChannelReceiptID = "channel_inbox_demo_private"
		item.ChannelConversation = "与 Owner 的私聊"
		effectiveSource = "channel"
	case shape.channel == "group":
		item.SourceKind = "channel_message"
		item.SourceAgentID = "张三"
		item.ChannelSender = "张三"
		item.ChannelSenderRole = "participant"
		item.ChannelSenderIsOwner = false
		item.ChannelReceiptID = "channel_inbox_demo_group"
		item.ChannelConversation = "AHA2 技术群"
		item.MentionedParticipants = "AHA"
		effectiveSource = "channel"
	case source == "pet":
		item.SourceKind = "pet_delegate_message"
		item.SourceAgentID = "pet"
	}
	templates, err := engine.TemplatesForLanguage(ctx, language)
	if err != nil {
		return BuildInput{}, "", err
	}
	userMessage, err := RenderInboxBatchContent(templates, []InboxTemplateItem{item})
	if err != nil {
		return BuildInput{}, "", err
	}

	currentRoundID := "round-demo-current"
	currentTurnID := "turn-demo-current"
	conversation := []domain.ConversationItem{
		{
			Sequence: 1, ID: "conversation-demo-1", TaskID: task.ID, RoundID: "round-demo-1",
			AgentID: "main", FromAgentID: "owner", RouteKind: "owner_message",
			Category: "chat", Kind: "user_message", Summary: "请先定位登录超时问题。",
			CreatedAt: now.Add(-10 * time.Minute),
		},
		{
			Sequence: 2, ID: "conversation-demo-2", TaskID: task.ID, RoundID: "round-demo-1",
			AgentID: "main", FromAgentID: "main", RouteKind: "turn_result",
			Category: "chat", Kind: "agent_message", Summary: "已定位到令牌刷新串行化导致的等待，准备修改。",
			CreatedAt: now.Add(-9 * time.Minute),
		},
	}
	identity := ""
	identitySupplement := ""
	if shape.petTask {
		identity = "pet-task"
		identitySupplement = demoPetCoreDigest(language, taskID)
	}
	input := BuildInput{
		Project: project, Workspace: workspace, Task: task, Agent: agent, Snapshot: snapshot,
		ProductLine:  domain.ProductLine{ID: "product-line-demo", ProjectID: project.ID, Name: "Main", Default: true},
		Conversation: conversation, CurrentRoundID: currentRoundID, CurrentTurnID: currentTurnID,
		IncludeRecentContext: true, UserMessage: userMessage,
		ChannelContext: channelContext, Identity: identity,
		IdentitySupplement: identitySupplement,
		AgentAPIURL:        "http://127.0.0.1:8766", Language: language,
	}
	return input, effectiveSource, nil
}

func demoChannelContext(shape scenarioShape, taskID string) map[string]any {
	if shape.channel == "" {
		return nil
	}
	context := map[string]any{
		"schema": "aha.channel-context/v1", "instance_id": "channel-instance-demo",
		"provider": "feishu",
	}
	route := map[string]any{}
	if shape.channel == "private" {
		context["endpoint"] = domain.ChannelEndpointAssistantDM
		context["conversation_id"] = "conversation-private-demo"
		context["chat_display_name"] = "与 Owner 的私聊"
		context["inbound_receipt_id"] = "channel_inbox_demo_private"
		context["actor"] = map[string]any{
			"display_name": "Owner", "role": "owner", "is_owner": true, "is_bot": false,
		}
		if shape.channelBound {
			route["mode"] = "task_route"
		} else {
			route["mode"] = "conversation_host"
		}
	} else {
		context["endpoint"] = domain.ChannelEndpointGroupDigitalHuman
		context["conversation_id"] = "conversation-group-demo"
		context["chat_display_name"] = "AHA2 技术群"
		context["inbound_receipt_id"] = "channel_inbox_demo_group"
		context["actor"] = map[string]any{
			"display_name": "张三", "role": "participant", "is_owner": false, "is_bot": false,
		}
		context["mentions"] = []any{map[string]any{"display_name": "AHA"}}
		if shape.channelBound {
			route["mode"] = "task_route"
		} else {
			route["mode"] = "conversation_host"
		}
	}
	if shape.channelBound {
		route["target_task_id"] = taskID
	}
	context["route"] = route
	return context
}

// demoPetCoreDigest mirrors the runtime supplement's shape so the pet scenario
// shows where the real core digest is injected. The content is explicitly
// demo data; the real Turn reads the owner's core files.
func demoPetCoreDigest(language, taskID string) string {
	root := "/demo/aha2/.aha2-context/" + taskID + "/core"
	if domain.NormalizeLanguage(language) == domain.LanguageEnglish {
		return "Core digest (core_version demo), generated from the core files at runtime. It is only a summary: when it is marked truncated or a rule is unclear, read the full file below; the files win over this digest.\n" +
			"Identity:\nDemo pet identity summary.\n" +
			"Principles:\nDemo pet principles summary.\n" +
			"Open loops: 0 open, first 0:\n" +
			"Agenda: 0 next/waiting, first 0:\n" +
			"Full files:\n- identity: " + root + "/identity.md\n- principles: " + root + "/principles.md\n- loops: " + root + "/loops.md\n- agenda: " + root + "/agenda.md"
	}
	return "核心摘要（core_version demo）：运行时从 core 文件生成，只是摘要；标记截断或规则不清时读取下列全文，以文件为准。\n" +
		"身份：\n演示用宠物身份摘要。\n" +
		"做事准则：\n演示用宠物准则摘要。\n" +
		"开放闭环：未闭环 0 条，前 0 条：\n" +
		"自主议程：next/waiting 0 条，前 0 条：\n" +
		"全文：\n- identity: " + root + "/identity.md\n- principles: " + root + "/principles.md\n- loops: " + root + "/loops.md\n- agenda: " + root + "/agenda.md"
}
