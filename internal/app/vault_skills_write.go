package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// Vault skill writes are file-first: SKILL.md plus text assets under
// global/skills/<slug>/ or projects/<manifest-slug>/skills/<slug>/. The legacy
// skills table is never written by these paths. Auto-attach stays a separate
// preference row keyed by the stable vault skill key.

var (
	// ErrVaultSkillForbidden reports a scope/project the caller may not write.
	ErrVaultSkillForbidden = errors.New("knowledge vault skill permission denied")
	// ErrVaultSkillReadOnly covers archive packages.
	ErrVaultSkillReadOnly = errors.New("knowledge vault skill is read-only")
)

// VaultSkillWriteInput is the app-level create/update form shared by the Web
// editor, the Agent Control API and the pet MCP tools.
type VaultSkillWriteInput struct {
	Scope        string
	ProjectID    string
	Slug         string
	Name         string
	Description  string
	Instructions string
	Files        []knowledgevault.SkillPackageFile
	BaseVersion  int
	Enabled      *bool
	Status       string
	AllowGlobal  bool
}

// CreateVaultSkill writes a new package. Auto-attach is deliberately not part
// of creation: every new skill starts with no preference row, so it is not
// preselected for new tasks until the owner enables it.
func (s *Service) CreateVaultSkill(ctx context.Context, input VaultSkillWriteInput) (domain.Skill, error) {
	if s == nil || s.knowledgeVault == nil {
		return domain.Skill{}, knowledgevault.ErrDisabled
	}
	scope := strings.ToLower(strings.TrimSpace(input.Scope))
	if scope == "" {
		scope = knowledgevault.SkillScopeProject
	}
	projectID := strings.TrimSpace(input.ProjectID)
	switch scope {
	case knowledgevault.SkillScopeGlobal:
		if !input.AllowGlobal {
			return domain.Skill{}, ErrVaultSkillForbidden
		}
		projectID = ""
	case knowledgevault.SkillScopeProject:
		if projectID == "" {
			return domain.Skill{}, fmt.Errorf("project_id 不能为空")
		}
		if _, err := s.store.Project(ctx, projectID); err != nil {
			return domain.Skill{}, fmt.Errorf("项目不存在")
		}
	default:
		return domain.Skill{}, fmt.Errorf("scope 必须是 project 或 global")
	}
	result, err := s.knowledgeVault.CreateSkillPackage(ctx, knowledgevault.SkillWriteRequest{
		Scope: scope, ProjectID: projectID, Slug: input.Slug, Name: input.Name,
		Description:  skillDescription(input.Name, input.Description, input.Instructions),
		Instructions: input.Instructions, Files: input.Files,
		Enabled: input.Enabled, Status: input.Status,
	})
	if err != nil {
		return domain.Skill{}, err
	}
	return s.vaultSkillByKey(ctx, result.Key)
}

// UpdateVaultSkill rewrites SKILL.md (and any supplied files) in place. A
// missing Files list rewrites only SKILL.md and leaves every other package
// asset untouched.
func (s *Service) UpdateVaultSkill(ctx context.Context, key string, input VaultSkillWriteInput) (domain.Skill, error) {
	if s == nil || s.knowledgeVault == nil {
		return domain.Skill{}, knowledgevault.ErrDisabled
	}
	existing, err := s.vaultSkillPackageByKey(ctx, key)
	if err != nil {
		return domain.Skill{}, err
	}
	switch existing.Scope {
	case knowledgevault.SkillScopeGlobal:
		if !input.AllowGlobal {
			return domain.Skill{}, ErrVaultSkillForbidden
		}
	case knowledgevault.SkillScopeProject:
		projectID := strings.TrimSpace(input.ProjectID)
		if projectID != "" && projectID != existing.ProjectID {
			return domain.Skill{}, ErrVaultSkillForbidden
		}
	default:
		return domain.Skill{}, ErrVaultSkillReadOnly
	}
	requestedFiles := input.Files
	if len(requestedFiles) > 0 {
		// An Agent sends the complete package, so the request scope must still
		// match the package it resolved; the file paths themselves are validated
		// by the vault writer.
		if scope := strings.ToLower(strings.TrimSpace(input.Scope)); scope != "" && scope != existing.Scope {
			return domain.Skill{}, ErrVaultSkillForbidden
		}
	}
	result, err := s.knowledgeVault.UpdateSkillPackage(ctx, knowledgevault.SkillWriteRequest{
		Key: existing.Key, Scope: existing.Scope, ProjectID: existing.ProjectID,
		Name: input.Name, Description: input.Description, Instructions: input.Instructions,
		Files: requestedFiles, BaseVersion: input.BaseVersion, Enabled: input.Enabled, Status: input.Status,
	})
	if err != nil {
		return domain.Skill{}, err
	}
	return s.vaultSkillByKey(ctx, result.Key)
}

