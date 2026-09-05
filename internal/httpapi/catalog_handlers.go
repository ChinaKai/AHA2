package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Server) listProjects(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListProjects(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_projects_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "projects": items})
}

func (s *Server) createProject(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Name               string `json:"name"`
		Description        string `json:"description"`
		ProjectType        string `json:"project_type"`
		RepositoryIdentity string `json:"repository_identity"`
		DefaultBranch      string `json:"default_branch"`
		KnowledgePolicy    string `json:"knowledge_policy"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	payload.Name = strings.TrimSpace(payload.Name)
	if payload.Name == "" {
		writeError(writer, http.StatusBadRequest, "project_name_required")
		return
	}
	projectType := strings.TrimSpace(payload.ProjectType)
	if projectType == "" {
		projectType = "folder"
	}
	if projectType != "folder" && projectType != "git" {
		writeError(writer, http.StatusBadRequest, "invalid_project_type")
		return
	}
	now := time.Now().UTC()
	item := domain.Project{
		ID: domain.NewID("project"), Name: payload.Name, Description: strings.TrimSpace(payload.Description),
		ProjectType:        projectType,
		RepositoryIdentity: strings.TrimSpace(payload.RepositoryIdentity), DefaultBranch: strings.TrimSpace(payload.DefaultBranch),
		KnowledgePolicy: normalizeProjectKnowledgePolicy(payload.KnowledgePolicy),
		CreatedAt:       now, UpdatedAt: now,
	}
	if err := s.store.CreateProject(request.Context(), item); err != nil {
		writeError(writer, http.StatusInternalServerError, "create_project_failed")
		return
	}
	s.audit(request, "project.create", "project", item.ID, map[string]any{"name": item.Name, "project_type": item.ProjectType})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "project": item})
}

func (s *Server) updateProject(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	existing, err := s.store.Project(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "project_not_found")
		return
	}
	var payload struct {
		Name               string `json:"name"`
		Description        string `json:"description"`
		ProjectType        string `json:"project_type"`
		RepositoryIdentity string `json:"repository_identity"`
		DefaultBranch      string `json:"default_branch"`
		KnowledgePolicy    string `json:"knowledge_policy"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		writeError(writer, http.StatusBadRequest, "project_name_required")
		return
	}
	projectType := strings.TrimSpace(payload.ProjectType)
	if projectType == "" {
		projectType = "folder"
	}
	if projectType != "folder" && projectType != "git" {
		writeError(writer, http.StatusBadRequest, "invalid_project_type")
		return
	}
	existing.Name = name
	existing.Description = strings.TrimSpace(payload.Description)
	existing.ProjectType = projectType
	existing.RepositoryIdentity = strings.TrimSpace(payload.RepositoryIdentity)
	existing.DefaultBranch = strings.TrimSpace(payload.DefaultBranch)
	existing.KnowledgePolicy = normalizeProjectKnowledgePolicy(payload.KnowledgePolicy)
	existing.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateProject(request.Context(), existing); err != nil {
		writeError(writer, http.StatusInternalServerError, "update_project_failed")
		return
	}
	s.audit(request, "project.update", "project", id, map[string]any{"project_type": existing.ProjectType})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "project": existing})
}

func normalizeProjectKnowledgePolicy(value string) string {
	if strings.TrimSpace(value) == "disabled" {
		return "disabled"
	}
	return "enabled"
}

func (s *Server) listWorkspaces(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListWorkspaces(request.Context(), request.URL.Query().Get("project_id"))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_workspaces_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "workspaces": items})
}

