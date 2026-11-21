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
	"github.com/ChinaKai/AHA2/internal/workspace"
)

//go:embed templates/*.md templates/zh-CN/*.md
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
	Project          domain.Project
	Workspace        domain.Workspace
	Task             domain.Task
	Agent            domain.TaskAgent
	Snapshot         domain.RuntimeConfigSnapshot
	GlobalKnowledge  []domain.KnowledgeEntry
	ProjectKnowledge []domain.KnowledgeEntry
	StaleKnowledge   []domain.KnowledgeEntry
	// KnowledgeGuide is the slim vault guide injected ahead of the knowledge
	// tree. It explains boundaries and the Agent API in every Turn.
	KnowledgeGuide         string
	Skills                 []domain.Skill
	ProductLine            domain.ProductLine
	KnowledgeEnabled       bool
	Conversation           []domain.ConversationItem
	RecoveryConversation   []domain.ConversationItem
	Turns                  []domain.Turn
	Hardware               []domain.HardwareGroup
	MailAccounts           []domain.MailAccount
	MailTemplates          []domain.MailTemplate
	MailDefaultTemplates   map[string]string
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
	Identity               string
	// IdentitySupplement is runtime text appended right after the identity
	// template, such as the pet core digest. It keeps per-Turn content out of
	// the editable template body.
	IdentitySupplement string
	Language           string
	// Memory is the Task state card the previous Turns left behind. It is
	// printed after the Task section so the Agent starts from where the work
	// stands instead of reconstructing it from the conversation.
	Memory domain.TaskMemory
}

// MaxInlineAttachmentBytes is the per-Turn Context inline cap. It is
// deliberately independent of the upload/storage cap: a large upload remains
// available as metadata, but its bytes are never read into the prompt or
// materialized into the workspace on every Turn.
const MaxInlineAttachmentBytes int64 = 4 << 20

// MaxInlineAttachmentBytesPerTurn bounds the total attachment bytes inlined
// into one Turn. The per-attachment cap alone still multiplies across a long
// conversation; this ceiling keeps the per-Turn memory and Context write cost
// bounded no matter how many attachments were posted.
const MaxInlineAttachmentBytesPerTurn int64 = 16 << 20

type AttachmentResource struct {
	Attachment domain.Attachment
	// Content is populated only when Inline is true. A metadata-only resource
	// keeps Content empty so binary bytes never enter the prompt.
	Content string
	Inline  bool
	// BudgetExhausted distinguishes "this attachment fits the per-attachment
	// cap, but newer attachments used the per-Turn budget" from "the file
	// itself exceeds the inline cap". The metadata must report the real reason.
	BudgetExhausted bool
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
	Role    string
	Time    string
	Summary string
}

type recentContextExchange struct {
	Number int
	Users  []recentContextUser
	Reply  string
	Time   string
}

// FormatPromptTime gives the model an unambiguous local timestamp. It is used
// by both the Inbox Batch and recent-context.md so message order and relative
// references are stable across turns.
func FormatPromptTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format("2006-01-02 15:04:05 -07:00")
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

type mailContextAccount struct {
	ID                   string
	FromAddress          string
	DisplayName          string
	Aliases              []string
	Status               string
	CredentialConfigured bool
	DefaultTemplateID    string
}

type mailContextTemplate struct {
	ID        string
	Name      string
	Version   int
	Mode      string
	Scope     string
	ProjectID string
	Variables string
}

// mailContextGuideTemplate carries the reference content of a template that is
// bound as an account default and uses guide mode. It is injected so the Agent
// can write the final subject/body without an extra tool call.
type mailContextGuideTemplate struct {
	ID                   string
	Name                 string
	Version              int
	Scope                string
	Description          string
	Subject              string
	BodyText             string
	BodyHTML             string
	BodyTextTruncated    bool
	BodyHTMLTruncated    bool
	DescriptionTruncated bool
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
	ADBUSB             bool
	ADBSerial          string
	ADBConnection      string
	SSHAuthentication  string
	Username           string
	PasswordConfigured bool
}

type attachmentIndexEntry struct {
	ID              string
	Name            string
	RelativePath    string
	MediaType       string
	Size            int64
	SHA256          string
	Inline          bool
	BudgetExhausted bool
	DownloadPath    string
}

type InboxTemplateItem struct {
	Sequence              int64
	SourceKind            string
	SourceAgentID         string
	CreatedAt             string
	ChannelSender         string
	ChannelSenderRole     string
	ChannelSenderIsOwner  bool
	ChannelSenderIsBot    bool
	ChannelReceiptID      string
	ChannelConversation   string
	MentionedParticipants string
	ReplyContext          *domain.ChannelReplyContext
	Content               string
	Attachments           []domain.Attachment
}

type templateData struct {
	AgentID   string
	AgentRole string
	Identity  string
	Channel   string
	// ChannelGroup and ChannelTaskRoute select the external-channel rules that
	// apply to this Turn: a private chat never needs the group rules, and only a
	// task_route conversation has receipts to resolve.
	ChannelGroup          bool
	ChannelTaskRoute      bool
	Backend               string
	Collaboration         string
	MaxAgents             int
	TaskID                string
	TaskCode              string
	TaskTitle             string
	ContextRoot           string
	ScratchRoot           string
	TaskWorkspace         string
	Workspace             string
	WorkspaceTransport    string
	ProjectName           string
	OriginalRequest       string
	CurrentGoal           string
	CurrentGoalSummary    string
	TaskBranch            string
	InboxBatch            string
	CompactHandoff        string
	RecoveryHandoff       string
	Recovery              *recoveryHandoffEvidence
	AgentAPIURL           string
	AvailableContext      []AvailableContextGroup
	RecentExchanges       []recentContextExchange
	TurnDiagnostics       []turnDiagnostic
	HardwareGroups        []hardwareContextGroup
	MailAccounts          []mailContextAccount
	MailTemplates         []mailContextTemplate
	DefaultGuideTemplates []mailContextGuideTemplate
	AttachmentEntries     []attachmentIndexEntry
	InboxItems            []InboxTemplateItem
	// The Task state card, as rendered by the task-workspace section.
	MemoryCard          bool
	MemoryStale         bool
	MemoryStatus        string
	MemoryNextSteps     []string
	MemoryConstraints   []string
	MemoryOpenQuestions []string
}

// applyMemoryCard copies the Task state card into the template data. Only a
// Task Agent keeps one: the pet has its core files and a channel host Task
// answers one conversation at a time.
//
// The card is stale when an earlier Main Turn finished without writing it. A
// stale card is still shown, with a warning, because an outdated note the
// Agent knows to verify beats no note at all.
func applyMemoryCard(data *templateData, input BuildInput) {
	if data.Identity != identityTaskAgent {
		return
	}
	memory := input.Memory
	data.MemoryStatus = strings.TrimSpace(memory.Status)
	data.MemoryNextSteps = memory.NextSteps
	data.MemoryOpenQuestions = memory.OpenQuestions
	for _, note := range memory.Constraints {
		if text := strings.TrimSpace(note.Text); text != "" {
			data.MemoryConstraints = append(data.MemoryConstraints, text)
		}
	}
	lastMainTurnID, lastSequence := "", -1
	for _, turn := range input.Turns {
		if turn.ID == input.CurrentTurnID || turn.AgentID != "main" || turn.FinishedAt.IsZero() {
			continue
		}
		if turn.Sequence > lastSequence {
			lastMainTurnID, lastSequence = turn.ID, turn.Sequence
		}
	}
	data.MemoryStale = lastMainTurnID != "" && memory.StatusTurnID != lastMainTurnID
	hasContent := data.MemoryStatus != "" || len(data.MemoryNextSteps) > 0 ||
		len(data.MemoryConstraints) > 0 || len(data.MemoryOpenQuestions) > 0
	data.MemoryCard = hasContent || data.MemoryStale
}

func NewEngine(repository Repository) *Engine {
	return &Engine{repository: repository}
}

