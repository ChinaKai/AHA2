package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

const petKnowledgeTextLimit = 20000

type PetKnowledgeBrief struct {
	ID           string `json:"id"`
	Scope        string `json:"scope"`
	ProjectID    string `json:"project_id,omitempty"`
	Title        string `json:"title"`
	Slug         string `json:"slug,omitempty"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	Revision     int    `json:"revision"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	SourceTaskID string `json:"source_task_id,omitempty"`
}

type PetKnowledgeProposalBrief struct {
	ID           string `json:"id"`
	EntryID      string `json:"entry_id"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	BaseRevision int    `json:"base_revision"`
	ReviewMode   string `json:"review_mode,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type PetKnowledgeView struct {
	Entries   []PetKnowledgeBrief         `json:"entries"`
	Proposals []PetKnowledgeProposalBrief `json:"proposals,omitempty"`
}

type PetKnowledgeDetail struct {
	PetKnowledgeBrief
	Body           string  `json:"body"`
	ParentID       string  `json:"parent_id,omitempty"`
	ContentHash    string  `json:"content_hash,omitempty"`
	Confidence     float64 `json:"confidence"`
	HelpCount      int     `json:"helped_count"`
	StaleCount     int     `json:"stale_count"`
	LastVerifiedAt string  `json:"last_verified_at,omitempty"`
	Truncated      bool    `json:"truncated,omitempty"`
}

func (s *Service) petKnowledgeReadable(ctx context.Context) (domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowViewTasks && !assistant.PetAllowManageKnowledge {
		return domain.OwnerAssistant{}, fmt.Errorf("主人未开启查看知识信息权限")
	}
	return assistant, nil
}

// petSkillListReadable allows create_tasks because a pet that can create or
// update tasks needs to discover usable Skill IDs for skill_ids. It is only
// used by the read-only Skill listing; Skill management stays on
// petKnowledgeWritable and other knowledge reads stay on petKnowledgeReadable.
func (s *Service) petSkillListReadable(ctx context.Context) (domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowViewTasks && !assistant.PetAllowManageKnowledge && !assistant.PetAllowCreateTasks {
		return domain.OwnerAssistant{}, fmt.Errorf("主人未开启查看 Skill 信息权限")
	}
	return assistant, nil
}

func (s *Service) petKnowledgeWritable(ctx context.Context) (domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowManageKnowledge {
		return domain.OwnerAssistant{}, fmt.Errorf("主人未开启管理知识/Skill/Prompt 权限")
	}
	return assistant, nil
}

