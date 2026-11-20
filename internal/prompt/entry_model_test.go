package prompt

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// fixturePromptRepository is an in-memory Repository. It records writes so the
// read-only migration preview can prove it never persists anything.
type fixturePromptRepository struct {
	overrides map[string]domain.PromptTemplate
	upserts   int
	deletes   int
}

func (repository *fixturePromptRepository) PromptTemplateOverrides(context.Context) (map[string]domain.PromptTemplate, error) {
	result := make(map[string]domain.PromptTemplate, len(repository.overrides))
	for id, item := range repository.overrides {
		result[id] = item
	}
	return result, nil
}

func (repository *fixturePromptRepository) UpsertPromptTemplateOverride(_ context.Context, id, content string, now time.Time) error {
	repository.upserts++
	if repository.overrides == nil {
		repository.overrides = map[string]domain.PromptTemplate{}
	}
	version := repository.overrides[id].Version + 1
	repository.overrides[id] = domain.PromptTemplate{ID: id, Content: content, Version: version, UpdatedAt: now}
	return nil
}

func (repository *fixturePromptRepository) DeletePromptTemplateOverride(_ context.Context, id string) error {
	repository.deletes++
	delete(repository.overrides, id)
	return nil
}

func TestEntryCatalogCoversEveryBuiltinTemplate(t *testing.T) {
	t.Parallel()
	engine := NewEngine(&fixturePromptRepository{})
	catalog, err := engine.EntryCatalog(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Entries) != 2 || catalog.Entries[0].ID != EntryCore || catalog.Entries[1].ID != EntryTaskContext {
		t.Fatalf("entries=%#v", catalog.Entries)
	}
	seenLegacy := map[string]string{}
	seenAnchor := map[string]bool{}
	requiredAnchors := 0
	dynamicAnchors := 0
	for _, entry := range catalog.Entries {
		for _, anchor := range entry.Anchors {
			if anchor.EntryID != entry.ID {
				t.Fatalf("anchor %s entry mismatch: %s", anchor.Key, anchor.EntryID)
			}
			key := anchorRefKey(anchor.EntryID, anchor.Key)
			if seenAnchor[key] {
				t.Fatalf("duplicate anchor %s", key)
			}
			seenAnchor[key] = true
			if previous, ok := seenLegacy[anchor.LegacyID]; ok {
				t.Fatalf("legacy template %s mapped twice: %s and %s", anchor.LegacyID, previous, key)
			}
			seenLegacy[anchor.LegacyID] = key
			if anchor.Required {
				requiredAnchors++
			}
			if anchor.Dynamic {
				dynamicAnchors++
			}
		}
	}
	if len(seenLegacy) != len(builtinTemplates) {
		t.Fatalf("anchors cover %d templates, want %d", len(seenLegacy), len(builtinTemplates))
	}
	if requiredAnchors != 12 {
		t.Fatalf("required anchors=%d, want 12", requiredAnchors)
	}
	if dynamicAnchors != 7 {
		t.Fatalf("dynamic anchors=%d, want 7 generated context fragments", dynamicAnchors)
	}
	for _, builtin := range builtinTemplates {
		key, ok := seenLegacy[builtin.ID]
		if !ok {
			t.Fatalf("builtin template %s has no anchor", builtin.ID)
		}
		anchor := anchorByKey(t, catalog, key)
		if anchor.FrozenVersion != builtin.Version {
			t.Fatalf("%s frozen at v%d but builtin is v%d; update the phase 3 mapping deliberately", builtin.ID, anchor.FrozenVersion, builtin.Version)
		}
		if anchor.Name != builtin.Name || anchor.Required != builtin.Required {
			t.Fatalf("%s metadata drifted from builtin: %#v", builtin.ID, anchor)
		}
		if !builtin.Editable && anchor.Editable {
			t.Fatalf("%s is read-only in the legacy path but editable in the entry model", builtin.ID)
		}
		if strings.HasPrefix(builtin.ID, "context.") && (!anchor.Dynamic || anchor.Editable) {
			t.Fatalf("%s must be a non-editable generated anchor: %#v", builtin.ID, anchor)
		}
	}
	for _, id := range []string{"protocol.attachment-delivery", "resource.agent-api", "resource.attachment-protocol", "resource.group-permissions"} {
		anchor := anchorByLegacy(t, catalog, id)
		if anchor.Editable {
			t.Fatalf("%s must stay editable=false in the entry model", id)
		}
	}
}

