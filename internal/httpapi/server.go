package httpapi

import (
	"context"
	"crypto/rand"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/channel"
	"github.com/ChinaKai/AHA2/internal/codexaccount"
	"github.com/ChinaKai/AHA2/internal/desktop"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	mailpkg "github.com/ChinaKai/AHA2/internal/mail"
	"github.com/ChinaKai/AHA2/internal/managedprocess"
	"github.com/ChinaKai/AHA2/internal/outboundproxy"
	"github.com/ChinaKai/AHA2/internal/store"
	"github.com/ChinaKai/AHA2/internal/workspace"
	"github.com/ChinaKai/AHA2/internal/workspaceterminal"
)

const sessionCookieName = "aha2_session"

type Config struct {
	Store *store.Store
	// SyncStore is an optional independent SQLite pool used by scheduled and
	// manual sync runs. When nil, sync shares Store (the historical behavior).
	// main opens a dedicated Store so long sync phases cannot block Web reads.
	SyncStore         *store.Store
	Auth              *auth.Service
	App               *app.Service
	Web               fs.FS
	Logger            *slog.Logger
	SecureCookie      bool
	AllowCrossOrigin  bool
	DetectWorkspace   func(context.Context, domain.Workspace) (domain.Workspace, error)
	Secrets           SecretStore
	Hardware          *hardware.Manager
	Browser           *browser.Service
	Desktop           *desktop.Manager
	CodexAccounts     *codexaccount.Manager
	AgentCapabilities *agentapi.Capabilities

	ScratchAttachmentLimit int64

	ManagedProcesses *managedprocess.Manager
	OutboundProxy    *outboundproxy.Controller
	Channels         *channel.Service
	MailTester       mailpkg.Tester
	// MailTaskBaseURL is the external https base used for task.url template
	// variables. Relative or non-https values are treated as unconfigured.
	MailTaskBaseURL string
	// MailDownloadBaseURL is the public https base of the independent
	// controlled-download entry (for example https://mail-downloads.example.com).
	// Link-mode attachments are rejected when it is unconfigured.
	MailDownloadBaseURL string
	ProbeSSHHostKey     func(context.Context, string) (hardware.SSHHostKeyInfo, error)
	TrustSSHHostKey     func(context.Context, string, string) (hardware.SSHHostKeyInfo, error)
	PairADB             func(context.Context, string, string) (string, error)
	ExecuteADB          func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
	ExecuteADBUSB       func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
	ExecuteADBTCP       func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
	ListADBUSBDevices   func(context.Context) (hardware.ADBUSBDeviceList, error)
	Version             string
	WebVersion          string
	// ListenAddress is what this process was launched with, reported so the UI can
	// tell a saved-but-not-yet-applied address from an active one.
	ListenAddress string
	// LocalOwnerIngest registers the loopback-only owner message ingest route.
	// It is off by default; when off the path is not routed at all.
	LocalOwnerIngest bool
	// KnowledgeVault is the Phase 3 runtime manager. It is always non-nil in
	// production so routes can stay registered and report "disabled" with a
	// 404 while the switch is off. The legacy knowledge routes never change.
	KnowledgeVault *knowledgevault.Manager
	// KnowledgeVaultSync is the background auto-sync engine. Nil keeps the
	// settings panel informative but without automatic Git operations (tests
	// and support commands do not need a loop).
	KnowledgeVaultSync *knowledgevault.Syncer
	// BackupPreviewTimeout overrides the data-management backup scan deadline.
	// Zero keeps the production default; tests use a very short value to
	// exercise the 504 response without waiting ten seconds.
	BackupPreviewTimeout time.Duration
	StartedAt            time.Time
}

type Server struct {
	workspaceTerminals       *workspaceterminal.Manager
	store                    *store.Store
	syncStore                *store.Store
	auth                     *auth.Service
	app                      *app.Service
	web                      fs.FS
	logger                   *slog.Logger
	secureCookie             bool
	originPolicyMu           sync.RWMutex
	validateOrigin           bool
	originStartupOverride    bool
	accessScope              *accessScopeGate
	startupListenAddress     string
	detectWorkspace          func(context.Context, domain.Workspace) (domain.Workspace, error)
	secrets                  SecretStore
	hardware                 *hardware.Manager
	browser                  *browser.Service
	desktop                  *desktop.Manager
	codexAccounts            *codexaccount.Manager
	agentCapabilities        *agentapi.Capabilities
	attachmentLimit          int64
	managedProcesses         *managedprocess.Manager
	outboundProxy            *outboundproxy.Controller
	channels                 *channel.Service
	mailTester               mailpkg.Tester
	mailTaskBaseURL          string
	mailDownloadBaseURL      string
	mailDownloadKey          []byte
	mailDownloadLimiter      *mailDownloadLimiter
	mailDownloadHosts        map[string]bool
	mailDownloadNonceMu      sync.Mutex
	mailDownloadNonces       map[string]time.Time
	probeSSHHostKey          func(context.Context, string) (hardware.SSHHostKeyInfo, error)
	trustSSHHostKey          func(context.Context, string, string) (hardware.SSHHostKeyInfo, error)
	pairADB                  func(context.Context, string, string) (string, error)
	backupPreviewTimeout     time.Duration
	executeADB               func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
	executeADBUSB            func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
	executeADBTCP            func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
	listADBUSBDevices        func(context.Context) (hardware.ADBUSBDeviceList, error)
	adbForwards              *adbForwardRegistry
	adbForwardMu             sync.Mutex // serializes forward add/remove from authorization through the registry update
	version                  string
	webVersion               string
	startedAt                time.Time
	localOwnerIngest         bool
	localOwnerMessageLimiter *localOwnerMessageLimiter
	syncRunMu                sync.RWMutex
	syncRun                  syncRunProgress
	syncNextRunAt            time.Time
	syncLoopStarted          bool
	authLimiter              *authLimiter
	modelDetectionJobs       *modelDetectionJobs
	systemMu                 sync.Mutex
	systemCachedAt           time.Time
	systemCache              map[string]any
	runtimeContextCache      runtimeContextCache
	knowledgeVault           *knowledgevault.Manager
	knowledgeVaultSync       *knowledgevault.Syncer
	// revealPath opens a vault path in the host file manager. It is nil in
	// production (the platform opener is used) and injectable in tests so a
	// loopback request never launches a real file manager under `go test`.
	revealPath func(string) error
}

