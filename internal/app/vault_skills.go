package app

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// Vault skills are file packages, not rows in the legacy skills table. A
// preference row remembers the owner's auto-attach choice; task resolution
// merges eligible vault skills with legacy AutoAttach skills and reads the
// package through the existing Reader.SkillFiles path.

var (
	// ErrVaultSkillNotFound reports a key that the current vault does not
	// expose. Disabling a stale preference is still allowed.
	ErrVaultSkillNotFound = errors.New("knowledge vault skill not found")
	// ErrVaultSkillNotAutoAttachable covers archive skills; auto-attach only
	// makes sense for global and project libraries.
	ErrVaultSkillNotAutoAttachable = errors.New("knowledge vault skill cannot auto-attach")
	// ErrVaultGlobalConfirmationRequired enforces the owner rule that a global
	// vault skill may only auto-enable after an explicit second confirmation.
	ErrVaultGlobalConfirmationRequired = errors.New("global knowledge vault skill requires confirmation")
)

// VaultSkillView is the API shape for one vault skill plus its preference and
// the reason auto-attach would or would not take effect in a given project.
type VaultSkillView struct {
	Key       string `json:"key"`
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	ProjectID string `json:"project_id,omitempty"`
	Library   string `json:"library"`
	// ProjectName is the authoritative AHA2 project name for project and
	// archive skills. Global skills leave it empty so the UI keeps "全局".
	ProjectName         string   `json:"project_name,omitempty"`
	Description         string   `json:"description,omitempty"`
	FileCount           int      `json:"file_count"`
	Files               []string `json:"files,omitempty"`
	RelativePath        string   `json:"relative_path"`
	AbsolutePath        string   `json:"absolute_path"`
	OpenPath            string   `json:"open_path"`
	Source              string   `json:"source"`
	AutoAttach          bool     `json:"auto_attach"`
	AutoAttachEffective bool     `json:"auto_attach_effective"`
	AutoAttachConfirmed bool     `json:"auto_attach_confirmed"`
	CanUpdate           bool     `json:"can_update"`
	ProjectBound        bool     `json:"project_bound"`
	UnavailableReason   string   `json:"unavailable_reason,omitempty"`
}

// VaultSkillCatalog is the read model for the vault Skills tab and the task
// picker. BoundProjectIDs lets the Web decide whether a project may inherit
// global vault skills without a second round trip.
type VaultSkillCatalog struct {
	RuntimeRead     bool             `json:"runtime_read"`
	IndexReady      bool             `json:"index_ready"`
	BoundProjectIDs []string         `json:"bound_project_ids"`
	Skills          []VaultSkillView `json:"skills"`
}

func (s *Service) vaultSkillPreferences(ctx context.Context) map[string]domain.VaultSkillPreference {
	result := map[string]domain.VaultSkillPreference{}
	items, err := s.store.ListVaultSkillPreferences(ctx)
	if err != nil {
		log.Printf("list knowledge vault skill preferences: %v", err)
		return result
	}
	for _, item := range items {
		result[item.SkillID] = item
	}
	return result
}

