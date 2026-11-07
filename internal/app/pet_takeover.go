package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardwareconfig"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

// PetTakeoverWorkspaceInput mirrors the Web takeover payload. Taking over a
// read-only mirror creates a new local Workspace; the source device is never
// modified.
type PetTakeoverWorkspaceInput struct {
	WorkspaceID           string `json:"workspace_id"`
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

func (s *Service) PetTakeoverRemoteWorkspace(ctx context.Context, input PetTakeoverWorkspaceInput) (domain.Workspace, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.Workspace{}, err
	}
	if !assistant.PetAllowCreateWorkspaces {
		return domain.Workspace{}, fmt.Errorf("主人未开启主动管理项目/Workspace 权限")
	}
	source, err := s.store.Workspace(ctx, strings.TrimSpace(input.WorkspaceID))
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("Workspace 不存在")
	}
	if !source.ReadOnly || source.OwnerDeviceID == "" {
		return domain.Workspace{}, fmt.Errorf("只能接管其他设备的只读 Workspace 镜像")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = source.Name + " (本机)"
	}
	rootPath := strings.TrimSpace(input.RootPath)
	transport := strings.ToLower(strings.TrimSpace(input.Transport))
	if rootPath == "" || (transport != "native" && transport != "wsl" && transport != "ssh") {
		return domain.Workspace{}, fmt.Errorf("接管必须明确提供本机 root_path 和 transport")
	}
	locality := strings.TrimSpace(input.Locality)
	if locality == "" {
		locality = "local"
		if transport == "ssh" {
			locality = "remote"
		}
	}
	now := s.now().UTC()
	workspace := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: source.ProjectID, Name: name,
		Locality: locality, Transport: transport, RootPath: rootPath,
		SSHHost: strings.TrimSpace(input.SSHHost), SSHUser: strings.TrimSpace(input.SSHUser),
		SSHPort: input.SSHPort, Distro: strings.TrimSpace(input.Distro),
		Health: "unknown", CreatedAt: now, UpdatedAt: now,
	}
	if transport == "ssh" {
		if workspace.SSHPort == 0 {
			workspace.SSHPort = 22
		}
		workspace.SSHAuth = petNormalizeSSHAuth(input.SSHAuth)
		password := strings.TrimSpace(input.SSHPassword)
		if source.SSHPasswordConfigured && password == "" {
			if input.ReuseRemoteCredential && s.secrets != nil {
				password, _ = s.secrets.Get(syncer.MirrorWorkspaceSecretRef(source.ID))
			}
			if password == "" {
				return domain.Workspace{}, fmt.Errorf("请选择复用已同步的加密凭据，或重新提供本机 SSH 凭据")
			}
		}
		if workspace.SSHHost == "" || workspace.SSHUser == "" || workspace.SSHPort < 1 || workspace.SSHPort > 65535 {
			return domain.Workspace{}, fmt.Errorf("SSH Host、User 和有效 Port 不能为空")
		}
		if password != "" {
			if s.secrets == nil {
				return domain.Workspace{}, fmt.Errorf("密钥存储不可用")
			}
			workspace.SSHCredentialRef = "workspace/" + workspace.ID + "/ssh/credential"
			workspace.SSHPasswordConfigured = true
			if err := s.secrets.PutMany(map[string]string{workspace.SSHCredentialRef: password}); err != nil {
				return domain.Workspace{}, err
			}
		}
		if workspace.SSHAuth == "password" && !workspace.SSHPasswordConfigured {
			return domain.Workspace{}, fmt.Errorf("密码认证需要提供 SSH 密码")
		}
	}
	if err := s.store.CreateWorkspace(ctx, workspace); err != nil {
		if workspace.SSHCredentialRef != "" && s.secrets != nil {
			_ = s.secrets.DeleteMany([]string{workspace.SSHCredentialRef})
		}
		return domain.Workspace{}, err
	}
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: now, Actor: "pet", Kind: "workspace_takeover",
		Detail: workspace.ID, Status: source.ID,
	})
	return workspace, nil
}

