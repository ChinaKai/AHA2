package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type PetPermissionsView struct {
	ViewTasks        bool `json:"view_tasks"`
	CreateTasks      bool `json:"create_tasks"`
	CreateWorkspaces bool `json:"create_workspaces"`
	MessageTasks     bool `json:"message_tasks"`
	ManageModels     bool `json:"manage_models"`
	ManageKnowledge  bool `json:"manage_knowledge"`
	ManageChannels   bool `json:"manage_channels"`
	ManageSystem     bool `json:"manage_system"`
}

func (s *Service) PetPermissions(ctx context.Context) (PetPermissionsView, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return PetPermissionsView{}, err
	}
	return petPermissionsView(assistant), nil
}

func petPermissionsView(assistant domain.OwnerAssistant) PetPermissionsView {
	return PetPermissionsView{
		ViewTasks: assistant.PetAllowViewTasks, CreateTasks: assistant.PetAllowCreateTasks,
		CreateWorkspaces: assistant.PetAllowCreateWorkspaces, MessageTasks: assistant.PetAllowMessageTasks,
		ManageModels: assistant.PetAllowManageModels, ManageKnowledge: assistant.PetAllowManageKnowledge,
		ManageChannels: assistant.PetAllowManageChannels, ManageSystem: assistant.PetAllowManageSystem,
	}
}

var petPermissionLabels = map[string]string{
	"view_tasks":        "查看其他任务信息",
	"create_tasks":      "主动创建/管理任务",
	"create_workspaces": "主动管理项目/Workspace",
	"message_tasks":     "给其他任务发消息",
	"manage_models":     "管理模型/Provider",
	"manage_knowledge":  "管理知识/Skill/Prompt",
	"manage_channels":   "管理渠道",
	"manage_system":     "管理系统设置",
}

func normalizePetPermission(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if _, ok := petPermissionLabels[value]; ok {
		return value
	}
	return ""
}

type PetPermissionChangeInput struct {
	Permission string `json:"permission"`
	Enabled    bool   `json:"enabled"`
	Reason     string `json:"reason"`
}

func (s *Service) PetRequestPermissionChange(ctx context.Context, input PetPermissionChangeInput) (domain.PetApproval, error) {
	permission := normalizePetPermission(input.Permission)
	if permission == "" {
		return domain.PetApproval{}, fmt.Errorf("未知的权限名称")
	}
	reason, reasonErr := requirePetApprovalReason(input.Reason)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	if len([]rune(reason)) > 1000 {
		return domain.PetApproval{}, fmt.Errorf("reason 不能超过 1000 个字符")
	}
	action := "开启"
	if !input.Enabled {
		action = "关闭"
	}
	payload, _ := json.Marshal(map[string]any{"permission": permission, "enabled": input.Enabled})
	summary := fmt.Sprintf("宠物申请%s权限「%s」。", action, petPermissionLabels[permission])
	if reason != "" {
		summary += " 原因：" + reason
	}
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "permission_change", Title: action + "宠物权限：" + petPermissionLabels[permission],
		Summary: summary, Reason: reason, Risk: "high", Payload: string(payload),
	})
}

// applyPetPermissionChange is called only after an owner approval. It updates
// one switch and leaves the current session untouched.
func (s *Service) applyPetPermissionChange(ctx context.Context, permission string, enabled bool) error {
	permission = normalizePetPermission(permission)
	if permission == "" {
		return fmt.Errorf("未知的权限名称")
	}
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return err
	}
	switch permission {
	case "view_tasks":
		assistant.PetAllowViewTasks = enabled
	case "create_tasks":
		assistant.PetAllowCreateTasks = enabled
	case "create_workspaces":
		assistant.PetAllowCreateWorkspaces = enabled
	case "message_tasks":
		assistant.PetAllowMessageTasks = enabled
	case "manage_models":
		assistant.PetAllowManageModels = enabled
	case "manage_knowledge":
		assistant.PetAllowManageKnowledge = enabled
	case "manage_channels":
		assistant.PetAllowManageChannels = enabled
	case "manage_system":
		assistant.PetAllowManageSystem = enabled
	}
	updatedAt := s.now().UTC()
	if err := s.store.UpdateOwnerPetPermissions(ctx, assistant.ID,
		assistant.PetAllowViewTasks, assistant.PetAllowCreateTasks,
		assistant.PetAllowCreateWorkspaces, assistant.PetAllowMessageTasks,
		assistant.PetAllowManageModels, assistant.PetAllowManageKnowledge,
		assistant.PetAllowManageChannels, assistant.PetAllowManageSystem,
		updatedAt); err != nil {
		return err
	}
	status := "disabled"
	if enabled {
		status = "enabled"
	}
	s.recordPetWork(ctx, "permission_changed", permission, status)
	return nil
}
