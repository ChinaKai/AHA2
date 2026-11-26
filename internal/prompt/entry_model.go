package prompt

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

// Phase 3 template convergence starts from two top-level entries, core and
// task-context, while the legacy template IDs stay valid as aliases. This file
// owns the frozen alias map, anchor and migration previews, and the entry-model
// renderer used by the production Build path. BuildLegacy is retained for one
// release as the byte-equivalence baseline.
const (
	EntryCore        = "core"
	EntryTaskContext = "task-context"

	anchorKindResident  = "resident"
	anchorKindFragment  = "fragment"
	anchorKindGenerated = "generated"
	anchorKindResource  = "resource"
)

// EntryAnchorRef is the legacy ID -> entry/anchor alias. It carries no mutable
// state, so it is safe to hand to previews and future Web UI code.
type EntryAnchorRef struct {
	EntryID  string `json:"entry_id"`
	Key      string `json:"key"`
	LegacyID string `json:"legacy_id"`
}

// EntryAnchor is one fragment of an entry. Generated anchors are produced by Go
// code from live Turn data: they can be previewed but never edited. Resource
// anchors are shared read-only references.
type EntryAnchor struct {
	EntryID       string `json:"entry_id"`
	Key           string `json:"key"`
	LegacyID      string `json:"legacy_id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Layer         string `json:"layer"`
	Kind          string `json:"kind"`
	Required      bool   `json:"required"`
	Editable      bool   `json:"editable"`
	Protected     bool   `json:"protected"`
	Dynamic       bool   `json:"dynamic"`
	FrozenVersion int    `json:"frozen_version"`
	Version       int    `json:"version"`
	Source        string `json:"source"`
	Order         int    `json:"order"`
	Heading       bool   `json:"heading"`
	Condition     string `json:"condition,omitempty"`
}

// PromptEntry is one of the two top-level entries.
type PromptEntry struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Anchors     []EntryAnchor `json:"anchors"`
}

// EntryCatalog is the entry-first view of the template registry.
type EntryCatalog struct {
	Entries []PromptEntry `json:"entries"`
}

// entryAnchorSpec is the frozen Phase 3 mapping. FrozenVersion is the builtin
// template version the alias was frozen at; a template bump must update this
// table deliberately so the migration contract stays visible in review.
type entryAnchorSpec struct {
	EntryID       string
	Key           string
	LegacyID      string
	Kind          string
	FrozenVersion int
	Order         int
	Heading       bool
	Condition     string
}

// Order is the resident Prompt order across both entries; entries themselves are
// organizational. Fixed rules render first (identity, role, channel, core
// boundaries, policy, channel, knowledge, Agent API, attachments) and the
// per-Turn sections last, so the Current Inbox Batch is the final thing the
// Agent reads. Resident templates carry their own "## " section headings: the
// template Name is an admin label, not prompt text.
var phaseThreeAnchorSpecs = []entryAnchorSpec{
	{EntryID: EntryCore, Key: "main", LegacyID: "core.default", Kind: anchorKindResident, FrozenVersion: 4, Order: 2, Condition: "always"},
	{EntryID: EntryCore, Key: "identity-task", LegacyID: "identity.task-agent", Kind: anchorKindResident, FrozenVersion: 2, Order: 0, Condition: "identity:task-agent"},
	{EntryID: EntryCore, Key: "identity-channel-assistant", LegacyID: "identity.channel-assistant", Kind: anchorKindResident, FrozenVersion: 2, Order: 0, Condition: "identity:channel-assistant"},
	{EntryID: EntryCore, Key: "identity-channel-digital-human", LegacyID: "identity.channel-digital-human", Kind: anchorKindResident, FrozenVersion: 3, Order: 0, Condition: "identity:channel-digital-human"},
	{EntryID: EntryCore, Key: "identity-pet", LegacyID: "identity.pet-task", Kind: anchorKindResident, FrozenVersion: 14, Order: 0, Condition: "identity:pet-task"},
	{EntryID: EntryCore, Key: "role-main", LegacyID: "role.main", Kind: anchorKindResident, FrozenVersion: 6, Order: 1, Condition: "role:main"},
	{EntryID: EntryCore, Key: "role-sub", LegacyID: "role.sub", Kind: anchorKindResident, FrozenVersion: 2, Order: 1, Condition: "role:sub"},
	{EntryID: EntryCore, Key: "channel-web", LegacyID: "channel.web", Kind: anchorKindResident, FrozenVersion: 2, Order: 4, Condition: "channel:web"},
	{EntryID: EntryCore, Key: "channel-external", LegacyID: "channel.external-channel", Kind: anchorKindResident, FrozenVersion: 8, Order: 4, Condition: "channel:external-channel"},

	{EntryID: EntryTaskContext, Key: "policy-auto", LegacyID: "policy.auto", Kind: anchorKindResident, FrozenVersion: 2, Order: 3, Condition: "policy:auto"},
	{EntryID: EntryTaskContext, Key: "policy-single", LegacyID: "policy.single", Kind: anchorKindResident, FrozenVersion: 2, Order: 3, Condition: "policy:single"},
	{EntryID: EntryTaskContext, Key: "knowledge", LegacyID: "protocol.knowledge", Kind: anchorKindResident, FrozenVersion: 3, Order: 5, Condition: "knowledge"},
	{EntryID: EntryTaskContext, Key: "compact-handoff", LegacyID: "section.compact-handoff", Kind: anchorKindResident, FrozenVersion: 1, Order: 8, Condition: "compact-handoff"},
	{EntryID: EntryTaskContext, Key: "task-workspace", LegacyID: "section.task-workspace", Kind: anchorKindResident, FrozenVersion: 4, Order: 9, Condition: "always"},
	{EntryID: EntryTaskContext, Key: "available-context", LegacyID: "section.available-context", Kind: anchorKindResident, FrozenVersion: 3, Order: 10, Condition: "always"},
	{EntryID: EntryTaskContext, Key: "current-inbox", LegacyID: "section.current-inbox", Kind: anchorKindResident, FrozenVersion: 1, Order: 11, Condition: "always"},

	{EntryID: EntryCore, Key: "agent-api", LegacyID: "protocol.agent-api", Kind: anchorKindResident, FrozenVersion: 5, Order: 6, Condition: "always"},
	{EntryID: EntryCore, Key: "attachment", LegacyID: "protocol.attachment-delivery", Kind: anchorKindResident, FrozenVersion: 2, Order: 7, Condition: "always"},

	{EntryID: EntryTaskContext, Key: "inbox-batch", LegacyID: "section.inbox-batch-content", Kind: anchorKindFragment, FrozenVersion: 4, Order: 20, Heading: true},

	// Generated files: Go code produces them from live Turn data. They stay
	// previewable but are not independently editable in the entry model.
	{EntryID: EntryTaskContext, Key: "task", LegacyID: "context.task", Kind: anchorKindGenerated, FrozenVersion: 1, Order: 100},
	{EntryID: EntryTaskContext, Key: "recent-context", LegacyID: "context.recent-context", Kind: anchorKindGenerated, FrozenVersion: 2, Order: 101},
	{EntryID: EntryTaskContext, Key: "recovery-handoff", LegacyID: "context.recovery-handoff", Kind: anchorKindGenerated, FrozenVersion: 2, Order: 102},
	{EntryID: EntryTaskContext, Key: "turn-diagnostics", LegacyID: "context.turn-diagnostics", Kind: anchorKindGenerated, FrozenVersion: 1, Order: 103},
	{EntryID: EntryTaskContext, Key: "hardware", LegacyID: "context.hardware", Kind: anchorKindGenerated, FrozenVersion: 1, Order: 104},
	{EntryID: EntryTaskContext, Key: "mail", LegacyID: "context.mail", Kind: anchorKindGenerated, FrozenVersion: 1, Order: 105},
	{EntryID: EntryTaskContext, Key: "attachments-index", LegacyID: "context.attachments-index", Kind: anchorKindGenerated, FrozenVersion: 3, Order: 106},

	// Shared read-only references. They are grouped under core because they are
	// global boundaries, not task context.
	{EntryID: EntryCore, Key: "resource-agent-api", LegacyID: "resource.agent-api", Kind: anchorKindResource, FrozenVersion: 18, Order: 200},
	{EntryID: EntryCore, Key: "resource-attachment-protocol", LegacyID: "resource.attachment-protocol", Kind: anchorKindResource, FrozenVersion: 1, Order: 201},
	{EntryID: EntryCore, Key: "resource-group-permissions", LegacyID: "resource.group-permissions", Kind: anchorKindResource, FrozenVersion: 1, Order: 202},
}

var phaseThreeEntryMeta = map[string][2]string{
	EntryCore:        {"AHA Core", "Shared safety, identity, role, channel, API, and attachment boundaries"},
	EntryTaskContext: {"Task Context", "Task policy, workspace, inbox, and generated context files"},
}

var phaseThreeEntryMetaZh = map[string][2]string{
	EntryCore:        {"AHA 核心入口", "安全、身份、角色、渠道、Agent API 与附件边界"},
	EntryTaskContext: {"任务上下文入口", "协作策略、知识协议、Task/Workspace、Inbox 与生成文件"},
}

func entryMeta(entryID, language string) (string, string) {
	if domain.NormalizeLanguage(language) == domain.LanguageSimplified {
		if meta, ok := phaseThreeEntryMetaZh[entryID]; ok {
			return meta[0], meta[1]
		}
	}
	meta := phaseThreeEntryMeta[entryID]
	return meta[0], meta[1]
}

func anchorRefFromSpec(spec entryAnchorSpec) EntryAnchorRef {
	return EntryAnchorRef{EntryID: spec.EntryID, Key: spec.Key, LegacyID: spec.LegacyID}
}

func anchorRefKey(entryID, key string) string {
	return entryID + "#" + key
}

func entryAnchorSpecByLegacyID(id string) (entryAnchorSpec, bool) {
	for _, spec := range phaseThreeAnchorSpecs {
		if spec.LegacyID == id {
			return spec, true
		}
	}
	return entryAnchorSpec{}, false
}

func entryAnchorSpecByKey(entryID, key string) (entryAnchorSpec, bool) {
	for _, spec := range phaseThreeAnchorSpecs {
		if spec.EntryID == entryID && spec.Key == key {
			return spec, true
		}
	}
	return entryAnchorSpec{}, false
}

// ResolveLegacyTemplateID resolves a frozen old ID to its entry anchor. The
// legacy ID itself stays valid for the current write/render path.
func ResolveLegacyTemplateID(id string) (EntryAnchorRef, bool) {
	spec, ok := entryAnchorSpecByLegacyID(id)
	if !ok {
		return EntryAnchorRef{}, false
	}
	return anchorRefFromSpec(spec), true
}

// LookupEntryAnchor resolves an entry#anchor pair back to its legacy ID.
func LookupEntryAnchor(entryID, key string) (EntryAnchorRef, bool) {
	spec, ok := entryAnchorSpecByKey(entryID, key)
	if !ok {
		return EntryAnchorRef{}, false
	}
	return anchorRefFromSpec(spec), true
}

// LegacyAnchors returns a copy of the frozen alias map in declaration order.
func LegacyAnchors() []EntryAnchorRef {
	result := make([]EntryAnchorRef, 0, len(phaseThreeAnchorSpecs))
	for _, spec := range phaseThreeAnchorSpecs {
		result = append(result, anchorRefFromSpec(spec))
	}
	return result
}

func anchorFromSpec(spec entryAnchorSpec, builtin domain.PromptTemplate, template domain.PromptTemplate) EntryAnchor {
	editable := builtin.Editable && spec.Kind != anchorKindGenerated
	anchor := EntryAnchor{
		EntryID:       spec.EntryID,
		Key:           spec.Key,
		LegacyID:      spec.LegacyID,
		Name:          template.Name,
		Description:   template.Description,
		Layer:         template.Layer,
		Kind:          spec.Kind,
		Required:      builtin.Required,
		Editable:      editable,
		Protected:     !editable,
		Dynamic:       spec.Kind == anchorKindGenerated,
		FrozenVersion: spec.FrozenVersion,
		Version:       template.Version,
		Source:        template.Source,
		Order:         spec.Order,
		Heading:       spec.Heading,
		Condition:     spec.Condition,
	}
	return anchor
}

// EntryCatalog returns the two-entry catalog with anchor metadata. Dynamic
// fragments are marked non-editable; their templates are still visible so the
// future UI can preview them.
func (engine *Engine) EntryCatalog(ctx context.Context, language string) (EntryCatalog, error) {
	templates, err := engine.TemplatesForLanguage(ctx, language)
	if err != nil {
		return EntryCatalog{}, err
	}
	byID := make(map[string]domain.PromptTemplate, len(templates))
	for _, item := range templates {
		byID[item.ID] = item
	}
	coreName, coreDescription := entryMeta(EntryCore, language)
	taskContextName, taskContextDescription := entryMeta(EntryTaskContext, language)
	entries := []PromptEntry{
		{ID: EntryCore, Name: coreName, Description: coreDescription},
		{ID: EntryTaskContext, Name: taskContextName, Description: taskContextDescription},
	}
	index := map[string]int{EntryCore: 0, EntryTaskContext: 1}
	for _, spec := range phaseThreeAnchorSpecs {
		builtin, ok := builtinTemplate(spec.LegacyID)
		if !ok {
			return EntryCatalog{}, fmt.Errorf("entry anchor %s references unknown template %s", anchorRefKey(spec.EntryID, spec.Key), spec.LegacyID)
		}
		template, ok := byID[spec.LegacyID]
		if !ok {
			return EntryCatalog{}, fmt.Errorf("entry anchor %s template %s was not loaded", anchorRefKey(spec.EntryID, spec.Key), spec.LegacyID)
		}
		position, ok := index[spec.EntryID]
		if !ok {
			return EntryCatalog{}, fmt.Errorf("entry anchor %s references unknown entry %s", anchorRefKey(spec.EntryID, spec.Key), spec.EntryID)
		}
		entries[position].Anchors = append(entries[position].Anchors, anchorFromSpec(spec, builtin, template))
	}
	return EntryCatalog{Entries: entries}, nil
}

// AnchorPreview is read-only data for the future template admin UI.
type AnchorPreview struct {
	EntryID       string `json:"entry_id"`
	Key           string `json:"key"`
	LegacyID      string `json:"legacy_id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Layer         string `json:"layer"`
	Kind          string `json:"kind"`
	Required      bool   `json:"required"`
	Editable      bool   `json:"editable"`
	Protected     bool   `json:"protected"`
	Dynamic       bool   `json:"dynamic"`
	FrozenVersion int    `json:"frozen_version"`
	Version       int    `json:"version"`
	Source        string `json:"source"`
	Content       string `json:"content"`
	Rendered      string `json:"rendered,omitempty"`
	RenderError   string `json:"render_error,omitempty"`
	// LegacyOverride marks an existing old-path override for a generated
	// anchor. The entry model does not apply it; the migration preview reports
	// it as a conflict instead of silently dropping or overwriting it.
	LegacyOverride        bool   `json:"legacy_override"`
	LegacyContent         string `json:"legacy_content,omitempty"`
	LegacyOverrideVersion int    `json:"legacy_override_version,omitempty"`
}