func anchorByKey(t *testing.T, catalog EntryCatalog, key string) EntryAnchor {
	t.Helper()
	for _, entry := range catalog.Entries {
		for _, anchor := range entry.Anchors {
			if anchorRefKey(entry.ID, anchor.Key) == key {
				return anchor
			}
		}
	}
	t.Fatalf("anchor %s missing", key)
	return EntryAnchor{}
}

func anchorByLegacy(t *testing.T, catalog EntryCatalog, legacyID string) EntryAnchor {
	t.Helper()
	for _, entry := range catalog.Entries {
		for _, anchor := range entry.Anchors {
			if anchor.LegacyID == legacyID {
				return anchor
			}
		}
	}
	t.Fatalf("legacy template %s has no anchor", legacyID)
	return EntryAnchor{}
}

func TestLegacyTemplateIDsResolveToEntryAnchors(t *testing.T) {
	t.Parallel()
	for _, builtin := range builtinTemplates {
		ref, ok := ResolveLegacyTemplateID(builtin.ID)
		if !ok {
			t.Fatalf("legacy template %s does not resolve", builtin.ID)
		}
		if ref.LegacyID != builtin.ID || ref.EntryID == "" || ref.Key == "" {
			t.Fatalf("legacy template %s resolved to %#v", builtin.ID, ref)
		}
		back, ok := LookupEntryAnchor(ref.EntryID, ref.Key)
		if !ok || back != ref {
			t.Fatalf("anchor %s did not resolve back: %#v ok=%t", anchorRefKey(ref.EntryID, ref.Key), back, ok)
		}
	}
	if _, ok := ResolveLegacyTemplateID("role.observer"); ok {
		t.Fatal("unknown legacy ID resolved")
	}
	if _, ok := ResolveLegacyTemplateID("core.main"); ok {
		t.Fatal("entry-style ID resolved as a legacy alias")
	}
	refs := LegacyAnchors()
	if len(refs) != len(builtinTemplates) {
		t.Fatalf("legacy alias count=%d, want %d", len(refs), len(builtinTemplates))
	}
}