// VaultSkillCatalog lists vault skills with auto-attach state. When projectID
// is set, global skills report project_bound and the effective flag for that
// project; project skills from other projects stay out of the result.
func (s *Service) VaultSkillCatalog(ctx context.Context, projectID string) (VaultSkillCatalog, error) {
	if s == nil || s.knowledgeVault == nil {
		return VaultSkillCatalog{}, knowledgevault.ErrDisabled
	}
	projectID = strings.TrimSpace(projectID)
	preferences := s.vaultSkillPreferences(ctx)
	projectNames := s.vaultProjectNames(ctx)
	result := VaultSkillCatalog{RuntimeRead: s.knowledgeVaultRuntimeRead, Skills: []VaultSkillView{}}
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		result.IndexReady = reader.IndexReady()
		manifest := reader.Manifest()
		for id := range manifest.Projects {
			result.BoundProjectIDs = append(result.BoundProjectIDs, id)
		}
		sort.Strings(result.BoundProjectIDs)
		projectBound := projectID != "" && strings.TrimSpace(manifest.Projects[projectID]) != ""
		response, err := reader.Skills(ctx)
		if err != nil {
			return err
		}
		for _, skill := range response.Skills {
			if projectID != "" && skill.Scope == "project" && skill.ProjectID != projectID {
				continue
			}
			preference := preferences[skill.Key]
			bound := true
			if projectID != "" {
				bound = projectBound
			}
			view := VaultSkillView{
				Key: skill.Key, ID: skill.ID, Slug: skill.Slug, Name: skill.Name,
				Scope: skill.Scope, ProjectID: skill.ProjectID, Library: skill.Library,
				Description: skill.Description, FileCount: skill.FileCount, Files: skill.Files,
				RelativePath: skill.RelativePath, AbsolutePath: skill.AbsolutePath, OpenPath: skill.OpenPath,
				Source: "vault", AutoAttach: preference.AutoAttach,
				AutoAttachConfirmed: !preference.GlobalAttachConfirmedAt.IsZero(),
				CanUpdate:           skill.Scope == "global" || skill.Scope == "project",
				ProjectBound:        bound,
			}
			applyVaultSkillProjectName(&view, manifest, projectNames, skill)
			if view.CanUpdate {
				view.AutoAttachEffective = preference.AutoAttach && result.RuntimeRead && result.IndexReady && bound &&
					(skill.Scope != "global" || view.AutoAttachConfirmed)
			}
			view.UnavailableReason = vaultSkillUnavailableReason(result.RuntimeRead, result.IndexReady, projectID, bound, view.CanUpdate)
			result.Skills = append(result.Skills, view)
		}
		return nil
	})
	if err != nil {
		return VaultSkillCatalog{}, err
	}
	sort.Slice(result.Skills, func(left, right int) bool {
		if result.Skills[left].Name != result.Skills[right].Name {
			return result.Skills[left].Name < result.Skills[right].Name
		}
		return result.Skills[left].Key < result.Skills[right].Key
	})
	return result, nil
}

// applyVaultSkillProjectName keeps a skill card's library label consistent with
// the library picker: the AHA2 project name first, then the vault slug, then the
// reader's own fallback. Global skills keep "全局" and no project name.
func applyVaultSkillProjectName(view *VaultSkillView, manifest knowledgevault.Manifest, names map[string]string, skill knowledgevault.SkillPackage) {
	if view.Scope == "global" {
		return
	}
	name := names[skill.ProjectID]
	if name == "" {
		slug := manifest.Projects[skill.ProjectID]
		if skill.Scope == "archive" {
			slug = manifest.Archives[skill.ProjectID]
		}
		name = strings.TrimSpace(slug)
	}
	if name == "" {
		name = skill.Library
	}
	if name != "" {
		view.ProjectName = name
		view.Library = name
	}
}

func vaultSkillUnavailableReason(runtimeRead, indexReady bool, projectID string, bound, autoAttachable bool) string {
	if !autoAttachable {
		return "archive_read_only"
	}
	if !runtimeRead {
		return "runtime_read_off"
	}
	if !indexReady {
		return "index_not_ready"
	}
	if projectID != "" && !bound {
		return "project_not_bound"
	}
	return ""
}

