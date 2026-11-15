package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

// KnowledgeIndexSource is one grantable project knowledge root together with
// the store that currently owns it. Callers that persist a grant must keep the
// two apart: a vault root ID is valid only while the project still reads the
// vault, and a legacy root ID must never be offered for a vault-routed project.
type KnowledgeIndexSource struct {
	Entry domain.KnowledgeEntry
	Vault bool
}

// KnowledgeIndexSources lists one verified knowledge root per project from the
// project's current read source. A vault read failure is returned rather than
// silently substituting the legacy root, because that would offer the owner a
// grant that can read the old library again.
func (s *Service) KnowledgeIndexSources(ctx context.Context) ([]KnowledgeIndexSource, error) {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]KnowledgeIndexSource, 0, len(projects))
	for _, project := range projects {
		decision := s.knowledgeReadDecision("project", project.ID)
		if decision.Err != nil {
			return nil, vaultReadFailure("project", project.ID, decision.Err)
		}
		if decision.Vault {
			entry, rootErr := s.vaultProjectRootEntry(ctx, project.ID)
			if rootErr != nil {
				return nil, vaultReadFailure("project", project.ID, rootErr)
			}
			if entry.ID == "" {
				log.Printf("channel knowledge roots: vault project %s has no indexed root; skipped", project.ID)
				continue
			}
			result = append(result, KnowledgeIndexSource{Entry: entry, Vault: true})
			continue
		}
		entry, legacyErr := s.store.KnowledgeRootForProject(ctx, project.ID)
		if errors.Is(legacyErr, sql.ErrNoRows) {
			continue
		}
		if legacyErr != nil {
			return nil, legacyErr
		}
		result = append(result, KnowledgeIndexSource{Entry: entry})
	}
	return result, nil
}

func (s *Service) vaultProjectRootEntry(ctx context.Context, projectID string) (domain.KnowledgeEntry, error) {
	projectID = strings.TrimSpace(projectID)
	var entry domain.KnowledgeEntry
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		if !reader.IndexReady() || strings.TrimSpace(reader.Manifest().Projects[projectID]) == "" {
			return knowledgevault.ErrReaderNotReady
		}
		entries, err := reader.Entries(ctx, "project", projectID)
		if err != nil {
			return err
		}
		for _, item := range entries {
			if !item.IsIndex {
				continue
			}
			mapped, ok := mapVaultKnowledgeEntry(item)
			if !ok {
				continue
			}
			entry = mapped
			return nil
		}
		return nil
	})
	return entry, err
}