func TestGeneratedAnchorsArePreviewableButNotEditable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := &fixturePromptRepository{}
	engine := NewEngine(repository)
	preview, err := engine.PreviewEntryAnchor(ctx, EntryTaskContext, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Dynamic || preview.Editable {
		t.Fatalf("generated anchor metadata=%#v", preview)
	}
	if strings.TrimSpace(preview.Content) == "" || strings.TrimSpace(preview.Rendered) == "" || preview.RenderError != "" {
		t.Fatalf("generated anchor preview failed: %#v", preview)
	}
	if err := engine.UpdateEntryAnchor(ctx, EntryTaskContext, "task", "CUSTOM", time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "generated") {
		t.Fatalf("generated anchor edit error=%v", err)
	}
	if err := engine.UpdateEntryAnchor(ctx, EntryCore, "attachment", "CUSTOM", time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only protocol edit error=%v", err)
	}
	if err := engine.UpdateEntryAnchor(ctx, EntryCore, "resource-agent-api", "CUSTOM", time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only resource edit error=%v", err)
	}
	if err := engine.UpdateEntryAnchor(ctx, EntryCore, "role-main", "CUSTOM MAIN {{.AgentID}}", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if repository.upserts != 1 {
		t.Fatalf("editable anchor upserts=%d, want 1", repository.upserts)
	}
	resident, err := engine.PreviewEntryAnchor(ctx, EntryCore, "role-main", "")
	if err != nil {
		t.Fatal(err)
	}
	if resident.Source != "override" || !strings.Contains(resident.Content, "CUSTOM MAIN") {
		t.Fatalf("resident anchor preview did not apply the override: %#v", resident)
	}
}

func TestMigrationPreviewMapsLegacyOverridesByAnchor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	oldRoleSub := `You are {{.AgentID}}, a focused Sub Agent. Complete only the assignment and messages routed to your Inbox, report concrete results and verification, and leave integration to Main.

Do not request additional agents, communicate directly with other Sub Agents, broaden the assignment, or redo work already settled by Main. Stop after the assigned evidence or change is complete; leave cross-cutting decisions, Task Memory integration, and final delivery to Main.`
	repository := &fixturePromptRepository{overrides: map[string]domain.PromptTemplate{
		"role.main":                    {ID: "role.main", Content: "CUSTOM MAIN {{.AgentID}}", Version: 3, UpdatedAt: now},
		"context.task":                 {ID: "context.task", Content: "CUSTOM TASK", Version: 2, UpdatedAt: now.Add(time.Minute)},
		"protocol.attachment-delivery": {ID: "protocol.attachment-delivery", Content: "CUSTOM ATTACHMENT", Version: 1, UpdatedAt: now},
		"role.sub":                     {ID: "role.sub", Content: oldRoleSub, Version: 2, UpdatedAt: now},
		"retired.prompt":               {ID: "retired.prompt", Content: "OLD", Version: 5, UpdatedAt: now},
	}}
	engine := NewEngine(repository)
	preview, err := engine.PreviewMigration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if repository.upserts != 0 || repository.deletes != 0 {
		t.Fatalf("migration preview wrote to the repository: upserts=%d deletes=%d", repository.upserts, repository.deletes)
	}
	if len(preview.Mappings) != len(builtinTemplates) {
		t.Fatalf("mappings=%d, want %d", len(preview.Mappings), len(builtinTemplates))
	}
	for _, mapping := range preview.Mappings {
		if mapping.FrozenVersion != mapping.BuiltinVersion {
			t.Fatalf("%s frozen v%d vs builtin v%d", mapping.LegacyID, mapping.FrozenVersion, mapping.BuiltinVersion)
		}
	}
	if len(preview.Merged) != 1 || preview.Merged[0].EntryID != EntryCore || preview.Merged[0].AnchorKey != "role-main" {
		t.Fatalf("merged=%#v", preview.Merged)
	}
	if preview.Merged[0].LegacyIDs[0] != "role.main" || preview.Merged[0].Version != 3 || preview.Merged[0].Content != "CUSTOM MAIN {{.AgentID}}" {
		t.Fatalf("merged role-main=%#v", preview.Merged[0])
	}
	conflicts := map[string]string{}
	for _, conflict := range preview.Conflicts {
		conflicts[conflict.LegacyID] = conflict.Code
	}
	if conflicts["context.task"] != "dynamic_anchor" {
		t.Fatalf("context.task conflict=%q", conflicts["context.task"])
	}
	if conflicts["protocol.attachment-delivery"] != "read_only_anchor" {
		t.Fatalf("attachment conflict=%q", conflicts["protocol.attachment-delivery"])
	}
	if len(preview.Orphans) != 1 || preview.Orphans[0].LegacyID != "retired.prompt" {
		t.Fatalf("orphans=%#v", preview.Orphans)
	}
	if len(preview.Ignored) != 1 || preview.Ignored[0].LegacyID != "role.sub" || preview.Ignored[0].Code != "superseded_builtin" {
		t.Fatalf("ignored=%#v", preview.Ignored)
	}
	accounted := len(preview.Conflicts) + len(preview.Orphans) + len(preview.Ignored)
	for _, merged := range preview.Merged {
		accounted += len(merged.LegacyIDs)
	}
	if accounted != len(repository.overrides) {
		t.Fatalf("accounted %d overrides, want %d; preview must not silently drop any", accounted, len(repository.overrides))
	}
}

func TestMigrationPreviewDoesNotWriteDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	if err := engine.UpdateTemplate(ctx, "role.main", "CUSTOM MAIN", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	before, err := database.PromptTemplateOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := engine.PreviewMigration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, err := database.PromptTemplateOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("migration preview changed overrides: before=%#v after=%#v", before, after)
	}
	if len(preview.Merged) != 1 || preview.Merged[0].Content != "CUSTOM MAIN" {
		t.Fatalf("merged=%#v", preview.Merged)
	}
}