// PetTakeoverTaskInput mirrors the Web takeover payload. Groups must confirm
// every hardware group of the remote Task before a local Task is created.
type PetTakeoverTaskInput struct {
	TaskID              string                        `json:"task_id"`
	WorkspaceID         string                        `json:"workspace_id"`
	Title               string                        `json:"title"`
	Request             string                        `json:"request"`
	Backend             string                        `json:"backend"`
	ModelSource         string                        `json:"model_source"`
	ModelID             string                        `json:"model_id"`
	WireModel           string                        `json:"wire_model"`
	CodexAccountID      string                        `json:"codex_account_id"`
	ReasoningEffort     string                        `json:"reasoning_effort"`
	StreamIdleTimeoutMS *int                          `json:"stream_idle_timeout_ms"`
	StreamMaxRetries    *int                          `json:"stream_max_retries"`
	Filesystem          string                        `json:"filesystem"`
	Approval            string                        `json:"approval"`
	ProxyEnabled        bool                          `json:"proxy_enabled"`
	FastMode            bool                          `json:"fast_mode"`
	StreamInput         bool                          `json:"stream_input"`
	CollaborationMode   string                        `json:"collaboration_mode"`
	MaxAgents           int                           `json:"max_agents"`
	Groups              []hardwareconfig.GroupPayload `json:"groups"`
}

