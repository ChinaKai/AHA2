package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// claudeAuthRunner serves the two commands probeClaudeBackend issues, so a test
// can pin the exact shape `claude auth status --json` leaves behind.
type claudeAuthRunner struct {
	auth Result
	err  error
}

func (runner claudeAuthRunner) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	if len(command.Args) > 0 && command.Args[0] == "auth" {
		return runner.auth, runner.err
	}
	if len(command.Args) > 0 && command.Args[0] == "--version" {
		return Result{Stdout: "2.1.274 (Claude Code)"}, nil
	}
	return Result{}, nil
}

// A logged-out account is a status, not a failed probe: `claude auth status
// --json` prints the complete JSON and exits 1. Reading the reply, rather than
// the exit code, is what lets the Web UI say "not logged in" instead of the
// unactionable "detection not finished".
func TestClaudeProbeReadsLoggedOutReplyDespiteNonZeroExit(t *testing.T) {
	item := domain.Workspace{ID: "ws", RootPath: "/tmp/ws"}
	probe, _ := probeClaudeBackend(context.Background(), claudeAuthRunner{auth: Result{
		Stdout:   `{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}` + "\n",
		ExitCode: 1,
	}}, item)

	if got := probe["auth_status"]; got != "not_logged_in" {
		t.Fatalf("auth_status = %v, want not_logged_in", got)
	}
	if _, present := probe["auth_error"]; present {
		t.Fatalf("a readable status must not report auth_error: %v", probe["auth_error"])
	}
}

func TestClaudeProbeReadsLoggedInReply(t *testing.T) {
	item := domain.Workspace{ID: "ws", RootPath: "/tmp/ws"}
	probe, _ := probeClaudeBackend(context.Background(), claudeAuthRunner{auth: Result{
		Stdout: `{"loggedIn":true,"authMethod":"oauth_token","apiProvider":"firstParty"}` + "\n",
	}}, item)

	if got := probe["auth_status"]; got != "logged_in" {
		t.Fatalf("auth_status = %v, want logged_in", got)
	}
	if got := probe["auth_method"]; got != "oauth_token" {
		t.Fatalf("auth_method = %v, want oauth_token", got)
	}
}

// An empty reply is a genuine failure, and the operator needs the reason:
// "exit code 0" alone reads as a contradiction rather than a diagnosis.
func TestClaudeProbeExplainsEmptyReply(t *testing.T) {
	item := domain.Workspace{ID: "ws", RootPath: "/tmp/ws"}
	probe, _ := probeClaudeBackend(context.Background(), claudeAuthRunner{}, item)

	if got := probe["auth_status"]; got != "unknown" {
		t.Fatalf("auth_status = %v, want unknown", got)
	}
	detail, _ := probe["auth_error"].(string)
	if !strings.Contains(detail, "退出码 0") {
		t.Fatalf("auth_error = %q, want it to explain the empty exit-0 reply", detail)
	}
}

func TestClaudeProbeKeepsStderrWhenAuthCommandFails(t *testing.T) {
	item := domain.Workspace{ID: "ws", RootPath: "/tmp/ws"}
	probe, _ := probeClaudeBackend(context.Background(), claudeAuthRunner{
		auth: Result{ExitCode: 1, Stderr: "Error: credentials file is not readable"},
		err:  errors.New("exit status 1"),
	}, item)

	if got := probe["auth_status"]; got != "unknown" {
		t.Fatalf("auth_status = %v, want unknown", got)
	}
	detail, _ := probe["auth_error"].(string)
	if !strings.Contains(detail, "credentials file is not readable") {
		t.Fatalf("auth_error = %q, want the stderr text preserved", detail)
	}
}
