package prompt

import (
	"context"
	"path/filepath"
	"reflect"
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
	if err != nil || len(templates) != 12 {
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
			{ID: "global-root", Scope: "global", Slug: "index", IsIndex: true, Type: "navigation", Title: "Global index", Body: "这是 AHA2 全局知识页面。通用知识以人类阅读为主，Agent 经验教训优先。", Revision: 1},
			{ID: store.GlobalGeneralKnowledgeID, Scope: "global", ParentID: "global-root", Slug: "general", Type: "navigation", Title: "通用知识", Body: "Human-first reference.", Revision: 1},
			{ID: store.GlobalAgentLessonsKnowledgeID, Scope: "global", ParentID: "global-root", Slug: "agent-lessons", Type: "navigation", Title: "Agent 经验教训", Body: "Agent-first lessons.", Revision: 1},
			{ID: store.GlobalTechnicalLessonsKnowledgeID, Scope: "global", ParentID: store.GlobalAgentLessonsKnowledgeID, Slug: "technical-diagnostics", Type: "navigation", Title: "技术诊断", Body: "Technical traps.", Revision: 1},
			{ID: store.GlobalBehaviorLessonsKnowledgeID, Scope: "global", ParentID: store.GlobalAgentLessonsKnowledgeID, Slug: "behavior-lessons", Type: "navigation", Title: "行为教训", Body: "Behavior corrections.", Revision: 1},
			{ID: "global-1", Scope: "global", ParentID: store.GlobalGeneralKnowledgeID, Slug: "global", Type: "practice", Title: "Global", Body: "# Global\n\nUse this for shared decisions.", Revision: 1},
			{ID: "global-deep", Scope: "global", ParentID: "global-1", Slug: "deep", Type: "practice", Title: "Deep", Body: "Nested detail", Revision: 1},
		},
		ProjectKnowledge: []domain.KnowledgeEntry{
			{ID: "project-root", Scope: "project", ProjectID: "project-1", Slug: "index", IsIndex: true, Type: "navigation", Title: "Project index", Body: "Project home.", Status: domain.KnowledgeVerified, Revision: 1},
			{ID: "project-practice", Scope: "project", ProjectID: "project-1", ParentID: "project-root", Slug: "project-practice", Type: "practice", Title: "Project practice", Body: "Practice body", Status: domain.KnowledgeVerified, Revision: 1},
			{ID: "project-navigation-group", Scope: "project", ProjectID: "project-1", ParentID: "project-root", Slug: "modules", Type: "practice", Title: "Module index", Body: "Choose a module.", Status: domain.KnowledgeVerified, Revision: 1},
			{ID: "project-navigation", Scope: "project", ProjectID: "project-1", ParentID: "project-navigation-group", Slug: "project-navigation", Type: "navigation", Title: "Project map", Body: "Navigation body", Status: domain.KnowledgeVerified, Revision: 1},
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
	for _, expected := range []string{"CUSTOM MAIN main", "fixed inbox", ".aha2-context", "Knowledge Protocol", "Do not enumerate the knowledge directory", "Agent Control API Protocol", "agent-api.md"} {
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
	var globalIndexFound, agentLessonsIndexFound, projectIndexFound, projectNavigationIndexFound, navigationGroupFound, staleIndexFound, staleDetailFound, knowledgeDetailFound, nestedDetailFound, projectDetailFound, navigationDetailFound, nestedNavigationDetailFound, attachmentIndexFound, attachmentFileFound, skillDetailFound, skillScriptFound, hardwareFound, agentAPIUTF8Found, agentAPISkillCreateFound bool
	knowledgeEntryPoints := 0
	manifestFound := false
	resources := append(append([]ContextResource(nil), preview.ContextManifest...), preview.SharedManifest...)
	for _, resource := range resources {
		if strings.HasPrefix(resource.ID, "knowledge-") && resource.EntryPoint {
			knowledgeEntryPoints++
			if strings.Contains(resource.Content, "- id:") || strings.Contains(resource.Content, "- scope:") {
				t.Fatalf("knowledge index leaked document metadata: %s", resource.Content)
			}
		}
		if resource.ID == "knowledge-global-index" && resource.EntryPoint && strings.Contains(resource.Content, "# 全局知识") && strings.Contains(resource.Content, "通用知识") && strings.Contains(resource.Content, "general.md") && strings.Contains(resource.Content, "agent-lessons.md") && !strings.Contains(resource.Content, "Use this for shared decisions.") {
			globalIndexFound = true
		}
		if resource.ID == "knowledge-"+store.GlobalAgentLessonsKnowledgeID && resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "global", "agent-lessons.md")) && strings.Contains(resource.Content, "技术诊断") && strings.Contains(resource.Content, "Technical traps.") && strings.Contains(resource.Content, "行为教训") {
			agentLessonsIndexFound = true
		}
		if resource.ID == "knowledge-project-index" && resource.EntryPoint && strings.Contains(resource.Content, "# 项目知识") && strings.Contains(resource.Content, "project-practice.md") && strings.Contains(resource.Content, "Practice body") && strings.Contains(resource.Content, "navigation/modules.md") && !strings.Contains(resource.Content, "Project map") && strings.Contains(resource.Content, "Project home.") {
			projectIndexFound = true
		}
		if resource.ID == "knowledge-project-navigation-index" && resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "project", "navigation", "index.md")) && strings.Contains(resource.Content, "# 项目导航") && strings.Contains(resource.Content, "根据下面的标题与摘要") && strings.Contains(resource.Content, "project-navigation.md") && strings.Contains(resource.Content, "Navigation body") && !strings.Contains(resource.Content, "Project practice") && !strings.Contains(resource.Content, "Deep route") {
			projectNavigationIndexFound = true
		}
		if resource.ID == "knowledge-pending-updates" && resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "pending-updates", "index.md")) && strings.Contains(resource.Content, "不得作为当前事实使用") && strings.Contains(resource.Content, "Old flow") {
			staleIndexFound = true
		}
		if resource.ID == "knowledge-project-navigation-index" && resource.EntryPoint && strings.Contains(resource.Content, "modules.md") && strings.Contains(resource.Content, "Choose a module.") && !strings.Contains(resource.Content, "Project map") {
			projectNavigationIndexFound = true
		}
		if resource.ID == "knowledge-project-navigation-group" && !resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "project", "navigation", "modules.md")) && strings.Contains(resource.Content, filepath.Join("modules", "project-navigation.md")) {
			navigationGroupFound = true
		}
		if resource.ID == "knowledge-stale-project-stale" && !resource.EntryPoint && strings.Contains(resource.Content, "entry_id: project-stale") && strings.Contains(resource.Content, "base_revision: 3") && strings.Contains(resource.Content, "Outdated instructions") {
			staleDetailFound = true
		}
		if resource.ID == "knowledge-global-1" && !resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "global", "general", "global.md")) && strings.Contains(resource.Content, "global/deep.md") {
			knowledgeDetailFound = true
		}
		if resource.ID == "knowledge-global-deep" && !resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "global", "general", "global", "deep.md")) {
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
		if resource.ID == "knowledge-project-navigation" && !resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "project", "navigation", "modules", "project-navigation.md")) && strings.Contains(resource.Content, "project-navigation/deep.md") {
			navigationDetailFound = true
		}
		if resource.ID == "knowledge-project-navigation-deep" && !resource.EntryPoint && strings.Contains(resource.Path, filepath.Join("knowledge", "project", "navigation", "modules", "project-navigation", "deep.md")) {
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
			manifestFound = true
		}
		if resource.ID == "hardware" && strings.Contains(resource.Content, "COM3") &&
			strings.Contains(resource.Content, "password configured: true") &&
			!strings.Contains(resource.Content, "credential") {
			hardwareFound = true
		}
		if resource.ID == "agent-api" && strings.Contains(resource.Content, "application/json; charset=utf-8") && strings.Contains(resource.Content, "UTF8.GetBytes") {
			agentAPIUTF8Found = true
		}
		if resource.ID == "agent-api" && strings.Contains(resource.Content, "POST /api/v1/agent/skills") && strings.Contains(resource.Content, "automatically selected for the current Task") {
			agentAPISkillCreateFound = true
		}
	}
	if !globalIndexFound || !agentLessonsIndexFound || !projectIndexFound || !projectNavigationIndexFound || !navigationGroupFound || !staleIndexFound || !staleDetailFound || !knowledgeDetailFound || !nestedDetailFound || !projectDetailFound || !navigationDetailFound || !nestedNavigationDetailFound || knowledgeEntryPoints != 5 {
		t.Fatalf("knowledge hierarchy missing: global=%t lessons=%t project=%t navigation=%t navigation_group=%t stale_index=%t stale_detail=%t detail=%t nested=%t project_detail=%t navigation_detail=%t nested_navigation=%t entrypoints=%d", globalIndexFound, agentLessonsIndexFound, projectIndexFound, projectNavigationIndexFound, navigationGroupFound, staleIndexFound, staleDetailFound, knowledgeDetailFound, nestedDetailFound, projectDetailFound, navigationDetailFound, nestedNavigationDetailFound, knowledgeEntryPoints)
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
	if !agentAPISkillCreateFound {
		t.Fatal("Agent API resource is missing the Skill creation contract")
	}
	var memoryFound bool
	for _, resource := range resources {
		if resource.ID == "task-memory" && strings.Contains(resource.Content, "decision one") &&
			strings.Contains(resource.Content, "fact one") && strings.Contains(resource.Content, "global-1") {
			memoryFound = true
		}
	}
	if !memoryFound {
		t.Fatal("task memory resource missing")
	}
	if manifestFound {
		t.Fatal("manifest.json remained in materialized context resources")
	}
	if strings.Contains(preview.EffectivePrompt, strings.Repeat("review", 20)) || strings.Contains(preview.EffectivePrompt, "global-1.md") {
		t.Fatal("knowledge or skill detail leaked into the effective prompt")
	}
	for _, expected := range []string{filepath.Join("knowledge", "global", "index.md"), filepath.Join("knowledge", "global", "agent-lessons.md"), filepath.Join("knowledge", "project", "index.md"), filepath.Join("knowledge", "project", "navigation", "index.md"), filepath.Join("knowledge", "pending-updates", "index.md")} {
		if !strings.Contains(preview.EffectivePrompt, expected) {
			t.Fatalf("effective prompt missing knowledge entrypoint %q", expected)
		}
	}
	for _, hidden := range []string{"project-practice.md", "project-navigation.md", filepath.Join("project-navigation", "deep.md")} {
		if strings.Contains(preview.EffectivePrompt, hidden) {
			t.Fatalf("knowledge detail %q leaked into effective prompt", hidden)
		}
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
	for _, resource := range append(append([]ContextResource(nil), preview.ContextManifest...), preview.SharedManifest...) {
		disabledKnowledge = disabledKnowledge || strings.HasPrefix(resource.ID, "knowledge-")
		selectedSkills = selectedSkills || resource.ID == "skill-skill-1"
	}
	if disabledKnowledge || !selectedSkills {
		t.Fatalf("knowledge and skills were not independently routed: %#v", preview.ContextManifest)
	}
	if strings.Contains(preview.EffectivePrompt, "Knowledge Protocol") {
		t.Fatal("knowledge protocol remained enabled when knowledge was disabled")
	}
	if preview.SharedRoot == "" || len(preview.SharedManifest) == 0 {
		t.Fatalf("disabled knowledge removed shared Skill and Agent API resources: root=%q resources=%d", preview.SharedRoot, len(preview.SharedManifest))
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

func TestEngineRoutesTaskTakeoverWithoutChannelAssistantIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	base := BuildInput{
		Project:   domain.Project{Name: "Project"},
		Workspace: domain.Workspace{Name: "Workspace", RootPath: "/repo", Transport: "native"},
		Task: domain.Task{
			ID: "task-identity", Code: "task-013", Title: "Identity", TaskWorkspacePath: "/repo",
			CollaborationMode: "single", MaxAgents: 1,
		},
		Agent:       domain.TaskAgent{AgentID: "main", Role: "main"},
		Snapshot:    domain.RuntimeConfigSnapshot{Backend: "codex"},
		UserMessage: "test identity",
	}
	tests := []struct {
		name           string
		channelContext map[string]any
		wantIdentity   string
		wantChannel    string
		wantSession    string
	}{
		{name: "web", wantIdentity: "Task Agent Identity", wantChannel: "AHA Web Channel", wantSession: "task-agent:web"},
		{
			name: "task takeover", channelContext: map[string]any{
				"endpoint": domain.ChannelEndpointAssistantDM,
				"route":    map[string]any{"mode": "task_route"},
			},
			wantIdentity: "Task Agent Identity", wantChannel: "External Channel", wantSession: "task-agent:external-channel",
		},
		{
			name: "owner assistant host", channelContext: map[string]any{
				"endpoint": domain.ChannelEndpointAssistantDM,
				"route":    map[string]any{"mode": "assistant"},
			},
			wantIdentity: "Channel Assistant Identity", wantChannel: "External Channel", wantSession: "channel-assistant:external-channel",
		},
		{
			name: "group host", channelContext: map[string]any{
				"endpoint": domain.ChannelEndpointGroupDigitalHuman,
				"route":    map[string]any{"mode": "group_qa"},
			},
			wantIdentity: "Channel Digital Human Identity", wantChannel: "External Channel", wantSession: "channel-digital-human:external-channel",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			input.ChannelContext = test.channelContext
			preview, buildErr := engine.Build(ctx, input)
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			if !strings.Contains(preview.EffectivePrompt, "## "+test.wantIdentity) || !strings.Contains(preview.EffectivePrompt, "## "+test.wantChannel) {
				t.Fatalf("prompt routing mismatch: identity=%q channel=%q\n%s", test.wantIdentity, test.wantChannel, preview.EffectivePrompt)
			}
			if got := BackendSessionContext(test.channelContext); got != test.wantSession {
				t.Fatalf("BackendSessionContext()=%q want %q", got, test.wantSession)
			}
			if test.name == "task takeover" && strings.Contains(preview.EffectivePrompt, "## Channel Assistant Identity") {
				t.Fatal("task takeover received channel assistant identity")
			}
		})
	}
}

func TestRemoteWindowsUNCContextPathKeepsNetworkRoot(t *testing.T) {
	t.Parallel()
	input := BuildInput{
		Workspace: domain.Workspace{Transport: "ssh", Platform: "windows/amd64"},
		Task:      domain.Task{ID: "task-1"}, Agent: domain.TaskAgent{AgentID: "main"},
	}
	if got := contextRootFor(input, `\\server\share\repo`); got != `//server/share/repo/.aha2-context/task-1/main` {
		t.Fatalf("remote Windows UNC context root = %q", got)
	}
}

func TestSharedSnapshotIsSharedByTaskAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	input := sharedSnapshotTestInput()

	mainResult, err := engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Agent = domain.TaskAgent{AgentID: "sub-002", Role: "sub"}
	subResult, err := engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}

	if mainResult.ContextRoot == subResult.ContextRoot {
		t.Fatalf("agent-private context roots were shared: %s", mainResult.ContextRoot)
	}
	if mainResult.SharedRoot == "" || mainResult.SharedRoot != subResult.SharedRoot {
		t.Fatalf("task shared root was not shared: main=%q sub=%q", mainResult.SharedRoot, subResult.SharedRoot)
	}
	wantRootName := "shared-" + contextSnapshotHash(sharedResources(input, ""))
	if filepath.Base(mainResult.SharedRoot) != wantRootName {
		t.Fatalf("shared root is not named by its content hash: got=%q want=%q", filepath.Base(mainResult.SharedRoot), wantRootName)
	}
	if !reflect.DeepEqual(mainResult.SharedManifest, subResult.SharedManifest) {
		t.Fatal("task agents received different shared manifests")
	}
	wantContents := map[string]string{}
	for _, item := range sharedResources(input, mainResult.SharedRoot) {
		wantContents[item.ID] = item.Content
	}
	for _, item := range mainResult.ContextManifest {
		if !strings.HasPrefix(strings.ToLower(item.Path), strings.ToLower(mainResult.ContextRoot+string(filepath.Separator))) {
			t.Fatalf("private resource escaped agent context root: %s", item.Path)
		}
		if strings.HasPrefix(item.ID, "knowledge-") || strings.HasPrefix(item.ID, "skill-") || item.ID == "agent-api" {
			t.Fatalf("shared resource remained in the private manifest: %s", item.ID)
		}
	}
	for _, item := range mainResult.SharedManifest {
		if !strings.HasPrefix(strings.ToLower(item.Path), strings.ToLower(mainResult.SharedRoot+string(filepath.Separator))) {
			t.Fatalf("resource escaped shared root: %s", item.Path)
		}
		if item.EntryPoint && !strings.Contains(mainResult.EffectivePrompt, item.Path) {
			t.Fatalf("prompt did not expose shared knowledge entrypoint: %s", item.Path)
		}
		if item.Content != wantContents[item.ID] {
			t.Fatalf("shared snapshot changed knowledge content for %s", item.ID)
		}
		if strings.Contains(item.URI, "/agents/") || !strings.HasPrefix(item.URI, "aha://tasks/task-shared/shared/") {
			t.Fatalf("shared resource retained an Agent-specific URI: %s", item.URI)
		}
	}
	for _, expected := range []string{"agent-api", "skill-skill-shared", "knowledge-global-index", "knowledge-project-index"} {
		if _, ok := wantContents[expected]; !ok {
			t.Fatalf("shared snapshot is missing %s", expected)
		}
	}
}