// SetVaultSkillAutoAttach persists only the preference. It never writes vault
// files, never creates a legacy skills row, and never touches migration/import
// metadata.
func (s *Service) SetVaultSkillAutoAttach(ctx context.Context, key string, autoAttach, confirmGlobal bool) (VaultSkillView, error) {
	if s == nil || s.knowledgeVault == nil {
		return VaultSkillView{}, knowledgevault.ErrDisabled
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return VaultSkillView{}, ErrVaultSkillNotFound
	}
	var existing domain.VaultSkillPreference
	found := false
	if items, err := s.store.ListVaultSkillPreferences(ctx); err == nil {
		for _, item := range items {
			if item.SkillID == key {
				existing, found = item, true
				break
			}
		}
	}
	var view VaultSkillView
	var resolved bool
	var indexReady bool
	projectNames := s.vaultProjectNames(ctx)
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		indexReady = reader.IndexReady()
		response, err := reader.Skills(ctx)
		if err != nil {
			return err
		}
		for _, skill := range response.Skills {
			if skill.Key != key {
				continue
			}
			if skill.Scope != "global" && skill.Scope != "project" {
				return ErrVaultSkillNotAutoAttachable
			}
			resolved = true
			view = VaultSkillView{
				Key: skill.Key, ID: skill.ID, Slug: skill.Slug, Name: skill.Name,
				Scope: skill.Scope, ProjectID: skill.ProjectID, Library: skill.Library,
				Description: skill.Description, FileCount: skill.FileCount, Files: skill.Files,
				RelativePath: skill.RelativePath, AbsolutePath: skill.AbsolutePath, OpenPath: skill.OpenPath,
				Source: "vault", CanUpdate: true,
			}
			applyVaultSkillProjectName(&view, reader.Manifest(), projectNames, skill)
			preference := domain.VaultSkillPreference{
				SkillID: skill.Key, Scope: skill.Scope, ProjectID: skill.ProjectID,
				AutoAttach: autoAttach, UpdatedAt: s.now().UTC(),
			}
			if skill.Scope == "global" && autoAttach {
				if existing.GlobalAttachConfirmedAt.IsZero() && !confirmGlobal {
					return ErrVaultGlobalConfirmationRequired
				}
				preference.GlobalAttachConfirmedAt = existing.GlobalAttachConfirmedAt
				if preference.GlobalAttachConfirmedAt.IsZero() {
					preference.GlobalAttachConfirmedAt = s.now().UTC()
				}
			}
			if err := s.store.SetVaultSkillPreference(ctx, preference); err != nil {
				return err
			}
			view.AutoAttach = autoAttach
			view.AutoAttachConfirmed = !preference.GlobalAttachConfirmedAt.IsZero()
			view.AutoAttachEffective = autoAttach && s.knowledgeVaultRuntimeRead && indexReady &&
				(skill.Scope != "global" || view.AutoAttachConfirmed)
			view.ProjectBound = true
			view.UnavailableReason = vaultSkillUnavailableReason(s.knowledgeVaultRuntimeRead, indexReady, "", true, true)
			return nil
		}
		return nil
	})
	if err != nil {
		return VaultSkillView{}, err
	}
	if !resolved {
		if autoAttach {
			return VaultSkillView{}, ErrVaultSkillNotFound
		}
		// Disabling a stale preference is a safe cleanup: the vault package is
		// gone, so the row must not resurrect if a new package takes the key.
		if found {
			existing.AutoAttach = false
			existing.GlobalAttachConfirmedAt = time.Time{}
			existing.UpdatedAt = s.now().UTC()
			if err := s.store.SetVaultSkillPreference(ctx, existing); err != nil {
				return VaultSkillView{}, err
			}
			return VaultSkillView{Key: key, Source: "vault", CanUpdate: true}, nil
		}
		return VaultSkillView{}, ErrVaultSkillNotFound
	}
	return view, nil
}

// vaultAutoAttachSelection is the auto-attach result for a project that reads
// from the vault. Slugs is the set of package slugs covered by the selection,
// which lets the caller explain legacy AutoAttach rows that no longer apply.
type vaultAutoAttachSelection struct {
	keys  []string
	slugs map[string]bool
}

// vaultAutoAttachSkillKeys returns the vault skills that should be pre-selected
// for a new task.
func (s *Service) vaultAutoAttachSkillKeys(ctx context.Context, projectID string) []string {
	return s.vaultAutoAttachSelection(ctx, projectID).keys
}