// PreviewEntryAnchor renders one anchor with sample data. Generated anchors
// show the code-owned builtin body; an old override of that same ID is surfaced
// separately rather than applied.
func (engine *Engine) PreviewEntryAnchor(ctx context.Context, entryID, key, language string) (AnchorPreview, error) {
	spec, ok := entryAnchorSpecByKey(entryID, key)
	if !ok {
		return AnchorPreview{}, fmt.Errorf("prompt entry anchor %s not found", anchorRefKey(entryID, key))
	}
	templates, err := engine.TemplatesForLanguage(ctx, language)
	if err != nil {
		return AnchorPreview{}, err
	}
	var resolved domain.PromptTemplate
	found := false
	for _, item := range templates {
		if item.ID == spec.LegacyID {
			resolved = item
			found = true
			break
		}
	}
	if !found {
		return AnchorPreview{}, fmt.Errorf("prompt template %s not found", spec.LegacyID)
	}
	builtin, _ := builtinTemplate(spec.LegacyID)
	anchor := anchorFromSpec(spec, builtin, resolved)
	preview := AnchorPreview{
		EntryID: entryID, Key: key, LegacyID: spec.LegacyID, Name: anchor.Name,
		Description: anchor.Description, Layer: anchor.Layer, Kind: spec.Kind, Required: anchor.Required,
		Editable: anchor.Editable, Protected: anchor.Protected, Dynamic: anchor.Dynamic,
		FrozenVersion: spec.FrozenVersion, Version: resolved.Version, Source: resolved.Source,
		Content: resolved.Content,
	}
	if spec.Kind == anchorKindGenerated {
		// Code owns generated files. Resolve the builtin body regardless of any
		// stale legacy override, and report that override without applying it.
		preview.Content = strings.TrimSpace(builtinBody(spec.LegacyID, language))
		preview.Version = builtin.Version
		preview.Source = "builtin"
		overrides, err := engine.repository.PromptTemplateOverrides(ctx)
		if err != nil {
			return AnchorPreview{}, err
		}
		if item, ok := overrides[spec.LegacyID]; ok {
			preview.LegacyOverride = true
			preview.LegacyContent = item.Content
			preview.LegacyOverrideVersion = item.Version
		}
	}
	rendered, renderErr := renderTemplate(spec.LegacyID, preview.Content, sampleTemplateData())
	if renderErr != nil {
		preview.RenderError = renderErr.Error()
	} else {
		preview.Rendered = rendered
	}
	return preview, nil
}

