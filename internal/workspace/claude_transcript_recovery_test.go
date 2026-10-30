package workspace

import (
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
