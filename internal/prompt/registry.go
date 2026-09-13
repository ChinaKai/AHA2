package prompt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

//go:embed templates/*.md
var templateFiles embed.FS

type Repository interface {
	PromptTemplateOverrides(context.Context) (map[string]domain.PromptTemplate, error)
	UpsertPromptTemplateOverride(context.Context, string, string, time.Time) error
	DeletePromptTemplateOverride(context.Context, string) error
}

type Engine struct {
	repository Repository
}

const (
	identityTaskAgent           = "task-agent"
	identityChannelAssistant    = "channel-assistant"
	identityChannelDigitalHuman = "channel-digital-human"
	channelWeb                  = "web"
	channelExternal             = "external-channel"
)

type BuildInput struct {
	Project                domain.Project
	Workspace              domain.Workspace
	Task                   domain.Task
	Agent                  domain.TaskAgent
	Snapshot               domain.RuntimeConfigSnapshot
	GlobalKnowledge        []domain.KnowledgeEntry
	ProjectKnowledge       []domain.KnowledgeEntry
	StaleKnowledge         []domain.KnowledgeEntry
	Skills                 []domain.Skill
	ProductLine            domain.ProductLine
	KnowledgeEnabled       bool
	Conversation           []domain.ConversationItem
	RecoveryConversation   []domain.ConversationItem
	Turns                  []domain.Turn
	Hardware               []domain.HardwareGroup
	Attachments            []AttachmentResource
	AgentAPIURL            string
	UserMessage            string
	Handoff                string
	CurrentTurnID          string
	CurrentRoundID         string
	IncludeRecentContext   bool
	IncludeRecoveryHandoff bool
	IncludeTurnDiagnostics bool
	ChannelContext         map[string]any
}

type AttachmentResource struct {
	Attachment domain.Attachment
	Content    string
}

