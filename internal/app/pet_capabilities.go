package app

import (
	"context"
	"strings"
)

type PetCapabilityTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

type PetCapabilitiesView struct {
	Permissions PetPermissionsView  `json:"permissions"`
	Tools       []PetCapabilityTool `json:"tools"`
	ToolCount   int                 `json:"tool_count"`
}

// PetCapabilities is the permission-aware command directory the pet can ask
// for instead of carrying every tool name in its identity prompt. Tool schemas
// still come from MCP tools/list; this returns only allowed names grouped by
// capability area.
func (s *Service) PetCapabilities(ctx context.Context) (PetCapabilitiesView, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return PetCapabilitiesView{}, err
	}
	view := PetCapabilitiesView{
		Permissions: petPermissionsView(assistant),
		Tools:       make([]PetCapabilityTool, 0),
	}
	for _, tool := range allPetMCPTools() {
		if !petToolAllowed(assistant, tool.Name) {
			continue
		}
		view.Tools = append(view.Tools, PetCapabilityTool{
			Name: tool.Name, Description: tool.Description, Category: petCapabilityCategory(tool.Name),
		})
	}
	view.ToolCount = len(view.Tools)
	return view, nil
}

func petCapabilityCategory(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case name == "aha_overview", name == "aha_pet_work_state", name == "aha_pet_permissions",
		name == "aha_capabilities", name == "aha_request_permission_change",
		name == "pet_request_approval", name == "pet_list_approvals", name == "pet_submit_idea",
		name == "pet_watch_task", name == "pet_unwatch_task":
		return "core"
	case name == "pet_update_core", name == "pet_write_worklog":
		return "memory"
	case strings.HasPrefix(name, "pet_"):
		return "presence"
	case strings.Contains(name, "channel"):
		return "channel"
	case strings.Contains(name, "knowledge"), strings.Contains(name, "skill"), strings.Contains(name, "prompt"):
		return "knowledge"
	case strings.Contains(name, "model"), strings.Contains(name, "provider"),
		strings.Contains(name, "env_group"), name == "aha_detect_models":
		return "model"
	case strings.Contains(name, "system"), strings.Contains(name, "security"), strings.Contains(name, "mcp"):
		return "system"
	case strings.Contains(name, "hardware"):
		return "hardware"
	case name == "aha_send_task_message":
		return "message"
	case strings.Contains(name, "project"), strings.Contains(name, "workspace"):
		return "workspace"
	case strings.Contains(name, "task"), strings.Contains(name, "attachment"):
		return "task"
	default:
		return "core"
	}
}