func (s *Service) PetListKnowledge(ctx context.Context, scope, projectID string, limit int) (PetKnowledgeView, error) {
	if _, err := s.petKnowledgeReadable(ctx); err != nil {
		return PetKnowledgeView{}, err
	}
	scope = strings.TrimSpace(scope)
	projectID = strings.TrimSpace(projectID)
	if scope == "" {
		scope = "project"
	}
	if scope != "global" && scope != "project" {
		return PetKnowledgeView{}, fmt.Errorf("scope 必须是 global 或 project")
	}
	if scope == "project" && projectID == "" {
		return PetKnowledgeView{}, fmt.Errorf("project_id 不能为空")
	}
	if scope == "global" {
		projectID = ""
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var entries []domain.KnowledgeEntry
	var err error
	if scope == "project" {
		if s.projectHasNoKnowledgeSpace(ctx, projectID) {
			// Infrastructure projects have no knowledge space; an empty result
			// is the honest answer, not an unbound error.
			return PetKnowledgeView{}, nil
		}
		if snapshot, vault, readErr := s.readVaultProjectKnowledge(ctx, projectID); readErr != nil {
			return PetKnowledgeView{}, readErr
		} else if vault {
			entries = append(entries, snapshot.Project...)
			entries = append(entries, snapshot.Stale...)
		} else {
			entries, err = s.store.ListProjectKnowledge(ctx, projectID, nil)
		}
	} else {
		if active, stale, vault, readErr := s.readVaultGlobalKnowledge(ctx); readErr != nil {
			return PetKnowledgeView{}, readErr
		} else if vault {
			entries = append(entries, active...)
			entries = append(entries, stale...)
		} else {
			entries, err = s.store.ListKnowledge(ctx, "global", "", nil)
		}
	}
	if err != nil {
		return PetKnowledgeView{}, err
	}
	view := PetKnowledgeView{Entries: make([]PetKnowledgeBrief, 0, limit)}
	for _, entry := range entries {
		if len(view.Entries) == limit {
			break
		}
		view.Entries = append(view.Entries, petKnowledgeBrief(entry))
	}
	proposals, err := s.store.ListKnowledgeProposals(ctx, scope, projectID)
	if err == nil {
		for _, proposal := range proposals {
			if len(view.Proposals) == 20 {
				break
			}
			if proposal.Status != domain.KnowledgeProposalPending {
				continue
			}
			// A proposal is only actionable in the source that publishes it.
			// A vault-routed project must not show legacy proposals left over
			// from before the switch, and a legacy project must not show vault
			// proposals it cannot publish.
			if !s.KnowledgeProposalVisible(proposal) {
				continue
			}
			view.Proposals = append(view.Proposals, PetKnowledgeProposalBrief{
				ID: proposal.ID, EntryID: proposal.EntryID, Title: proposal.Proposed.Title,
				Status: string(proposal.Status), BaseRevision: proposal.BaseRevision,
				ReviewMode: proposal.ReviewMode, UpdatedAt: petKnowledgeTime(proposal.UpdatedAt),
			})
		}
	}
	return view, nil
}

func (s *Service) PetKnowledgeDetail(ctx context.Context, id string) (PetKnowledgeDetail, error) {
	if _, err := s.petKnowledgeReadable(ctx); err != nil {
		return PetKnowledgeDetail{}, err
	}
	id = strings.TrimSpace(id)
	if raw, found, err := s.vaultEntryByID(ctx, id); err != nil {
		return PetKnowledgeDetail{}, err
	} else if found {
		entry, ok := mapVaultKnowledgeEntry(raw)
		if !ok {
			return PetKnowledgeDetail{}, fmt.Errorf("知识条目不存在")
		}
		return petKnowledgeDetailFromEntry(entry)
	}
	entry, err := s.store.Knowledge(ctx, id)
	if err != nil {
		return PetKnowledgeDetail{}, fmt.Errorf("知识条目不存在")
	}
	// A vault-routed project must not reveal a legacy-only row: the read source
	// cannot see it, so reporting it as existing would invite a write to the
	// wrong store.
	if s.readSourceIsVault(entry.Scope, entry.ProjectID) {
		return PetKnowledgeDetail{}, fmt.Errorf("知识条目不存在")
	}
	return petKnowledgeDetailFromEntry(entry)
}

func petKnowledgeDetailFromEntry(entry domain.KnowledgeEntry) (PetKnowledgeDetail, error) {
	body := entry.Body
	truncated := false
	if len([]rune(body)) > petKnowledgeTextLimit {
		body = string([]rune(body)[:petKnowledgeTextLimit])
		truncated = true
	}
	return PetKnowledgeDetail{
		PetKnowledgeBrief: petKnowledgeBrief(entry),
		Body:              body, ParentID: entry.ParentID, ContentHash: entry.ContentHash,
		Confidence: entry.Confidence, HelpCount: entry.HelpedCount, StaleCount: entry.StaleCount,
		LastVerifiedAt: petKnowledgeTime(entry.LastVerifiedAt), Truncated: truncated,
	}, nil
}

func petKnowledgeTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func petKnowledgeBrief(entry domain.KnowledgeEntry) PetKnowledgeBrief {
	return PetKnowledgeBrief{
		ID: entry.ID, Scope: entry.Scope, ProjectID: entry.ProjectID, Title: entry.Title,
		Slug: entry.Slug, Type: entry.Type, Status: string(entry.Status), Revision: entry.Revision,
		UpdatedAt: petKnowledgeTime(entry.UpdatedAt), SourceTaskID: entry.SourceTaskID,
	}
}

type PetSubmitKnowledgeResult struct {
	Entries   []PetKnowledgeBrief         `json:"entries"`
	Proposals []PetKnowledgeProposalBrief `json:"proposals"`
	Warning   string                      `json:"warning,omitempty"`
}

func (s *Service) PetSubmitKnowledge(ctx context.Context, scope, projectID string, candidates []KnowledgeCandidate) (PetSubmitKnowledgeResult, error) {
	if _, err := s.petKnowledgeWritable(ctx); err != nil {
		return PetSubmitKnowledgeResult{}, err
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	projectID = strings.TrimSpace(projectID)
	if scope == "" {
		scope = "project"
		if projectID == "" {
			scope = "global"
		}
	}
	if scope != "global" && scope != "project" {
		return PetSubmitKnowledgeResult{}, fmt.Errorf("scope 必须是 global 或 project")
	}
	if scope == "global" {
		projectID = ""
	} else {
		if projectID == "" {
			return PetSubmitKnowledgeResult{}, fmt.Errorf("project_id 不能为空")
		}
		if projectID == petProjectID {
			return PetSubmitKnowledgeResult{}, fmt.Errorf("不能向宠物自己的系统项目提交知识")
		}
		project, err := s.store.Project(ctx, projectID)
		if err != nil {
			return PetSubmitKnowledgeResult{}, fmt.Errorf("项目不存在")
		}
		if project.KnowledgePolicy == "disabled" {
			return PetSubmitKnowledgeResult{}, fmt.Errorf("该项目的知识功能已关闭")
		}
	}
	if len(candidates) == 0 || len(candidates) > 10 {
		return PetSubmitKnowledgeResult{}, fmt.Errorf("一次需要提交 1–10 条知识候选")
	}
	useVault, err := s.knowledgeWriteRoute(scope, projectID)
	if err != nil {
		return PetSubmitKnowledgeResult{}, err
	}
	for index := range candidates {
		candidate := &candidates[index]
		candidate.Scope = scope
		candidate.Title = strings.TrimSpace(candidate.Title)
		candidate.Body = strings.TrimSpace(candidate.Body)
		if candidate.Title == "" || candidate.Body == "" || len([]rune(candidate.Title)) > 300 || len([]rune(candidate.Body)) > petKnowledgeTextLimit {
			return PetSubmitKnowledgeResult{}, fmt.Errorf("知识候选的标题或正文无效")
		}
		if candidate.Confidence < 0 || candidate.Confidence > 1 {
			return PetSubmitKnowledgeResult{}, fmt.Errorf("知识置信度需要在 0–1 之间")
		}
		if strings.TrimSpace(candidate.Type) == "" {
			candidate.Type = "practice"
		}
		if candidate.EntryID != "" {
			var existing domain.KnowledgeEntry
			if useVault {
				item, found, lookupErr := s.vaultDomainEntry(ctx, candidate.EntryID)
				if lookupErr != nil {
					return PetSubmitKnowledgeResult{}, lookupErr
				}
				if !found {
					return PetSubmitKnowledgeResult{}, fmt.Errorf("知识条目 %s 不在文件知识库中", candidate.EntryID)
				}
				existing = item
			} else {
				item, lookupErr := s.store.Knowledge(ctx, candidate.EntryID)
				if lookupErr != nil {
					return PetSubmitKnowledgeResult{}, fmt.Errorf("知识条目 %s 不存在", candidate.EntryID)
				}
				existing = item
			}
			if existing.Scope != scope || (scope == "project" && existing.ProjectID != projectID) {
				if scope == "global" {
					return PetSubmitKnowledgeResult{}, fmt.Errorf("只能修订 global scope 的知识条目")
				}
				return PetSubmitKnowledgeResult{}, fmt.Errorf("只能修订当前项目的知识条目")
			}
			if candidate.BaseRevision <= 0 || candidate.BaseRevision != existing.Revision {
				return PetSubmitKnowledgeResult{}, fmt.Errorf("知识条目 %s 的 base_revision 已过期", candidate.EntryID)
			}
		}
	}
	turnID := ""
	if turn, turnErr := s.store.LatestTurnForAgent(ctx, petTaskID, "main"); turnErr == nil {
		turnID = turn.ID
	}
	lineID := ""
	if scope == "project" {
		lines, _ := s.store.ListProductLines(ctx, projectID)
		lineID = resolveProductLine(lines).ID
	}
	entries, proposals, err := s.createKnowledgeCandidates(ctx, projectID, petTaskID, turnID, "", lineID, candidates)
	if err != nil {
		return PetSubmitKnowledgeResult{}, err
	}
	result := PetSubmitKnowledgeResult{Entries: make([]PetKnowledgeBrief, 0, len(entries)), Proposals: make([]PetKnowledgeProposalBrief, 0, len(proposals))}
	if !useVault {
		if scope == "global" {
			result.Warning = s.vaultGlobalFallbackWarning()
		} else {
			result.Warning = s.vaultFallbackWarning(projectID)
		}
	}
	for _, entry := range entries {
		result.Entries = append(result.Entries, petKnowledgeBrief(entry))
	}
	for _, proposal := range proposals {
		result.Proposals = append(result.Proposals, PetKnowledgeProposalBrief{
			ID: proposal.ID, EntryID: proposal.EntryID, Title: proposal.Proposed.Title,
			Status: string(proposal.Status), BaseRevision: proposal.BaseRevision, ReviewMode: proposal.ReviewMode,
		})
	}
	workTarget := projectID
	if scope == "global" {
		workTarget = "global"
	}
	s.recordPetWork(ctx, "knowledge_submitted", workTarget, fmt.Sprintf("%d", len(entries)))
	return result, nil
}

type PetSkillBrief struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Scope       string `json:"scope"`
	ProjectID   string `json:"project_id,omitempty"`
	Version     int    `json:"version"`
	Status      string `json:"status"`
	Enabled     bool   `json:"enabled"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type PetSkillDetail struct {
	PetSkillBrief
	Instructions string   `json:"instructions"`
	Files        []string `json:"files,omitempty"`
	Truncated    bool     `json:"truncated,omitempty"`
	// FileContents is the text of the package files, returned only when the
	// caller asks for it: a pet that wants to change a script has to read it
	// first, and an update that sends files replaces the whole package.
	FileContents []knowledgevault.SkillPackageFile `json:"file_contents,omitempty"`
	// FilesOmitted names package files whose content is not in FileContents:
	// binaries, oversized files, and whatever did not fit in the read budget.
	FilesOmitted []string `json:"files_omitted,omitempty"`
}

// petSkillInstructionsLimit bounds the instructions one detail call returns. A
// detail that silently cut the text would make the next update write the cut
// version back, so the limit matches what the vault itself accepts.
const petSkillInstructionsLimit = 200000

// petSkillFilesReadBudget bounds the package text one detail call returns.
const petSkillFilesReadBudget = 400000

func (s *Service) PetListSkills(ctx context.Context, scope, projectID string, limit int) ([]PetSkillBrief, error) {
	if _, err := s.petSkillListReadable(ctx); err != nil {
		return nil, err
	}
	scope = strings.TrimSpace(scope)
	projectID = strings.TrimSpace(projectID)
	if scope != "global" && scope != "project" {
		scope = ""
	}
	if scope == "project" && projectID == "" {
		return nil, fmt.Errorf("project_id 不能为空")
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	// Skills are vault packages only. The legacy skills table is never read,
	// even when no vault manager is configured.
	items, err := s.ListVaultSkills(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PetSkillBrief, 0, limit)
	for _, item := range items {
		if scope != "" && item.Scope != scope {
			continue
		}
		if scope == "project" && item.ProjectID != projectID {
			continue
		}
		if len(result) == limit {
			break
		}
		result = append(result, petSkillBrief(item))
	}
	return result, nil
}

func (s *Service) PetSkillDetail(ctx context.Context, id string) (PetSkillDetail, error) {
	return s.PetSkillDetailWithFiles(ctx, id, false)
}

// PetSkillDetailWithFiles returns a Skill and, when includeFiles is set, the
// text of its package files.
func (s *Service) PetSkillDetailWithFiles(ctx context.Context, id string, includeFiles bool) (PetSkillDetail, error) {
	if _, err := s.petKnowledgeReadable(ctx); err != nil {
		return PetSkillDetail{}, err
	}
	id = strings.TrimSpace(id)
	// Vault packages are the only skill source; a legacy id is unavailable.
	item, err := s.vaultSkillByKey(ctx, id)
	if err != nil {
		return PetSkillDetail{}, fmt.Errorf("Skill 不存在")
	}
	instructions := item.Instructions
	truncated := false
	if len([]rune(instructions)) > petSkillInstructionsLimit {
		instructions = string([]rune(instructions)[:petSkillInstructionsLimit])
		truncated = true
	}
	detail := PetSkillDetail{PetSkillBrief: petSkillBrief(item), Instructions: instructions, Files: item.Files, Truncated: truncated}
	if !includeFiles {
		return detail, nil
	}
	pkg, err := s.vaultSkillPackageByKey(ctx, id)
	if err != nil {
		return PetSkillDetail{}, fmt.Errorf("Skill 不存在")
	}
	var files []knowledgevault.SkillPackageFile
	if err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		var readErr error
		files, readErr = reader.SkillFiles(pkg.RelativePath)
		return readErr
	}); err != nil {
		return PetSkillDetail{}, fmt.Errorf("读取 Skill 文件失败：%w", err)
	}
	returned := map[string]bool{}
	budget := petSkillFilesReadBudget
	for _, file := range files {
		if len(file.Content) > budget {
			continue
		}
		budget -= len(file.Content)
		returned[file.Path] = true
		detail.FileContents = append(detail.FileContents, file)
	}
	for _, name := range item.Files {
		if !returned[name] {
			detail.FilesOmitted = append(detail.FilesOmitted, name)
		}
	}
	return detail, nil
}

func petSkillBrief(item domain.Skill) PetSkillBrief {
	return PetSkillBrief{
		ID: item.ID, Name: item.Name, Description: item.Description, Scope: item.Scope,
		ProjectID: item.ProjectID, Version: item.Version, Status: item.Status, Enabled: item.Enabled,
		UpdatedAt: petKnowledgeTime(item.UpdatedAt),
	}
}

type PetSkillInput struct {
	SkillID      string `json:"skill_id"`
	Scope        string `json:"scope"`
	ProjectID    string `json:"project_id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
	Status       string `json:"status"`
	Enabled      *bool  `json:"enabled"`
	// Files is the complete package, SKILL.md included. Leaving it out changes
	// only the SKILL.md fields and keeps every other file as it is.
	Files []knowledgevault.SkillPackageFile `json:"files"`
}

func petSkillStatusValid(status string) bool {
	switch status {
	case "", "active", "draft", "disabled", "archived":
		return true
	}
	return false
}

// petSkillWriteError turns vault scope errors into something the pet can act
// on. Archive packages are imported history and nobody can write them; that is
// a different situation from a permission the Owner could grant.
func petSkillWriteError(err error) error {
	switch {
	case errors.Is(err, knowledgevault.ErrDisabled):
		return fmt.Errorf("知识库未启用，Skill 只能写入 vault")
	case errors.Is(err, ErrVaultSkillReadOnly):
		return fmt.Errorf("归档（archive）Skill 是只读的历史导入，不能修改或删除")
	}
	return err
}

// PetCreateSkill creates a project or global Skill. The pet is the Owner's
// delegate for the whole Skill library once manage_knowledge is granted, so it
// is not limited to project scope; a global Skill applies to every Project,
// which is why scope has to be asked for explicitly.
func (s *Service) PetCreateSkill(ctx context.Context, input PetSkillInput) (PetSkillDetail, error) {
	if _, err := s.petKnowledgeWritable(ctx); err != nil {
		return PetSkillDetail{}, err
	}
	scope := strings.ToLower(strings.TrimSpace(input.Scope))
	projectID := strings.TrimSpace(input.ProjectID)
	if scope == "" {
		scope = knowledgevault.SkillScopeProject
	}
	switch scope {
	case knowledgevault.SkillScopeProject:
		if projectID == "" {
			return PetSkillDetail{}, fmt.Errorf("project_id 不能为空；要创建全局 Skill 请传 scope=global")
		}
		if _, err := s.store.Project(ctx, projectID); err != nil {
			return PetSkillDetail{}, fmt.Errorf("项目不存在")
		}
	case knowledgevault.SkillScopeGlobal:
		projectID = ""
	default:
		return PetSkillDetail{}, fmt.Errorf("scope 必须是 project 或 global")
	}
	name := strings.TrimSpace(input.Name)
	instructions := strings.TrimSpace(input.Instructions)
	if len(input.Files) == 0 && (name == "" || instructions == "") {
		return PetSkillDetail{}, fmt.Errorf("Skill 名称和说明不能为空")
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = "active"
	}
	if !petSkillStatusValid(status) {
		return PetSkillDetail{}, fmt.Errorf("Skill 状态无效")
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	item, err := s.CreateVaultSkill(ctx, VaultSkillWriteInput{
		Scope: scope, ProjectID: projectID, Name: name,
		Description: strings.TrimSpace(input.Description), Instructions: instructions,
		Files: input.Files, Status: status, Enabled: &enabled, AllowGlobal: true,
	})
	if err != nil {
		return PetSkillDetail{}, petSkillWriteError(err)
	}
	s.recordPetWork(ctx, "skill_created", item.ID, item.Name)
	return s.PetSkillDetail(ctx, item.ID)
}

// PetUpdateSkill changes a project or global Skill. Without files it edits the
// SKILL.md fields only; with files it replaces the whole package.
func (s *Service) PetUpdateSkill(ctx context.Context, input PetSkillInput) (PetSkillDetail, error) {
	if _, err := s.petKnowledgeWritable(ctx); err != nil {
		return PetSkillDetail{}, err
	}
	existing, err := s.vaultSkillByKey(ctx, strings.TrimSpace(input.SkillID))
	if err != nil {
		return PetSkillDetail{}, fmt.Errorf("Skill 不存在")
	}
	status := strings.TrimSpace(input.Status)
	if !petSkillStatusValid(status) {
		return PetSkillDetail{}, fmt.Errorf("Skill 状态无效")
	}
	item, err := s.UpdateVaultSkill(ctx, existing.ID, VaultSkillWriteInput{
		ProjectID: existing.ProjectID, BaseVersion: existing.Version,
		Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description),
		Instructions: strings.TrimSpace(input.Instructions), Files: input.Files,
		Status: status, Enabled: input.Enabled, AllowGlobal: true,
	})
	if err != nil {
		return PetSkillDetail{}, petSkillWriteError(err)
	}
	s.recordPetWork(ctx, "skill_updated", item.ID, item.Name)
	return s.PetSkillDetail(ctx, item.ID)
}

func (s *Service) PetDeleteSkill(ctx context.Context, skillID, reason string) (domain.PetApproval, error) {
	if _, err := s.petKnowledgeWritable(ctx); err != nil {
		return domain.PetApproval{}, err
	}
	reason, err := requirePetApprovalReason(reason)
	if err != nil {
		return domain.PetApproval{}, err
	}
	item, err := s.vaultSkillByKey(ctx, strings.TrimSpace(skillID))
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("Skill 不存在")
	}
	if item.Scope != knowledgevault.SkillScopeProject && item.Scope != knowledgevault.SkillScopeGlobal {
		return domain.PetApproval{}, petSkillWriteError(ErrVaultSkillReadOnly)
	}
	scopeLabel := "项目"
	if item.Scope == knowledgevault.SkillScopeGlobal {
		scopeLabel = "全局"
	}
	payload, _ := json.Marshal(map[string]string{"skill_id": item.ID, "project_id": item.ProjectID})
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "delete_skill", Title: "删除 Skill：" + item.Name,
		Summary: fmt.Sprintf("宠物申请删除%s Skill「%s」（%s，版本 %d）。批准后移入知识库回收站，可恢复。", scopeLabel, item.Name, item.ID, item.Version),
		Reason:  reason, Risk: "high", Payload: string(payload),
	})
}