func (s *Server) createWorkspace(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		ProjectID        string `json:"project_id"`
		Name             string `json:"name"`
		Locality         string `json:"locality"`
		Transport        string `json:"transport"`
		RootPath         string `json:"root_path"`
		SSHHost          string `json:"ssh_host"`
		SSHUser          string `json:"ssh_user"`
		SSHPort          int    `json:"ssh_port"`
		SSHAuth          string `json:"ssh_auth"`
		SSHPassword      string `json:"ssh_password"`
		ClearSSHPassword bool   `json:"clear_ssh_password"`
		Distro           string `json:"distro"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if _, err := s.store.Project(request.Context(), payload.ProjectID); err != nil {
		writeError(writer, http.StatusBadRequest, "project_not_found")
		return
	}
	if payload.SSHPort == 0 {
		payload.SSHPort = 22
	}
	if payload.Locality == "" {
		payload.Locality = "local"
	}
	if payload.Transport == "" {
		payload.Transport = "native"
	}
	if strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.RootPath) == "" {
		writeError(writer, http.StatusBadRequest, "workspace_name_and_path_required")
		return
	}
	now := time.Now().UTC()
	item := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: payload.ProjectID, Name: strings.TrimSpace(payload.Name),
		Locality: payload.Locality, Transport: payload.Transport, RootPath: strings.TrimSpace(payload.RootPath),
		SSHHost: strings.TrimSpace(payload.SSHHost), SSHUser: strings.TrimSpace(payload.SSHUser), SSHPort: payload.SSHPort,
		Distro: strings.TrimSpace(payload.Distro),
		Health: "unknown", CreatedAt: now, UpdatedAt: now,
	}
	if item.Transport == "ssh" {
		item.SSHAuth = normalizeWorkspaceSSHAuth(payload.SSHAuth)
		if payload.SSHPassword != "" {
			if s.secrets == nil {
				writeError(writer, http.StatusInternalServerError, "secret_store_unavailable")
				return
			}
			item.SSHCredentialRef = workspaceSSHCredentialRef(item.ID)
			item.SSHPasswordConfigured = true
		}
		if err := validateWorkspaceSSH(item); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{
				"ok": false, "error": "workspace_ssh_invalid", "message": err.Error(),
			})
			return
		}
		if payload.SSHPassword != "" {
			if err := s.secrets.PutMany(map[string]string{item.SSHCredentialRef: payload.SSHPassword}); err != nil {
				writeError(writer, http.StatusInternalServerError, "store_secrets_failed")
				return
			}
		}
	}
	if err := s.store.CreateWorkspace(request.Context(), item); err != nil {
		if item.SSHCredentialRef != "" && s.secrets != nil {
			_ = s.secrets.DeleteMany([]string{item.SSHCredentialRef})
		}
		writeError(writer, http.StatusInternalServerError, "create_workspace_failed")
		return
	}
	s.audit(request, "workspace.create", "workspace", item.ID, map[string]any{"transport": item.Transport})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "workspace": item})
}

func (s *Server) updateWorkspace(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	existing, err := s.store.Workspace(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "workspace_not_found")
		return
	}
	var payload struct {
		Name             string `json:"name"`
		Locality         string `json:"locality"`
		Transport        string `json:"transport"`
		RootPath         string `json:"root_path"`
		SSHHost          string `json:"ssh_host"`
		SSHUser          string `json:"ssh_user"`
		SSHPort          int    `json:"ssh_port"`
		SSHAuth          string `json:"ssh_auth"`
		SSHPassword      string `json:"ssh_password"`
		ClearSSHPassword bool   `json:"clear_ssh_password"`
		Distro           string `json:"distro"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	name := strings.TrimSpace(payload.Name)
	rootPath := strings.TrimSpace(payload.RootPath)
	if name == "" || rootPath == "" {
		writeError(writer, http.StatusBadRequest, "workspace_name_and_path_required")
		return
	}
	if payload.Transport == "" {
		payload.Transport = "native"
	}
	if payload.Locality == "" {
		payload.Locality = "local"
	}
	if payload.SSHPort == 0 {
		payload.SSHPort = 22
	}
	existing.Name = name
	existing.Locality = payload.Locality
	existing.Transport = payload.Transport
	existing.RootPath = rootPath
	existing.SSHHost = strings.TrimSpace(payload.SSHHost)
	existing.SSHUser = strings.TrimSpace(payload.SSHUser)
	existing.SSHPort = payload.SSHPort
	existing.Distro = strings.TrimSpace(payload.Distro)
	oldCredentialRef := existing.SSHCredentialRef
	oldPassword, hadOldPassword := "", false
	if oldCredentialRef != "" && s.secrets != nil {
		oldPassword, hadOldPassword = s.secrets.Get(oldCredentialRef)
	}
	wroteCredential := false
	if existing.Transport == "ssh" {
		existing.SSHAuth = normalizeWorkspaceSSHAuth(payload.SSHAuth)
		if payload.ClearSSHPassword {
			existing.SSHCredentialRef = ""
			existing.SSHPasswordConfigured = false
		} else if payload.SSHPassword != "" {
			if s.secrets == nil {
				writeError(writer, http.StatusInternalServerError, "secret_store_unavailable")
				return
			}
			existing.SSHCredentialRef = workspaceSSHCredentialRef(existing.ID)
			existing.SSHPasswordConfigured = true
		}
		if err := validateWorkspaceSSH(existing); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{
				"ok": false, "error": "workspace_ssh_invalid", "message": err.Error(),
			})
			return
		}
		if payload.SSHPassword != "" && !payload.ClearSSHPassword {
			if err := s.secrets.PutMany(map[string]string{existing.SSHCredentialRef: payload.SSHPassword}); err != nil {
				writeError(writer, http.StatusInternalServerError, "store_secrets_failed")
				return
			}
			wroteCredential = true
		}
	} else {
		existing.SSHAuth = ""
		existing.SSHCredentialRef = ""
		existing.SSHPasswordConfigured = false
	}
	existing.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateWorkspaceConfig(request.Context(), existing); err != nil {
		if wroteCredential && s.secrets != nil {
			if oldCredentialRef == existing.SSHCredentialRef && hadOldPassword {
				_ = s.secrets.PutMany(map[string]string{oldCredentialRef: oldPassword})
			} else {
				_ = s.secrets.DeleteMany([]string{existing.SSHCredentialRef})
			}
		}
		writeError(writer, http.StatusInternalServerError, "update_workspace_failed")
		return
	}
	if oldCredentialRef != "" && oldCredentialRef != existing.SSHCredentialRef && s.secrets != nil {
		_ = s.secrets.DeleteMany([]string{oldCredentialRef})
	}
	s.audit(request, "workspace.update", "workspace", id, map[string]any{"transport": existing.Transport})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "workspace": existing})
}

