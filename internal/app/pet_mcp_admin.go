package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	petMCPServerCreateKind = "mcp_server_create"
	petMCPServerUpdateKind = "mcp_server_update"
	petMCPServerDeleteKind = "mcp_server_delete"
	petSetTaskMCPKind      = "set_task_mcp_servers"

	// petMCPSecretRef formats the Secret Store key the pet create/update path
	// looks up for an env/header value. The approval payload only carries the
	// key name, never the value.
	petMCPEnvSecretRef    = "mcp/%s/env/%s"
	petMCPHeaderSecretRef = "mcp/%s/headers/%s"
)

// PetMCPServerView is the sanitized MCP shape exposed to the pet. Env and
// header values, command args and URLs are deliberately omitted; only key
// names are returned.
type PetMCPServerView struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Transport   string   `json:"transport"`
	Enabled     bool     `json:"enabled"`
	Builtin     bool     `json:"builtin"`
	AutoInject  bool     `json:"auto_inject,omitempty"`
	Health      string   `json:"health"`
	ToolCount   int      `json:"tool_count"`
	EnvKeys     []string `json:"env_keys,omitempty"`
	HeaderKeys  []string `json:"header_keys,omitempty"`
}

type PetMCPServerListView struct {
	Servers []PetMCPServerView `json:"servers"`
	Count   int                `json:"count"`
}

type PetMCPServerTestResult struct {
	ID        string    `json:"id"`
	Health    string    `json:"health"`
	ToolCount int       `json:"tool_count"`
	Tools     []string  `json:"tools,omitempty"`
	Error     string    `json:"error,omitempty"`
	Probe     string    `json:"probe,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
}

type PetTaskMCPServersView struct {
	TaskID             string              `json:"task_id"`
	TaskTitle          string              `json:"task_title,omitempty"`
	MCPServerIDs       []string            `json:"mcp_server_ids"`
	EffectiveServerIDs []string            `json:"effective_server_ids,omitempty"`
	Servers            []PetMCPServerView  `json:"servers"`
	Status             string              `json:"status,omitempty"`
	Approval           *domain.PetApproval `json:"approval,omitempty"`
}

// PetMCPServerCreateInput intentionally has no env/header value fields. Strict
// decoding rejects unknown keys such as env or headers, so a caller cannot
// smuggle a secret into the approval body.
type PetMCPServerCreateInput struct {
	Name        string   `json:"name"`
	ServerID    string   `json:"server_id"`
	Description string   `json:"description"`
	Transport   string   `json:"transport"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	URL         string   `json:"url"`
	EnvKeys     []string `json:"env_keys"`
	HeaderKeys  []string `json:"header_keys"`
	Enabled     *bool    `json:"enabled"`
	Reason      string   `json:"reason"`
}

type PetMCPServerUpdateInput struct {
	ServerID    string    `json:"server_id"`
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Transport   *string   `json:"transport"`
	Command     *string   `json:"command"`
	Args        *[]string `json:"args"`
	URL         *string   `json:"url"`
	EnvKeys     *[]string `json:"env_keys"`
	HeaderKeys  *[]string `json:"header_keys"`
	Enabled     *bool     `json:"enabled"`
	Reason      string    `json:"reason"`
}

type PetMCPServerDeleteInput struct {
	ServerID string `json:"server_id"`
	Reason   string `json:"reason"`
}

type PetSetTaskMCPServersInput struct {
	TaskID    string   `json:"task_id"`
	ServerIDs []string `json:"server_ids"`
	Reason    string   `json:"reason"`
}

