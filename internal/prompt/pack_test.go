package prompt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/store"
)

func TestProtocolTemplatesAreFocusedAndFileBacked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	knowledge := templateContent(t, engine, ctx, "protocol.knowledge")
	agentAPI := templateContent(t, engine, ctx, "protocol.agent-api")
	for _, expected := range []string{
		"progressive, index-first", "perform one Knowledge closeout",
		"pending proposal is not current guidance",
	} {
		if !strings.Contains(knowledge, expected) {
			t.Fatalf("Knowledge Protocol does not contain %q", expected)
		}
	}
	for _, expected := range []string{
		"Task-scoped Agent Control API", "Never print, persist, or expose the API token",
		"Only Main may change durable Task state", "final response natural-language only",
	} {
		if !strings.Contains(agentAPI, expected) {
			t.Fatalf("Agent Control API Protocol does not contain %q", expected)
		}
	}
	if len([]rune(knowledge)) > 1800 || len([]rune(agentAPI)) > 1400 {
		t.Fatalf("protocol templates regressed to verbose copies: knowledge=%d agent_api=%d", len([]rune(knowledge)), len([]rune(agentAPI)))
	}
}

func TestBuiltinPromptContentIsNotEmbeddedInGo(t *testing.T) {
	t.Parallel()
	for _, item := range builtinTemplates {
		if strings.TrimSpace(item.Content) != "" {
			t.Fatalf("template %s embeds prompt content in Go", item.ID)
		}
	}
}
