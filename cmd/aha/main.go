package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
	"github.com/ChinaKai/AHA2/internal/codexaccount"
	"github.com/ChinaKai/AHA2/internal/execution"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"github.com/ChinaKai/AHA2/internal/httpapi"
	"github.com/ChinaKai/AHA2/internal/managedprocess"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
	"github.com/ChinaKai/AHA2/internal/webassets"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

const version = "0.2.0"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("aha2", version)
		return
	}
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	if len(os.Args) > 1 && os.Args[1] != "serve" && os.Args[1] != "version" && os.Args[1][0] != '-' {
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
	if command == "serve" {
		if err := serve(args); err != nil {
			slog.Error("AHA2 stopped", "error", err)
			os.Exit(1)
		}
	}
}

func serve(args []string) error {
	flags := flag.NewFlagSet("aha2 serve", flag.ContinueOnError)
	listen := flags.String("listen", envOr("AHA2_LISTEN", "0.0.0.0:8766"), "HTTP listen address")
	dataDir := flags.String("data-dir", envOr("AHA2_DATA_DIR", ".data"), "AHA2 data directory")
	setupToken := flags.String("setup-token", os.Getenv("AHA2_SETUP_TOKEN"), "one-time owner setup token")
	setupTokenFile := flags.String("setup-token-file", os.Getenv("AHA2_SETUP_TOKEN_FILE"), "file containing the one-time owner setup token")
	secureCookie := flags.Bool("secure-cookie", envBool("AHA2_SECURE_COOKIE"), "mark session cookie Secure")
	allowCrossOrigin := flags.Bool("allow-cross-origin", envBool("AHA2_ALLOW_CROSS_ORIGIN"), "disable Origin host validation for reverse proxies and embedded WebViews")
	agentAPIURL := flags.String("agent-api-url", os.Getenv("AHA2_AGENT_API_URL"), "Agent-reachable AHA2 base URL (required for remote workspaces)")
	allowInsecureAgentAPI := flags.Bool("allow-insecure-agent-api", envBool("AHA2_ALLOW_INSECURE_AGENT_API"), "allow a non-loopback http:// Agent API URL for trusted development networks")
	logLevel := flags.String("log-level", envOr("AHA2_LOG_LEVEL", "info"), "debug, info, warn, error")
	codexBinary := flags.String("codex-bin", envOr("AHA2_CODEX_BIN", "codex"), "Codex executable")
	claudeBinary := flags.String("claude-bin", envOr("AHA2_CLAUDE_BIN", "claude"), "Claude Code executable")
	if err := flags.Parse(args); err != nil {
		return err
	}
	level := new(slog.LevelVar)
	switch *logLevel {
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
	absoluteDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absoluteDataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if *setupTokenFile == "" {
		*setupTokenFile = filepath.Join(absoluteDataDir, "setup-token")
	}
	if *setupToken == "" {
		if value, readErr := os.ReadFile(*setupTokenFile); readErr == nil {
			*setupToken = strings.TrimSpace(string(value))
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	database, err := store.Open(ctx, filepath.Join(absoluteDataDir, "aha2.db"))
	if err != nil {
		return err
	}
	defer database.Close()
	recovery, err := database.RecoverInterrupted(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	if recovery.Turns > 0 {
		logger.Warn("recovered interrupted runtime", "turns", recovery.Turns, "tasks", recovery.Tasks)
	}
	secretStore, err := secrets.Open(filepath.Join(absoluteDataDir, "secrets.json"))
	if err != nil {
		return err
	}
	go (syncer.Runner{Store: database, Secrets: secretStore, TokenRef: syncer.DefaultTokenRef}).Loop(ctx)
	registrationOpen, err := database.OwnerExists(ctx)
	if err != nil {
		return err
	}
	if !registrationOpen && *setupToken == "" {
		*setupToken, err = generatedSetupToken()
		if err != nil {
			return err
		}
		if err := os.WriteFile(*setupTokenFile, []byte(*setupToken+"\n"), 0o600); err != nil {
			return fmt.Errorf("write setup token file: %w", err)
		}
		if err := os.Chmod(*setupTokenFile, 0o600); err != nil {
			return fmt.Errorf("restrict setup token file: %w", err)
		}
		logger.Warn("owner registration is open", "setup_token_file", *setupTokenFile)
	}
	authService := auth.NewService(database, *setupToken, 14*24*time.Hour)
	codexAccounts := codexaccount.New(ctx, database, secretStore, absoluteDataDir, *codexBinary)
	executor := execution.Executor{
		Codex:  backend.Codex{Binary: *codexBinary},
		Claude: backend.Claude{Binary: *claudeBinary},
	}
	appService := app.NewService(database, secretStore, executor)
	appService.SetWorkspacePreparer(execution.WorkspacePreparer{})
	appService.SetCodexAccountManager(codexAccounts)
	agentCapabilities := agentapi.NewCapabilities()
	managedProcesses := managedprocess.NewManager()
	defer managedProcesses.Close()
	resolvedAgentAPIURL := resolveAgentAPIURL(*listen, *agentAPIURL)
	if err := validateAgentAPIURL(resolvedAgentAPIURL, *allowInsecureAgentAPI); err != nil {
		return err
	}
	if *allowInsecureAgentAPI && strings.HasPrefix(strings.ToLower(resolvedAgentAPIURL), "http://") {
		logger.Warn("insecure Agent API URL enabled", "host", mustAgentAPIHost(resolvedAgentAPIURL))
	}
	appService.SetAgentAPI(agentCapabilities, resolvedAgentAPIURL)
	hardwareManager := hardware.NewManager(database)
	defer hardwareManager.Close()
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
		Store: database, Auth: authService, App: appService, Web: webassets.FS(),
		Logger: logger, SecureCookie: *secureCookie, AllowCrossOrigin: *allowCrossOrigin,
		DetectWorkspace:   workspace.Detect,
		Secrets:           secretStore,
		Hardware:          hardwareManager,
		CodexAccounts:     codexAccounts,
		AgentCapabilities: agentCapabilities,
		ManagedProcesses:  managedProcesses,
	})
	if *allowCrossOrigin {
		logger.Warn("Origin host validation disabled")
	}
	server := &http.Server{
		Addr:              *listen,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	errors := make(chan error, 1)
	go func() {
		logger.Info("AHA2 listening", "address", *listen, "data_dir", absoluteDataDir)
		errors <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	case err := <-errors:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
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

func validateAgentAPIURL(rawURL string, allowInsecure bool) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return fmt.Errorf("agent-api-url must be an http:// or https:// base URL without credentials")
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("agent-api-url must not contain a path, query, or fragment")
	}
	if parsed.Scheme == "https" || allowInsecure || agentAPIHostIsLoopback(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("non-loopback agent-api-url requires https://; use --allow-insecure-agent-api only on a trusted development network")
}

func agentAPIHostIsLoopback(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	address := net.ParseIP(strings.TrimSpace(host))
	return address != nil && address.IsLoopback()
}

func mustAgentAPIHost(rawURL string) string {
	parsed, _ := url.Parse(rawURL)
	return parsed.Host
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
