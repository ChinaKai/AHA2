package httpapi

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/channel"
	"github.com/ChinaKai/AHA2/internal/codexaccount"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"github.com/ChinaKai/AHA2/internal/managedprocess"
	"github.com/ChinaKai/AHA2/internal/store"
)

const sessionCookieName = "aha2_session"

type Config struct {
	Store             *store.Store
	Auth              *auth.Service
	App               *app.Service
	Web               fs.FS
	Logger            *slog.Logger
	SecureCookie      bool
	AllowCrossOrigin  bool
	DetectWorkspace   func(context.Context, domain.Workspace) (domain.Workspace, error)
	Secrets           SecretStore
	Hardware          *hardware.Manager
	CodexAccounts     *codexaccount.Manager
	AgentCapabilities *agentapi.Capabilities
	ManagedProcesses  *managedprocess.Manager
	Channels          *channel.Service
	ProbeSSHHostKey   func(context.Context, string) (hardware.SSHHostKeyInfo, error)
	TrustSSHHostKey   func(context.Context, string, string) (hardware.SSHHostKeyInfo, error)
	Version           string
	StartedAt         time.Time
}

type Server struct {
	store                 *store.Store
	auth                  *auth.Service
	app                   *app.Service
	web                   fs.FS
	logger                *slog.Logger
	secureCookie          bool
	originPolicyMu        sync.RWMutex
	validateOrigin        bool
	originStartupOverride bool
	detectWorkspace       func(context.Context, domain.Workspace) (domain.Workspace, error)
	secrets               SecretStore
	hardware              *hardware.Manager
	codexAccounts         *codexaccount.Manager
	agentCapabilities     *agentapi.Capabilities
	managedProcesses      *managedprocess.Manager
	channels              *channel.Service
	probeSSHHostKey       func(context.Context, string) (hardware.SSHHostKeyInfo, error)
	trustSSHHostKey       func(context.Context, string, string) (hardware.SSHHostKeyInfo, error)
	version               string
	startedAt             time.Time
	syncRunMu             sync.RWMutex
	syncRun               syncRunProgress
	authLimiter           *authLimiter
	modelDetectionJobs    *modelDetectionJobs
	systemMu              sync.Mutex
	systemCachedAt        time.Time
	systemCache           map[string]any
}

