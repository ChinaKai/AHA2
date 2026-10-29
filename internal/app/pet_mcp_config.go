package app

import "github.com/ChinaKai/AHA2/internal/domain"

func petMCPAllowedTools(task domain.Task) []string {
	if task.ID != petTaskID {
		return nil
	}
	return []string{
		"mcp__aha2-pet__pet_play",
		"mcp__aha2-pet__pet_move_to",
		"mcp__aha2-pet__pet_walk_path",
		"mcp__aha2-pet__pet_mood",
		"mcp__aha2-pet__pet_thinking",
		"mcp__aha2-pet__pet_open_task",
		"mcp__aha2-pet__pet_sleep",
		"mcp__aha2-pet__pet_wake",
	}
}