var builtinTemplates = []domain.PromptTemplate{
	{ID: "core.default", Name: "AHA Core", Layer: "core", Description: "所有 Agent 共用的安全、Workspace 与 AHA 编排边界", Editable: true, Required: true, Version: 4},
	{ID: "role.main", Name: "Main Agent", Layer: "role", Description: "Main 的规划、整合和最终交付职责", Editable: true, Required: true, Version: 5},
	{ID: "role.sub", Name: "Sub Agent", Layer: "role", Description: "Sub 的聚焦执行和隔离职责", Editable: true, Required: true, Version: 2},
	{ID: "identity.task-agent", Name: "Task Agent Identity", Layer: "identity", Description: "代码与项目任务场景身份", Editable: true, Required: false, Version: 2},
	{ID: "identity.channel-assistant", Name: "Channel Assistant Identity", Layer: "identity", Description: "Owner 私聊渠道助手身份", Editable: true, Required: false, Version: 2},
	{ID: "identity.channel-digital-human", Name: "Channel Digital Human Identity", Layer: "identity", Description: "受限群聊电子人身份", Editable: true, Required: false, Version: 3},
	{ID: "identity.pet-task", Name: "Pet Task Identity", Layer: "identity", Description: "宠物 Task 的电子分身定位、owner-delegate 权限和 core/worklog 路径", Editable: true, Required: false, Version: 14},
	{ID: "channel.web", Name: "AHA Web Channel", Layer: "channel", Description: "Web 渠道消息行为", Editable: true, Required: false, Version: 2},
	{ID: "channel.external-channel", Name: "External Channel", Layer: "channel", Description: "外部渠道消息行为", Editable: true, Required: false, Version: 5},
	{ID: "policy.auto", Name: "Auto Collaboration", Layer: "policy", Description: "AHA 自动协作策略", Editable: true, Required: false, Version: 2},
	{ID: "policy.single", Name: "Single Agent", Layer: "policy", Description: "单 Agent 策略", Editable: true, Required: false, Version: 2},
	{ID: "protocol.knowledge", Name: "Knowledge Protocol", Layer: "protocol", Description: "按 index 渐进读取知识并形成反馈与修订闭环", Editable: true, Required: false, Version: 3},
	{ID: "protocol.agent-api", Name: "Agent Control API Protocol", Layer: "protocol", Description: "Agent API 的权限、进度与输出边界", Editable: true, Required: true, Version: 5},
	{ID: "protocol.attachment-delivery", Name: "Attachment Delivery Protocol", Layer: "protocol", Description: "所有 Task 必须遵守的附件上传、绑定与回执边界", Editable: false, Required: true, Version: 2},
	{ID: "context.task", Name: "Task Context File", Layer: "context", Description: "内容模板", Editable: true, Required: true, Version: 1},
	{ID: "context.recent-context", Name: "Recent Context File", Layer: "context", Description: "recent-context.md 的内容模板", Editable: true, Required: false, Version: 2},
	{ID: "context.recovery-handoff", Name: "Recovery Handoff Inline", Layer: "context", Description: "Current Inbox Batch 开头的中断续接证据模板", Editable: true, Required: false, Version: 2},
	{ID: "context.turn-diagnostics", Name: "Turn Diagnostics File", Layer: "context", Description: "diagnostics/turns.md 的内容模板", Editable: true, Required: false, Version: 1},
	{ID: "context.hardware", Name: "Hardware Context File", Layer: "context", Description: "hardware.md 的非敏感硬件上下文模板", Editable: true, Required: false, Version: 1},
	{ID: "context.mail", Name: "Mail Context File", Layer: "context", Description: "mail.md 的非敏感邮件上下文模板；不含凭据、正文或收件人历史", Editable: true, Required: false, Version: 1},
	{ID: "context.attachments-index", Name: "Attachments Index File", Layer: "context", Description: "attachments/index.md 的内容模板", Editable: true, Required: false, Version: 2},
	{ID: "section.compact-handoff", Name: "Compact Handoff Section", Layer: "section", Description: "Backend Session 压缩后的 Prompt 段落", Editable: true, Required: false, Version: 1},
	{ID: "section.task-workspace", Name: "Task And Workspace Section", Layer: "section", Description: "Task 与 Workspace 摘要、任务状态卡", Editable: true, Required: true, Version: 3},
	{ID: "section.available-context", Name: "Available Context Section", Layer: "section", Description: "可用上下文入口列表段落", Editable: true, Required: true, Version: 3},
	{ID: "section.current-inbox", Name: "Current Inbox Section", Layer: "section", Description: "当前 Inbox Batch 段落", Editable: true, Required: true, Version: 1},
	{ID: "section.inbox-batch-content", Name: "Inbox Batch Content", Layer: "section", Description: "Inbox 消息、渠道来源、引用上下文和附件引用的内容模板", Editable: true, Required: true, Version: 4},
	{ID: "resource.agent-api", Name: "Agent API Reference File", Layer: "resource", Description: "Agent API 的 HTTP 参考；内置 MCP 工具不覆盖时查阅", Editable: false, Required: true, Version: 12},
	{ID: "resource.attachment-protocol", Name: "Attachment Protocol File", Layer: "resource", Description: "只读参考模板；附件上传、绑定与回执规程", Editable: false, Required: true, Version: 1},
	{ID: "resource.group-permissions", Name: "Group Permission Rules File", Layer: "resource", Description: "只读参考模板；群聊 task_route 的 Owner 权限申请规则", Editable: false, Required: false, Version: 1},
}