// channelAllowedKnowledge is the single read path for channel knowledge. The
// policy and its grants stay in the configuration tables, but every granted ID
// is resolved against the current read source of the project that owns it.
// Legacy grants that point at a document of a vault-routed project therefore
// stop granting anything; a vault read failure aborts the whole read instead of
// returning legacy rows.
func (s *Service) channelAllowedKnowledge(ctx context.Context, instanceID, endpointKind, conversationID string) ([]domain.KnowledgeEntry, error) {
	selection, err := s.store.ChannelKnowledgeSelection(ctx, instanceID, endpointKind)
	if errors.Is(err, store.ErrChannelKnowledgeUnconfigured) {
		// A channel created while the vault is the read source has no policy:
		// vault knowledge is never granted implicitly. This is an explicit empty
		// selection, not a read failure; the owner selects sources in the channel
		// settings and the UI shows the unconfigured state.
		log.Printf("channel knowledge instance %s endpoint %s is not configured; using an empty selection", instanceID, endpointKind)
		return []domain.KnowledgeEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	scopeMode := selection.ScopeMode
	if endpointKind == domain.ChannelEndpointAssistantDM {
		// The private chat is verified as the bound Owner; it always sees all
		// normal project knowledge. Only the group digital human is scoped.
		scopeMode = "all"
	}
	result := []domain.KnowledgeEntry{}
	seen := map[string]bool{}
	appendEntry := func(entry domain.KnowledgeEntry) {
		if strings.TrimSpace(entry.ID) == "" || seen[entry.ID] {
			return
		}
		seen[entry.ID] = true
		result = append(result, entry)
	}

	// The fixed channel index is always readable, including while the channel
	// host project itself is still unbound from the vault.
	if entry, ok, entryErr := s.channelKnowledgeEntry(ctx, selection.FixedIndexEntryID); entryErr != nil {
		return nil, entryErr
	} else if ok {
		appendEntry(entry)
	}

	if scopeMode == "all" {
		projects, err := s.store.ListProjects(ctx)
		if err != nil {
			return nil, err
		}
		for _, project := range projects {
			// A channel host project is infrastructure, not a knowledge
			// source; the legacy "all" rule excluded it the same way.
			if project.ProjectType == "channel" {
				continue
			}
			entries, sourceErr := s.channelScopeEntries(ctx, "project", project.ID)
			if sourceErr != nil {
				return nil, sourceErr
			}
			for _, entry := range entries {
				appendEntry(entry)
			}
		}
		return result, nil
	}

	for _, grant := range selection.Grants {
		entry, ok, entryErr := s.channelKnowledgeEntry(ctx, grant.KnowledgeEntryID)
		if entryErr != nil {
			return nil, entryErr
		}
		if !ok || entry.Status != domain.KnowledgeVerified {
			continue
		}
		appendEntry(entry)
		if grant.GrantScope != "subtree" {
			continue
		}
		entries, sourceErr := s.channelScopeEntries(ctx, entry.Scope, entry.ProjectID)
		if sourceErr != nil {
			return nil, sourceErr
		}
		children := map[string][]domain.KnowledgeEntry{}
		for _, candidate := range entries {
			children[candidate.ParentID] = append(children[candidate.ParentID], candidate)
		}
		queue := append([]domain.KnowledgeEntry(nil), children[entry.ID]...)
		for len(queue) > 0 {
			child := queue[0]
			queue = queue[1:]
			if seen[child.ID] {
				continue
			}
			appendEntry(child)
			queue = append(queue, children[child.ID]...)
		}
	}
	return result, nil
}

// channelScopeEntries returns the verified entries of one scope from the
// source that currently owns it.
func (s *Service) channelScopeEntries(ctx context.Context, scope, projectID string) ([]domain.KnowledgeEntry, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "global" {
		active, _, vault, err := s.readVaultGlobalKnowledge(ctx)
		if err != nil {
			return nil, err
		}
		if vault {
			return filterChannelKnowledge(active), nil
		}
		entries, err := s.store.ListKnowledge(ctx, "global", "", nil)
		if err != nil {
			return nil, err
		}
		return filterChannelKnowledge(entries), nil
	}
	snapshot, vault, err := s.readVaultProjectKnowledge(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if vault {
		return filterChannelKnowledge(snapshot.Project), nil
	}
	entries, err := s.store.ListKnowledge(ctx, "project", projectID, nil)
	if err != nil {
		return nil, err
	}
	return filterChannelKnowledge(entries), nil
}

func filterChannelKnowledge(entries []domain.KnowledgeEntry) []domain.KnowledgeEntry {
	result := make([]domain.KnowledgeEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Status != domain.KnowledgeVerified {
			continue
		}
		if entry.BranchScope != "" || entry.ProductLineID != "" {
			continue
		}
		result = append(result, entry)
	}
	return result
}

// channelKnowledgeEntry resolves one stored grant/fixed-index ID. The vault is
// consulted first so a migrated ID is served from the vault, but an ID that
// only exists in the legacy tables is honoured only when its project still
// reads legacy; on a vault-routed project it is an expired grant.
func (s *Service) channelKnowledgeEntry(ctx context.Context, id string) (domain.KnowledgeEntry, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return domain.KnowledgeEntry{}, false, nil
	}
	// Probe the vault only while vault reads are effective. With the runtime
	// read gate off every scope is legacy by definition, so touching a broken
	// vault reader would turn a forced legacy read into an error.
	if s.knowledgeVault != nil && s.knowledgeVault.Enabled() && s.knowledgeVaultRuntimeRead {
		raw, found, err := s.vaultEntryByID(ctx, id)
		if err != nil {
			return domain.KnowledgeEntry{}, false, err
		}
		if found {
			scope, projectID := knowledgeProjectIDForScope(raw.Scope, raw.ProjectID)
			if !s.readSourceIsVault(scope, projectID) {
				return domain.KnowledgeEntry{}, false, nil
			}
			entry, ok := mapVaultKnowledgeEntry(raw)
			return entry, ok, nil
		}
	}
	entry, err := s.store.Knowledge(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeEntry{}, false, nil
	}
	if err != nil {
		return domain.KnowledgeEntry{}, false, err
	}
	scope, projectID := knowledgeProjectIDForScope(entry.Scope, entry.ProjectID)
	if s.readSourceIsVault(scope, projectID) {
		log.Printf("channel knowledge: legacy-only entry %s belongs to a vault-routed project; grant ignored", id)
		return domain.KnowledgeEntry{}, false, nil
	}
	return entry, true, nil
}

// channelKnowledgeRootIDs returns the grantable root IDs of both sources for
// the channel API. Legacy IDs are kept separate so the store can skip its
// legacy-only status check for vault IDs.
func (s *Service) ChannelKnowledgeRootIDs(ctx context.Context) (map[string]bool, map[string]bool, error) {
	sources, err := s.KnowledgeIndexSources(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list channel knowledge sources: %w", err)
	}
	legacy := map[string]bool{}
	vault := map[string]bool{}
	for _, source := range sources {
		legacy[source.Entry.ID] = true
		if source.Vault {
			vault[source.Entry.ID] = true
		}
	}
	return legacy, vault, nil
}