// vaultAutoAttachSelection resolves the vault skills that should be
// pre-selected for a new task. It applies only when the vault read gate, the
// project's manifest binding, and the index are all ready. Preferences that can
// no longer be honoured are logged instead of silently disappearing.
func (s *Service) vaultAutoAttachSelection(ctx context.Context, projectID string) vaultAutoAttachSelection {
	selection := vaultAutoAttachSelection{slugs: map[string]bool{}}
	preferences, err := s.store.ListVaultSkillPreferences(ctx)
	if err != nil {
		log.Printf("knowledge vault auto-attach preferences for project %s: %v", projectID, err)
		return selection
	}
	applicable := make([]domain.VaultSkillPreference, 0, len(preferences))
	for _, item := range preferences {
		if !item.AutoAttach {
			continue
		}
		switch item.Scope {
		case "global":
			if !item.GlobalAttachConfirmedAt.IsZero() {
				applicable = append(applicable, item)
			}
		case "project":
			if item.ProjectID == projectID {
				applicable = append(applicable, item)
			}
		}
	}
	if len(applicable) == 0 {
		return selection
	}
	if !s.vaultReadEnabled(projectID) {
		log.Printf("knowledge vault auto-attach for project %s: %d preference(s) not applied (%s)", projectID, len(applicable), s.vaultReadDisabledReason(projectID))
		return selection
	}
	available := map[string]knowledgevault.SkillPackage{}
	_ = s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		response, err := reader.Skills(ctx)
		if err != nil {
			return err
		}
		for _, skill := range response.Skills {
			if skill.Scope == "global" || (skill.Scope == "project" && skill.ProjectID == projectID) {
				available[skill.Key] = skill
			}
		}
		return nil
	})
	// A global and a project library can carry the same slug. Selecting both
	// would put two identically named skills into the task prompt, so keep the
	// more specific project copy and preserve the first-seen order.
	bySlug := map[string]string{}
	order := make([]string, 0, len(applicable))
	for _, item := range applicable {
		skill, ok := available[item.SkillID]
		if !ok {
			log.Printf("knowledge vault auto-attach: skill %s no longer exists in the vault; skipped for project %s", item.SkillID, projectID)
			continue
		}
		slug := strings.TrimSpace(skill.Slug)
		if slug == "" {
			slug = skill.Key
		}
		current, seen := bySlug[slug]
		if !seen {
			order = append(order, slug)
			bySlug[slug] = item.SkillID
			continue
		}
		if skill.Scope == "project" && available[current].Scope != "project" {
			bySlug[slug] = item.SkillID
			continue
		}
		log.Printf("knowledge vault auto-attach: skill %s shares slug %q with %s; skipped for project %s", item.SkillID, slug, current, projectID)
	}
	selection.keys = make([]string, 0, len(order))
	for _, slug := range order {
		selection.keys = append(selection.keys, bySlug[slug])
		selection.slugs[slug] = true
	}
	return selection
}

// supersededLegacyAutoAttach lists the legacy AutoAttach rows that used to be
// merged into a task but are now excluded because the project reads auto-attach
// from the vault. Rows whose slug is covered by the vault selection are not
// reported: the same package is still attached, just under its vault id. The
// returned rows have no vault counterpart, so the task silently loses them
// unless the caller reports why.
func (s *Service) supersededLegacyAutoAttach(ctx context.Context, projectID string, vaultSlugs map[string]bool) []domain.Skill {
	items, err := s.store.ListSkills(ctx, "", "", true)
	if err != nil {
		log.Printf("knowledge vault auto-attach for project %s: list legacy skills: %v", projectID, err)
		return nil
	}
	result := make([]domain.Skill, 0)
	for _, item := range items {
		if !item.AutoAttach || !(item.Scope == "global" || (item.Scope == "project" && item.ProjectID == projectID)) {
			continue
		}
		slug := strings.TrimSpace(item.PackageSlug)
		if slug == "" {
			slug = knowledgevault.Slugify(item.Name)
		}
		slug = knowledgevault.SafeSegment(slug, "skill")
		if vaultSlugs[slug] {
			continue
		}
		result = append(result, item)
	}
	return result
}