var supersededBuiltinOverrideHashes = map[string]map[string]bool{
	"core.default": {
		"920d894f754ef5fee568b02244dce076e63c79323f5c04c03b3524e44f399c3b": true,
		"a347ba83ec16dd0e946bbd8ad3350841d56187fd01251d61fe8bc0c848bdfadd": true,
		// v1 before the scratch boundary rule; an unmodified override must not
		// hide the rule from existing installations.
		"7053edc7d33e461aba5a061c6e6b76099d3e5f345342e3c3eeca5ec857608c69": true,
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"239daf74b4ee73802bfe0b9d207a91847c3e2dac27eef4b070d2935110fa52d4": true,
		"ad632fee762a12d164530e5050bd6b510814757676f587d0a018bfcf65959d63": true,
		"2176f21f52c5d378135e275c761b91219b217a412e0c9b9c060a5f3c4450c295": true,
		"db43418abcd968ee87f2c0b99654fab1be5a3e4ebf1eaab3e3d84e04c7a97c2a": true,
		// The v0.5.547 builtins, before the pet was described as the Owner's avatar
		// and partner and its messages were given Owner authority.
		"96bb736bd1b5147150e65dd5e8ce8152ffc4b1c3ff13c0dea9369534baa60aff": true,
		"b761495ae018cbca2a875655fce189d2d92e062fa742e4ec8c47d635b09ea542": true,
		"a0bb0f79c613c3cebe465d927f1f213ac00662b63270b2442ffef065c6a00cea": true,
		"2a18b808af82a9c02a189ce6bfbaacd823adcefe09874fbf0718323dbf2d775e": true,
	},
	"role.main": {
		"497fffe9552e6978f283985916e9563ec59b8a3a080f2759ef5da56731a489a0": true,
		"d4cc743eaf8270e21634508f17e47ee240924c3bb57426a7c8a126b7af64cd3d": true,
		// Adds the pre-final scratch cleanup reminder.
		"1ee35434ece95016d4b804a7e9a0d7268cc79eef31fd2dad4e89825f40136cc9": true,
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"db9d4e3399c32fc39a56ac2d38e8eee929369b3d45ef18a3964ac5f352585b72": true,
		"6fb5ceb8b2e48174d57c1c15622e7d6f3a35238e0fbdd6bd000e9b76bc0ad1c5": true,
		"a6d313bcd3fa54aedc22722d66283c4ef872be3507dbf6fb3a3eb5cb84c658d2": true,
		"8c3628c820ea226ca653db61c22c0fe96731a2e744ea4cde3e9cf574efc16b1c": true,
	},
	"section.task-workspace": {
		// Adds the runtime scratch path and read-only Context boundary.
		"99ed10ce10700b61f8d8e2800382c2a8343d8959007cb5237d4f2ae31022c197": true,
		// Before the Task state card was appended to the section.
		"1c093fd69d35a0b4ed2827849f75a8728d984370ce52d75e1def1e3dc1b09e86": true,
		"fab692626aef4dc2f0366bee0ee56c5c37206c6e9202740d39fb540dbc188391": true,
		"5b12e24479ea081a83cd3959e281f4174f529c22690d7ce1679dc4546d90b78a": true,
		"15a896edb0860c04e4fd1be9ff14749aef3fb33ce644e4eb88d19601a06979c7": true,
	},
	"role.sub": {
		"388229947d72bbd80f97bd93a316e88bf8425b13a0d1845b66089c54d20a0d5f": true,
		"05cfc5eb0815fde5b4ce67beea783dc3621faf239dd2817fa06d3652421af14c": true,
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"89a0dcef50449695846fc99d23655cdd2f4882df57a4d456dfa15b305d7ded74": true,
		"1a0c5d48adca814cd4c542e0d8b94d06177e75932dc3099a6bf3b4a8ac58d231": true,
		"56cc6950890a9ebf329b849b660ec7e19cfd9962d8195e9d948bcb4627c5b7f1": true,
		"582e336503f8d647fd9024909a2b8496ec35f8ef16198d6638f31878d17c57ff": true,
	},
	"section.inbox-batch-content": {
		"5fa0f46660226c7ccc0d18c91ae55a813230b3651194dee277e3fe180df2356d": true,
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"c92eb1124de0b5360485f211e69412d0bba005317ee0efb867fdefae77fc0f67": true,
		"53f6c871f7453c5081c672191fdc3141efb96c0da0ecb05fa896f5af603e106d": true,
		"f5ee6f97c3c3f8228ce032dd649ffd25c89ed630c11dde5e50f9dc47fdd20e62": true,
		"4d698e38a239a8622b8f3cff9df68c59b9744539750b0efe5b54a8a1d1fc0511": true,
		// The v0.5.547 builtins, before the pet was described as the Owner's avatar
		// and partner and its messages were given Owner authority.
		"9539139dd3190d2796c1b991c74b2c70775b83427aae2ad3167eb724f569ccb5": true,
		"9e365c330affd8df091345b7c892342f3b283dbe0c8db5506308093799bca4e3": true,
		"697e2989009283765ad2a37cb2a8d6977d0f6e0a29205488f395fd3eb62c7c32": true,
		"25c0403f4de2ba3b94517cb96c254ed24d10496ac9f77593f8d9e00fd41ad547": true,
	},
	"identity.pet-task": {
		"a5f90c9f238a5b97a04049c4cda2cc210aacb1a06174c845af8bc707704178bf": true,
		"0d2e42d11759a7c1cbad9d7a802c2a4b4a00a527b194096e8dee61702ddfaa34": true,
		"4be52f4650bb255ffb23ed5ab85da4daff075b7dced4f94867fbf27ab49b85ae": true,
		"4774b2662357d0c48f61e1ff0fc8ffd9e6fcb72670784fa3a2599dbc97012faf": true,
		// The v1 prompt described the pet's persona and duties inline. Those now
		// live in core/identity.md and core/principles.md. The v2 prompt still
		// scoped the pet to a chat/diary assistant; v3 makes it an owner-delegate
		// that starts with read-only diagnostics; v4 added write tools; v5 added
		// remote-mirror discovery, takeover, and retire tools; v6 added the
		// knowledge/Skill/Prompt owner-delegate surface; v7 added channel
		// discovery, contacts, and approval-gated external sends; v8 added
		// channel enable/disable and security settings via approval; v9 added
		// permission-change requests and channel lifecycle approvals; v10
		// replaced the command dump with aha_capabilities and pet_say/idea; v11
		// makes the pet a coordinator with a read-only runtime; v12 removes the
		// retired personal journal tool and workspace path.
		"9c13cd2859c558166db608c0195bc1809ace8c01d29425337fc87232700de19a": true,
		"faa4cf84331e46273cbd52d9ec0cf0707864d8e0fd87a0f8b3e374f93f9d68f9": true,
		"cfc4559e7cfcc119c3f7711255f4e8bf02be02f08ba2f958535545c8a338aa81": true,
		"24a722080f6044380af1b8f6c98e856b7f8bb0952ef2afb98a422cebf654ecb5": true,
		"a9b90a8bc7bfb1defcc8dddd0530b3cc06d6df14288da37142c1f6041b5488d5": true,
		"b4b0a1889ef3013fbb23237bb19d4c855492cee832d6edf098b8b4efaac0d542": true,
		"38fe86fac2740ac0a552419b1267ebe0c79b9bfb5347ec0f999881991f475b7d": true,
		"61446d0c1c1e8d07504050bedaedd6499489151e2b36fc36e4722bf95eaa8903": true,
		"c4d14681b31e310759633872da0d01d74d6f69fd1c0b3aa8a2758c55440f0bf5": true,
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"232bd43e8daa63a760596e22c83a02745b2d49874fe41f082c6be383d83a735d": true,
		"cbf624e6696c98e1c9a643586f58ba486f85b51ea1c829b83b5f8e03685b1905": true,
		"4b39c8dd13eea1e81242565ec7a8cf5243395d344ee1c5e107b641c23ff16208": true,
		"4f500e3acb936c2e56854d51f95e81bbda0551f03580f3261ea1cef5e597dc7f": true,
		// The v0.5.547 builtins, before the pet was described as the Owner's avatar
		// and partner and its messages were given Owner authority.
		"cbdabd75853dfca5cdbd11730a4313e94d814ae4391b8f4b6a7410eef7b0a8a2": true,
		"41877414dfa11c8d0a5945f7d9d01f13e9d4c7b6cb1623f54f77aeb7c3ae3f67": true,
		"20e40b8e28d7708a3c08e603a87728172ea8fbbe0c4e6d2413a30ae627aaee0b": true,
		"121b709cf85a67e3ddbaab3b2d4df9ff80ec95b266fd8dbf94437e46a48f6729": true,
	},
	"section.available-context": {
		// The previous builtins repeated the pet core/diary paths that Pet Task
		// Identity already owns; discard an unmodified override of either version.
		"88f7ddab63240d7dffe30efab3dd57f9fee0360f5cce5387c114ca3366ff0039": true,
		"49803c2773aad1968094484789be9b8c253084b50fede4bda767ad9476e7c190": true,
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"4ff967ce4ab046ef2d17c0ab74d66a5afce4a28f61a371b35cf77c458ae67695": true,
		"7d87028ef781235e459d57454a7ef218452f023ce2e20b0f58547eb090b56000": true,
		"3d42e28e20c34701f86889fae3aab9ff80e8b139d8aeb8c92dd021d5cc22ac18": true,
		"642ea6fb568b5270ce68df58b46e0532e47e087f24a532871144eeac41d91250": true,
	},
	"channel.external-channel": {
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"fc938dee8ed157bf137b51c72177975a598848f992a44e62b22d9eca5a427c00": true,
		"416fc168fc9c65fed118fdefca86e208fd4e9ac583d9ce3b12f6c661a864b0b7": true,
		"486be4ac6197132dab802a5af52a7dd1e43111e94e227cb6b43c761bc9545d59": true,
		"1cc946d3096f8bd9adf13e059ceb67ef4a6da5ce4eaceec50e0daec16e20a99a": true,
	},
	"channel.web": {
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"72059a9850540039d80064002aae6cf9afef1e418d182f139c3e588b73c5d4fa": true,
		"7194e9594a00447d14ef32712b1c62a8f71e21bc68933a52da27d252930bffd5": true,
	},
	"identity.channel-assistant": {
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"4c378288f608547624ea462dca479494f9a87ad4b614c477786bce8a4b3be631": true,
		"191ae1b194de3eb8abea10bc860472029a007718fd923e92e8bfdba8e69ce493": true,
		"b4c11bd14aed2392822f71be5084a34d6f3937168ad28c70459f9abe603a862e": true,
		"0d7c05a9f4788c79531cefeda920013fc5d0a8af8a071758f1c7723b58c4e772": true,
	},
	"identity.channel-digital-human": {
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"4b827d8a44da93ed5858b2ac31e65215b45f0b32799378be1e037a07d0981922": true,
		"872f7a601ae26c1cafa51ebf85e7635bcb50d635b36f1234a0502d020ac429b2": true,
		"864c1817154dbf2497bd68c27ee06ec4967c923a274869951dddb1bbbd1d3609": true,
		"c3b7a8eb44d22993cb42af32eafb0e8d1825dbd24c3ce013bccfa98df87dbf81": true,
	},
	"identity.task-agent": {
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"3d04207b3bcbe9276e8f7e5f45f2a13258c81f05365642d6f2e097d526c7ff5f": true,
		"8889d88b2b8c2e59375fd529a234a0b87af780f90e680e72206bc3c0d4514e81": true,
		"0f959d94e1813015fd34ccfbc30e659de785740e9734344d6d24a6654341be62": true,
		"bdbc00660e7c4ec48e22e583f2b4368095ac343e6e1ec87c71c3fd61ff1dc7c4": true,
	},
	"policy.auto": {
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"fd604e60b2a0a19682dadc5f9ba313bb98fd82118b709468ebd33554fca0986d": true,
		"284d290c0710bffde3b6b57f27f422fe6f9db7c5bf3644945010238a6285f870": true,
	},
	"policy.single": {
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"0bfa457d1d53ae0b6a7efc0851e48666c6f254565cbc56448d40662bd2837ba0": true,
		"2bc547c1cfa5ee1a380a9bc1a0b6234ab6af47e633fa89df101b856c82711645": true,
	},
	"protocol.agent-api": {
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"b3de146ab2fc11923d5f5284719a6ae3b451a1d77e1594f6118cc66248475fb7": true,
		"3a38f23dcdb9971a6af193107ac59178f43673a5b29695aaf70a67e2e6f87cdd": true,
		"65652e6c35ce26adced6dfe609d83e2c72efd86e66f0595a936c57d064ad49ab": true,
		"f8eff1c49bd5f44a1867fb60d918dbcbdab46180bf1ee3894ed0bce255671772": true,
	},
	"protocol.knowledge": {
		// The builtins before the prompt rewrite that explains the system, gives
		// each rule its reason, and moves the per-Turn sections last (en, zh-CN;
		// LF and CRLF).
		"62e0a87d5b01af8bd4c106b03c2090c343acc633932651f32d18f357350a2c0b": true,
		"e34552f1265d803f2ad90295d5d3f3ad8c524ab6cee66ca71ee39bc50f62795b": true,
		"4bcb7b04e154963e4f7b3076f94122a2bbfae2ba2d37107d6922da737b30cb26": true,
		"fbc17b4c27576a674dd88ecbe3cc6846af2ff9b8dd06e563112169804073c4f8": true,
	},
}

