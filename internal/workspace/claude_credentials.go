package workspace

import (
	"context"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// ClaudeCodeOAuthTokenEnv is the variable Claude Code reads to authenticate as
// the operator's own account.
//
// A machine can hold its Claude login in two places: Claude Code's credentials
// file, or this variable exported from a shell profile. The second kind works in
// an interactive shell but not through AHA's channel, which runs every command
// with a non-interactive shell that never loads a profile. The workspace then
// reports "not logged in" while the operator's own `claude` works fine, and the
// discrepancy looks like a detection bug when it is an environment one.
const ClaudeCodeOAuthTokenEnv = "CLAUDE_CODE_OAUTH_TOKEN"

// claudeCredentialReadTimeout bounds the profile read. Shell startup is fast; a
// profile that blocks should not hold up detection.
const claudeCredentialReadTimeout = 10 * time.Second

// ClaudeCredentialEnvironment returns the operator's Claude credential as an
// environment override, or nil when there is none to lend.
//
// It borrows the value from a login shell and nothing else: callers apply the
// result to the same non-interactive command they already run, so a profile's
// other exports never reach the task environment. That distinction is the whole
// point -- inheriting the profile wholesale would make every task depend on the
// operator's personal shell setup, so a stray line in a profile could change
// what tasks do.
//
// Reading is best-effort. A machine that logs in through Claude Code's
// credentials file has no such variable, yields nothing here, and detection
// proceeds exactly as it did before.
//
// The returned value is a credential: callers must not log it, persist it, or
// copy it into a probe result.
func ClaudeCredentialEnvironment(ctx context.Context, runner Runner, item domain.Workspace) map[string]string {
	result, err := runDetectionCommand(ctx, item, runner, Command{
		Executable: "sh",
		// -l loads the login profile, which is where this kind of login lives.
		// No Dir: the value does not depend on the workspace path, and a root
		// path that cannot be entered must not hide an otherwise valid login.
		Args:    []string{"-lc", `printf %s "$` + ClaudeCodeOAuthTokenEnv + `"`},
		Timeout: claudeCredentialReadTimeout,
	})
	if err != nil || result.ExitCode != 0 {
		return nil
	}
	// A profile that greets the user prints its banner before our value, so read
	// the last line rather than the whole stream.
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	token := strings.TrimSpace(lines[len(lines)-1])
	// Anything that is not a single bounded word is not a token worth guessing
	// at. A token is one opaque string; whitespace means we captured something
	// else -- a banner, a shell error, an empty profile.
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, " \t\r\n") {
		return nil
	}
	return map[string]string{ClaudeCodeOAuthTokenEnv: token}
}

// claudeProbeEnvironment builds the environment for a Claude probe: the native
// source's blanked ANTHROPIC_* variables, plus the operator's borrowed
// credential when there is one.
func claudeProbeEnvironment(credential map[string]string) map[string]string {
	environment := claudeNativeEnvironment()
	for key, value := range credential {
		environment[key] = value
	}
	return environment
}

// isClaudeCredentialKey reports whether a variable name carries a Claude
// credential, which is what masking decisions key off.
func isClaudeCredentialKey(name string) bool {
	return name == ClaudeCodeOAuthTokenEnv
}
