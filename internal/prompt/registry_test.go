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
		GlobalKnowledge: []domain.KnowledgeEntry{
			{ID: "global-root", Scope: "global", Slug: "index", IsIndex: true, Type: "navigation", Title: "Global index", Body: "Start with [Global](global.md).", Revision: 1},
			{ID: "global-1", Scope: "global", ParentID: "global-root", Slug: "global", Type: "practice", Title: "Global", Body: strings.Repeat("knowledge", 200), Revision: 1},
			{ID: "global-deep", Scope: "global", ParentID: "global-1", Slug: "deep", Type: "practice", Title: "Deep", Body: "Nested detail", Revision: 1},
		},
		ProjectKnowledge: []domain.KnowledgeEntry{
			{ID: "project-root", Scope: "project", ProjectID: "project-1", Slug: "index", IsIndex: true, Type: "navigation", Title: "Project index", Body: "Project home.", Status: domain.KnowledgeVerified, Revision: 1},
			{ID: "project-practice", Scope: "project", ProjectID: "project-1", ParentID: "project-root", Slug: "project-practice", Type: "practice", Title: "Project practice", Body: "Practice body", Status: domain.KnowledgeVerified, Revision: 1},
			{ID: "project-navigation", Scope: "project", ProjectID: "project-1", ParentID: "project-root", Slug: "project-navigation", Type: "navigation", Title: "Project map", Body: "Navigation body", Status: domain.KnowledgeVerified, Revision: 1},
			{ID: "project-navigation-deep", Scope: "project", ProjectID: "project-1", ParentID: "project-navigation", Slug: "deep", Type: "navigation", Title: "Deep route", Body: "Nested navigation", Status: domain.KnowledgeVerified, Revision: 1},
		},
		StaleKnowledge: []domain.KnowledgeEntry{
			{ID: "project-stale", Scope: "project", ProjectID: "project-1", Slug: "old-flow", Type: "practice", Title: "Old flow", Body: "Outdated instructions", Status: domain.KnowledgeStale, FeedbackState: "wrong", Revision: 3},
		},
		Attachments: []AttachmentResource{{Attachment: domain.Attachment{ID: "attachment-1", TaskID: "task-1", Name: "screen.png", MediaType: "image/png", Size: 4}, Content: "PNG!"}},
		Skills: []domain.Skill{{ID: "skill-1", PackageSlug: "review", Scope: "global", Name: "Review", Description: "Review code", Instructions: strings.Repeat("review", 200), Version: 1, Enabled: true, PackageFiles: []domain.SkillFile{
			{Path: "SKILL.md", Content: "---\nname: Review\ndescription: Review code\n---\n\n" + strings.Repeat("review", 200)},
			{Path: "scripts/check.sh", Content: "echo reviewed"},
		}}},
		KnowledgeEnabled: true,
		AgentAPIURL:      "http://127.0.0.1:8766",
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
	for _, expected := range []string{"CUSTOM MAIN main", "fixed inbox", ".aha2-context", "manifest.json", "Agent Control API Protocol", "agent-api.md"} {
		if !strings.Contains(preview.EffectivePrompt, expected) {
			t.Fatalf("effective prompt missing %q: %s", expected, preview.EffectivePrompt)
		}
	}
	if strings.Contains(preview.EffectivePrompt, "aha2_checkpoint") || strings.Contains(preview.EffectivePrompt, "Turn Checkpoint Protocol") {
		t.Fatal("legacy checkpoint protocol remained in the prompt")
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
	var globalIndexFound, projectIndexFound, projectNavigationIndexFound, staleIndexFound, staleDetailFound, knowledgeDetailFound, nestedDetailFound, projectDetailFound, navigationDetailFound, nestedNavigationDetailFound, attachmentIndexFound, attachmentFileFound, skillDetailFound, skillScriptFound, hardwareFound, agentAPIUTF8Found bool
	knowledgeEntryPoints := 0
	var manifest ContextResource
	for _, resource := range preview.ContextManifest {
		if strings.HasPrefix(resource.ID, "knowledge-") && resource.EntryPoint {
			knowledgeEntryPoints++
			if strings.Contains(resource.Content, "- id:") || strings.Contains(resource.Content, "- scope:") {
				t.Fatalf("knowledge index leaked document metadata: %s", resource.Content)
			}
		}
		if resource.ID == "knowledge-global-index" && resource.EntryPoint && strings.Contains(resource.Content, "# 全局知识") && strings.Contains(resource.Content, "global.md") && !strings.Contains(resource.Content, "global/deep.md") {
			globalIndexFound = true
		}
		if resource.ID == "knowledge-project-index" && resource.EntryPoint && strings.Contains(resource.Content, "# 项目知识") && strings.Contains(resource.Content, "project-practice.md") && strings.Contains(resource.Content, "Practice body") && !strings.Contains(resource.Content, "Project map") && strings.Contains(resource.Content, "Project home.") {
			projectIndexFound = true
		}
		if resource.ID == "knowledge-project-navigation-index" && resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "project", "navigation", "index.md")) && strings.Contains(resource.Content, "# 项目导航") && strings.Contains(resource.Content, "project-navigation.md") && strings.Contains(resource.Content, "Navigation body") && !strings.Contains(resource.Content, "Project practice") && !strings.Contains(resource.Content, "Deep route") {
			projectNavigationIndexFound = true
		}
		if resource.ID == "knowledge-pending-updates" && resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "pending-updates", "index.md")) && strings.Contains(resource.Content, "不得作为当前事实使用") && strings.Contains(resource.Content, "Old flow") {
			staleIndexFound = true
		}
		if resource.ID == "knowledge-stale-project-stale" && !resource.EntryPoint && strings.Contains(resource.Content, "entry_id: project-stale") && strings.Contains(resource.Content, "base_revision: 3") && strings.Contains(resource.Content, "Outdated instructions") {
			staleDetailFound = true
		}
		if resource.ID == "knowledge-global-1" && !resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "global", "global.md")) && strings.Contains(resource.Content, "global/deep.md") {
			knowledgeDetailFound = true
		}
		if resource.ID == "knowledge-global-deep" && !resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "global", "global", "deep.md")) {
			nestedDetailFound = true
		}
		if resource.ID == "knowledge-project-practice" && !resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "project", "project-practice.md")) {
			projectDetailFound = true
		}
		if resource.ID == "knowledge-project-navigation" && !resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "project", "navigation", "project-navigation.md")) && strings.Contains(resource.Content, "project-navigation/deep.md") {
			navigationDetailFound = true
		}
		if resource.ID == "knowledge-project-navigation-deep" && !resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "project", "navigation", "project-navigation", "deep.md")) {
			nestedNavigationDetailFound = true
		}
		if resource.ID == "attachments-index" && resource.EntryPoint && strings.Contains(resource.Content, "attachment-1/screen.png") && !strings.Contains(resource.Content, `\attachment-1`) {
			attachmentIndexFound = true
		}
		if resource.ID == "attachment-attachment-1" && !resource.EntryPoint && resource.Content == "PNG!" {
			attachmentFileFound = true
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
		if resource.ID == "agent-api" && strings.Contains(resource.Content, "application/json; charset=utf-8") && strings.Contains(resource.Content, "UTF8.GetBytes") {
			agentAPIUTF8Found = true
		}
	}
	if !globalIndexFound || !projectIndexFound || !projectNavigationIndexFound || !staleIndexFound || !staleDetailFound || !knowledgeDetailFound || !nestedDetailFound || !projectDetailFound || !navigationDetailFound || !nestedNavigationDetailFound || knowledgeEntryPoints != 4 {
		t.Fatalf("knowledge hierarchy missing: global=%t project=%t navigation=%t stale_index=%t stale_detail=%t detail=%t nested=%t project_detail=%t navigation_detail=%t nested_navigation=%t entrypoints=%d", globalIndexFound, projectIndexFound, projectNavigationIndexFound, staleIndexFound, staleDetailFound, knowledgeDetailFound, nestedDetailFound, projectDetailFound, navigationDetailFound, nestedNavigationDetailFound, knowledgeEntryPoints)
	}
	if !attachmentIndexFound || !attachmentFileFound {
		t.Fatal("attachment index or file resource missing")
	}
	if !skillDetailFound || !skillScriptFound {
		t.Fatal("skill package entrypoint or script resource missing")
	}
	if !hardwareFound {
		t.Fatal("sanitized hardware resource missing")
	}
	if !agentAPIUTF8Found {
		t.Fatal("Agent API resource is missing the PowerShell UTF-8 request contract")
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
	for _, expected := range []string{filepath.Join("knowledge", "global", "index.md"), filepath.Join("knowledge", "project", "index.md"), filepath.Join("knowledge", "project", "navigation", "index.md"), filepath.Join("knowledge", "pending-updates", "index.md")} {
		if !strings.Contains(preview.EffectivePrompt, expected) {
			t.Fatalf("effective prompt missing knowledge entrypoint %q", expected)
		}
	}
	for _, hidden := range []string{"project-practice.md", "project-navigation.md", filepath.Join("project-navigation", "deep.md")} {
		if strings.Contains(preview.EffectivePrompt, hidden) {
			t.Fatalf("knowledge detail %q leaked into effective prompt", hidden)
		}
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
		disabledKnowledge = disabledKnowledge || strings.HasPrefix(resource.ID, "knowledge-")
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