// petMCPServerApprovalPayload is the secret-free config persisted in an
// owner_decision approval. Execution rebuilds env/header values from the
// Secret Store or the existing server row.
type petMCPServerApprovalPayload struct {
	RequestID   string   `json:"request_id"`
	ServerID    string   `json:"server_id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Transport   string   `json:"transport"`
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
	URL         string   `json:"url,omitempty"`
	EnvKeys     []string `json:"env_keys,omitempty"`
	HeaderKeys  []string `json:"header_keys,omitempty"`
	Enabled     bool     `json:"enabled"`
}

type petSetTaskMCPApprovalPayload struct {
	RequestID string   `json:"request_id"`
	TaskID    string   `json:"task_id"`
	ServerIDs []string `json:"server_ids"`
}

func decodePetMCPAdminJSON(args json.RawMessage, target any) error {
	if len(bytes.TrimSpace(args)) == 0 {
		args = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid mcp request: %w", err)
	}
	return nil
}

func (s *Service) PetListMCPServers(ctx context.Context) (PetMCPServerListView, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return PetMCPServerListView{}, err
	}
	items, err := s.MCPServers(ctx)
	if err != nil {
		return PetMCPServerListView{}, err
	}
	view := PetMCPServerListView{Servers: make([]PetMCPServerView, 0, len(items))}
	for _, item := range items {
		view.Servers = append(view.Servers, petMCPServerView(item))
	}
	view.Count = len(view.Servers)
	return view, nil
}

func (s *Service) PetTestMCPServer(ctx context.Context, id string) (PetMCPServerTestResult, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return PetMCPServerTestResult{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return PetMCPServerTestResult{}, fmt.Errorf("server_id 不能为空")
	}
	item, err := s.MCPServerByID(ctx, id)
	if err != nil {
		return PetMCPServerTestResult{}, fmt.Errorf("MCP 不存在")
	}
	result := PetMCPServerTestResult{ID: item.ID, CheckedAt: s.now().UTC()}
	switch {
	case item.Builtin && item.ID == "mcp_pet":
		result.Health = "ok"
		result.Tools = petMCPToolNames(item)
		result.Probe = "builtin"
	case item.Builtin:
		// Channel/mail are injected per Turn; there is no standalone endpoint
		// to probe, but their tool catalog is known.
		result.Health = "ok"
		result.Tools = petMCPToolNames(item)
		result.Probe = "auto_inject"
	case item.Transport == "stdio" && strings.TrimSpace(item.Command) != "":
		tools, probeErr := fetchStdioTools(ctx, item)
		result.Probe = "stdio"
		result = applyPetMCPProbe(result, tools, probeErr, item)
	case item.Transport == "http" && strings.TrimSpace(item.URL) != "":
		tools, probeErr := fetchMCPTools(ctx, item)
		result.Probe = "http"
		result = applyPetMCPProbe(result, tools, probeErr, item)
	default:
		result.Health = "error"
		result.Error = "MCP 配置缺少可探测的 transport 或地址"
	}
	result.ToolCount = len(result.Tools)
	return result, nil
}

func applyPetMCPProbe(result PetMCPServerTestResult, tools []domain.MCPTool, probeErr error, item domain.MCPServer) PetMCPServerTestResult {
	if probeErr != nil {
		result.Health = "error"
		result.Error = redactPetMCPText(probeErr.Error(), item)
		return result
	}
	result.Health = "ok"
	// A server controls its own tool names, so redact them too: a hostile MCP
	// could otherwise echo an env/header secret back through tools/list.
	result.Tools = make([]string, 0, len(tools))
	for _, name := range petMCPToolNamesFromTools(tools) {
		result.Tools = append(result.Tools, redactPetMCPText(name, item))
	}
	return result
}

func (s *Service) PetListTaskMCPServers(ctx context.Context, taskID string) (PetTaskMCPServersView, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return PetTaskMCPServersView{}, err
	}
	task, project, err := s.petTaskAndProject(ctx, taskID)
	if err != nil {
		return PetTaskMCPServersView{}, err
	}
	view := PetTaskMCPServersView{
		TaskID:       task.ID,
		TaskTitle:    task.Title,
		MCPServerIDs: append([]string{}, task.MCPServerIDs...),
		Servers:      make([]PetMCPServerView, 0),
	}
	if effective, err := s.normalizeTaskMCPServers(ctx, project, task.MCPServerIDs); err == nil {
		view.EffectiveServerIDs = effective
	}
	items, err := s.MCPServers(ctx)
	if err != nil {
		return PetTaskMCPServersView{}, err
	}
	byID := make(map[string]domain.MCPServer, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, id := range task.MCPServerIDs {
		if item, ok := byID[id]; ok {
			view.Servers = append(view.Servers, petMCPServerView(item))
			continue
		}
		view.Servers = append(view.Servers, PetMCPServerView{ID: id, Name: "(已删除)", Health: "missing"})
	}
	return view, nil
}

func (s *Service) PetSetTaskMCPServers(ctx context.Context, input PetSetTaskMCPServersInput) (PetTaskMCPServersView, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return PetTaskMCPServersView{}, err
	}
	task, project, err := s.petTaskAndProject(ctx, input.TaskID)
	if err != nil {
		return PetTaskMCPServersView{}, err
	}
	ids, err := s.validatePetTaskMCPServerIDs(ctx, project, input.ServerIDs)
	if err != nil {
		return PetTaskMCPServersView{}, err
	}
	if task.ID != petTaskID {
		reason, reasonErr := requiredPetApprovalReason(input.Reason)
		if reasonErr != nil {
			return PetTaskMCPServersView{}, fmt.Errorf("操作其他 Task 需要审批：%w", reasonErr)
		}
		payload, _ := json.Marshal(petSetTaskMCPApprovalPayload{
			RequestID: domain.NewID("mcp_req"), TaskID: task.ID, ServerIDs: ids,
		})
		approval, err := s.CreatePetApproval(ctx, PetApprovalInput{
			Kind:  petSetTaskMCPKind,
			Title: "设置 Task MCP 绑定：" + truncateAssistantText(task.Title, 80),
			Summary: fmt.Sprintf("宠物申请把 Task「%s」（%s）的 MCP 绑定改为 %s。批准后才生效。",
				truncateAssistantText(task.Title, 80), task.ID, strings.Join(ids, "、")),
			Reason: reason, Risk: "high", Payload: string(payload),
		})
		if err != nil {
			return PetTaskMCPServersView{}, err
		}
		view, err := s.PetListTaskMCPServers(ctx, task.ID)
		if err != nil {
			return PetTaskMCPServersView{}, err
		}
		view.Status = petApprovalPending
		view.Approval = &approval
		return view, nil
	}
	if _, err := s.SetTaskMCPServers(ctx, task.ID, ids); err != nil {
		return PetTaskMCPServersView{}, err
	}
	s.auditPetMCPServer(ctx, "pet.mcp_server.bind_task", map[string]any{
		"task_id": task.ID, "server_ids": ids,
	})
	view, err := s.PetListTaskMCPServers(ctx, task.ID)
	if err != nil {
		return PetTaskMCPServersView{}, err
	}
	view.Status = "applied"
	return view, nil
}

func (s *Service) PetRequestMCPServerCreate(ctx context.Context, input PetMCPServerCreateInput) (domain.PetApproval, error) {
	reason, err := s.petMCPWriteReason(ctx, input.Reason)
	if err != nil {
		return domain.PetApproval{}, err
	}
	serverID := strings.TrimSpace(input.ServerID)
	if serverID == "" {
		serverID = domain.NewID("mcp")
	}
	if err := validatePetMCPServerID(serverID); err != nil {
		return domain.PetApproval{}, err
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	config := petMCPServerApprovalPayload{
		RequestID: domain.NewID("mcp_req"), ServerID: serverID,
		Name: input.Name, Description: input.Description, Transport: input.Transport,
		Command: input.Command, Args: input.Args, URL: input.URL,
		EnvKeys: input.EnvKeys, HeaderKeys: input.HeaderKeys, Enabled: enabled,
	}
	normalizePetMCPServerConfig(&config)
	if err := validatePetMCPServerConfig(config); err != nil {
		return domain.PetApproval{}, err
	}
	if existing, err := s.MCPServerByID(ctx, serverID); err == nil {
		if existing.Builtin {
			return domain.PetApproval{}, fmt.Errorf("内置 MCP 不能通过宠物工具新增")
		}
		return domain.PetApproval{}, fmt.Errorf("MCP %s 已存在", serverID)
	}
	payload, _ := json.Marshal(config)
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind:    petMCPServerCreateKind,
		Title:   "新增外部 MCP：" + truncateAssistantText(config.Name, 80),
		Summary: petMCPServerCreateSummary(config),
		Reason:  reason, Risk: "high", Payload: string(payload),
	})
}

func (s *Service) PetRequestMCPServerUpdate(ctx context.Context, input PetMCPServerUpdateInput) (domain.PetApproval, error) {
	reason, err := s.petMCPWriteReason(ctx, input.Reason)
	if err != nil {
		return domain.PetApproval{}, err
	}
	serverID := strings.TrimSpace(input.ServerID)
	if serverID == "" {
		return domain.PetApproval{}, fmt.Errorf("server_id 不能为空")
	}
	existing, err := s.MCPServerByID(ctx, serverID)
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("MCP 不存在")
	}
	if existing.Builtin {
		return domain.PetApproval{}, fmt.Errorf("内置 MCP 不能修改")
	}
	config := petMCPServerApprovalPayload{
		RequestID: domain.NewID("mcp_req"), ServerID: existing.ID,
		Name: existing.Name, Description: existing.Description, Transport: existing.Transport,
		Command: existing.Command, Args: append([]string{}, existing.Args...), URL: existing.URL,
		EnvKeys: sortedStringKeys(existing.Env), HeaderKeys: sortedStringKeys(existing.Headers),
		Enabled: existing.Enabled,
	}
	if input.Name != nil {
		config.Name = *input.Name
	}
	if input.Description != nil {
		config.Description = *input.Description
	}
	if input.Transport != nil {
		config.Transport = *input.Transport
	}
	if input.Command != nil {
		config.Command = *input.Command
	}
	if input.Args != nil {
		config.Args = append([]string{}, (*input.Args)...)
	}
	if input.URL != nil {
		config.URL = *input.URL
	}
	if input.EnvKeys != nil {
		config.EnvKeys = append([]string{}, (*input.EnvKeys)...)
	}
	if input.HeaderKeys != nil {
		config.HeaderKeys = append([]string{}, (*input.HeaderKeys)...)
	}
	if input.Enabled != nil {
		config.Enabled = *input.Enabled
	}
	normalizePetMCPServerConfig(&config)
	if err := validatePetMCPServerConfig(config); err != nil {
		return domain.PetApproval{}, err
	}
	payload, _ := json.Marshal(config)
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind:  petMCPServerUpdateKind,
		Title: "更新外部 MCP：" + truncateAssistantText(config.Name, 80),
		Summary: fmt.Sprintf("宠物申请更新外部 MCP「%s」（%s）。批准后才写入；验证过的字段：name、transport、command/args、url、env/header 键名、enabled。",
			truncateAssistantText(config.Name, 80), config.ServerID),
		Reason: reason, Risk: "high", Payload: string(payload),
	})
}

func (s *Service) PetRequestMCPServerDelete(ctx context.Context, input PetMCPServerDeleteInput) (domain.PetApproval, error) {
	reason, err := s.petMCPWriteReason(ctx, input.Reason)
	if err != nil {
		return domain.PetApproval{}, err
	}
	serverID := strings.TrimSpace(input.ServerID)
	if serverID == "" {
		return domain.PetApproval{}, fmt.Errorf("server_id 不能为空")
	}
	existing, err := s.MCPServerByID(ctx, serverID)
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("MCP 不存在")
	}
	if existing.Builtin {
		return domain.PetApproval{}, fmt.Errorf("内置 MCP 不能删除")
	}
	payload, _ := json.Marshal(map[string]string{
		"request_id": domain.NewID("mcp_req"), "server_id": existing.ID,
	})
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind:  petMCPServerDeleteKind,
		Title: "删除外部 MCP：" + truncateAssistantText(existing.Name, 80),
		Summary: fmt.Sprintf("宠物申请删除外部 MCP「%s」（%s）。批准后才执行；已绑定该 MCP 的 Task 将不再加载它。",
			truncateAssistantText(existing.Name, 80), existing.ID),
		Reason: reason, Risk: "high", Payload: string(payload),
	})
}

func (s *Service) petMCPWriteReason(ctx context.Context, reason string) (string, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return "", err
	}
	return requiredPetApprovalReason(reason)
}

func petMCPServerCreateSummary(config petMCPServerApprovalPayload) string {
	var detail string
	if config.Transport == "http" {
		detail = "HTTP 地址：" + sanitizePetMCPURLForDisplay(config.URL)
	} else {
		detail = "stdio 命令：" + config.Command
	}
	return truncateAssistantText(fmt.Sprintf(
		"宠物申请新增 %s MCP「%s」（%s）。%s。批准前请确认来源可信：外部 MCP 会以 AHA 服务账号权限运行，stdio 等价于执行任意命令。env/header 只记录键名 %s，secret 值需由主人在批准后于 Web 填写或提前放入 Secret Store（mcp/<server_id>/env/<KEY>）。",
		config.Transport, truncateAssistantText(config.Name, 80), config.ServerID, detail,
		petMCPKeySummary(config.EnvKeys, config.HeaderKeys),
	), 1900)
}

func petMCPKeySummary(envKeys, headerKeys []string) string {
	parts := make([]string, 0, 2)
	if len(envKeys) > 0 {
		parts = append(parts, "env="+strings.Join(envKeys, ","))
	} else {
		parts = append(parts, "env=无")
	}
	if len(headerKeys) > 0 {
		parts = append(parts, "headers="+strings.Join(headerKeys, ","))
	} else {
		parts = append(parts, "headers=无")
	}
	return strings.Join(parts, "；")
}

func petMCPServerView(item domain.MCPServer) PetMCPServerView {
	return PetMCPServerView{
		ID: item.ID, Name: item.Name, Description: item.Description,
		Transport: item.Transport, Enabled: item.Enabled, Builtin: item.Builtin,
		AutoInject: item.AutoInject, Health: item.Health,
		ToolCount: len(item.Tools),
		EnvKeys:   sortedStringKeys(item.Env), HeaderKeys: sortedStringKeys(item.Headers),
	}
}

func sortedStringKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func petMCPToolNames(item domain.MCPServer) []string {
	return petMCPToolNamesFromTools(item.Tools)
}

func petMCPToolNamesFromTools(tools []domain.MCPTool) []string {
	result := make([]string, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		result = append(result, tool.Name)
	}
	if len(result) > 100 {
		result = result[:100]
	}
	return result
}

func redactPetMCPText(text string, item domain.MCPServer) string {
	text = strings.TrimSpace(text)
	if item.URL != "" {
		text = strings.ReplaceAll(text, item.URL, sanitizePetMCPURLForDisplay(item.URL))
	}
	for _, value := range item.Env {
		if value != "" {
			text = strings.ReplaceAll(text, value, "***")
		}
	}
	for _, value := range item.Headers {
		if value != "" {
			text = strings.ReplaceAll(text, value, "***")
		}
	}
	if parsed, err := url.Parse(item.URL); err == nil && parsed.User != nil {
		if password, ok := parsed.User.Password(); ok {
			text = strings.ReplaceAll(text, password, "***")
		}
	}
	if len(text) > 600 {
		text = text[:600] + "…"
	}
	return text
}

func sanitizePetMCPURLForDisplay(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "(无效 URL)"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func (s *Service) petTaskAndProject(ctx context.Context, taskID string) (domain.Task, domain.Project, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return domain.Task{}, domain.Project{}, fmt.Errorf("task_id 不能为空")
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.Task{}, domain.Project{}, fmt.Errorf("Task 不存在")
	}
	project, err := s.store.Project(ctx, task.ProjectID)
	if err != nil {
		return domain.Task{}, domain.Project{}, err
	}
	return task, project, nil
}

func (s *Service) validatePetTaskMCPServerIDs(ctx context.Context, project domain.Project, requested []string) ([]string, error) {
	items, err := s.MCPServers(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]domain.MCPServer, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(requested))
	for _, raw := range requested {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, fmt.Errorf("server_ids 不能包含空值")
		}
		if seen[id] {
			continue
		}
		item, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("MCP %s 不存在", id)
		}
		if !item.Enabled {
			return nil, fmt.Errorf("MCP %s 未启用，不能绑定", id)
		}
		if item.AutoInject || id == "mcp_channel" || id == "mcp_mail" {
			return nil, fmt.Errorf("MCP %s 由系统自动注入，不能手动绑定", id)
		}
		if id == "mcp_pet" && project.ID != petProjectID {
			return nil, fmt.Errorf("mcp_pet 只能绑定宠物 Task")
		}
		seen[id] = true
		result = append(result, id)
	}
	if len(result) > 50 {
		return nil, fmt.Errorf("一个 Task 最多绑定 50 个 MCP")
	}
	return result, nil
}

func validatePetMCPServerID(id string) error {
	if id == "" || len(id) > 80 {
		return fmt.Errorf("server_id 长度必须在 1 到 80 之间")
	}
	for _, char := range id {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '_' || char == '-':
		default:
			return fmt.Errorf("server_id 只能包含字母、数字、下划线和短横线")
		}
	}
	if id == "mcp_pet" || id == "mcp_channel" || id == "mcp_mail" {
		return fmt.Errorf("内置 MCP ID 不能使用")
	}
	return nil
}

func normalizePetMCPServerConfig(config *petMCPServerApprovalPayload) {
	config.ServerID = strings.TrimSpace(config.ServerID)
	config.Name = strings.TrimSpace(config.Name)
	config.Description = strings.TrimSpace(config.Description)
	config.Transport = strings.ToLower(strings.TrimSpace(config.Transport))
	config.Command = strings.TrimSpace(config.Command)
	config.URL = strings.TrimSpace(config.URL)
	config.EnvKeys = normalizePetMCPKeys(config.EnvKeys)
	config.HeaderKeys = normalizePetMCPKeys(config.HeaderKeys)
}

func normalizePetMCPKeys(keys []string) []string {
	if keys == nil {
		return nil
	}
	result := make([]string, len(keys))
	for index, key := range keys {
		result[index] = strings.TrimSpace(key)
	}
	return result
}

func validatePetMCPServerConfig(config petMCPServerApprovalPayload) error {
	if err := validatePetMCPServerID(config.ServerID); err != nil {
		return err
	}
	name := strings.TrimSpace(config.Name)
	if name == "" || len([]rune(name)) > 120 {
		return fmt.Errorf("MCP 名称必填且不能超过 120 个字符")
	}
	transport := strings.ToLower(strings.TrimSpace(config.Transport))
	switch transport {
	case "http":
		if err := validatePetMCPHTTPURL(config.URL); err != nil {
			return err
		}
		if strings.TrimSpace(config.Command) != "" || len(config.Args) > 0 {
			return fmt.Errorf("HTTP MCP 不能同时提供 command/args")
		}
	case "stdio":
		if err := validatePetMCPStdioCommand(config.Command, config.Args); err != nil {
			return err
		}
		if strings.TrimSpace(config.URL) != "" {
			return fmt.Errorf("stdio MCP 不能同时提供 url")
		}
	default:
		return fmt.Errorf("transport 必须是 http 或 stdio")
	}
	if err := validatePetMCPKeyNames(config.EnvKeys, true); err != nil {
		return err
	}
	if err := validatePetMCPKeyNames(config.HeaderKeys, false); err != nil {
		return err
	}
	return nil
}

func validatePetMCPHTTPURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("HTTP MCP URL 必填")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("HTTP MCP URL 无效")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("HTTP MCP URL 只允许 http 或 https")
	}
	if parsed.User != nil {
		return fmt.Errorf("HTTP MCP URL 不能内嵌凭据；请使用 env/header 键名和 Secret Store")
	}
	if parsed.RawQuery != "" {
		return fmt.Errorf("HTTP MCP URL 不能带查询参数，避免 secret 进入审批正文；请使用 env/header 键名")
	}
	return nil
}

var petMCPBlockedStdioExecutables = map[string]bool{
	"cmd": true, "cmd.exe": true,
	"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true, "fish": true,
	"powershell": true, "powershell.exe": true, "pwsh": true, "pwsh.exe": true,
	"wsl": true, "wsl.exe": true, "cscript": true, "wscript": true,
	"mshta": true, "mshta.exe": true, "rundll32": true, "rundll32.exe": true,
	"regsvr32": true, "regsvr32.exe": true,
}

func validatePetMCPStdioCommand(command string, args []string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("stdio MCP command 必填")
	}
	if strings.ContainsAny(command, "\r\n\x00") {
		return fmt.Errorf("stdio MCP command 不能包含换行或空字符")
	}
	base := strings.ToLower(filepath.Base(command))
	if petMCPBlockedStdioExecutables[base] {
		return fmt.Errorf("stdio MCP 不允许使用 shell 或脚本宿主命令：%s", base)
	}
	for _, suffix := range []string{".cmd", ".bat", ".ps1"} {
		if strings.HasSuffix(base, suffix) {
			return fmt.Errorf("stdio MCP 不允许使用需要 shell 包装的脚本：%s", base)
		}
	}
	for _, arg := range args {
		if strings.ContainsAny(arg, "\r\n\x00") {
			return fmt.Errorf("stdio MCP args 不能包含换行或空字符")
		}
		if petMCPArgLooksLikeSecret(arg) {
			return fmt.Errorf("stdio MCP args 不能包含 secret 值；请使用 env 键名和 Secret Store")
		}
	}
	return nil
}

func petMCPArgLooksLikeSecret(arg string) bool {
	lower := strings.ToLower(strings.TrimSpace(arg))
	for _, marker := range []string{
		"password=", "passwd=", "secret=", "token=", "api_key=", "apikey=", "api-key=",
		"authorization=", "bearer ",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for _, flag := range []string{
		"--password", "--passwd", "--secret", "--token", "--api-key", "--apikey", "--authorization",
		"-password", "-passwd", "-secret", "-token", "-api-key", "-apikey",
	} {
		if lower == flag || strings.HasPrefix(lower, flag+"=") {
			return true
		}
	}
	return false
}

func validatePetMCPKeyNames(keys []string, env bool) error {
	if len(keys) > 64 {
		return fmt.Errorf("环境变量/Header 键名最多 64 个")
	}
	seen := map[string]bool{}
	for _, raw := range keys {
		key := strings.TrimSpace(raw)
		if key == "" {
			return fmt.Errorf("环境变量/Header 键名不能为空")
		}
		if len(key) > 128 {
			return fmt.Errorf("环境变量/Header 键名过长")
		}
		if seen[key] {
			return fmt.Errorf("环境变量/Header 键名重复：%s", key)
		}
		seen[key] = true
		if env {
			for index, char := range key {
				valid := char == '_' || (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (index > 0 && char >= '0' && char <= '9')
				if !valid {
					return fmt.Errorf("环境变量键名无效：%s", key)
				}
			}
			continue
		}
		for _, char := range key {
			if char <= ' ' || char >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", char) {
				return fmt.Errorf("Header 键名无效：%s", key)
			}
		}
	}
	return nil
}

func (s *Service) applyPetMCPSecretValues(item *domain.MCPServer, existing domain.MCPServer, envKeys, headerKeys []string) {
	// The approval payload always carries the full desired key set, so an empty
	// list means "clear" rather than "leave unchanged". Values are reused from
	// the current row or resolved from the Secret Store; the payload never
	// contains them.
	item.Env = make(map[string]string, len(envKeys))
	for _, key := range envKeys {
		if value, ok := existing.Env[key]; ok && value != "" {
			item.Env[key] = value
			continue
		}
		if value, ok := s.petMCPSecret(petMCPEnvSecretRef, item.ID, key); ok {
			item.Env[key] = value
		}
	}
	item.Headers = make(map[string]string, len(headerKeys))
	for _, key := range headerKeys {
		if value, ok := existing.Headers[key]; ok && value != "" {
			item.Headers[key] = value
			continue
		}
		if value, ok := s.petMCPSecret(petMCPHeaderSecretRef, item.ID, key); ok {
			item.Headers[key] = value
		}
	}
}

func (s *Service) petMCPSecret(format, serverID, key string) (string, bool) {
	if s.secrets == nil {
		return "", false
	}
	value, ok := s.secrets.Get(fmt.Sprintf(format, serverID, key))
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

func (s *Service) executePetMCPServerCreate(ctx context.Context, payload petApprovalPayload) error {
	config := petMCPServerApprovalPayload{
		RequestID: payload.RequestID, ServerID: payload.ServerID, Name: payload.Name,
		Description: payload.Description, Transport: payload.Transport,
		Command: payload.Command, Args: payload.Args, URL: payload.URL,
		EnvKeys: payload.EnvKeys, HeaderKeys: payload.HeaderKeys, Enabled: payload.Enabled,
	}
	normalizePetMCPServerConfig(&config)
	if err := validatePetMCPServerConfig(config); err != nil {
		return err
	}
	if existing, err := s.MCPServerByID(ctx, config.ServerID); err == nil {
		if existing.Builtin {
			return fmt.Errorf("内置 MCP 不能被覆盖")
		}
		if petMCPServerConfigMatches(existing, config) {
			return nil
		}
		return fmt.Errorf("MCP %s 已存在且配置不同，不能重复创建", config.ServerID)
	}
	item := petMCPServerFromConfig(config, domain.MCPServer{})
	s.applyPetMCPSecretValues(&item, domain.MCPServer{}, config.EnvKeys, config.HeaderKeys)
	saved, err := s.SaveMCPServer(ctx, item)
	if err != nil {
		return err
	}
	s.auditPetMCPServer(ctx, "pet.mcp_server.create", map[string]any{
		"server_id": saved.ID, "transport": saved.Transport,
		"env_keys": sortedStringKeys(saved.Env), "header_keys": sortedStringKeys(saved.Headers),
	})
	s.recordPetWork(ctx, "mcp_server_created", saved.ID, "approved")
	return nil
}

func (s *Service) executePetMCPServerUpdate(ctx context.Context, payload petApprovalPayload) error {
	existing, err := s.MCPServerByID(ctx, payload.ServerID)
	if err != nil {
		return fmt.Errorf("MCP 不存在")
	}
	if existing.Builtin {
		return fmt.Errorf("内置 MCP 不能修改")
	}
	config := petMCPServerApprovalPayload{
		RequestID: payload.RequestID, ServerID: existing.ID, Name: payload.Name,
		Description: payload.Description, Transport: payload.Transport,
		Command: payload.Command, Args: payload.Args, URL: payload.URL,
		EnvKeys: payload.EnvKeys, HeaderKeys: payload.HeaderKeys, Enabled: payload.Enabled,
	}
	normalizePetMCPServerConfig(&config)
	if err := validatePetMCPServerConfig(config); err != nil {
		return err
	}
	desired := petMCPServerFromConfig(config, existing)
	s.applyPetMCPSecretValues(&desired, existing, config.EnvKeys, config.HeaderKeys)
	if petMCPServerConfigMatches(existing, config) {
		return nil
	}
	saved, err := s.SaveMCPServer(ctx, desired)
	if err != nil {
		return err
	}
	s.auditPetMCPServer(ctx, "pet.mcp_server.update", map[string]any{
		"server_id": saved.ID, "transport": saved.Transport,
		"env_keys": sortedStringKeys(saved.Env), "header_keys": sortedStringKeys(saved.Headers),
	})
	s.recordPetWork(ctx, "mcp_server_updated", saved.ID, "approved")
	return nil
}

func (s *Service) executePetMCPServerDelete(ctx context.Context, payload petApprovalPayload) error {
	id := strings.TrimSpace(payload.ServerID)
	if id == "" {
		return fmt.Errorf("server_id 不能为空")
	}
	existing, err := s.MCPServerByID(ctx, id)
	if err != nil {
		// Delete is idempotent: already gone is the desired end state.
		return nil
	}
	if existing.Builtin {
		return fmt.Errorf("内置 MCP 不能删除")
	}
	if err := s.DeleteMCPServer(ctx, id); err != nil {
		return err
	}
	s.auditPetMCPServer(ctx, "pet.mcp_server.delete", map[string]any{"server_id": id})
	s.recordPetWork(ctx, "mcp_server_deleted", id, "approved")
	return nil
}

func (s *Service) executePetSetTaskMCPServers(ctx context.Context, payload petApprovalPayload) error {
	task, project, err := s.petTaskAndProject(ctx, payload.TaskID)
	if err != nil {
		return err
	}
	ids, err := s.validatePetTaskMCPServerIDs(ctx, project, payload.ServerIDs)
	if err != nil {
		return err
	}
	if _, err := s.SetTaskMCPServers(ctx, task.ID, ids); err != nil {
		return err
	}
	s.auditPetMCPServer(ctx, "pet.mcp_server.bind_task", map[string]any{
		"task_id": task.ID, "server_ids": ids,
	})
	s.recordPetWork(ctx, "task_mcp_updated", task.ID, "approved")
	return nil
}

func petMCPServerFromConfig(config petMCPServerApprovalPayload, base domain.MCPServer) domain.MCPServer {
	item := base
	item.ID = config.ServerID
	item.Name = strings.TrimSpace(config.Name)
	item.Description = strings.TrimSpace(config.Description)
	item.Transport = strings.ToLower(strings.TrimSpace(config.Transport))
	item.Command = strings.TrimSpace(config.Command)
	item.Args = append([]string{}, config.Args...)
	item.URL = strings.TrimSpace(config.URL)
	item.Enabled = config.Enabled
	item.Builtin = false
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return item
}

func petMCPServerConfigMatches(existing domain.MCPServer, config petMCPServerApprovalPayload) bool {
	if existing.Name != strings.TrimSpace(config.Name) ||
		existing.Description != strings.TrimSpace(config.Description) ||
		existing.Transport != strings.ToLower(strings.TrimSpace(config.Transport)) ||
		existing.Command != strings.TrimSpace(config.Command) ||
		existing.URL != strings.TrimSpace(config.URL) ||
		existing.Enabled != config.Enabled {
		return false
	}
	if !sameStringSlices(existing.Args, config.Args) {
		return false
	}
	if !sameStringSlices(sortedStringKeys(existing.Env), config.EnvKeys) {
		return false
	}
	return sameStringSlices(sortedStringKeys(existing.Headers), config.HeaderKeys)
}

func (s *Service) auditPetMCPServer(ctx context.Context, action string, details map[string]any) {
	_ = s.store.AppendAudit(ctx, "pet", action, "mcp_server", "", details, s.now().UTC().Format(time.RFC3339Nano))
}
