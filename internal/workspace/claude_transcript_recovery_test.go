package workspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLastClaudeAssistantTextReturnsFinalReply(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	transcript := strings.Join([]string{
		`{"type":"user","timestamp":"` + now.Format(time.RFC3339Nano) + `","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","timestamp":"` + now.Format(time.RFC3339Nano) + `","message":{"model":"m","content":[{"type":"thinking","thinking":"hmm"}]}}`,
		`{"type":"assistant","timestamp":"` + now.Format(time.RFC3339Nano) + `","message":{"model":"m","content":[{"type":"text","text":"（趴在圈里，慢慢睡着了）"}]}}`,
		`{"type":"last-prompt","lastPrompt":"..."}`,
	}, "\n")
	if got := lastClaudeAssistantText(transcript, now.Add(-time.Minute)); got != "（趴在圈里，慢慢睡着了）" {
		t.Fatalf("recovered text=%q", got)
	}
}

func TestClaudeTranscriptWithoutSessionIDFindsNewestError(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	configDir := t.TempDir()
	projectDir := filepath.Join(configDir, "projects", ClaudeProjectSlug(workDir))
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	transcript := `{"type":"assistant","timestamp":"` + now.Format(time.RFC3339Nano) + `","isApiErrorMessage":true,"apiErrorStatus":403,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 403 You've reached your concurrent request limit"}]}}`
	if err := os.WriteFile(filepath.Join(projectDir, "orphan-session.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ClaudeTranscriptLastErrorText(context.Background(), LocalRunner{}, configDir, workDir, "", now.Add(-time.Minute))
	if !strings.Contains(got, "concurrent request limit") {
		t.Fatalf("error text=%q", got)
	}
}

func TestLastClaudeErrorTextReturnsSyntheticProviderError(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	transcript := strings.Join([]string{
		`{"type":"assistant","timestamp":"` + now.Format(time.RFC3339Nano) + `","isApiErrorMessage":true,"apiErrorStatus":403,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 403 You've reached your concurrent request limit"}]}}`,
		`{"type":"result","timestamp":"` + now.Format(time.RFC3339Nano) + `","subtype":"error_during_execution","error":"unknown"}`,
	}, "\n")
	if got := lastClaudeErrorText(transcript, now.Add(-time.Minute)); !strings.Contains(got, "concurrent request limit") {
		t.Fatalf("error text=%q", got)
	}
	old := `{"type":"assistant","timestamp":"` + now.Add(-time.Hour).Format(time.RFC3339Nano) + `","isApiErrorMessage":true,"message":{"model":"<synthetic>","content":[{"type":"text","text":"old error"}]}}`
	if got := lastClaudeErrorText(old, now.Add(-time.Minute)); got != "" {
		t.Fatalf("old error was reused: %q", got)
	}
}

func TestLastClaudeAssistantTextRejectsIncompleteTurn(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	cases := map[string]string{
		"tool result after text": strings.Join([]string{
			`{"type":"assistant","timestamp":"` + now.Format(time.RFC3339Nano) + `","message":{"model":"m","content":[{"type":"text","text":"commentary"}]}}`,
			`{"type":"user","timestamp":"` + now.Format(time.RFC3339Nano) + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"ok"}]}}`,
		}, "\n"),
		"synthetic api error": strings.Join([]string{
			`{"type":"assistant","timestamp":"` + now.Format(time.RFC3339Nano) + `","isApiErrorMessage":true,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 400"}]}}`,
		}, "\n"),
		"defaulted text from an old turn": strings.Join([]string{
			`{"type":"assistant","timestamp":"` + now.Add(-time.Hour).Format(time.RFC3339Nano) + `","message":{"model":"m","content":[{"type":"text","text":"old"}]}}`,
			`{"type":"user","timestamp":"` + now.Format(time.RFC3339Nano) + `","message":{"role":"user","content":"new prompt"}}`,
		}, "\n"),
		"text with tool use": strings.Join([]string{
			`{"type":"assistant","timestamp":"` + now.Format(time.RFC3339Nano) + `","message":{"model":"m","content":[{"type":"text","text":"let me check"},{"type":"tool_use","id":"t","name":"Bash","input":{}}]}}`,
		}, "\n"),
	}
	for name, transcript := range cases {
		t.Run(name, func(t *testing.T) {
			if got := lastClaudeAssistantText(transcript, now.Add(-time.Minute)); got != "" {
				t.Fatalf("incomplete turn recovered %q", got)
			}
		})
	}
}

type capturingWindowsRunner struct {
	command Command
}

func (runner *capturingWindowsRunner) DetectedPlatform() string { return "windows/amd64" }

func (runner *capturingWindowsRunner) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	runner.command = command
	return Result{Stdout: "present", ExitCode: 0}, nil
}

func TestLocalRunnerReportsHostPlatform(t *testing.T) {
	t.Parallel()
	want := runtime.GOOS + "/" + runtime.GOARCH
	if got := (LocalRunner{}).DetectedPlatform(); got != want {
		t.Fatalf("platform=%q, want %q", got, want)
	}
}

func TestClaudeTranscriptExistsEmbedsWindowsPathInCommand(t *testing.T) {
	t.Parallel()
	runner := &capturingWindowsRunner{}
	if !ClaudeTranscriptExists(context.Background(), runner, `C:\config`, `C:\work`, "sess") {
		t.Fatal("transcript was not reported present")
	}
	if runner.command.Executable != "powershell.exe" {
		t.Fatalf("executable=%q", runner.command.Executable)
	}
	script := strings.Join(runner.command.Args, " ")
	if !strings.Contains(script, "sess.jsonl") || !strings.Contains(script, "Test-Path") {
		t.Fatalf("windows script did not embed the target: %q", script)
	}
	for _, arg := range runner.command.Args {
		if strings.HasSuffix(arg, "sess.jsonl") {
			t.Fatalf("target was passed as a separate argv, which does not bind to param(): %#v", runner.command.Args)
		}
	}
}

func TestPowerShellQuoteEscapesSingleQuotes(t *testing.T) {
	t.Parallel()
	if got, want := powershellQuote(`C:\owner's dir\file.jsonl`), `'C:\owner''s dir\file.jsonl'`; got != want {
		t.Fatalf("quote=%q, want %q", got, want)
	}
}