func equivalenceScenarios(t *testing.T) []struct {
	name   string
	input  BuildInput
	expect []string
	absent []string
} {
	t.Helper()
	base := func() BuildInput {
		return BuildInput{
			Project:   domain.Project{Name: "Project"},
			Workspace: domain.Workspace{Name: "Workspace", RootPath: "/repo", Transport: "native"},
			Task: domain.Task{
				ID: "task-1", Code: "task-001", Title: "Prompt", OriginalRequest: "request",
				CurrentGoal: "ship", TaskWorkspacePath: "/repo/.worktree", CollaborationMode: "auto", MaxAgents: 3,
			},
			Agent:       domain.TaskAgent{AgentID: "main", Role: "main"},
			Snapshot:    domain.RuntimeConfigSnapshot{Backend: "codex"},
			AgentAPIURL: "http://127.0.0.1:8766",
			UserMessage: "fixed inbox",
		}
	}
	return []struct {
		name   string
		input  BuildInput
		expect []string
		absent []string
	}{
		{
			name: "code task auto knowledge on",
			input: func() BuildInput {
				item := base()
				item.KnowledgeEnabled = true
				return item
			}(),
			expect: []string{"## Where instructions come from", "You are an AHA2 Task Agent", "You are this Task's Main", "AHA Web: the Owner reads", "AHA auto collaboration", "## Knowledge base", "## Task and workspace", "## Working with AHA", "Attachments: they require"},
		},
		{
			name: "code task zh-CN",
			input: func() BuildInput {
				item := base()
				item.Language = domain.LanguageSimplified
				return item
			}(),
			expect: []string{"## 指令从哪里来", "你是本 Task 的 Main", "AHA 自动协作"},
		},
		{
			name: "code task single knowledge off",
			input: func() BuildInput {
				item := base()
				item.Task.CollaborationMode = "single"
				return item
			}(),
			expect: []string{"single-agent mode", "## Task and workspace"},
			absent: []string{"AHA auto collaboration", "## Knowledge base"},
		},
		{
			name: "pet task identity",
			input: func() BuildInput {
				item := base()
				item.Identity = "pet-task"
				item.IdentitySupplement = "PET DIGEST"
				item.Task.CollaborationMode = "single"
				return item
			}(),
			expect: []string{"You are AHA2's pet", "PET DIGEST"},
		},
		{
			name: "channel assistant",
			input: func() BuildInput {
				item := base()
				item.ChannelContext = map[string]any{"endpoint": domain.ChannelEndpointAssistantDM}
				return item
			}(),
			expect: []string{"private AHA channel assistant", "## External channel"},
			absent: []string{"AHA Web: the Owner reads"},
		},
		{
			name: "group digital human",
			input: func() BuildInput {
				item := base()
				item.ChannelContext = map[string]any{"endpoint": domain.ChannelEndpointGroupDigitalHuman}
				return item
			}(),
			expect: []string{"public group digital human", "## External channel"},
		},
		{
			name: "group task route keeps task identity",
			input: func() BuildInput {
				item := base()
				item.ChannelContext = map[string]any{
					"endpoint": domain.ChannelEndpointGroupDigitalHuman,
					"route":    map[string]any{"mode": "task_route"},
				}
				return item
			}(),
			expect: []string{"You are an AHA2 Task Agent", "## External channel"},
			absent: []string{"public group digital human"},
		},
		{
			name: "sub agent",
			input: func() BuildInput {
				item := base()
				item.Agent = domain.TaskAgent{AgentID: "sub-001", Role: "sub"}
				return item
			}(),
			expect: []string{"a Sub Agent of this Task"},
			absent: []string{"You are this Task's Main", "AHA auto collaboration", "single-agent mode"},
		},
		{
			name: "compact handoff",
			input: func() BuildInput {
				item := base()
				item.Handoff = "previous session summary"
				return item
			}(),
			expect: []string{"## Compact Handoff", "was compacted by AHA"},
		},
		{
			name: "hardware context",
			input: func() BuildInput {
				item := base()
				item.Hardware = []domain.HardwareGroup{{
					ID: "board", Description: "Main board", Mode: domain.HardwareModeBoth,
					Serial: domain.HardwareSerialConfig{Device: "COM3", Baudrate: 115200}, Access: domain.HardwareAccessReadOnly,
				}}
				return item
			}(),
			expect: []string{"## Where instructions come from"},
		},
		{
			name: "mail context",
			input: func() BuildInput {
				item := base()
				item.MailAccounts = []domain.MailAccount{{ID: "mail-1", Address: "agent@example.com", DisplayName: "Agent", Status: domain.MailAccountOK}}
				return item
			}(),
			expect: []string{"## Where instructions come from"},
		},
	}
}

func TestProductionBuildRendersTheSamePromptAsLegacyBuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := NewEngine(&fixturePromptRepository{})
	for _, scenario := range equivalenceScenarios(t) {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			legacy, err := engine.BuildLegacy(ctx, scenario.input)
			if err != nil {
				t.Fatal(err)
			}
			production, err := engine.Build(ctx, scenario.input)
			if err != nil {
				t.Fatal(err)
			}
			if legacy.EffectivePrompt != production.EffectivePrompt {
				t.Fatalf("effective prompt differs\nlegacy:\n%s\nproduction:\n%s", legacy.EffectivePrompt, production.EffectivePrompt)
			}
			if legacy.ContextRoot != production.ContextRoot || legacy.SharedRoot != production.SharedRoot {
				t.Fatalf("context roots differ: legacy=%q/%q production=%q/%q", legacy.ContextRoot, legacy.SharedRoot, production.ContextRoot, production.SharedRoot)
			}
			if !reflect.DeepEqual(legacy.ContextManifest, production.ContextManifest) {
				t.Fatalf("context manifest differs\nlegacy=%#v\nproduction=%#v", legacy.ContextManifest, production.ContextManifest)
			}
			if !reflect.DeepEqual(legacy.SharedManifest, production.SharedManifest) {
				t.Fatalf("shared manifest differs")
			}
			for _, expected := range scenario.expect {
				if !strings.Contains(production.EffectivePrompt, expected) {
					t.Fatalf("prompt missing %q: %s", expected, production.EffectivePrompt)
				}
			}
			for _, absent := range scenario.absent {
				if strings.Contains(production.EffectivePrompt, absent) {
					t.Fatalf("prompt unexpectedly contains %q", absent)
				}
			}
		})
	}
}

