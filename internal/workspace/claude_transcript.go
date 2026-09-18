package workspace

import (
	"context"
	"os"
	"path"
	"strings"
	"time"
)

// ClaudeProjectSlug converts a working directory into the directory name Claude
// uses for that project's transcripts: every character that is not a letter,
// digit or dash becomes a dash (verified against Claude Code 2.1.207, where
// both "/tmp/a_b" and "/tmp/a.b" collapse to "-tmp-a-b").
func ClaudeProjectSlug(workDir string) string {
	value := strings.TrimSpace(workDir)
	var builder strings.Builder
	builder.Grow(len(value))
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' {
			builder.WriteRune(character)
			continue
		}
		builder.WriteByte('-')
	}
	return builder.String()
}

// DefaultClaudeConfigDir returns the config directory Claude uses on the machine
// this process runs on when CLAUDE_CONFIG_DIR is unset, i.e. ~/.claude there.
//
// Use ClaudeDefaultConfigDirOn for a workspace that is not this host: a WSL or SSH
// workspace has its own home directory, and the control plane's own home does not
// exist on it.
func DefaultClaudeConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return path.Join(strings.ReplaceAll(home, `\`, "/"), ".claude")
}

// ClaudeDefaultConfigDirOn returns the config directory Claude uses on the
// workspace host when CLAUDE_CONFIG_DIR is unset.
//
// The host is asked rather than assumed, because the transcript of a backend
// session lives in that host's home directory, not in the control plane's. A WSL
// or SSH workspace has its own home, and guessing the control plane's own home
// would look in a directory that does not exist there.
func ClaudeDefaultConfigDirOn(ctx context.Context, runner Runner) string {
	if IsWindowsRunner(runner) {
		// A Windows host has no POSIX shell; its home is the user profile.
		result, err := runner.Run(ctx, Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				`[Console]::Out.Write($env:USERPROFILE)`},
			Timeout: 20 * time.Second,
		}, nil)
		if err != nil || result.ExitCode != 0 {
			return ""
		}
		profile := strings.TrimSpace(strings.Trim(result.Stdout, "\x00"))
		if profile == "" {
			return ""
		}
		return windowsRemotePathJoin(profile, ".claude")
	}
	result, err := runner.Run(ctx, Command{
		Executable: "sh",
		Args:       []string{"-c", `printf '%s' "${HOME:-}/.claude"`},
		Timeout:    15 * time.Second,
	}, nil)
	if err == nil && result.ExitCode == 0 {
		if directory := strings.TrimSpace(result.Stdout); directory != "" && directory != "/.claude" {
			return directory
		}
	}
	return DefaultClaudeConfigDir()
}

// ClaudeTranscriptExists reports whether the transcript for a session already
// exists under the given config directory.
//
// A session can only be resumed from the config directory that holds its
// transcript. AHA gives new sessions their own isolated directory, but a session
// started before that isolation existed lives in the backend's own default
// directory, so resuming it there is the only way to continue it.
func ClaudeTranscriptExists(ctx context.Context, runner Runner, configDir, workDir, sessionID string) bool {
	if strings.TrimSpace(configDir) == "" || strings.TrimSpace(sessionID) == "" {
		return false
	}
	target := path.Join(configDir, "projects", ClaudeProjectSlug(workDir), sessionID+".jsonl")
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				`param([string]$Target) if (Test-Path -LiteralPath $Target -PathType Leaf) { 'present' }`, target},
			Timeout: 20 * time.Second,
		}
	} else {
		command = Command{Executable: "test", Args: []string{"-f", target}, Timeout: 15 * time.Second}
	}
	result, err := runner.Run(ctx, command, nil)
	if err != nil || result.ExitCode != 0 {
		return false
	}
	if IsWindowsRunner(runner) {
		return strings.TrimSpace(result.Stdout) == "present"
	}
	return true
}