// DeleteVaultSkill moves the package into the vault trash. Archive packages
// cannot be deleted, and a global package needs the owner/global permission.
func (s *Service) DeleteVaultSkill(ctx context.Context, key string, allowGlobal bool) error {
	if s == nil || s.knowledgeVault == nil {
		return knowledgevault.ErrDisabled
	}
	existing, err := s.vaultSkillPackageByKey(ctx, key)
	if err != nil {
		return err
	}
	switch existing.Scope {
	case knowledgevault.SkillScopeGlobal:
		if !allowGlobal {
			return ErrVaultSkillForbidden
		}
	case knowledgevault.SkillScopeProject:
	default:
		return ErrVaultSkillReadOnly
	}
	if _, err := s.knowledgeVault.DeleteSkillPackage(ctx, existing.Key); err != nil {
		return err
	}
	// Clear the preference so a future package that happens to take the same
	// key is not silently auto-attached by a stale row.
	if preferences, err := s.store.ListVaultSkillPreferences(ctx); err == nil {
		for _, preference := range preferences {
			if preference.SkillID != existing.Key {
				continue
			}
			preference.AutoAttach = false
			preference.GlobalAttachConfirmedAt = time.Time{}
			preference.UpdatedAt = s.now().UTC()
			if err := s.store.SetVaultSkillPreference(ctx, preference); err != nil {
				return fmt.Errorf("clear vault skill preference: %w", err)
			}
			break
		}
	}
	return nil
}

// ListVaultSkills maps every package the vault exposes into the shared Skill
// shape so the read-only Web list and the task picker have one source. The
// legacy table is neither read nor written.
func (s *Service) ListVaultSkills(ctx context.Context) ([]domain.Skill, error) {
	if s == nil || s.knowledgeVault == nil {
		return []domain.Skill{}, nil
	}
	preferences := s.vaultSkillPreferences(ctx)
	result := []domain.Skill{}
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		response, err := reader.Skills(ctx)
		if err != nil {
			return err
		}
		for _, skill := range response.Skills {
			result = append(result, vaultSkillToDomain(skill, preferences[skill.Key]))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// vaultSkillByKey resolves one stable vault key into the API shape. Legacy
// skills-table ids are not aliases: after the cutover they are unavailable.
func (s *Service) vaultSkillByKey(ctx context.Context, key string) (domain.Skill, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return domain.Skill{}, ErrVaultSkillNotFound
	}
	if s == nil || s.knowledgeVault == nil {
		return domain.Skill{}, knowledgevault.ErrDisabled
	}
	preferences := s.vaultSkillPreferences(ctx)
	var result domain.Skill
	found := false
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		response, err := reader.Skills(ctx)
		if err != nil {
			return err
		}
		for _, skill := range response.Skills {
			if skill.Key == key {
				result = vaultSkillToDomain(skill, preferences[skill.Key])
				found = true
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return domain.Skill{}, err
	}
	if !found {
		return domain.Skill{}, ErrVaultSkillNotFound
	}
	return result, nil
}

func (s *Service) vaultSkillPackageByKey(ctx context.Context, key string) (knowledgevault.SkillPackage, error) {
	if s == nil || s.knowledgeVault == nil {
		return knowledgevault.SkillPackage{}, knowledgevault.ErrDisabled
	}
	key = strings.TrimSpace(key)
	var result knowledgevault.SkillPackage
	found := false
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		response, err := reader.Skills(ctx)
		if err != nil {
			return err
		}
		for _, skill := range response.Skills {
			if skill.Key == key {
				result = skill
				found = true
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return knowledgevault.SkillPackage{}, err
	}
	if !found {
		return knowledgevault.SkillPackage{}, ErrVaultSkillNotFound
	}
	return result, nil
}

// vaultSkillKeyForName resolves a well-known skill (tiered-code-review) from
// the vault, preferring the source project's library over global.
func (s *Service) vaultSkillKeyForName(ctx context.Context, projectID, name string) (string, bool) {
	if s == nil || s.knowledgeVault == nil {
		return "", false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	projectKey, globalKey := "", ""
	_ = s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		response, err := reader.Skills(ctx)
		if err != nil {
			return nil
		}
		for _, skill := range response.Skills {
			if !skill.Enabled || skill.Status != "active" {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(skill.Name), name) && !strings.EqualFold(strings.TrimSpace(skill.Slug), name) {
				continue
			}
			switch skill.Scope {
			case knowledgevault.SkillScopeProject:
				if skill.ProjectID == projectID && projectKey == "" {
					projectKey = skill.Key
				}
			case knowledgevault.SkillScopeGlobal:
				if globalKey == "" {
					globalKey = skill.Key
				}
			}
		}
		return nil
	})
	if projectKey != "" {
		return projectKey, true
	}
	if globalKey != "" {
		return globalKey, true
	}
	return "", false
}

// skillDescription keeps SKILL.md valid when an older caller omits the
// optional description field: derive a single-line summary from the first line
// of the instructions instead of rejecting the skill.
func skillDescription(name, description, instructions string) string {
	description = strings.TrimSpace(strings.ReplaceAll(description, "\n", " "))
	if description != "" {
		return description
	}
	for _, line := range strings.Split(instructions, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#>*- "))
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > 160 {
			line = string(runes[:160])
		}
		return line
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Skill"
	}
	return name
}

func vaultSkillToDomain(skill knowledgevault.SkillPackage, preference domain.VaultSkillPreference) domain.Skill {
	modified := time.Time{}
	if info, err := os.Stat(skill.OpenPath); err == nil {
		modified = info.ModTime().UTC()
	}
	return domain.Skill{
		ID: skill.Key, PackageSlug: skill.Slug, Scope: skill.Scope, ProjectID: skill.ProjectID,
		Name: skill.Name, Description: skill.Description, Instructions: skill.Instructions,
		Version: skill.Version, Status: skill.Status, Enabled: skill.Enabled,
		AutoAttach: preference.AutoAttach, SourcePath: skill.AbsolutePath,
		CanUpdate: skill.Scope == knowledgevault.SkillScopeGlobal || skill.Scope == knowledgevault.SkillScopeProject,
		Files:     append([]string{}, skill.Files...), CreatedAt: modified, UpdatedAt: modified,
	}
}