func (s *Service) PetDeleteKnowledge(ctx context.Context, entryID, reason string) (domain.PetApproval, error) {
	if _, err := s.petKnowledgeWritable(ctx); err != nil {
		return domain.PetApproval{}, err
	}
	reason, err := requirePetApprovalReason(reason)
	if err != nil {
		return domain.PetApproval{}, err
	}
	entryID = strings.TrimSpace(entryID)
	// Check the vault before the legacy table: a vault-only entry has no legacy
	// row, and reporting "not found" here would contradict PetListKnowledge.
	if _, found, lookupErr := s.vaultEntryByID(ctx, entryID); lookupErr != nil {
		return domain.PetApproval{}, lookupErr
	} else if found {
		return domain.PetApproval{}, ErrKnowledgeVaultDeleteUnsupported
	}
	item, err := s.store.Knowledge(ctx, entryID)
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("知识条目不存在")
	}
	if s.readSourceIsVault(item.Scope, item.ProjectID) {
		return domain.PetApproval{}, fmt.Errorf("知识条目不在当前读取源中，不能删除")
	}
	if item.IsIndex || store.IsManagedGlobalKnowledgeCategory(item.ID) {
		return domain.PetApproval{}, fmt.Errorf("知识入口不能删除")
	}
	if item.BoundProjectID != "" && item.BindingMode != "project" {
		return domain.PetApproval{}, fmt.Errorf("该知识条目来自只读知识库，不能删除")
	}
	payload, _ := json.Marshal(map[string]string{"knowledge_id": item.ID})
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "delete_knowledge", Title: "删除知识：" + item.Title,
		Summary: fmt.Sprintf("宠物申请删除知识条目「%s」（%s，scope=%s，revision=%d）。批准后才执行。", item.Title, item.ID, item.Scope, item.Revision),
		Reason:  reason, Risk: "high", Payload: string(payload),
	})
}

