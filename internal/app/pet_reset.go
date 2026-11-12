package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const petResetConfirmation = "重置宠物"

type PetResetResult struct {
	Tasks      int      `json:"tasks"`
	Workspaces int      `json:"workspaces"`
	Approvals  int      `json:"approvals"`
	Events     int      `json:"events"`
	Watches    int      `json:"watches"`
	Messages   int      `json:"messages"`
	Hints      int      `json:"hints"`
	Warnings   []string `json:"warnings,omitempty"`
}

// ResetOwnerPet removes this device's generated pet workspace, Task, records
// and workspace files. The system pet project and read-only sync mirrors are
// kept so other devices' pet data is never changed. The caller must pass the
// literal confirmation text; the HTTP handler and the Web dialog both require
// the same input, so a stray API call cannot erase the pet by mistake.
//
// Owner preferences (model, character, permission switches, schedule) are
// kept. The pet is disabled so the server-side autonomy loop cannot recreate a
// fresh Task before the owner has reviewed the reset.
func (s *Service) ResetOwnerPet(ctx context.Context, confirmation string) (PetResetResult, error) {
	if strings.TrimSpace(confirmation) != petResetConfirmation {
		return PetResetResult{}, fmt.Errorf("确认文字不匹配，未执行重置")
	}

	// Serialize against the autonomous brain and the lazy pet Task creation
	// path. The brain normally checks PetEnabled itself; holding this lock also
	// covers a brain call that already passed that check.
	s.petBrainMu.Lock()
	defer s.petBrainMu.Unlock()
	s.petTaskMu.Lock()
	defer s.petTaskMu.Unlock()

	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil {
		return PetResetResult{}, err
	}

	tasks, err := s.store.ListTasks(ctx, petProjectID)
	if err != nil {
		return PetResetResult{}, err
	}
	taskSet := map[string]bool{}
	for _, task := range tasks {
		taskSet[task.ID] = true
	}
	// A legacy assistant row may point at a Task outside the system project.
	// Add those ids only when the row really exists so the reported count is
	// accurate on an already-empty installation.
	for _, id := range []string{petTaskID, assistant.PetTaskID} {
		if id == "" || taskSet[id] {
			continue
		}
		if _, err := s.store.Task(ctx, id); err == nil {
			taskSet[id] = true
		}
	}
	taskIDs := make([]string, 0, len(taskSet))
	for id := range taskSet {
		taskIDs = append(taskIDs, id)
	}
	sort.Strings(taskIDs)

	workspaces, err := s.store.ListWorkspaces(ctx, petProjectID)
	if err != nil {
		return PetResetResult{}, err
	}
	roots := make([]string, 0, len(workspaces))

	result := PetResetResult{Tasks: len(taskIDs)}
	counts, err := s.store.ResetPetRecords(ctx, assistant.ID, taskIDs, s.now().UTC())
	if err != nil {
		return result, err
	}
	result.Approvals = counts.Approvals
	result.Events = counts.Events
	result.Watches = counts.Watches
	result.Messages = counts.Messages
	result.Hints = counts.Hints

	for _, taskID := range taskIDs {
		// Stop a running pet turn before its rows and workspace disappear. The
		// cancellation is best-effort; the pet is already disabled above.
		if turn, activeErr := s.store.ActiveTurn(ctx, taskID); activeErr == nil {
			_ = s.InterruptTurn(ctx, turn.ID)
		}
		if task, taskErr := s.store.Task(ctx, taskID); taskErr == nil {
			if cleanupErr := s.CleanupTaskScratch(ctx, task); cleanupErr != nil {
				log.Printf("cleanup task scratch %s: %v", task.ID, cleanupErr)
			}
		}
		if err := s.store.DeleteTask(ctx, taskID); err != nil {
			return result, fmt.Errorf("删除宠物 Task 失败：%w", err)
		}
	}

	for _, workspace := range workspaces {
		// Read-only Workspace rows are mirrors owned by another device and must
		// stay untouched. Writable rows are this device's local pet workspace;
		// delete them without a sync tombstone so no other device is changed.
		if workspace.ReadOnly {
			continue
		}
		result.Workspaces++
		if root := strings.TrimSpace(workspace.RootPath); root != "" {
			roots = append(roots, root)
		}
		if err := s.store.DeleteWorkspaceLocal(ctx, workspace.ID); err != nil {
			return result, fmt.Errorf("删除本机宠物 Workspace 失败：%w", err)
		}
	}

	// Keep the system pet project and all read-only mirrors. Other devices can
	// continue using their own pet; only this device's Task/Workspace/records
	// and files are cleared.
	result.Warnings = s.removePetWorkspaceState(roots)
	if state, stateErr := s.OwnerPetState(ctx); stateErr == nil {
		s.publishPetState(state)
	}
	return result, nil
}

// removePetWorkspaceState deletes the known pet-owned contents. The default
// data directory belongs entirely to the pet and can be removed; a custom
// Workspace root may contain owner files, so only the pet's well-known
// subdirectories and generated guides are removed there.
func (s *Service) removePetWorkspaceState(roots []string) []string {
	defaultRoot := filepath.Clean(filepath.Join(s.store.DataDir(), "pet-workspace"))
	seen := map[string]bool{}
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root != "" {
			seen[filepath.Clean(root)] = true
		}
	}
	add(defaultRoot)
	for _, root := range roots {
		add(root)
	}

	warnings := []string{}
	for root := range seen {
		if root == defaultRoot {
			if err := os.RemoveAll(root); err != nil {
				warnings = append(warnings, fmt.Sprintf("清理 %s 失败：%v", root, err))
			}
			continue
		}
		// Retain the retired directory in explicit owner reset cleanup for old installations.
		for _, name := range []string{"core", "diary", "worklog", "pet-lab"} {
			if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
				warnings = append(warnings, fmt.Sprintf("清理 %s 失败：%v", filepath.Join(root, name), err))
			}
		}
		for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
			path := filepath.Join(root, name)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			content := strings.TrimSpace(string(data))
			for _, guide := range petWorkspaceGuides {
				if content == strings.TrimSpace(guide) {
					if err := os.Remove(path); err != nil {
						warnings = append(warnings, fmt.Sprintf("清理 %s 失败：%v", path, err))
					}
					break
				}
			}
		}
	}
	sort.Strings(warnings)
	return warnings
}