func New(config Config) *Server {
	if config.Desktop == nil {
		config.Desktop = desktop.New(desktop.NativeProvider())
	}
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
	pairADB := config.PairADB
	if pairADB == nil {
		pairADB = hardware.PairADB
	}
	executeADB := config.ExecuteADB
	if executeADB == nil {
		executeADB = hardware.ExecuteADB
	}
	executeADBUSB := config.ExecuteADBUSB
	if executeADBUSB == nil {
		executeADBUSB = hardware.ExecuteADBUSB
	}
	executeADBTCP := config.ExecuteADBTCP
	if executeADBTCP == nil {
		executeADBTCP = hardware.ExecuteADBTCP
	}
	listADBUSBDevices := config.ListADBUSBDevices
	if listADBUSBDevices == nil {
		listADBUSBDevices = hardware.ListADBUSBDevices
	}
	mailTester := config.MailTester
	if mailTester == nil {
		mailTester = mailpkg.NewSMTPTester()
	}
	downloadKey := make([]byte, 32)
	if _, err := rand.Read(downloadKey); err != nil {
		// A download nonce key is required for confirm-page integrity. Wiping
		// it makes the endpoint return 503 instead of accepting unsigned forms.
		downloadKey = nil
	}
	// The independent download entry only accepts its configured public Host.
	// With no download base URL config there is no valid Host and every request
	// is rejected, which is the safe default.
	downloadHosts := map[string]bool{}
	if parsed, err := url.Parse(strings.TrimSpace(config.MailDownloadBaseURL)); err == nil &&
		strings.EqualFold(parsed.Scheme, "https") && parsed.Hostname() != "" {
		downloadHosts[strings.ToLower(parsed.Hostname())] = true
	}
	validateOrigin := !config.AllowCrossOrigin
	// Default to the LAN scope: AHA2 binds 0.0.0.0, so narrowing on startup before
	// anyone has chosen would cut off clients that already rely on it.
	accessScopeValue := domain.AccessScopeLAN
	if config.Store != nil {
		if settings, err := config.Store.SecuritySettings(context.Background()); err == nil {
			if !config.AllowCrossOrigin {
				validateOrigin = settings.ValidateOrigin
			}
			accessScopeValue = settings.AccessScope
		}
	}
	syncStore := config.SyncStore
	if syncStore == nil {
		syncStore = config.Store
	}
	attachmentLimit := config.ScratchAttachmentLimit
	if attachmentLimit <= 0 || attachmentLimit > workspace.MaxScratchAttachmentBytes {
		attachmentLimit = workspace.DefaultScratchAttachmentBytes
	}
	server := &Server{
		workspaceTerminals: workspaceterminal.New(),
		store:              config.Store, syncStore: syncStore, auth: config.Auth, app: config.App, web: config.Web,
		logger: logger, secureCookie: config.SecureCookie, validateOrigin: validateOrigin,
		originStartupOverride:    config.AllowCrossOrigin,
		accessScope:              newAccessScopeGate(accessScopeValue),
		startupListenAddress:     config.ListenAddress,
		detectWorkspace:          config.DetectWorkspace,
		secrets:                  config.Secrets,
		hardware:                 config.Hardware,
		browser:                  config.Browser,
		desktop:                  config.Desktop,
		codexAccounts:            config.CodexAccounts,
		agentCapabilities:        config.AgentCapabilities,
		attachmentLimit:          attachmentLimit,
		managedProcesses:         config.ManagedProcesses,
		outboundProxy:            config.OutboundProxy,
		channels:                 config.Channels,
		mailTester:               mailTester,
		mailTaskBaseURL:          strings.TrimRight(strings.TrimSpace(config.MailTaskBaseURL), "/"),
		mailDownloadBaseURL:      strings.TrimRight(strings.TrimSpace(config.MailDownloadBaseURL), "/"),
		mailDownloadKey:          downloadKey,
		mailDownloadLimiter:      newMailDownloadLimiter(),
		mailDownloadHosts:        downloadHosts,
		mailDownloadNonces:       map[string]time.Time{},
		probeSSHHostKey:          probeSSHHostKey,
		trustSSHHostKey:          trustSSHHostKey,
		pairADB:                  pairADB,
		executeADB:               executeADB,
		executeADBUSB:            executeADBUSB,
		executeADBTCP:            executeADBTCP,
		listADBUSBDevices:        listADBUSBDevices,
		adbForwards:              newADBForwardRegistry(),
		version:                  config.Version,
		webVersion:               config.WebVersion,
		startedAt:                startedAt,
		localOwnerIngest:         config.LocalOwnerIngest,
		localOwnerMessageLimiter: newLocalOwnerMessageLimiter(),
		authLimiter:              newAuthLimiter(),
		modelDetectionJobs:       newModelDetectionJobs(),
		knowledgeVault:           config.KnowledgeVault,
		knowledgeVaultSync:       config.KnowledgeVaultSync,
		backupPreviewTimeout:     config.BackupPreviewTimeout,
	}
	if server.app != nil && server.hardware != nil {
		server.app.SetPetHardwareDisconnector(server.hardware)
	}
	if server.app != nil {
		server.app.SetDesktopMCPAvailability(func(taskID string) bool { return server.desktop != nil && server.desktop.Status(taskID).Supported })
	}
	if server.webVersion == "" {
		server.webVersion = server.version
	}
	// Provider discovery and capability probes deliberately use direct
	// connections, matching POST /api/v1/providers/detect-models. Routing only
	// these calls through the managed proxy made the catalog work while every
	// probe failed with "destination connection failed", because the catalog
	// endpoint is direct.
	return server
}

func (s *Server) Handler() http.Handler {
	return s.securityHeaders(s.requestLog(s.slowRequestLog(s.enforceAccessScope(s.repairTextEncoding(s.routeMux())))))
}