func (s *Server) detectWorkspaceHandler(writer http.ResponseWriter, request *http.Request) {
	if s.detectWorkspace == nil {
		writeError(writer, http.StatusNotImplemented, "workspace_detection_unavailable")
		return
	}
	item, err := s.store.Workspace(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "workspace_not_found")
		return
	}
	if item.SSHCredentialRef != "" && s.secrets != nil {
		item.SSHPassword, _ = s.secrets.Get(item.SSHCredentialRef)
	}
	detected, err := s.detectWorkspace(request.Context(), item)
	if err != nil {
		item.Health = "error"
		item.Capabilities = map[string]any{"error": err.Error()}
		item.LastDetectedAt, item.UpdatedAt = time.Now().UTC(), time.Now().UTC()
		_ = s.store.UpdateWorkspaceDetection(request.Context(), item)
		writeJSON(writer, http.StatusBadGateway, map[string]any{"ok": false, "error": "workspace_detection_failed", "message": err.Error(), "workspace": item})
		return
	}
	if err := s.store.UpdateWorkspaceDetection(request.Context(), detected); err != nil {
		writeError(writer, http.StatusInternalServerError, "workspace_detection_store_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "workspace": detected})
}

func workspaceSSHCredentialRef(workspaceID string) string {
	return "workspace/" + workspaceID + "/ssh/credential"
}

func normalizeWorkspaceSSHAuth(value string) string {
	switch strings.TrimSpace(value) {
	case "password":
		return "password"
	case "key":
		return "key"
	default:
		return "auto"
	}
}

func validateWorkspaceSSH(item domain.Workspace) error {
	if item.SSHHost == "" {
		return fmt.Errorf("SSH Host 不能为空")
	}
	if item.SSHUser == "" {
		return fmt.Errorf("SSH User 不能为空")
	}
	if item.SSHPort < 1 || item.SSHPort > 65535 {
		return fmt.Errorf("SSH Port 必须在 1 到 65535 之间")
	}
	if item.SSHAuth == "password" && !item.SSHPasswordConfigured {
		return fmt.Errorf("密码认证需要配置 SSH 密码")
	}
	return nil
}

func (s *Server) listModels(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListModels(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_models_failed")
		return
	}
	visible := make([]domain.Model, 0, len(items))
	for _, item := range items {
		if item.Source != domain.ModelSourceOfficial {
			visible = append(visible, item)
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "models": visible})
}

func (s *Server) createModel(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		DisplayName       string `json:"display_name"`
		ProviderID        string `json:"provider_id"`
		Backend           string `json:"backend"`
		WireModel         string `json:"wire_model"`
		WireAPI           string `json:"wire_api"`
		ContextWindow     int64  `json:"context_window"`
		MaxOutputTokens   int64  `json:"max_output_tokens"`
		DefaultEffort     string `json:"default_reasoning_effort"`
		DefaultEnvGroupID string `json:"default_env_group_id"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	payload.DisplayName = strings.TrimSpace(payload.DisplayName)
	payload.ProviderID = strings.TrimSpace(payload.ProviderID)
	payload.Backend = strings.TrimSpace(payload.Backend)
	payload.WireModel = strings.TrimSpace(payload.WireModel)
	if payload.DisplayName == "" || payload.ProviderID == "" || payload.Backend == "" || payload.WireModel == "" {
		writeError(writer, http.StatusBadRequest, "model_fields_required")
		return
	}
	now := time.Now().UTC()
	item := domain.Model{
		ID: domain.NewID("model"), DisplayName: payload.DisplayName, ProviderID: payload.ProviderID,
		Backend: payload.Backend, WireModel: payload.WireModel, WireAPI: strings.TrimSpace(payload.WireAPI),
		ContextWindow: payload.ContextWindow, MaxOutputTokens: payload.MaxOutputTokens,
		DefaultEffort: strings.TrimSpace(payload.DefaultEffort), DefaultEnvGroupID: strings.TrimSpace(payload.DefaultEnvGroupID),
		Capabilities: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	if item.DefaultEnvGroupID != "" {
		group, err := s.store.EnvGroup(request.Context(), item.DefaultEnvGroupID)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "env_group_not_found"})
			return
		}
		if group.Backend != "" && group.Backend != item.Backend {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "env_group_backend_mismatch"})
			return
		}
	}
	if err := s.store.UpsertModel(request.Context(), item); err != nil {
		writeError(writer, http.StatusInternalServerError, "create_model_failed")
		return
	}
	s.audit(request, "model.create", "model", item.ID, map[string]any{"backend": item.Backend, "provider_id": item.ProviderID})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "model": item})
}

func (s *Server) listEnvGroups(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListEnvGroups(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_env_groups_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "env_groups": items})
}

func (s *Server) createEnvGroup(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Name        string            `json:"name"`
		ProviderID  string            `json:"provider_id"`
		Backend     string            `json:"backend"`
		Environment map[string]string `json:"environment"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	payload.Name = strings.TrimSpace(payload.Name)
	payload.ProviderID = strings.TrimSpace(payload.ProviderID)
	payload.Backend = strings.TrimSpace(payload.Backend)
	if payload.Name == "" || payload.ProviderID == "" || payload.Backend == "" {
		writeError(writer, http.StatusBadRequest, "env_group_fields_required")
		return
	}
	now := time.Now().UTC()
	item := domain.EnvGroup{
		ID: domain.NewID("env"), Name: payload.Name, ProviderID: payload.ProviderID, Backend: payload.Backend,
		Revision: 1, Environment: payload.Environment, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.UpsertEnvGroup(request.Context(), item); err != nil {
		writeError(writer, http.StatusInternalServerError, "create_env_group_failed")
		return
	}
	s.audit(request, "env_group.create", "env_group", item.ID, map[string]any{"backend": item.Backend})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "env_group": item})
}

func (s *Server) audit(request *http.Request, action, resourceType, resourceID string, data map[string]any) {
	actor := ""
	if session, ok := sessionFromContext(request.Context()); ok {
		actor = session.OwnerID
	} else if claims, ok := agentClaimsFromContext(request.Context()); ok {
		actor = "agent:" + claims.AgentID
	}
	_ = s.store.AppendAudit(request.Context(), actor, action, resourceType, resourceID, data, time.Now().UTC().Format(time.RFC3339Nano))
}

func queryInt(request *http.Request, key string, fallback int64) int64 {
	value, err := strconv.ParseInt(request.URL.Query().Get(key), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}
