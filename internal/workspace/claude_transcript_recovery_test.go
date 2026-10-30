package workspace

import (
	"context"
	"os"
	"path/filepath"
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
