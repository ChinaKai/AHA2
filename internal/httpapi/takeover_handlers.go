package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

type workspaceTakeoverPayload struct {
	Name                  string `json:"name"`
	Locality              string `json:"locality"`
	Transport             string `json:"transport"`
	RootPath              string `json:"root_path"`
	SSHHost               string `json:"ssh_host"`
	SSHUser               string `json:"ssh_user"`
	SSHPort               int    `json:"ssh_port"`
	SSHAuth               string `json:"ssh_auth"`
	SSHPassword           string `json:"ssh_password"`
	ReuseRemoteCredential bool   `json:"reuse_remote_credential"`
	Distro                string `json:"distro"`
}

func (s *Server) takeoverWorkspace(writer http.ResponseWriter, request *http.Request) {
	source, err := s.store.Workspace(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "workspace_not_found")
		return
	}
	if !source.ReadOnly || source.OwnerDeviceID == "" {
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "workspace_not_remote", "message": "只能接管其他设备的只读 Workspace 镜像"})
		return
	}
	var payload workspaceTakeoverPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	payload.Name = strings.TrimSpace(payload.Name)
	payload.RootPath = strings.TrimSpace(payload.RootPath)
	payload.Transport = strings.TrimSpace(payload.Transport)
	if payload.Name == "" {
		payload.Name = source.Name + " (本机)"
	}
	if payload.RootPath == "" || (payload.Transport != "native" && payload.Transport != "wsl" && payload.Transport != "ssh") {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "local_workspace_fields_required", "message": "接管必须明确提供本机 root_path 和 transport"})
		return
	}
	if payload.Locality == "" {
		payload.Locality = "local"
		if payload.Transport == "ssh" {
			payload.Locality = "remote"
		}
	}
	now := time.Now().UTC()
	item := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: source.ProjectID, Name: payload.Name,
		Locality: payload.Locality, Transport: payload.Transport, RootPath: payload.RootPath,
		SSHHost: strings.TrimSpace(payload.SSHHost), SSHUser: strings.TrimSpace(payload.SSHUser), SSHPort: payload.SSHPort,
		Distro: strings.TrimSpace(payload.Distro), Health: "unknown", CreatedAt: now, UpdatedAt: now,
	}
	if item.Transport == "ssh" {
		if item.SSHPort == 0 {
			item.SSHPort = 22
		}
		item.SSHAuth = normalizeWorkspaceSSHAuth(payload.SSHAuth)
		if source.SSHPasswordConfigured && payload.SSHPassword == "" {
			if payload.ReuseRemoteCredential && s.secrets != nil {
				payload.SSHPassword, _ = s.secrets.Get(syncer.MirrorWorkspaceSecretRef(source.ID))
			}
			if payload.SSHPassword == "" {
				writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "local_ssh_credential_required", "message": "请选择复用已同步的加密凭据，或重新提供本机 SSH 凭据"})
				return
			}
		}
		if payload.SSHPassword != "" {
			if s.secrets == nil {
				writeError(writer, http.StatusInternalServerError, "secret_store_unavailable")
				return
			}
			item.SSHCredentialRef = workspaceSSHCredentialRef(item.ID)
			item.SSHPasswordConfigured = true
		}
		if err := validateWorkspaceSSH(item); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "workspace_ssh_invalid", "message": err.Error()})
			return
		}
	}
	if item.SSHCredentialRef != "" {
		if err := s.secrets.PutMany(map[string]string{item.SSHCredentialRef: payload.SSHPassword}); err != nil {
			writeError(writer, http.StatusInternalServerError, "store_secrets_failed")
			return
		}
	}
	if err := s.store.CreateWorkspace(request.Context(), item); err != nil {
		if item.SSHCredentialRef != "" && s.secrets != nil {
			_ = s.secrets.DeleteMany([]string{item.SSHCredentialRef})
		}
		writeError(writer, http.StatusInternalServerError, "workspace_takeover_failed")
		return
	}
	s.audit(request, "workspace.takeover", "workspace", item.ID, map[string]any{"source_workspace_id": source.ID, "owner_device_id": source.OwnerDeviceID})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "workspace": item, "source_workspace_id": source.ID})
}

