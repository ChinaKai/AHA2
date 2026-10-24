package httpapi

import "github.com/ChinaKai/AHA2/internal/domain"

// compactTaskMemory keeps browser and Agent API responses to the current goal
// and its bounded change timeline. Legacy arrays remain available internally
// for synchronization and migration.
func compactTaskMemory(memory domain.TaskMemory) domain.TaskMemory {
	return domain.TaskMemory{
		TaskID:      memory.TaskID,
		CurrentGoal: memory.CurrentGoal,
		GoalHistory: memory.GoalHistory,
		UpdatedAt:   memory.UpdatedAt,
	}
}
