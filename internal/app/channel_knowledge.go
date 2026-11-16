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

// channelAllowedKnowledge is the single read path for channel knowledge.
// The private chat is the verified bound Owner and is never scoped by channel
// bindings: it reads global plus every ready project space, like an
// unrestricted Task. Only the group digital human is scoped. With the vault as
// the read source that scope is stored in knowledge_bindings (endpoint list
// first, instance default as the compatibility fallback) and an empty selection
// stays empty; with the legacy tables it is the per-entry policy below. Every
// selected ID is resolved against the source that currently owns its project,
// so a vault read failure aborts the read instead of returning legacy rows.
func (s *Service) channelAllowedKnowledge(ctx context.Context, instanceID, endpointKind, conversationID string) ([]domain.KnowledgeEntry, error) {
	if endpointKind == domain.ChannelEndpointAssistantDM {
		return s.channelDmKnowledge(ctx)
	}
	// Model A bindings point at whole project spaces and are the group scope
	// whenever the vault is the configured read source. The endpoint list
	// replaces the instance default; global is an explicit companion selection
	// with the same precedence. An empty selection stays empty, never an
	// implicit host-space or global grant.
	if s.channelKnowledgeBindingsEnabled() {
		selection, err := s.ChannelKnowledgeBindingSelection(ctx, instanceID, endpointKind)
		if err != nil {
			return nil, err
		}
		if len(selection.Bindings) == 0 && !selection.IncludeGlobal {
			return []domain.KnowledgeEntry{}, nil
		}
		return s.channelGroupBindingEntries(ctx, selection)
	}
	if s.knowledgeLegacyDisabled.Load() {
		// No binding and no legacy tables: an explicit empty selection. The
		// channel does not borrow its host space implicitly.
		return []domain.KnowledgeEntry{}, nil
	}
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

// channelKnowledgeBindingsEnabled reports whether knowledge_bindings is the
// configured source of the group scope. It follows the manager switch rather
// than a transient index state: once the vault is enabled the owner selects
// group sources as project spaces, and a temporarily unready index must not
// resurrect the retired per-entry policy.
func (s *Service) channelKnowledgeBindingsEnabled() bool {
	return s != nil && s.knowledgeVault != nil && s.knowledgeVault.Enabled()
}

// channelDmKnowledge implements the private-chat rule. The DM is the verified
// bound Owner, so channel bindings never restrict it: it reads global plus
// every ready project space. Channel and system hosts are infrastructure rather
// than knowledge sources and stay excluded; a dedicated library is only
// included once its space is ready and has an indexed root.
func (s *Service) channelDmKnowledge(ctx context.Context) ([]domain.KnowledgeEntry, error) {
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
	global, err := s.channelScopeEntries(ctx, "global", "")
	if err != nil {
		return nil, err
	}
	appendEntries(global)
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		switch project.ProjectType {
		case "channel", "system":
			continue
		}
		if project.KnowledgeState != domain.KnowledgeStateReady {
			continue
		}
		if project.ProjectType == "knowledge" {
			indexed, indexErr := s.channelProjectIndexed(ctx, project.ID)
			if indexErr != nil {
				return nil, indexErr
			}
			if !indexed {
				continue
			}
		}
		entries, sourceErr := s.channelScopeEntries(ctx, "project", project.ID)
		if sourceErr != nil {
			return nil, sourceErr
		}
		appendEntries(entries)
	}
	return result, nil
}

// channelProjectIndexed reports whether a dedicated library has a readable root
// index in the source that currently owns it.
func (s *Service) channelProjectIndexed(ctx context.Context, projectID string) (bool, error) {
	decision := s.knowledgeReadDecision("project", projectID)
	if decision.Err != nil {
		return false, vaultReadFailure("project", projectID, decision.Err)
	}
	if decision.Vault {
		entry, err := s.vaultProjectRootEntry(ctx, projectID)
		if err != nil {
			return false, vaultReadFailure("project", projectID, err)
		}
		return strings.TrimSpace(entry.ID) != "", nil
	}
	entry, err := s.store.KnowledgeRootForProject(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(entry.ID) != "", nil
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
