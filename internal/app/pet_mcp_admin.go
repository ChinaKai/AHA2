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

// petMCPServerUpdatePayload stores only the fields the pet asked to change.
// Copying the whole existing row here used to leak command/args/URL values back
// through the tool result and pet_list_approvals; execution now merges this
// diff with the current row at approval time.
type petMCPServerUpdatePayload struct {
	RequestID   string    `json:"request_id"`
	ServerID    string    `json:"server_id"`
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Transport   *string   `json:"transport,omitempty"`
	Command     *string   `json:"command,omitempty"`
	Args        *[]string `json:"args,omitempty"`
	URL         *string   `json:"url,omitempty"`
	EnvKeys     *[]string `json:"env_keys,omitempty"`
	HeaderKeys  *[]string `json:"header_keys,omitempty"`
	Enabled     *bool     `json:"enabled,omitempty"`
}

type petMCPServerDeletePayload struct {
	RequestID string `json:"request_id"`
	ServerID  string `json:"server_id"`
}

// isPetMCPApprovalKind reports whether kind belongs to the approval-gated MCP
// management surface. These kinds may only be created by the dedicated tools
// and must re-check manage_system when executed.
func isPetMCPApprovalKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case petMCPServerCreateKind, petMCPServerUpdateKind, petMCPServerDeleteKind, petSetTaskMCPKind:
		return true
	}
	return false
}

func decodeStoredPetMCPPayload(raw string, target any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("审批参数无效")
	}
	return nil
}

// executePetMCPApproval is the single execution gate for MCP approvals. It
// re-checks manage_system because permission may have been revoked between the
// request and the owner's approval.
func (s *Service) executePetMCPApproval(ctx context.Context, item domain.PetApproval) error {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(item.Kind)) {
	case petMCPServerCreateKind:
		var payload petMCPServerApprovalPayload
		if err := decodeStoredPetMCPPayload(item.Payload, &payload); err != nil {
			return err
		}
		return s.executePetMCPServerCreate(ctx, payload, item.ID)
	case petMCPServerUpdateKind:
		var payload petMCPServerUpdatePayload
		if err := decodeStoredPetMCPPayload(item.Payload, &payload); err != nil {
			return err
		}
		return s.executePetMCPServerUpdate(ctx, payload, item.ID)
	case petMCPServerDeleteKind:
		var payload petMCPServerDeletePayload
		if err := decodeStoredPetMCPPayload(item.Payload, &payload); err != nil {
			return err
		}
		return s.executePetMCPServerDelete(ctx, payload, item.ID)
	case petSetTaskMCPKind:
		var payload petSetTaskMCPApprovalPayload
		if err := decodeStoredPetMCPPayload(item.Payload, &payload); err != nil {
			return err
		}
		return s.executePetSetTaskMCPServers(ctx, payload, item.ID)
	}
	return fmt.Errorf("未知的 MCP 审批类型")
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
	if !item.Enabled {
		// A disabled stdio server must not be started just to probe it.
		result.Health = "disabled"
		result.Error = "MCP 已禁用，未执行探测"
		return result, nil
	}
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
	s.auditPetMCPServer(ctx, "pet.mcp_server.bind_task", "task", task.ID, map[string]any{
		"task_id": task.ID, "server_ids": ids, "requested_by": "pet",
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
	config := petMCPServerConfigFromExisting(existing)
	config.RequestID = domain.NewID("mcp_req")
	applyPetMCPServerUpdateFields(&config, existing, input)
	normalizePetMCPServerConfig(&config)
	if err := validatePetMCPServerConfig(config); err != nil {
		return domain.PetApproval{}, err
	}
	update, changed := petMCPServerUpdateDiff(existing, config)
	if !changed {
		return domain.PetApproval{}, fmt.Errorf("没有检测到需要修改的字段")
	}
	payload, _ := json.Marshal(update)
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind:    petMCPServerUpdateKind,
		Title:   "更新外部 MCP：" + truncateAssistantText(config.Name, 80),
		Summary: petMCPServerUpdateSummary(existing, config),
		Reason:  reason, Risk: "high", Payload: string(payload),
	})
}

func petMCPServerConfigFromExisting(existing domain.MCPServer) petMCPServerApprovalPayload {
	return petMCPServerApprovalPayload{
		ServerID: existing.ID, Name: existing.Name, Description: existing.Description,
		Transport: existing.Transport, Command: existing.Command,
		Args: append([]string{}, existing.Args...), URL: existing.URL,
		EnvKeys: sortedStringKeys(existing.Env), HeaderKeys: sortedStringKeys(existing.Headers),
		Enabled: existing.Enabled,
	}
}

