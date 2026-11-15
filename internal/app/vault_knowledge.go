package app

import (
	"context"
	"errors"
	"fmt"
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

// knowledgeVaultBackend is the subset of *knowledgevault.Manager the service
// uses. Keeping it an interface gives tests a way to reproduce a reader
// failure, which must stop the read instead of falling back to legacy rows.
type knowledgeVaultBackend interface {
	Config() knowledgevault.Config
	Enabled() bool
	Guide() (knowledgevault.Guide, error)
	WithReader(func(*knowledgevault.Reader) error) error
	SetRuntimeWrite(bool)
	RuntimeWriteEnabled() bool
	WriteKnowledge(context.Context, knowledgevault.WriteRequest) (knowledgevault.WriteResult, error)
	KnowledgeFeedback(context.Context, knowledgevault.FeedbackRequest) (knowledgevault.FeedbackResult, error)
}

type vaultKnowledgeSnapshot struct {
	Global  []domain.KnowledgeEntry
	Project []domain.KnowledgeEntry
	Stale   []domain.KnowledgeEntry
}

// knowledgeReadDecision is the single "which store is authoritative" decision
// every knowledge read uses. Vault=false means the legacy tables are the
// current source and a fallback warning explains why. Vault=true with Err set
// is a fail-closed state: the vault is the current source but it could not be
// read, so callers must not serve legacy knowledge instead.
type knowledgeReadDecision struct {
	Vault   bool
	Warning string
	Err     error
}

// knowledgeReadDecision routes one scope/project. The Web vault switch
// (manager enabled), the independent runtime read gate and the support
// rollback allowlist must all agree; for a project the index must be ready and
// the manifest must bind it. Global knowledge has no project binding, so only
// its own gate decides.
func (s *Service) knowledgeReadDecision(scope, projectID string) knowledgeReadDecision {
	scope = strings.ToLower(strings.TrimSpace(scope))
	projectID = strings.TrimSpace(projectID)
	if s == nil || s.knowledgeVault == nil || !s.knowledgeVault.Enabled() {
		return knowledgeReadDecision{}
	}
	if !s.knowledgeVaultRuntimeRead {
		return knowledgeReadDecision{Warning: "知识库已启用，但支持诊断开关关闭了读取；本次使用旧知识库。"}
	}
	if scope == "global" {
		if raw, configured := os.LookupEnv(knowledgeVaultReadProjectsEnv); configured && strings.TrimSpace(raw) == "" {
			return knowledgeReadDecision{Warning: "知识库已启用，但支持诊断回退名单已清空；全局知识使用旧知识库。"}
		}
	} else if !knowledgeVaultReadAllowed(projectID) {
		return knowledgeReadDecision{Warning: "知识库已启用，但支持诊断的项目回退名单排除了当前项目；本次使用旧知识库。"}
	}
	decision := knowledgeReadDecision{Vault: true}
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		switch {
		case !reader.IndexReady():
			decision.Vault = false
			decision.Warning = "知识库已启用，但索引未就绪；本次使用旧知识库。"
		case scope != "global" && strings.TrimSpace(reader.Manifest().Projects[projectID]) == "":
			decision.Vault = false
			decision.Warning = "知识库已启用，但项目未绑定到知识库；本次使用旧知识库。"
		}
		return nil
	})
	if err != nil {
		// The manager is on and every routing gate admitted this scope, so the
		// vault is authoritative. An unreadable reader must fail the read, not
		// silently serve the old tables the owner moved away from.
		decision.Vault = true
		decision.Err = err
	}
	return decision
}

// vaultProjectNames returns authoritative display names from the AHA2 projects
// table. The vault manifest only stores derived slugs plus a display-name cache
// that older rebuilds filled with the generated "知识首页" page title, so every
// vault read model that shows a project name starts here.
func (s *Service) vaultProjectNames(ctx context.Context) map[string]string {
	result := map[string]string{}
	if s == nil || s.store == nil {
		return result
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		log.Printf("list projects for vault display names: %v", err)
		return result
	}
	for _, project := range projects {
		if name := strings.TrimSpace(project.Name); name != "" {
			result[project.ID] = name
		}
	}
	return result
}

// VaultLibraries returns the vault library picker with AHA2 project names
// overlaid on the manifest. A project deleted from the database keeps its vault
// slug rather than the stale manifest display-name cache.
func (s *Service) VaultLibraries(ctx context.Context) (knowledgevault.LibraryList, error) {
	if s == nil || s.knowledgeVault == nil {
		return knowledgevault.LibraryList{}, knowledgevault.ErrDisabled
	}
	names := s.vaultProjectNames(ctx)
	var result knowledgevault.LibraryList
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		result = reader.Libraries(ctx)
		applyVaultLibraryNames(&result, reader.Manifest(), names)
		return nil
	})
	if err != nil {
		return knowledgevault.LibraryList{}, err
	}
	return result, nil
}