func (s *Service) vaultReadDisabledReason(projectID string) string {
	if s == nil || s.knowledgeVault == nil {
		return "knowledge vault is not configured"
	}
	if !s.knowledgeVaultRuntimeRead {
		return "vault runtime read is off"
	}
	if !knowledgeVaultReadAllowed(projectID) {
		return "project is excluded by the read rollback allowlist"
	}
	reason := "project is not bound in the vault manifest or the index is not ready"
	_ = s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		if reader.IndexReady() && strings.TrimSpace(reader.Manifest().Projects[projectID]) != "" {
			reason = "vault reader is unavailable"
		}
		return nil
	})
	return reason
}

// vaultTaskSkills materializes explicitly selected vault skill ids. It reuses
// Reader.SkillFiles, so the prompt sees the same package files as a migrated
// legacy skill and there is no second reader.
func (s *Service) vaultTaskSkills(ctx context.Context, projectID string, ids []string) []domain.Skill {
	if len(ids) == 0 {
		return nil
	}
	if !s.vaultReadEnabled(projectID) {
		log.Printf("knowledge vault task skills for project %s: %d selected skill(s) unavailable (%s)", projectID, len(ids), s.vaultReadDisabledReason(projectID))
		return nil
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	result := make([]domain.Skill, 0, len(ids))
	_ = s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		response, err := reader.Skills(ctx)
		if err != nil {
			return err
		}
		for _, skill := range response.Skills {
			if !wanted[skill.Key] {
				continue
			}
			if skill.Scope != "global" && !(skill.Scope == "project" && skill.ProjectID == projectID) {
				log.Printf("knowledge vault task skill %s does not apply to project %s; skipped", skill.Key, projectID)
				continue
			}
			files, err := reader.SkillFiles(skill.RelativePath)
			if err != nil {
				log.Printf("knowledge vault task skill %s package at %s is unreadable; skipped: %v", skill.Key, skill.RelativePath, err)
				continue
			}
			packageFiles := make([]domain.SkillFile, 0, len(files))
			names := make([]string, 0, len(files))
			for _, file := range files {
				packageFiles = append(packageFiles, domain.SkillFile{Path: file.Path, Content: file.Content})
				names = append(names, file.Path)
			}
			result = append(result, domain.Skill{
				ID: skill.Key, PackageSlug: skill.Slug, Scope: skill.Scope, ProjectID: skill.ProjectID,
				Name: skill.Name, Description: skill.Description, Version: 1, Status: "active", Enabled: true,
				SourcePath: filepath.Join(reader.Config().VaultPath, filepath.FromSlash(skill.RelativePath)),
				Files:      names, PackageFiles: packageFiles,
			})
		}
		return nil
	})
	if len(result) != len(ids) {
		seen := map[string]bool{}
		for _, item := range result {
			seen[item.ID] = true
		}
		for _, id := range ids {
			if !seen[id] {
				log.Printf("knowledge vault task skill %s for project %s was not loaded; task continues with the remaining skills", id, projectID)
			}
		}
	}
	return result
}

// vaultSkillExistsInProject validates one selected vault id for task updates.
func (s *Service) vaultSkillExistsInProject(ctx context.Context, projectID, id string) bool {
	if !s.vaultReadEnabled(projectID) {
		return false
	}
	found := false
	_ = s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		response, err := reader.Skills(ctx)
		if err != nil {
			return err
		}
		for _, skill := range response.Skills {
			if skill.Key != id {
				continue
			}
			if skill.Scope == "global" || (skill.Scope == "project" && skill.ProjectID == projectID) {
				found = true
			}
			break
		}
		return nil
	})
	return found
}