// applyPetMCPServerUpdateFields overlays only the fields the pet submitted on
// top of the current row. Transport switches clear the incompatible endpoint
// field unless the pet set it explicitly, so the diff stays honest.
func applyPetMCPServerUpdateFields(config *petMCPServerApprovalPayload, existing domain.MCPServer, input PetMCPServerUpdateInput) {
	originalTransport := strings.ToLower(strings.TrimSpace(existing.Transport))
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
	if input.Transport == nil {
		return
	}
	if strings.ToLower(strings.TrimSpace(config.Transport)) == originalTransport {
		return
	}
	switch strings.ToLower(strings.TrimSpace(config.Transport)) {
	case "http":
		if input.Command == nil {
			config.Command = ""
		}
		if input.Args == nil {
			config.Args = nil
		}
	case "stdio":
		if input.URL == nil {
			config.URL = ""
		}
	}
}

// petMCPServerUpdateDiff keeps only fields whose merged value differs from the
// current row. Existing values for unchanged fields never enter the approval.
func petMCPServerUpdateDiff(existing domain.MCPServer, config petMCPServerApprovalPayload) (petMCPServerUpdatePayload, bool) {
	diff := petMCPServerUpdatePayload{RequestID: config.RequestID, ServerID: config.ServerID}
	changed := false
	if config.Name != strings.TrimSpace(existing.Name) {
		value := config.Name
		diff.Name = &value
		changed = true
	}
	if config.Description != strings.TrimSpace(existing.Description) {
		value := config.Description
		diff.Description = &value
		changed = true
	}
	if strings.ToLower(config.Transport) != strings.ToLower(strings.TrimSpace(existing.Transport)) {
		value := config.Transport
		diff.Transport = &value
		changed = true
	}
	if config.Command != strings.TrimSpace(existing.Command) {
		value := config.Command
		diff.Command = &value
		changed = true
	}
	if !sameStringSlices(config.Args, existing.Args) {
		value := append([]string{}, config.Args...)
		diff.Args = &value
		changed = true
	}
	if config.URL != strings.TrimSpace(existing.URL) {
		value := config.URL
		diff.URL = &value
		changed = true
	}
	if !sameStringSlices(sortedStringsCopy(config.EnvKeys), sortedStringKeys(existing.Env)) {
		value := append([]string{}, config.EnvKeys...)
		diff.EnvKeys = &value
		changed = true
	}
	if !sameStringSlices(sortedStringsCopy(config.HeaderKeys), sortedStringKeys(existing.Headers)) {
		value := append([]string{}, config.HeaderKeys...)
		diff.HeaderKeys = &value
		changed = true
	}
	if config.Enabled != existing.Enabled {
		value := config.Enabled
		diff.Enabled = &value
		changed = true
	}
	return diff, changed
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
		detail = "stdio 命令：" + redactPetMCPText(config.Command, domain.MCPServer{})
		if len(config.Args) > 0 {
			detail += "；参数：" + petMCPArgsForSummary(config.Args, domain.MCPServer{})
		}
	}
	return truncateAssistantText(fmt.Sprintf(
		"宠物申请新增 %s MCP「%s」（%s）。%s。批准前请确认来源可信：外部 MCP 会以 AHA 服务账号权限运行，stdio 等价于执行任意命令。env/header 只记录键名 %s，secret 值需由主人在批准后于 Web 填写或提前放入 Secret Store（mcp/<server_id>/env/<KEY>）。",
		config.Transport, truncateAssistantText(config.Name, 80), config.ServerID, detail,
		petMCPKeySummary(config.EnvKeys, config.HeaderKeys),
	), 1900)
}

