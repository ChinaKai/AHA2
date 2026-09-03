package prompt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestEngineRoutesTemplatesAndBuildsContextManifest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	templates, err := engine.Templates(ctx)
	if err != nil || len(templates) != 8 {
		t.Fatalf("templates=%d err=%v", len(templates), err)
	}
	if err := engine.UpdateTemplate(ctx, "role.main", "CUSTOM MAIN {{.AgentID}}", templates[0].UpdatedAt); err != nil {
		t.Fatal(err)
	}
	input := BuildInput{
		Project:   domain.Project{Name: "Project"},
		Workspace: domain.Workspace{Name: "Workspace", RootPath: "/repo", Transport: "native"},
		Task: domain.Task{
			ID: "task-1", Code: "task-001", Title: "Prompt", OriginalRequest: strings.Repeat("large", 500),
			CurrentGoal: "ship", TaskWorkspacePath: "/repo/.worktree", CollaborationMode: "auto", MaxAgents: 3,
		},
		Agent:    domain.TaskAgent{AgentID: "main", Role: "main"},
		Snapshot: domain.RuntimeConfigSnapshot{Backend: "codex"},
		Memory: domain.TaskMemory{
			Facts: []string{"fact one"}, Decisions: []string{"decision one"},
		},
		GlobalKnowledge: []domain.KnowledgeEntry{{Title: "Global", Body: strings.Repeat("knowledge", 200)}},
		UserMessage:     "fixed inbox",
	}
	preview, err := engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"CUSTOM MAIN main", "fixed inbox", ".aha2-context", "manifest.json", "Turn Checkpoint Protocol"} {
		if !strings.Contains(preview.EffectivePrompt, expected) {
			t.Fatalf("effective prompt missing %q: %s", expected, preview.EffectivePrompt)
		}
	}
	if strings.Contains(preview.EffectivePrompt, strings.Repeat("knowledge", 20)) ||
		strings.Contains(preview.EffectivePrompt, strings.Repeat("large", 20)) {
		t.Fatal("large context was injected inline")
	}
	if strings.Contains(preview.EffectivePrompt, "Task Memory summary") ||
		strings.Contains(preview.EffectivePrompt, "decision one") ||
		strings.Contains(preview.EffectivePrompt, "fact one") {
		t.Fatal("task memory was injected inline")
	}
	var knowledgeFound bool
	var manifest ContextResource
	for _, resource := range preview.ContextManifest {
		if resource.ID == "knowledge-global" && strings.Contains(resource.Content, "Global") {
			knowledgeFound = true
		}
		if resource.ID == "manifest" {
			manifest = resource
		}
	}
	if !knowledgeFound {
		t.Fatal("global knowledge resource missing")
	}
	var memoryFound bool
	for _, resource := range preview.ContextManifest {
		if resource.ID == "task-memory" && strings.Contains(resource.Content, "decision one") &&
			strings.Contains(resource.Content, "fact one") {
			memoryFound = true
		}
	}
	if !memoryFound {
		t.Fatal("task memory resource missing")
	}
	if strings.Contains(manifest.Content, strings.Repeat("knowledge", 20)) {
		t.Fatal("manifest duplicated resource content")
	}
	if !strings.Contains(manifest.Content, `"id"`) || strings.Contains(manifest.Content, `"ID"`) {
		t.Fatalf("manifest fields are not normalized JSON: %s", manifest.Content)
	}
	if err := engine.ResetTemplate(ctx, "role.main"); err != nil {
		t.Fatal(err)
	}
}
