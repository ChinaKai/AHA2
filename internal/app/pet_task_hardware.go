package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type PetTaskHardwareGroupBrief struct {
	ID                 string `json:"id"`
	ResourceID         string `json:"resource_id,omitempty"`
	Position           int    `json:"position"`
	Description        string `json:"description"`
	Mode               string `json:"mode"`
	Username           string `json:"username,omitempty"`
	PasswordConfigured bool   `json:"password_configured"`
}

type PetTaskHardwareView struct {
	Bindings []domain.TaskHardwareBinding `json:"bindings"`
	Groups   []PetTaskHardwareGroupBrief  `json:"groups"`
}

func (s *Service) PetHardwareResources(ctx context.Context) ([]PetHardwareResourceView, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return nil, err
	}
	if !assistant.PetAllowViewTasks {
		return nil, fmt.Errorf("主人未开启查看 AHA 信息权限")
	}
	items, err := s.store.HardwareResources(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PetHardwareResourceView, 0, len(items))
	for _, item := range items {
		result = append(result, petHardwareResourceView(item))
	}
	return result, nil
}

func (s *Service) PetTaskHardware(ctx context.Context, taskID string) (PetTaskHardwareView, error) {
	task, err := s.petViewableTask(ctx, taskID)
	if err != nil {
		return PetTaskHardwareView{}, err
	}
	bindings, err := s.store.TaskHardwareBindings(ctx, task.ID)
	if err != nil {
		return PetTaskHardwareView{}, err
	}
	groups, err := s.store.HardwareGroups(ctx, task.ID)
	if err != nil {
		return PetTaskHardwareView{}, err
	}
	view := PetTaskHardwareView{Bindings: bindings, Groups: make([]PetTaskHardwareGroupBrief, 0, len(groups))}
	for _, group := range groups {
		view.Groups = append(view.Groups, PetTaskHardwareGroupBrief{
			ID: group.ID, ResourceID: group.ResourceID, Position: group.Position,
			Description: group.Description, Mode: group.Mode, Username: group.Username,
			PasswordConfigured: group.PasswordConfigured,
		})
	}
	return view, nil
}

func (s *Service) PetRequestDeleteHardwareResource(ctx context.Context, resourceID string, reason ...string) (domain.PetApproval, error) {
	reasonText, reasonErr := requiredPetApprovalReason(reason...)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.PetApproval{}, err
	}
	if !assistant.PetAllowManageSystem {
		return domain.PetApproval{}, fmt.Errorf("主人未开启管理系统设置权限")
	}
	resource, err := s.store.HardwareResource(ctx, strings.TrimSpace(resourceID))
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("硬件资源不存在")
	}
	payload, _ := json.Marshal(map[string]string{"resource_id": resource.ID})
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "delete_hardware_resource", Title: "删除硬件资源：" + resource.Description,
		Summary: fmt.Sprintf(
			"宠物申请删除硬件资源「%s」（%s），当前有 %d 个 Task 绑定。批准后才执行。",
			resource.Description, resource.ID, resource.BindingCount,
		),
		Reason: reasonText, Risk: "high", Payload: string(payload),
	})
}

func (s *Service) deleteHardwareResourceNow(ctx context.Context, resourceID string) error {
	resource, err := s.store.HardwareResource(ctx, strings.TrimSpace(resourceID))
	if err != nil {
		return fmt.Errorf("硬件资源不存在")
	}
	bindings, _ := s.store.HardwareResourceBindings(ctx, resource.ID)
	if s.petHardware != nil {
		for _, binding := range bindings {
			if resource.Supports(domain.HardwareTransportSerial) {
				s.petHardware.Disconnect(binding.TaskID, binding.ID, domain.HardwareTransportSerial)
			}
			if resource.Supports(domain.HardwareTransportNetwork) {
				s.petHardware.Disconnect(binding.TaskID, binding.ID, domain.HardwareTransportNetwork)
			}
		}
	}
	if err := s.store.DeleteHardwareResource(ctx, resource.ID); err != nil {
		return err
	}
	if s.secrets != nil && resource.CredentialRef != "" {
		shared, sharedErr := s.store.HardwareCredentialReferenced(ctx, resource.CredentialRef, resource.ID)
		if sharedErr == nil && !shared {
			_ = s.secrets.DeleteMany([]string{resource.CredentialRef})
		}
	}
	return nil
}

type PetUpdateTaskHardwareInput struct {
	TaskID      string   `json:"task_id"`
	ResourceIDs []string `json:"resource_ids"`
}

func (s *Service) PetUpdateTaskHardware(ctx context.Context, input PetUpdateTaskHardwareInput) (PetTaskHardwareView, error) {
	task, _, err := s.petManageableTask(ctx, input.TaskID)
	if err != nil {
		return PetTaskHardwareView{}, err
	}
	bindings := make([]domain.TaskHardwareBinding, 0, len(input.ResourceIDs))
	seen := map[string]bool{}
	for _, raw := range input.ResourceIDs {
		resourceID := strings.TrimSpace(raw)
		if resourceID == "" || seen[resourceID] {
			continue
		}
		seen[resourceID] = true
		if _, err := s.store.HardwareResource(ctx, resourceID); err != nil {
			return PetTaskHardwareView{}, fmt.Errorf("硬件资源 %s 不存在", resourceID)
		}
		bindings = append(bindings, domain.TaskHardwareBinding{
			TaskID: task.ID, ID: domain.NewID("hardware_binding"), ResourceID: resourceID,
		})
	}
	if len(bindings) > 8 {
		return PetTaskHardwareView{}, fmt.Errorf("一个 Task 最多绑定 8 个硬件资源")
	}
	if err := s.store.ReplaceTaskHardwareBindings(ctx, task.ID, bindings); err != nil {
		return PetTaskHardwareView{}, err
	}
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: "pet", Kind: "task_hardware_updated",
		Detail: task.ID, Status: "ok",
	})
	return s.PetTaskHardware(ctx, task.ID)
}