type PetPromptTemplateBrief struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Layer       string `json:"layer"`
	Description string `json:"description,omitempty"`
	Version     int    `json:"version"`
	Editable    bool   `json:"editable"`
	Required    bool   `json:"required"`
	Source      string `json:"source,omitempty"`
	Protected   bool   `json:"protected,omitempty"`
}

type PetPromptTemplateDetail struct {
	PetPromptTemplateBrief
	Content string `json:"content"`
}

func (s *Service) PetListPromptTemplates(ctx context.Context) ([]PetPromptTemplateBrief, error) {
	if _, err := s.petKnowledgeReadable(ctx); err != nil {
		return nil, err
	}
	items, err := s.PromptTemplates(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PetPromptTemplateBrief, 0, len(items))
	for _, item := range items {
		result = append(result, PetPromptTemplateBrief{
			ID: item.ID, Name: item.Name, Layer: item.Layer, Description: item.Description,
			Version: item.Version, Editable: item.Editable, Required: item.Required,
			Source: item.Source, Protected: petPromptTemplateProtected(item.ID),
		})
	}
	return result, nil
}

func (s *Service) PetPromptTemplateDetail(ctx context.Context, id string) (PetPromptTemplateDetail, error) {
	if _, err := s.petKnowledgeWritable(ctx); err != nil {
		return PetPromptTemplateDetail{}, err
	}
	id = strings.TrimSpace(id)
	items, err := s.PromptTemplates(ctx)
	if err != nil {
		return PetPromptTemplateDetail{}, err
	}
	for _, item := range items {
		if item.ID != id {
			continue
		}
		return PetPromptTemplateDetail{
			PetPromptTemplateBrief: PetPromptTemplateBrief{
				ID: item.ID, Name: item.Name, Layer: item.Layer, Description: item.Description,
				Version: item.Version, Editable: item.Editable, Required: item.Required,
				Source: item.Source, Protected: petPromptTemplateProtected(item.ID),
			},
			Content: item.Content,
		}, nil
	}
	return PetPromptTemplateDetail{}, fmt.Errorf("Prompt 模板不存在")
}

