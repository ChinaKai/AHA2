package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/channel"
	"github.com/ChinaKai/AHA2/internal/codexaccount"
	"github.com/ChinaKai/AHA2/internal/execution"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"github.com/ChinaKai/AHA2/internal/httpapi"
	"github.com/ChinaKai/AHA2/internal/instancelock"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	mailpkg "github.com/ChinaKai/AHA2/internal/mail"
	"github.com/ChinaKai/AHA2/internal/mailworker"
	"github.com/ChinaKai/AHA2/internal/managedprocess"
	"github.com/ChinaKai/AHA2/internal/outboundproxy"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
	"github.com/ChinaKai/AHA2/internal/webassets"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

var version = "dev"
var webVersion = ""

func main() {
	command, args, err := parseCommand(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if command == "version" {
		fmt.Println("aha2", version)
		return
	}
	if command == "listen" {
		err = printEffectiveListen(args)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if command == "local-ingest-secret" {
		err = storeLocalIngestSecret(args)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if command == "agent-proxy" {
		// Hidden by design: this runs on the workspace inside an SSH session,
		// with the tunnel on stdin/stdout. It is not an operator command.
		if err := runAgentProxy(args, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if command == "db" {
		os.Exit(dbCommand(args, os.Stdout, os.Stderr))
	}
	switch command {
	case "serve":
		err = serve(args)
	case "service-run":
		err = runPlatformService(args)
	case "import-aha1-knowledge":
		err = importAHA1Knowledge(args)
	case "knowledge-vault":
		err = knowledgeVault(args)
	}
	if err != nil {
		slog.Error("AHA2 stopped", "error", err)
		os.Exit(1)
	}
}

func parseCommand(args []string) (string, []string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "serve", args, nil
	}
	switch args[0] {
	case "serve":
		return "serve", args[1:], nil
	case "version":
		if len(args) != 1 {
			return "", nil, fmt.Errorf("version does not accept arguments")
		}
		return "version", nil, nil
	case "listen":
		return "listen", args[1:], nil
	case "local-ingest-secret":
		return "local-ingest-secret", args[1:], nil
	case "agent-proxy":
		return "agent-proxy", args[1:], nil
	case "import-aha1-knowledge":
		return "import-aha1-knowledge", args[1:], nil
	case "knowledge-vault":
		return "knowledge-vault", args[1:], nil
	case "db":
		return "db", args[1:], nil
	case "service":
		if len(args) < 2 || args[1] != "run" {
			return "", nil, fmt.Errorf("usage: aha2 service run [serve flags]")
		}
		return "service-run", args[2:], nil
	default:
		return "", nil, fmt.Errorf("unknown command %q", args[0])
	}
}

type serveOptions struct {
	listen                string
	dataDir               string
	setupToken            string
	setupTokenFile        string
	secureCookie          bool
	allowCrossOrigin      bool
	agentAPIURL           string
	attachmentMaxBytes    int64
	mailTaskBaseURL       string
	mailDownloadBaseURL   string
	mailDownloadListen    string
	logLevel              string
	codexBinary           string
	claudeBinary          string
	dshBinary             string
	localOwnerIngest      bool
	knowledgeVaultEnabled bool
	// knowledgeVaultEnabledSet records an explicit support-only override of the
	// startup default. Normal installs leave it false and let the backend choose
	// fresh-install (vault on) vs upgrade (legacy until verified).
	knowledgeVaultEnabledSet   bool
	knowledgeVaultRuntimeRead  bool
	knowledgeVaultRuntimeWrite bool
	knowledgeVaultPath         string
	knowledgeVaultIndex        string
	// deprecatedLiveInput records old live-inbox switches that were supplied.
	// They are accepted so existing service definitions keep starting, but they
	// no longer select any behavior.
	deprecatedLiveInput []string
	// deprecatedAgentAPIInsecure records the removed plaintext-HTTP switch. Old
	// scheduled tasks still pass it; accepting it as a no-op is what keeps them
	// starting after the security convergence.
	deprecatedAgentAPIInsecure []string
}

func serve(args []string) error {
	options, err := parseServeOptions("aha2 serve", args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runControlPlane(ctx, options, nil)
}

func parseServeOptions(name string, args []string) (serveOptions, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	listen := flags.String("listen", envOr("AHA2_LISTEN", "0.0.0.0:8766"), "HTTP listen address")
	dataDir := flags.String("data-dir", envOr("AHA2_DATA_DIR", ".data"), "AHA2 data directory")
	setupToken := flags.String("setup-token", os.Getenv("AHA2_SETUP_TOKEN"), "owner setup and local password recovery token")
	setupTokenFile := flags.String("setup-token-file", os.Getenv("AHA2_SETUP_TOKEN_FILE"), "file containing the owner setup and recovery token")
	secureCookie := flags.Bool("secure-cookie", envBool("AHA2_SECURE_COOKIE"), "mark session cookie Secure")
	allowCrossOrigin := flags.Bool("allow-cross-origin", envBool("AHA2_ALLOW_CROSS_ORIGIN"), "disable Origin host validation for reverse proxies and embedded WebViews")
	agentAPIURL := flags.String("agent-api-url", os.Getenv("AHA2_AGENT_API_URL"), "Agent-reachable AHA2 base URL (https or loopback; optional)")
	attachmentMaxMiB := flags.Int64("attachment-materialize-max-mib", workspace.DefaultScratchAttachmentBytes>>20, "maximum size in MiB for one Task attachment copied to Agent scratch (1-200)")
	// Removed switch. Kept as a parse-only no-op so service definitions written
	// by an older installer still start; it cannot re-enable plaintext HTTP.
	flags.Bool("allow-insecure-agent-api", false, "deprecated no-op: non-loopback http Agent API addresses are not supported")
	mailTaskBaseURL := flags.String("mail-task-base-url", os.Getenv("AHA2_MAIL_TASK_BASE_URL"), "external https base URL used for task.url mail template variables")
	mailDownloadBaseURL := flags.String("mail-download-base-url", os.Getenv("AHA2_MAIL_DOWNLOAD_BASE_URL"), "external https base URL of the independent controlled-download entry")
	mailDownloadListen := flags.String("mail-download-listen", os.Getenv("AHA2_MAIL_DOWNLOAD_LISTEN"), "optional separate listen address for the controlled-download entry (for example 127.0.0.1:8767)")
	logLevel := flags.String("log-level", envOr("AHA2_LOG_LEVEL", "info"), "debug, info, warn, error")
	codexBinary := flags.String("codex-bin", envOr("AHA2_CODEX_BIN", "codex"), "Codex executable")
	claudeBinary := flags.String("claude-bin", envOr("AHA2_CLAUDE_BIN", "claude"), "Claude Code executable")
	dshBinary := flags.String("dsh-bin", envOr("AHA2_DSH_BIN", "dsh"), "DeepSeek Harness executable")
	localOwnerIngest := flags.Bool("local-owner-ingest", envBool("AHA2_LOCAL_OWNER_INGEST"), "enable the loopback-only local owner message ingest endpoint")
	knowledgeVaultEnabled := flags.Bool("knowledge-vault-enabled", envBool("AHA2_KNOWLEDGE_VAULT_ENABLED"), "support-only startup override for the file-backed knowledge vault switch (normally leave unset so fresh installs default on and upgrades wait for verified migration)")
	knowledgeVaultRuntimeRead := flags.Bool("knowledge-vault-runtime-read", envBoolOr("AHA2_KNOWLEDGE_VAULT_RUNTIME_READ", true), "support-only emergency read cutoff (not shown in the Web UI; unset AHA2_KNOWLEDGE_VAULT_READ_PROJECTS means all projects)")
	knowledgeVaultRuntimeWrite := flags.Bool("knowledge-vault-runtime-write", envBoolOr("AHA2_KNOWLEDGE_VAULT_RUNTIME_WRITE", true), "support-only emergency Agent write cutoff (the owner-visible switch still decides the source)")
	knowledgeVaultPath := flags.String("knowledge-vault-path", os.Getenv("AHA2_KNOWLEDGE_VAULT_PATH"), "knowledge vault directory (default <data-dir>/knowledge-vault)")
	knowledgeVaultIndex := flags.String("knowledge-vault-index", os.Getenv("AHA2_KNOWLEDGE_VAULT_INDEX"), "knowledge index database (default <data-dir>/knowledge-vault-index.db)")
	// Live inbox delivery is always enabled when the runner can stream stdin.
	// Keep the old switches as parse-only no-ops so existing service definitions
	// and scripts still start; they cannot turn the behavior on or off.
	flags.Bool("claude-stream-input", false, "deprecated no-op: live inbox delivery is always enabled when the runner can stream stdin")
	flags.Bool("codex-stream-input", false, "deprecated no-op: live inbox delivery is always enabled when the runner can stream stdin")
	if err := flags.Parse(args); err != nil {
		return serveOptions{}, err
	}
	if flags.NArg() != 0 {
		return serveOptions{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *attachmentMaxMiB < 1 || *attachmentMaxMiB > workspace.MaxScratchAttachmentBytes>>20 {
		return serveOptions{}, fmt.Errorf("attachment-materialize-max-mib must be between 1 and %d", workspace.MaxScratchAttachmentBytes>>20)
	}
	deprecatedLiveInput := deprecatedLiveInputSettings(flags)
	deprecatedAgentAPIInsecure := deprecatedAgentAPIInsecureSettings(flags)
	knowledgeVaultEnabledSet := false
	flags.Visit(func(item *flag.Flag) {
		if item.Name == "knowledge-vault-enabled" {
			knowledgeVaultEnabledSet = true
		}
	})
	if _, present := os.LookupEnv("AHA2_KNOWLEDGE_VAULT_ENABLED"); present {
		knowledgeVaultEnabledSet = true
	}
	return serveOptions{
		listen: *listen, dataDir: *dataDir, setupToken: *setupToken, setupTokenFile: *setupTokenFile,
		secureCookie: *secureCookie, allowCrossOrigin: *allowCrossOrigin, agentAPIURL: *agentAPIURL,
		attachmentMaxBytes:  *attachmentMaxMiB << 20,
		mailTaskBaseURL:     *mailTaskBaseURL,
		mailDownloadBaseURL: *mailDownloadBaseURL, mailDownloadListen: *mailDownloadListen,
		logLevel: *logLevel, codexBinary: *codexBinary,
		claudeBinary: *claudeBinary, dshBinary: *dshBinary,
		localOwnerIngest:           *localOwnerIngest,
		knowledgeVaultEnabled:      *knowledgeVaultEnabled,
		knowledgeVaultEnabledSet:   knowledgeVaultEnabledSet,
		knowledgeVaultRuntimeRead:  *knowledgeVaultRuntimeRead,
		knowledgeVaultRuntimeWrite: *knowledgeVaultRuntimeWrite,
		knowledgeVaultPath:         *knowledgeVaultPath,
		knowledgeVaultIndex:        *knowledgeVaultIndex,
		deprecatedLiveInput:        deprecatedLiveInput,
		deprecatedAgentAPIInsecure: deprecatedAgentAPIInsecure,
	}, nil
}

// deprecatedAgentAPIInsecureSettings reports the removed plaintext-HTTP switch.
func deprecatedAgentAPIInsecureSettings(flags *flag.FlagSet) []string {
	var settings []string
	flags.Visit(func(item *flag.Flag) {
		if item.Name == "allow-insecure-agent-api" {
			settings = append(settings, "--"+item.Name)
		}
	})
	for _, name := range []string{"AHA2_ALLOW_INSECURE_AGENT_API"} {
		if os.Getenv(name) != "" {
			settings = append(settings, name)
		}
	}
	return settings
}

// deprecatedLiveInputSettings reports old live-inbox switches supplied through
// flags or the environment. They are ignored, but logging them makes the
// no-op explicit instead of leaving an operator to wonder why nothing changed.
func deprecatedLiveInputSettings(flags *flag.FlagSet) []string {
	var settings []string
	flags.Visit(func(item *flag.Flag) {
		if item.Name == "claude-stream-input" || item.Name == "codex-stream-input" {
			settings = append(settings, "--"+item.Name)
		}
	})
	for _, name := range []string{"AHA2_CLAUDE_STREAM_INPUT", "AHA2_CODEX_STREAM_INPUT"} {
		if _, present := os.LookupEnv(name); present {
			settings = append(settings, name)
		}
	}
	return settings
}

func runControlPlane(ctx context.Context, options serveOptions, ready func()) error {
	level := new(slog.LevelVar)
	switch options.logLevel {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	for _, setting := range options.deprecatedLiveInput {
		logger.Warn("deprecated live input setting ignored; live inbox delivery is always enabled when the runner can stream stdin", "setting", setting)
	}
	for _, setting := range options.deprecatedAgentAPIInsecure {
		logger.Warn("deprecated Agent API insecure switch ignored; only https:// or loopback http:// addresses are supported", "setting", setting)
	}
	absoluteDataDir, err := filepath.Abs(options.dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absoluteDataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	// The offline compaction CLI takes the same lock. An idle control plane
	// keeps no SQLite transaction open, so this advisory lock is what makes
	// "stop AHA2 first" enforceable instead of a best-effort instruction.
	instanceLock, err := instancelock.Acquire(absoluteDataDir)
	if err != nil {
		if errors.Is(err, instancelock.ErrHeld) {
			return fmt.Errorf("data directory %s is already in use by another AHA2 process: %w", absoluteDataDir, err)
		}
		return fmt.Errorf("lock data directory: %w", err)
	}
	defer instanceLock.Close()
	knowledgeVaultConfig, err := (knowledgevault.Config{
		Enabled: options.knowledgeVaultEnabled, DataDir: absoluteDataDir,
		VaultPath: options.knowledgeVaultPath, IndexPath: options.knowledgeVaultIndex,
	}).Normalized()
	if err != nil {
		return fmt.Errorf("resolve knowledge vault configuration: %w", err)
	}
	// The manager always exists so the Phase 3 routes stay registered and can
	// report "disabled" with a plain 404. The effective value is applied after
	// the database is open: a Web toggle (configured=1) outranks env/flags.
	knowledgeVaultManager, err := knowledgevault.NewManager(knowledgeVaultConfig)
	if err != nil {
		return fmt.Errorf("create knowledge vault manager: %w", err)
	}
	defer knowledgeVaultManager.Close()
	if options.setupTokenFile == "" {
		options.setupTokenFile = filepath.Join(absoluteDataDir, "setup-token")
	}
	if options.setupToken == "" {
		if value, readErr := os.ReadFile(options.setupTokenFile); readErr == nil {
			options.setupToken = strings.TrimSpace(string(value))
		}
	}
	database, err := store.Open(ctx, filepath.Join(absoluteDataDir, "aha2.db"))
	if err != nil {
		return err
	}
	defer database.Close()
	// task-711: hardware terminal IO now lives in <data-dir>/hardware-io.
	// SQLite-era rows are dropped once at startup (Owner decision); a failure
	// is logged and does not block startup because nothing reads the table.
	if cleared, clearErr := database.ClearLegacyHardwareIO(ctx); clearErr != nil {
		logger.Error("clear legacy hardware_io rows failed", "error", clearErr)
	} else if !cleared.AlreadyEmpty {
		logger.Info("cleared legacy hardware_io rows; run `aha2 db compact` with the service stopped to shrink aha2.db",
			"rows", cleared.Rows, "freed_pages", cleared.FreedPages, "sequence_floor", cleared.FloorSeq, "duration_ms", cleared.DurationMS)
	}
	go runHardwareIOMaintenance(ctx, database, logger)
	// Single-switch startup precedence: a Web toggle persisted with configured=1
	// is authoritative. Otherwise the backend decides fresh install vs upgrade:
	// a fresh install defaults to the vault, while an upgrade with legacy
	// knowledge stays on legacy until a migration/import is verified. The old
	// env/flag is a support-only override and is NOT persisted.
	vaultSettings, err := database.KnowledgeVaultSettings(ctx)
	if err != nil {
		return fmt.Errorf("read knowledge vault settings: %w", err)
	}
	legacyInventory, err := database.LegacyKnowledgeInventory(ctx)
	if err != nil {
		return fmt.Errorf("read legacy knowledge inventory: %w", err)
	}
	startupDefault := knowledgevault.DefaultVaultEnabled(legacyInventory.HasLegacy(), vaultSettings.VaultPopulated())
	if options.knowledgeVaultEnabledSet {
		startupDefault = options.knowledgeVaultEnabled
	}
	vaultEnabled, vaultSource := knowledgevault.EffectiveStartupSwitch(
		vaultSettings.Configured, vaultSettings.Enabled, startupDefault,
	)
	legacyCutoverApplied := false
	if applied, cutoverErr := database.LegacyCutoverApplied(ctx); cutoverErr == nil {
		legacyCutoverApplied = applied
	} else {
		logger.Warn("read legacy knowledge cutover state", "error", cutoverErr)
	}
	if legacyCutoverApplied && !vaultEnabled {
		// After the old tables are dropped there is no usable legacy source.
		// Keeping the vault closed would make every knowledge read fail, so the
		// one-way cutover pins the runtime to the vault.
		vaultEnabled = true
		vaultSource = knowledgevault.SourceDatabase
		logger.Warn("legacy knowledge tables are gone; forcing the file vault on")
	}
	knowledgeVaultManager.SetSource(vaultSource)
	knowledgeVaultManager.SetRuntimeWrite(options.knowledgeVaultRuntimeWrite)
	// A previously configured vault can be temporarily missing. Keep its
	// readiness diagnosable without creating a new vault in its place.
	if vaultEnabled && !vaultSettings.Configured && !legacyInventory.HasLegacy() && !vaultSettings.VaultPopulated() && !legacyCutoverApplied {
		if err := initializeFreshKnowledgeVault(ctx, knowledgeVaultManager); err != nil {
			logger.Warn("knowledge vault startup initialization failed; continuing startup",
				"vault", knowledgeVaultConfig.VaultPath, "index", knowledgeVaultConfig.IndexPath, "error", err)
		}
	}
	if err := knowledgeVaultManager.SetEnabled(ctx, vaultEnabled); err != nil {
		// A broken index must not keep the whole control plane from starting.
		// The index is derived, so quarantine and rebuild it from the files
		// once; if that also fails, report it and keep the vault closed.
		if _, recoverErr := knowledgeVaultManager.RecoverIndex(ctx); recoverErr == nil {
			if reopenErr := knowledgeVaultManager.SetEnabled(ctx, vaultEnabled); reopenErr == nil {
				logger.Warn("knowledge vault index was repaired automatically", "error", err)
			} else {
				logger.Warn("knowledge vault could not be opened after index repair", "error", reopenErr)
			}
		} else {
			logger.Warn("knowledge vault could not be opened; starting with it disabled",
				"vault", knowledgeVaultConfig.VaultPath, "index", knowledgeVaultConfig.IndexPath,
				"error", err, "recovery_error", recoverErr)
		}
	}
	logger.Info("knowledge vault switch",
		"enabled", knowledgeVaultManager.Enabled(), "source", vaultSource,
		"legacy_entries", legacyInventory.Entries, "legacy_skills", legacyInventory.Skills+legacyInventory.SkillPackages,
		"migration_verified", !vaultSettings.MigrationVerifiedAt.IsZero(),
		"import_verified", !vaultSettings.ImportVerifiedAt.IsZero(),
		"vault", knowledgeVaultConfig.VaultPath, "index", knowledgeVaultConfig.IndexPath)
	if !options.knowledgeVaultRuntimeRead || !options.knowledgeVaultRuntimeWrite || app.KnowledgeVaultReadScope() != "all" {
		// Support-only overrides: logged for diagnostics, never rendered as owner
		// switches. They can only narrow the manager switch, never replace it.
		logger.Warn("knowledge vault support overrides active",
			"runtime_read", options.knowledgeVaultRuntimeRead,
			"runtime_write", options.knowledgeVaultRuntimeWrite,
			"read_projects", app.KnowledgeVaultReadScope())
	}
	// The periodic sync worker gets its own pool over the same WAL file. Long
	// export/pull phases no longer occupy the Web API connection; writes still
	// serialize through SQLite with busy_timeout and an immediate transaction
	// lock. Close order matters: this handle closes before the primary one.
	syncDatabase, err := database.OpenSecondary(ctx)
	if err != nil {
		return fmt.Errorf("open sync database: %w", err)
	}
	defer syncDatabase.Close()
	recovery, err := database.RecoverInterrupted(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	if recovery.Turns > 0 || recovery.PetApprovals > 0 {
		logger.Warn(
			"recovered interrupted runtime",
			"turns", recovery.Turns,
			"tasks", recovery.Tasks,
			"requeued_inbox_items", recovery.RequeuedInboxItems,
			"pet_approvals", recovery.PetApprovals,
		)
	}
	secretStore, err := secrets.Open(filepath.Join(absoluteDataDir, "secrets.json"))
	if err != nil {
		return err
	}
	proxyRuntime := outboundproxy.New(database, secretStore)
	proxyRuntime.Start(ctx)
	defer proxyRuntime.Close()
	registrationOpen, err := database.OwnerExists(ctx)
	if err != nil {
		return err
	}
	if !registrationOpen && options.setupToken == "" {
		options.setupToken, err = generatedSetupToken()
		if err != nil {
			return err
		}
		if err := os.WriteFile(options.setupTokenFile, []byte(options.setupToken+"\n"), 0o600); err != nil {
			return fmt.Errorf("write setup token file: %w", err)
		}
		if err := os.Chmod(options.setupTokenFile, 0o600); err != nil {
			return fmt.Errorf("restrict setup token file: %w", err)
		}
		logger.Warn("owner registration is open", "setup_token_file", options.setupTokenFile)
	}
	authService := auth.NewService(database, options.setupToken, 14*24*time.Hour)
	codexAccounts := codexaccount.New(ctx, database, secretStore, absoluteDataDir, options.codexBinary)
	codexAccounts.SetProxyRuntime(proxyRuntime)
	executor := execution.Executor{
		Codex:    backend.Codex{Binary: options.codexBinary},
		Claude:   backend.Claude{Binary: options.claudeBinary},
		DSH:      backend.DSH{Binary: options.dshBinary},
		Settings: database,
	}
	appService := app.NewService(database, secretStore, executor)
	knowledgeVaultManager.StartAutoIndex(ctx, 0)
	appService.SetLegacyKnowledgeDisabled(legacyCutoverApplied)
	appService.SetKnowledgeVault(knowledgeVaultManager, options.knowledgeVaultRuntimeRead)
	appService.SetKnowledgeVaultRuntimeWrite(options.knowledgeVaultRuntimeWrite)
	// Model A startup reconciliation: manifest spaces must have their root
	// index, ready projects must be indexed, and channel/system hosts get their
	// empty identity space back. Failures are reported and left retryable; a
	// broken space never silently falls back to the legacy knowledge tables.
	if reconcile, reconcileErr := appService.ReconcileKnowledgeSpaces(ctx); reconcileErr != nil {
		logger.Warn("knowledge space reconciliation incomplete", "error", reconcileErr)
	} else if reconcile.Enabled {
		logger.Info("knowledge space reconciliation",
			"checked", reconcile.Checked, "ready", reconcile.Ready,
			"provisioned", reconcile.Provisioned, "cleaned", reconcile.Cleaned,
			"errors", reconcile.MarkedError,
			"orphans", reconcile.Orphans, "warnings", len(reconcile.Warnings))
	}
	// Background auto-sync commits gated text changes, runs pull --ff-only and
	// pushes. The syncer is wired only after reconciliation so its first
	// preflight sees the final space layout; it never runs without the owner
	// switch and never merges, resets or force-pushes.
	knowledgeVaultSyncer := knowledgevault.NewSyncer(knowledgeVaultManager, database, knowledgevault.SyncerConfig{
		Emit: func(event knowledgevault.SyncEvent) {
			appService.RecordKnowledgeSyncEvent(context.Background(), event.Kind, event.Detail)
		},
		Recovered: func() {
			_, _ = appService.ResolveKnowledgeSyncNotifications(context.Background())
		},
	})
	knowledgeVaultManager.SetAutoCommitRequester(knowledgeVaultSyncer.NotifyWrite)
	appService.SetKnowledgeSyncRecovery(knowledgeVaultSyncer)
	knowledgeVaultSyncer.Start(ctx)
	defer knowledgeVaultSyncer.Stop()
	appService.SetBuildInfo(version, webVersion, time.Now().UTC())
	appService.MigratePetTaskGoal(ctx)
	appService.MigrateOwnerAssistantUnifiedModel(ctx)
	appService.StartPetTaskNotifications()
	appService.SetProxyRuntime(proxyRuntime)
	appService.SetRunContext(ctx)
	appService.StartPetAutonomy()
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := appService.Shutdown(shutdownContext); err != nil {
			logger.Warn("agent runtime shutdown incomplete", "error", err)
		}
	}()
	appService.SetWorkspacePreparer(execution.WorkspacePreparer{})
	appService.SetCodexAccountManager(codexAccounts)
	agentCapabilities := agentapi.NewCapabilities()
	managedProcesses := managedprocess.NewManager()
	appService.SetManagedProcesses(managedProcesses)
	defer managedProcesses.Close()
	resolvedAgentAPIURL := resolveAgentAPIURL(options.listen, options.agentAPIURL)
	if err := validateAgentAPIURL(resolvedAgentAPIURL); err != nil {
		// An older service definition may still carry a plaintext LAN address.
		// Refusing to start would take every task down to enforce a boundary the
		// automatic selection path can satisfy; drop the value instead and say so.
		logger.Warn("ignoring Agent API address outside the https/loopback boundary; using the listen-derived address", "error", err)
		resolvedAgentAPIURL = resolveAgentAPIURL(options.listen, "")
		if err := validateAgentAPIURL(resolvedAgentAPIURL); err != nil {
			return err
		}
	}
	appService.SetAgentAPI(agentCapabilities, resolvedAgentAPIURL)
	channelService := channel.New(channel.Config{Store: database, App: appService, Secrets: secretStore, DataDir: absoluteDataDir, Logger: logger})
	appService.SetPetChannelSender(channelService)
	if err := channelService.RefreshPlugins(ctx); err != nil {
		logger.Warn("channel plugin discovery failed", "error", err)
	}
	channelService.Start(ctx)
	hardwareManager := hardware.NewManager(database)
	appService.SetPetHardwareDisconnector(hardwareManager)
	defer hardwareManager.Close()
	// The shared browser lives in a separate host process so it can run in the
	// owner's interactive Windows session. Unless an explicit loopback URL is
	// supplied for troubleshooting, AHA discovers the packaged host, allocates a
	// random loopback port and a fresh token, and supervises the child itself.
	browserHostURL := strings.TrimSpace(os.Getenv("AHA2_BROWSER_HOST_URL"))
	browserHostToken := strings.TrimSpace(os.Getenv("AHA2_BROWSER_HOST_TOKEN"))
	if browserHostToken == "" {
		if tokenFile := strings.TrimSpace(os.Getenv("AHA2_BROWSER_HOST_TOKEN_FILE")); tokenFile != "" {
			if data, readErr := os.ReadFile(tokenFile); readErr == nil {
				browserHostToken = strings.TrimSpace(string(data))
			} else {
				logger.Warn("browser host token file could not be read", "error", readErr)
			}
		}
	}
	// Backend/agent processes inherit this environment. Remove the secret so a
	// Task shell cannot read the loopback host token from its parent process.
	_ = os.Unsetenv("AHA2_BROWSER_HOST_TOKEN")
	_ = os.Unsetenv("AHA2_BROWSER_HOST_TOKEN_FILE")
	browserHostID := envOr("AHA2_BROWSER_HOST_ID", browser.DefaultHostID)
	var browserHost browser.Host
	var browserHostManager *browser.HostManager
	if browserHostURL != "" {
		// Test/troubleshooting override: use an already-running host.
		browserHost = browser.NewHTTPHost(browserHostURL, browserHostToken)
		logger.Info("shared browser host configured", "host_id", browserHostID, "source", "AHA2_BROWSER_HOST_URL")
	} else {
		browserHostManager = browser.NewHostManager(browser.HostManagerOptions{
			DataDir: filepath.Join(absoluteDataDir, "browser-profiles"),
			HostID:  browserHostID,
			Logger:  logger,
		})
		browserHost = browserHostManager
		go func() {
			// Starting the host must never block or fail AHA's own startup. A
			// failure leaves the manager fail-closed and the resources page shows
			// the actionable status instead of a configured-URL prompt.
			if startErr := browserHostManager.Start(ctx); startErr != nil {
				logger.Warn("browser host auto-start failed; browser features fail closed",
					"code", browser.Code(startErr), "detail", browser.PublicMessage(startErr))
				return
			}
			logger.Info("browser host auto-started", "host_id", browserHostID)
		}()
		defer func() {
			stopContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if stopErr := browserHostManager.Stop(stopContext); stopErr != nil {
				logger.Warn("browser host shutdown incomplete", "error", stopErr)
			}
		}()
	}
	browserService := browser.NewService(database, browserHost, browserHostID)
	if err := appService.ResumePending(ctx); err != nil {
		return fmt.Errorf("resume pending agent inbox: %w", err)
	}
	removeLegacyStubConfiguration(ctx, database)
	if coded, err := database.BackfillTaskCodes(ctx); err != nil {
		logger.Warn("task code backfill failed", "error", err)
	} else if coded > 0 {
		logger.Info("backfilled task codes", "tasks", coded)
	}
	if backfilled, err := database.BackfillProviders(ctx); err != nil {
		logger.Warn("provider backfill failed", "error", err)
	} else if backfilled > 0 {
		logger.Info("backfilled legacy providers", "providers", backfilled)
	}
	apiServer := httpapi.New(httpapi.Config{
		Store: database, SyncStore: syncDatabase, Auth: authService, App: appService, Web: webassets.FS(),
		Logger: logger, SecureCookie: options.secureCookie, AllowCrossOrigin: options.allowCrossOrigin,
		DetectWorkspace:        workspace.Detect,
		Secrets:                secretStore,
		Hardware:               hardwareManager,
		Browser:                browserService,
		CodexAccounts:          codexAccounts,
		AgentCapabilities:      agentCapabilities,
		ScratchAttachmentLimit: options.attachmentMaxBytes,
		ManagedProcesses:       managedProcesses,
		OutboundProxy:          proxyRuntime,
		Channels:               channelService,
		MailTaskBaseURL:        options.mailTaskBaseURL,
		MailDownloadBaseURL:    options.mailDownloadBaseURL,
		Version:                version,
		WebVersion:             webVersion,
		ListenAddress:          options.listen,
		LocalOwnerIngest:       options.localOwnerIngest,
		KnowledgeVault:         knowledgeVaultManager,
		KnowledgeVaultSync:     knowledgeVaultSyncer,
	})
	if options.allowCrossOrigin {
		logger.Warn("Origin host validation disabled")
	}
	appService.SetPetSecurityApplier(apiServer)
	apiServer.StartSyncLoop(ctx)
	defer apiServer.CloseDesktop()
	mailWorker := mailworker.New(mailworker.Config{
		Store: database, Secrets: secretStore, Sender: mailpkg.NewSMTPSender(),
		Logger: logger, DownloadBaseURL: options.mailDownloadBaseURL,
		Publisher: appService.Hub(),
	})
	// The worker's final writeback is detached from the run context, but the
	// process still waits for the round to finish so a known SMTP 250 is not
	// lost on shutdown.
	workerCtx, workerCancel := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		mailWorker.Run(workerCtx)
	}()
	defer func() {
		workerCancel()
		select {
		case <-workerDone:
		case <-time.After(10 * time.Second):
			logger.Warn("mail worker shutdown timed out")
		}
	}()
	if address := strings.TrimSpace(options.mailDownloadListen); address != "" {
		downloadServer := &http.Server{
			Addr: address, Handler: apiServer.MailDownloadHandler(),
			ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second,
			MaxHeaderBytes: 1 << 20,
		}
		downloadListener, listenErr := net.Listen("tcp", address)
		if listenErr != nil {
			return fmt.Errorf("listen mail download entry: %w", listenErr)
		}
		defer downloadListener.Close()
		go func() {
			logger.Info("mail controlled-download entry listening", "address", downloadListener.Addr().String())
			if serveErr := downloadServer.Serve(downloadListener); serveErr != nil && serveErr != http.ErrServerClosed {
				logger.Warn("mail controlled-download entry stopped", "error", serveErr)
			}
		}()
		defer downloadServer.Close()
	} else if strings.TrimSpace(options.mailDownloadBaseURL) != "" {
		logger.Warn("mail download base URL is configured but no separate listener is set; the front proxy must route /d/")
	}
	server := &http.Server{
		Addr:              options.listen,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	listener, err := net.Listen("tcp", options.listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	if address, ok := listener.Addr().(*net.TCPAddr); ok {
		channelService.SetRuntimeBaseURL(fmt.Sprintf("http://127.0.0.1:%d", address.Port))
	}
	errors := make(chan error, 1)
	go func() {
		logger.Info("AHA2 listening", "address", listener.Addr().String(), "data_dir", absoluteDataDir)
		errors <- server.Serve(listener)
	}()
	if ready != nil {
		ready()
	}
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := appService.Shutdown(shutdownContext); err != nil {
			logger.Warn("agent runtime shutdown incomplete", "error", err)
		}
		return server.Shutdown(shutdownContext)
	case err := <-errors:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func initializeFreshKnowledgeVault(ctx context.Context, manager *knowledgevault.Manager) error {
	config := manager.Config()
	if knowledgevault.VaultInitialized(config.VaultPath) {
		// A startup interrupted after creating the skeleton can still be
		// missing the derived index. Rebuild it before accepting requests.
		if _, err := os.Stat(config.IndexPath); errors.Is(err, os.ErrNotExist) {
			_, err := manager.Rebuild(ctx)
			return err
		}
		return nil
	}
	// A failed bootstrap can leave empty directories before the manifest is
	// written. Retry those, and leave manifest-less vaults with files alone.
	if err := filepath.WalkDir(config.VaultPath, func(path string, entry os.DirEntry, err error) error {
		if path == config.VaultPath && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return fmt.Errorf("vault has no manifest and contains %s; automatic initialization requires an empty vault", path)
		}
		return nil
	}); err != nil {
		return err
	}
	_, err := manager.Initialize(ctx, false)
	return err
}

func resolveAgentAPIURL(listen, configured string) string {
	if value := strings.TrimRight(strings.TrimSpace(configured), "/"); value != "" {
		return value
	}
	address := strings.TrimSpace(listen)
	if address == "" {
		address = "0.0.0.0:8766"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func validateAgentAPIURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return fmt.Errorf("agent-api-url must be an http:// or https:// base URL without credentials")
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("agent-api-url must not contain a path, query, or fragment")
	}
	if parsed.Scheme == "https" || agentAPIHostIsLoopback(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("non-loopback agent-api-url requires https://")
}

func agentAPIHostIsLoopback(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	address := net.ParseIP(strings.TrimSpace(host))
	return address != nil && address.IsLoopback()
}

// removeLegacyStubConfiguration drops the auto-created stub backend model and
// env group that existed in earlier AHA2 builds. New installs never create it;
// this cleans up data from a previous version.
func removeLegacyStubConfiguration(ctx context.Context, database *store.Store) {
	_ = database.DeleteModel(ctx, "model_stub")
	_ = database.DeleteEnvGroup(ctx, "env_stub")
	_ = database.DeleteProvider(ctx, "stub")
}

func generatedSetupToken() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string) bool {
	switch os.Getenv(name) {
	case "1", "true", "TRUE", "yes", "YES":
		return true
	default:
		return false
	}
}

// envBoolOr is envBool with a non-false default. An unset or blank value keeps
// the fallback; any other value that is not an explicit truthy word disables
// the switch. That fail-closed direction matches the old envBool and keeps a
// misspelled rollback value (for example "off" or "disabled") from silently
// leaving a default-on vault gate enabled.
func envBoolOr(name string, fallback bool) bool {
	value, present := os.LookupEnv(name)
	if !present || strings.TrimSpace(value) == "" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// printEffectiveListen reports the listen address the running instance would use,
// resolving the persisted override against the flag default.
//
// The tray starts aha2.exe, so it needs this before the server is up: it reads
// the setting without opening a socket. It is a read-only query and prints one
// line of plain text so the tray can consume it without parsing JSON.
func printEffectiveListen(args []string) error {
	flags := flag.NewFlagSet("aha2 listen", flag.ContinueOnError)
	dataDir := flags.String("data-dir", envOr("AHA2_DATA_DIR", ".data"), "AHA2 data directory")
	fallback := flags.String("default", "0.0.0.0:8766", "address to report when no override is stored")
	if err := flags.Parse(args); err != nil {
		return err
	}
	address := strings.TrimSpace(*fallback)
	if address == "" {
		address = "0.0.0.0:8766"
	}
	storePath := filepath.Join(*dataDir, "aha2.db")
	if _, err := os.Stat(storePath); err != nil {
		// No database yet: this is a first run, so the flag default stands.
		fmt.Println(address)
		return nil
	}
	database, err := store.Open(context.Background(), storePath)
	if err != nil {
		return fmt.Errorf("open store to read listen address: %w", err)
	}
	defer database.Close()
	settings, err := database.NetworkSettings(context.Background())
	if err != nil {
		return fmt.Errorf("read listen address: %w", err)
	}
	if stored := strings.TrimSpace(settings.ListenAddress); stored != "" {
		address = stored
	}
	fmt.Println(address)
	return nil
}

// runHardwareIOMaintenance applies retention, the disk cap and Task deletion
// to the terminal IO files: once shortly after startup, then hourly, and
// early (at most once a minute) when writes push the files over the cap.
func runHardwareIOMaintenance(ctx context.Context, database *store.Store, logger *slog.Logger) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	overCap := database.HardwareIOCapacitySignal()
	var lastRun time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-overCap:
			if wait := time.Minute - time.Since(lastRun); wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
		}
		lastRun = time.Now()
		result, err := database.RunHardwareIOMaintenance(ctx)
		if err != nil {
			logger.Warn("hardware io maintenance failed", "error", err)
		}
		if result.RetentionSegments+result.CapacitySegments+result.OrphanTasks > 0 || result.OverLimit {
			logger.Info("hardware io maintenance",
				"retention_segments", result.RetentionSegments, "retention_bytes", result.RetentionBytes,
				"capacity_segments", result.CapacitySegments, "capacity_bytes", result.CapacityBytes,
				"orphan_tasks", result.OrphanTasks, "total_bytes", result.TotalBytes, "over_limit", result.OverLimit)
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(time.Hour)
	}
}
