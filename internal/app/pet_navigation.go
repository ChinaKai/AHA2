package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	// petNavigationTTL bounds how long an open_task request waits for a Web
	// session before it is expired instead of navigating the owner much later.
	petNavigationTTL = 10 * time.Minute
	// petNavigationClaimLimit keeps one Web reconnection from replaying an
	// unbounded backlog of stale navigation requests.
	petNavigationClaimLimit = 20
)

// QueuePetNavigation persists one open_task request. The live SSE event is
// still published for connected clients, but the row makes the request
// survive a background Turn with no Web client attached.
func (s *Service) QueuePetNavigation(ctx context.Context, taskID string) (domain.PetNavigationAction, error) {
	now := s.now().UTC()
	action := domain.PetNavigationAction{
		ID:        domain.NewID("pet_nav"),
		TaskID:    strings.TrimSpace(taskID),
		Status:    domain.PetNavigationQueued,
		CreatedAt: now,
		ExpiresAt: now.Add(petNavigationTTL),
	}
	if action.TaskID == "" {
		return domain.PetNavigationAction{}, fmt.Errorf("pet_open_task 需要 task ID 或编号")
	}
	if err := s.store.CreatePetNavigationAction(ctx, action); err != nil {
		return domain.PetNavigationAction{}, err
	}
	return action, nil
}

// ClaimPetNavigation consumes pending navigation requests for one Web session.
// The store performs the atomic pending -> consumed transition; expired rows
// are reported separately and never returned for navigation.
func (s *Service) ClaimPetNavigation(ctx context.Context, sessionID string, ids []string) ([]domain.PetNavigationAction, int, error) {
	return s.store.ClaimPetNavigationActions(ctx, s.now().UTC(), sessionID, ids, petNavigationClaimLimit)
}

// resolvePetOpenTask accepts both the internal Task id (task_...) and the
// human-facing code (task-001) the pet sees in open_tasks and the Web UI.
func (s *Service) resolvePetOpenTask(ctx context.Context, value string) (domain.Task, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return domain.Task{}, fmt.Errorf("pet_open_task 需要 task ID 或编号")
	}
	if task, err := s.store.Task(ctx, value); err == nil {
		return task, nil
	}
	if task, err := s.store.TaskByCode(ctx, value); err == nil {
		return task, nil
	}
	return domain.Task{}, fmt.Errorf("找不到要打开的 Task：%s", value)
}

// isPetTaskReference is the cheap shape check used by the model-facing action
// parsers before the store confirms the Task actually exists.
func isPetTaskReference(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "task_") || strings.HasPrefix(value, "task-")
}
