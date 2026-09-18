package workspace

import (
	"context"
	"os"
	"path/filepath"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// ClaudeConfigDir returns the isolated Claude Code config directory used by one
// AHA backend session.
//
// Claude stores its transcript under <config>/projects/<cwd-slug>/<session_id>.jsonl.
// Pointing Env-provider runs at their own directory keeps an AHA session from
// sharing a transcript with the operator's own Claude Code, and gives every
// backend session a unique marker that identifies the process still holding it.
func ClaudeConfigDir(item domain.Workspace, workDir, sessionID string) string {
	if item.Transport == "native" {
		return filepath.Join(workDir, ".aha2-context", "runtime", "claude-home", sessionID)
	}
	return JoinRemotePath(item, workDir, ".aha2-context", "runtime", "claude-home", sessionID)
}

// EnsureClaudeConfigDir creates the isolated Claude config directory if needed.
// Claude recreates missing subdirectories itself, but not this root.
func EnsureClaudeConfigDir(ctx context.Context, item domain.Workspace, runner Runner, directory string) error {
	if item.Transport == "native" {
		return os.MkdirAll(directory, 0o700)
	}
	return EnsureRemoteDirectory(ctx, runner, directory)
}

// SessionHome describes where a backend keeps the state of one AHA backend
// session and how to point that backend at it.
type SessionHome struct {
	// Dir is the backend's config/state directory for this session.
	Dir string
	// EnvName is the environment variable that directs the backend at Dir. It is
	// empty when the backend must keep using its default location, in which case
	// the session can only be identified from the running command line.
	EnvName string
}

// SessionHomeInput carries what the per-backend layouts need.
type SessionHomeInput struct {
	Backend        string
	Workspace      domain.Workspace
	WorkDir        string
	SessionID      string
	CodexAccountID string
	EnvGroupID     string
}

// SessionHomeFor resolves the session home for a backend.
//
// It returns false when the backend has no per-session home AHA can address.
func SessionHomeFor(input SessionHomeInput) (SessionHome, bool) {
	switch input.Backend {
	case "codex":
		return SessionHome{
			Dir:     CodexSessionHomeDir(input.Workspace, input.WorkDir, input.SessionID, input.CodexAccountID),
			EnvName: "CODEX_HOME",
		}, true
	case "claude":
		home := SessionHome{Dir: ClaudeConfigDir(input.Workspace, input.WorkDir, input.SessionID)}
		if input.EnvGroupID != domain.ClaudeNativeEnvGroupID {
			// Runs with their own credentials can use a private config dir.
			home.EnvName = "CLAUDE_CONFIG_DIR"
		}
		// The native source authenticates with the operator's logged-in Claude
		// Code account, whose credentials live in the default config dir, so it
		// keeps that location and is identified from its command line instead.
		return home, true
	}
	return SessionHome{}, false
}

// SupportsSessionHome reports whether AHA can address a per-session home for the
// backend, which is what makes process-level writer cleanup possible.
func SupportsSessionHome(backend string) bool {
	_, ok := SessionHomeFor(SessionHomeInput{Backend: backend})
	return ok
}