func TestSharedSnapshotChangesWithSharedContent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	input := sharedSnapshotTestInput()

	first, err := engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.ProjectKnowledge = append([]domain.KnowledgeEntry(nil), input.ProjectKnowledge...)
	input.ProjectKnowledge[1].Body = "Updated detail body"
	input.ProjectKnowledge[1].Revision++
	second, err := engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.SharedRoot == second.SharedRoot {
		t.Fatalf("knowledge change reused immutable snapshot root: %s", first.SharedRoot)
	}
	if reflect.DeepEqual(first.SharedManifest, second.SharedManifest) {
		t.Fatal("knowledge change reused the previous manifest")
	}
	first = second
	input.Skills = append([]domain.Skill(nil), input.Skills...)
	input.Skills[0].PackageFiles = append([]domain.SkillFile(nil), input.Skills[0].PackageFiles...)
	input.Skills[0].PackageFiles[0].Content += "\nupdated"
	second, err = engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.SharedRoot == second.SharedRoot {
		t.Fatalf("Skill change reused immutable snapshot root: %s", first.SharedRoot)
	}
	first = second
	input.AgentAPIURL = "http://127.0.0.1:9876"
	second, err = engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.SharedRoot == second.SharedRoot {
		t.Fatalf("Agent API URL change reused immutable snapshot root: %s", first.SharedRoot)
	}
}

