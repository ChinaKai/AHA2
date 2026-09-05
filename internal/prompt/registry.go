package prompt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

//go:embed templates/*.md
var templateFiles embed.FS

type Repository interface {
	PromptTemplateOverrides(context.Context) (map[string]domain.PromptTemplate, error)
	UpsertPromptTemplateOverride(context.Context, string, string, time.Time) error
	DeletePromptTemplateOverride(context.Context, string) error
}

type Engine struct {
	repository Repository
}

type BuildInput struct {
	Project          domain.Project
	Workspace        domain.Workspace
	Task             domain.Task
	Agent            domain.TaskAgent
	Snapshot         domain.RuntimeConfigSnapshot
	Memory           domain.TaskMemory
	GlobalKnowledge  []domain.KnowledgeEntry
	ProjectKnowledge []domain.KnowledgeEntry
	Skills           []domain.Skill
	ProductLine      domain.ProductLine
	KnowledgeEnabled bool
	Conversation     []domain.ConversationItem
	Turns            []domain.Turn
	Hardware         []domain.HardwareGroup
	AgentAPIURL      string
	UserMessage      string
	Handoff          string
}

type ContextResource struct {
	ID          string `json:"id"`
	URI         string `json:"uri"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Chars       int    `json:"chars"`
	Content     string `json:"content,omitempty"`
	EntryPoint  bool   `json:"entry_point,omitempty"`
}

type BuildResult struct {
	EffectivePrompt string
	ContextRoot     string
	ContextManifest []ContextResource
}

type templateData struct {
	AgentID            string
	AgentRole          string
	Identity           string
	Channel            string
	Backend            string
	Collaboration      string
	MaxAgents          int
	TaskID             string
	TaskCode           string
	TaskTitle          string
	ContextRoot        string
	TaskWorkspace      string
	Workspace          string
	WorkspaceTransport string
}

func NewEngine(repository Repository) *Engine {
	return &Engine{repository: repository}
}

var builtinTemplates = []domain.PromptTemplate{
	{ID: "core.default", Name: "AHA Core", Layer: "core", Description: "所有 Agent 共用的安全、Workspace 与 AHA 编排边界", Editable: true, Required: true, Version: 1},
	{ID: "role.main", Name: "Main Agent", Layer: "role", Description: "Main 的规划、整合和最终交付职责", Editable: true, Required: true, Version: 1},
	{ID: "role.sub", Name: "Sub Agent", Layer: "role", Description: "Sub 的聚焦执行和隔离职责", Editable: true, Required: true, Version: 1},
	{ID: "identity.task-agent", Name: "Task Agent Identity", Layer: "identity", Description: "代码与项目任务场景身份", Editable: true, Required: false, Version: 1},
	{ID: "channel.web", Name: "AHA Web Channel", Layer: "channel", Description: "Web 渠道消息行为", Editable: true, Required: false, Version: 1},
	{ID: "policy.auto", Name: "Auto Collaboration", Layer: "policy", Description: "AHA 自动协作策略", Editable: true, Required: false, Version: 1},
	{ID: "policy.single", Name: "Single Agent", Layer: "policy", Description: "单 Agent 策略", Editable: true, Required: false, Version: 1},
	{ID: "protocol.agent-api", Name: "Agent Control API Protocol", Layer: "protocol", Description: "通过 Agent API 提交结构化状态，最终回复仅保留自然语言", Content: agentAPIProtocol, Editable: false, Required: true, Version: 1},
}

func (engine *Engine) Templates(ctx context.Context) ([]domain.PromptTemplate, error) {
	overrides, err := engine.repository.PromptTemplateOverrides(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.PromptTemplate, 0, len(builtinTemplates))
	for _, item := range builtinTemplates {
		if item.Content == "" {
			data, readErr := templateFiles.ReadFile("templates/" + strings.ReplaceAll(item.ID, ".", "-") + ".md")
			if readErr != nil {
				return nil, readErr
			}
			item.Content = strings.TrimSpace(string(data))
		}
		item.Source = "builtin"
		if override, ok := overrides[item.ID]; ok && item.Editable {
			item.Content = override.Content
			item.Version = override.Version + 1
			item.UpdatedAt = override.UpdatedAt
			item.Source = "override"
		}
		result = append(result, item)
	}
	return result, nil
}

func (engine *Engine) UpdateTemplate(ctx context.Context, id, content string, now time.Time) error {
	item, ok := builtinTemplate(id)
	if !ok {
		return fmt.Errorf("prompt template not found")
	}
	if !item.Editable {
		return fmt.Errorf("prompt template is managed by code")
	}
	content = strings.TrimSpace(content)
	if content == "" || len([]rune(content)) > 100000 {
		return fmt.Errorf("prompt template content must contain 1 to 100000 characters")
	}
	if _, err := template.New(id).Option("missingkey=error").Parse(content); err != nil {
		return fmt.Errorf("invalid prompt template: %w", err)
	}
	return engine.repository.UpsertPromptTemplateOverride(ctx, id, content, now)
}

func (engine *Engine) ResetTemplate(ctx context.Context, id string) error {
	if _, ok := builtinTemplate(id); !ok {
		return fmt.Errorf("prompt template not found")
	}
	return engine.repository.DeletePromptTemplateOverride(ctx, id)
}

func (engine *Engine) Build(ctx context.Context, input BuildInput) (BuildResult, error) {
	templates, err := engine.Templates(ctx)
	if err != nil {
		return BuildResult{}, err
	}
	templateByID := make(map[string]domain.PromptTemplate, len(templates))
	for _, item := range templates {
		templateByID[item.ID] = item
	}
	workDir := input.Task.TaskWorkspacePath
	if workDir == "" {
		workDir = input.Workspace.RootPath
	}
	contextRoot := contextRootFor(input, workDir)
	data := templateData{
		AgentID: input.Agent.AgentID, AgentRole: input.Agent.Role, Identity: "task-agent",
		Channel: "web", Backend: input.Snapshot.Backend, Collaboration: input.Task.CollaborationMode,
		MaxAgents: input.Task.MaxAgents, TaskID: input.Task.ID, TaskCode: input.Task.Code,
		TaskTitle: input.Task.Title, ContextRoot: contextRoot, TaskWorkspace: workDir,
		Workspace: input.Workspace.Name, WorkspaceTransport: input.Workspace.Transport,
	}
	resources := buildResources(input, contextRoot, workDir)
	templateIDs := []string{"core.default", "identity.task-agent"}
	if input.Agent.Role == "sub" {
		templateIDs = append(templateIDs, "role.sub")
	} else {
		templateIDs = append(templateIDs, "role.main")
	}
	templateIDs = append(templateIDs, "channel.web")
	if input.Agent.Role == "main" {
		if input.Task.CollaborationMode == "single" {
			templateIDs = append(templateIDs, "policy.single")
		} else {
			templateIDs = append(templateIDs, "policy.auto")
		}
	}
	var parts []string
	for _, id := range templateIDs {
		item := templateByID[id]
		content, renderErr := renderTemplate(item.ID, item.Content, data)
		if renderErr != nil {
			return BuildResult{}, renderErr
		}
		parts = append(parts, "## "+item.Name+"\n"+content)
	}
	if strings.TrimSpace(input.Handoff) != "" {
		parts = append(parts, "## Compact Handoff\nThe previous Backend Session was compacted by AHA. Continue from this durable handoff in the new session:\n\n"+strings.TrimSpace(input.Handoff))
	}
	parts = append(parts,
		"## Task and workspace\n"+taskSummary(input, workDir),
		"## Available context\n"+manifestText(resources),
		"## Current Inbox Batch\n"+strings.TrimSpace(input.UserMessage),
	)
	protocol := templateByID["protocol.agent-api"]
	protocolContent, err := renderTemplate(protocol.ID, protocol.Content, data)
	if err != nil {
		return BuildResult{}, err
	}
	parts = append(parts, "## "+protocol.Name+"\n"+protocolContent)
	effective := strings.Join(parts, "\n\n")
	return BuildResult{
		EffectivePrompt: effective, ContextRoot: contextRoot, ContextManifest: resources,
	}, nil
}

func builtinTemplate(id string) (domain.PromptTemplate, bool) {
	for _, item := range builtinTemplates {
		if item.ID == id {
			return item, true
		}
	}
	return domain.PromptTemplate{}, false
}

func renderTemplate(id, content string, data templateData) (string, error) {
	value, err := template.New(id).Option("missingkey=error").Parse(content)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := value.Execute(&output, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(output.String()), nil
}

func safeAgentID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "main"
	}
	return strings.NewReplacer("/", "-", "\\", "-", "..", "-").Replace(value)
}

func contextRootFor(input BuildInput, workDir string) string {
	if input.Workspace.Transport == "native" {
		return filepath.Join(workDir, ".aha2-context", input.Task.ID, safeAgentID(input.Agent.AgentID))
	}
	workDir = strings.ReplaceAll(workDir, "\\", "/")
	return path.Join(workDir, ".aha2-context", input.Task.ID, safeAgentID(input.Agent.AgentID))
}

func taskSummary(input BuildInput, workDir string) string {
	return fmt.Sprintf(
		"- project: %s\n- task: %s (%s)\n- current goal: %s\n- workspace: %s\n- task workdir: %s\n- transport: %s\n- branch: %s",
		input.Project.Name, input.Task.Title, input.Task.Code, truncate(input.Task.CurrentGoal, 800),
		input.Workspace.Name, workDir, input.Workspace.Transport, input.Task.TaskBranch,
	)
}

func truncate(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit-1]) + "..."
}

func buildResources(input BuildInput, root, workDir string) []ContextResource {
	resources := []ContextResource{
		resource(input, "task", joinContextPath(input, root, "task.md"), "完整 Task、Project 与 Workspace 信息", taskResource(input, workDir)),
		resource(input, "task-memory", joinContextPath(input, root, "task-memory.md"), "完整 Task Memory", fullMemory(input.Memory)),
		resource(input, "conversation", joinContextPath(input, root, "conversation.md"), "当前 Agent 最近 Conversation", conversationResource(input.Conversation)),
		resource(input, "turns", joinContextPath(input, root, "turns.md"), "当前 Agent Turn 历史", turnsResource(input.Turns, input.Agent.AgentID)),
	}
	if len(input.Hardware) > 0 {
		resources = append(resources, resource(input, "hardware", joinContextPath(input, root, "hardware.md"), "Task 硬件调试配置（不含密码）", hardwareResource(input.Hardware)))
	}
	if strings.TrimSpace(input.AgentAPIURL) != "" {
		resources = append(resources, resource(input, "agent-api", joinContextPath(input, root, "agent-api.md"), "当前 Turn 的受限 Agent API 使用说明", agentAPIResource(input.AgentAPIURL)))
	}
	if input.KnowledgeEnabled {
		resources = append(resources, knowledgeResources(input, root)...)
	}
	if len(input.Skills) > 0 {
		resources = append(resources, skillResources(input, root)...)
	}
	metadata := append([]ContextResource(nil), resources...)
	for index := range metadata {
		metadata[index].Content = ""
	}
	manifest, _ := json.MarshalIndent(map[string]any{"version": 1, "resources": metadata}, "", "  ")
	return append([]ContextResource{resource(input, "manifest", joinContextPath(input, root, "manifest.json"), "上下文资源清单", string(manifest))}, resources...)
}

func joinContextPath(input BuildInput, values ...string) string {
	if input.Workspace.Transport == "native" {
		return filepath.Join(values...)
	}
	return path.Join(values...)
}

func resource(input BuildInput, id, path, description, content string) ContextResource {
	return ContextResource{
		ID: id, URI: fmt.Sprintf("aha://tasks/%s/agents/%s/context/%s", input.Task.ID, input.Agent.AgentID, id),
		Path: path, Description: description, Chars: len([]rune(content)), Content: content, EntryPoint: true,
	}
}

func detailResource(input BuildInput, id, path, description, content string) ContextResource {
	item := resource(input, id, path, description, content)
	item.EntryPoint = false
	return item
}

func manifestText(resources []ContextResource) string {
	lines := []string{"Large context is available as workspace files. Read only what is needed:"}
	for _, item := range resources {
		if !item.EntryPoint {
			continue
		}
		lines = append(lines, fmt.Sprintf("- `%s` (%s, %d chars)", item.Path, item.Description, item.Chars))
	}
	return strings.Join(lines, "\n")
}

func knowledgeResources(input BuildInput, root string) []ContextResource {
	directory := joinContextPath(input, root, "knowledge")
	entries := append([]domain.KnowledgeEntry(nil), input.ProjectKnowledge...)
	entries = append(entries, input.GlobalKnowledge...)
	lines := []string{"# Knowledge", "", "Read only entries relevant to the current task. Report actual usage through the Agent Knowledge feedback API."}
	if input.ProductLine.ID != "" {
		lines = append(lines, "", fmt.Sprintf("Active product line: %s (%s)", input.ProductLine.Name, input.ProductLine.BranchPattern))
	}
	resources := []ContextResource{}
	for _, entry := range entries {
		entryPath := joinContextPath(input, directory, entry.ID+".md")
		lines = append(lines, fmt.Sprintf("- [%s] `%s` · %s/%s · revision %d · confidence %.2f", entry.ID, entryPath, entry.Scope, entry.Type, entry.Revision, entry.Confidence))
		body := fmt.Sprintf("# %s\n\n- id: %s\n- scope: %s\n- type: %s\n- revision: %d\n- content_hash: %s\n- product_line_id: %s\n- verified_commit: %s\n\n%s\n", entry.Title, entry.ID, entry.Scope, entry.Type, entry.Revision, entry.ContentHash, entry.ProductLineID, entry.VerifiedCommit, entry.Body)
		resources = append(resources, detailResource(input, "knowledge-"+entry.ID, entryPath, entry.Title, body))
	}
	if len(entries) == 0 {
		lines = append(lines, "", "No verified knowledge is currently available.")
	}
	index := resource(input, "knowledge-index", joinContextPath(input, directory, "index.md"), "Knowledge entrypoint", strings.Join(lines, "\n"))
	return append([]ContextResource{index}, resources...)
}

func skillResources(input BuildInput, root string) []ContextResource {
	directory := joinContextPath(input, root, "skills")
	resources := []ContextResource{}
	for _, skill := range input.Skills {
		slug := skill.PackageSlug
		if slug == "" {
			slug = skill.ID
		}
		packageDirectory := joinContextPath(input, directory, slug)
		files := skill.PackageFiles
		if len(files) == 0 {
			files = []domain.SkillFile{{Path: "SKILL.md", Content: fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", skill.Name, skill.Description, skill.Instructions)}}
		}
		fileHashes := map[string]string{}
		for _, file := range files {
			fileHashes[file.Path] = fmt.Sprintf("%x", sha256.Sum256([]byte(file.Content)))
		}
		metadata, _ := json.MarshalIndent(map[string]any{
			"skill_id": skill.ID, "version": skill.Version, "scope": skill.Scope,
			"project_id": skill.ProjectID, "files": fileHashes, "writeback": "agent_api",
		}, "", "  ")
		resources = append(resources, detailResource(input, "skill-"+skill.ID+"-manifest", joinContextPath(input, packageDirectory, "skill-manifest.json"), skill.Name+" package metadata", string(metadata)))
		for index, file := range files {
			filePath := joinContextPath(input, packageDirectory, file.Path)
			item := detailResource(input, fmt.Sprintf("skill-%s-file-%d", skill.ID, index), filePath, skill.Name+" package file", file.Content)
			if file.Path == "SKILL.md" {
				item = resource(input, "skill-"+skill.ID, filePath, fmt.Sprintf("Selected Skill: %s - %s", skill.Name, skill.Description), file.Content)
			}
			resources = append(resources, item)
		}
	}
	return resources
}

func taskResource(input BuildInput, workDir string) string {
	return fmt.Sprintf("# Task\n\nID: %s\nCode: %s\nTitle: %s\nOriginal request:\n%s\n\nCurrent goal:\n%s\n\nProject: %s\nWorkspace: %s\nTask workdir: %s\n",
		input.Task.ID, input.Task.Code, input.Task.Title, input.Task.OriginalRequest, input.Task.CurrentGoal,
		input.Project.Name, input.Workspace.Name, workDir)
}

func fullMemory(memory domain.TaskMemory) string {
	value := "# Task Memory\n\n" + memoryText(memory)
	if refs, ok := memory.Extra["knowledge_refs"]; ok {
		if data, err := json.MarshalIndent(refs, "", "  "); err == nil && string(data) != "null" && string(data) != "[]" {
			value += "\n\n## Knowledge references\n\n```json\n" + string(data) + "\n```"
		}
	}
	return value
}

func conversationResource(items []domain.ConversationItem) string {
	var lines []string
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- [%s/%s from %s] %s", item.Category, item.Kind, item.FromAgentID, item.Summary))
	}
	if len(lines) == 0 {
		return "# Conversation\n\n-"
	}
	return "# Conversation\n\n" + strings.Join(lines, "\n")
}

func turnsResource(turns []domain.Turn, agentID string) string {
	var lines []string
	for _, turn := range turns {
		if turn.AgentID != agentID {
			continue
		}
		body := turn.Result
		if body == "" {
			body = turn.Error
		}
		lines = append(lines, fmt.Sprintf("- Turn %d %s [%s]: %s", turn.Sequence, turn.AgentID, turn.Status, truncate(body, 1000)))
	}
	if len(lines) == 0 {
		return "# Turns\n\n-"
	}
	return "# Turns\n\n" + strings.Join(lines, "\n")
}

func hardwareResource(groups []domain.HardwareGroup) string {
	lines := []string{"# Hardware", "", "Credentials are never included in this file."}
	for _, group := range groups {
		lines = append(lines,
			"",
			fmt.Sprintf("## %s", group.ID),
			fmt.Sprintf("- description: %s", group.Description),
			fmt.Sprintf("- mode: %s", group.Mode),
			fmt.Sprintf("- access: %s", group.Access),
		)
		if group.Supports(domain.HardwareTransportSerial) {
			lines = append(lines, fmt.Sprintf("- serial: %s @ %d", group.Serial.Device, group.Serial.Baudrate))
		}
		if group.Supports(domain.HardwareTransportNetwork) {
			lines = append(lines, fmt.Sprintf(
				"- network: %s:%d (%s)", group.Network.Host, group.Network.Port, group.Network.Protocol,
			))
			if group.Network.Protocol == domain.HardwareProtocolSSH {
				lines = append(lines, fmt.Sprintf("- ssh authentication: %s", normalizeHardwareSSHAuth(group.Network.SSHAuth)))
			}
		}
		if group.Username != "" {
			lines = append(lines, fmt.Sprintf("- username: %s", group.Username))
		}
		lines = append(lines, fmt.Sprintf("- password configured: %t", group.PasswordConfigured))
	}
	if len(groups) == 0 {
		lines = append(lines, "", "No hardware groups configured.")
	}
	return strings.Join(lines, "\n")
}

func agentAPIResource(baseURL string) string {
	return fmt.Sprintf(`# Agent API

The AHA2 control plane exposes a Task-scoped API at %s.
Use the value of environment variable AHA2_AGENT_API_TOKEN as a Bearer token. Never print, persist, or include that token in a response. The capability expires when this Turn finishes.

Send JSON with Content-Type: application/json. The final assistant response must be natural language only; never append a checkpoint.

## Turn state

- GET /api/v1/agent/capabilities
- PATCH /api/v1/agent/turn/memory with {"append":{"decisions":[],"facts":[],"excluded":[],"progress":[],"verification":[],"next_actions":[]}}
- POST /api/v1/agent/turn/messages with {"message":"concise user-facing progress"}
- POST /api/v1/agent/collaboration/batches with {"actions":[{"agent_id":"sub-001","title":"...","assignment":"...","required":true}],"main_followup":"..."}
- GET /api/v1/agent/project/workspaces
- GET /api/v1/agent/project/runtimes
- POST /api/v1/agent/tasks with {"workspace_id":"...","title":"...","request":"...","clone_hardware":true,"backend":"claude","model_source":"provider","model_id":"..."}
- GET /api/v1/agent/tasks/{task}

Task creation inherits the current Turn runtime when runtime fields are omitted. To select another configured runtime, first list project runtimes and pass back the exact backend/model fields; credentials and permissions are never accepted in this payload.

Only Main may change Memory, Knowledge, Skills, or collaboration. Send material progress promptly through turn/messages. Do not claim an update was sent unless the API returned success.

## Knowledge and Skills

- GET /api/v1/agent/knowledge
- GET /api/v1/agent/knowledge/{id}
- POST /api/v1/agent/knowledge/candidates with {"candidates":[{"entry_id":"","base_revision":0,"scope":"project","type":"practice","title":"...","body":"...","confidence":0.8,"product_line_id":""}]}
- POST /api/v1/agent/knowledge/{id}/feedback with {"kind":"helped|stale|wrong"}
- GET /api/v1/agent/skills
- GET /api/v1/agent/skills/{id}
- PUT /api/v1/agent/skills/{id} with {"base_version":1,"name":"...","description":"...","files":[{"path":"SKILL.md","content":"..."}]}

For an existing Knowledge entry, base_revision is required and conflicts return HTTP 409. Skill updates replace the complete text package, require its current base_version, and are limited to Skills selected by this Task.

## Managed processes

- GET /api/v1/agent/processes
- POST /api/v1/agent/processes with JSON {"name":"dev-server","executable":"...","args":[],"cwd":"...","env":{}}
- GET /api/v1/agent/processes/{name}
- POST /api/v1/agent/processes/{name}/stop

Managed processes are owned by the AHA2 service rather than the Agent subprocess, so they continue after the current Turn. They stop when explicitly requested or when AHA2 itself shuts down. cwd must stay inside the selected Task workspace.

## Hardware

- GET /api/v1/agent/hardware
- GET /api/v1/agent/hardware/{hardware}/terminal?transport=serial|network&after=0&limit=500
- POST /api/v1/agent/hardware/{hardware}/connect?transport=serial|network
- POST /api/v1/agent/hardware/{hardware}/disconnect?transport=serial|network
- POST /api/v1/agent/hardware/{hardware}/send?transport=serial|network with JSON {"data":"...","encoding":"text|hex"}
- POST /api/v1/agent/hardware/{hardware}/login?transport=serial|network with configurable prompts, line_ending, wakeup, timeout_seconds, and retries

All requests require Authorization: Bearer $AHA2_AGENT_API_TOKEN. The API cannot change hardware configuration and enforces the Task/hardware read-only rules.`, strings.TrimRight(baseURL, "/"))
}

func normalizeHardwareSSHAuth(value string) string {
	switch value {
	case domain.HardwareSSHAuthPassword, domain.HardwareSSHAuthKey:
		return value
	default:
		return domain.HardwareSSHAuthAuto
	}
}