// applyVaultLibraryNames replaces manifest-derived names in place. It covers
// the duplicated Libraries/Projects/Archived slices because the reader builds
// both views from the same manifest entries.
func applyVaultLibraryNames(result *knowledgevault.LibraryList, manifest knowledgevault.Manifest, names map[string]string) {
	resolve := func(id, slug, fallback string) string {
		if name := names[id]; name != "" {
			return name
		}
		if slug = strings.TrimSpace(slug); slug != "" {
			return slug
		}
		return fallback
	}
	for index := range result.Projects {
		item := &result.Projects[index]
		item.Name = resolve(item.ProjectID, manifest.Projects[item.ProjectID], item.Name)
	}
	for index := range result.Archived {
		item := &result.Archived[index]
		item.Name = resolve(item.ProjectID, manifest.Archives[item.ProjectID], item.Name)
	}
	for index := range result.Libraries {
		item := &result.Libraries[index]
		switch item.Kind {
		case "project":
			item.Name = resolve(item.ProjectID, manifest.Projects[item.ProjectID], item.Name)
		case "archive":
			item.Name = resolve(item.ProjectID, manifest.Archives[item.ProjectID], item.Name)
		}
	}
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

// vaultReadEnabled reports whether the vault is the current source for the
// project, including the fail-closed case where it is authoritative but could
// not be read. Write routing follows this so a transient reader failure never
// writes a row the project no longer reads.
func (s *Service) vaultReadEnabled(projectID string) bool {
	return s.knowledgeReadDecision("project", projectID).Vault
}

func vaultReadFailure(scope, projectID string, err error) error {
	target := "global"
	if strings.TrimSpace(scope) != "global" {
		target = "project " + strings.TrimSpace(projectID)
	}
	return fmt.Errorf("文件知识库读取 %s 失败，已停止使用旧知识库: %w", target, err)
}

// vaultProjectSnapshot reads one project's vault entries. It re-checks the
// index and manifest binding that selected the source, so a vault that turns
// unready between routing and reading is an error rather than an empty success.
func (s *Service) vaultProjectSnapshot(ctx context.Context, projectID string) (vaultKnowledgeSnapshot, error) {
	snapshot := vaultKnowledgeSnapshot{}
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		if !reader.IndexReady() || strings.TrimSpace(reader.Manifest().Projects[strings.TrimSpace(projectID)]) == "" {
			return knowledgevault.ErrReaderNotReady
		}
		project, err := reader.Entries(ctx, "project", strings.TrimSpace(projectID))
		if err != nil {
			return err
		}
		var stale []domain.KnowledgeEntry
		snapshot.Project, stale = splitVaultKnowledge(mapVaultKnowledge(project))
		snapshot.Stale = append(snapshot.Stale, stale...)
		return nil
	})
	if err != nil {
		return vaultKnowledgeSnapshot{}, err
	}
	return snapshot, nil
}

// vaultGlobalSnapshot reads global vault entries. Global routing never looks at
// a project binding: that is what keeps a global read correct when the project
// itself still uses the legacy tables.
func (s *Service) vaultGlobalSnapshot(ctx context.Context) ([]domain.KnowledgeEntry, []domain.KnowledgeEntry, error) {
	var active, stale []domain.KnowledgeEntry
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		if !reader.IndexReady() {
			return knowledgevault.ErrReaderNotReady
		}
		entries, err := reader.Entries(ctx, "global", "")
		if err != nil {
			return err
		}
		active, stale = splitVaultKnowledge(mapVaultKnowledge(entries))
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return active, stale, nil
}

// readVaultProjectKnowledge returns (snapshot, true, nil) when the vault is the
// current source, (zero, false, nil) when the legacy tables are, and an error
// when the vault is authoritative but unreadable. Callers must stop on the
// error instead of reading legacy rows.
func (s *Service) readVaultProjectKnowledge(ctx context.Context, projectID string) (vaultKnowledgeSnapshot, bool, error) {
	decision := s.knowledgeReadDecision("project", projectID)
	if decision.Err != nil {
		log.Printf("knowledge vault project %s: %v", projectID, decision.Err)
		return vaultKnowledgeSnapshot{}, true, vaultReadFailure("project", projectID, decision.Err)
	}
	if !decision.Vault {
		if decision.Warning != "" {
			log.Printf("knowledge vault project %s: %s", projectID, decision.Warning)
		}
		return vaultKnowledgeSnapshot{}, false, nil
	}
	snapshot, err := s.vaultProjectSnapshot(ctx, projectID)
	if err != nil {
		log.Printf("knowledge vault project %s read failed: %v", projectID, err)
		return vaultKnowledgeSnapshot{}, true, vaultReadFailure("project", projectID, err)
	}
	return snapshot, true, nil
}

