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
			Extra: map[string]any{"knowledge_refs": []map[string]any{{"id": "global-1", "revision": 1}}},
		},
		GlobalKnowledge: []domain.KnowledgeEntry{{ID: "global-1", Scope: "global", Type: "practice", Title: "Global", Body: strings.Repeat("knowledge", 200), Revision: 1}},
		Skills: []domain.Skill{{ID: "skill-1", PackageSlug: "review", Scope: "global", Name: "Review", Description: "Review code", Instructions: strings.Repeat("review", 200), Version: 1, Enabled: true, PackageFiles: []domain.SkillFile{
			{Path: "SKILL.md", Content: "---\nname: Review\ndescription: Review code\n---\n\n" + strings.Repeat("review", 200)},
			{Path: "scripts/check.sh", Content: "echo reviewed"},
		}}},
		KnowledgeEnabled: true,
		Hardware: []domain.HardwareGroup{{
			ID: "board", Description: "Main board", Mode: domain.HardwareModeBoth,
			Serial:   domain.HardwareSerialConfig{Device: "COM3", Baudrate: 115200},
			Network:  domain.HardwareNetworkConfig{Host: "192.0.2.10", Port: 23, Protocol: domain.HardwareProtocolTelnet},
			Username: "root", CredentialRef: "hardware/task-1/board/credential",
			PasswordConfigured: true, Access: domain.HardwareAccessReadOnly,
		}},
		UserMessage: "fixed inbox",
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
	var knowledgeIndexFound, knowledgeDetailFound, skillDetailFound, skillScriptFound, hardwareFound bool
	var manifest ContextResource
	for _, resource := range preview.ContextManifest {
		if resource.ID == "knowledge-index" && resource.EntryPoint && strings.Contains(resource.Content, "global-1") {
			knowledgeIndexFound = true
		}
		if resource.ID == "knowledge-global-1" && !resource.EntryPoint && strings.Contains(resource.Content, "Global") {
			knowledgeDetailFound = true
		}
		if resource.ID == "skill-skill-1" && resource.EntryPoint && strings.Contains(resource.Path, "skills/review/SKILL.md") {
			skillDetailFound = true
		}
		if strings.Contains(resource.Path, "skills/review/scripts/check.sh") && !resource.EntryPoint && strings.Contains(resource.Content, "reviewed") {
			skillScriptFound = true
		}
		if resource.ID == "manifest" {
			manifest = resource
		}
		if resource.ID == "hardware" && strings.Contains(resource.Content, "COM3") &&
			strings.Contains(resource.Content, "password configured: true") &&
			!strings.Contains(resource.Content, "credential") {
			hardwareFound = true
		}
	}
	if !knowledgeIndexFound || !knowledgeDetailFound {
		t.Fatal("knowledge entrypoint or detail resource missing")
	}
	if !skillDetailFound || !skillScriptFound {
		t.Fatal("skill package entrypoint or script resource missing")
	}
	if !hardwareFound {
		t.Fatal("sanitized hardware resource missing")
	}
	var memoryFound bool
	for _, resource := range preview.ContextManifest {
		if resource.ID == "task-memory" && strings.Contains(resource.Content, "decision one") &&
			strings.Contains(resource.Content, "fact one") && strings.Contains(resource.Content, "global-1") {
			memoryFound = true
		}
	}
	if !memoryFound {
		t.Fatal("task memory resource missing")
	}
	if strings.Contains(manifest.Content, strings.Repeat("knowledge", 20)) {
		t.Fatal("manifest duplicated resource content")
	}
	if strings.Contains(preview.EffectivePrompt, strings.Repeat("review", 20)) || strings.Contains(preview.EffectivePrompt, "global-1.md") {
		t.Fatal("knowledge or skill detail leaked into the effective prompt")
	}
	if !strings.Contains(manifest.Content, `"id"`) || strings.Contains(manifest.Content, `"ID"`) {
		t.Fatalf("manifest fields are not normalized JSON: %s", manifest.Content)
	}
	if err := engine.ResetTemplate(ctx, "role.main"); err != nil {
		t.Fatal(err)
	}
	input.KnowledgeEnabled = false
	preview, err = engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	var disabledKnowledge, selectedSkills bool
	for _, resource := range preview.ContextManifest {
		disabledKnowledge = disabledKnowledge || resource.ID == "knowledge-index"
		selectedSkills = selectedSkills || resource.ID == "skill-skill-1"
	}
	if disabledKnowledge || !selectedSkills {
		t.Fatalf("knowledge and skills were not independently routed: %#v", preview.ContextManifest)
	}
	input.Skills = nil
	preview, err = engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range preview.ContextManifest {
		if strings.HasPrefix(resource.ID, "skill-") {
			t.Fatal("skills entrypoint was created without selected skills")
		}
	}
}