// petMCPServerUpdateSummary lists every changed field with the new value
// sanitized. It deliberately omits unchanged existing values, so the approval
// card and pet_list_approvals cannot expose command/args/URL path secrets that
// the list and test tools redact.
func petMCPServerUpdateSummary(existing domain.MCPServer, config petMCPServerApprovalPayload) string {
	parts := make([]string, 0, 8)
	if config.Name != strings.TrimSpace(existing.Name) {
		parts = append(parts, "名称改为「"+truncateAssistantText(config.Name, 80)+"」")
	}
	if config.Description != strings.TrimSpace(existing.Description) {
		parts = append(parts, "描述已更新")
	}
	if strings.ToLower(config.Transport) != strings.ToLower(strings.TrimSpace(existing.Transport)) {
		parts = append(parts, fmt.Sprintf("transport 从 %s 改为 %s", existing.Transport, config.Transport))
	}
	if config.Enabled != existing.Enabled {
		parts = append(parts, fmt.Sprintf("enabled 改为 %t", config.Enabled))
	}
	if config.Command != strings.TrimSpace(existing.Command) {
		parts = append(parts, "stdio command 改为 "+redactPetMCPText(config.Command, existing))
	}
	if !sameStringSlices(config.Args, existing.Args) {
		parts = append(parts, "stdio args 改为 "+petMCPArgsForSummary(config.Args, existing))
	}
	if config.URL != strings.TrimSpace(existing.URL) {
		parts = append(parts, "HTTP 地址改为 "+sanitizePetMCPURLForDisplay(config.URL))
	}
	if !sameStringSlices(sortedStringsCopy(config.EnvKeys), sortedStringKeys(existing.Env)) {
		parts = append(parts, "env 键名："+petMCPKeyChangeSummary(existing.Env, config.EnvKeys))
	}
	if !sameStringSlices(sortedStringsCopy(config.HeaderKeys), sortedStringKeys(existing.Headers)) {
		parts = append(parts, "header 键名："+petMCPKeyChangeSummary(existing.Headers, config.HeaderKeys))
	}
	if len(parts) == 0 {
		parts = append(parts, "无字段变化")
	}
	summary := fmt.Sprintf("宠物申请更新外部 MCP「%s」（%s）。改动：%s。批准后才写入。",
		truncateAssistantText(config.Name, 80), config.ServerID, strings.Join(parts, "；"))
	if petMCPEndpointChanged(existing, config.Transport, config.Command, config.Args, config.URL) {
		if count := petMCPExistingSecretCount(existing); count > 0 {
			summary += fmt.Sprintf(" 注意：端点已变更，现有 %d 个 env/header secret 不会自动沿用旧配置值；Secret Store 中已有的值仍会使用，其余需主人重新填写。", count)
		} else {
			summary += " 注意：端点已变更。"
		}
	}
	return truncateAssistantText(summary, 1900)
}

func petMCPArgsForSummary(args []string, item domain.MCPServer) string {
	if len(args) == 0 {
		return "(无)"
	}
	rendered := make([]string, 0, len(args))
	for _, arg := range args {
		value := redactPetMCPText(arg, item)
		if petMCPArgLooksLikeSecret(value) {
			value = "***"
		}
		rendered = append(rendered, value)
	}
	return truncateAssistantText(strings.Join(rendered, " "), 300)
}