func (s *Service) PetTakeoverRemoteTask(ctx context.Context, input PetTakeoverTaskInput) (domain.Task, []domain.HardwareGroup, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.Task{}, nil, err
	}
	if !assistant.PetAllowCreateTasks {
		return domain.Task{}, nil, fmt.Errorf("主人未开启主动创建/管理任务权限")
	}
	taskID := strings.TrimSpace(input.TaskID)
	if taskID == "" {
		return domain.Task{}, nil, fmt.Errorf("task_id 不能为空")
	}
	source, err := s.store.RemoteTaskMirror(ctx, taskID)
	if err != nil {
		return domain.Task{}, nil, fmt.Errorf("远程 Task 镜像不存在")
	}
	workspace, err := s.store.Workspace(ctx, strings.TrimSpace(input.WorkspaceID))
	if err != nil || workspace.ReadOnly || workspace.ProjectID != source.Task.ProjectID || strings.TrimSpace(workspace.RootPath) == "" {
		return domain.Task{}, nil, fmt.Errorf("接管 Task 必须选择同项目的本机可编辑 Workspace")
	}
	if len(input.Groups) != len(source.Hardware) {
		return domain.Task{}, nil, fmt.Errorf("必须逐项确认所有本机硬件配置")
	}
	rawByID := make(map[string]hardwareconfig.GroupPayload, len(input.Groups))
	for _, raw := range input.Groups {
		rawByID[hardwareconfig.NormalizeID(raw.ID)] = raw
	}
	for _, remote := range source.Hardware {
		raw, ok := rawByID[remote.ID]
		if !ok || remote.Serial.Device != "" && strings.TrimSpace(raw.Serial.Device) == "" || remote.Network.Host != "" && strings.TrimSpace(raw.Network.Host) == "" {
			return domain.Task{}, nil, fmt.Errorf("硬件组 %s 必须重新提供本机串口和网络端点", remote.ID)
		}
		if remote.PasswordConfigured && raw.Password == "" {
			if raw.ReuseRemoteCredential && s.secrets != nil {
				raw.Password, _ = s.secrets.Get(syncer.MirrorHardwareSecretRef(source.Task.OwnerDeviceID, source.SourceTaskID, remote.ID))
				rawByID[remote.ID] = raw
			}
			if raw.Password == "" {
				return domain.Task{}, nil, fmt.Errorf("硬件组 %s 请选择复用已同步的加密凭据，或重新提供本机凭据", remote.ID)
			}
		}
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = source.Task.Title + " (本机)"
	}
	goal := strings.TrimSpace(input.Request)
	if goal == "" {
		goal = strings.TrimSpace(source.Task.OriginalRequest)
	}
	if goal == "" {
		goal = "接管远端 Task 配置"
	}
	filesystem := strings.TrimSpace(input.Filesystem)
	if filesystem == "" {
		filesystem = "workspace-write"
	}
	approval := strings.TrimSpace(input.Approval)
	if approval == "" {
		approval = "never"
	}
	mode := strings.TrimSpace(input.CollaborationMode)
	if mode == "" {
		mode = source.Task.CollaborationMode
	}
	maxAgents := input.MaxAgents
	if maxAgents < 1 {
		maxAgents = source.Task.MaxAgents
	}
	created, err := s.CreateTask(ctx, CreateTaskInput{
		ProjectID: source.Task.ProjectID, WorkspaceID: workspace.ID, Title: title, Request: goal,
		Isolation: "inplace", Backend: input.Backend, ModelSource: input.ModelSource,
		ModelID: input.ModelID, WireModel: input.WireModel, CodexAccountID: input.CodexAccountID,
		ReasoningEffort:     input.ReasoningEffort,
		StreamIdleTimeoutMS: input.StreamIdleTimeoutMS, StreamMaxRetries: input.StreamMaxRetries,
		Filesystem: filesystem, Approval: approval,
		ProxyEnabled: input.ProxyEnabled, FastMode: input.FastMode, StreamInput: input.StreamInput,
		CollaborationMode: mode, MaxAgents: maxAgents,
		KnowledgePolicy: source.Task.KnowledgePolicy,
	})
	if err != nil {
		return domain.Task{}, nil, fmt.Errorf("接管 Task 失败：%w", err)
	}
	s.watchPetTask(ctx, created.ID, "takeover")
	now := s.now().UTC()
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
		group, normalizeErr := hardwareconfig.NormalizeGroup(created.ID, remote.ID, index, raw, domain.HardwareGroup{}, now)
		if normalizeErr != nil {
			s.cleanupFailedPetTakeover(ctx, created, secretValues)
			return domain.Task{}, nil, normalizeErr
		}
		if raw.Password != "" {
			if s.secrets == nil {
				s.cleanupFailedPetTakeover(ctx, created, secretValues)
				return domain.Task{}, nil, fmt.Errorf("密钥存储不可用")
			}
			group.CredentialRef = "hardware/" + created.ID + "/" + remote.ID + "/credential"
			group.PasswordConfigured = true
			secretValues[group.CredentialRef] = raw.Password
		}
		groups = append(groups, group)
	}
	if len(secretValues) > 0 {
		if err := s.secrets.PutMany(secretValues); err != nil {
			s.cleanupFailedPetTakeover(ctx, created, secretValues)
			return domain.Task{}, nil, err
		}
	}
	if err := s.store.ReplaceHardwareGroups(ctx, created.ID, groups); err != nil {
		s.cleanupFailedPetTakeover(ctx, created, secretValues)
		return domain.Task{}, nil, err
	}
	groups, _ = s.store.HardwareGroups(ctx, created.ID)
	s.recordPetWork(ctx, "task_takeover", created.ID, source.SourceTaskID)
	return created, groups, nil
}

func (s *Service) cleanupFailedPetTakeover(ctx context.Context, task domain.Task, values map[string]string) {
	if len(values) > 0 && s.secrets != nil {
		refs := make([]string, 0, len(values))
		for ref := range values {
			refs = append(refs, ref)
		}
		_ = s.secrets.DeleteMany(refs)
	}
	_ = s.store.DeleteTask(ctx, task.ID)
	if task.RuntimeConfigSnapshotID != "" {
		_ = s.store.DeleteRuntimeSnapshot(ctx, task.RuntimeConfigSnapshotID)
	}
}