func TestSharedSnapshotFollowsAvailableSharedResources(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	input := sharedSnapshotTestInput()
	input.KnowledgeEnabled = false

	result, err := engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.SharedRoot == "" || len(result.SharedManifest) == 0 {
		t.Fatalf("disabled knowledge removed Skill or Agent API shared state: root=%q manifest=%#v", result.SharedRoot, result.SharedManifest)
	}
	if strings.Contains(result.EffectivePrompt, "Knowledge Protocol") || strings.Contains(result.EffectivePrompt, filepath.Join("knowledge", "project", "index.md")) {
		t.Fatal("disabled knowledge was exposed in the prompt")
	}
	for _, item := range result.SharedManifest {
		if strings.HasPrefix(item.ID, "knowledge-") {
			t.Fatalf("disabled knowledge remained in shared snapshot: %s", item.ID)
		}
	}
	input.AgentAPIURL = ""
	input.Skills = nil
	result, err = engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.SharedRoot != "" || len(result.SharedManifest) != 0 {
		t.Fatalf("empty shared resources created a snapshot: root=%q manifest=%#v", result.SharedRoot, result.SharedManifest)
	}
}

func sharedSnapshotTestInput() BuildInput {
	return BuildInput{
		Project:     domain.Project{Name: "Project"},
		Workspace:   domain.Workspace{Name: "Workspace", RootPath: filepath.Join("repo"), Transport: "native"},
		Task:        domain.Task{ID: "task-shared", Code: "task-001", Title: "Shared knowledge", CurrentGoal: "test", CollaborationMode: "auto", MaxAgents: 3},
		Agent:       domain.TaskAgent{AgentID: "main", Role: "main"},
		Snapshot:    domain.RuntimeConfigSnapshot{Backend: "codex"},
		AgentAPIURL: "http://127.0.0.1:8766",
		Skills: []domain.Skill{{ID: "skill-shared", PackageSlug: "shared-skill", Scope: "global", Name: "Shared Skill", Version: 1, Enabled: true, PackageFiles: []domain.SkillFile{
			{Path: "SKILL.md", Content: "---\nname: shared-skill\ndescription: shared\n---\n\nShared instructions."},
		}}},
		KnowledgeEnabled: true,
		GlobalKnowledge: []domain.KnowledgeEntry{
			{ID: "global-root", Scope: "global", Slug: "index", IsIndex: true, Type: "navigation", Title: "Global", Body: "Global index", Revision: 1},
			{ID: "global-detail", Scope: "global", ParentID: "global-root", Slug: "detail", Type: "practice", Title: "Global detail", Body: "Global detail body", Revision: 1},
		},
		ProjectKnowledge: []domain.KnowledgeEntry{
			{ID: "project-root", Scope: "project", Slug: "index", IsIndex: true, Type: "navigation", Title: "Project", Body: "Project index", Revision: 1},
			{ID: "project-detail", Scope: "project", ParentID: "project-root", Slug: "detail", Type: "practice", Title: "Project detail", Body: "Project detail body", Revision: 1},
		},
	}
}