func (engine *Engine) Templates(ctx context.Context) ([]domain.PromptTemplate, error) {
	return engine.TemplatesForLanguage(ctx, "")
}

// TemplatesForLanguage returns the built-in template catalog in the requested
// language. An explicit zh-CN preference selects the translated variants;
// otherwise the canonical English templates remain the fallback. Owner
// overrides are language-neutral and always win.
func (engine *Engine) TemplatesForLanguage(ctx context.Context, language string) ([]domain.PromptTemplate, error) {
	overrides, err := engine.repository.PromptTemplateOverrides(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.PromptTemplate, 0, len(builtinTemplates))
	useChinese := domain.NormalizeLanguage(language) == domain.LanguageSimplified
	for _, item := range builtinTemplates {
		if item.Content == "" {
			name := strings.ReplaceAll(item.ID, ".", "-") + ".md"
			path := "templates/" + name
			if useChinese {
				if _, statErr := templateFiles.ReadFile("templates/zh-CN/" + name); statErr == nil {
					path = "templates/zh-CN/" + name
				}
			}
			data, readErr := templateFiles.ReadFile(path)
			if readErr != nil {
				return nil, readErr
			}
			item.Content = strings.TrimSpace(string(data))
		}
		if useChinese {
			localizeTemplateMeta(&item)
		} else if description := englishTemplateDescriptions[item.ID]; description != "" {
			item.Description = description
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

var englishTemplateDescriptions = map[string]string{
	"core.default":                   "Shared safety, Workspace, and AHA orchestration boundaries for all Agents",
	"role.main":                      "Main's planning, integration, and final delivery responsibilities",
	"role.sub":                       "Sub Agent's focused execution and isolation responsibilities",
	"identity.task-agent":            "Identity for code and project Tasks",
	"identity.channel-assistant":     "Identity for an Owner direct-channel assistant",
	"identity.channel-digital-human": "Identity for a restricted group-chat digital human",
	"identity.pet-task":              "Pet Task identity, owner-delegate permissions, and core/worklog paths",
	"channel.web":                    "Web channel message behavior",
	"channel.external-channel":       "External channel message behavior",
	"policy.auto":                    "AHA auto-collaboration policy",
	"policy.single":                  "Single-Agent policy",
	"protocol.knowledge":             "Progressively read knowledge by index and close the feedback and revision loop",
	"protocol.agent-api":             "Agent API permission, progress, and output boundaries",
	"protocol.attachment-delivery":   "Attachment upload, binding, and receipt boundaries required for every Task",
	"context.task":                   "Task context template",
	"context.recent-context":         "recent-context.md template",
	"context.recovery-handoff":       "Recovery-handoff evidence template at the start of Current Inbox Batch",
	"context.turn-diagnostics":       "diagnostics/turns.md template",
	"context.hardware":               "Non-sensitive hardware context template for hardware.md",
	"context.attachments-index":      "attachments/index.md template",
	"section.compact-handoff":        "Prompt section after Backend Session compaction",
	"section.task-workspace":         "Task and Workspace summary section",
	"section.available-context":      "Available-context entry list section",
	"section.current-inbox":          "Current Inbox Batch section",
	"section.inbox-batch-content":    "Template for Inbox messages, channel sources, quoted context, and attachment references",
	"resource.agent-api":             "HTTP reference for the Agent API; the fallback when built-in MCP tools do not cover an operation",
	"resource.attachment-protocol":   "Read-only reference template; attachment upload, binding, and receipt protocol",
	"resource.group-permissions":     "Read-only reference template; Owner permission rules for group task routes",
}

var chineseTemplateNames = map[string]string{
	"core.default":                   "AHA 核心边界",
	"role.main":                      "主 Agent",
	"role.sub":                       "子 Agent",
	"identity.task-agent":            "任务 Agent 身份",
	"identity.channel-assistant":     "渠道助手身份",
	"identity.channel-digital-human": "群聊电子人身份",
	"identity.pet-task":              "宠物 Task 身份",
	"channel.web":                    "AHA Web 渠道",
	"channel.external-channel":       "外部渠道",
	"policy.auto":                    "自动协作",
	"policy.single":                  "单 Agent",
	"protocol.knowledge":             "知识协议",
	"protocol.agent-api":             "Agent 控制 API 协议",
	"protocol.attachment-delivery":   "附件交付协议",
	"context.task":                   "Task 上下文文件",
	"context.recent-context":         "最近上下文文件",
	"context.recovery-handoff":       "中断续接内联段落",
	"context.turn-diagnostics":       "Turn 诊断文件",
	"context.hardware":               "硬件上下文文件",
	"context.attachments-index":      "附件索引文件",
	"section.compact-handoff":        "压缩交接段落",
	"section.task-workspace":         "Task 与 Workspace 段落",
	"section.available-context":      "可用上下文段落",
	"section.current-inbox":          "当前 Inbox 段落",
	"section.inbox-batch-content":    "Inbox 批次内容",
	"resource.agent-api":             "Agent API 参考文件",
	"resource.attachment-protocol":   "附件协议文件",
	"resource.group-permissions":     "群聊权限申请规则文件",
}

func localizeTemplateMeta(item *domain.PromptTemplate) {
	if name := chineseTemplateNames[item.ID]; name != "" {
		item.Name = name
	}
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
	data := any(templateData{})
	if strings.HasPrefix(id, "identity.pet-") {
		data = map[string]any{"Nickname": "", "Identity": "", "Principles": "", "Snapshot": "", "Layout": "", "Tools": "", "Page": "", "IdleMinutes": 0}
	}
	if err := parsed.Execute(&output, data); err != nil {
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

func (engine *Engine) RenderInboxBatch(ctx context.Context, language string, items []InboxTemplateItem) (string, error) {
	templates, err := engine.TemplatesForLanguage(ctx, language)
	if err != nil {
		return "", err
	}
	return RenderInboxBatchContent(templates, items)
}

// RenderInboxBatchContent renders an inbox batch from templates already loaded
// by TemplatesForLanguage. A caller that must render inside a database
// transaction uses this so the render does not issue another database read
// from that transaction's pool.
func RenderInboxBatchContent(templates []domain.PromptTemplate, items []InboxTemplateItem) (string, error) {
	templateByID := make(map[string]domain.PromptTemplate, len(templates))
	for _, item := range templates {
		templateByID[item.ID] = item
	}
	return renderTemplateByID(templateByID, "section.inbox-batch-content", templateData{InboxItems: items})
}

// languageDirective tells the agent which language to use for Owner-facing
// text. The resident prompts stay in Chinese because they are the control-plane
// instructions; this small section changes only the response language.
func languageDirective(language string) string {
	switch domain.NormalizeLanguage(language) {
	case domain.LanguageEnglish:
		return "## Response language\nUse English for your user-facing answer, progress messages, and summaries unless the Owner explicitly asks for another language. When replying in an external channel, match the language used by that channel's participants."
	case domain.LanguageSimplified:
		return "## 回复语言\n回复 owner、写进度消息和总结默认用简体中文，除非 owner 本轮明确要求其他语言；给外部渠道发消息时，跟随对方使用的语言。"
	default:
		return "## Response language\nReply in the language of the Owner's latest message. If the message language is ambiguous, use Simplified Chinese. In an external channel, match the participants' language."
	}
}

// Build is the production prompt renderer. Resident fragments are selected and
// ordered through the core/task-context entry model, with every legacy template
// ID resolved through its frozen anchor alias. The rendered bytes are identical
// to BuildLegacy for the supported Turn shapes.
func (engine *Engine) Build(ctx context.Context, input BuildInput) (BuildResult, error) {
	return engine.BuildEntryModel(ctx, input)
}

// BuildLegacy is the pre-entry-model renderer. It is retained for one release
// as a testable equivalence baseline and rollback reference; new callers must
// use Build.
func (engine *Engine) BuildLegacy(ctx context.Context, input BuildInput) (BuildResult, error) {
	templates, err := engine.TemplatesForLanguage(ctx, input.Language)
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
	scratchRoot := ""
	if strings.TrimSpace(workDir) != "" {
		scratchRoot, err = workspace.AgentScratchDir(input.Workspace, workDir, input.Task.ID, safeAgentID(input.Agent.AgentID))
		if err != nil {
			return BuildResult{}, err
		}
	}
	identityName, channelName := identityAndChannel(input.ChannelContext)
	if strings.TrimSpace(input.Identity) != "" {
		identityName = strings.TrimSpace(input.Identity)
	}
	identityTemplate := "identity." + identityName
	channelTemplate := "channel." + channelName
	data := templateData{
		AgentID: input.Agent.AgentID, AgentRole: input.Agent.Role, Identity: identityName,
		Channel: channelName, ChannelGroup: channelIsGroup(input.ChannelContext),
		ChannelTaskRoute: channelRouteMode(input.ChannelContext) == "task_route",
		Backend:          input.Snapshot.Backend, Collaboration: input.Task.CollaborationMode,
		MaxAgents: input.Task.MaxAgents, TaskID: input.Task.ID, TaskCode: input.Task.Code,
		TaskTitle: input.Task.Title, ContextRoot: contextRoot, ScratchRoot: scratchRoot, TaskWorkspace: workDir,
		Workspace: input.Workspace.Name, WorkspaceTransport: input.Workspace.Transport,
		ProjectName: input.Project.Name, OriginalRequest: input.Task.OriginalRequest,
		CurrentGoal: input.Task.CurrentGoal, CurrentGoalSummary: truncate(input.Task.CurrentGoal, 800),
		TaskBranch: input.Task.TaskBranch, InboxBatch: strings.TrimSpace(input.UserMessage),
		CompactHandoff: strings.TrimSpace(input.Handoff), AgentAPIURL: strings.TrimRight(input.AgentAPIURL, "/"),
	}
	applyMemoryCard(&data, input)
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
	data.AvailableContext = availableContextEntries(resources)
	templateIDs := residentTemplateIDs(input.Agent.Role, input.Task.CollaborationMode, input.KnowledgeEnabled, identityTemplate, channelTemplate)
	var parts []string
	for _, id := range templateIDs {
		content, renderErr := renderTemplateByID(templateByID, id, data)
		if renderErr != nil {
			return BuildResult{}, renderErr
		}
		if id == identityTemplate {
			if supplement := strings.TrimSpace(input.IdentitySupplement); supplement != "" {
				content = strings.TrimRight(content, "\n") + "\n\n" + supplement
			}
		}
		parts = append(parts, content)
	}
	// Fixed rules come first and the per-Turn payload last, so the Agent reads
	// the request right after the rules that govern it.
	for _, id := range residentProtocolTemplateIDs {
		content, renderErr := renderTemplateByID(templateByID, id, data)
		if renderErr != nil {
			return BuildResult{}, renderErr
		}
		parts = append(parts, content)
	}
	if directive := languageDirective(input.Language); directive != "" {
		parts = append(parts, directive)
	}
	if data.CompactHandoff != "" {
		content, renderErr := renderTemplateByID(templateByID, "section.compact-handoff", data)
		if renderErr != nil {
			return BuildResult{}, renderErr
		}
		parts = append(parts, content)
	}
	for _, id := range residentSectionTemplateIDs {
		content, renderErr := renderTemplateByID(templateByID, id, data)
		if renderErr != nil {
			return BuildResult{}, renderErr
		}
		parts = append(parts, content)
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

// residentTemplateIDs lists the templates rendered in full, in order, for a Turn
// with the given shape. It is separate from Build so the residency guardrail can
// enumerate exactly the set the renderer uses: a template that becomes resident
// without a budget entry should fail the test rather than quietly ship
// unguarded, which is how the largest layer in the system went unnoticed.
func residentTemplateIDs(agentRole, collaborationMode string, knowledgeEnabled bool, identityTemplate, channelTemplate string) []string {
	ids := []string{identityTemplate}
	if agentRole == "sub" {
		ids = append(ids, "role.sub")
	} else {
		ids = append(ids, "role.main")
	}
	ids = append(ids, "core.default")
	if agentRole == "main" {
		if collaborationMode == "single" {
			ids = append(ids, "policy.single")
		} else {
			ids = append(ids, "policy.auto")
		}
	}
	ids = append(ids, channelTemplate)
	if knowledgeEnabled {
		ids = append(ids, "protocol.knowledge")
	}
	return ids
}

// The layers below are appended after the role-dependent block. They render
// unconditionally except for the compact handoff, which appears once a Backend
// Session has been compacted.
var (
	residentSectionTemplateIDs     = []string{"section.task-workspace", "section.available-context", "section.current-inbox"}
	residentProtocolTemplateIDs    = []string{"protocol.agent-api", "protocol.attachment-delivery"}
	residentConditionalTemplateIDs = []string{"section.compact-handoff"}
)

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

// channelIsGroup reports whether the Turn came through the group endpoint. A
// task_route can be bound to either a group or a private chat, so the route
// mode alone does not say which rules apply.
func channelIsGroup(channelContext map[string]any) bool {
	return contextString(channelContext, "endpoint") == domain.ChannelEndpointGroupDigitalHuman
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
	if len(input.MailAccounts) > 0 {
		mailData := data
		mailData.MailAccounts = mailContextAccounts(input.MailAccounts, input.MailDefaultTemplates)
		mailData.MailTemplates = mailContextTemplates(input.MailTemplates)
		mailData.DefaultGuideTemplates = mailContextGuideTemplates(input.MailTemplates, input.MailDefaultTemplates)
		mailContent, renderErr := renderTemplateByID(templates, "context.mail", mailData)
		if renderErr != nil {
			return nil, renderErr
		}
		resources = append(resources, resource(input, "mail", joinContextPath(input, root, "mail.md"), templates["context.mail"].Description, mailContent))
	}
	if len(input.Attachments) > 0 {
		attachments, renderErr := attachmentResources(input, root, templates, data)
		if renderErr != nil {
			return nil, renderErr
		}
		resources = append(resources, attachments...)
	}
	if len(input.ChannelContext) > 0 {
		if channelGroupTaskRoute(input.ChannelContext) {
			permissions, renderErr := renderTemplateByID(templates, "resource.group-permissions", data)
			if renderErr != nil {
				return nil, renderErr
			}
			resources = append(resources, resource(input, "group-permissions",
				joinContextPath(input, root, "group-permissions.md"),
				templates["resource.group-permissions"].Description, permissions))
		}
		data, _ := json.MarshalIndent(input.ChannelContext, "", "  ")
		resources = append(resources, resource(input, "channel-context", joinContextPath(input, root, "channel-context.json"), "服务端验证的只读渠道上下文；不授予额外权限", string(data)))
	}
	return resources, nil
}

func channelGroupTaskRoute(values map[string]any) bool {
	if contextString(values, "endpoint") != domain.ChannelEndpointGroupDigitalHuman {
		return false
	}
	route, _ := values["route"].(map[string]any)
	return contextString(route, "mode") == "task_route"
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
	// The attachment procedure is a resource rather than resident text, so it is
	// materialized for every Task: the resident protocol template only points at
	// it, and a pointer without the file would lose the procedure entirely.
	attachmentContent, err := renderTemplateByID(templates, "resource.attachment-protocol", data)
	if err != nil {
		return "", nil, err
	}
	agentAPIDescription := templates["resource.agent-api"].Description
	attachmentDescription := templates["resource.attachment-protocol"].Description
	seed := sharedResources(input, "", agentAPIContent, agentAPIDescription, attachmentContent, attachmentDescription)
	if len(seed) == 0 {
		return "", nil, nil
	}
	hash := contextSnapshotHash(seed)
	root := joinContextPath(input, workDir, ".aha2-context", input.Task.ID, "shared-"+hash)
	manifest := sharedResources(input, root, agentAPIContent, agentAPIDescription, attachmentContent, attachmentDescription)
	for index := range manifest {
		manifest[index].URI = fmt.Sprintf("aha://tasks/%s/shared/%s", input.Task.ID, manifest[index].ID)
	}
	return root, manifest, nil
}

func sharedResources(input BuildInput, root, agentAPIContent, agentAPIDescription, attachmentContent, attachmentDescription string) []ContextResource {
	resources := []ContextResource{}
	if strings.TrimSpace(input.AgentAPIURL) != "" {
		resources = append(resources, resource(input, "agent-api", joinContextPath(input, root, "agent-api.md"), agentAPIDescription, agentAPIContent))
	}
	if strings.TrimSpace(attachmentContent) != "" {
		resources = append(resources, resource(input, "attachment-protocol", joinContextPath(input, root, "attachment-protocol.md"), attachmentDescription, attachmentContent))
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
		entry := attachmentIndexEntry{
			ID: item.ID, Name: item.Name, MediaType: item.MediaType, Size: item.Size,
			SHA256: item.SHA256, Inline: value.Inline, BudgetExhausted: value.BudgetExhausted,
			DownloadPath: "/api/v1/tasks/" + input.Task.ID + "/attachments/" + item.ID,
		}
		if value.Inline {
			filePath := joinContextPath(input, directory, item.ID, item.Name)
			entry.RelativePath = knowledgeRelativePath(indexPath, filePath)
			resources = append(resources, attachmentDetailResource(
				input, "attachment-"+item.ID, filePath, "Message attachment: "+item.Name, value.Content))
		} else {
			// Keep the Context entry point useful without the bytes: the index
			// links to a small metadata file instead of a file that was never
			// written.
			metadataPath := joinContextPath(input, directory, item.ID, "metadata.md")
			entry.RelativePath = knowledgeRelativePath(indexPath, metadataPath)
			resources = append(resources, attachmentDetailResource(
				input, "attachment-"+item.ID+"-metadata", metadataPath,
				"Attachment metadata; content not inlined: "+item.Name,
				attachmentMetadata(item, input.Task.ID, value.BudgetExhausted)))
		}
		data.AttachmentEntries = append(data.AttachmentEntries, entry)
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

// attachmentDetailResource counts bytes rather than runes. Attachment content
// is often binary, and len([]rune(content)) would allocate a second
// representation of every inlined file for a value that is never rendered for
// non-entry-point resources.
func attachmentDetailResource(input BuildInput, id, path, description, content string) ContextResource {
	return ContextResource{
		ID: id, URI: fmt.Sprintf("aha://tasks/%s/agents/%s/context/%s", input.Task.ID, input.Agent.AgentID, id),
		Path: path, Description: description, Chars: len(content), Content: content,
	}
}

func attachmentMetadata(item domain.Attachment, taskID string, budgetExhausted bool) string {
	inlineMiB := MaxInlineAttachmentBytes / (1 << 20)
	reason := fmt.Sprintf("it exceeds the %d MiB per-Turn Context inline limit", inlineMiB)
	if budgetExhausted {
		reason = "the per-Turn Context inline budget was exhausted by newer attachments"
	}
	return fmt.Sprintf(
		"# %s\n\n- id: %s\n- media_type: %s\n- size: %d bytes\n- sha256: %s\n"+
			"- content: not inlined; %s\n"+
			"- download: GET /api/v1/tasks/%s/attachments/%s (an Owner session is required)\n",
		item.Name, item.ID, item.MediaType, item.Size, item.SHA256, reason, taskID, item.ID)
}

// AvailableContextGroup is one directory to print once, followed by the entry
// point paths beneath it. Paths are relative to Root.
type AvailableContextGroup struct {
	Root    string
	Entries []ContextResource
}

// availableContextEntries selects the entry points and groups them under their
// parent directories, so each directory is printed once rather than once per
// entry.
//
// Full paths re-sent ~110 rune of per-snapshot prefix on every line of every
// Turn, and the shared snapshot's hash directory on top of that. Factoring out
// the longest *common* directory recovers only the outer prefix, because the
// entries span two roots beneath it (the agent's own context and the shared
// snapshot) and the common prefix therefore stops above the hash directory —
// the budget guard caught exactly that, with the hash still printed six times.
//
// Grouping by parent directory instead prints the hash once, because the
// shared snapshot's directory is one group. A directory holding a single entry
// is not worth a heading of its own, so those entries keep their full path;
// nothing is made relative to a root that does not contain it.
func availableContextEntries(resourceSets ...[]ContextResource) []AvailableContextGroup {
	entries := []ContextResource{}
	paths := []string{}
	for _, resources := range resourceSets {
		for _, item := range resources {
			if !item.EntryPoint {
				continue
			}
			entries = append(entries, item)
			paths = append(paths, item.Path)
		}
	}
	roots := availableContextRoots(paths)
	groups := []AvailableContextGroup{}
	position := map[string]int{}
	uncRoots := map[string]bool{}
	for index := range entries {
		root := coveringRoot(roots, paths[index])
		// A UNC path is written with two leading separators, and the tree
		// collapses them into one. joinRemoteContextPath goes out of its way to
		// keep the pair, because "//host/share" and "/host/share" are different
		// paths to the host that reads them, so restore it here rather than
		// printing a root no Windows consumer can open. Both separators count:
		// a remote workspace reaches the template as //host/share, but the same
		// share is also written \\host\share, and the two must come out alike.
		if isUNCPath(paths[index]) && strings.HasPrefix(root, "/") && !strings.HasPrefix(root, "//") {
			uncRoots[root] = true
		}
		slot, seen := position[root]
		if !seen {
			slot = len(groups)
			position[root] = slot
			groups = append(groups, AvailableContextGroup{Root: root})
		}
		entries[index].Path = relativeToContextRoot(root, paths[index])
		groups[slot].Entries = append(groups[slot].Entries, entries[index])
	}
	for index := range groups {
		if uncRoots[groups[index].Root] {
			groups[index].Root = "/" + groups[index].Root
		}
	}
	return groups
}

// availableContextRootHeading renders a group heading. The root search measures
// the same string, so the two cannot drift apart.
func availableContextRootHeading(root string) string {
	return "Paths under `" + root + "/` (each `aha://` URI below is also valid):"
}

// availableContextRoots chooses the directories worth printing once, as a root
// the entries beneath them are shown relative to.
//
// Full paths re-sent ~110 rune of per-snapshot prefix on every line of every
// Turn, and the shared snapshot's hash directory on top of that. Opening the
// longest *common* directory instead recovers only the outer prefix: the
// entries span two roots beneath it (the agent's own context and the shared
// snapshot), so the common prefix stops above the hash directory, which then
// repeats on six lines. The budget guard caught exactly that.
//
// So the choice is made by costing the rendered result. Two earlier versions
// decided per directory and each failed a case a reader gets right at a glance:
// one left a heading behind for a directory holding a single entry, and the
// other let the shallowest shared directory swallow the deeper ones, so /home
// was opened and the hash directory under it was never reached. Both were
// fixing a local rule that is really a global trade — a heading costs a line
// but removes a prefix from everything below.
//
// The tree of directories is laminar, which is what makes an exact answer
// cheap: each directory is opened or closed independently of its siblings, and
// the only context a subtree needs from above is the nearest open ancestor.
func availableContextRoots(paths []string) []string {
	root := buildContextTree(paths)
	if root == nil {
		return nil
	}
	chooseContextRoots(root, "")
	return openContextRoots(root, nil)
}

type contextTreeNode struct {
	Directory string
	// key is the path from the tree root, used to identify the node.
	key      string
	files    []string
	children []*contextTreeNode
	byName   map[string]*contextTreeNode
	opened   bool
}

func buildContextTree(paths []string) *contextTreeNode {
	if len(paths) == 0 {
		return nil
	}
	root := &contextTreeNode{key: "", byName: map[string]*contextTreeNode{}}
	for _, path := range paths {
		segments := splitPathSegments(path)
		if len(segments) == 0 {
			continue
		}
		node := root
		// The leading empty segment of an absolute path is the separator
		// itself, which every directory already carries in its name. A UNC
		// path has two of them, so the empties are skipped rather than
		// consumed once: treating the second as a name would create a child
		// whose directory equals its parent's, and two nodes sharing a
		// directory is what made the UNC case below fail.
		start := 0
		if segments[0] == "" {
			start = 1
			node = ensureContextChild(root, "/")
		}
		for index := start; index < len(segments)-1; index++ {
			if segments[index] == "" {
				continue
			}
			node = ensureContextChild(node, segments[index])
		}
		node.files = append(node.files, segments[len(segments)-1])
	}
	return root
}

func ensureContextChild(parent *contextTreeNode, name string) *contextTreeNode {
	if child, ok := parent.byName[name]; ok {
		return child
	}
	directory := name
	switch parent.Directory {
	case "":
	case "/":
		// The filesystem root already carries its separator.
		directory = "/" + name
	default:
		directory = parent.Directory + "/" + name
	}
	child := &contextTreeNode{
		Directory: directory,
		key:       parent.key + "/" + name,
		byName:    map[string]*contextTreeNode{},
	}
	parent.byName[name] = child
	parent.children = append(parent.children, child)
	return child
}

// chooseContextRoots records which directories to print, and returns the cost
// of rendering the subtree below openAncestor.
//
// Costing and choosing are separate passes on purpose. Setting the flag in the
// same pass that weighs the two branches is wrong however it is written: the
// recursive calls that produce the "closed" figure also set flags on the
// children, so the last branch evaluated leaves its marks behind even when the
// other branch won. The answers still look plausible, which is what makes it
// worth splitting rather than patching.
func chooseContextRoots(node *contextTreeNode, openAncestor string) int {
	costs := map[string]int{}
	total := contextRootCosts(node, openAncestor, costs)
	recordContextRoots(node, openAncestor, costs)
	return total
}

// contextRootCostKey identifies a node by its path from the tree root rather
// than by Directory. Keying on the directory would collide as soon as two nodes
// carry the same one, which a malformed path shape can produce, and the
// collision does not fail loudly: the second node silently reuses the first
// node's cost and the roots chosen are merely wrong.
func contextRootCostKey(node *contextTreeNode, openAncestor string) string {
	return node.key + "\x00" + openAncestor
}

func contextRootCosts(node *contextTreeNode, openAncestor string, memo map[string]int) int {
	key := contextRootCostKey(node, openAncestor)
	if cost, ok := memo[key]; ok {
		return cost
	}
	closed := 0
	for _, file := range node.files {
		closed += len(relativeToContextRoot(openAncestor, joinContextFile(node, file))) + 1
	}
	for _, child := range node.children {
		closed += contextRootCosts(child, openAncestor, memo)
	}
	best := closed
	if node.Directory != "" {
		opened := len(availableContextRootHeading(node.Directory)) + 1
		for _, file := range node.files {
			opened += len(relativeToContextRoot(node.Directory, joinContextFile(node, file))) + 1
		}
		for _, child := range node.children {
			opened += contextRootCosts(child, node.Directory, memo)
		}
		if opened < closed {
			best = opened
		}
	}
	memo[key] = best
	return best
}

// recordContextRoots replays the costing to mark the winning directories.
func recordContextRoots(node *contextTreeNode, openAncestor string, costs map[string]int) {
	closed := 0
	for _, file := range node.files {
		closed += len(relativeToContextRoot(openAncestor, joinContextFile(node, file))) + 1
	}
	for _, child := range node.children {
		closed += costs[contextRootCostKey(child, openAncestor)]
	}
	chosen := openAncestor
	if node.Directory != "" {
		opened := len(availableContextRootHeading(node.Directory)) + 1
		for _, file := range node.files {
			opened += len(relativeToContextRoot(node.Directory, joinContextFile(node, file))) + 1
		}
		for _, child := range node.children {
			opened += costs[contextRootCostKey(child, node.Directory)]
		}
		if opened < closed {
			node.opened = true
			chosen = node.Directory
		}
	}
	for _, child := range node.children {
		recordContextRoots(child, chosen, costs)
	}
}

// openContextRoots collects the marked directories in a stable order.
func openContextRoots(node *contextTreeNode, roots []string) []string {
	if node.opened {
		roots = append(roots, node.Directory)
	}
	for _, child := range node.children {
		roots = openContextRoots(child, roots)
	}
	return roots
}

func joinContextFile(node *contextTreeNode, file string) string {
	if node.Directory == "" {
		return file
	}
	return node.Directory + "/" + file
}

// coveringRoot returns the deepest printed root containing path, or "" when the
// path stands outside every root and must stay absolute.
func coveringRoot(roots []string, path string) string {
	segments := comparablePathSegments(path)
	chosen := ""
	for _, root := range roots {
		rootSegments := comparablePathSegments(root)
		if len(rootSegments) < len(segments) && hasPathPrefix(segments, rootSegments) && len(root) > len(chosen) {
			chosen = root
		}
	}
	return chosen
}

// isUNCPath reports whether a path names a network share, written either as
// \\host\share or //host/share.
func isUNCPath(path string) bool {
	return strings.HasPrefix(path, "//") || strings.HasPrefix(path, `\\`)
}

func relativeToContextRoot(root, path string) string {
	if root == "" {
		return path
	}
	return joinPathTail(comparablePathSegments(path), len(comparablePathSegments(root)))
}

func splitPathSegments(value string) []string {
	return strings.Split(strings.TrimRight(strings.ReplaceAll(value, "\\", "/"), "/"), "/")
}

// comparablePathSegments splits a path for comparison, dropping the empty
// segments that leading separators produce. A UNC path starts with two of them
// and an absolute path with one, while a directory name built by the tree
// builder carries exactly one, so comparing raw segments would find that
// \\server\share shares nothing with the root /server/share chosen for it.
func comparablePathSegments(value string) []string {
	segments := splitPathSegments(value)
	start := 0
	for start < len(segments) && segments[start] == "" {
		start++
	}
	return segments[start:]
}

func hasPathPrefix(segments, prefix []string) bool {
	if len(prefix) >= len(segments) {
		return false
	}
	for index, segment := range prefix {
		if segment != segments[index] {
			return false
		}
	}
	return true
}

// joinPathTail renders the segments below the first depth of a path.
func joinPathTail(segments []string, depth int) string {
	if depth == 0 {
		return strings.Join(segments, "/")
	}
	return strings.Join(segments[depth:], "/")
}

func knowledgeResources(input BuildInput, root string) []ContextResource {
	directory := joinContextPath(input, root, "knowledge")
	resources := []ContextResource{}
	if guide := strings.TrimSpace(input.KnowledgeGuide); guide != "" {
		description := "知识库使用指南：目录结构、读写边界、类型、反馈和 Agent API 限制"
		if domain.NormalizeLanguage(input.Language) == domain.LanguageEnglish {
			description = "Knowledge-base guide: layout, scope boundaries, types, feedback, and Agent API limits"
		}
		resources = append(resources, resource(input, "knowledge-guide", joinContextPath(input, directory, "guide.md"), description, guide))
	}
	resources = append(resources, knowledgeScopeResources(input, directory, "global", input.GlobalKnowledge)...)
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
	english := domain.NormalizeLanguage(input.Language) == domain.LanguageEnglish
	title := "# 待修订知识"
	intro := "以下文档已被标记为过时或错误，不得作为当前事实使用。仅当本轮工作获得相关代码、测试或文档证据时，读取对应旧文档并通过 Agent Knowledge API 提交完整修订提案。不要脱离当前任务猜测更新，也不要直接恢复 verified。"
	pendingBodyTitle := "## 已发布但待修订的内容"
	staleDescription := "待修订知识队列；仅在当前工作提供相关证据时读取并提交修订提案"
	if english {
		title = "# Knowledge pending revision"
		intro = "The documents below are marked stale or wrong and must not be used as current facts. Only when this Turn has relevant code, test, or documentation evidence, read the old document and submit a complete revision proposal through the Agent Knowledge API. Do not guess updates or restore verified directly."
		pendingBodyTitle = "## Published content pending revision"
		staleDescription = "Knowledge pending revision; read only when this Turn has relevant evidence and submit a revision proposal"
	}
	lines := []string{
		title,
		"",
		intro,
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
		staleSuffix := "（待修订，非有效知识）"
		if english {
			staleSuffix = " (stale, not current knowledge)"
		}
		body := fmt.Sprintf("# %s\n\n- entry_id: %s\n- base_revision: %d\n- scope: %s\n- project_id: %s\n- type: %s\n- feedback: %s\n\n%s\n\n%s\n", entry.Title, entry.ID, entry.Revision, entry.Scope, entry.ProjectID, entry.Type, feedback, pendingBodyTitle, entry.Body)
		resources = append(resources, detailResource(input, "knowledge-stale-"+entry.ID, detailPath, entry.Title+staleSuffix, body))
	}
	index := resource(input, "knowledge-pending-updates", queuePath, staleDescription, strings.Join(lines, "\n"))
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
	english := domain.NormalizeLanguage(input.Language) == domain.LanguageEnglish
	globalTitle, globalDescription, globalEmpty := "全局知识", "这是 AHA2 全局知识页面。通用知识以人类阅读为主，Agent 仅在任务主题明确相关时按需读取；Agent 经验教训用于沉淀可跨项目复用的技术诊断与行为教训。请根据分类、标题与摘要选择阅读路径。", "当前没有可用的全局知识文档。"
	if english {
		globalTitle = "Global knowledge"
		globalDescription = "This is AHA2's global knowledge page. General knowledge is primarily for human reading; Agents read it only when clearly relevant to the Task topic. Agent lessons capture technical diagnostics and behavioral lessons reusable across projects. Choose reading paths from the categories, titles, and summaries."
		globalEmpty = "No global knowledge documents are available."
	}
	specs := []indexSpec{{
		id: "knowledge-global-index", path: joinContextPath(input, scopeDirectory, "index.md"),
		title: globalTitle, description: globalDescription,
		empty: globalEmpty, rootBacked: true,
		include: func(domain.KnowledgeEntry) bool { return true },
	}}
	if scope == "project" {
		projectTitle, projectDescription, projectEmpty := "项目知识", "这是当前项目的知识页面，保存项目实践、技术决策、诊断结论和可复用经验。请根据下面的标题与摘要按需打开文档。", "当前没有可用的项目知识文档。"
		navigationTitle, navigationDescription, navigationEmpty := "项目导航", "这是当前项目的导航页面，按模块、代码路径、边界与关键流程组织入口。请根据下面的标题与摘要选择阅读路径。", "当前没有可用的项目导航文档。"
		if english {
			projectTitle = "Project knowledge"
			projectDescription = "This page holds the current project's knowledge: practices, technical decisions, diagnostic conclusions, and reusable experience. Open documents as needed from the titles and summaries below."
			projectEmpty = "No project knowledge documents are available."
			navigationTitle = "Project navigation"
			navigationDescription = "This page organizes the current project's entry points by module, code path, boundary, and key flow. Choose reading paths from the titles and summaries below."
			navigationEmpty = "No project navigation documents are available."
		}
		specs = []indexSpec{
			{
				id: "knowledge-project-index", path: joinContextPath(input, scopeDirectory, "index.md"),
				title: projectTitle, description: projectDescription,
				empty: projectEmpty, rootBacked: true,
				include: func(domain.KnowledgeEntry) bool { return true },
			},
			{
				id: "knowledge-project-navigation-index", path: joinContextPath(input, scopeDirectory, "navigation", "index.md"),
				title: navigationTitle, description: navigationDescription,
				empty:   navigationEmpty,
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

// skillPackageDirectory resolves the per-skill context directory. The layout
// is scope-first: skills/global/<slug>/ and
// skills/project/<projectID>/<slug>/. A same-scope duplicate slug receives a
// numeric suffix in encounter order instead of silently overwriting the earlier
// package. usedDirectories is shared across the whole skill list.
func skillPackageDirectory(input BuildInput, skillsRoot string, skill domain.Skill, usedDirectories map[string]bool) string {
	slug := contextPathSegment(skill.PackageSlug)
	if slug == "" {
		slug = contextPathSegment(skill.ID)
	}
	if slug == "" {
		slug = "skill"
	}
	segments := []string{}
	switch scope := strings.ToLower(strings.TrimSpace(skill.Scope)); scope {
	case "project":
		project := contextPathSegment(skill.ProjectID)
		if project == "" {
			project = "_unbound"
		}
		segments = append(segments, "project", project, slug)
	case "", "global":
		segments = append(segments, "global", slug)
	default:
		segments = append(segments, scope, slug)
	}
	base := segments[len(segments)-1]
	for suffix := 2; ; suffix++ {
		key := strings.Join(segments, "/")
		if !usedDirectories[key] {
			usedDirectories[key] = true
			return joinContextPath(input, append([]string{skillsRoot}, segments...)...)
		}
		segments[len(segments)-1] = fmt.Sprintf("%s-%d", base, suffix)
	}
}

// contextPathSegment makes one prompt path component safe. Scope, project and
// slug values come from vault metadata, so path separators, control characters
// and dot-only names must never escape the intended directory.
func contextPathSegment(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var builder strings.Builder
	for _, char := range raw {
		switch {
		case char == '/' || char == '\\':
			builder.WriteRune('-')
		case char < 0x20 || char == 0x7f:
			builder.WriteRune('-')
		default:
			builder.WriteRune(char)
		}
	}
	value := strings.Trim(builder.String(), ". ")
	if value == "" || value == "_" {
		return ""
	}
	return value
}

func skillResources(input BuildInput, root string) []ContextResource {
	directory := joinContextPath(input, root, "skills")
	resources := []ContextResource{}
	// Skill packages are namespaced by scope and project so a global package
	// and a project package that share a slug never overwrite each other.
	// Duplicates inside the same scope still get a deterministic suffix.
	usedDirectories := map[string]bool{}
	for _, skill := range input.Skills {
		packageDirectory := skillPackageDirectory(input, directory, skill, usedDirectories)
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

func recentContextSender(item domain.ConversationItem) (string, string) {
	sender := strings.TrimSpace(item.FromAgentID)
	if sender == "" {
		sender = "owner"
	}
	channelContext, _ := item.Payload["channel_context"].(map[string]any)
	actor, _ := channelContext["actor"].(map[string]any)
	if name := strings.TrimSpace(fmt.Sprint(actor["display_name"])); name != "" && name != "<nil>" {
		sender = name
	}
	role := strings.TrimSpace(fmt.Sprint(actor["role"]))
	if role == "<nil>" {
		role = ""
	}
	return sender, role
}

func recentContextExchanges(items []domain.ConversationItem, currentTurnID, currentRoundID string) []recentContextExchange {
	type exchange struct {
		users     []recentContextUser
		reply     string
		replyTime time.Time
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
			sender, role := recentContextSender(item)
			current.users = append(current.users, recentContextUser{Sender: sender, Role: role, Time: FormatPromptTime(item.CreatedAt), Summary: summary})
		case "agent_message":
			if item.AgentID != "" && item.AgentID != "main" {
				continue
			}
			if item.RouteKind != "" && item.RouteKind != "turn_result" {
				continue
			}
			current.reply = summary
			current.replyTime = item.CreatedAt
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
			Time:   FormatPromptTime(item.replyTime),
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
		if item.HasNetwork && group.Network.Protocol == domain.HardwareProtocolADB {
			item.ADBConnection = group.Network.NormalizedADB().ADBConnection
		}
		if item.HasNetwork && group.Network.ADBUSB() {
			item.ADBUSB, item.ADBSerial = true, group.Network.ADBSerial
		}
		if item.HasNetwork && group.Network.Protocol == domain.HardwareProtocolSSH {
			item.SSHAuthentication = normalizeHardwareSSHAuth(group.Network.SSHAuth)
		}
		result = append(result, item)
	}
	return result
}

func mailContextAccounts(accounts []domain.MailAccount, defaultTemplates map[string]string) []mailContextAccount {
	result := make([]mailContextAccount, 0, len(accounts))
	for _, account := range accounts {
		aliases := make([]string, 0, len(account.Aliases))
		for _, alias := range account.Aliases {
			if strings.TrimSpace(alias.Address) != "" {
				aliases = append(aliases, strings.TrimSpace(alias.Address))
			}
		}
		result = append(result, mailContextAccount{
			ID: account.ID, FromAddress: account.Address, DisplayName: account.DisplayName,
			Aliases: aliases, Status: string(account.Status),
			CredentialConfigured: account.CredentialConfigured,
			DefaultTemplateID:    defaultTemplates[account.ID],
		})
	}
	return result
}

func mailContextTemplates(templates []domain.MailTemplate) []mailContextTemplate {
	result := make([]mailContextTemplate, 0, len(templates))
	for _, template := range templates {
		mode := template.Mode
		if mode == "" {
			mode = domain.MailTemplateModeRender
		}
		variables := ""
		if mode == domain.MailTemplateModeRender {
			variables = strings.Join(template.RequiredVariables, ", ")
		}
		result = append(result, mailContextTemplate{
			ID: template.ID, Name: template.Name, Version: template.CurrentVersion,
			Mode: string(mode), Scope: string(template.Scope), ProjectID: template.ProjectID,
			Variables: variables,
		})
	}
	return result
}

// Context is a small index, not a content dump. A guide template bound as the
// account default is the one exception: the Agent needs its reference sample to
// compose without an extra tool round-trip. Long samples are truncated here
// with an explicit marker rendered by the mail template.
const (
	maxMailGuideDescriptionRunes = 2000
	maxMailGuideBodyRunes        = 6000
)

func mailContextGuideTemplates(templates []domain.MailTemplate, defaultTemplates map[string]string) []mailContextGuideTemplate {
	if len(defaultTemplates) == 0 {
		return nil
	}
	defaultIDs := make(map[string]bool, len(defaultTemplates))
	for _, id := range defaultTemplates {
		if strings.TrimSpace(id) != "" {
			defaultIDs[strings.TrimSpace(id)] = true
		}
	}
	result := make([]mailContextGuideTemplate, 0, len(defaultIDs))
	seen := make(map[string]bool, len(defaultIDs))
	for _, template := range templates {
		if !defaultIDs[template.ID] || seen[template.ID] {
			continue
		}
		if template.Mode != domain.MailTemplateModeGuide {
			continue
		}
		seen[template.ID] = true
		description, descriptionTruncated := truncateContextText(template.Description, maxMailGuideDescriptionRunes)
		bodyText, bodyTextTruncated := truncateContextText(template.BodyTextTemplate, maxMailGuideBodyRunes)
		bodyHTML, bodyHTMLTruncated := truncateContextText(template.BodyHTMLTemplate, maxMailGuideBodyRunes)
		result = append(result, mailContextGuideTemplate{
			ID: template.ID, Name: template.Name, Version: template.CurrentVersion,
			Scope: string(template.Scope), Description: description,
			Subject: template.SubjectTemplate, BodyText: bodyText, BodyHTML: bodyHTML,
			BodyTextTruncated: bodyTextTruncated, BodyHTMLTruncated: bodyHTMLTruncated,
			DescriptionTruncated: descriptionTruncated,
		})
	}
	return result
}

func truncateContextText(value string, limitRunes int) (string, bool) {
	if limitRunes <= 0 {
		return "", value != ""
	}
	runes := []rune(value)
	if len(runes) <= limitRunes {
		return value, false
	}
	return string(runes[:limitRunes]), true
}

func normalizeHardwareSSHAuth(value string) string {
	switch value {
	case domain.HardwareSSHAuthPassword, domain.HardwareSSHAuthKey:
		return value
	default:
		return domain.HardwareSSHAuthAuto
	}
}

// RenderTemplate renders a named builtin/override template with arbitrary data.
// It lets feature prompts share the prompt registry without going through the
// full Task prompt builder.
func (engine *Engine) RenderTemplate(ctx context.Context, id string, data any) (string, error) {
	templates, err := engine.Templates(ctx)
	if err != nil {
		return "", err
	}
	for _, item := range templates {
		if item.ID != id {
			continue
		}
		parsed, err := template.New(id).Option("missingkey=error").Parse(item.Content)
		if err != nil {
			return "", err
		}
		var output bytes.Buffer
		if err := parsed.Execute(&output, data); err != nil {
			return "", err
		}
		return strings.TrimSpace(output.String()), nil
	}
	return "", fmt.Errorf("prompt template %s not found", id)
}
