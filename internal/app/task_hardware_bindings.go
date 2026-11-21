package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardwareconfig"
)

const MaxTaskHardwareResources = 16

// ReplaceTaskHardwareResourceBindings is shared by Web and pet management.
// Caller-specific owner/Task permissions are checked before entering here.
// Validation completes before any live connection is closed or row is changed.
func (s *Service) ReplaceTaskHardwareResourceBindings(ctx context.Context, taskID string, bindings []domain.TaskHardwareBinding) error {
	if len(bindings) > MaxTaskHardwareResources {
		return fmt.Errorf("每个任务最多启用 %d 个硬件资源", MaxTaskHardwareResources)
	}
	if _, err := s.store.Task(ctx, taskID); err != nil {
		return err
	}
	existing, err := s.store.TaskHardwareBindings(ctx, taskID)
	if err != nil {
		return err
	}
	byID := map[string]domain.TaskHardwareBinding{}
	for _, item := range existing {
		byID[item.ID] = item
	}
	ids, resources := map[string]bool{}, map[string]bool{}
	now := s.now().UTC()
	for index := range bindings {
		item := &bindings[index]
		item.TaskID = taskID
		item.ResourceID = strings.TrimSpace(item.ResourceID)
		item.ID = hardwareconfig.NormalizeID(item.ID)
		if item.ID == "" {
			item.ID = hardwareconfig.NormalizeID(item.ResourceID)
		}
		if item.ID == "" || item.ResourceID == "" || ids[item.ID] || resources[item.ResourceID] {
			return fmt.Errorf("硬件绑定为空或重复")
		}
		if _, err := s.store.HardwareResource(ctx, item.ResourceID); err != nil {
			return fmt.Errorf("硬件资源不存在")
		}
		ids[item.ID], resources[item.ResourceID] = true, true
		item.CreatedAt, item.UpdatedAt = now, now
		if previous, ok := byID[item.ID]; ok && previous.ResourceID == item.ResourceID {
			item.CreatedAt = previous.CreatedAt
		}
	}
	groups, err := s.store.HardwareGroups(ctx, taskID)
	if err != nil {
		return err
	}
	if s.petHardware != nil {
		for _, group := range groups {
			for _, transport := range []string{domain.HardwareTransportSerial, domain.HardwareTransportNetwork} {
				if group.Supports(transport) {
					s.petHardware.Disconnect(taskID, group.ID, transport)
				}
			}
		}
	}
	return s.store.ReplaceTaskHardwareBindings(ctx, taskID, bindings)
}