func New(config Config) *Server {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	startedAt := config.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	probeSSHHostKey := config.ProbeSSHHostKey
	if probeSSHHostKey == nil {
		probeSSHHostKey = hardware.ProbeSSHHostKey
	}
	trustSSHHostKey := config.TrustSSHHostKey
	if trustSSHHostKey == nil {
		trustSSHHostKey = hardware.TrustSSHHostKey
	}
	validateOrigin := !config.AllowCrossOrigin
	if config.Store != nil && !config.AllowCrossOrigin {
		if settings, err := config.Store.SecuritySettings(context.Background()); err == nil {
			validateOrigin = settings.ValidateOrigin
		}
	}
	return &Server{
		store: config.Store, auth: config.Auth, app: config.App, web: config.Web,
		logger: logger, secureCookie: config.SecureCookie, validateOrigin: validateOrigin,
		originStartupOverride: config.AllowCrossOrigin,
		detectWorkspace:       config.DetectWorkspace,
		secrets:               config.Secrets,
		hardware:              config.Hardware,
		codexAccounts:         config.CodexAccounts,
		agentCapabilities:     config.AgentCapabilities,
		managedProcesses:      config.ManagedProcesses,
		channels:              config.Channels,
		probeSSHHostKey:       probeSSHHostKey,
		trustSSHHostKey:       trustSSHHostKey,
		version:               config.Version,
		startedAt:             startedAt,
		authLimiter:           newAuthLimiter(),
		modelDetectionJobs:    newModelDetectionJobs(),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/v1/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/v1/auth/register", s.authRegister)
	mux.HandleFunc("POST /api/v1/auth/login", s.authLogin)
	mux.HandleFunc("POST /api/v1/auth/recover", s.authRecover)
	mux.Handle("POST /api/channel-runtime/v1/handshake", s.withChannelCapability("", http.HandlerFunc(s.channelRuntimeHandshake)))
	mux.Handle("PUT /api/channel-runtime/v1/instances/{instance_id}/health", s.withChannelCapability("channel.health.write", http.HandlerFunc(s.channelRuntimeHealth)))
	mux.Handle("POST /api/channel-runtime/v1/instances/{instance_id}/commands:claim", s.withChannelCapability("channel.command.claim", http.HandlerFunc(s.channelRuntimeClaimCommands)))
	mux.Handle("POST /api/channel-runtime/v1/instances/{instance_id}/inbound-events", s.withChannelCapability("channel.inbound.write", http.HandlerFunc(s.channelRuntimeInbound)))
	mux.Handle("POST /api/channel-runtime/v1/commands/{id}/progress", s.withChannelCapability("channel.command.progress", http.HandlerFunc(s.channelRuntimeCommandProgress)))
	mux.Handle("POST /api/channel-runtime/v1/commands/{id}/complete", s.withChannelCapability("channel.command.complete", http.HandlerFunc(s.channelRuntimeCommandComplete)))
	mux.Handle("POST /api/channel-runtime/v1/instances/{instance_id}/deliveries:claim", s.withChannelCapability("channel.delivery.claim", http.HandlerFunc(s.channelRuntimeClaimDeliveries)))
	mux.Handle("POST /api/channel-runtime/v1/deliveries/{id}/ack", s.withChannelCapability("channel.delivery.ack", http.HandlerFunc(s.channelRuntimeDeliveryAck)))
	mux.Handle("POST /api/channel-runtime/v1/deliveries/{id}/nack", s.withChannelCapability("channel.delivery.ack", http.HandlerFunc(s.channelRuntimeDeliveryNack)))
	mux.Handle("POST /api/channel-runtime/v1/commands/{id}/attachment", s.withChannelCapability("channel.media.upload", http.HandlerFunc(s.channelRuntimeUploadMedia)))
	mux.Handle("GET /api/channel-runtime/v1/deliveries/{id}/attachment", s.withChannelCapability("channel.media.read", http.HandlerFunc(s.channelRuntimeMediaContent)))
	mux.Handle("POST /api/channel-runtime/v1/deliveries/{id}/media", s.withChannelCapability("channel.media.read", http.HandlerFunc(s.channelRuntimeMediaUploaded)))
	mux.Handle("POST /api/v1/auth/password", s.withAuth(http.HandlerFunc(s.authChangePassword)))
	mux.Handle("POST /api/v1/auth/logout", s.withAuth(http.HandlerFunc(s.authLogout)))

	mux.Handle("GET /api/v1/system", s.withAuth(http.HandlerFunc(s.systemInfo)))
	mux.Handle("GET /api/v1/settings/proxy", s.withAuth(http.HandlerFunc(s.proxySettings)))
	mux.Handle("PUT /api/v1/settings/proxy", s.withAuth(http.HandlerFunc(s.updateProxySettings)))
	mux.Handle("POST /api/v1/settings/proxy/test", s.withAuth(http.HandlerFunc(s.testProxySettings)))
	mux.Handle("GET /api/v1/settings/security", s.withAuth(http.HandlerFunc(s.securitySettings)))
	mux.Handle("PUT /api/v1/settings/security", s.withAuth(http.HandlerFunc(s.updateSecuritySettings)))
	mux.Handle("GET /api/v1/settings/agent-api", s.withAuth(http.HandlerFunc(s.agentAPISettings)))
	mux.Handle("PUT /api/v1/settings/agent-api", s.withAuth(http.HandlerFunc(s.updateAgentAPISettings)))
	mux.Handle("GET /api/v1/settings/backend", s.withAuth(http.HandlerFunc(s.backendSettings)))
	mux.Handle("PUT /api/v1/settings/backend", s.withAuth(http.HandlerFunc(s.updateBackendSettings)))
	mux.Handle("GET /api/v1/settings/knowledge-review", s.withAuth(http.HandlerFunc(s.knowledgeReviewSettings)))
	mux.Handle("PUT /api/v1/settings/knowledge-review", s.withAuth(http.HandlerFunc(s.updateKnowledgeReviewSettings)))
	mux.Handle("GET /api/v1/settings/sync", s.withAuth(http.HandlerFunc(s.syncSettings)))
	mux.Handle("PUT /api/v1/settings/sync", s.withAuth(http.HandlerFunc(s.updateSyncSettings)))
	mux.Handle("GET /api/v1/settings/sync/status", s.withAuth(http.HandlerFunc(s.syncStatus)))
	mux.Handle("GET /api/v1/settings/sync/preview", s.withAuth(http.HandlerFunc(s.syncPreview)))
	mux.Handle("POST /api/v1/settings/sync/run", s.withAuth(http.HandlerFunc(s.runSync)))
	mux.Handle("GET /api/v1/settings/sync/conflicts", s.withAuth(http.HandlerFunc(s.syncConflicts)))
	mux.Handle("GET /api/v1/channel-providers", s.withAuth(http.HandlerFunc(s.channelProviders)))
	mux.Handle("PATCH /api/v1/channel-plugins/{id}", s.withAuth(http.HandlerFunc(s.updateChannelPlugin)))
	mux.Handle("GET /api/v1/channel-instances", s.withAuth(http.HandlerFunc(s.channelInstances)))
	mux.Handle("POST /api/v1/channel-instances", s.withAuth(http.HandlerFunc(s.createChannelInstance)))
	mux.Handle("GET /api/v1/channel-instances/{id}", s.withAuth(http.HandlerFunc(s.channelInstance)))
	mux.Handle("PATCH /api/v1/channel-instances/{id}", s.withAuth(http.HandlerFunc(s.updateChannelInstance)))
	mux.Handle("POST /api/v1/channel-instances/{id}/reset-binding", s.withAuth(http.HandlerFunc(s.resetChannelBinding)))
	mux.Handle("POST /api/v1/channel-instances/{id}/archive", s.withAuth(http.HandlerFunc(s.archiveChannelInstance)))
	mux.Handle("GET /api/v1/channel-instances/{id}/purge-preview", s.withAuth(http.HandlerFunc(s.channelPurgePreview)))
	mux.Handle("POST /api/v1/channel-instances/{id}/purge", s.withAuth(http.HandlerFunc(s.purgeChannelInstance)))
	mux.Handle("PUT /api/v1/channel-instances/{id}/credentials", s.withAuth(http.HandlerFunc(s.updateChannelCredentials)))
	mux.Handle("POST /api/v1/channel-instances/{id}/onboarding-sessions", s.withAuth(http.HandlerFunc(s.startChannelOnboarding)))
	mux.Handle("GET /api/v1/channel-onboarding-sessions/{id}", s.withAuth(http.HandlerFunc(s.channelOnboarding)))
	mux.Handle("GET /api/v1/channel-onboarding-sessions/{id}/qr", s.withAuth(http.HandlerFunc(s.channelOnboardingQR)))
	mux.Handle("POST /api/v1/channel-onboarding-sessions/{id}/cancel", s.withAuth(http.HandlerFunc(s.cancelChannelOnboarding)))
	mux.Handle("GET /api/v1/channel-instances/{id}/handoffs", s.withAuth(http.HandlerFunc(s.channelHandoffs)))
	mux.Handle("GET /api/v1/channel-instances/{id}/deliveries", s.withAuth(http.HandlerFunc(s.channelDeliveries)))
	mux.Handle("POST /api/v1/channel-deliveries/{id}/replay", s.withAuth(http.HandlerFunc(s.replayChannelDelivery)))
	mux.Handle("POST /api/v1/channel-deliveries/{id}/skip", s.withAuth(http.HandlerFunc(s.skipChannelDelivery)))
	mux.Handle("GET /api/v1/channel-instances/{id}/knowledge-policy", s.withAuth(http.HandlerFunc(s.channelKnowledgePolicy)))
	mux.Handle("PUT /api/v1/channel-instances/{id}/knowledge-policy", s.withAuth(http.HandlerFunc(s.updateChannelKnowledgePolicy)))
	mux.Handle("GET /api/v1/channel-instances/{id}/knowledge-records", s.withAuth(http.HandlerFunc(s.channelKnowledgeRecords)))
	mux.Handle("POST /api/v1/channel-knowledge-records/{id}/promote", s.withAuth(http.HandlerFunc(s.promoteChannelKnowledgeRecord)))
	mux.Handle("GET /api/v1/projects", s.withAuth(http.HandlerFunc(s.listProjects)))
	mux.Handle("POST /api/v1/projects", s.withAuth(http.HandlerFunc(s.createProject)))
	mux.Handle("PUT /api/v1/projects/{id}", s.withAuth(http.HandlerFunc(s.updateProject)))
	mux.Handle("DELETE /api/v1/projects/{id}", s.withAuth(http.HandlerFunc(s.deleteProject)))
	mux.Handle("GET /api/v1/workspaces", s.withAuth(http.HandlerFunc(s.listWorkspaces)))
	mux.Handle("POST /api/v1/workspaces", s.withAuth(http.HandlerFunc(s.createWorkspace)))
	mux.Handle("PUT /api/v1/workspaces/{id}", s.withAuth(http.HandlerFunc(s.updateWorkspace)))
	mux.Handle("DELETE /api/v1/workspaces/{id}", s.withAuth(http.HandlerFunc(s.deleteWorkspace)))
	mux.Handle("DELETE /api/v1/workspaces/{id}/remote-mirror", s.withAuth(http.HandlerFunc(s.retireRemoteWorkspaceMirror)))
	mux.Handle("POST /api/v1/workspaces/{id}/takeover", s.withAuth(http.HandlerFunc(s.takeoverWorkspace)))
	mux.Handle("POST /api/v1/workspaces/{id}/detect", s.withAuth(http.HandlerFunc(s.detectWorkspaceHandler)))
	mux.Handle("GET /api/v1/workspaces/{id}/host-key", s.withAuth(http.HandlerFunc(s.workspaceSSHHostKey)))
	mux.Handle("POST /api/v1/workspaces/{id}/host-key/trust", s.withAuth(http.HandlerFunc(s.trustWorkspaceSSHHostKey)))
	mux.Handle("GET /api/v1/providers", s.withAuth(http.HandlerFunc(s.listProviders)))
	mux.Handle("POST /api/v1/providers", s.withAuth(http.HandlerFunc(s.createProvider)))
	mux.Handle("PUT /api/v1/providers/{id}", s.withAuth(http.HandlerFunc(s.updateProvider)))
	mux.Handle("DELETE /api/v1/providers/{id}", s.withAuth(http.HandlerFunc(s.deleteProvider)))
	mux.Handle("GET /api/v1/models", s.withAuth(http.HandlerFunc(s.listModels)))
	mux.Handle("POST /api/v1/models", s.withAuth(http.HandlerFunc(s.createModel)))
	mux.Handle("PUT /api/v1/models/{id}", s.withAuth(http.HandlerFunc(s.updateModel)))
	mux.Handle("DELETE /api/v1/models/{id}", s.withAuth(http.HandlerFunc(s.deleteModel)))
	mux.Handle("GET /api/v1/env-groups", s.withAuth(http.HandlerFunc(s.listEnvGroups)))
	mux.Handle("POST /api/v1/env-groups", s.withAuth(http.HandlerFunc(s.createEnvGroup)))
	mux.Handle("POST /api/v1/providers/detect-models", s.withAuth(http.HandlerFunc(s.detectModelsHandler)))
	mux.Handle("POST /api/v1/providers/{id}/model-detection-jobs", s.withAuth(http.HandlerFunc(s.createModelDetectionJob)))
	mux.Handle("GET /api/v1/providers/{id}/model-detection-jobs/{job}/events", s.withAuth(http.HandlerFunc(s.modelDetectionJobEvents)))
	mux.Handle("POST /api/v1/providers/{id}/model-detection-jobs/{job}/cancel", s.withAuth(http.HandlerFunc(s.cancelModelDetectionJob)))
	mux.Handle("POST /api/v1/providers/add-models", s.withAuth(http.HandlerFunc(s.addModelsHandler)))
	mux.Handle("GET /api/v1/codex-accounts", s.withAuth(http.HandlerFunc(s.listCodexAccounts)))
	mux.Handle("POST /api/v1/codex-accounts/import-local", s.withAuth(http.HandlerFunc(s.importLocalCodexAccount)))
	mux.Handle("POST /api/v1/codex-accounts/import", s.withAuth(http.HandlerFunc(s.importCodexAccount)))
	mux.Handle("POST /api/v1/codex-accounts/login", s.withAuth(http.HandlerFunc(s.startCodexAccountLogin)))
	mux.Handle("GET /api/v1/codex-accounts/login/{id}", s.withAuth(http.HandlerFunc(s.codexAccountLoginStatus)))
	mux.Handle("POST /api/v1/codex-accounts/login/{id}/callback", s.withAuth(http.HandlerFunc(s.submitCodexAccountCallback)))
	mux.Handle("DELETE /api/v1/codex-accounts/login/{id}", s.withAuth(http.HandlerFunc(s.cancelCodexAccountLogin)))
	mux.Handle("DELETE /api/v1/codex-accounts/{id}", s.withAuth(http.HandlerFunc(s.deleteCodexAccount)))
	mux.Handle("POST /api/v1/codex-accounts/{id}/refresh", s.withAuth(http.HandlerFunc(s.refreshCodexAccount)))
	mux.Handle("GET /api/v1/prompts/templates", s.withAuth(http.HandlerFunc(s.promptTemplates)))
	mux.Handle("PUT /api/v1/prompts/templates/{id}", s.withAuth(http.HandlerFunc(s.updatePromptTemplate)))
	mux.Handle("POST /api/v1/prompts/templates/{id}/reset", s.withAuth(http.HandlerFunc(s.resetPromptTemplate)))
	mux.Handle("GET /api/v1/hardware/serial-ports", s.withAuth(http.HandlerFunc(s.hardwareSerialPorts)))

	mux.Handle("GET /api/v1/tasks", s.withAuth(http.HandlerFunc(s.listTasks)))
	mux.Handle("POST /api/v1/tasks", s.withAuth(http.HandlerFunc(s.createTask)))
	mux.Handle("POST /api/v1/tasks/{id}/start", s.withAuth(http.HandlerFunc(s.startTask)))
	mux.Handle("DELETE /api/v1/tasks/{id}", s.withAuth(http.HandlerFunc(s.deleteTask)))
	mux.Handle("DELETE /api/v1/tasks/{id}/remote-mirror", s.withAuth(http.HandlerFunc(s.retireRemoteTaskMirror)))
	mux.Handle("POST /api/v1/tasks/{id}/takeover", s.withAuth(http.HandlerFunc(s.takeoverTask)))
	mux.Handle("GET /api/v1/tasks/{id}", s.withAuth(http.HandlerFunc(s.taskDetail)))
	mux.Handle("GET /api/v1/tasks/{id}/conversation", s.withAuth(http.HandlerFunc(s.taskConversation)))
	mux.Handle("GET /api/v1/tasks/{id}/context", s.withAuth(http.HandlerFunc(s.taskContext)))
	mux.Handle("POST /api/v1/tasks/{id}/messages", s.withAuth(http.HandlerFunc(s.submitMessage)))
	mux.Handle("POST /api/v1/tasks/{id}/attachments", s.withAuth(http.HandlerFunc(s.uploadTaskAttachment)))
	mux.Handle("GET /api/v1/tasks/{id}/attachments/{attachment}", s.withAuth(http.HandlerFunc(s.taskAttachmentContent)))
	mux.Handle("DELETE /api/v1/tasks/{id}/attachments/{attachment}", s.withAuth(http.HandlerFunc(s.deleteTaskAttachment)))
	mux.Handle("GET /api/v1/tasks/{id}/agents", s.withAuth(http.HandlerFunc(s.taskAgents)))
	mux.Handle("PATCH /api/v1/tasks/{id}/collaboration", s.withAuth(http.HandlerFunc(s.updateTaskCollaboration)))
	mux.Handle("GET /api/v1/tasks/{id}/agents/{agent}/conversation", s.withAuth(http.HandlerFunc(s.agentConversation)))
	mux.Handle("GET /api/v1/tasks/{id}/agents/{agent}/context", s.withAuth(http.HandlerFunc(s.agentContext)))
	mux.Handle("POST /api/v1/tasks/{id}/agents/{agent}/messages", s.withAuth(http.HandlerFunc(s.submitAgentMessage)))
	mux.Handle("PATCH /api/v1/tasks/{id}/agents/{agent}", s.withAuth(http.HandlerFunc(s.updateAgentConfig)))
	mux.Handle("POST /api/v1/tasks/{id}/agents/{agent}/session/compact", s.withAuth(http.HandlerFunc(s.compactAgentSession)))
	mux.Handle("POST /api/v1/tasks/{id}/agents/{agent}/session/reset", s.withAuth(http.HandlerFunc(s.resetAgentSession)))
	mux.Handle("GET /api/v1/tasks/{id}/hardware", s.withAuth(http.HandlerFunc(s.taskHardware)))
	mux.Handle("PUT /api/v1/tasks/{id}/hardware", s.withAuth(http.HandlerFunc(s.updateTaskHardware)))
	mux.Handle("GET /api/v1/tasks/{id}/hardware/{hardware}/terminal", s.withAuth(http.HandlerFunc(s.hardwareTerminal)))
	mux.Handle("GET /api/v1/tasks/{id}/hardware/{hardware}/terminal/ws", s.withAuth(http.HandlerFunc(s.hardwareTerminalWebSocket)))
	mux.Handle("POST /api/v1/tasks/{id}/hardware/{hardware}/connect", s.withAuth(http.HandlerFunc(s.connectHardware)))
	mux.Handle("GET /api/v1/tasks/{id}/hardware/{hardware}/host-key", s.withAuth(http.HandlerFunc(s.hardwareSSHHostKey)))
	mux.Handle("POST /api/v1/tasks/{id}/hardware/{hardware}/host-key/trust", s.withAuth(http.HandlerFunc(s.trustHardwareSSHHostKey)))
	mux.Handle("POST /api/v1/tasks/{id}/hardware/{hardware}/disconnect", s.withAuth(http.HandlerFunc(s.disconnectHardware)))
	mux.Handle("POST /api/v1/tasks/{id}/hardware/{hardware}/send", s.withAuth(http.HandlerFunc(s.sendHardware)))
	mux.Handle("GET /api/v1/agent/hardware", s.withAgentCapability(http.HandlerFunc(s.agentHardware)))
	mux.Handle("GET /api/v1/agent/hardware/{hardware}/terminal", s.withAgentCapability(http.HandlerFunc(s.agentHardwareTerminal)))
	mux.Handle("POST /api/v1/agent/hardware/{hardware}/connect", s.withAgentCapability(http.HandlerFunc(s.agentConnectHardware)))
	mux.Handle("POST /api/v1/agent/hardware/{hardware}/disconnect", s.withAgentCapability(http.HandlerFunc(s.agentDisconnectHardware)))
	mux.Handle("POST /api/v1/agent/hardware/{hardware}/send", s.withAgentCapability(http.HandlerFunc(s.agentSendHardware)))
	mux.Handle("POST /api/v1/agent/hardware/{hardware}/login", s.withAgentCapability(http.HandlerFunc(s.agentLoginHardware)))
	mux.Handle("GET /api/v1/agent/processes", s.withAgentCapability(http.HandlerFunc(s.agentProcesses)))
	mux.Handle("POST /api/v1/agent/processes", s.withAgentCapability(http.HandlerFunc(s.startAgentProcess)))
	mux.Handle("GET /api/v1/agent/processes/{name}", s.withAgentCapability(http.HandlerFunc(s.agentProcessStatus)))
	mux.Handle("POST /api/v1/agent/processes/{name}/stop", s.withAgentCapability(http.HandlerFunc(s.stopAgentProcess)))
	mux.Handle("GET /api/v1/agent/capabilities", s.withAgentCapability(http.HandlerFunc(s.agentCapabilitiesInfo)))
	mux.Handle("PATCH /api/v1/agent/turn/memory", s.withAgentCapability(http.HandlerFunc(s.updateAgentMemory)))
	mux.Handle("POST /api/v1/agent/turn/messages", s.withAgentCapability(http.HandlerFunc(s.addAgentProgressMessage)))
	mux.Handle("POST /api/v1/agent/turn/attachments", s.withAgentCapability(http.HandlerFunc(s.uploadAgentAttachment)))
	mux.Handle("GET /api/v1/agent/knowledge", s.withAgentCapability(http.HandlerFunc(s.agentKnowledge)))
	mux.Handle("GET /api/v1/agent/knowledge/{knowledge}", s.withAgentCapability(http.HandlerFunc(s.agentKnowledgeEntry)))
	mux.Handle("POST /api/v1/agent/knowledge/candidates", s.withAgentCapability(http.HandlerFunc(s.submitAgentKnowledge)))
	mux.Handle("POST /api/v1/agent/knowledge/{knowledge}/feedback", s.withAgentCapability(http.HandlerFunc(s.submitAgentKnowledgeFeedback)))
	mux.Handle("POST /api/v1/agent/collaboration/batches", s.withAgentCapability(http.HandlerFunc(s.submitAgentCollaboration)))
	mux.Handle("GET /api/v1/agent/skills", s.withAgentCapability(http.HandlerFunc(s.agentSkills)))
	mux.Handle("POST /api/v1/agent/skills", s.withAgentCapability(http.HandlerFunc(s.createAgentSkill)))
	mux.Handle("GET /api/v1/agent/skills/{skill}", s.withAgentCapability(http.HandlerFunc(s.agentSkill)))
	mux.Handle("PUT /api/v1/agent/skills/{skill}", s.withAgentCapability(http.HandlerFunc(s.updateAgentSkill)))
	mux.Handle("GET /api/v1/agent/project/workspaces", s.withAgentCapability(http.HandlerFunc(s.agentProjectWorkspaces)))
	mux.Handle("GET /api/v1/agent/project/runtimes", s.withAgentCapability(http.HandlerFunc(s.agentProjectRuntimes)))
	mux.Handle("GET /api/v1/agent/channel/context", s.withAgentCapability(http.HandlerFunc(s.agentChannelContext)))
	mux.Handle("GET /api/v1/agent/channel/catalog", s.withAgentCapability(http.HandlerFunc(s.agentChannelCatalog)))
	mux.Handle("POST /api/v1/agent/channel/actions/preview", s.withAgentCapability(http.HandlerFunc(s.previewAgentChannelAction)))
	mux.Handle("POST /api/v1/agent/channel/handoffs", s.withAgentCapability(http.HandlerFunc(s.createAgentChannelHandoff)))
	mux.Handle("POST /api/v1/agent/tasks", s.withAgentCapability(http.HandlerFunc(s.createAgentTask)))
	mux.Handle("GET /api/v1/agent/tasks/{task}", s.withAgentCapability(http.HandlerFunc(s.agentTaskStatus)))
	mux.Handle("PATCH /api/v1/tasks/{id}", s.withAuth(http.HandlerFunc(s.updateTaskTitle)))
	mux.Handle("POST /api/v1/tasks/{id}/complete", s.withAuth(http.HandlerFunc(s.completeTask)))
	mux.Handle("POST /api/v1/tasks/{id}/reopen", s.withAuth(http.HandlerFunc(s.reopenTask)))
	mux.Handle("POST /api/v1/turns/{id}/interrupt", s.withAuth(http.HandlerFunc(s.interruptTurn)))
	mux.Handle("POST /api/v1/rounds/{id}/interrupt", s.withAuth(http.HandlerFunc(s.interruptRound)))
	mux.Handle("GET /api/v1/tasks/{id}/events", s.withAuth(http.HandlerFunc(s.taskEvents)))
	mux.Handle("GET /api/v1/events", s.withAuth(http.HandlerFunc(s.allEvents)))

	mux.Handle("GET /api/v1/knowledge", s.withAuth(http.HandlerFunc(s.listKnowledge)))
	mux.Handle("GET /api/v1/knowledge/{id}", s.withAuth(http.HandlerFunc(s.knowledgeDetail)))
	mux.Handle("GET /api/v1/knowledge/libraries", s.withAuth(http.HandlerFunc(s.listKnowledgeLibraries)))
	mux.Handle("POST /api/v1/knowledge/libraries/{id}/bind", s.withAuth(http.HandlerFunc(s.bindKnowledgeLibrary)))
	mux.Handle("POST /api/v1/knowledge/libraries/{id}/unbind", s.withAuth(http.HandlerFunc(s.unbindKnowledgeLibrary)))
	mux.Handle("DELETE /api/v1/knowledge/libraries/{id}", s.withAuth(http.HandlerFunc(s.deleteKnowledgeLibrary)))
	mux.Handle("POST /api/v1/projects/{id}/knowledge/detach", s.withAuth(http.HandlerFunc(s.detachProjectKnowledge)))
	mux.Handle("DELETE /api/v1/projects/{id}/knowledge", s.withAuth(http.HandlerFunc(s.deleteProjectKnowledge)))
	mux.Handle("POST /api/v1/knowledge", s.withAuth(http.HandlerFunc(s.createKnowledge)))
	mux.Handle("PUT /api/v1/knowledge/{id}", s.withAuth(http.HandlerFunc(s.updateKnowledge)))
	mux.Handle("DELETE /api/v1/knowledge/{id}", s.withAuth(http.HandlerFunc(s.deleteKnowledge)))
	mux.Handle("POST /api/v1/knowledge/{id}/verify", s.withAuth(http.HandlerFunc(s.verifyKnowledge)))
	mux.Handle("POST /api/v1/knowledge/{id}/feedback", s.withAuth(http.HandlerFunc(s.feedbackKnowledge)))
	mux.Handle("POST /api/v1/knowledge/proposals/{id}/approve", s.withAuth(http.HandlerFunc(s.approveKnowledgeProposal)))
	mux.Handle("POST /api/v1/knowledge/proposals/{id}/reject", s.withAuth(http.HandlerFunc(s.rejectKnowledgeProposal)))
	mux.Handle("POST /api/v1/knowledge/proposals/batch", s.withAuth(http.HandlerFunc(s.batchKnowledgeProposalReviews)))
	mux.Handle("GET /api/v1/projects/{id}/product-lines", s.withAuth(http.HandlerFunc(s.listProductLines)))
	mux.Handle("POST /api/v1/projects/{id}/product-lines", s.withAuth(http.HandlerFunc(s.createProductLine)))
	mux.Handle("DELETE /api/v1/projects/{id}/product-lines/{line}", s.withAuth(http.HandlerFunc(s.deleteProductLine)))
	mux.Handle("GET /api/v1/skills", s.withAuth(http.HandlerFunc(s.listSkills)))
	mux.Handle("POST /api/v1/skills", s.withAuth(http.HandlerFunc(s.createSkill)))
	mux.Handle("PUT /api/v1/skills/{id}", s.withAuth(http.HandlerFunc(s.updateSkill)))
	mux.Handle("DELETE /api/v1/skills/{id}", s.withAuth(http.HandlerFunc(s.deleteSkill)))

	mux.HandleFunc("/", s.serveWeb)
	return s.securityHeaders(s.requestLog(mux))
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; script-src 'self'")
		next.ServeHTTP(writer, request)
	})
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		start := time.Now()
		next.ServeHTTP(writer, request)
		if !strings.HasPrefix(request.URL.Path, "/assets/") {
			s.logger.Debug("http request", "method", request.Method, "path", request.URL.Path, "duration", time.Since(start))
		}
	})
}