// routeMux registers every route of the main listener. It is separate from
// Handler so the coverage inventory can be checked against the real mux.
func (s *Server) routeMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	if s.localOwnerIngest {
		mux.HandleFunc("POST /api/v1/local/owner-messages", s.localOwnerMessageIngest)
	}
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
	mux.Handle("GET /api/v1/system/data", s.withAuth(http.HandlerFunc(s.dataInventory)))
	mux.Handle("GET /api/v1/system/data/backups", s.withAuth(http.HandlerFunc(s.previewBackupCleanup)))
	mux.Handle("POST /api/v1/system/data/cleanup", s.withAuth(http.HandlerFunc(s.cleanupData)))
	mux.Handle("GET /api/v1/system/data/database", s.withAuth(http.HandlerFunc(s.databaseStats)))
	mux.Handle("PUT /api/v1/system/data/hardware-io/retention", s.withAuth(http.HandlerFunc(s.updateHardwareIORetention)))
	mux.Handle("POST /api/v1/system/data/hardware-io/preview", s.withAuth(http.HandlerFunc(s.previewHardwareIOPurge)))
	mux.Handle("POST /api/v1/system/data/hardware-io/purge", s.withAuth(http.HandlerFunc(s.purgeHardwareIO)))
	mux.Handle("GET /api/v1/system/data/hardware-io/purge/progress", s.withAuth(http.HandlerFunc(s.hardwareIOPurgeProgress)))
	mux.Handle("PUT /api/v1/system/data/conversation/retention", s.withAuth(http.HandlerFunc(s.updateConversationRetention)))
	mux.Handle("POST /api/v1/system/data/conversation/preview", s.withAuth(http.HandlerFunc(s.previewConversationPurge)))
	mux.Handle("POST /api/v1/system/data/conversation/purge", s.withAuth(http.HandlerFunc(s.purgeConversation)))
	mux.Handle("GET /api/v1/system/data/conversation/purge/progress", s.withAuth(http.HandlerFunc(s.conversationPurgeProgress)))
	mux.Handle("GET /api/v1/system/data/database/offline-compaction", s.withAuth(http.HandlerFunc(s.offlineCompactionInfo)))
	mux.Handle("GET /api/v1/assistant", s.withAuth(http.HandlerFunc(s.ownerAssistant)))
	mux.Handle("GET /api/v1/onboarding", s.withAuth(http.HandlerFunc(s.onboarding)))
	mux.Handle("PUT /api/v1/onboarding/dismiss", s.withAuth(http.HandlerFunc(s.dismissOnboarding)))
	mux.Handle("POST /api/v1/onboarding/workspace", s.withAuth(http.HandlerFunc(s.createOnboardingWorkspace)))
	mux.Handle("POST /api/v1/onboarding/task", s.withAuth(http.HandlerFunc(s.createOnboardingTask)))
	mux.Handle("PUT /api/v1/assistant", s.withAuth(http.HandlerFunc(s.updateOwnerAssistant)))
	mux.Handle("POST /api/v1/assistant/messages", s.withAuth(http.HandlerFunc(s.askOwnerAssistant)))
	mux.Handle("GET /api/v1/assistant/hints", s.withAuth(http.HandlerFunc(s.ownerAssistantHints)))
	mux.Handle("POST /api/v1/assistant/hints/dismiss", s.withAuth(http.HandlerFunc(s.dismissOwnerAssistantHint)))
	mux.Handle("GET /api/v1/pet", s.withAuth(http.HandlerFunc(s.ownerPetState)))
	mux.Handle("PUT /api/v1/pet", s.withAuth(http.HandlerFunc(s.updateOwnerPetState)))
	mux.Handle("GET /api/v1/pet/events", s.withAuth(http.HandlerFunc(s.ownerPetEvents)))
	mux.Handle("POST /api/v1/pet/brain", s.withAuth(http.HandlerFunc(s.ownerPetBrain)))
	mux.Handle("PUT /api/v1/pet/settings", s.withAuth(http.HandlerFunc(s.updateOwnerPetSettings)))
	mux.Handle("POST /api/v1/pet/reset", s.withAuth(http.HandlerFunc(s.resetOwnerPet)))
	mux.Handle("GET /api/v1/pet/activity", s.withAuth(http.HandlerFunc(s.ownerPetActivity)))
	mux.Handle("GET /api/v1/pet/prompts", s.withAuth(http.HandlerFunc(s.ownerPetPrompts)))
	mux.Handle("POST /api/v1/pet/control", s.withAuth(http.HandlerFunc(s.controlOwnerPet)))
	mux.Handle("POST /api/v1/pet/navigation/claim", s.withAuth(http.HandlerFunc(s.claimOwnerPetNavigation)))
	mux.Handle("GET /api/v1/pet/approvals/pending-count", s.withAuth(http.HandlerFunc(s.petPendingApprovalCount)))
	mux.Handle("GET /api/v1/pet/approvals/{id}", s.withAuth(http.HandlerFunc(s.petApprovalDetail)))
	mux.Handle("GET /api/v1/pet/workbench", s.withAuth(http.HandlerFunc(s.petWorkbench)))
	mux.Handle("GET /api/v1/pet/workbench/insights", s.withAuth(http.HandlerFunc(s.petInsights)))
	mux.Handle("PUT /api/v1/pet/workbench/core/{name}", s.withAuth(s.savePetWorkbenchDocument("core")))
	mux.Handle("PUT /api/v1/pet/workbench/worklog/{name}", s.withAuth(s.savePetWorkbenchDocument("worklog")))
	mux.Handle("POST /api/v1/pet/watches/{task}", s.withAuth(http.HandlerFunc(s.followPetWatch)))
	mux.Handle("DELETE /api/v1/pet/watches/{task}", s.withAuth(http.HandlerFunc(s.deletePetWatch)))
	mux.Handle("PATCH /api/v1/pet/watches/{task}", s.withAuth(http.HandlerFunc(s.updatePetWatch)))
	mux.Handle("POST /api/v1/pet/approvals/{id}/resolve", s.withAuth(http.HandlerFunc(s.resolvePetApproval)))
	mux.Handle("GET /api/v1/mcp-servers", s.withAuth(http.HandlerFunc(s.listMCPServers)))
	mux.Handle("GET /api/v1/mcp-servers/{id}", s.withAuth(http.HandlerFunc(s.mcpServerDetail)))
	mux.Handle("POST /api/v1/mcp-servers", s.withAuth(http.HandlerFunc(s.saveMCPServer)))
	mux.Handle("DELETE /api/v1/mcp-servers/{id}", s.withAuth(http.HandlerFunc(s.deleteMCPServer)))
	mux.Handle("POST /api/v1/mcp-servers/test", s.withAuth(http.HandlerFunc(s.testMCPServer)))
	mux.Handle("PUT /api/v1/tasks/{id}/mcp-servers", s.withAuth(http.HandlerFunc(s.setTaskMCPServers)))
	// The pet MCP surface is capability-authenticated like every other agent
	// endpoint: the handler additionally enforces the pet Task identity and a
	// direct loopback source. See pet_mcp.go.
	mux.Handle("POST /api/v1/pet/mcp", s.withAgentCapability(http.HandlerFunc(s.petMCP)))
	mux.Handle("GET /api/v1/settings/proxy", s.withAuth(http.HandlerFunc(s.proxySettings)))
	mux.Handle("PUT /api/v1/settings/proxy", s.withAuth(http.HandlerFunc(s.updateProxySettings)))
	mux.Handle("POST /api/v1/settings/proxy/test", s.withAuth(http.HandlerFunc(s.testProxySettings)))
	mux.Handle("POST /api/v1/settings/proxy/subscription", s.withAuth(http.HandlerFunc(s.importProxySubscription)))
	mux.Handle("POST /api/v1/settings/proxy/subscription/refresh", s.withAuth(http.HandlerFunc(s.refreshProxySubscription)))
	mux.Handle("POST /api/v1/settings/proxy/profiles/{id}/refresh", s.withAuth(http.HandlerFunc(s.refreshProxyProfile)))
	mux.Handle("PATCH /api/v1/settings/proxy/profiles/{id}", s.withAuth(http.HandlerFunc(s.patchProxyProfile)))
	mux.Handle("POST /api/v1/settings/proxy/profiles/{id}/activate", s.withAuth(http.HandlerFunc(s.activateProxyProfile)))
	mux.Handle("POST /api/v1/settings/proxy/profiles/{id}/nodes/{node_id}/test", s.withAuth(http.HandlerFunc(s.testProxyNode)))
	mux.Handle("DELETE /api/v1/settings/proxy/profiles/{id}", s.withAuth(http.HandlerFunc(s.deleteProxyProfile)))
	mux.Handle("GET /api/v1/settings/security", s.withAuth(http.HandlerFunc(s.securitySettings)))
	mux.Handle("PUT /api/v1/settings/security", s.withAuth(http.HandlerFunc(s.updateSecuritySettings)))
	mux.Handle("GET /api/v1/settings/network", s.withAuth(http.HandlerFunc(s.networkSettings)))
	mux.Handle("PUT /api/v1/settings/network", s.withAuth(http.HandlerFunc(s.updateNetworkSettings)))
	mux.Handle("GET /api/v1/settings/agent-api", s.withAuth(http.HandlerFunc(s.agentAPISettings)))
	mux.Handle("PUT /api/v1/settings/agent-api", s.withAuth(http.HandlerFunc(s.updateAgentAPISettings)))
	mux.Handle("GET /api/v1/settings/backend", s.withAuth(http.HandlerFunc(s.backendSettings)))
	mux.Handle("PUT /api/v1/settings/backend", s.withAuth(http.HandlerFunc(s.updateBackendSettings)))
	mux.Handle("GET /api/v1/settings/general", s.withAuth(http.HandlerFunc(s.generalSettings)))
	mux.Handle("PUT /api/v1/settings/general", s.withAuth(http.HandlerFunc(s.updateGeneralSettings)))
	mux.Handle("GET /api/v1/settings/knowledge-review", s.withAuth(http.HandlerFunc(s.knowledgeReviewSettings)))
	mux.Handle("PUT /api/v1/settings/knowledge-review", s.withAuth(http.HandlerFunc(s.updateKnowledgeReviewSettings)))
	mux.Handle("GET /api/v1/settings/sync", s.withAuth(http.HandlerFunc(s.syncSettings)))
	mux.Handle("PUT /api/v1/settings/sync", s.withAuth(http.HandlerFunc(s.updateSyncSettings)))
	mux.Handle("GET /api/v1/settings/sync/status", s.withAuth(http.HandlerFunc(s.syncStatus)))
	mux.Handle("GET /api/v1/settings/sync/preview", s.withAuth(http.HandlerFunc(s.syncPreview)))
	mux.Handle("POST /api/v1/settings/sync/run", s.withAuth(http.HandlerFunc(s.runSync)))
	mux.Handle("GET /api/v1/settings/sync/conflicts", s.withAuth(http.HandlerFunc(s.syncConflicts)))
	mux.Handle("POST /api/v1/settings/sync/conflicts/{id}/resolve", s.withAuth(http.HandlerFunc(s.resolveSyncConflict)))
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
	mux.Handle("GET /api/v1/channel-instances/{id}/onboarding-options", s.withAuth(http.HandlerFunc(s.channelOnboardingOptions)))
	mux.Handle("POST /api/v1/channel-instances/{id}/runtime-menus/reset", s.withAuth(http.HandlerFunc(s.resetChannelRuntimeMenus)))
	mux.Handle("POST /api/v1/channel-instances/{id}/onboarding-sessions", s.withAuth(http.HandlerFunc(s.startChannelOnboarding)))
	mux.Handle("GET /api/v1/channel-onboarding-sessions/{id}", s.withAuth(http.HandlerFunc(s.channelOnboarding)))
	mux.Handle("GET /api/v1/channel-onboarding-sessions/{id}/qr", s.withAuth(http.HandlerFunc(s.channelOnboardingQR)))
	mux.Handle("POST /api/v1/channel-onboarding-sessions/{id}/cancel", s.withAuth(http.HandlerFunc(s.cancelChannelOnboarding)))
	mux.Handle("GET /api/v1/channel-instances/{id}/conversations", s.withAuth(http.HandlerFunc(s.channelConversationSummaries)))
	mux.Handle("POST /api/v1/channel-instances/{id}/conversations/refresh", s.withAuth(http.HandlerFunc(s.channelConversationsRefresh)))
	mux.Handle("PUT /api/v1/channel-instances/{id}/conversations/groups/{chatID}/reception", s.withAuth(http.HandlerFunc(s.channelGroupReception)))
	mux.Handle("PUT /api/v1/channel-instances/{id}/conversations/groups/{chatID}/topic-wake", s.withAuth(http.HandlerFunc(s.channelGroupTopicWake)))
	mux.Handle("GET /api/v1/channel-instances/{id}/handoffs", s.withAuth(http.HandlerFunc(s.channelHandoffs)))
	mux.Handle("GET /api/v1/channel-instances/{id}/deliveries", s.withAuth(http.HandlerFunc(s.channelDeliveries)))
	mux.Handle("GET /api/v1/channel-deliveries/{id}/attempts", s.withAuth(http.HandlerFunc(s.channelDeliveryAttempts)))
	mux.Handle("POST /api/v1/channel-deliveries/{id}/replay", s.withAuth(http.HandlerFunc(s.replayChannelDelivery)))
	mux.Handle("POST /api/v1/channel-deliveries/{id}/skip", s.withAuth(http.HandlerFunc(s.skipChannelDelivery)))
	mux.Handle("GET /api/v1/channel-instances/{id}/knowledge-bindings", s.withAuth(http.HandlerFunc(s.channelKnowledgeBindings)))
	mux.Handle("PUT /api/v1/channel-instances/{id}/knowledge-bindings", s.withAuth(http.HandlerFunc(s.updateChannelKnowledgeBindings)))
	mux.Handle("PUT /api/v1/channel-instances/{id}/knowledge-bindings/group", s.withAuth(http.HandlerFunc(s.updateChannelGroupKnowledgeBindings)))
	mux.Handle("GET /api/v1/channel-instances/{id}/knowledge-policy", s.withAuth(http.HandlerFunc(s.channelKnowledgePolicy)))
	mux.Handle("PUT /api/v1/channel-instances/{id}/knowledge-policy", s.withAuth(http.HandlerFunc(s.updateChannelKnowledgePolicy)))
	mux.Handle("GET /api/v1/projects", s.withAuth(http.HandlerFunc(s.listProjects)))
	mux.Handle("POST /api/v1/projects", s.withAuth(http.HandlerFunc(s.createProject)))
	mux.Handle("PUT /api/v1/projects/{id}", s.withAuth(http.HandlerFunc(s.updateProject)))
	mux.Handle("DELETE /api/v1/projects/{id}", s.withAuth(http.HandlerFunc(s.deleteProject)))
	mux.Handle("GET /api/v1/workspaces", s.withAuth(http.HandlerFunc(s.listWorkspaces)))
	mux.Handle("GET /api/v1/workspaces/{workspace}/viewer/{view}", s.withAuth(http.HandlerFunc(s.workspaceView)))
	mux.HandleFunc("/api/v1/workspaces/{workspace}/viewer/{view}", workspaceViewerMethodNotAllowed)
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
	mux.Handle("POST /api/v1/models/batch-delete", s.withAuth(http.HandlerFunc(s.requestBatchDeleteModels)))
	mux.Handle("POST /api/v1/models/{id}/health-check", s.withAuth(http.HandlerFunc(s.checkModelHealth)))
	mux.Handle("GET /api/v1/env-groups", s.withAuth(http.HandlerFunc(s.listEnvGroups)))
	mux.Handle("POST /api/v1/env-groups", s.withAuth(http.HandlerFunc(s.createEnvGroup)))
	mux.Handle("POST /api/v1/providers/detect-models", s.withAuth(http.HandlerFunc(s.detectModelsHandler)))
	mux.Handle("POST /api/v1/providers/{id}/model-detection-jobs", s.withAuth(http.HandlerFunc(s.createModelDetectionJob)))
	mux.Handle("GET /api/v1/providers/{id}/model-detection-jobs/{job}/events", s.withAuth(http.HandlerFunc(s.modelDetectionJobEvents)))
	mux.Handle("POST /api/v1/providers/{id}/model-detection-jobs/{job}/cancel", s.withAuth(http.HandlerFunc(s.cancelModelDetectionJob)))
	mux.Handle("POST /api/v1/providers/{id}/model-probes", s.withAuth(http.HandlerFunc(s.probeProviderModel)))
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
	// Phase 3b adds only read-only entry views. The legacy template routes
	// above keep their existing edit/reset semantics for at least this release.
	mux.Handle("GET /api/v1/prompts/entries", s.withAuth(http.HandlerFunc(s.promptEntryCatalog)))
	mux.Handle("GET /api/v1/prompts/entries/{entry}/anchors/{anchor}", s.withAuth(http.HandlerFunc(s.promptEntryAnchorPreview)))
	mux.Handle("GET /api/v1/prompts/migration-preview", s.withAuth(http.HandlerFunc(s.promptMigrationPreview)))
	// The scenario preview renders a demo snapshot through the production
	// builder; it is a read-only GET and accepts no draft content.
	mux.Handle("GET /api/v1/prompts/scenario-preview", s.withAuth(http.HandlerFunc(s.promptScenarioPreview)))
	// Non-GET requests to the read-only views get an explicit 405 from the
	// authenticated handler; the newer method-specific GET patterns win first.
	mux.Handle("/api/v1/prompts/entries", s.withAuth(http.HandlerFunc(s.rejectPromptReadOnly)))
	mux.Handle("/api/v1/prompts/entries/{entry}/anchors/{anchor}", s.withAuth(http.HandlerFunc(s.rejectPromptReadOnly)))
	mux.Handle("/api/v1/prompts/migration-preview", s.withAuth(http.HandlerFunc(s.rejectPromptReadOnly)))
	mux.Handle("/api/v1/prompts/scenario-preview", s.withAuth(http.HandlerFunc(s.rejectPromptReadOnly)))
	mux.Handle("GET /api/v1/hardware/serial-ports", s.withAuth(http.HandlerFunc(s.hardwareSerialPorts)))
	mux.Handle("GET /api/v1/hardware-resources", s.withAuth(http.HandlerFunc(s.listHardwareResources)))
	mux.Handle("POST /api/v1/hardware-resources", s.withAuth(http.HandlerFunc(s.createHardwareResource)))
	mux.Handle("PUT /api/v1/hardware-resources/{id}", s.withAuth(http.HandlerFunc(s.updateHardwareResource)))
	mux.Handle("DELETE /api/v1/hardware-resources/{id}", s.withAuth(http.HandlerFunc(s.deleteHardwareResource)))
	mux.Handle(hardwareADBPairRoute, s.withAuth(http.HandlerFunc(s.pairHardwareADB)))
	// Owner-only: lists USB ADB devices on the AHA host for resource config.
	// POST so the adb server start it may cause needs CSRF + same origin.
	mux.Handle("POST /api/v1/hardware/adb/usb-devices", s.withAuth(http.HandlerFunc(s.hardwareADBUSBDevices)))
	s.registerBrowserRoutes(mux)

	// Mail resources phase one task B: Owner-only account/template/binding
	// management, preview and connection test. Agent capability routes are
	// deliberately absent (task D).
	mux.Handle("GET /api/v1/mail-accounts", s.withAuth(http.HandlerFunc(s.listMailAccounts)))
	mux.Handle("POST /api/v1/mail-accounts", s.withAuth(http.HandlerFunc(s.createMailAccount)))
	mux.Handle("GET /api/v1/mail-accounts/{id}", s.withAuth(http.HandlerFunc(s.mailAccountDetail)))
	mux.Handle("PUT /api/v1/mail-accounts/{id}", s.withAuth(http.HandlerFunc(s.updateMailAccount)))
	mux.Handle("DELETE /api/v1/mail-accounts/{id}", s.withAuth(http.HandlerFunc(s.deleteMailAccount)))
	mux.Handle("POST /api/v1/mail-accounts/{id}/test", s.withAuth(http.HandlerFunc(s.testMailAccount)))
	mux.Handle("GET /api/v1/mail-templates", s.withAuth(http.HandlerFunc(s.listMailTemplates)))
	mux.Handle("POST /api/v1/mail-templates", s.withAuth(http.HandlerFunc(s.createMailTemplate)))
	mux.Handle("GET /api/v1/mail-templates/{id}", s.withAuth(http.HandlerFunc(s.mailTemplateDetail)))
	mux.Handle("PUT /api/v1/mail-templates/{id}", s.withAuth(http.HandlerFunc(s.updateMailTemplate)))
	mux.Handle("DELETE /api/v1/mail-templates/{id}", s.withAuth(http.HandlerFunc(s.deleteMailTemplate)))
	mux.Handle("GET /api/v1/mail-templates/{id}/versions", s.withAuth(http.HandlerFunc(s.mailTemplateVersions)))
	mux.Handle("POST /api/v1/mail-templates/preview", s.withAuth(http.HandlerFunc(s.previewMailTemplateContent)))
	mux.Handle("POST /api/v1/mail-templates/{id}/preview", s.withAuth(http.HandlerFunc(s.previewMailTemplate)))
	mux.Handle("GET /api/v1/mail-bindings", s.withAuth(http.HandlerFunc(s.listMailBindings)))
	mux.Handle("POST /api/v1/mail-bindings", s.withAuth(http.HandlerFunc(s.createMailBinding)))
	mux.Handle("PUT /api/v1/mail-bindings/{id}", s.withAuth(http.HandlerFunc(s.updateMailBinding)))
	mux.Handle("DELETE /api/v1/mail-bindings/{id}", s.withAuth(http.HandlerFunc(s.deleteMailBinding)))
	mux.Handle("GET /api/v1/mail-messages", s.withAuth(http.HandlerFunc(s.listMailMessages)))
	mux.Handle("POST /api/v1/mail-messages", s.withAuth(http.HandlerFunc(s.createMailMessage)))
	mux.Handle("GET /api/v1/mail-messages/{id}", s.withAuth(http.HandlerFunc(s.mailMessageDetail)))
	mux.Handle("POST /api/v1/mail-messages/{id}/submit", s.withAuth(http.HandlerFunc(s.submitMailMessage)))
	mux.Handle("POST /api/v1/mail-messages/{id}/approve", s.withAuth(http.HandlerFunc(s.approveMailMessage)))
	mux.Handle("POST /api/v1/mail-messages/{id}/reject", s.withAuth(http.HandlerFunc(s.rejectMailMessage)))
	mux.Handle("POST /api/v1/mail-messages/{id}/cancel", s.withAuth(http.HandlerFunc(s.cancelMailMessage)))
	mux.Handle("POST /api/v1/mail-messages/{id}/retry", s.withAuth(http.HandlerFunc(s.retryMailMessage)))
	mux.Handle("GET /api/v1/mail-messages/{id}/attempts", s.withAuth(http.HandlerFunc(s.mailMessageAttempts)))
	mux.Handle("GET /api/v1/mail-messages/{id}/download-links", s.withAuth(http.HandlerFunc(s.mailMessageDownloadLinks)))
	mux.Handle("POST /api/v1/mail-messages/{id}/download-links/{attachment_id}/revoke", s.withAuth(http.HandlerFunc(s.revokeMailMessageDownloadLink)))

	mux.Handle("GET /api/v1/tasks", s.withAuth(http.HandlerFunc(s.listTasks)))
	mux.Handle("GET /api/v1/task-clone-sources", s.withAuth(http.HandlerFunc(s.listTaskCloneSources)))
	mux.Handle("POST /api/v1/tasks", s.withAuth(http.HandlerFunc(s.createTask)))
	mux.Handle("POST /api/v1/tasks/{id}/start", s.withAuth(http.HandlerFunc(s.startTask)))
	mux.Handle("DELETE /api/v1/tasks/{id}", s.withAuth(http.HandlerFunc(s.deleteTask)))
	mux.Handle("DELETE /api/v1/tasks/{id}/remote-mirror", s.withAuth(http.HandlerFunc(s.retireRemoteTaskMirror)))
	mux.Handle("POST /api/v1/tasks/{id}/takeover", s.withAuth(http.HandlerFunc(s.takeoverTask)))
	mux.Handle("GET /api/v1/tasks/{id}", s.withAuth(http.HandlerFunc(s.taskDetail)))
	mux.Handle("GET /api/v1/tasks/{id}/workspace/{view}", s.withAuth(http.HandlerFunc(s.workspaceView)))
	mux.HandleFunc("/api/v1/tasks/{id}/workspace/{view}", workspaceViewerMethodNotAllowed)
	mux.Handle("GET /api/v1/tasks/{id}/workspace-targets", s.withAuth(http.HandlerFunc(s.workspaceViewerTargets)))
	mux.Handle("GET /api/v1/tasks/{id}/workspace-target/{workspace}/{view}", s.withAuth(http.HandlerFunc(s.workspaceView)))
	mux.HandleFunc("/api/v1/tasks/{id}/workspace-target/{workspace}/{view}", workspaceViewerMethodNotAllowed)
	s.registerWorkspaceTerminalRoutes(mux)
	mux.Handle("GET /api/v1/tasks/{id}/channel-routes", s.withAuth(http.HandlerFunc(s.taskChannelRoutes)))
	mux.Handle("POST /api/v1/tasks/{id}/channel-routes", s.withAuth(http.HandlerFunc(s.bindTaskChannelRoute)))
	mux.Handle("DELETE /api/v1/tasks/{id}/channel-routes/{route}", s.withAuth(http.HandlerFunc(s.unbindTaskChannelRoute)))
	mux.Handle("POST /api/v1/tasks/{id}/channel-members:refresh", s.withAuth(http.HandlerFunc(s.refreshTaskChannelMembers)))
	mux.Handle("PUT /api/v1/tasks/{id}/channel-contacts", s.withAuth(http.HandlerFunc(s.updateTaskChannelContacts)))
	mux.Handle("GET /api/v1/tasks/{id}/conversation", s.withAuth(http.HandlerFunc(s.taskConversation)))
	mux.Handle("GET /api/v1/tasks/{id}/context", s.withAuth(http.HandlerFunc(s.taskContext)))
	mux.Handle("POST /api/v1/tasks/{id}/messages", s.withAuth(http.HandlerFunc(s.submitMessage)))
	mux.Handle("POST /api/v1/tasks/{id}/attachments", s.withAuth(http.HandlerFunc(s.uploadTaskAttachment)))
	mux.Handle("GET /api/v1/tasks/{id}/mail", s.withAuth(http.HandlerFunc(s.taskMailOptions)))
	mux.Handle("GET /api/v1/tasks/{id}/attachments/{attachment}", s.withOwnerOrAgentAuth(http.HandlerFunc(s.taskAttachmentContent)))
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
	s.registerDesktopRoutes(mux)
	mux.Handle("PUT /api/v1/tasks/{id}/hardware", s.withAuth(http.HandlerFunc(s.updateTaskHardware)))
	mux.Handle("GET /api/v1/tasks/{id}/hardware/{hardware}/terminal", s.withAuth(http.HandlerFunc(s.hardwareTerminal)))
	mux.Handle("GET /api/v1/tasks/{id}/hardware/{hardware}/terminal/ws", s.withAuth(http.HandlerFunc(s.hardwareTerminalWebSocket)))
	mux.Handle("POST /api/v1/tasks/{id}/hardware/{hardware}/connect", s.withAuth(http.HandlerFunc(s.connectHardware)))
	mux.Handle("PUT /api/v1/tasks/{id}/hardware-bindings", s.withAuth(http.HandlerFunc(s.updateTaskHardwareBindings)))
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
	mux.Handle(agentADBExecRoute, s.withAgentCapability(http.HandlerFunc(s.agentADBExec)))
	mux.Handle("GET /api/v1/agent/processes", s.withAgentCapability(http.HandlerFunc(s.agentProcesses)))
	mux.Handle("POST /api/v1/agent/processes", s.withAgentCapability(http.HandlerFunc(s.startAgentProcess)))
	mux.Handle("GET /api/v1/agent/processes/{name}", s.withAgentCapability(http.HandlerFunc(s.agentProcessStatus)))
	mux.Handle("POST /api/v1/agent/processes/{name}/stop", s.withAgentCapability(http.HandlerFunc(s.stopAgentProcess)))
	mux.Handle("GET /api/v1/agent/capabilities", s.withAgentCapability(http.HandlerFunc(s.agentCapabilitiesInfo)))
	// Task-scoped shared-browser control. Every grant-addressed route verifies
	// that the grant belongs to the capability's Task before calling the service.
	mux.Handle("GET /api/v1/agent/browser", s.withAgentCapability(http.HandlerFunc(s.agentBrowserState)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/tabs", s.withAgentCapability(http.HandlerFunc(s.agentBrowserTabs)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/tabs", s.withAgentCapability(http.HandlerFunc(s.agentBrowserOpenTab)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/tabs/current", s.withAgentCapability(http.HandlerFunc(s.agentBrowserBindCurrent)))
	mux.Handle("DELETE /api/v1/agent/browser/grants/{grant}/tabs/{tab}", s.withAgentCapability(http.HandlerFunc(s.agentBrowserCloseTab)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/activate", s.withAgentCapability(http.HandlerFunc(s.agentBrowserActivate)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/navigate", s.withAgentCapability(http.HandlerFunc(s.agentBrowserNavigate)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/snapshot", s.withAgentCapability(http.HandlerFunc(s.agentBrowserSnapshot)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/frame", s.withAgentCapability(http.HandlerFunc(s.agentBrowserFrame)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/actions", s.withAgentCapability(http.HandlerFunc(s.agentBrowserAction)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/files", s.withAgentCapability(http.HandlerFunc(s.agentBrowserUpload)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/downloads", s.withAgentCapability(http.HandlerFunc(s.agentBrowserDownloads)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/downloads/{download}", s.withAgentCapability(http.HandlerFunc(s.agentBrowserDownload)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/cookies", s.withAgentCapability(http.HandlerFunc(s.agentBrowserCookies)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/cookies", s.withAgentCapability(http.HandlerFunc(s.agentBrowserImportCookies)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/bookmarks", s.withAgentCapability(http.HandlerFunc(s.agentBrowserBookmarks)))
	mux.Handle("DELETE /api/v1/agent/browser/grants/{grant}", s.withAgentCapability(http.HandlerFunc(s.agentBrowserRelease)))
	mux.Handle("PATCH /api/v1/agent/turn/memory", s.withAgentCapability(http.HandlerFunc(s.updateAgentMemory)))
	mux.Handle("POST /api/v1/agent/turn/messages", s.withAgentCapability(http.HandlerFunc(s.addAgentProgressMessage)))
	mux.Handle(waitAgentInboxRoute, s.withAgentCapability(http.HandlerFunc(s.waitAgentInbox)))
	mux.Handle("POST /api/v1/agent/turn/inbox/claim", s.withAgentCapability(http.HandlerFunc(s.claimAgentInbox)))
	mux.Handle("POST /api/v1/agent/turn/inbox/ack", s.withAgentCapability(http.HandlerFunc(s.ackAgentInbox)))
	mux.Handle("POST /api/v1/agent/channel/reply-decision", s.withAgentCapability(http.HandlerFunc(s.setAgentChannelReplyDecision)))
	mux.Handle("POST /api/v1/agent/channel/topic-wake/revoke", s.withAgentCapability(http.HandlerFunc(s.revokeAgentFeishuTopicWake)))
	mux.Handle("POST /api/v1/agent/turn/attachments", s.withAgentCapability(http.HandlerFunc(s.uploadAgentAttachment)))
	mux.Handle("POST /api/v1/agent/turn/attachments/materialize", s.withAgentCapability(http.HandlerFunc(s.materializeAgentAttachment)))
	mux.Handle("GET /api/v1/agent/knowledge", s.withAgentCapability(http.HandlerFunc(s.agentKnowledge)))
	mux.Handle("GET /api/v1/agent/knowledge/guide", s.withAgentCapability(http.HandlerFunc(s.agentKnowledgeGuide)))
	mux.Handle("GET /api/v1/agent/knowledge/{knowledge}", s.withAgentCapability(http.HandlerFunc(s.agentKnowledgeEntry)))
	mux.Handle("POST /api/v1/agent/knowledge/candidates", s.withAgentCapability(http.HandlerFunc(s.submitAgentKnowledge)))
	mux.Handle("POST /api/v1/agent/knowledge/entries", s.withAgentCapability(http.HandlerFunc(s.submitAgentKnowledgeEntry)))
	mux.Handle("POST /api/v1/agent/knowledge/feedback", s.withAgentCapability(http.HandlerFunc(s.submitAgentKnowledgeFeedbackEntry)))
	mux.Handle("POST /api/v1/agent/knowledge/{knowledge}/feedback", s.withAgentCapability(http.HandlerFunc(s.submitAgentKnowledgeFeedback)))
	mux.Handle("POST /api/v1/agent/collaboration/batches", s.withAgentCapability(http.HandlerFunc(s.submitAgentCollaboration)))
	mux.Handle(waitAgentCollaborationRoute, s.withAgentCapability(http.HandlerFunc(s.waitAgentCollaboration)))
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
	mux.Handle("GET /api/v1/agent/channel/contacts", s.withAgentCapability(http.HandlerFunc(s.agentTaskChannelContacts)))
	mux.Handle("POST /api/v1/agent/channel/messages", s.withAgentCapability(http.HandlerFunc(s.sendAgentTaskChannelMessage)))
	mux.Handle("POST /api/v1/agent/channel/replies", s.withAgentCapability(http.HandlerFunc(s.sendAgentChannelReply)))
	// Narrow MCP for group task_route Turns. It uses the same turn capability
	// as the Agent API and exposes only channel_reply / channel_reply_decision.
	mux.Handle("POST /api/v1/agent/channel/mcp", s.withAgentCapability(http.HandlerFunc(s.agentChannelMCP)))
	// Core Task API MCP. Every method uses the same Turn capability as the
	// HTTP Agent API; write tools are additionally restricted to a writable
	// Main Turn inside the handler.
	mux.Handle("POST /api/v1/agent/core/mcp", s.withAgentCapability(http.HandlerFunc(s.agentCoreMCP)))
	// Knowledge domain MCP. It shares the Turn capability and is refused when
	// the Task's knowledge switch is off or the Turn resolves a channel route.
	mux.Handle("POST /api/v1/agent/knowledge/mcp", s.withAgentCapability(http.HandlerFunc(s.agentKnowledgeMCP)))
	// Workspace domain MCP. Read-only first slice: it is refused unless the Task
	// has a bound Workspace in its own Project and the Turn is not channel-hosted.
	mux.Handle("POST /api/v1/agent/workspace/mcp", s.withAgentCapability(http.HandlerFunc(s.agentWorkspaceMCP)))
	mux.Handle("POST /api/v1/agent/collab/mcp", s.withAgentCapability(http.HandlerFunc(s.agentCollabMCP)))
	// Shared-browser domain MCP. Read-only first slice; refused unless the Task
	// currently holds an active browser grant and the Turn is not channel-hosted.
	mux.Handle("POST /api/v1/agent/browser/mcp", s.withAgentCapability(http.HandlerFunc(s.agentBrowserMCP)))
	// Task hardware domain MCP. Read and guarded device I/O with an independent
	// injection trigger: the Task must have at least one bound hardware group.
	mux.Handle("POST /api/v1/agent/hardware/mcp", s.withAgentCapability(http.HandlerFunc(s.agentHardwareMCP)))
	mux.Handle("POST /api/v1/agent/desktop/mcp", s.withAgentCapability(http.HandlerFunc(s.agentDesktopMCP)))
	mux.Handle("POST /api/v1/agent/desktop/stop", s.withAgentCapability(http.HandlerFunc(s.agentDesktopStop)))
	mux.Handle("GET /api/v1/agent/mail/accounts", s.withAgentCapability(http.HandlerFunc(s.agentMailAccounts)))
	mux.Handle("GET /api/v1/agent/mail/templates", s.withAgentCapability(http.HandlerFunc(s.agentMailTemplates)))
	mux.Handle("GET /api/v1/agent/mail/templates/{id}", s.withAgentCapability(http.HandlerFunc(s.agentMailTemplateDetail)))
	mux.Handle("POST /api/v1/agent/mail/preview", s.withAgentCapability(http.HandlerFunc(s.agentMailPreview)))
	mux.Handle("POST /api/v1/agent/mail/messages", s.withAgentCapability(http.HandlerFunc(s.agentMailSend)))
	mux.Handle("GET /api/v1/agent/mail/messages/{id}", s.withAgentCapability(http.HandlerFunc(s.agentMailMessageStatus)))
	mux.Handle("POST /api/v1/agent/mail/mcp", s.withAgentCapability(http.HandlerFunc(s.agentMailMCP)))
	mux.Handle("POST /api/v1/agent/tasks", s.withAgentCapability(http.HandlerFunc(s.createAgentTask)))
	mux.Handle("GET /api/v1/agent/tasks/{task}", s.withAgentCapability(http.HandlerFunc(s.agentTaskStatus)))
	mux.Handle("PATCH /api/v1/tasks/{id}", s.withAuth(http.HandlerFunc(s.updateTaskTitle)))
	mux.Handle("POST /api/v1/tasks/{id}/complete", s.withAuth(http.HandlerFunc(s.completeTask)))
	mux.Handle("POST /api/v1/tasks/{id}/reopen", s.withAuth(http.HandlerFunc(s.reopenTask)))
	mux.Handle("POST /api/v1/tasks/{id}/permission-requests/{request}/resolve", s.withAuth(http.HandlerFunc(s.resolveTaskPermissionRequest)))
	mux.Handle("POST /api/v1/turns/{id}/interrupt", s.withAuth(http.HandlerFunc(s.interruptTurn)))
	mux.Handle("POST /api/v1/rounds/{id}/interrupt", s.withAuth(http.HandlerFunc(s.interruptRound)))
	mux.Handle("GET /api/v1/tasks/{id}/events", s.withAuth(http.HandlerFunc(s.taskEvents)))
	mux.Handle("GET /api/v1/events", s.withAuth(http.HandlerFunc(s.allEvents)))

	mux.Handle("GET /api/v1/knowledge", s.withAuth(http.HandlerFunc(s.listKnowledge)))
	mux.Handle("GET /api/v1/knowledge/{id}", s.withAuth(http.HandlerFunc(s.knowledgeDetail)))
	mux.Handle("GET /api/v1/knowledge/indexes", s.withAuth(http.HandlerFunc(s.knowledgeIndexes)))
	mux.Handle("GET /api/v1/knowledge/libraries", s.withAuth(http.HandlerFunc(s.listKnowledgeLibraries)))
	mux.Handle("POST /api/v1/knowledge/libraries/{id}/bind", s.withAuth(http.HandlerFunc(s.bindKnowledgeLibrary)))
	mux.Handle("POST /api/v1/knowledge/libraries/{id}/unbind", s.withAuth(http.HandlerFunc(s.unbindKnowledgeLibrary)))
	mux.Handle("DELETE /api/v1/knowledge/libraries/{id}", s.withAuth(http.HandlerFunc(s.deleteKnowledgeLibrary)))
	mux.Handle("POST /api/v1/projects/{id}/knowledge-space", s.withAuth(http.HandlerFunc(s.retryProjectKnowledgeSpace)))
	mux.Handle("POST /api/v1/projects/{id}/knowledge/detach", s.withAuth(http.HandlerFunc(s.detachProjectKnowledge)))
	mux.Handle("DELETE /api/v1/projects/{id}/knowledge", s.withAuth(http.HandlerFunc(s.deleteProjectKnowledge)))
	mux.Handle("POST /api/v1/knowledge", s.withAuth(http.HandlerFunc(s.createKnowledge)))
	mux.Handle("PUT /api/v1/knowledge/{id}", s.withAuth(http.HandlerFunc(s.updateKnowledge)))
	mux.Handle("DELETE /api/v1/knowledge/{id}", s.withAuth(http.HandlerFunc(s.deleteKnowledge)))
	mux.Handle("POST /api/v1/knowledge/{id}/verify", s.withAuth(http.HandlerFunc(s.verifyKnowledge)))
	mux.Handle("POST /api/v1/knowledge/{id}/feedback", s.withAuth(http.HandlerFunc(s.feedbackKnowledge)))
	mux.Handle("GET /api/v1/knowledge/proposals", s.withAuth(http.HandlerFunc(s.listKnowledgeProposals)))
	mux.Handle("POST /api/v1/knowledge/proposals/{id}/approve", s.withAuth(http.HandlerFunc(s.approveKnowledgeProposal)))
	mux.Handle("POST /api/v1/knowledge/proposals/{id}/reject", s.withAuth(http.HandlerFunc(s.rejectKnowledgeProposal)))
	mux.Handle("POST /api/v1/knowledge/proposals/batch", s.withAuth(http.HandlerFunc(s.batchKnowledgeProposalReviews)))
	// Phase 2/3 read-only vault API. Routes are always registered when the
	// manager exists; when the runtime switch is off every data endpoint
	// answers 404 knowledge_vault_disabled. The legacy knowledge routes are
	// untouched.
	if s.knowledgeVault != nil {
		mux.Handle("GET /api/v1/knowledge-vault/libraries", s.withAuth(http.HandlerFunc(s.knowledgeVaultLibraries)))
		mux.Handle("GET /api/v1/knowledge-vault/tree", s.withAuth(http.HandlerFunc(s.knowledgeVaultTree)))
		mux.Handle("GET /api/v1/knowledge-vault/files", s.withAuth(http.HandlerFunc(s.knowledgeVaultFiles)))
		mux.Handle("GET /api/v1/knowledge-vault/file", s.withAuth(http.HandlerFunc(s.knowledgeVaultFile)))
		mux.Handle("GET /api/v1/knowledge-vault/file/download", s.withAuth(http.HandlerFunc(s.knowledgeVaultFileDownload)))
		mux.Handle("GET /api/v1/knowledge-vault/open-info", s.withAuth(http.HandlerFunc(s.knowledgeVaultOpenInfo)))
		mux.Handle("POST /api/v1/knowledge-vault/reveal", s.withAuth(http.HandlerFunc(s.revealKnowledgeVaultPath)))
		mux.Handle("GET /api/v1/knowledge-vault/doc/{id}", s.withAuth(http.HandlerFunc(s.knowledgeVaultDocument)))
		mux.Handle("GET /api/v1/knowledge-vault/doc/{id}/diff", s.withAuth(http.HandlerFunc(s.knowledgeVaultDocumentDiff)))
		mux.Handle("GET /api/v1/knowledge-vault/search", s.withAuth(http.HandlerFunc(s.knowledgeVaultSearch)))
		mux.Handle("GET /api/v1/knowledge-vault/recent", s.withAuth(http.HandlerFunc(s.knowledgeVaultRecent)))
		mux.Handle("GET /api/v1/knowledge-vault/inbox", s.withAuth(http.HandlerFunc(s.knowledgeVaultInbox)))
		mux.Handle("GET /api/v1/knowledge-vault/skills", s.withAuth(http.HandlerFunc(s.knowledgeVaultSkills)))
		mux.Handle("PUT /api/v1/knowledge-vault/skills/auto-attach", s.withAuth(http.HandlerFunc(s.updateKnowledgeVaultSkillAutoAttach)))
		mux.Handle("GET /api/v1/settings/knowledge-vault", s.withAuth(http.HandlerFunc(s.knowledgeVaultSettings)))
		mux.Handle("PUT /api/v1/settings/knowledge-vault", s.withAuth(http.HandlerFunc(s.updateKnowledgeVaultSettings)))
		mux.Handle("POST /api/v1/settings/knowledge-vault/init", s.withAuth(http.HandlerFunc(s.initializeKnowledgeVault)))
		mux.Handle("POST /api/v1/settings/knowledge-vault/rebuild", s.withAuth(http.HandlerFunc(s.rebuildKnowledgeVaultIndex)))
		mux.Handle("POST /api/v1/settings/knowledge-vault/cutover-legacy", s.withAuth(http.HandlerFunc(s.cutoverLegacyKnowledge)))
		mux.Handle("POST /api/v1/settings/knowledge-vault/migrate", s.withAuth(http.HandlerFunc(s.migrateKnowledgeVault)))
		mux.Handle("POST /api/v1/settings/knowledge-vault/import", s.withAuth(http.HandlerFunc(s.importKnowledgeVault)))
		mux.Handle("GET /api/v1/knowledge-vault/git/status", s.withAuth(http.HandlerFunc(s.knowledgeVaultGitStatus)))
		mux.Handle("PUT /api/v1/knowledge-vault/git/remote", s.withAuth(http.HandlerFunc(s.updateKnowledgeVaultGitRemote)))
		mux.Handle("POST /api/v1/knowledge-vault/git/init", s.withAuth(http.HandlerFunc(s.initializeKnowledgeVaultGit)))
		mux.Handle("POST /api/v1/knowledge-vault/git/commit", s.withAuth(http.HandlerFunc(s.commitKnowledgeVaultGit)))
		mux.Handle("POST /api/v1/knowledge-vault/git/pull", s.withAuth(http.HandlerFunc(s.pullKnowledgeVaultGit)))
		mux.Handle("POST /api/v1/knowledge-vault/git/push", s.withAuth(http.HandlerFunc(s.pushKnowledgeVaultGit)))
		mux.Handle("POST /api/v1/knowledge-vault/git/sync", s.withAuth(http.HandlerFunc(s.syncKnowledgeVaultGit)))
	}
	mux.Handle("GET /api/v1/projects/{id}/product-lines", s.withAuth(http.HandlerFunc(s.listProductLines)))
	mux.Handle("POST /api/v1/projects/{id}/product-lines", s.withAuth(http.HandlerFunc(s.createProductLine)))
	mux.Handle("DELETE /api/v1/projects/{id}/product-lines/{line}", s.withAuth(http.HandlerFunc(s.deleteProductLine)))
	mux.Handle("GET /api/v1/skills", s.withAuth(http.HandlerFunc(s.listSkills)))
	// The Web skills page is read-only. Keep the old write paths registered so
	// legacy clients get an explicit 405 instead of the SPA catch-all's HTML.
	mux.Handle("POST /api/v1/skills", s.withAuth(http.HandlerFunc(s.rejectRemovedSkillWrite)))
	mux.Handle("PUT /api/v1/skills/{id}", s.withAuth(http.HandlerFunc(s.rejectRemovedSkillWrite)))
	mux.Handle("DELETE /api/v1/skills/{id}", s.withAuth(http.HandlerFunc(s.rejectRemovedSkillWrite)))

	mux.HandleFunc("/", s.serveWeb)
	return mux
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data: https: blob:; style-src 'self' 'unsafe-inline'; script-src 'self'")
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
