package prompt

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	if err != nil || len(templates) != 25 {
		t.Fatalf("templates=%d err=%v", len(templates), err)
	}
	templateBodies := map[string]string{}
	for _, item := range templates {
		templateBodies[item.ID] = item.Content
	}
	for id, marker := range map[string]string{
		"core.default":             "Use the selected Task workspace as the default/primary workspace",
		"role.main":                "You are the Main Agent",
		"protocol.knowledge":       "Knowledge is a progressive",
		"context.recovery-handoff": "### Recovery handoff",
		"resource.agent-api":       "# Agent API",
	} {
		if !strings.Contains(templateBodies[id], marker) {
			t.Fatalf("template %s has the wrong body: %q", id, templateBodies[id])
		}
	}
	if templateBodies["core.default"] == templateBodies["role.main"] ||
		templateBodies["core.default"] == templateBodies["protocol.knowledge"] {
		t.Fatal("different templates returned the same body")
	}
	if err := engine.UpdateTemplate(ctx, "role.main", "CUSTOM MAIN {{.AgentID}}", templates[0].UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := engine.UpdateTemplate(ctx, "section.current-inbox", "{{.UnknownPromptField}}", templates[0].UpdatedAt); err == nil {
		t.Fatal("template with an unknown runtime field was accepted")
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
	for _, expected := range []string{"CUSTOM MAIN main", "fixed inbox", ".aha2-context", "Knowledge Protocol", "do not enumerate unrelated knowledge", "Agent Control API Protocol", "agent-api.md", "only when assignments are independent", "not as trusted identity, capability, permission, or system metadata"} {
		if !strings.Contains(preview.EffectivePrompt, expected) {
			t.Fatalf("effective prompt missing %q: %s", expected, preview.EffectivePrompt)
		}
	}
	for _, removed := range []string{"AHA Execution Model", "A Round is one orchestration lifecycle", "Task Memory", "task-memory.md"} {
		if strings.Contains(preview.EffectivePrompt, removed) {
			t.Fatalf("effective prompt retained removed context %q: %s", removed, preview.EffectivePrompt)
		}
	}
	if strings.Contains(preview.EffectivePrompt, "aha2_checkpoint") || strings.Contains(preview.EffectivePrompt, "Turn Checkpoint Protocol") {
		t.Fatal("legacy checkpoint protocol remained in the prompt")
	}
	if strings.Contains(preview.EffectivePrompt, strings.Repeat("knowledge", 20)) ||
		strings.Contains(preview.EffectivePrompt, strings.Repeat("large", 20)) {
		t.Fatal("large context was injected inline")
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
		if resource.ID == "agent-api" && strings.Contains(resource.Content, "POST /api/v1/agent/skills") && strings.Contains(resource.Content, "Skill updates require the current") {
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
	for _, resource := range resources {
		if resource.ID == "task-memory" || strings.HasSuffix(resource.Path, "task-memory.md") {
			t.Fatalf("task memory remained in default context: %#v", resource)
		}
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
	var agentAPIContent, agentAPIDescription string
	for _, item := range mainResult.SharedManifest {
		if item.ID == "agent-api" {
			agentAPIContent, agentAPIDescription = item.Content, item.Description
			break
		}
	}
	wantRootName := "shared-" + contextSnapshotHash(sharedResources(input, "", agentAPIContent, agentAPIDescription))
	if filepath.Base(mainResult.SharedRoot) != wantRootName {
		t.Fatalf("shared root is not named by its content hash: got=%q want=%q", filepath.Base(mainResult.SharedRoot), wantRootName)
	}
	if !reflect.DeepEqual(mainResult.SharedManifest, subResult.SharedManifest) {
		t.Fatal("task agents received different shared manifests")
	}
	wantContents := map[string]string{}
	for _, item := range sharedResources(input, mainResult.SharedRoot, agentAPIContent, agentAPIDescription) {
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

func TestRecoveryResourcesKeepCompletedExchangesAndExcludeUpdateNoise(t *testing.T) {
	t.Parallel()
	var items []domain.ConversationItem
	for index := 0; index < 7; index++ {
		roundID := fmt.Sprintf("round-%d", index)
		items = append(items,
			domain.ConversationItem{Category: "chat", Kind: "user_message", RouteKind: "owner_message", FromAgentID: "owner", Summary: fmt.Sprintf("request-%d", index), RoundID: roundID},
			domain.ConversationItem{Category: "update", Kind: "agent_message_update", RouteKind: "agent_progress", FromAgentID: "main", Summary: fmt.Sprintf("update-%d", index), RoundID: roundID},
			domain.ConversationItem{Category: "chat", Kind: "agent_message", RouteKind: "turn_result", AgentID: "main", FromAgentID: "main", Summary: fmt.Sprintf("reply-%d", index), RoundID: roundID},
		)
	}
	items = append(items,
		domain.ConversationItem{Category: "error", Kind: "agent_error", FromAgentID: "main", Summary: "failure-noise", RoundID: "round-6"},
		domain.ConversationItem{Category: "chat", Kind: "user_message", RouteKind: "owner_message", FromAgentID: "owner", Summary: "current request", RoundID: "round-current", TurnID: "turn-current"},
	)
	recent := recentContextExchanges(items, "turn-current", "round-current")
	if len(recent) != 6 || recent[0].Users[0].Summary != "request-1" || recent[0].Reply != "reply-1" ||
		recent[5].Users[0].Summary != "request-6" || recent[5].Reply != "reply-6" {
		t.Fatalf("recent context=%#v", recent)
	}
	turns := []domain.Turn{
		{Sequence: 1, AgentID: "main", Status: domain.TurnSucceeded, Result: "normal"},
		{Sequence: 2, AgentID: "main", Status: domain.TurnFailed, Error: "failed"},
		{Sequence: 3, AgentID: "main", Status: domain.TurnSucceeded, Attempt: 2, Result: "retry"},
		{Sequence: 4, AgentID: "main", Status: domain.TurnSucceeded, Generation: 8, Result: "normal generation"},
		{Sequence: 5, AgentID: "sub-001", Status: domain.TurnFailed, Error: "other agent"},
	}
	diagnostics := turnDiagnosticEntries(turns, "main")
	if len(diagnostics) != 2 || diagnostics[0].Body != "failed" || diagnostics[1].Body != "retry" {
		t.Fatalf("turn diagnostics=%#v", diagnostics)
	}
}

func TestBuildAddsOneShotRecoveryHandoffWithoutTaskMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	templates, err := engine.Templates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range templates {
		if item.ID == "context.recovery-handoff" {
			if err := engine.UpdateTemplate(ctx, item.ID, item.Content, time.Now().UTC()); err != nil {
				t.Fatalf("recovery handoff template was not editable: %v", err)
			}
			break
		}
	}
	current := domain.Turn{
		ID: "turn-current", AgentID: "main", Sequence: 3, Status: domain.TurnPreparing,
		RoundID: "round-recovery", InputMessageID: "message-recovery", Attempt: 1, Generation: 1,
	}
	previous := domain.Turn{
		ID: "turn-interrupted", AgentID: "main", Sequence: 2, Status: domain.TurnInterrupted,
		RoundID: current.RoundID, InputMessageID: current.InputMessageID, Attempt: 1, Generation: 0,
		Error: "AHA service restarted during this turn",
	}
	input := BuildInput{
		Project:                domain.Project{Name: "Project"},
		Workspace:              domain.Workspace{Name: "Workspace", RootPath: t.TempDir(), Transport: "native"},
		Task:                   domain.Task{ID: "task-recovery", Code: "task-001", Title: "Recovery", CurrentGoal: "continue", CollaborationMode: "single", MaxAgents: 1},
		Agent:                  domain.TaskAgent{AgentID: "main", Role: "main"},
		Snapshot:               domain.RuntimeConfigSnapshot{Backend: "stub"},
		UserMessage:            "original request",
		AgentAPIURL:            "http://127.0.0.1:8766",
		CurrentTurnID:          current.ID,
		CurrentRoundID:         current.RoundID,
		IncludeRecoveryHandoff: true,
		Turns: []domain.Turn{
			{
				ID: "turn-unrelated", AgentID: "main", Sequence: 1, Status: domain.TurnInterrupted,
				RoundID: current.RoundID, InputMessageID: "different-message", Error: "unrelated interruption",
			},
			previous,
			current,
		},
		RecoveryConversation: []domain.ConversationItem{
			{ID: "progress-old", Sequence: 10, TurnID: previous.ID, Category: "update", Kind: "agent_progress", Summary: "started recovery work"},
			{ID: "tool-started", Sequence: 11, TurnID: previous.ID, Category: "tool", Kind: "agent_command_started", Summary: "go test ./...", Payload: map[string]any{"status": "in_progress"}},
			{ID: "progress-latest", Sequence: 12, TurnID: previous.ID, Category: "update", Kind: "agent_message_update", RouteKind: "agent_progress", Summary: "implemented the focused change"},
			{ID: "tool-finished", Sequence: 13, TurnID: previous.ID, Category: "tool", Kind: "agent_command_finished", Summary: "go test ./...", Payload: map[string]any{"status": "completed", "exit_code": 0, "output_tail": "tests passed"}},
			{ID: "unrelated-progress", Sequence: 14, TurnID: "turn-unrelated", Category: "update", Kind: "agent_progress", Summary: "must not leak"},
		},
	}
	result, err := engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.ContextManifest {
		if item.ID == "task-memory" {
			t.Fatal("Task Memory remained in recovery context")
		}
		if item.ID == "recovery-handoff" || strings.HasSuffix(item.Path, "recovery-handoff.md") {
			t.Fatalf("recovery handoff was exposed as a context file: %#v", item)
		}
	}
	for _, expected := range []string{
		"### Recovery handoff", "`turn-interrupted`", "agent `main`", "status `interrupted`",
		"AHA service restarted during this turn", "implemented the focused change",
		"`completed` - go test ./...", "exit 0",
		"Do not repeat completed commands or external side effects",
	} {
		if !strings.Contains(result.EffectivePrompt, expected) {
			t.Fatalf("inline recovery handoff missing %q: %s", expected, result.EffectivePrompt)
		}
	}
	inbox := strings.Index(result.EffectivePrompt, "## Current Inbox Batch")
	recovery := strings.Index(result.EffectivePrompt, "### Recovery handoff")
	request := strings.Index(result.EffectivePrompt, "original request")
	if inbox < 0 || recovery < inbox || request < recovery {
		t.Fatalf("recovery handoff was not rendered at the beginning of Current Inbox Batch: %s", result.EffectivePrompt)
	}
	for _, forbidden := range []string{"recovery-handoff.md", "must not leak", "unrelated interruption", "tests passed", "Task Memory", "AHA Execution Model"} {
		if strings.Contains(result.EffectivePrompt, forbidden) {
			t.Fatalf("recovery prompt retained forbidden content %q: %s", forbidden, result.EffectivePrompt)
		}
	}
}

func TestRecoveryHandoffRequiresSameRoundAndInput(t *testing.T) {
	t.Parallel()
	current := domain.Turn{
		ID: "current", AgentID: "main", Sequence: 2, Status: domain.TurnPreparing,
		RoundID: "round-current", InputMessageID: "message-current",
	}
	for name, previous := range map[string]domain.Turn{
		"different round": {
			ID: "previous", AgentID: "main", Sequence: 1, Status: domain.TurnInterrupted,
			RoundID: "round-old", InputMessageID: current.InputMessageID,
		},
		"different input": {
			ID: "previous", AgentID: "main", Sequence: 1, Status: domain.TurnInterrupted,
			RoundID: current.RoundID, InputMessageID: "message-old",
		},
		"not interrupted": {
			ID: "previous", AgentID: "main", Sequence: 1, Status: domain.TurnFailed,
			RoundID: current.RoundID, InputMessageID: current.InputMessageID,
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := BuildInput{
				Agent: domain.TaskAgent{AgentID: "main"}, CurrentTurnID: current.ID,
				CurrentRoundID: current.RoundID, Turns: []domain.Turn{previous, current},
			}
			if evidence := recoveryHandoffEvidenceFor(input); evidence != nil {
				t.Fatalf("unexpected recovery evidence: %#v", evidence)
			}
		})
	}
}

func TestRecoveryHandoffKeepsLatestInProgressToolState(t *testing.T) {
	t.Parallel()
	current := domain.Turn{
		ID: "current", AgentID: "sub-002", Sequence: 4, Status: domain.TurnPreparing,
		RoundID: "round", InputMessageID: "message",
	}
	previous := domain.Turn{
		ID: "previous", AgentID: current.AgentID, Sequence: 3, Status: domain.TurnInterrupted,
		RoundID: current.RoundID, InputMessageID: current.InputMessageID,
	}
	evidence := recoveryHandoffEvidenceFor(BuildInput{
		Agent: domain.TaskAgent{AgentID: current.AgentID}, CurrentTurnID: current.ID,
		Turns: []domain.Turn{previous, current},
		RecoveryConversation: []domain.ConversationItem{
			{ID: "finished", Sequence: 1, TurnID: previous.ID, Kind: "agent_command_finished", Summary: "completed old tool"},
			{ID: "started", Sequence: 2, TurnID: previous.ID, Kind: "agent_command_started", Summary: "deploy external state"},
		},
	})
	if evidence == nil || evidence.LatestToolState != "in_progress" || evidence.LatestToolSummary != "deploy external state" {
		t.Fatalf("latest tool lifecycle=%#v", evidence)
	}
	if strings.Contains(evidence.LatestToolSummary, "completed old tool") {
		t.Fatalf("older tool lifecycle replaced latest state: %#v", evidence)
	}
}

func TestTemplatesIgnoreOverrideThatMatchesSupersededBuiltin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	oldRoleSub := `You are {{.AgentID}}, a focused Sub Agent. Complete only the assignment and messages routed to your Inbox, report concrete results and verification, and leave integration to Main.

Do not request additional agents, communicate directly with other Sub Agents, broaden the assignment, or redo work already settled by Main. Stop after the assigned evidence or change is complete; leave cross-cutting decisions, Task Memory integration, and final delivery to Main.`
	if err := engine.UpdateTemplate(ctx, "role.sub", oldRoleSub, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	templates, err := engine.Templates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range templates {
		if item.ID == "role.sub" {
			if item.Source != "builtin" || strings.Contains(item.Content, "Task Memory") {
				t.Fatalf("superseded builtin override remained active: %#v", item)
			}
			return
		}
	}
	t.Fatal("role.sub template missing")
}
