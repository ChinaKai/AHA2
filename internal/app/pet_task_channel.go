package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Service) PetTaskChannelRoute(ctx context.Context, taskID string) (domain.ChannelTaskRoute, error) {
	task, err := s.petViewableTask(ctx, taskID)
	if err != nil {
		return domain.ChannelTaskRoute{}, err
	}
	route, err := s.store.ActiveChannelTaskRouteForTask(ctx, task.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChannelTaskRoute{}, nil
	}
	return route, err
}

type PetBindTaskChannelInput struct {
	TaskID         string `json:"task_id"`
	InstanceID     string `json:"instance_id"`
	ConversationID string `json:"conversation_id"`
}

func (s *Service) PetBindTaskChannel(ctx context.Context, input PetBindTaskChannelInput) (domain.ChannelTaskRoute, error) {
	task, _, err := s.petManageableTask(ctx, input.TaskID)
	if err != nil {
		return domain.ChannelTaskRoute{}, err
	}
	instanceID := strings.TrimSpace(input.InstanceID)
	conversationID := strings.TrimSpace(input.ConversationID)
	if instanceID == "" || conversationID == "" {
		return domain.ChannelTaskRoute{}, fmt.Errorf("instance_id 和 conversation_id 不能为空")
	}
	route, err := s.store.ActivateOwnerChannelTaskRoute(ctx, instanceID, conversationID, task.ID, s.now().UTC())
	if err != nil {
		return domain.ChannelTaskRoute{}, err
	}
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: "pet", Kind: "task_channel_bound",
		Detail: task.ID, Status: route.ID,
	})
	return route, nil
}

func (s *Service) PetUnbindTaskChannel(ctx context.Context, taskID, routeID string) error {
	task, _, err := s.petManageableTask(ctx, taskID)
	if err != nil {
		return err
	}
	routeID = strings.TrimSpace(routeID)
	if routeID == "" {
		return fmt.Errorf("route_id 不能为空")
	}
	if err := s.store.ExitOwnerChannelTaskRoute(ctx, routeID, task.ID, s.now().UTC()); err != nil {
		return err
	}
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: "pet", Kind: "task_channel_unbound",
		Detail: task.ID, Status: routeID,
	})
	return nil
}