func petMCPKeyChangeSummary(existing map[string]string, merged []string) string {
	before := map[string]bool{}
	for key := range existing {
		before[key] = true
	}
	after := map[string]bool{}
	for _, key := range merged {
		after[key] = true
	}
	added := make([]string, 0, len(after))
	for key := range after {
		if !before[key] {
			added = append(added, key)
		}
	}
	removed := make([]string, 0, len(before))
	for key := range before {
		if !after[key] {
			removed = append(removed, key)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	switch {
	case len(added) > 0 && len(removed) > 0:
		return "新增 " + strings.Join(added, ",") + "；移除 " + strings.Join(removed, ",")
	case len(added) > 0:
		return "新增 " + strings.Join(added, ",")
	case len(removed) > 0:
		return "移除 " + strings.Join(removed, ",")
	default:
		return "无变化"
	}
}

func petMCPExistingSecretCount(item domain.MCPServer) int {
	count := 0
	for _, value := range item.Env {
		if value != "" {
			count++
		}
	}
	for _, value := range item.Headers {
		if value != "" {
			count++
		}
	}
	return count
}

// petMCPEndpointChanged reports whether an update sends the server to a new
// destination. HTTP compares scheme+host; stdio compares command and args.
// Existing secret values are only reused when this is false.
func petMCPEndpointChanged(existing domain.MCPServer, transport, command string, args []string, rawURL string) bool {
	existingTransport := strings.ToLower(strings.TrimSpace(existing.Transport))
	nextTransport := strings.ToLower(strings.TrimSpace(transport))
	if existingTransport != nextTransport {
		return true
	}
	switch nextTransport {
	case "http":
		return petMCPURLHost(existing.URL) != petMCPURLHost(rawURL)
	case "stdio":
		return strings.TrimSpace(existing.Command) != strings.TrimSpace(command) ||
			!sameStringSlices(existing.Args, args)
	}
	return true
}

func petMCPURLHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}

func sortedStringsCopy(values []string) []string {
	if values == nil {
		return nil
	}
	copied := append([]string{}, values...)
	sort.Strings(copied)
	return copied
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

// Keys are base names with a trailing ".exe" removed before lookup, so both
// "bash" and "BASH.EXE" are rejected.
var petMCPBlockedStdioExecutables = map[string]bool{
	"cmd": true,
	"sh":  true, "bash": true, "dash": true, "zsh": true, "ksh": true, "fish": true,
	"powershell": true, "pwsh": true,
	"wsl": true, "cscript": true, "wscript": true,
	"mshta": true, "rundll32": true, "regsvr32": true,
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
	executable := strings.TrimSuffix(base, ".exe")
	if petMCPBlockedStdioExecutables[executable] {
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

func (s *Service) applyPetMCPSecretValues(item *domain.MCPServer, existing domain.MCPServer, envKeys, headerKeys []string, reuseExisting bool) {
	// The approval payload always carries the full desired key set, so an empty
	// list means "clear" rather than "leave unchanged". Values are reused from
	// the current row only when the endpoint did not change, or resolved from
	// the Secret Store; the payload never contains them.
	item.Env = make(map[string]string, len(envKeys))
	for _, key := range envKeys {
		if reuseExisting {
			if value, ok := existing.Env[key]; ok && value != "" {
				item.Env[key] = value
				continue
			}
		}
		if value, ok := s.petMCPSecret(petMCPEnvSecretRef, item.ID, key); ok {
			item.Env[key] = value
		}
	}
	item.Headers = make(map[string]string, len(headerKeys))
	for _, key := range headerKeys {
		if reuseExisting {
			if value, ok := existing.Headers[key]; ok && value != "" {
				item.Headers[key] = value
				continue
			}
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

func (s *Service) executePetMCPServerCreate(ctx context.Context, payload petMCPServerApprovalPayload, approvalID string) error {
	config := payload
	normalizePetMCPServerConfig(&config)
	if err := validatePetMCPServerConfig(config); err != nil {
		return err
	}
	item := petMCPServerFromConfig(config, domain.MCPServer{})
	s.applyPetMCPSecretValues(&item, domain.MCPServer{}, config.EnvKeys, config.HeaderKeys, false)
	if existing, err := s.MCPServerByID(ctx, config.ServerID); err == nil {
		if existing.Builtin {
			return fmt.Errorf("内置 MCP 不能被覆盖")
		}
		// Replay after a successful create is a no-op. Missing Secret Store
		// values mean the stored row may have fewer keys than the request;
		// compare the observable config and tolerate that subset instead of
		// failing an execution that already took effect.
		if petMCPServerRowsMatch(existing, item) ||
			(petMCPServerBaseConfigMatches(existing, item) &&
				petMCPStringSetSubset(sortedStringKeys(existing.Env), sortedStringKeys(item.Env)) &&
				petMCPStringSetSubset(sortedStringKeys(existing.Headers), sortedStringKeys(item.Headers))) {
			return nil
		}
		return fmt.Errorf("MCP %s 已存在且配置不同，不能重复创建", config.ServerID)
	}
	saved, err := s.SaveMCPServer(ctx, item)
	if err != nil {
		return err
	}
	s.auditPetMCPServer(ctx, "pet.mcp_server.create", "mcp_server", saved.ID, map[string]any{
		"server_id": saved.ID, "transport": saved.Transport,
		"env_keys": sortedStringKeys(saved.Env), "header_keys": sortedStringKeys(saved.Headers),
		"approval_id": approvalID, "requested_by": "pet",
	})
	s.recordPetWork(ctx, "mcp_server_created", saved.ID, "approved")
	return nil
}

func (s *Service) executePetMCPServerUpdate(ctx context.Context, payload petMCPServerUpdatePayload, approvalID string) error {
	existing, err := s.MCPServerByID(ctx, strings.TrimSpace(payload.ServerID))
	if err != nil {
		return fmt.Errorf("MCP 不存在")
	}
	if existing.Builtin {
		return fmt.Errorf("内置 MCP 不能修改")
	}
	config := petMCPServerConfigFromExisting(existing)
	config.RequestID = payload.RequestID
	applyPetMCPServerUpdateFields(&config, existing, payload.asInput())
	normalizePetMCPServerConfig(&config)
	if err := validatePetMCPServerConfig(config); err != nil {
		return err
	}
	desired := petMCPServerFromConfig(config, existing)
	reuseExisting := !petMCPEndpointChanged(existing, desired.Transport, desired.Command, desired.Args, desired.URL)
	s.applyPetMCPSecretValues(&desired, existing, config.EnvKeys, config.HeaderKeys, reuseExisting)
	if petMCPServerRowsMatch(existing, desired) {
		return nil
	}
	saved, err := s.SaveMCPServer(ctx, desired)
	if err != nil {
		return err
	}
	s.auditPetMCPServer(ctx, "pet.mcp_server.update", "mcp_server", saved.ID, map[string]any{
		"server_id": saved.ID, "transport": saved.Transport,
		"env_keys": sortedStringKeys(saved.Env), "header_keys": sortedStringKeys(saved.Headers),
		"approval_id": approvalID, "requested_by": "pet",
	})
	s.recordPetWork(ctx, "mcp_server_updated", saved.ID, "approved")
	return nil
}

func (s *Service) executePetMCPServerDelete(ctx context.Context, payload petMCPServerDeletePayload, approvalID string) error {
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
	s.auditPetMCPServer(ctx, "pet.mcp_server.delete", "mcp_server", id, map[string]any{
		"server_id": id, "approval_id": approvalID, "requested_by": "pet",
	})
	s.recordPetWork(ctx, "mcp_server_deleted", id, "approved")
	return nil
}

func (s *Service) executePetSetTaskMCPServers(ctx context.Context, payload petSetTaskMCPApprovalPayload, approvalID string) error {
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
	s.auditPetMCPServer(ctx, "pet.mcp_server.bind_task", "task", task.ID, map[string]any{
		"task_id": task.ID, "server_ids": ids, "approval_id": approvalID, "requested_by": "pet",
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

// petMCPServerRowsMatch compares the persisted configuration, including secret
// values. Key order and nil/empty maps do not matter, so replaying an executed
// approval is idempotent even when it was built before a normalization pass.
func petMCPServerRowsMatch(existing, desired domain.MCPServer) bool {
	return petMCPServerBaseConfigMatches(existing, desired) &&
		sameStringMaps(existing.Env, desired.Env) &&
		sameStringMaps(existing.Headers, desired.Headers)
}

func petMCPServerBaseConfigMatches(existing, desired domain.MCPServer) bool {
	if strings.TrimSpace(existing.Name) != strings.TrimSpace(desired.Name) ||
		strings.TrimSpace(existing.Description) != strings.TrimSpace(desired.Description) ||
		strings.ToLower(strings.TrimSpace(existing.Transport)) != strings.ToLower(strings.TrimSpace(desired.Transport)) ||
		strings.TrimSpace(existing.Command) != strings.TrimSpace(desired.Command) ||
		strings.TrimSpace(existing.URL) != strings.TrimSpace(desired.URL) ||
		existing.Enabled != desired.Enabled {
		return false
	}
	return sameStringSlices(existing.Args, desired.Args)
}

func sameStringMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func petMCPStringSetSubset(subset, superset []string) bool {
	if len(subset) == 0 {
		return true
	}
	values := make(map[string]bool, len(superset))
	for _, value := range superset {
		values[value] = true
	}
	for _, value := range subset {
		if !values[value] {
			return false
		}
	}
	return true
}

func (payload petMCPServerUpdatePayload) asInput() PetMCPServerUpdateInput {
	return PetMCPServerUpdateInput{
		ServerID: payload.ServerID, Name: payload.Name, Description: payload.Description,
		Transport: payload.Transport, Command: payload.Command, Args: payload.Args,
		URL: payload.URL, EnvKeys: payload.EnvKeys, HeaderKeys: payload.HeaderKeys,
		Enabled: payload.Enabled,
	}
}

func (s *Service) auditPetMCPServer(ctx context.Context, action, resourceType, resourceID string, details map[string]any) {
	// Attribute the write to the Owner who approved it, never to "pet" (which
	// is not an owner id). If no owner row exists yet, keep the event with an
	// empty owner id rather than dropping the audit trail.
	ownerID, _ := s.store.PrimaryOwnerID(ctx)
	_ = s.store.AppendAudit(ctx, strings.TrimSpace(ownerID), action, resourceType, resourceID, details, s.now().UTC().Format(time.RFC3339Nano))
}
