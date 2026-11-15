package app

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// knowledgeVaultReadProjectsEnv is a support-only emergency rollback. The
// owner-visible model has a single manager switch; this environment variable
// exists for diagnostics and is never rendered in the Web UI. An unset value
// admits any non-empty project ID, an explicit comma list admits only its
// members, and an explicit empty string rolls every project back to legacy.
const knowledgeVaultReadProjectsEnv = "AHA2_KNOWLEDGE_VAULT_READ_PROJECTS"

// aha2ProjectID is the builder project used by the Phase 2 canary tests and
// kept as a named fixture now that the default is no longer a two-project list.
const aha2ProjectID = "project_6ff866f348d8a892d5e8"

func knowledgeVaultReadAllowed(projectID string) bool {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return false
	}
	raw, configured := os.LookupEnv(knowledgeVaultReadProjectsEnv)
	if !configured {
		return true
	}
	for _, item := range strings.Split(raw, ",") {
		if strings.TrimSpace(item) == projectID {
			return true
		}
	}
	return false
}

// KnowledgeVaultReadScope describes the support-only rollback allowlist for
// startup diagnostics: "all" when unset, "none" when explicitly empty,
// otherwise the configured comma list.
func KnowledgeVaultReadScope() string {
	raw, configured := os.LookupEnv(knowledgeVaultReadProjectsEnv)
	if !configured {
		return "all"
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "none"
	}
	return raw
}

type vaultKnowledgeSnapshot struct {
	Global  []domain.KnowledgeEntry
	Project []domain.KnowledgeEntry
	Stale   []domain.KnowledgeEntry
}

// vaultReadEnabled is the source-routing gate: the Web vault switch (manager
// enabled), the independent runtime gate, and the rollback allowlist must all
// agree, and the index must be ready with the manifest binding the project.
// The readiness checks make writes follow the source a read would actually use:
// an unbound or not-yet-indexed project keeps the legacy path for both.
func (s *Service) vaultReadEnabled(projectID string) bool {
	if s == nil || s.knowledgeVault == nil || !s.knowledgeVaultRuntimeRead || !knowledgeVaultReadAllowed(projectID) {
		return false
	}
	available := false
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		available = reader.IndexReady() && strings.TrimSpace(reader.Manifest().Projects[projectID]) != ""
		return nil
	})
	return err == nil && available
}

func (s *Service) vaultKnowledgeForProject(ctx context.Context, projectID string) (vaultKnowledgeSnapshot, bool) {
	if !s.vaultReadEnabled(projectID) {
		if warning := s.vaultFallbackWarning(projectID); warning != "" {
			log.Printf("knowledge vault project %s: %s", projectID, warning)
		}
		return vaultKnowledgeSnapshot{}, false
	}
	snapshot := vaultKnowledgeSnapshot{}
	ok := false
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		if !reader.IndexReady() || strings.TrimSpace(reader.Manifest().Projects[projectID]) == "" {
			return nil
		}
		global, err := reader.Entries(ctx, "global", "")
		if err != nil {
			return err
		}
		project, err := reader.Entries(ctx, "project", projectID)
		if err != nil {
			return err
		}
		var stale []domain.KnowledgeEntry
		snapshot.Global, stale = splitVaultKnowledge(mapVaultKnowledge(global))
		snapshot.Stale = append(snapshot.Stale, stale...)
		snapshot.Project, stale = splitVaultKnowledge(mapVaultKnowledge(project))
		snapshot.Stale = append(snapshot.Stale, stale...)
		ok = true
		return nil
	})
	if err != nil {
		if !errors.Is(err, knowledgevault.ErrDisabled) {
			log.Printf("knowledge vault read for project %s failed; using legacy knowledge: %v", projectID, err)
		}
		return vaultKnowledgeSnapshot{}, false
	}
	return snapshot, ok
}

func mapVaultKnowledge(entries []knowledgevault.IndexEntry) []domain.KnowledgeEntry {
	result := make([]domain.KnowledgeEntry, 0, len(entries))
	for _, entry := range entries {
		item, ok := mapVaultKnowledgeEntry(entry)
		if ok {
			result = append(result, item)
		}
	}
	return result
}

