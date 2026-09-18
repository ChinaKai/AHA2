package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// CodexAuthProfileDir returns the authenticated Codex profile directory used by
// official-account runs for one backend session.
func CodexAuthProfileDir(item domain.Workspace, workDir, sessionID string) string {
	return codexHomeSubdir(item, workDir, "codex-auth", sessionID)
}

// codexHomeSubdir joins a per-session Codex home directory for any workspace kind.
func codexHomeSubdir(item domain.Workspace, workDir, kind, sessionID string) string {
	if item.Transport == "native" {
		return filepath.Join(workDir, ".aha2-context", "runtime", kind, sessionID)
	}
	return JoinRemotePath(item, workDir, ".aha2-context", "runtime", kind, sessionID)
}

// CodexEnvHomeDir returns the isolated Codex home used by Env-provider runs.
//
// Env providers authenticate with the group's own credentials, so they must not
// read the operator's ~/.codex. Sharing that directory also leaks the user's
// global config (including any mcp_servers entry) into AHA turns and makes every
// AHA session a second writer of the same rollout files. The directory is keyed
// by AHA backend session so a rotated session always starts from a clean home.
func CodexEnvHomeDir(item domain.Workspace, workDir, sessionID string) string {
	return codexHomeSubdir(item, workDir, "codex-env", sessionID)
}

// CodexSessionHomeDir returns the Codex home a backend session used.
//
// Official-account runs get an authenticated profile directory, while
// Env-provider runs get the isolated directory. Knowing which layout applies
// lets AHA locate the process still holding a session without storing an extra
// column.
func CodexSessionHomeDir(item domain.Workspace, workDir, sessionID, codexAccountID string) string {
	if strings.TrimSpace(codexAccountID) != "" {
		return CodexAuthProfileDir(item, workDir, sessionID)
	}
	return CodexEnvHomeDir(item, workDir, sessionID)
}

// EnsureCodexEnvHome creates the isolated Codex home if it does not exist.
// Codex refuses to start when CODEX_HOME is missing, so this must run before
// every Env-provider invocation.
func EnsureCodexEnvHome(ctx context.Context, item domain.Workspace, runner Runner, directory string) error {
	if item.Transport == "native" {
		return os.MkdirAll(directory, 0o700)
	}
	return EnsureRemoteDirectory(ctx, runner, directory)
}
