package workspace

import (
	"context"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// shellStubRunner answers each command the way the machine under test would: the
// login-shell read returns whatever the profile exports, and everything else is
// delegated to the supplied handler.
type shellStubRunner struct {
	profileStdout string
	profileStderr string
	profileExit   int
	profileErr    error
	seen          []Command
	other         func(Command) Result
}

func (runner *shellStubRunner) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	runner.seen = append(runner.seen, command)
	if len(command.Args) > 0 && command.Args[0] == "-lc" {
		return Result{Stdout: runner.profileStdout, Stderr: runner.profileStderr, ExitCode: runner.profileExit}, runner.profileErr
	}
	if runner.other != nil {
		return runner.other(command), nil
	}
	return Result{}, nil
}

func (runner *shellStubRunner) lastCommand() Command {
	if len(runner.seen) == 0 {
		return Command{}
	}
	return runner.seen[len(runner.seen)-1]
}

// A login that lives only in a shell profile is invisible to AHA's
// non-interactive channel. Borrowing the value is what lets the probe report the
// login the operator actually has.
func TestClaudeCredentialEnvironmentBorrowsProfileLogin(t *testing.T) {
	const token = "sk-ant-oat01-EXAMPLE-NOT-REAL"
	runner := &shellStubRunner{profileStdout: token}

	credential := ClaudeCredentialEnvironment(context.Background(), runner, domain.Workspace{ID: "ws", RootPath: "/tmp/ws"})

	if got := credential[ClaudeCodeOAuthTokenEnv]; got != token {
		t.Fatalf("credential = %q, want the profile's token", got)
	}
	// The read must go through a login shell, since that is the only place a
	// profile login is visible.
	if command := runner.lastCommand(); len(command.Args) == 0 || command.Args[0] != "-lc" {
		t.Fatalf("credential read used %v, want a login shell", runner.lastCommand().Args)
	}
}

// A profile that greets the user must not corrupt the value: the banner precedes
// the token on stdout.
func TestClaudeCredentialEnvironmentIgnoresProfileBanner(t *testing.T) {
	runner := &shellStubRunner{profileStdout: "Welcome to the build server\nsk-ant-oat01-EXAMPLE-NOT-REAL\n"}

	credential := ClaudeCredentialEnvironment(context.Background(), runner, domain.Workspace{ID: "ws"})

	if got := credential[ClaudeCodeOAuthTokenEnv]; got != "sk-ant-oat01-EXAMPLE-NOT-REAL" {
		t.Fatalf("credential = %q, want the token after the banner", got)
	}
}

// Machines that log in through Claude Code's credentials file export nothing.
// Reading must then yield no credential rather than an empty or bogus one, so
// detection proceeds exactly as it did before this existed.
func TestClaudeCredentialEnvironmentWithoutProfileLogin(t *testing.T) {
	for name, stdout := range map[string]string{
		"nothing exported": "",
		"only whitespace":  "  \n ",
		"a shell error":    "sh: 1: cannot execute profile",
	} {
		t.Run(name, func(t *testing.T) {
			runner := &shellStubRunner{profileStdout: stdout}
			if credential := ClaudeCredentialEnvironment(context.Background(), runner, domain.Workspace{ID: "ws"}); credential != nil {
				t.Fatalf("credential = %v, want none", credential)
			}
		})
	}
}

// A failed profile read must not be mistaken for an empty one, and must not
// break the rest of detection.
func TestClaudeCredentialEnvironmentSurvivesFailedRead(t *testing.T) {
	runner := &shellStubRunner{profileExit: 1, profileStderr: "sh: profile not found"}

	if credential := ClaudeCredentialEnvironment(context.Background(), runner, domain.Workspace{ID: "ws"}); credential != nil {
		t.Fatalf("credential = %v, want none after a failed read", credential)
	}
}

// The borrowed credential has to reach the probe, or the workspace keeps
// reporting a login the operator does not have.
func TestClaudeProbeUsesBorrowedCredential(t *testing.T) {
	var authEnv map[string]string
	runner := &shellStubRunner{
		profileStdout: "sk-ant-oat01-EXAMPLE-NOT-REAL",
		other: func(command Command) Result {
			switch {
			case len(command.Args) > 0 && command.Args[0] == "--version":
				return Result{Stdout: "2.1.274 (Claude Code)"}
			case len(command.Args) > 0 && command.Args[0] == "auth":
				authEnv = command.Env
				return Result{Stdout: `{"loggedIn":true,"authMethod":"oauth_token","apiProvider":"firstParty"}`}
			}
			return Result{ExitCode: 0}
		},
	}

	probe, _ := probeClaudeBackend(context.Background(), runner, domain.Workspace{ID: "ws", RootPath: "/tmp/ws"})

	if got := probe["auth_status"]; got != "logged_in" {
		t.Fatalf("auth_status = %v, want logged_in", got)
	}
	if got := authEnv[ClaudeCodeOAuthTokenEnv]; got != "sk-ant-oat01-EXAMPLE-NOT-REAL" {
		t.Fatalf("probe credential = %q, want the borrowed token", got)
	}
	// The native source's blanking must survive the merge: lending a credential
	// must not quietly re-enable an ANTHROPIC_* variable AHA deliberately clears.
	if value, present := authEnv["ANTHROPIC_API_KEY"]; !present || value != "" {
		t.Fatalf("ANTHROPIC_API_KEY = %q (present=%v), want it still blanked", value, present)
	}
}

// The credential is a secret. It must not appear in the probe result, which is
// persisted as a capability and shown to the operator.
func TestClaudeProbeNeverPublishesCredential(t *testing.T) {
	const token = "sk-ant-oat01-EXAMPLE-NOT-REAL"
	runner := &shellStubRunner{
		profileStdout: token,
		other: func(command Command) Result {
			switch {
			case len(command.Args) > 0 && command.Args[0] == "--version":
				return Result{Stdout: "2.1.274 (Claude Code)"}
			case len(command.Args) > 0 && command.Args[0] == "auth":
				// A rejected token is the realistic leak: the backend quotes it back.
				return Result{ExitCode: 1, Stderr: "invalid token: " + token}
			}
			return Result{ExitCode: 0}
		},
	}

	probe, _ := probeClaudeBackend(context.Background(), runner, domain.Workspace{ID: "ws", RootPath: "/tmp/ws"})

	for key, value := range probe {
		if text, ok := value.(string); ok && strings.Contains(text, token) {
			t.Fatalf("probe[%q] leaked the credential: %q", key, text)
		}
	}
	if detail, _ := probe["auth_error"].(string); !strings.Contains(detail, "[已隐藏]") {
		t.Fatalf("auth_error = %q, want the credential masked", detail)
	}
}

func TestSafeDisplayTextMasksCredentials(t *testing.T) {
	item := domain.Workspace{}
	for _, token := range []string{
		"sk-ant-oat01-EXAMPLE-NOT-REAL",
		"sk-ant-api03-EXAMPLE-NOT-REAL",
		"sk-ant-admin01-EXAMPLE-NOT-REAL",
	} {
		masked := safeDisplayText(item, "auth failed with "+token+" rejected")
		if strings.Contains(masked, token) {
			t.Fatalf("safeDisplayText leaked %q: %q", token, masked)
		}
		if !strings.Contains(masked, "[已隐藏]") {
			t.Fatalf("safeDisplayText did not mark the redaction: %q", masked)
		}
	}
}