func TestProductionBuildResolvesLegacyIDOverridesByAnchorWithoutWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	input := equivalenceScenarios(t)[0].input
	now := time.Date(2026, 11, 19, 15, 0, 0, 0, time.UTC)
	repository := &fixturePromptRepository{overrides: map[string]domain.PromptTemplate{
		"role.main": {
			ID: "role.main", Content: "CUSTOM MAIN {{.AgentID}}", Version: 7, UpdatedAt: now,
		},
		"protocol.attachment-delivery": {
			ID: "protocol.attachment-delivery", Content: "READ-ONLY OVERRIDE MUST NOT RENDER", Version: 1, UpdatedAt: now,
		},
	}}
	engine := NewEngine(repository)
	before, err := repository.PromptTemplateOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}

	ref, ok := ResolveLegacyTemplateID("role.main")
	if !ok || ref.EntryID != EntryCore || ref.Key != "role-main" {
		t.Fatalf("legacy override ID did not resolve to core#role-main: %#v ok=%t", ref, ok)
	}
	production, err := engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := engine.BuildLegacy(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if production.EffectivePrompt != legacy.EffectivePrompt {
		t.Fatalf("override merge changed the production output\nlegacy:\n%s\nproduction:\n%s", legacy.EffectivePrompt, production.EffectivePrompt)
	}
	if !strings.Contains(production.EffectivePrompt, "CUSTOM MAIN main") {
		t.Fatalf("editable legacy-ID override was not merged through its anchor: %s", production.EffectivePrompt)
	}
	if strings.Contains(production.EffectivePrompt, "READ-ONLY OVERRIDE MUST NOT RENDER") {
		t.Fatal("read-only legacy ID accepted an owner override in the production path")
	}
	if repository.upserts != 0 || repository.deletes != 0 {
		t.Fatalf("render path wrote overrides: upserts=%d deletes=%d", repository.upserts, repository.deletes)
	}
	after, err := repository.PromptTemplateOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("render path mutated override data:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestRequiredContentStillRendersThroughTheEntryMap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := NewEngine(&fixturePromptRepository{})
	input := equivalenceScenarios(t)[0].input
	result, err := engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"This prompt is AHA's working agreement with you",
		"You are this Task's Main", "## Working with AHA", "Attachments: they require",
		"## Task and workspace", "## Available context", "## Current Inbox Batch",
	} {
		if !strings.Contains(result.EffectivePrompt, marker) {
			t.Fatalf("required content %q missing from the entry-model prompt", marker)
		}
	}
	resourceIDs := map[string]bool{}
	for _, resource := range append(append([]ContextResource(nil), result.ContextManifest...), result.SharedManifest...) {
		resourceIDs[resource.ID] = true
	}
	for _, id := range []string{"task", "agent-api", "attachment-protocol"} {
		if !resourceIDs[id] {
			t.Fatalf("required generated/shared resource %q missing: %#v", id, resourceIDs)
		}
	}
	subInput := input
	subInput.Agent = domain.TaskAgent{AgentID: "sub-001", Role: "sub"}
	subResult, err := engine.Build(ctx, subInput)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(subResult.EffectivePrompt, "a Sub Agent of this Task") {
		t.Fatal("required Sub Agent role content missing")
	}
	templates, err := engine.TemplatesForLanguage(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := RenderInboxBatchContent(templates, []InboxTemplateItem{{Sequence: 1, SourceKind: "user", SourceAgentID: "owner", Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inbox, "## Inbox 1") {
		t.Fatalf("required inbox batch content missing: %s", inbox)
	}
	if len(LegacyAnchors()) != len(builtinTemplates) {
		t.Fatalf("alias map lost templates")
	}
}
