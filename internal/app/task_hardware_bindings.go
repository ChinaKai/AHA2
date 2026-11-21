package app

import (
	"context"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardwareconfig"
)

const MaxTaskHardwareResources = 16

type HardwareBindingError struct{ Code string }

func (e *HardwareBindingError) Error() string { return e.Code }

// ReplaceTaskHardwareResourceBindings is shared by Web and pet management.
// Caller-specific owner/Task permissions are checked before entering here.
// Validation completes before any live connection is closed or row is changed.
func (s *Service) ReplaceTaskHardwareResourceBindings(ctx context.Context, taskID string, bindings []domain.TaskHardwareBinding) error {
	if len(bindings) > MaxTaskHardwareResources {
		return &HardwareBindingError{Code: "hardware_binding_limit"}
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
		if item.ID == "" || item.ResourceID == "" {
			return &HardwareBindingError{Code: "hardware_binding_empty"}
		}
		if ids[item.ID] || resources[item.ResourceID] {
			return &HardwareBindingError{Code: "hardware_binding_duplicate"}
		}
		if _, err := s.store.HardwareResource(ctx, item.ResourceID); err != nil {
			return &HardwareBindingError{Code: "hardware_resource_not_found"}
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
	keepIDs := []string{}
	keep := map[string]bool{}
	unchanged := len(existing) == len(bindings)
	for index, item := range bindings {
		if previous, ok := byID[item.ID]; ok && previous.ResourceID == item.ResourceID {
			keepIDs = append(keepIDs, item.ID)
			keep[item.ID] = true
		}
		if index >= len(existing) || existing[index].ID != item.ID || existing[index].ResourceID != item.ResourceID {
			unchanged = false
		}
	}
	if !unchanged {
		if err := s.store.ReplaceTaskHardwareBindings(ctx, taskID, bindings); err != nil {
			return err
		}
	}
	if runtime, ok := s.petHardware.(interface{ DisconnectTaskExcept(string, []string) }); ok {
		runtime.DisconnectTaskExcept(taskID, keepIDs)
	} else if s.petHardware != nil {
		for _, group := range groups {
			if keep[group.ID] {
				continue
			}
			for _, transport := range []string{domain.HardwareTransportSerial, domain.HardwareTransportNetwork} {
				if group.Supports(transport) {
					s.petHardware.Disconnect(taskID, group.ID, transport)
				}
			}
		}
	}
	return nil
}