// readVaultGlobalKnowledge is the global counterpart of
// readVaultProjectKnowledge. Its gate is independent of any project binding.
func (s *Service) readVaultGlobalKnowledge(ctx context.Context) ([]domain.KnowledgeEntry, []domain.KnowledgeEntry, bool, error) {
	decision := s.knowledgeReadDecision("global", "")
	if decision.Err != nil {
		log.Printf("knowledge vault global: %v", decision.Err)
		return nil, nil, true, vaultReadFailure("global", "", decision.Err)
	}
	if !decision.Vault {
		if decision.Warning != "" {
			log.Printf("knowledge vault global: %s", decision.Warning)
		}
		return nil, nil, false, nil
	}
	active, stale, err := s.vaultGlobalSnapshot(ctx)
	if err != nil {
		log.Printf("knowledge vault global read failed: %v", err)
		return nil, nil, true, vaultReadFailure("global", "", err)
	}
	return active, stale, true, nil
}

// pendingKnowledgeIDs returns pending proposal entry IDs that target one
// source. Stale vault entries with a pending vault proposal are suppressed just
// like legacy entries with a pending legacy proposal.
func (s *Service) pendingKnowledgeIDs(ctx context.Context, vault bool) map[string]bool {
	proposals, err := s.store.ListKnowledgeProposals(ctx, "", "")
	if err != nil {
		return nil
	}
	result := map[string]bool{}
	for _, proposal := range proposals {
		if proposal.Status != domain.KnowledgeProposalPending {
			continue
		}
		targetVault := proposal.TargetSource == domain.KnowledgeProposalTargetVault
		if targetVault == vault {
			result[proposal.EntryID] = true
		}
	}
	return result
}

// projectKnowledgeForTask is the project half of a Turn/Agent knowledge read.
// The returned source tells the caller which pending set applies. Vault errors
// are returned, never turned into a legacy fallback.
func (s *Service) projectKnowledgeForTask(ctx context.Context, projectID string, productLine domain.ProductLine, excludePending bool) ([]domain.KnowledgeEntry, []domain.KnowledgeEntry, string, error) {
	snapshot, vault, err := s.readVaultProjectKnowledge(ctx, projectID)
	if err != nil {
		return nil, nil, "vault", err
	}
	if vault {
		stale := snapshot.Stale
		if excludePending {
			pending := s.pendingKnowledgeIDs(ctx, true)
			stale = filterPendingStale(stale, pending)
		}
		return snapshot.Project, stale, "vault", nil
	}
	active, _ := s.store.ListApplicableKnowledge(ctx, projectID, productLine.ID, []domain.KnowledgeStatus{domain.KnowledgeVerified})
	stale, _ := s.store.ListApplicableKnowledge(ctx, projectID, productLine.ID, []domain.KnowledgeStatus{domain.KnowledgeStale})
	if excludePending {
		stale = filterPendingStale(stale, s.pendingKnowledgeIDs(ctx, false))
	}
	return active, stale, "legacy", nil
}

// globalKnowledgeForTask is the global half. It routes independently from the
// project so an unbound project can still be served by a ready vault global
// library, and a vault-bound project never sees legacy global rows.
func (s *Service) globalKnowledgeForTask(ctx context.Context, excludePending bool) ([]domain.KnowledgeEntry, []domain.KnowledgeEntry, string, error) {
	active, stale, vault, err := s.readVaultGlobalKnowledge(ctx)
	if err != nil {
		return nil, nil, "vault", err
	}
	if vault {
		if excludePending {
			stale = filterPendingStale(stale, s.pendingKnowledgeIDs(ctx, true))
		}
		return active, stale, "vault", nil
	}
	active, _ = s.store.ListKnowledge(ctx, "global", "", []domain.KnowledgeStatus{domain.KnowledgeVerified})
	stale, _ = s.store.ListKnowledge(ctx, "global", "", []domain.KnowledgeStatus{domain.KnowledgeStale})
	if excludePending {
		stale = filterPendingStale(stale, s.pendingKnowledgeIDs(ctx, false))
	}
	return active, stale, "legacy", nil
}

func filterPendingStale(entries []domain.KnowledgeEntry, pending map[string]bool) []domain.KnowledgeEntry {
	result := make([]domain.KnowledgeEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsIndex || pending[entry.ID] {
			continue
		}
		result = append(result, entry)
	}
	return result
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