func builtinBody(id, language string) string {
	name := strings.ReplaceAll(id, ".", "-") + ".md"
	path := "templates/" + name
	if domain.NormalizeLanguage(language) == domain.LanguageSimplified {
		if _, err := templateFiles.ReadFile("templates/zh-CN/" + name); err == nil {
			path = "templates/zh-CN/" + name
		}
	}
	data, err := templateFiles.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func sampleTemplateData() templateData {
	return templateData{
		AgentID: "main", AgentRole: "main", Identity: identityTaskAgent, Channel: channelWeb,
		Backend: "codex", Collaboration: "single", MaxAgents: 1,
		TaskID: "task-sample", TaskCode: "task-001", TaskTitle: "Sample task",
		ContextRoot:   "/workspace/.aha2-context/task-sample/main",
		ScratchRoot:   "/workspace/.aha2-context/task-sample/scratch/main",
		TaskWorkspace: "/workspace", Workspace: "Sample workspace", WorkspaceTransport: "native",
		ProjectName: "Sample project", OriginalRequest: "Sample request", CurrentGoal: "Sample goal",
		TaskBranch: "aha/task-sample", InboxBatch: "Sample inbox", AgentAPIURL: "http://127.0.0.1:8766",
	}
}

// UpdateEntryAnchor is the entry-first write gate. It refuses generated and
// read-only anchors and otherwise delegates to the existing override storage, so
// this phase adds no new persistence behavior.
func (engine *Engine) UpdateEntryAnchor(ctx context.Context, entryID, key, content string, now time.Time) error {
	spec, ok := entryAnchorSpecByKey(entryID, key)
	if !ok {
		return fmt.Errorf("prompt entry anchor %s not found", anchorRefKey(entryID, key))
	}
	builtin, _ := builtinTemplate(spec.LegacyID)
	if spec.Kind == anchorKindGenerated {
		return fmt.Errorf("prompt entry anchor %s is generated and cannot be edited", anchorRefKey(entryID, key))
	}
	if !builtin.Editable {
		return fmt.Errorf("prompt entry anchor %s is read-only", anchorRefKey(entryID, key))
	}
	return engine.UpdateTemplate(ctx, spec.LegacyID, content, now)
}

// entryTemplateOrder returns the resident legacy IDs for one Turn in Prompt
// order. It mirrors the selection rules of residentTemplateIDs without using
// that list, so the equivalence test can catch an anchor mapping or ordering
// mistake instead of comparing a map to itself.
func entryTemplateOrder(input BuildInput) []string {
	identityName, channelName := identityAndChannel(input.ChannelContext)
	if strings.TrimSpace(input.Identity) != "" {
		identityName = strings.TrimSpace(input.Identity)
	}
	specs := append([]entryAnchorSpec(nil), phaseThreeAnchorSpecs...)
	sort.SliceStable(specs, func(left, right int) bool { return specs[left].Order < specs[right].Order })
	ids := make([]string, 0, len(specs))
	for _, spec := range specs {
		if spec.Kind != anchorKindResident {
			continue
		}
		if !anchorConditionMatches(spec.Condition, input, identityName, channelName) {
			continue
		}
		ids = append(ids, spec.LegacyID)
	}
	return ids
}

func anchorConditionMatches(condition string, input BuildInput, identityName, channelName string) bool {
	switch {
	case condition == "always":
		return true
	case strings.HasPrefix(condition, "identity:"):
		return identityName == strings.TrimPrefix(condition, "identity:")
	case strings.HasPrefix(condition, "channel:"):
		return channelName == strings.TrimPrefix(condition, "channel:")
	case condition == "role:main":
		return input.Agent.Role != "sub"
	case condition == "role:sub":
		return input.Agent.Role == "sub"
	case condition == "policy:auto":
		return input.Agent.Role != "sub" && input.Task.CollaborationMode != "single"
	case condition == "policy:single":
		return input.Agent.Role != "sub" && input.Task.CollaborationMode == "single"
	case condition == "knowledge":
		return input.KnowledgeEnabled
	case condition == "compact-handoff":
		return strings.TrimSpace(input.Handoff) != ""
	}
	return false
}

// entryRenderedFragment is one resident fragment exactly as Build appends it.
// It exists so read-only structure tools can inspect and reorder the same
// fragments the production renderer emits, without parsing the final prompt or
// growing a second rendering path. Rendered is the text appended to the prompt
// (including the "## <Name>" heading when the anchor has one); Content is the
// rendered body without that heading.
type entryRenderedFragment struct {
	LegacyID string
	Name     string
	Heading  bool
	Content  string
	Rendered string
}

// BuildEntryModel renders the resident Prompt by resolving and ordering every
// fragment through the entry model. Build delegates here in production;
// BuildLegacy remains the pre-entry-model reference used by the equivalence
// tests for one release.
func (engine *Engine) BuildEntryModel(ctx context.Context, input BuildInput) (BuildResult, error) {
	result, _, err := engine.buildEntryModel(ctx, input)
	return result, err
}

// buildEntryModel returns both the production result and the ordered resident
// fragments. Joining the fragments with a blank line is exactly how the
// EffectivePrompt is produced, so callers can reorder them for a structural
// preview without changing any fragment text.
func (engine *Engine) buildEntryModel(ctx context.Context, input BuildInput) (BuildResult, []entryRenderedFragment, error) {
	templates, err := engine.TemplatesForLanguage(ctx, input.Language)
	if err != nil {
		return BuildResult{}, nil, err
	}
	templateByID := make(map[string]domain.PromptTemplate, len(templates))
	for _, item := range templates {
		if _, ok := ResolveLegacyTemplateID(item.ID); !ok {
			return BuildResult{}, nil, fmt.Errorf("prompt template %s has no entry anchor", item.ID)
		}
		templateByID[item.ID] = item
	}
	workDir := input.Task.TaskWorkspacePath
	if workDir == "" {
		workDir = input.Workspace.RootPath
	}
	contextRoot := contextRootFor(input, workDir)
	scratchRoot := ""
	if strings.TrimSpace(workDir) != "" {
		scratchRoot, err = workspace.AgentScratchDir(input.Workspace, workDir, input.Task.ID, safeAgentID(input.Agent.AgentID))
		if err != nil {
			return BuildResult{}, nil, err
		}
	}
	identityName, channelName := identityAndChannel(input.ChannelContext)
	if strings.TrimSpace(input.Identity) != "" {
		identityName = strings.TrimSpace(input.Identity)
	}
	// The legacy renderer failed when an identity/channel template was not
	// registered. Keep that boundary explicit instead of silently dropping the
	// fragment when no anchor condition matches.
	for _, id := range []string{"identity." + identityName, "channel." + channelName} {
		if _, ok := templateByID[id]; !ok {
			return BuildResult{}, nil, fmt.Errorf("prompt template %s is not registered", id)
		}
	}
	data := templateData{
		AgentID: input.Agent.AgentID, AgentRole: input.Agent.Role, Identity: identityName,
		Channel: channelName, ChannelGroup: channelIsGroup(input.ChannelContext),
		ChannelTaskRoute: channelRouteMode(input.ChannelContext) == "task_route",
		Backend:          input.Snapshot.Backend, Collaboration: input.Task.CollaborationMode,
		MaxAgents: input.Task.MaxAgents, TaskID: input.Task.ID, TaskCode: input.Task.Code,
		TaskTitle: input.Task.Title, ContextRoot: contextRoot, ScratchRoot: scratchRoot, TaskWorkspace: workDir,
		Workspace: input.Workspace.Name, WorkspaceTransport: input.Workspace.Transport,
		ProjectName: input.Project.Name, OriginalRequest: input.Task.OriginalRequest,
		CurrentGoal: input.Task.CurrentGoal, CurrentGoalSummary: truncate(input.Task.CurrentGoal, 800),
		TaskBranch: input.Task.TaskBranch, InboxBatch: strings.TrimSpace(input.UserMessage),
		CompactHandoff: strings.TrimSpace(input.Handoff), AgentAPIURL: strings.TrimRight(input.AgentAPIURL, "/"),
	}
	applyMemoryCard(&data, input)
	if input.IncludeRecoveryHandoff {
		if evidence := recoveryHandoffEvidenceFor(input); evidence != nil {
			recoveryData := data
			recoveryData.Recovery = evidence
			recovery, renderErr := renderTemplateByID(templateByID, "context.recovery-handoff", recoveryData)
			if renderErr != nil {
				return BuildResult{}, nil, renderErr
			}
			data.RecoveryHandoff = recovery
		}
	}
	contextResources, err := buildResources(input, contextRoot, templateByID, data)
	if err != nil {
		return BuildResult{}, nil, err
	}
	sharedRoot, sharedManifest, err := buildSharedSnapshot(input, workDir, templateByID, data)
	if err != nil {
		return BuildResult{}, nil, err
	}
	resources := append(append([]ContextResource(nil), contextResources...), sharedManifest...)
	data.AvailableContext = availableContextEntries(resources)
	var parts []string
	fragments := make([]entryRenderedFragment, 0, len(phaseThreeAnchorSpecs))
	directiveAdded := false
	appendDirective := func() {
		directiveAdded = true
		if directive := languageDirective(input.Language); directive != "" {
			parts = append(parts, directive)
			fragments = append(fragments, entryRenderedFragment{
				LegacyID: "language.directive", Name: "Response language", Content: directive, Rendered: directive,
			})
		}
	}
	for _, id := range entryTemplateOrder(input) {
		anchor, ok := ResolveLegacyTemplateID(id)
		if !ok {
			return BuildResult{}, nil, fmt.Errorf("resident template %s has no entry anchor", id)
		}
		spec, _ := entryAnchorSpecByKey(anchor.EntryID, anchor.Key)
		content, renderErr := renderTemplateByID(templateByID, id, data)
		if renderErr != nil {
			return BuildResult{}, nil, renderErr
		}
		if id == "identity."+identityName {
			if supplement := strings.TrimSpace(input.IdentitySupplement); supplement != "" {
				content = strings.TrimRight(content, "\n") + "\n\n" + supplement
			}
		}
		// The response-language directive closes the fixed rules, ahead of the
		// per-Turn sections.
		if !directiveAdded && strings.HasPrefix(id, "section.") {
			appendDirective()
		}
		rendered := content
		if spec.Heading {
			rendered = "## " + templateByID[id].Name + "\n" + content
		}
		parts = append(parts, rendered)
		fragments = append(fragments, entryRenderedFragment{
			LegacyID: id, Name: templateByID[id].Name, Heading: spec.Heading, Content: content, Rendered: rendered,
		})
	}
	if !directiveAdded {
		appendDirective()
	}
	effective := strings.Join(parts, "\n\n")
	return BuildResult{
		EffectivePrompt: effective, ContextRoot: contextRoot, ContextManifest: contextResources,
		SharedRoot: sharedRoot, SharedManifest: sharedManifest,
	}, fragments, nil
}

// Migration preview data. The preview is computed from the existing override
// map and never writes: it reports what would carry to an anchor, what is an
// orphan, what conflicts with the entry model, and what the legacy path already
// ignores.
type LegacyMapping struct {
	LegacyID       string `json:"legacy_id"`
	EntryID        string `json:"entry_id"`
	AnchorKey      string `json:"anchor_key"`
	Kind           string `json:"kind"`
	Dynamic        bool   `json:"dynamic"`
	Editable       bool   `json:"editable"`
	Required       bool   `json:"required"`
	FrozenVersion  int    `json:"frozen_version"`
	BuiltinVersion int    `json:"builtin_version"`
}

type MergedAnchorOverride struct {
	EntryID     string    `json:"entry_id"`
	AnchorKey   string    `json:"anchor_key"`
	LegacyIDs   []string  `json:"legacy_ids"`
	Version     int       `json:"version"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	ContentHash string    `json:"content_hash"`
	Content     string    `json:"content"`
}

type OrphanOverride struct {
	LegacyID  string    `json:"legacy_id"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type OverrideConflict struct {
	LegacyID  string `json:"legacy_id"`
	EntryID   string `json:"entry_id"`
	AnchorKey string `json:"anchor_key"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Version   int    `json:"version"`
}

type IgnoredOverride struct {
	LegacyID  string `json:"legacy_id"`
	EntryID   string `json:"entry_id"`
	AnchorKey string `json:"anchor_key"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Version   int    `json:"version"`
}

type MigrationPreview struct {
	Mappings  []LegacyMapping        `json:"mappings"`
	Merged    []MergedAnchorOverride `json:"merged"`
	Orphans   []OrphanOverride       `json:"orphans"`
	Conflicts []OverrideConflict     `json:"conflicts"`
	Ignored   []IgnoredOverride      `json:"ignored"`
}

// PreviewMigration reads the override table and maps it onto entry anchors. It
// is read-only by construction: the Repository interface exposes writes, but
// this method only calls PromptTemplateOverrides.
func (engine *Engine) PreviewMigration(ctx context.Context) (MigrationPreview, error) {
	overrides, err := engine.repository.PromptTemplateOverrides(ctx)
	if err != nil {
		return MigrationPreview{}, err
	}
	return buildMigrationPreview(overrides), nil
}

func buildMigrationPreview(overrides map[string]domain.PromptTemplate) MigrationPreview {
	preview := MigrationPreview{
		Mappings:  make([]LegacyMapping, 0, len(phaseThreeAnchorSpecs)),
		Merged:    []MergedAnchorOverride{},
		Orphans:   []OrphanOverride{},
		Conflicts: []OverrideConflict{},
		Ignored:   []IgnoredOverride{},
	}
	for _, spec := range phaseThreeAnchorSpecs {
		builtin, _ := builtinTemplate(spec.LegacyID)
		preview.Mappings = append(preview.Mappings, LegacyMapping{
			LegacyID: spec.LegacyID, EntryID: spec.EntryID, AnchorKey: spec.Key, Kind: spec.Kind,
			Dynamic: spec.Kind == anchorKindGenerated, Editable: builtin.Editable && spec.Kind != anchorKindGenerated,
			Required: builtin.Required, FrozenVersion: spec.FrozenVersion, BuiltinVersion: builtin.Version,
		})
	}
	ids := make([]string, 0, len(overrides))
	for id := range overrides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	carried := map[string][]domain.PromptTemplate{}
	for _, id := range ids {
		override := overrides[id]
		spec, ok := entryAnchorSpecByLegacyID(id)
		if !ok {
			preview.Orphans = append(preview.Orphans, OrphanOverride{LegacyID: id, Version: override.Version, UpdatedAt: override.UpdatedAt})
			continue
		}
		builtin, _ := builtinTemplate(id)
		switch {
		case isSupersededBuiltinOverride(id, override.Content):
			preview.Ignored = append(preview.Ignored, IgnoredOverride{
				LegacyID: id, EntryID: spec.EntryID, AnchorKey: spec.Key, Code: "superseded_builtin",
				Message: "matches a superseded builtin revision the legacy path already ignores", Version: override.Version,
			})
		case spec.Kind == anchorKindGenerated:
			preview.Conflicts = append(preview.Conflicts, OverrideConflict{
				LegacyID: id, EntryID: spec.EntryID, AnchorKey: spec.Key, Code: "dynamic_anchor",
				Message: "generated fragments are produced by Go code and cannot be carried into an editable anchor", Version: override.Version,
			})
		case !builtin.Editable:
			preview.Conflicts = append(preview.Conflicts, OverrideConflict{
				LegacyID: id, EntryID: spec.EntryID, AnchorKey: spec.Key, Code: "read_only_anchor",
				Message: "anchor is read-only in both the legacy path and the entry model", Version: override.Version,
			})
		default:
			carried[anchorRefKey(spec.EntryID, spec.Key)] = append(carried[anchorRefKey(spec.EntryID, spec.Key)], override)
		}
	}
	keys := make([]string, 0, len(carried))
	for key := range carried {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		items := carried[key]
		spec, _ := entryAnchorSpecByKey(splitAnchorKey(key))
		first := items[0]
		conflict := false
		for _, item := range items[1:] {
			if strings.TrimSpace(item.Content) != strings.TrimSpace(first.Content) {
				conflict = true
				preview.Conflicts = append(preview.Conflicts, OverrideConflict{
					LegacyID: item.ID, EntryID: spec.EntryID, AnchorKey: spec.Key, Code: "duplicate_anchor",
					Message: "another legacy override already maps to this anchor with different content", Version: item.Version,
				})
			}
		}
		if conflict {
			continue
		}
		latest := first
		legacyIDs := make([]string, 0, len(items))
		for _, item := range items {
			legacyIDs = append(legacyIDs, item.ID)
			if item.UpdatedAt.After(latest.UpdatedAt) {
				latest = item
			}
		}
		sort.Strings(legacyIDs)
		preview.Merged = append(preview.Merged, MergedAnchorOverride{
			EntryID: spec.EntryID, AnchorKey: spec.Key, LegacyIDs: legacyIDs,
			Version: latest.Version, UpdatedAt: latest.UpdatedAt,
			ContentHash: contentHash(latest.Content), Content: latest.Content,
		})
	}
	return preview
}

func splitAnchorKey(key string) (string, string) {
	entryID, anchorKey, _ := strings.Cut(key, "#")
	return entryID, anchorKey
}

func contentHash(content string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(content))))
}