type ContextResource struct {
	ID          string `json:"id"`
	URI         string `json:"uri"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Chars       int    `json:"chars"`
	Content     string `json:"content,omitempty"`
	EntryPoint  bool   `json:"entry_point,omitempty"`
}

type BuildResult struct {
	EffectivePrompt string
	ContextRoot     string
	ContextManifest []ContextResource
	SharedRoot      string
	SharedManifest  []ContextResource
}

type recentContextUser struct {
	Sender  string
	Summary string
}

type recentContextExchange struct {
	Number int
	Users  []recentContextUser
	Reply  string
}

type recoveryHandoffEvidence struct {
	PreviousTurnID       string
	PreviousTurnSequence int
	AgentID              string
	Status               string
	Attempt              int
	Generation           int
	Error                string
	LatestProgress       string
	LatestToolSummary    string
	LatestToolState      string
	LatestToolExitCode   string
}

type turnDiagnostic struct {
	Sequence   int
	AgentID    string
	Status     string
	Attempt    int
	Generation int
	Body       string
}

type hardwareContextGroup struct {
	ID                 string
	Description        string
	Access             string
	Mode               string
	HasSerial          bool
	SerialDevice       string
	SerialBaudrate     int
	HasNetwork         bool
	NetworkHost        string
	NetworkPort        int
	NetworkProtocol    string
	SSHAuthentication  string
	Username           string
	PasswordConfigured bool
}

type attachmentIndexEntry struct {
	ID           string
	Name         string
	RelativePath string
	MediaType    string
	Size         int64
}

type InboxTemplateItem struct {
	Sequence              int64
	SourceKind            string
	SourceAgentID         string
	ChannelSender         string
	ChannelConversation   string
	MentionedParticipants string
	Content               string
	Attachments           []domain.Attachment
}

type templateData struct {
	AgentID            string
	AgentRole          string
	Identity           string
	Channel            string
	Backend            string
	Collaboration      string
	MaxAgents          int
	TaskID             string
	TaskCode           string
	TaskTitle          string
	ContextRoot        string
	TaskWorkspace      string
	Workspace          string
	WorkspaceTransport string
	ProjectName        string
	OriginalRequest    string
	CurrentGoal        string
	CurrentGoalSummary string
	TaskBranch         string
	InboxBatch         string
	CompactHandoff     string
	RecoveryHandoff    string
	Recovery           *recoveryHandoffEvidence
	AgentAPIURL        string
	AvailableContext   []ContextResource
	RecentExchanges    []recentContextExchange
	TurnDiagnostics    []turnDiagnostic
	HardwareGroups     []hardwareContextGroup
	AttachmentEntries  []attachmentIndexEntry
	InboxItems         []InboxTemplateItem
}

func NewEngine(repository Repository) *Engine {
	return &Engine{repository: repository}
}

var builtinTemplates = []domain.PromptTemplate{
	{ID: "core.default", Name: "AHA Core", Layer: "core", Description: "所有 Agent 共用的安全、Workspace 与 AHA 编排边界", Editable: true, Required: true, Version: 1},
	{ID: "role.main", Name: "Main Agent", Layer: "role", Description: "Main 的规划、整合和最终交付职责", Editable: true, Required: true, Version: 1},
	{ID: "role.sub", Name: "Sub Agent", Layer: "role", Description: "Sub 的聚焦执行和隔离职责", Editable: true, Required: true, Version: 1},
	{ID: "identity.task-agent", Name: "Task Agent Identity", Layer: "identity", Description: "代码与项目任务场景身份", Editable: true, Required: false, Version: 1},
	{ID: "identity.channel-assistant", Name: "Channel Assistant Identity", Layer: "identity", Description: "Owner 私聊渠道助手身份", Editable: true, Required: false, Version: 1},
	{ID: "identity.channel-digital-human", Name: "Channel Digital Human Identity", Layer: "identity", Description: "受限群聊电子人身份", Editable: true, Required: false, Version: 1},
	{ID: "channel.web", Name: "AHA Web Channel", Layer: "channel", Description: "Web 渠道消息行为", Editable: true, Required: false, Version: 1},
	{ID: "channel.external-channel", Name: "External Channel", Layer: "channel", Description: "外部渠道消息行为", Editable: true, Required: false, Version: 1},
	{ID: "policy.auto", Name: "Auto Collaboration", Layer: "policy", Description: "AHA 自动协作策略", Editable: true, Required: false, Version: 1},
	{ID: "policy.single", Name: "Single Agent", Layer: "policy", Description: "单 Agent 策略", Editable: true, Required: false, Version: 1},
	{ID: "protocol.knowledge", Name: "Knowledge Protocol", Layer: "protocol", Description: "按 index 渐进读取知识并形成反馈与修订闭环", Editable: true, Required: false, Version: 2},
	{ID: "protocol.agent-api", Name: "Agent Control API Protocol", Layer: "protocol", Description: "Agent API 的权限、进度与输出边界", Editable: true, Required: true, Version: 3},
	{ID: "protocol.attachment-delivery", Name: "Attachment Delivery Protocol", Layer: "protocol", Description: "所有 Task 必须遵守的附件上传、绑定与回执边界", Editable: false, Required: true, Version: 1},
	{ID: "context.task", Name: "Task Context File", Layer: "context", Description: "task.md 的内容模板", Editable: true, Required: true, Version: 1},
	{ID: "context.recent-context", Name: "Recent Context File", Layer: "context", Description: "recent-context.md 的内容模板", Editable: true, Required: false, Version: 1},
	{ID: "context.recovery-handoff", Name: "Recovery Handoff Inline", Layer: "context", Description: "Current Inbox Batch 开头的中断续接证据模板", Editable: true, Required: false, Version: 2},
	{ID: "context.turn-diagnostics", Name: "Turn Diagnostics File", Layer: "context", Description: "diagnostics/turns.md 的内容模板", Editable: true, Required: false, Version: 1},
	{ID: "context.hardware", Name: "Hardware Context File", Layer: "context", Description: "hardware.md 的非敏感硬件上下文模板", Editable: true, Required: false, Version: 1},
	{ID: "context.attachments-index", Name: "Attachments Index File", Layer: "context", Description: "attachments/index.md 的内容模板", Editable: true, Required: false, Version: 1},
	{ID: "section.compact-handoff", Name: "Compact Handoff Section", Layer: "section", Description: "Backend Session 压缩后的 Prompt 段落", Editable: true, Required: false, Version: 1},
	{ID: "section.task-workspace", Name: "Task And Workspace Section", Layer: "section", Description: "Task 与 Workspace 摘要段落", Editable: true, Required: true, Version: 1},
	{ID: "section.available-context", Name: "Available Context Section", Layer: "section", Description: "可用上下文入口列表段落", Editable: true, Required: true, Version: 1},
	{ID: "section.current-inbox", Name: "Current Inbox Section", Layer: "section", Description: "当前 Inbox Batch 段落", Editable: true, Required: true, Version: 1},
	{ID: "section.inbox-batch-content", Name: "Inbox Batch Content", Layer: "section", Description: "Inbox 消息、渠道来源和附件引用的内容模板", Editable: true, Required: true, Version: 1},
	{ID: "resource.agent-api", Name: "Agent API Reference File", Layer: "resource", Description: "agent-api.md 的只读参考模板", Editable: false, Required: true, Version: 1},
}

var supersededBuiltinOverrideHashes = map[string]map[string]bool{
	"core.default": {
		"920d894f754ef5fee568b02244dce076e63c79323f5c04c03b3524e44f399c3b": true,
		"a347ba83ec16dd0e946bbd8ad3350841d56187fd01251d61fe8bc0c848bdfadd": true,
	},
	"role.main": {
		"497fffe9552e6978f283985916e9563ec59b8a3a080f2759ef5da56731a489a0": true,
		"d4cc743eaf8270e21634508f17e47ee240924c3bb57426a7c8a126b7af64cd3d": true,
	},
	"role.sub": {
		"388229947d72bbd80f97bd93a316e88bf8425b13a0d1845b66089c54d20a0d5f": true,
		"05cfc5eb0815fde5b4ce67beea783dc3621faf239dd2817fa06d3652421af14c": true,
	},
}

func (engine *Engine) Templates(ctx context.Context) ([]domain.PromptTemplate, error) {
	overrides, err := engine.repository.PromptTemplateOverrides(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.PromptTemplate, 0, len(builtinTemplates))
	for _, item := range builtinTemplates {
		if item.Content == "" {
			data, readErr := templateFiles.ReadFile("templates/" + strings.ReplaceAll(item.ID, ".", "-") + ".md")
			if readErr != nil {
				return nil, readErr
			}
			item.Content = strings.TrimSpace(string(data))
		}
		item.Source = "builtin"
		if override, ok := overrides[item.ID]; ok && item.Editable && !isSupersededBuiltinOverride(item.ID, override.Content) {
			item.Content = override.Content
			item.Version = override.Version + 1
			item.UpdatedAt = override.UpdatedAt
			item.Source = "override"
		}
		result = append(result, item)
	}
	return result, nil
}

func isSupersededBuiltinOverride(id, content string) bool {
	expected, ok := supersededBuiltinOverrideHashes[id]
	if !ok {
		return false
	}
	actual := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(content))))
	return expected[actual]
}

func (engine *Engine) UpdateTemplate(ctx context.Context, id, content string, now time.Time) error {
	item, ok := builtinTemplate(id)
	if !ok {
		return fmt.Errorf("prompt template not found")
	}
	if !item.Editable {
		return fmt.Errorf("prompt template is read-only")
	}
	content = strings.TrimSpace(content)
	if content == "" || len([]rune(content)) > 100000 {
		return fmt.Errorf("prompt template content must contain 1 to 100000 characters")
	}
	parsed, err := template.New(id).Option("missingkey=error").Parse(content)
	if err != nil {
		return fmt.Errorf("invalid prompt template: %w", err)
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, templateData{}); err != nil {
		return fmt.Errorf("invalid prompt template data reference: %w", err)
	}
	return engine.repository.UpsertPromptTemplateOverride(ctx, id, content, now)
}

func (engine *Engine) ResetTemplate(ctx context.Context, id string) error {
	if _, ok := builtinTemplate(id); !ok {
		return fmt.Errorf("prompt template not found")
	}
	return engine.repository.DeletePromptTemplateOverride(ctx, id)
}

func (engine *Engine) RenderInboxBatch(ctx context.Context, items []InboxTemplateItem) (string, error) {
	templates, err := engine.Templates(ctx)
	if err != nil {
		return "", err
	}
	templateByID := make(map[string]domain.PromptTemplate, len(templates))
	for _, item := range templates {
		templateByID[item.ID] = item
	}
	return renderTemplateByID(templateByID, "section.inbox-batch-content", templateData{InboxItems: items})
}

func (engine *Engine) Build(ctx context.Context, input BuildInput) (BuildResult, error) {
	templates, err := engine.Templates(ctx)
	if err != nil {
		return BuildResult{}, err
	}
	templateByID := make(map[string]domain.PromptTemplate, len(templates))
	for _, item := range templates {
		templateByID[item.ID] = item
	}
	workDir := input.Task.TaskWorkspacePath
	if workDir == "" {
		workDir = input.Workspace.RootPath
	}
	contextRoot := contextRootFor(input, workDir)
	identityName, channelName := identityAndChannel(input.ChannelContext)
	identityTemplate := "identity." + identityName
	channelTemplate := "channel." + channelName
	data := templateData{
		AgentID: input.Agent.AgentID, AgentRole: input.Agent.Role, Identity: identityName,
		Channel: channelName, Backend: input.Snapshot.Backend, Collaboration: input.Task.CollaborationMode,
		MaxAgents: input.Task.MaxAgents, TaskID: input.Task.ID, TaskCode: input.Task.Code,
		TaskTitle: input.Task.Title, ContextRoot: contextRoot, TaskWorkspace: workDir,
		Workspace: input.Workspace.Name, WorkspaceTransport: input.Workspace.Transport,
		ProjectName: input.Project.Name, OriginalRequest: input.Task.OriginalRequest,
		CurrentGoal: input.Task.CurrentGoal, CurrentGoalSummary: truncate(input.Task.CurrentGoal, 800),
		TaskBranch: input.Task.TaskBranch, InboxBatch: strings.TrimSpace(input.UserMessage),
		CompactHandoff: strings.TrimSpace(input.Handoff), AgentAPIURL: strings.TrimRight(input.AgentAPIURL, "/"),
	}
	if input.IncludeRecoveryHandoff {
		if evidence := recoveryHandoffEvidenceFor(input); evidence != nil {
			recoveryData := data
			recoveryData.Recovery = evidence
			recovery, renderErr := renderTemplateByID(templateByID, "context.recovery-handoff", recoveryData)
			if renderErr != nil {
				return BuildResult{}, renderErr
			}
			data.RecoveryHandoff = recovery
		}
	}
	contextResources, err := buildResources(input, contextRoot, templateByID, data)
	if err != nil {
		return BuildResult{}, err
	}
	sharedRoot, sharedManifest, err := buildSharedSnapshot(input, workDir, templateByID, data)
	if err != nil {
		return BuildResult{}, err
	}
	resources := append(append([]ContextResource(nil), contextResources...), sharedManifest...)
	for _, item := range resources {
		if item.EntryPoint {
			data.AvailableContext = append(data.AvailableContext, item)
		}
	}
	templateIDs := []string{"core.default", identityTemplate}
	if input.Agent.Role == "sub" {
		templateIDs = append(templateIDs, "role.sub")
	} else {
		templateIDs = append(templateIDs, "role.main")
	}
	templateIDs = append(templateIDs, channelTemplate)
	if input.Agent.Role == "main" {
		if input.Task.CollaborationMode == "single" {
			templateIDs = append(templateIDs, "policy.single")
		} else {
			templateIDs = append(templateIDs, "policy.auto")
		}
	}
	if input.KnowledgeEnabled {
		templateIDs = append(templateIDs, "protocol.knowledge")
	}
	var parts []string
	for _, id := range templateIDs {
		content, renderErr := renderTemplateByID(templateByID, id, data)
		if renderErr != nil {
			return BuildResult{}, renderErr
		}
		item := templateByID[id]
		parts = append(parts, "## "+item.Name+"\n"+content)
	}
	if data.CompactHandoff != "" {
		content, renderErr := renderTemplateByID(templateByID, "section.compact-handoff", data)
		if renderErr != nil {
			return BuildResult{}, renderErr
		}
		parts = append(parts, content)
	}
	for _, id := range []string{"section.task-workspace", "section.available-context", "section.current-inbox"} {
		content, renderErr := renderTemplateByID(templateByID, id, data)
		if renderErr != nil {
			return BuildResult{}, renderErr
		}
		parts = append(parts, content)
	}
	for _, id := range []string{"protocol.agent-api", "protocol.attachment-delivery"} {
		content, renderErr := renderTemplateByID(templateByID, id, data)
		if renderErr != nil {
			return BuildResult{}, renderErr
		}
		item := templateByID[id]
		parts = append(parts, "## "+item.Name+"\n"+content)
	}
	effective := strings.Join(parts, "\n\n")
	return BuildResult{
		EffectivePrompt: effective, ContextRoot: contextRoot, ContextManifest: contextResources,
		SharedRoot: sharedRoot, SharedManifest: sharedManifest,
	}, nil
}

func renderTemplateByID(templates map[string]domain.PromptTemplate, id string, data templateData) (string, error) {
	item, ok := templates[id]
	if !ok {
		return "", fmt.Errorf("prompt template %s is not registered", id)
	}
	return renderTemplate(item.ID, item.Content, data)
}

// BackendSessionContext returns the prompt identity/channel boundary for Backend
// Session reuse. A task_route is still a Task Agent, but remains distinct from a
// Web turn because the external-channel delivery rules also form part of the
// session's system prompt.
func BackendSessionContext(channelContext map[string]any) string {
	identity, channel := identityAndChannel(channelContext)
	return identity + ":" + channel
}

func identityAndChannel(channelContext map[string]any) (string, string) {
	endpoint := contextString(channelContext, "endpoint")
	if endpoint == "" {
		return identityTaskAgent, channelWeb
	}
	if channelRouteMode(channelContext) == "task_route" {
		return identityTaskAgent, channelExternal
	}
	if endpoint == domain.ChannelEndpointGroupDigitalHuman {
		return identityChannelDigitalHuman, channelExternal
	}
	return identityChannelAssistant, channelExternal
}

func channelRouteMode(channelContext map[string]any) string {
	route, _ := channelContext["route"].(map[string]any)
	return contextString(route, "mode")
}

func contextString(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func builtinTemplate(id string) (domain.PromptTemplate, bool) {
	for _, item := range builtinTemplates {
		if item.ID == id {
			return item, true
		}
	}
	return domain.PromptTemplate{}, false
}

func renderTemplate(id, content string, data templateData) (string, error) {
	value, err := template.New(id).Option("missingkey=error").Parse(content)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := value.Execute(&output, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(output.String()), nil
}

func safeAgentID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "main"
	}
	return strings.NewReplacer("/", "-", "\\", "-", "..", "-").Replace(value)
}

func contextRootFor(input BuildInput, workDir string) string {
	if input.Workspace.Transport == "native" {
		return filepath.Join(workDir, ".aha2-context", input.Task.ID, safeAgentID(input.Agent.AgentID))
	}
	return joinRemoteContextPath(workDir, ".aha2-context", input.Task.ID, safeAgentID(input.Agent.AgentID))
}

func truncate(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit-1]) + "..."
}

func buildResources(
	input BuildInput,
	root string,
	templates map[string]domain.PromptTemplate,
	data templateData,
) ([]ContextResource, error) {
	taskContent, err := renderTemplateByID(templates, "context.task", data)
	if err != nil {
		return nil, err
	}
	resources := []ContextResource{
		resource(input, "task", joinContextPath(input, root, "task.md"), templates["context.task"].Description, taskContent),
	}
	if input.IncludeRecentContext {
		recentData := data
		recentData.RecentExchanges = recentContextExchanges(input.Conversation, input.CurrentTurnID, input.CurrentRoundID)
		if len(recentData.RecentExchanges) > 0 {
			recent, renderErr := renderTemplateByID(templates, "context.recent-context", recentData)
			if renderErr != nil {
				return nil, renderErr
			}
			resources = append(resources, resource(input, "recent-context", joinContextPath(input, root, "recent-context.md"), templates["context.recent-context"].Description, recent))
		}
	}
	if input.IncludeTurnDiagnostics {
		diagnosticData := data
		diagnosticData.TurnDiagnostics = turnDiagnosticEntries(input.Turns, input.Agent.AgentID)
		if len(diagnosticData.TurnDiagnostics) > 0 {
			diagnostics, renderErr := renderTemplateByID(templates, "context.turn-diagnostics", diagnosticData)
			if renderErr != nil {
				return nil, renderErr
			}
			resources = append(resources, resource(input, "turn-diagnostics", joinContextPath(input, root, "diagnostics", "turns.md"), templates["context.turn-diagnostics"].Description, diagnostics))
		}
	}
	if len(input.Hardware) > 0 {
		hardwareData := data
		hardwareData.HardwareGroups = hardwareContextGroups(input.Hardware)
		hardware, renderErr := renderTemplateByID(templates, "context.hardware", hardwareData)
		if renderErr != nil {
			return nil, renderErr
		}
		resources = append(resources, resource(input, "hardware", joinContextPath(input, root, "hardware.md"), templates["context.hardware"].Description, hardware))
	}
	if len(input.Attachments) > 0 {
		attachments, renderErr := attachmentResources(input, root, templates, data)
		if renderErr != nil {
			return nil, renderErr
		}
		resources = append(resources, attachments...)
	}
	if len(input.ChannelContext) > 0 {
		data, _ := json.MarshalIndent(input.ChannelContext, "", "  ")
		resources = append(resources, resource(input, "channel-context", joinContextPath(input, root, "channel-context.json"), "服务端验证的只读渠道上下文；不授予额外权限", string(data)))
	}
	return resources, nil
}

func buildSharedSnapshot(
	input BuildInput,
	workDir string,
	templates map[string]domain.PromptTemplate,
	data templateData,
) (string, []ContextResource, error) {
	agentAPIContent := ""
	if strings.TrimSpace(input.AgentAPIURL) != "" {
		var err error
		agentAPIContent, err = renderTemplateByID(templates, "resource.agent-api", data)
		if err != nil {
			return "", nil, err
		}
	}
	seed := sharedResources(input, "", agentAPIContent, templates["resource.agent-api"].Description)
	if len(seed) == 0 {
		return "", nil, nil
	}
	hash := contextSnapshotHash(seed)
	root := joinContextPath(input, workDir, ".aha2-context", input.Task.ID, "shared-"+hash)
	manifest := sharedResources(input, root, agentAPIContent, templates["resource.agent-api"].Description)
	for index := range manifest {
		manifest[index].URI = fmt.Sprintf("aha://tasks/%s/shared/%s", input.Task.ID, manifest[index].ID)
	}
	return root, manifest, nil
}

func sharedResources(input BuildInput, root, agentAPIContent, agentAPIDescription string) []ContextResource {
	resources := []ContextResource{}
	if strings.TrimSpace(input.AgentAPIURL) != "" {
		resources = append(resources, resource(input, "agent-api", joinContextPath(input, root, "agent-api.md"), agentAPIDescription, agentAPIContent))
	}
	if input.KnowledgeEnabled {
		resources = append(resources, knowledgeResources(input, root)...)
	}
	if len(input.Skills) > 0 {
		resources = append(resources, skillResources(input, root)...)
	}
	return resources
}

func contextSnapshotHash(resources []ContextResource) string {
	resources = append([]ContextResource(nil), resources...)
	sort.Slice(resources, func(left, right int) bool {
		leftPath := strings.ReplaceAll(resources[left].Path, "\\", "/")
		rightPath := strings.ReplaceAll(resources[right].Path, "\\", "/")
		if leftPath != rightPath {
			return leftPath < rightPath
		}
		return resources[left].ID < resources[right].ID
	})
	hash := sha256.New()
	for _, item := range resources {
		path := strings.ReplaceAll(item.Path, "\\", "/")
		fmt.Fprintf(hash, "%d:%s\n%d:%s\n", len(path), path, len(item.Content), item.Content)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func attachmentResources(
	input BuildInput,
	root string,
	templates map[string]domain.PromptTemplate,
	data templateData,
) ([]ContextResource, error) {
	directory := joinContextPath(input, root, "attachments")
	indexPath := joinContextPath(input, directory, "index.md")
	resources := []ContextResource{}
	for _, value := range input.Attachments {
		item := value.Attachment
		filePath := joinContextPath(input, directory, item.ID, item.Name)
		relative := knowledgeRelativePath(indexPath, filePath)
		data.AttachmentEntries = append(data.AttachmentEntries, attachmentIndexEntry{
			ID: item.ID, Name: item.Name, RelativePath: relative, MediaType: item.MediaType, Size: item.Size,
		})
		resources = append(resources, detailResource(input, "attachment-"+item.ID, filePath, "Message attachment: "+item.Name, value.Content))
	}
	indexContent, err := renderTemplateByID(templates, "context.attachments-index", data)
	if err != nil {
		return nil, err
	}
	index := resource(input, "attachments-index", indexPath, templates["context.attachments-index"].Description, indexContent)
	return append([]ContextResource{index}, resources...), nil
}

func joinContextPath(input BuildInput, values ...string) string {
	if input.Workspace.Transport == "native" {
		return filepath.Join(values...)
	}
	return joinRemoteContextPath(values...)
}

func joinRemoteContextPath(values ...string) string {
	if len(values) == 0 {
		return ""
	}
	first := strings.ReplaceAll(values[0], "\\", "/")
	unc := strings.HasPrefix(first, "//")
	values = append([]string(nil), values...)
	values[0] = first
	joined := path.Join(values...)
	if unc {
		return "//" + strings.TrimPrefix(joined, "/")
	}
	return joined
}

func resource(input BuildInput, id, path, description, content string) ContextResource {
	return ContextResource{
		ID: id, URI: fmt.Sprintf("aha://tasks/%s/agents/%s/context/%s", input.Task.ID, input.Agent.AgentID, id),
		Path: path, Description: description, Chars: len([]rune(content)), Content: content, EntryPoint: true,
	}
}

func detailResource(input BuildInput, id, path, description, content string) ContextResource {
	item := resource(input, id, path, description, content)
	item.EntryPoint = false
	return item
}

func knowledgeResources(input BuildInput, root string) []ContextResource {
	directory := joinContextPath(input, root, "knowledge")
	resources := knowledgeScopeResources(input, directory, "global", input.GlobalKnowledge)
	resources = append(resources, knowledgeScopeResources(input, directory, "project", input.ProjectKnowledge)...)
	return append(resources, staleKnowledgeResources(input, directory, input.StaleKnowledge)...)
}

func staleKnowledgeResources(input BuildInput, directory string, entries []domain.KnowledgeEntry) []ContextResource {
	if len(entries) == 0 {
		return nil
	}
	entries = append([]domain.KnowledgeEntry(nil), entries...)
	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].Scope != entries[right].Scope {
			return entries[left].Scope < entries[right].Scope
		}
		if entries[left].ProjectID != entries[right].ProjectID {
			return entries[left].ProjectID < entries[right].ProjectID
		}
		return entries[left].Title < entries[right].Title
	})
	queuePath := joinContextPath(input, directory, "pending-updates", "index.md")
	lines := []string{
		"# 待修订知识",
		"",
		"以下文档已被标记为过时或错误，不得作为当前事实使用。仅当本轮工作获得相关代码、测试或文档证据时，读取对应旧文档并通过 Agent Knowledge API 提交完整修订提案。不要脱离当前任务猜测更新，也不要直接恢复 verified。",
		"",
		"## Documents",
		"",
	}
	resources := make([]ContextResource, 0, len(entries)+1)
	for _, entry := range entries {
		name := knowledgePathSegment(entry) + "-" + entry.ID + ".md"
		detailPath := joinContextPath(input, directory, "pending-updates", entry.Scope, name)
		relative := knowledgeRelativePath(queuePath, detailPath)
		feedback := entry.FeedbackState
		if feedback == "" {
			feedback = "stale"
		}
		lines = append(lines, fmt.Sprintf("- [%s](%s) — %s / %s / revision %d / %s", entry.Title, relative, entry.Scope, entry.Type, entry.Revision, feedback))
		body := fmt.Sprintf("# %s\n\n- entry_id: %s\n- base_revision: %d\n- scope: %s\n- project_id: %s\n- type: %s\n- feedback: %s\n\n## 已发布但待修订的内容\n\n%s\n", entry.Title, entry.ID, entry.Revision, entry.Scope, entry.ProjectID, entry.Type, feedback, entry.Body)
		resources = append(resources, detailResource(input, "knowledge-stale-"+entry.ID, detailPath, entry.Title+"（待修订，非有效知识）", body))
	}
	index := resource(input, "knowledge-pending-updates", queuePath, "待修订知识队列；仅在当前工作提供相关证据时读取并提交修订提案", strings.Join(lines, "\n"))
	return append([]ContextResource{index}, resources...)
}

func knowledgeScopeResources(input BuildInput, directory, scope string, entries []domain.KnowledgeEntry) []ContextResource {
	entries = append([]domain.KnowledgeEntry(nil), entries...)
	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].IsIndex != entries[right].IsIndex {
			return entries[left].IsIndex
		}
		if entries[left].SortOrder != entries[right].SortOrder {
			return entries[left].SortOrder < entries[right].SortOrder
		}
		if entries[left].Slug != entries[right].Slug {
			return entries[left].Slug < entries[right].Slug
		}
		return entries[left].ID < entries[right].ID
	})
	scopeDirectory := joinContextPath(input, directory, scope)
	byID := make(map[string]domain.KnowledgeEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	navigationIDs := make(map[string]bool, len(entries))
	if scope == "project" {
		for _, entry := range entries {
			if entry.IsIndex || entry.Type != "navigation" {
				continue
			}
			navigationIDs[entry.ID] = true
			seen := map[string]bool{}
			parentID := entry.ParentID
			for parentID != "" && !seen[parentID] {
				seen[parentID] = true
				parent, ok := byID[parentID]
				if !ok || parent.IsIndex {
					break
				}
				navigationIDs[parent.ID] = true
				parentID = parent.ParentID
			}
		}
	}
	paths := make(map[string]string, len(entries))
	entryCategory := func(entry domain.KnowledgeEntry) string {
		if scope == "project" && !entry.IsIndex && navigationIDs[entry.ID] {
			return "navigation"
		}
		return ""
	}
	var entryPath func(domain.KnowledgeEntry, map[string]bool) string
	entryPath = func(entry domain.KnowledgeEntry, visiting map[string]bool) string {
		if value := paths[entry.ID]; value != "" {
			return value
		}
		if entry.IsIndex && entry.ParentID == "" {
			value := joinContextPath(input, scopeDirectory, "index.md")
			paths[entry.ID] = value
			return value
		}
		segment := knowledgePathSegment(entry)
		category := entryCategory(entry)
		baseDirectory := scopeDirectory
		if category != "" {
			baseDirectory = joinContextPath(input, scopeDirectory, category)
		}
		value := joinContextPath(input, baseDirectory, segment+".md")
		if parent, ok := byID[entry.ParentID]; ok && entryCategory(parent) == category && !visiting[parent.ID] && !(parent.IsIndex && parent.ParentID == "") {
			visiting[entry.ID] = true
			parentPath := strings.TrimSuffix(entryPath(parent, visiting), ".md")
			delete(visiting, entry.ID)
			value = joinContextPath(input, parentPath, segment+".md")
		}
		paths[entry.ID] = value
		return value
	}
	var rootEntry *domain.KnowledgeEntry
	for index := range entries {
		if entries[index].IsIndex && entries[index].ParentID == "" {
			rootEntry = &entries[index]
			break
		}
	}
	type indexSpec struct {
		id, path, title, description, empty string
		rootBacked                          bool
		include                             func(domain.KnowledgeEntry) bool
	}
	specs := []indexSpec{{
		id: "knowledge-global-index", path: joinContextPath(input, scopeDirectory, "index.md"),
		title: "全局知识", description: "这是 AHA2 全局知识页面。通用知识以人类阅读为主，Agent 仅在任务主题明确相关时按需读取；Agent 经验教训用于沉淀可跨项目复用的技术诊断与行为教训。请根据分类、标题与摘要选择阅读路径。",
		empty: "当前没有可用的全局知识文档。", rootBacked: true,
		include: func(domain.KnowledgeEntry) bool { return true },
	}}
	if scope == "project" {
		specs = []indexSpec{
			{
				id: "knowledge-project-index", path: joinContextPath(input, scopeDirectory, "index.md"),
				title: "项目知识", description: "这是当前项目的知识页面，保存项目实践、技术决策、诊断结论和可复用经验。请根据下面的标题与摘要按需打开文档。",
				empty: "当前没有可用的项目知识文档。", rootBacked: true,
				include: func(domain.KnowledgeEntry) bool { return true },
			},
			{
				id: "knowledge-project-navigation-index", path: joinContextPath(input, scopeDirectory, "navigation", "index.md"),
				title: "项目导航", description: "这是当前项目的导航页面，按模块、代码路径、边界与关键流程组织入口。请根据下面的标题与摘要选择阅读路径。",
				empty:   "当前没有可用的项目导航文档。",
				include: func(entry domain.KnowledgeEntry) bool { return navigationIDs[entry.ID] },
			},
		}
	}
	resources := make([]ContextResource, 0, len(specs)+len(entries))
	for _, spec := range specs {
		lines := []string{"# " + spec.title, "", spec.description}
		if spec.rootBacked && rootEntry != nil {
			description := strings.TrimSpace(rootEntry.Body)
			if description == "" || description == "这里汇总跨项目共享的知识文档。" || description == "这里汇总本项目的知识文档与常用入口。" {
				description = spec.description
			}
			lines = []string{"# " + spec.title, "", description}
		}
		if scope == "project" && input.ProductLine.ID != "" {
			lines = append(lines, "", fmt.Sprintf("Active product line: %s (%s)", input.ProductLine.Name, input.ProductLine.BranchPattern))
		}
		lines = append(lines, "", "## Documents", "")
		childCount := 0
		for _, entry := range entries {
			if (rootEntry != nil && entry.ID == rootEntry.ID) || !spec.include(entry) {
				continue
			}
			if parent, parentAvailable := byID[entry.ParentID]; parentAvailable && (rootEntry == nil || parent.ID != rootEntry.ID) && spec.include(parent) {
				continue
			}
			relative := knowledgeRelativePath(spec.path, entryPath(entry, map[string]bool{}))
			lines = append(lines, fmt.Sprintf("- [%s](%s)：%s", entry.Title, relative, knowledgeIndexSummary(entry.Title, entry.Body)))
			childCount++
		}
		if childCount == 0 {
			lines = append(lines, spec.empty)
		}
		resources = append(resources, resource(input, spec.id, spec.path, spec.title+" entrypoint", strings.Join(lines, "\n")))
	}
	for _, entry := range entries {
		if rootEntry != nil && entry.ID == rootEntry.ID {
			continue
		}
		body := knowledgeEntryText(entry)
		children := []string{}
		for _, child := range entries {
			if child.ParentID != entry.ID {
				continue
			}
			relative := knowledgeRelativePath(entryPath(entry, map[string]bool{}), entryPath(child, map[string]bool{}))
			children = append(children, fmt.Sprintf("- [%s](%s)：%s", child.Title, relative, knowledgeIndexSummary(child.Title, child.Body)))
		}
		if len(children) > 0 {
			body += "\n\n## Children\n\n" + strings.Join(children, "\n") + "\n"
		}
		item := detailResource(input, "knowledge-"+entry.ID, entryPath(entry, map[string]bool{}), entry.Title, body)
		if scope == "global" && entry.Slug == "agent-lessons" && rootEntry != nil && entry.ParentID == rootEntry.ID {
			item.EntryPoint = true
			item.Description = "Agent 经验教训优先索引；按任务相关性继续读取技术诊断或行为教训"
			item.Content = "# " + entry.Title + "\n\n" + strings.TrimSpace(entry.Body)
			if len(children) > 0 {
				item.Content += "\n\n## Documents\n\n" + strings.Join(children, "\n") + "\n"
			}
			item.Chars = len([]rune(item.Content))
		}
		resources = append(resources, item)
	}
	return resources
}

func knowledgeIndexSummary(title, body string) string {
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, ">-*+0123456789.) "))
		if line != "" && !strings.EqualFold(line, strings.TrimSpace(title)) {
			return truncate(line, 160)
		}
	}
	return "打开文档查看详情。"
}

func knowledgeEntryText(entry domain.KnowledgeEntry) string {
	return fmt.Sprintf("# %s\n\n- id: %s\n- scope: %s\n- parent_id: %s\n- slug: %s\n- sort_order: %d\n- is_index: %t\n- type: %s\n- revision: %d\n- content_hash: %s\n- product_line_id: %s\n- verified_commit: %s\n\n%s\n", entry.Title, entry.ID, entry.Scope, entry.ParentID, entry.Slug, entry.SortOrder, entry.IsIndex, entry.Type, entry.Revision, entry.ContentHash, entry.ProductLineID, entry.VerifiedCommit, entry.Body)
}

func knowledgePathSegment(entry domain.KnowledgeEntry) string {
	if entry.Slug != "" && entry.Slug != "index" {
		return entry.Slug
	}
	fallback := strings.NewReplacer("/", "-", "\\", "-", "..", "-").Replace(entry.ID)
	if entry.Slug == "index" {
		return "index-" + fallback
	}
	return fallback
}

func knowledgeRelativePath(from, to string) string {
	value, err := filepath.Rel(filepath.Dir(from), to)
	if err != nil {
		return filepath.ToSlash(to)
	}
	return filepath.ToSlash(value)
}

func skillResources(input BuildInput, root string) []ContextResource {
	directory := joinContextPath(input, root, "skills")
	resources := []ContextResource{}
	for _, skill := range input.Skills {
		slug := skill.PackageSlug
		if slug == "" {
			slug = skill.ID
		}
		packageDirectory := joinContextPath(input, directory, slug)
		files := skill.PackageFiles
		if len(files) == 0 {
			files = []domain.SkillFile{{Path: "SKILL.md", Content: fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", skill.Name, skill.Description, skill.Instructions)}}
		}
		fileHashes := map[string]string{}
		for _, file := range files {
			fileHashes[file.Path] = fmt.Sprintf("%x", sha256.Sum256([]byte(file.Content)))
		}
		metadata, _ := json.MarshalIndent(map[string]any{
			"skill_id": skill.ID, "version": skill.Version, "scope": skill.Scope,
			"project_id": skill.ProjectID, "files": fileHashes, "writeback": "agent_api",
		}, "", "  ")
		resources = append(resources, detailResource(input, "skill-"+skill.ID+"-manifest", joinContextPath(input, packageDirectory, "skill-manifest.json"), skill.Name+" package metadata", string(metadata)))
		for index, file := range files {
			filePath := joinContextPath(input, packageDirectory, file.Path)
			item := detailResource(input, fmt.Sprintf("skill-%s-file-%d", skill.ID, index), filePath, skill.Name+" package file", file.Content)
			if file.Path == "SKILL.md" {
				item = resource(input, "skill-"+skill.ID, filePath, fmt.Sprintf("Selected Skill: %s - %s", skill.Name, skill.Description), file.Content)
			}
			resources = append(resources, item)
		}
	}
	return resources
}

func recentContextExchanges(items []domain.ConversationItem, currentTurnID, currentRoundID string) []recentContextExchange {
	type exchange struct {
		users []recentContextUser
		reply string
	}
	exchanges := map[string]*exchange{}
	var order []string
	for _, item := range items {
		if item.TurnID == currentTurnID || currentRoundID != "" && item.RoundID == currentRoundID || item.Category != "chat" {
			continue
		}
		summary := strings.TrimSpace(item.Summary)
		if summary == "" {
			continue
		}
		key := item.RoundID
		if key == "" {
			key = item.TurnID
		}
		if key == "" {
			continue
		}
		current, ok := exchanges[key]
		if !ok {
			current = &exchange{}
			exchanges[key] = current
			order = append(order, key)
		}
		switch item.Kind {
		case "user_message":
			sender := strings.TrimSpace(item.FromAgentID)
			if sender == "" {
				sender = "owner"
			}
			current.users = append(current.users, recentContextUser{Sender: sender, Summary: summary})
		case "agent_message":
			if item.AgentID != "" && item.AgentID != "main" {
				continue
			}
			if item.RouteKind != "" && item.RouteKind != "turn_result" {
				continue
			}
			current.reply = summary
		}
	}
	var completed []*exchange
	for _, key := range order {
		item := exchanges[key]
		if len(item.users) > 0 && item.reply != "" {
			completed = append(completed, item)
		}
	}
	const exchangeLimit = 6
	if len(completed) > exchangeLimit {
		completed = completed[len(completed)-exchangeLimit:]
	}
	result := make([]recentContextExchange, 0, len(completed))
	for index, item := range completed {
		result = append(result, recentContextExchange{
			Number: index + 1,
			Users:  item.users,
			Reply:  item.reply,
		})
	}
	return result
}

func recoveryHandoffEvidenceFor(input BuildInput) *recoveryHandoffEvidence {
	var current domain.Turn
	for _, turn := range input.Turns {
		if turn.ID == input.CurrentTurnID {
			current = turn
			break
		}
	}
	if current.ID == "" || current.RoundID == "" || current.InputMessageID == "" {
		return nil
	}
	var previous domain.Turn
	for _, candidate := range input.Turns {
		if candidate.AgentID != current.AgentID ||
			candidate.Sequence >= current.Sequence ||
			candidate.Status != domain.TurnInterrupted ||
			candidate.RoundID != current.RoundID ||
			candidate.InputMessageID != current.InputMessageID {
			continue
		}
		if previous.ID == "" || candidate.Sequence > previous.Sequence {
			previous = candidate
		}
	}
	if previous.ID == "" {
		return nil
	}
	evidence := &recoveryHandoffEvidence{
		PreviousTurnID:       previous.ID,
		PreviousTurnSequence: previous.Sequence,
		AgentID:              previous.AgentID,
		Status:               string(previous.Status),
		Attempt:              previous.Attempt,
		Generation:           previous.Generation,
		Error:                truncate(previous.Error, 500),
	}
	var latestProgress, latestTool domain.ConversationItem
	for _, item := range input.RecoveryConversation {
		if item.TurnID != previous.ID {
			continue
		}
		if recoveryProgressItem(item) && conversationItemAfter(item, latestProgress) {
			latestProgress = item
		}
		if (item.Kind == "agent_command_started" || item.Kind == "agent_command_finished") &&
			conversationItemAfter(item, latestTool) {
			latestTool = item
		}
	}
	evidence.LatestProgress = truncate(latestProgress.Summary, 600)
	if latestTool.ID != "" || latestTool.Kind != "" {
		evidence.LatestToolSummary = truncate(latestTool.Summary, 400)
		evidence.LatestToolState = strings.TrimSpace(fmt.Sprint(latestTool.Payload["status"]))
		if evidence.LatestToolState == "" || evidence.LatestToolState == "<nil>" {
			if latestTool.Kind == "agent_command_finished" {
				evidence.LatestToolState = "completed"
			} else {
				evidence.LatestToolState = "in_progress"
			}
		}
		if value := latestTool.Payload["exit_code"]; value != nil {
			evidence.LatestToolExitCode = strings.TrimSpace(fmt.Sprint(value))
		}
	}
	return evidence
}

func recoveryProgressItem(item domain.ConversationItem) bool {
	return item.Kind == "agent_progress" ||
		item.Kind == "agent_message_update" && item.RouteKind == "agent_progress"
}

func conversationItemAfter(candidate, current domain.ConversationItem) bool {
	if current.ID == "" && current.Kind == "" {
		return true
	}
	if candidate.Sequence != current.Sequence {
		return candidate.Sequence > current.Sequence
	}
	return candidate.CreatedAt.After(current.CreatedAt)
}

func turnDiagnosticEntries(turns []domain.Turn, agentID string) []turnDiagnostic {
	var result []turnDiagnostic
	for _, turn := range turns {
		if turn.AgentID != agentID || !turnNeedsDiagnostics(turn) {
			continue
		}
		body := turn.Result
		if body == "" {
			body = turn.Error
		}
		result = append(result, turnDiagnostic{
			Sequence: turn.Sequence, AgentID: turn.AgentID, Status: string(turn.Status),
			Attempt: turn.Attempt, Generation: turn.Generation, Body: truncate(body, 1000),
		})
	}
	if len(result) > 10 {
		result = result[len(result)-10:]
	}
	return result
}

func turnNeedsDiagnostics(turn domain.Turn) bool {
	return turn.Status == domain.TurnFailed || turn.Status == domain.TurnInterrupted || turn.Status == domain.TurnBlocked || !turn.StalledAt.IsZero() || turn.Attempt > 1
}

func hardwareContextGroups(groups []domain.HardwareGroup) []hardwareContextGroup {
	result := make([]hardwareContextGroup, 0, len(groups))
	for _, group := range groups {
		item := hardwareContextGroup{
			ID: group.ID, Description: group.Description, Access: string(group.Access), Mode: string(group.Mode),
			HasSerial: group.Supports(domain.HardwareTransportSerial), SerialDevice: group.Serial.Device, SerialBaudrate: group.Serial.Baudrate,
			HasNetwork: group.Supports(domain.HardwareTransportNetwork), NetworkHost: group.Network.Host, NetworkPort: group.Network.Port,
			NetworkProtocol: string(group.Network.Protocol), Username: group.Username, PasswordConfigured: group.PasswordConfigured,
		}
		if item.HasNetwork && group.Network.Protocol == domain.HardwareProtocolSSH {
			item.SSHAuthentication = normalizeHardwareSSHAuth(group.Network.SSHAuth)
		}
		result = append(result, item)
	}
	return result
}

func normalizeHardwareSSHAuth(value string) string {
	switch value {
	case domain.HardwareSSHAuthPassword, domain.HardwareSSHAuthKey:
		return value
	default:
		return domain.HardwareSSHAuthAuto
	}
}