type taskTakeoverPayload struct {
	WorkspaceID       string                 `json:"workspace_id"`
	Title             string                 `json:"title"`
	Request           string                 `json:"request"`
	Backend           string                 `json:"backend"`
	ModelSource       string                 `json:"model_source"`
	ModelID           string                 `json:"model_id"`
	WireModel         string                 `json:"wire_model"`
	CodexAccountID    string                 `json:"codex_account_id"`
	ReasoningEffort   string                 `json:"reasoning_effort"`
	Filesystem        string                 `json:"filesystem"`
	Approval          string                 `json:"approval"`
	ProxyEnabled      bool                   `json:"proxy_enabled"`
	CollaborationMode string                 `json:"collaboration_mode"`
	MaxAgents         int                    `json:"max_agents"`
	Groups            []hardwareGroupPayload `json:"groups"`
}

func (s *Server) takeoverTask(writer http.ResponseWriter, request *http.Request) {
	source, err := s.store.RemoteTaskMirror(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "remote_task_not_found")
		return
	}
	var payload taskTakeoverPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	workspace, err := s.store.Workspace(request.Context(), strings.TrimSpace(payload.WorkspaceID))
	if err != nil || workspace.ReadOnly || workspace.ProjectID != source.Task.ProjectID || strings.TrimSpace(workspace.RootPath) == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "local_workspace_required", "message": "接管 Task 必须选择同项目的本机可编辑 Workspace"})
		return
	}
	if len(payload.Groups) != len(source.Hardware) {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "local_hardware_confirmation_required", "message": "必须逐项确认所有本机硬件配置"})
		return
	}
	rawByID := make(map[string]hardwareGroupPayload, len(payload.Groups))
	for _, raw := range payload.Groups {
		rawByID[normalizeHardwareID(raw.ID)] = raw
	}
	for _, remote := range source.Hardware {
		raw, ok := rawByID[remote.ID]
		if !ok || remote.Serial.Device != "" && strings.TrimSpace(raw.Serial.Device) == "" || remote.Network.Host != "" && strings.TrimSpace(raw.Network.Host) == "" {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "local_hardware_fields_required", "message": fmt.Sprintf("硬件组 %s 必须重新提供本机串口和网络端点", remote.ID)})
			return
		}
		if remote.PasswordConfigured && raw.Password == "" {
			if raw.ReuseRemoteCredential && s.secrets != nil {
				raw.Password, _ = s.secrets.Get(syncer.MirrorHardwareSecretRef(source.Task.OwnerDeviceID, source.SourceTaskID, remote.ID))
				rawByID[remote.ID] = raw
			}
			if raw.Password == "" {
				writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "local_hardware_credential_required", "message": fmt.Sprintf("硬件组 %s 请选择复用已同步的加密凭据，或重新提供本机凭据", remote.ID)})
				return
			}
		}
	}
	if s.app == nil {
		writeError(writer, http.StatusServiceUnavailable, "task_service_unavailable")
		return
	}
	title := strings.TrimSpace(payload.Title)
	if title == "" {
		title = source.Task.Title + " (本机)"
	}
	goal := strings.TrimSpace(payload.Request)
	if goal == "" {
		goal = strings.TrimSpace(source.Task.OriginalRequest)
	}
	if goal == "" {
		goal = "接管远端 Task 配置"
	}
	filesystem := strings.TrimSpace(payload.Filesystem)
	if filesystem == "" {
		filesystem = "workspace-write"
	}
	approval := strings.TrimSpace(payload.Approval)
	if approval == "" {
		approval = "never"
	}
	mode := strings.TrimSpace(payload.CollaborationMode)
	if mode == "" {
		mode = source.Task.CollaborationMode
	}
	maxAgents := payload.MaxAgents
	if maxAgents < 1 {
		maxAgents = source.Task.MaxAgents
	}
	created, err := s.app.CreateTask(request.Context(), app.CreateTaskInput{
		ProjectID: source.Task.ProjectID, WorkspaceID: workspace.ID, Title: title, Request: goal,
		Isolation: "inplace", Backend: payload.Backend, ModelSource: payload.ModelSource,
		ModelID: payload.ModelID, WireModel: payload.WireModel, CodexAccountID: payload.CodexAccountID,
		ReasoningEffort: payload.ReasoningEffort, Filesystem: filesystem, Approval: approval,
		ProxyEnabled: payload.ProxyEnabled, CollaborationMode: mode, MaxAgents: maxAgents,
		KnowledgePolicy: source.Task.KnowledgePolicy,
	})
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "task_takeover_failed", "message": err.Error()})
		return
	}
	groups := make([]domain.HardwareGroup, 0, len(source.Hardware))
	secretValues := map[string]string{}
	for index, remote := range source.Hardware {
		raw := rawByID[remote.ID]
		if strings.TrimSpace(raw.Description) == "" {
			raw.Description = remote.Description
		}
		if strings.TrimSpace(raw.Mode) == "" {
			raw.Mode = remote.Mode
		}
		if raw.Serial.Baudrate == 0 {
			raw.Serial.Baudrate = remote.Serial.Baudrate
		}
		if raw.Network.Port == 0 {
			raw.Network.Port = remote.Network.Port
		}
		if raw.Network.Protocol == "" {
			raw.Network.Protocol = remote.Network.Protocol
		}
		if raw.Network.SSHAuth == "" {
			raw.Network.SSHAuth = remote.Network.SSHAuth
		}
		if raw.Username == "" {
			raw.Username = remote.Username
		}
		raw.Access = domain.HardwareAccessReadWrite
		group, normalizeErr := normalizeHardwareGroup(created.ID, remote.ID, index, raw, domain.HardwareGroup{}, time.Now().UTC())
		if normalizeErr != nil {
			s.cleanupFailedTakeover(request, created, secretValues)
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "local_hardware_invalid", "message": normalizeErr.Error()})
			return
		}
		if raw.Password != "" {
			if s.secrets == nil {
				s.cleanupFailedTakeover(request, created, secretValues)
				writeError(writer, http.StatusInternalServerError, "secret_store_unavailable")
				return
			}
			group.CredentialRef = "hardware/" + created.ID + "/" + remote.ID + "/credential"
			group.PasswordConfigured = true
			secretValues[group.CredentialRef] = raw.Password
		}
		groups = append(groups, group)
	}
	if len(secretValues) > 0 {
		if err := s.secrets.PutMany(secretValues); err != nil {
			s.cleanupFailedTakeover(request, created, secretValues)
			writeError(writer, http.StatusInternalServerError, "store_secrets_failed")
			return
		}
	}
	if err := s.store.ReplaceHardwareGroups(request.Context(), created.ID, groups); err != nil {
		s.cleanupFailedTakeover(request, created, secretValues)
		writeError(writer, http.StatusInternalServerError, "hardware_takeover_failed")
		return
	}
	s.audit(request, "task.takeover", "task", created.ID, map[string]any{"source_task_id": source.SourceTaskID, "owner_device_id": source.Task.OwnerDeviceID})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "task": created, "hardware": groups, "source_task_id": source.SourceTaskID})
}

func (s *Server) cleanupFailedTakeover(request *http.Request, task domain.Task, secrets map[string]string) {
	if len(secrets) > 0 && s.secrets != nil {
		refs := make([]string, 0, len(secrets))
		for ref := range secrets {
			refs = append(refs, ref)
		}
		_ = s.secrets.DeleteMany(refs)
	}
	_ = s.store.DeleteTask(request.Context(), task.ID)
	if task.RuntimeConfigSnapshotID != "" {
		_ = s.store.DeleteRuntimeSnapshot(request.Context(), task.RuntimeConfigSnapshotID)
	}
}