// petPromptTemplateProtected keeps the pet away from its own identity, the
// shared security/role rules, and the protocols that define its boundaries.
func petPromptTemplateProtected(id string) bool {
	switch {
	case id == "core.default", strings.HasPrefix(id, "identity."),
		strings.HasPrefix(id, "role."), strings.HasPrefix(id, "policy."),
		strings.HasPrefix(id, "protocol."), strings.HasPrefix(id, "resource."):
		return true
	default:
		return false
	}
}

func (s *Service) PetRequestPromptUpdate(ctx context.Context, templateID, content, reason string) (domain.PetApproval, error) {
	if _, err := s.petKnowledgeWritable(ctx); err != nil {
		return domain.PetApproval{}, err
	}
	templateID = strings.TrimSpace(templateID)
	content = strings.TrimSpace(content)
	reason, err := requirePetApprovalReason(reason)
	if err != nil {
		return domain.PetApproval{}, err
	}
	if content == "" || len([]rune(content)) > 6000 {
		return domain.PetApproval{}, fmt.Errorf("Prompt 内容不能为空且不能超过 6000 个字符")
	}
	if petPromptTemplateProtected(templateID) {
		return domain.PetApproval{}, fmt.Errorf("宠物身份、角色、策略、协议和安全边界 Prompt 不允许由宠物修改")
	}
	items, err := s.PromptTemplates(ctx)
	if err != nil {
		return domain.PetApproval{}, err
	}
	var target domain.PromptTemplate
	found := false
	for _, item := range items {
		if item.ID == templateID {
			target, found = item, true
			break
		}
	}
	if !found || !target.Editable {
		return domain.PetApproval{}, fmt.Errorf("Prompt 模板不存在或不可编辑")
	}
	payload, _ := json.Marshal(map[string]string{"template_id": templateID, "content": content})
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "update_prompt_template", Title: "修改 Prompt：" + target.Name,
		Summary: fmt.Sprintf("宠物申请修改 Prompt 模板「%s」（%s）。批准后才写入。", target.Name, templateID),
		Reason:  reason, Risk: "high", Payload: string(payload),
	})
}