func mapVaultKnowledgeEntry(entry knowledgevault.IndexEntry) (domain.KnowledgeEntry, bool) {
	path := strings.TrimPrefix(strings.ReplaceAll(entry.Path, "\\", "/"), "./")
	if path == knowledgevault.GuideRelativePath || entry.Archive {
		return domain.KnowledgeEntry{}, false
	}
	if strings.HasPrefix(path, "archive/") || strings.HasPrefix(path, "orphans/") {
		return domain.KnowledgeEntry{}, false
	}
	switch entry.Scope {
	case "global", "project":
	default:
		return domain.KnowledgeEntry{}, false
	}
	status := domain.KnowledgeStatus(strings.ToLower(strings.TrimSpace(entry.Status)))
	if status == "" {
		status = domain.KnowledgeVerified
	}
	if status != domain.KnowledgeVerified && status != domain.KnowledgeStale {
		return domain.KnowledgeEntry{}, false
	}
	revision := entry.Revision
	if revision <= 0 {
		revision = 1
	}
	feedback := ""
	if status == domain.KnowledgeStale {
		feedback = "stale"
	}
	return domain.KnowledgeEntry{
		ID: entry.ID, Scope: entry.Scope, ProjectID: entry.ProjectID,
		ParentID: entry.ParentID, Slug: entry.Slug, SortOrder: entry.SortOrder,
		IsIndex: entry.IsIndex, Type: entry.Type, Title: entry.Title, Body: entry.Body,
		Status: status, Confidence: entry.Confidence, Revision: revision,
		ContentHash: entry.ContentHash, FeedbackState: feedback,
		SourceTaskID: entry.SourceTask, SourceTurnID: entry.SourceTurn,
		CreatedAt: parseVaultTime(entry.Created), UpdatedAt: parseVaultTime(entry.Updated),
	}, true
}

func splitVaultKnowledge(entries []domain.KnowledgeEntry) ([]domain.KnowledgeEntry, []domain.KnowledgeEntry) {
	active := make([]domain.KnowledgeEntry, 0, len(entries))
	stale := []domain.KnowledgeEntry{}
	for _, entry := range entries {
		switch entry.Status {
		case domain.KnowledgeStale:
			if !entry.IsIndex {
				stale = append(stale, entry)
			}
		case domain.KnowledgeVerified:
			active = append(active, entry)
		}
	}
	return active, stale
}

func parseVaultTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func (s *Service) vaultPendingKnowledgeIDs(ctx context.Context) map[string]bool {
	pending, err := s.store.ListKnowledgeProposals(ctx, "", "")
	if err != nil {
		return nil
	}
	result := map[string]bool{}
	for _, proposal := range pending {
		if proposal.Status == domain.KnowledgeProposalPending {
			result[proposal.EntryID] = true
		}
	}
	return result
}

func (s *Service) knowledgeGuidePrompt() string {
	if s.knowledgeVault == nil {
		return ""
	}
	guide, err := s.knowledgeVault.Guide()
	if err != nil {
		if !errors.Is(err, knowledgevault.ErrGuideNotFound) {
			log.Printf("read knowledge guide: %v", err)
		}
		return ""
	}
	return guide.Prompt
}

// applyVaultSkillPackages replaces selected skill packages with the vault copy.
// A missing or unreadable package keeps the legacy dataDir copy; the old skill
// directories are never modified or removed.
func (s *Service) applyVaultSkillPackages(ctx context.Context, projectID string, skills []domain.Skill) []domain.Skill {
	if !s.vaultReadEnabled(projectID) || len(skills) == 0 {
		return skills
	}
	result := append([]domain.Skill(nil), skills...)
	_ = s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		if !reader.VaultReady() {
			return nil
		}
		manifest := reader.Manifest()
		for index := range result {
			skill := &result[index]
			// Vault-selected skills already carry their package files; only
			// migrated legacy skills need the vault copy substituted.
			if knowledgevault.IsVaultSkillID(skill.ID) {
				continue
			}
			slug := strings.TrimSpace(skill.PackageSlug)
			if slug == "" {
				slug = knowledgevault.Slugify(skill.Name)
			}
			slug = knowledgevault.SafeSegment(slug, "skill")
			relative := ""
			switch skill.Scope {
			case "global":
				relative = "global/skills/" + slug
			case "project":
				owner := strings.TrimSpace(skill.ProjectID)
				if owner == "" {
					owner = projectID
				}
				projectSlug := strings.TrimSpace(manifest.Projects[owner])
				if projectSlug == "" {
					log.Printf("knowledge vault skill %s: project %s has no vault slug; using legacy package", skill.ID, owner)
					continue
				}
				relative = "projects/" + projectSlug + "/skills/" + slug
			default:
				continue
			}
			files, err := reader.SkillFiles(relative)
			if err != nil {
				log.Printf("knowledge vault skill %s not found at %s; using legacy package: %v", skill.ID, relative, err)
				continue
			}
			packageFiles := make([]domain.SkillFile, 0, len(files))
			names := make([]string, 0, len(files))
			for _, file := range files {
				packageFiles = append(packageFiles, domain.SkillFile{Path: file.Path, Content: file.Content})
				names = append(names, file.Path)
			}
			skill.PackageFiles = packageFiles
			skill.Files = names
			skill.SourcePath = filepath.Join(reader.Config().VaultPath, filepath.FromSlash(relative))
		}
		return nil
	})
	return result
}