func TestRecoveryResourcesExcludeToolNoiseAndNormalTurns(t *testing.T) {
	t.Parallel()
	items := []domain.ConversationItem{
		{Category: "chat", Kind: "user_message", FromAgentID: "owner", Summary: "previous request", TurnID: "turn-old"},
		{Category: "tool", Kind: "agent_command_started", FromAgentID: "main", Summary: "secret-noise", TurnID: "turn-old"},
		{Category: "update", Kind: "turn_duration", FromAgentID: "aha", Summary: "duration-noise", TurnID: "turn-old"},
		{Category: "error", Kind: "agent_error", FromAgentID: "main", Summary: "previous failure", TurnID: "turn-old"},
		{Category: "chat", Kind: "user_message", FromAgentID: "owner", Summary: "current request", TurnID: "turn-current"},
	}
	recent := recentContextResource(items, "turn-current", "")
	if !strings.Contains(recent, "previous request") || !strings.Contains(recent, "previous failure") || strings.Contains(recent, "secret-noise") || strings.Contains(recent, "duration-noise") || strings.Contains(recent, "current request") {
		t.Fatalf("recent context=%s", recent)
	}
	turns := []domain.Turn{
		{Sequence: 1, AgentID: "main", Status: domain.TurnSucceeded, Result: "normal"},
		{Sequence: 2, AgentID: "main", Status: domain.TurnFailed, Error: "failed"},
		{Sequence: 3, AgentID: "main", Status: domain.TurnSucceeded, Attempt: 2, Result: "retry"},
		{Sequence: 4, AgentID: "main", Status: domain.TurnSucceeded, Generation: 8, Result: "normal generation"},
		{Sequence: 5, AgentID: "sub-001", Status: domain.TurnFailed, Error: "other agent"},
	}
	diagnostics := turnDiagnosticsResource(turns, "main")
	if !strings.Contains(diagnostics, "failed") || !strings.Contains(diagnostics, "retry") || strings.Contains(diagnostics, "normal generation") || strings.Contains(diagnostics, "other agent") {
		t.Fatalf("turn diagnostics=%s", diagnostics)
	}
}
