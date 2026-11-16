package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// ErrKnowledgeSpaceNotReady reports a binding to a project whose space is not
// ready. A binding only ever points at a project in model A, so an
// uninitialized space is an explicit configuration error, not an empty read.
var ErrKnowledgeSpaceNotReady = errors.New("knowledge_space_not_ready")

// ChannelKnowledgeBindingSelection is the effective group selection. Global is
// a separate boolean because it has no projects row to bind; the endpoint row
// overrides the instance default exactly like the space list.
type ChannelKnowledgeBindingSelection struct {
	Bindings      []domain.KnowledgeBinding
	IncludeGlobal bool
}

// ChannelKnowledgeBindingSelection lists the effective group selection for one
// consumer from a single store snapshot. Endpoint-level rows replace the
// instance default when present.
func (s *Service) ChannelKnowledgeBindingSelection(ctx context.Context, instanceID, endpointKind string) (ChannelKnowledgeBindingSelection, error) {
	selection, err := s.store.ResolveChannelKnowledgeBindingSelection(ctx, instanceID, endpointKind)
	if err != nil {
		return ChannelKnowledgeBindingSelection{}, err
	}
	return ChannelKnowledgeBindingSelection{Bindings: selection.Bindings, IncludeGlobal: selection.IncludeGlobal}, nil
}

// ChannelKnowledgeBindings lists the effective space bindings for one consumer.
func (s *Service) ChannelKnowledgeBindings(ctx context.Context, instanceID, endpointKind string) ([]domain.KnowledgeBinding, error) {
	return s.store.ResolveChannelKnowledgeBindings(ctx, instanceID, endpointKind)
}

// ReplaceChannelKnowledgeBindings replaces the endpoint (or instance) binding
// list and, when includeGlobal is non-nil, its explicit global selection. Every
// space must be a ready project space; unknown or uninitialized projects are
// rejected before anything is written.
func (s *Service) ReplaceChannelKnowledgeBindings(ctx context.Context, instanceID, endpointKind string, bindings []domain.KnowledgeBinding, includeGlobal *bool) ([]domain.KnowledgeBinding, error) {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return nil, fmt.Errorf("channel instance id is required")
	}
	endpointKind = strings.TrimSpace(endpointKind)
	consumerType, consumerID := domain.KnowledgeBindingChannelInstance, instanceID
	if endpointKind != "" {
		consumerType = domain.KnowledgeBindingChannelEndpoint
		consumerID = store.ChannelEndpointConsumerID(instanceID, endpointKind)
	}
	validated, err := s.validateChannelKnowledgeBindings(ctx, consumerType, consumerID, bindings)
	if err != nil {
		return nil, err
	}
	return s.store.ReplaceKnowledgeBindings(ctx, consumerType, consumerID, validated, includeGlobal)
}

// ReplaceChannelGroupKnowledgeBindings is the Web save path. It writes the
// group endpoint selection and clears both instance defaults in one store
// transaction, so "global only" and "nothing selected" fail closed instead of
// falling back to a space list the owner just removed.
func (s *Service) ReplaceChannelGroupKnowledgeBindings(ctx context.Context, instanceID string, bindings []domain.KnowledgeBinding, includeGlobal bool) ([]domain.KnowledgeBinding, error) {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return nil, fmt.Errorf("channel instance id is required")
	}
	consumerID := store.ChannelEndpointConsumerID(instanceID, domain.ChannelEndpointGroupDigitalHuman)
	validated, err := s.validateChannelKnowledgeBindings(ctx, domain.KnowledgeBindingChannelEndpoint, consumerID, bindings)
	if err != nil {
		return nil, err
	}
	return s.store.ReplaceChannelGroupKnowledgeBindings(ctx, instanceID, validated, includeGlobal)
}

func (s *Service) validateChannelKnowledgeBindings(ctx context.Context, consumerType, consumerID string, bindings []domain.KnowledgeBinding) ([]domain.KnowledgeBinding, error) {
	validated := make([]domain.KnowledgeBinding, 0, len(bindings))
	for _, binding := range bindings {
		space := strings.TrimSpace(binding.SpaceProject)
		project, err := s.store.Project(ctx, space)
		if err != nil {
			return nil, fmt.Errorf("knowledge space %s: %w", space, err)
		}
		if project.KnowledgeState != domain.KnowledgeStateReady {
			return nil, fmt.Errorf("%w: 项目 %s 的知识空间状态为 %s", ErrKnowledgeSpaceNotReady, project.ID, project.KnowledgeState)
		}
		if project.ProjectType == "system" {
			return nil, fmt.Errorf("系统项目 %s 不能作为渠道知识库", project.ID)
		}
		access := strings.TrimSpace(binding.AccessMode)
		if access != "read_write" {
			access = "read"
		}
		scope := strings.TrimSpace(binding.ScopeMode)
		if scope != "selected" {
			scope = "all"
		}
		validated = append(validated, domain.KnowledgeBinding{
			ConsumerType: consumerType, ConsumerID: consumerID,
			SpaceProject: project.ID, AccessMode: access, ScopeMode: scope,
		})
	}
	return validated, nil
}

// channelGroupBindingEntries is the group read path. Global is optional and
// only included when the owner selected it; project spaces follow the
// endpoint/instance binding list. Entries are deduped by ID across both sources
// and a failing source aborts the read instead of silently dropping it.
func (s *Service) channelGroupBindingEntries(ctx context.Context, selection ChannelKnowledgeBindingSelection) ([]domain.KnowledgeEntry, error) {
	result := []domain.KnowledgeEntry{}
	seen := map[string]bool{}
	appendEntries := func(entries []domain.KnowledgeEntry) {
		for _, entry := range entries {
			if strings.TrimSpace(entry.ID) == "" || seen[entry.ID] {
				continue
			}
			seen[entry.ID] = true
			result = append(result, entry)
		}
	}
	if selection.IncludeGlobal {
		global, err := s.channelScopeEntries(ctx, "global", "")
		if err != nil {
			return nil, err
		}
		appendEntries(global)
	}
	projectEntries, err := s.channelBindingEntries(ctx, selection.Bindings)
	if err != nil {
		return nil, err
	}
	appendEntries(projectEntries)
	return result, nil
}

// channelBindingEntries is the read path for explicit channel bindings. Each
// binding currently reads the complete verified content of its project space,
// which is the same source a project Turn would see. access_mode is stored for
// a future write path but does not change reads yet, and scope_mode=selected
// has no sub-scope table, so it also reads the whole space. Failures fail
// closed.
func (s *Service) channelBindingEntries(ctx context.Context, bindings []domain.KnowledgeBinding) ([]domain.KnowledgeEntry, error) {
	result := []domain.KnowledgeEntry{}
	seen := map[string]bool{}
	for _, binding := range bindings {
		entries, err := s.channelScopeEntries(ctx, "project", binding.SpaceProject)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if strings.TrimSpace(entry.ID) == "" || seen[entry.ID] {
				continue
			}
			seen[entry.ID] = true
			result = append(result, entry)
		}
	}
	return result, nil
}
