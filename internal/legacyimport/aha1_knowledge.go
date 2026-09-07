package legacyimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// Target is the subset of Store used by the AHA1 knowledge importer. Keeping
// this interface explicit makes the planner testable and ensures dry-run only
// uses read operations.
type Target interface {
	ListProjects(context.Context) ([]domain.Project, error)
	ListKnowledge(context.Context, string, string, []domain.KnowledgeStatus) ([]domain.KnowledgeEntry, error)
	ListKnowledgeProposals(context.Context, string, string) ([]domain.KnowledgeProposal, error)
	ListKnowledgeLibraries(context.Context) ([]domain.KnowledgeLibrary, error)
	ListSkills(context.Context, string, string, bool) ([]domain.Skill, error)
	CreateProject(context.Context, domain.Project) error
	CreateKnowledge(context.Context, domain.KnowledgeEntry) error
	UpdateKnowledge(context.Context, domain.KnowledgeEntry) error
	DeleteKnowledge(context.Context, string) error
	ImportKnowledgeProposal(context.Context, domain.KnowledgeProposal) error
	CreateSkill(context.Context, domain.Skill) error
	UpdateSkillPackage(context.Context, domain.Skill, int, []domain.SkillFile) (domain.Skill, error)
}

type Options struct {
	SourceDir  string
	ProjectMap map[string]string
}

type Report struct {
	Applied            bool     `json:"applied"`
	SourceFiles        int      `json:"source_files"`
	SourceBytes        int64    `json:"source_bytes"`
	SourceSnapshotHash string   `json:"source_snapshot_sha256"`
	SourceDocuments    int      `json:"source_documents"`
	SourceSkills       int      `json:"source_skills"`
	SourceSkillFiles   int      `json:"source_skill_files"`
	SkillFilesImported int      `json:"skill_files_imported"`
	SourceAssets       int      `json:"source_assets"`
	UniqueAssets       int      `json:"unique_assets"`
	AssetsEmbedded     int      `json:"assets_embedded"`
	AssetBytesEmbedded int64    `json:"asset_bytes_embedded"`
	DuplicateAssets    int      `json:"duplicate_assets_collapsed"`
	MatchedProjects    int      `json:"matched_projects"`
	ArchiveProjects    int      `json:"archive_projects"`
	KnowledgeLibraries int      `json:"knowledge_libraries"`
	KnowledgeCreate    int      `json:"knowledge_create"`
	KnowledgeUpdate    int      `json:"knowledge_update"`
	KnowledgeDelete    int      `json:"knowledge_delete"`
	KnowledgeUnchanged int      `json:"knowledge_unchanged"`
	WorklogsSkipped    int      `json:"worklogs_skipped"`
	KnowledgePending   int      `json:"knowledge_pending"`
	ProposalCreate     int      `json:"proposal_create"`
	ProposalUpdate     int      `json:"proposal_update"`
	ProposalUnchanged  int      `json:"proposal_unchanged"`
	SkillCreate        int      `json:"skill_create"`
	SkillUpdate        int      `json:"skill_update"`
	SkillUnchanged     int      `json:"skill_unchanged"`
	Redactions         int      `json:"redactions"`
	HeadingsRemoved    int      `json:"duplicate_headings_removed"`
	LinksRewritten     int      `json:"links_rewritten"`
	MissingLocalLinks  int      `json:"missing_local_links"`
	WikiLinks          int      `json:"wiki_links"`
	WikiLinksAnnotated int      `json:"wiki_links_annotated"`
	AbsoluteLocalLinks int      `json:"absolute_local_links"`
	UnresolvedAssets   []string `json:"unresolved_assets,omitempty"`
	LinkIssues         []string `json:"link_issues,omitempty"`
	RedactedFiles      []string `json:"redacted_files,omitempty"`
	Skipped            []string `json:"skipped,omitempty"`
}

type Plan struct {
	Report    Report
	projects  []domain.Project
	knowledge []plannedKnowledge
	deletions []domain.KnowledgeEntry
	proposals []plannedProposal
	skills    []plannedSkill
}

type plannedKnowledge struct {
	Action string
	Entry  domain.KnowledgeEntry
}

type plannedProposal struct {
	Action   string
	Proposal domain.KnowledgeProposal
}

type plannedSkill struct {
	Action string
	Skill  domain.Skill
	Files  []domain.SkillFile
}

type legacyProject struct {
	Key        string
	Name       string
	Identity   string
	TargetID   string
	TargetRoot string
}

type legacyProjectJSON struct {
	DisplayName   string   `json:"display_name"`
	GitIdentities []string `json:"git_identities"`
	Bindings      []struct {
		Remote string `json:"remote"`
	} `json:"bindings"`
}

type pendingFile struct {
	ID         string         `json:"id"`
	Scope      string         `json:"scope"`
	ProjectKey string         `json:"project_key"`
	Title      string         `json:"title"`
	Body       string         `json:"body"`
	Status     string         `json:"status"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
	Meta       map[string]any `json:"meta"`
}

type knowledgeAsset struct {
	Absolute string
	Relative string
	Hash     string
	MIME     string
	Data     []byte
}

var (
	frontmatterPattern = regexp.MustCompile(`(?s)^---\s*\r?\n(.*?)\r?\n---\s*(?:\r?\n|$)`)
	headingPattern     = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)
	credentialLine     = regexp.MustCompile(`(?im)^(\s*(?:password|passwd|passphrase|密码|口令|账号|用户名|username|token|api[_-]?key|access[_-]?token|refresh[_-]?token|secret|authorization|cookie)\s*[:=：]\s*).*$`)
	querySecret        = regexp.MustCompile(`(?i)([?&](?:token|access_token|refresh_token|api_key|key)=)[^&#\s)]+`)
	bearerSecret       = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)
	prefixedSecret     = regexp.MustCompile(`(?i)\b(?:sk-[A-Za-z0-9_-]{12,}|ghp_[A-Za-z0-9]{12,}|glpat-[A-Za-z0-9_-]{12,}|AKIA[A-Z0-9]{12,})\b`)
	privateKey         = regexp.MustCompile(`(?s)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?-----END [^-\r\n]*PRIVATE KEY-----`)
	markdownAsset      = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+\.(?:png|svg|jpe?g|gif|webp))(?:\s+["'][^"']*["'])?\)`)
	markdownLink       = regexp.MustCompile(`!?\[[^\]]*\]\(([^)\s]+)`)
	markdownTarget     = regexp.MustCompile(`(!?\[[^\]]*\]\()([^)\s]+)([^)]*\))`)
	wikiLink           = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	windowsLocalPath   = regexp.MustCompile(`(?i)^[a-z]:[\\/]`)
	unsafeSVG          = regexp.MustCompile(`(?i)<(?:script|foreignObject|iframe|object|embed)\b|on[a-z]+\s*=|(?:href|xlink:href)\s*=\s*["']?\s*(?:javascript:|https?:|data:)`)
)

const personalProjectKey = "__aha1_personal_archive__"

func Analyze(ctx context.Context, target Target, options Options) (*Plan, error) {
	source, err := filepath.Abs(strings.TrimSpace(options.SourceDir))
	if err != nil || source == "" {
		return nil, fmt.Errorf("resolve AHA1 knowledge source: %w", err)
	}
	if info, statErr := os.Stat(source); statErr != nil || !info.IsDir() {
		return nil, fmt.Errorf("AHA1 knowledge source is not a directory: %s", source)
	}
	if _, err := os.Stat(filepath.Join(source, "aha-knowledge.json")); err != nil {
		return nil, fmt.Errorf("AHA1 knowledge registry is missing: %w", err)
	}
	sourceFiles, sourceBytes, sourceHash, err := sourceSnapshot(source)
	if err != nil {
		return nil, err
	}
	projects, err := target.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Report: Report{SourceFiles: sourceFiles, SourceBytes: sourceBytes, SourceSnapshotHash: sourceHash}}
	legacyProjects, err := resolveProjects(source, projects, options.ProjectMap, plan)
	if err != nil {
		return nil, err
	}
	libraries, err := target.ListKnowledgeLibraries(ctx)
	if err != nil {
		return nil, err
	}
	plan.Report.KnowledgeLibraries = len(libraries)
	for _, project := range plan.projects {
		if project.ProjectType == "knowledge" {
			plan.Report.KnowledgeLibraries++
		}
	}
	existingKnowledge, existingProposals, err := readExistingKnowledge(ctx, target, projects, plan.projects)
	if err != nil {
		return nil, err
	}
	existingSkills, err := target.ListSkills(ctx, "", "", false)
	if err != nil {
		return nil, err
	}
	assets, err := scanKnowledgeAssets(source)
	if err != nil {
		return nil, err
	}
	plan.Report.SourceAssets = len(assets)
	uniqueAssetHashes := map[string]bool{}
	for _, asset := range assets {
		uniqueAssetHashes[asset.Hash] = true
	}
	plan.Report.UniqueAssets = len(uniqueAssetHashes)
	plan.Report.DuplicateAssets = len(assets) - len(uniqueAssetHashes)
	if err := planDocuments(source, legacyProjects, existingKnowledge, existingProposals, assets, plan); err != nil {
		return nil, err
	}
	if err := planPending(source, legacyProjects, existingProposals, plan); err != nil {
		return nil, err
	}
	if err := planSkills(source, existingSkills, plan); err != nil {
		return nil, err
	}
	sort.Strings(plan.Report.UnresolvedAssets)
	plan.Report.UnresolvedAssets = uniqueStrings(plan.Report.UnresolvedAssets)
	sort.Strings(plan.Report.LinkIssues)
	plan.Report.LinkIssues = uniqueStrings(plan.Report.LinkIssues)
	sort.Strings(plan.Report.RedactedFiles)
	plan.Report.RedactedFiles = uniqueStrings(plan.Report.RedactedFiles)
	sort.Strings(plan.Report.Skipped)
	plan.Report.Skipped = uniqueStrings(plan.Report.Skipped)
	return plan, nil
}

func Execute(ctx context.Context, target Target, plan *Plan) (Report, error) {
	if plan == nil {
		return Report{}, fmt.Errorf("AHA1 knowledge import plan is required")
	}
	report := plan.Report
	for _, project := range plan.projects {
		if err := target.CreateProject(ctx, project); err != nil {
			return report, fmt.Errorf("create archive project %s: %w", project.Name, err)
		}
	}
	for _, item := range plan.knowledge {
		switch item.Action {
		case "create":
			if err := target.CreateKnowledge(ctx, item.Entry); err != nil {
				return report, fmt.Errorf("create knowledge %s: %w", item.Entry.Title, err)
			}
		case "update":
			if err := target.UpdateKnowledge(ctx, item.Entry); err != nil {
				return report, fmt.Errorf("update knowledge %s: %w", item.Entry.Title, err)
			}
		}
	}
	for _, entry := range plan.deletions {
		if err := target.DeleteKnowledge(ctx, entry.ID); err != nil {
			return report, fmt.Errorf("delete imported worklog %s: %w", entry.Title, err)
		}
	}
	for _, item := range plan.proposals {
		if item.Action == "unchanged" {
			continue
		}
		if err := target.ImportKnowledgeProposal(ctx, item.Proposal); err != nil {
			return report, fmt.Errorf("import knowledge proposal %s: %w", item.Proposal.Proposed.Title, err)
		}
	}
	for _, item := range plan.skills {
		switch item.Action {
		case "create":
			if err := target.CreateSkill(ctx, item.Skill); err != nil {
				return report, fmt.Errorf("create skill %s: %w", item.Skill.Name, err)
			}
			created := item.Skill
			if _, err := target.UpdateSkillPackage(ctx, created, created.Version, item.Files); err != nil {
				return report, fmt.Errorf("write skill package %s: %w", item.Skill.Name, err)
			}
		case "update":
			if _, err := target.UpdateSkillPackage(ctx, item.Skill, item.Skill.Version, item.Files); err != nil {
				return report, fmt.Errorf("update skill package %s: %w", item.Skill.Name, err)
			}
		}
	}
	report.Applied = true
	return report, nil
}

func resolveProjects(source string, existing []domain.Project, explicit map[string]string, plan *Plan) (map[string]legacyProject, error) {
	byID := map[string]domain.Project{}
	byIdentity := map[string][]domain.Project{}
	for _, project := range existing {
		byID[project.ID] = project
		identity := normalizeRepository(project.RepositoryIdentity)
		if identity != "" {
			byIdentity[identity] = append(byIdentity[identity], project)
		}
	}
	root := filepath.Join(source, "projects")
	directories, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]legacyProject{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := map[string]legacyProject{}
	now := time.Now().UTC()
	for _, directory := range directories {
		if !directory.IsDir() {
			continue
		}
		key := directory.Name()
		meta := legacyProjectJSON{}
		if data, readErr := os.ReadFile(filepath.Join(root, key, "project.json")); readErr == nil {
			_ = json.Unmarshal(data, &meta)
		}
		name := strings.TrimSpace(meta.DisplayName)
		if name == "" {
			name = key
		}
		identity := ""
		for _, binding := range meta.Bindings {
			if identity = normalizeRepository(binding.Remote); identity != "" {
				break
			}
		}
		if identity == "" && len(meta.GitIdentities) > 0 {
			identity = normalizeRepository(meta.GitIdentities[0])
		}
		targetID := strings.TrimSpace(explicit[key])
		if targetID != "" {
			if _, ok := byID[targetID]; !ok {
				return nil, fmt.Errorf("project map %s references unknown AHA2 project %s", key, targetID)
			}
			plan.Report.MatchedProjects++
		} else if matches := byIdentity[identity]; identity != "" && len(matches) == 1 {
			targetID = matches[0].ID
			plan.Report.MatchedProjects++
		} else if identity != "" && len(byIdentity[identity]) > 1 {
			return nil, fmt.Errorf("legacy project %s matches multiple AHA2 projects; use --project-map", key)
		} else {
			targetID = stableID("project_aha1", key)
			if _, ok := byID[targetID]; ok {
				plan.Report.MatchedProjects++
			} else {
				project := domain.Project{
					ID: targetID, Name: name + " (AHA1 archive)",
					Description: "Imported AHA1 knowledge archive for legacy project " + key + ".",
					ProjectType: "knowledge", RepositoryIdentity: identity,
					KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
				}
				plan.projects = append(plan.projects, project)
				byID[targetID] = project
				plan.Report.ArchiveProjects++
			}
		}
		result[key] = legacyProject{Key: key, Name: name, Identity: identity, TargetID: targetID, TargetRoot: knowledgeRootID("project", targetID)}
	}
	if hasLegacyPersonalData(source) {
		personalID := stableID("project_aha1", "personal-archive")
		if _, ok := byID[personalID]; ok {
			plan.Report.MatchedProjects++
		} else {
			project := domain.Project{
				ID: personalID, Name: "AHA1 Personal Archive",
				Description: "Isolated AHA1 personal knowledge, captures, and pending notes.",
				ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
			}
			plan.projects = append(plan.projects, project)
			byID[personalID] = project
			plan.Report.ArchiveProjects++
		}
		result[personalProjectKey] = legacyProject{
			Key: personalProjectKey, Name: "AHA1 Personal Archive", TargetID: personalID,
			TargetRoot: knowledgeRootID("project", personalID),
		}
	}
	sort.Slice(plan.projects, func(i, j int) bool { return plan.projects[i].ID < plan.projects[j].ID })
	return result, nil
}

func hasLegacyPersonalData(source string) bool {
	for _, directory := range []string{"personal", "capture"} {
		if info, err := os.Stat(filepath.Join(source, directory)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func readExistingKnowledge(ctx context.Context, target Target, existingProjects, plannedProjects []domain.Project) ([]domain.KnowledgeEntry, []domain.KnowledgeProposal, error) {
	entries, err := target.ListKnowledge(ctx, "global", "", nil)
	if err != nil {
		return nil, nil, err
	}
	proposals, err := target.ListKnowledgeProposals(ctx, "global", "")
	if err != nil {
		return nil, nil, err
	}
	for _, project := range append(append([]domain.Project{}, existingProjects...), plannedProjects...) {
		items, listErr := target.ListKnowledge(ctx, "project", project.ID, nil)
		if listErr != nil {
			return nil, nil, listErr
		}
		entries = append(entries, items...)
		itemsProposals, listErr := target.ListKnowledgeProposals(ctx, "project", project.ID)
		if listErr != nil {
			return nil, nil, listErr
		}
		proposals = append(proposals, itemsProposals...)
	}
	return entries, uniqueProposals(proposals), nil
}

func planDocuments(source string, projects map[string]legacyProject, existing []domain.KnowledgeEntry, proposals []domain.KnowledgeProposal, assets []knowledgeAsset, plan *Plan) error {
	existingByID := map[string]domain.KnowledgeEntry{}
	existingByKey := map[string]domain.KnowledgeEntry{}
	for _, entry := range existing {
		existingByID[entry.ID] = entry
		existingByKey[knowledgeKey(entry.Scope, entry.ProjectID, entry.ParentID, entry.Slug)] = entry
	}
	pendingByKey := map[string]domain.KnowledgeProposal{}
	for _, proposal := range proposals {
		if proposal.Status == domain.KnowledgeProposalPending {
			entry := proposal.Proposed
			pendingByKey[knowledgeKey(entry.Scope, entry.ProjectID, entry.ParentID, entry.Slug)] = proposal
		}
	}
	assetsByPath := map[string]knowledgeAsset{}
	for _, asset := range assets {
		assetsByPath[filepath.Clean(asset.Absolute)] = asset
	}
	referencedAssets := map[string]bool{}
	type sourceDoc struct {
		absolute, relative, scope, projectID, projectKey string
	}
	documents := []sourceDoc{}
	for _, top := range []string{"general", "personal", "capture"} {
		root := filepath.Join(source, top)
		_ = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
				return walkErr
			}
			relative, _ := filepath.Rel(source, filePath)
			document := sourceDoc{absolute: filePath, relative: filepath.ToSlash(relative), scope: "global"}
			if top != "general" {
				document.scope = "project"
				document.projectID = projects[personalProjectKey].TargetID
				document.projectKey = personalProjectKey
			}
			documents = append(documents, document)
			return nil
		})
	}
	for key, project := range projects {
		if key == personalProjectKey {
			continue
		}
		root := filepath.Join(source, "projects", key)
		_ = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
				return walkErr
			}
			relative, _ := filepath.Rel(root, filePath)
			documents = append(documents, sourceDoc{absolute: filePath, relative: filepath.ToSlash(relative), scope: "project", projectID: project.TargetID, projectKey: key})
			return nil
		})
	}
	sort.Slice(documents, func(i, j int) bool {
		leftDepth, rightDepth := strings.Count(documents[i].relative, "/"), strings.Count(documents[j].relative, "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return documents[i].absolute < documents[j].absolute
	})
	documentPaths := map[string]string{}
	for _, item := range documents {
		documentPaths[filepath.Clean(item.absolute)] = logicalDocumentPath(item.relative)
	}
	plannedByID := map[string]domain.KnowledgeEntry{}
	worklogDeletionIDs := map[string]bool{}
	for _, item := range documents {
		plan.Report.SourceDocuments++
		if isWorklogPath(item.relative) {
			plan.Report.WorklogsSkipped++
			worklogDeletionIDs[stableID("knowledge_aha1", item.scope, item.projectKey, item.relative)] = true
			for _, directory := range directoryPrefixes(path.Dir(item.relative)) {
				worklogDeletionIDs[stableID("knowledge_aha1_dir", item.scope, item.projectKey, directory)] = true
			}
			continue
		}
		raw, err := os.ReadFile(item.absolute)
		if err != nil {
			return err
		}
		if !utf8.Valid(raw) {
			return fmt.Errorf("AHA1 knowledge is not valid UTF-8: %s", item.relative)
		}
		meta, body, err := parseFrontmatter(string(raw))
		if err != nil {
			return fmt.Errorf("parse frontmatter %s: %w", item.relative, err)
		}
		sourcePath := item.relative
		if item.projectKey != "" && item.projectKey != personalProjectKey {
			sourcePath = "projects/" + item.projectKey + "/" + item.relative
		}
		body, redactions := sanitizeBody(body)
		plan.Report.Redactions += redactions
		if redactions > 0 {
			plan.Report.RedactedFiles = append(plan.Report.RedactedFiles, sourcePath)
		}
		auditLinks(item.absolute, sourcePath, body, documentPaths, plan)
		body, annotatedWikiLinks := annotateWikiLinks(body)
		plan.Report.WikiLinksAnnotated += annotatedWikiLinks
		body, rewritten := rewriteKnowledgeLinks(item.absolute, body, documentPaths)
		plan.Report.LinksRewritten += rewritten
		body, embedded, err := embedKnowledgeAssets(source, item.absolute, body, assetsByPath)
		if err != nil {
			return fmt.Errorf("embed knowledge assets for %s: %w", sourcePath, err)
		}
		for _, asset := range embedded {
			referencedAssets[asset.Hash] = true
		}
		for _, match := range markdownAsset.FindAllStringSubmatch(markdownOutsideCode(body), -1) {
			if len(match) > 1 && !strings.Contains(match[1], "://") {
				plan.Report.UnresolvedAssets = append(plan.Report.UnresolvedAssets, sourcePath+" -> "+match[1])
			}
		}
		title := metadataText(meta, "title")
		if title == "" {
			title = firstHeading(body)
		}
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(item.relative), filepath.Ext(item.relative))
		}
		body, headingRemoved := stripDuplicateHeading(body, title)
		if headingRemoved {
			plan.Report.HeadingsRemoved++
		}
		directory := path.Dir(item.relative)
		folderKind := ""
		if directory == "." {
			directory = ""
		}
		if item.scope == "project" && strings.HasPrefix(item.relative, "navigation/") {
			directory = strings.TrimPrefix(directory, "navigation")
			directory = strings.TrimPrefix(directory, "/")
			folderKind = "navigation"
		}
		parentID := ensureFolders(item.scope, item.projectID, item.projectKey, directory, folderKind, plannedByID, existingByID, existingByKey, pendingByKey, plan)
		base := strings.TrimSuffix(path.Base(item.relative), path.Ext(item.relative))
		slug := knowledgeSlug(base)
		if strings.EqualFold(base, "index") {
			slug = "overview"
		}
		deterministicID := stableID("knowledge_aha1", item.scope, item.projectKey, item.relative)
		entryID := deterministicID
		key := knowledgeKey(item.scope, item.projectID, parentID, slug)
		for {
			current, occupied := existingByKey[key]
			pending, proposed := pendingByKey[key]
			currentSameSource := occupied && (evidenceSourcePath(current.EvidenceJSON) == sourcePath || evidenceSourcePath(current.EvidenceJSON) == "" && current.Title == title)
			pendingSameSource := proposed && (evidenceSourcePath(pending.Proposed.EvidenceJSON) == sourcePath || evidenceSourcePath(pending.Proposed.EvidenceJSON) == "" && pending.Proposed.Title == title)
			if (occupied && current.ID != deterministicID && !currentSameSource) || (proposed && pending.Proposed.ID != deterministicID && !pendingSameSource) {
				slug = disambiguatedSlug(slug, deterministicID)
				key = knowledgeKey(item.scope, item.projectID, parentID, slug)
				continue
			}
			break
		}
		if current, ok := existingByKey[key]; ok && (current.ID == deterministicID || evidenceSourcePath(current.EvidenceJSON) == sourcePath || (evidenceSourcePath(current.EvidenceJSON) == "" && current.Title == title)) {
			entryID = current.ID
		}
		kind := metadataText(meta, "type")
		if kind == "" {
			kind = "practice"
		}
		status := domain.KnowledgeVerified
		if strings.HasPrefix(item.relative, "capture/") || strings.Contains(item.relative, "/worklog/") || strings.HasPrefix(item.relative, "worklog/") || kind == "task_worklog" {
			status = domain.KnowledgeCandidate
		}
		createdAt := metadataTime(meta, "created_at")
		updatedAt := metadataTime(meta, "updated_at")
		if createdAt.IsZero() {
			createdAt = fileTime(item.absolute)
		}
		if updatedAt.IsZero() {
			updatedAt = createdAt
		}
		confidence := metadataFloat(meta, "confidence", 0.8)
		evidence, _ := json.Marshal(map[string]any{
			"importer": "aha1-knowledge-v1", "source_path": sourcePath, "legacy_project_key": item.projectKey,
			"legacy_id": metadataText(meta, "id"), "source_sha256": hashBytes(raw), "redactions": redactions,
		})
		entry := domain.KnowledgeEntry{
			ID: entryID, Scope: item.scope, ProjectID: item.projectID, ParentID: parentID, Slug: slug,
			SortOrder: 100, IsIndex: false, Type: kind, Title: title, Body: strings.TrimSpace(body), Status: status,
			Confidence: confidence, Revision: 1, EvidenceJSON: string(evidence), CreatedAt: createdAt, UpdatedAt: updatedAt,
		}
		entry.ContentHash = knowledgeContentHash(entry.Title, entry.Body)
		if status == domain.KnowledgeVerified {
			entry.LastVerifiedAt = updatedAt
		}
		if _, ok := pendingByKey[key]; ok {
			plan.Report.KnowledgePending++
			plan.Report.Skipped = append(plan.Report.Skipped, item.relative+": target has a pending proposal with the same path")
			continue
		}
		action, normalized := knowledgeAction(entry, existingByID)
		plan.knowledge = append(plan.knowledge, plannedKnowledge{Action: action, Entry: normalized})
		plannedByID[normalized.ID] = normalized
		existingByID[normalized.ID] = normalized
		existingByKey[key] = normalized
		incrementKnowledgeAction(plan, action)
	}
	for id := range worklogDeletionIDs {
		if entry, ok := existingByID[id]; ok && (entry.Type == "task_worklog" || strings.HasPrefix(entry.Body, "AHA1 导入目录：`worklog")) {
			plan.deletions = append(plan.deletions, entry)
		}
	}
	sort.Slice(plan.deletions, func(i, j int) bool {
		left, right := entryDepth(plan.deletions[i], existingByID), entryDepth(plan.deletions[j], existingByID)
		if left != right {
			return left > right
		}
		return plan.deletions[i].ID < plan.deletions[j].ID
	})
	plan.Report.KnowledgeDelete = len(plan.deletions)
	if err := planUnreferencedAssets(projects, assets, referencedAssets, plannedByID, existingByID, existingByKey, pendingByKey, plan); err != nil {
		return err
	}
	embeddedHashes := map[string]bool{}
	for hash := range referencedAssets {
		embeddedHashes[hash] = true
	}
	for _, item := range plan.knowledge {
		if strings.HasPrefix(item.Entry.ID, "knowledge_aha1_asset_") {
			var evidence struct {
				SourceSHA256 string `json:"source_sha256"`
			}
			_ = json.Unmarshal([]byte(item.Entry.EvidenceJSON), &evidence)
			if evidence.SourceSHA256 != "" {
				embeddedHashes[evidence.SourceSHA256] = true
			}
		}
	}
	plan.Report.AssetsEmbedded = len(embeddedHashes)
	for hash := range embeddedHashes {
		for _, asset := range assets {
			if asset.Hash == hash {
				plan.Report.AssetBytesEmbedded += int64(len(asset.Data))
				break
			}
		}
	}
	sort.SliceStable(plan.knowledge, func(i, j int) bool {
		leftDepth := entryDepth(plan.knowledge[i].Entry, plannedByID)
		rightDepth := entryDepth(plan.knowledge[j].Entry, plannedByID)
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return plan.knowledge[i].Entry.ID < plan.knowledge[j].Entry.ID
	})
	return nil
}

func ensureFolders(scope, projectID, projectKey, directory, folderKind string, planned, existingByID map[string]domain.KnowledgeEntry, existingByKey map[string]domain.KnowledgeEntry, pendingByKey map[string]domain.KnowledgeProposal, plan *Plan) string {
	parentID := knowledgeRootID(scope, projectID)
	if directory == "" {
		return parentID
	}
	current := ""
	for _, segment := range strings.Split(directory, "/") {
		if segment == "" || segment == "." {
			continue
		}
		if current == "" {
			current = segment
		} else {
			current += "/" + segment
		}
		id := stableID("knowledge_aha1_dir", scope, projectKey, current)
		slug := knowledgeSlug(segment)
		key := knowledgeKey(scope, projectID, parentID, slug)
		if existing, ok := existingByKey[key]; ok {
			id = existing.ID
		}
		if existing, ok := existingByID[id]; ok {
			parentID = existing.ID
			continue
		}
		if proposal, ok := pendingByKey[key]; ok {
			parentID = proposal.Proposed.ID
			continue
		}
		kind := folderKind
		if kind == "" {
			kind = "practice"
		}
		if scope == "global" || strings.HasPrefix(current, "navigation") {
			kind = "navigation"
		}
		now := time.Now().UTC()
		entry := domain.KnowledgeEntry{
			ID: id, Scope: scope, ProjectID: projectID, ParentID: parentID, Slug: slug, SortOrder: 10,
			Type: kind, Title: folderTitle(segment), Body: "AHA1 导入目录：`" + current + "`。", Status: domain.KnowledgeVerified,
			Confidence: 1, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
		}
		entry.ContentHash = knowledgeContentHash(entry.Title, entry.Body)
		action, normalized := knowledgeAction(entry, existingByID)
		plan.knowledge = append(plan.knowledge, plannedKnowledge{Action: action, Entry: normalized})
		planned[id] = normalized
		existingByID[id] = normalized
		existingByKey[key] = normalized
		incrementKnowledgeAction(plan, action)
		parentID = id
	}
	return parentID
}

func planPending(source string, projects map[string]legacyProject, existing []domain.KnowledgeProposal, plan *Plan) error {
	root := filepath.Join(source, ".pending")
	files, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	byID := map[string]domain.KnowledgeProposal{}
	for _, proposal := range existing {
		byID[proposal.ID] = proposal
	}
	for _, file := range files {
		if file.IsDir() || strings.ToLower(filepath.Ext(file.Name())) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, file.Name()))
		if err != nil {
			return err
		}
		var legacy pendingFile
		if err := json.Unmarshal(data, &legacy); err != nil {
			return fmt.Errorf("decode pending candidate %s: %w", file.Name(), err)
		}
		body, redactions := sanitizeBody(legacy.Body)
		plan.Report.Redactions += redactions
		if redactions > 0 {
			plan.Report.RedactedFiles = append(plan.Report.RedactedFiles, ".pending/"+file.Name())
		}
		body, headingRemoved := stripDuplicateHeading(body, legacy.Title)
		if headingRemoved {
			plan.Report.HeadingsRemoved++
		}
		scope, projectID := "global", ""
		if legacy.Scope == "personal" {
			scope, projectID = "project", projects[personalProjectKey].TargetID
		} else if legacy.Scope == "project" {
			project, ok := projects[legacy.ProjectKey]
			if !ok {
				plan.Report.Skipped = append(plan.Report.Skipped, ".pending/"+file.Name()+": unknown project key")
				continue
			}
			scope, projectID = "project", project.TargetID
		}
		entryID := stableID("knowledge_aha1_pending", file.Name())
		proposalID := stableID("knowledge_proposal_aha1", file.Name())
		createdAt, _ := time.Parse(time.RFC3339, legacy.CreatedAt)
		updatedAt, _ := time.Parse(time.RFC3339, legacy.UpdatedAt)
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		if updatedAt.IsZero() {
			updatedAt = createdAt
		}
		evidence, _ := json.Marshal(map[string]any{"importer": "aha1-knowledge-v1", "source_path": ".pending/" + file.Name(), "legacy_id": legacy.ID, "source_sha256": hashBytes(data), "redactions": redactions})
		entry := domain.KnowledgeEntry{
			ID: entryID, Scope: scope, ProjectID: projectID, ParentID: knowledgeRootID(scope, projectID),
			Slug: knowledgeSlug(strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))), SortOrder: 900,
			Type: "practice", Title: strings.TrimSpace(legacy.Title), Body: strings.TrimSpace(body), Status: domain.KnowledgeCandidate,
			Confidence: metadataFloat(legacy.Meta, "confidence", 0.8), Revision: 1, EvidenceJSON: string(evidence), CreatedAt: createdAt, UpdatedAt: updatedAt,
		}
		entry.ContentHash = knowledgeContentHash(entry.Title, entry.Body)
		proposal := domain.KnowledgeProposal{ID: proposalID, EntryID: entryID, BaseRevision: 0, Proposed: entry, Status: domain.KnowledgeProposalPending, CreatedAt: createdAt, UpdatedAt: updatedAt}
		action := "create"
		if current, ok := byID[proposalID]; ok {
			action = "update"
			if proposalEqual(current, proposal) {
				action = "unchanged"
			}
		}
		plan.proposals = append(plan.proposals, plannedProposal{Action: action, Proposal: proposal})
		switch action {
		case "create":
			plan.Report.ProposalCreate++
		case "update":
			plan.Report.ProposalUpdate++
		default:
			plan.Report.ProposalUnchanged++
		}
	}
	return nil
}

func planSkills(source string, existing []domain.Skill, plan *Plan) error {
	root := filepath.Join(source, "skills")
	directories, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	byID := map[string]domain.Skill{}
	bySlug := map[string]domain.Skill{}
	for _, skill := range existing {
		byID[skill.ID], bySlug[skill.PackageSlug] = skill, skill
	}
	for _, directory := range directories {
		if !directory.IsDir() {
			continue
		}
		packageRoot := filepath.Join(root, directory.Name())
		entryPath := filepath.Join(packageRoot, "SKILL.md")
		entryData, err := os.ReadFile(entryPath)
		if err != nil {
			continue
		}
		meta, instructions, err := parseFrontmatter(string(entryData))
		if err != nil {
			return fmt.Errorf("parse skill %s: %w", directory.Name(), err)
		}
		name := metadataText(meta, "name")
		if name == "" {
			name = directory.Name()
		}
		description := metadataText(meta, "description")
		files := []domain.SkillFile{}
		err = filepath.WalkDir(packageRoot, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			plan.Report.SourceSkillFiles++
			relative, _ := filepath.Rel(packageRoot, filePath)
			relative = filepath.ToSlash(relative)
			data, readErr := os.ReadFile(filePath)
			if readErr != nil {
				return readErr
			}
			if strings.EqualFold(filepath.Ext(relative), ".pyc") || !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
				plan.Report.Skipped = append(plan.Report.Skipped, "skills/"+directory.Name()+"/"+relative+": binary or generated file")
				return nil
			}
			content, redactions := sanitizeBody(string(data))
			plan.Report.Redactions += redactions
			if redactions > 0 {
				plan.Report.RedactedFiles = append(plan.Report.RedactedFiles, "skills/"+directory.Name()+"/"+relative)
			}
			if relative == "SKILL.md" {
				_, sanitizedInstructions, parseErr := parseFrontmatter(content)
				if parseErr != nil {
					return parseErr
				}
				instructions = sanitizedInstructions
			}
			if strings.EqualFold(filepath.Ext(relative), ".md") {
				auditLinks(filePath, "skills/"+directory.Name()+"/"+relative, content, nil, plan)
			}
			files = append(files, domain.SkillFile{Path: relative, Content: content})
			return nil
		})
		if err != nil {
			return err
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		id := stableID("skill_aha1", directory.Name())
		current, exists := byID[id]
		if byName, ok := bySlug[directory.Name()]; ok {
			current, exists, id = byName, true, byName.ID
		}
		now := time.Now().UTC()
		skill := domain.Skill{ID: id, PackageSlug: directory.Name(), Scope: "global", Name: name, Description: description, Instructions: strings.TrimSpace(instructions), Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
		action := "create"
		if exists {
			skill.Version, skill.CreatedAt = current.Version, current.CreatedAt
			action = "update"
			if skillMetadataEqual(current, skill) && skillFilesEqual(current.PackageFiles, files) {
				action = "unchanged"
			}
		}
		plan.skills = append(plan.skills, plannedSkill{Action: action, Skill: skill, Files: files})
		plan.Report.SourceSkills++
		plan.Report.SkillFilesImported += len(files)
		switch action {
		case "create":
			plan.Report.SkillCreate++
		case "update":
			plan.Report.SkillUpdate++
		default:
			plan.Report.SkillUnchanged++
		}
	}
	return nil
}

func knowledgeAction(entry domain.KnowledgeEntry, existing map[string]domain.KnowledgeEntry) (string, domain.KnowledgeEntry) {
	current, ok := existing[entry.ID]
	if !ok {
		return "create", entry
	}
	if knowledgeEqual(current, entry) {
		return "unchanged", current
	}
	entry.CreatedAt = current.CreatedAt
	entry.UpdatedAt = time.Now().UTC()
	entry.Revision = current.Revision + 1
	entry.HelpedCount, entry.StaleCount, entry.FeedbackState = current.HelpedCount, current.StaleCount, current.FeedbackState
	if entry.Status == domain.KnowledgeVerified {
		entry.LastVerifiedAt = entry.UpdatedAt
	}
	return "update", entry
}

func incrementKnowledgeAction(plan *Plan, action string) {
	switch action {
	case "create":
		plan.Report.KnowledgeCreate++
	case "update":
		plan.Report.KnowledgeUpdate++
	default:
		plan.Report.KnowledgeUnchanged++
	}
}

func parseFrontmatter(value string) (map[string]any, string, error) {
	match := frontmatterPattern.FindStringSubmatchIndex(value)
	if match == nil {
		return map[string]any{}, value, nil
	}
	frontmatter := strings.TrimSpace(value[match[2]:match[3]])
	body := value[match[1]:]
	metadata := map[string]any{}
	if strings.HasPrefix(frontmatter, "{") {
		if err := json.Unmarshal([]byte(frontmatter), &metadata); err != nil {
			return nil, "", err
		}
		return metadata, body, nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(frontmatter, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key, raw := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if key == "" {
			continue
		}
		if len(raw) >= 2 && (raw[0] == '"' && raw[len(raw)-1] == '"' || raw[0] == '\'' && raw[len(raw)-1] == '\'') {
			if raw[0] == '\'' {
				raw = strings.ReplaceAll(raw[1:len(raw)-1], "''", "'")
			} else if unquoted, err := strconv.Unquote(raw); err == nil {
				raw = unquoted
			}
		}
		metadata[key] = raw
	}
	return metadata, body, nil
}

func sanitizeBody(value string) (string, int) {
	result, count := value, 0
	for _, item := range []struct {
		pattern     *regexp.Regexp
		replacement string
	}{
		{privateKey, "[REDACTED PRIVATE KEY]"},
		{credentialLine, "${1}[REDACTED]"},
		{querySecret, "${1}[REDACTED]"},
		{bearerSecret, "${1}[REDACTED]"},
		{prefixedSecret, "[REDACTED TOKEN]"},
	} {
		matches := item.pattern.FindAllStringIndex(result, -1)
		if len(matches) > 0 {
			count += len(matches)
			result = item.pattern.ReplaceAllString(result, item.replacement)
		}
	}
	return result, count
}

func stripDuplicateHeading(body, title string) (string, bool) {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		match := headingPattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) == 2 && strings.EqualFold(strings.TrimSpace(match[1]), strings.TrimSpace(title)) {
			lines = append(lines[:index], lines[index+1:]...)
			return strings.TrimSpace(strings.Join(lines, "\n")), true
		}
		break
	}
	return strings.TrimSpace(normalized), false
}

func auditLinks(absolutePath, relativePath, body string, documents map[string]string, plan *Plan) {
	body = markdownOutsideCode(body)
	for _, match := range wikiLink.FindAllStringSubmatch(body, -1) {
		if len(match) < 2 {
			continue
		}
		plan.Report.WikiLinks++
	}
	for _, match := range markdownLink.FindAllStringSubmatch(body, -1) {
		if len(match) < 2 {
			continue
		}
		destination := strings.Trim(strings.TrimSpace(match[1]), "<>")
		if index := strings.IndexAny(destination, "?#"); index >= 0 {
			destination = destination[:index]
		}
		if destination == "" || strings.Contains(destination, "://") || strings.HasPrefix(destination, "mailto:") || strings.HasPrefix(destination, "tel:") {
			continue
		}
		if strings.HasPrefix(destination, "/") || strings.HasPrefix(destination, "~/") || windowsLocalPath.MatchString(destination) {
			plan.Report.AbsoluteLocalLinks++
			plan.Report.LinkIssues = append(plan.Report.LinkIssues, relativePath+" -> "+destination+" (absolute local path)")
			continue
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(absolutePath), filepath.FromSlash(destination)))
		if _, err := os.Stat(resolved); err == nil {
			continue
		}
		if _, ok := resolveKnowledgeDocument(absolutePath, destination, documents); !ok {
			plan.Report.MissingLocalLinks++
			plan.Report.LinkIssues = append(plan.Report.LinkIssues, relativePath+" -> "+destination+" (missing)")
		}
	}
}

func rewriteKnowledgeLinks(absolutePath, body string, documents map[string]string) (string, int) {
	current, ok := documents[filepath.Clean(absolutePath)]
	if !ok {
		return body, 0
	}
	rewritten := 0
	result := transformMarkdownOutsideCode(body, func(segment string) string {
		return markdownTarget.ReplaceAllStringFunc(segment, func(value string) string {
			parts := markdownTarget.FindStringSubmatch(value)
			if len(parts) != 4 {
				return value
			}
			destination := strings.Trim(parts[2], "<>")
			suffix := ""
			if index := strings.IndexAny(destination, "?#"); index >= 0 {
				suffix, destination = destination[index:], destination[:index]
			}
			if destination == "" || strings.Contains(destination, "://") || strings.HasPrefix(destination, "/") || strings.HasPrefix(destination, "~/") || windowsLocalPath.MatchString(destination) {
				return value
			}
			target, exists := resolveKnowledgeDocument(absolutePath, destination, documents)
			if !exists {
				return value
			}
			relative, err := filepath.Rel(filepath.FromSlash(path.Dir(current)), filepath.FromSlash(target))
			if err != nil {
				return value
			}
			relative = filepath.ToSlash(relative)
			if strings.HasPrefix(parts[2], "<") && strings.HasSuffix(parts[2], ">") {
				relative = "<" + relative + suffix + ">"
			} else {
				relative += suffix
			}
			rewritten++
			return parts[1] + relative + parts[3]
		})
	})
	return result, rewritten
}

func resolveKnowledgeDocument(absolutePath, destination string, documents map[string]string) (string, bool) {
	if len(documents) == 0 {
		return "", false
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(absolutePath), filepath.FromSlash(destination)))
	if target, ok := documents[resolved]; ok {
		return target, true
	}
	for directory := filepath.Dir(absolutePath); directory != filepath.Dir(directory); directory = filepath.Dir(directory) {
		if strings.EqualFold(filepath.Base(directory), "navigation") {
			fallback := filepath.Clean(filepath.Join(directory, filepath.FromSlash(destination)))
			target, ok := documents[fallback]
			return target, ok
		}
	}
	return "", false
}

func transformMarkdownOutsideCode(value string, transform func(string) string) string {
	return mapMarkdownParts(value, transform, func(code string) string { return code })
}

func annotateWikiLinks(value string) (string, int) {
	count := 0
	result := transformMarkdownOutsideCode(value, func(segment string) string {
		return wikiLink.ReplaceAllStringFunc(segment, func(link string) string {
			match := wikiLink.FindStringSubmatch(link)
			if len(match) != 2 {
				return link
			}
			count++
			label := strings.ReplaceAll(strings.TrimSpace(match[1]), "`", "'")
			return "`" + label + "`（AHA1 WikiLink，原目标不存在）"
		})
	})
	return result, count
}

func markdownOutsideCode(value string) string {
	return mapMarkdownParts(value, func(text string) string { return text }, maskMarkdownCode)
}

func mapMarkdownParts(value string, textTransform, codeTransform func(string) string) string {
	lines := strings.SplitAfter(value, "\n")
	var result strings.Builder
	fenceCharacter := byte(0)
	fenceLength := 0
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		markerCharacter, markerLength := byte(0), 0
		if indent <= 3 && len(trimmed) > 0 && (trimmed[0] == '`' || trimmed[0] == '~') {
			markerCharacter = trimmed[0]
			for markerLength < len(trimmed) && trimmed[markerLength] == markerCharacter {
				markerLength++
			}
			if markerLength < 3 {
				markerCharacter, markerLength = 0, 0
			}
		}
		if fenceCharacter != 0 {
			result.WriteString(codeTransform(line))
			if markerCharacter == fenceCharacter && markerLength >= fenceLength && strings.TrimSpace(trimmed[markerLength:]) == "" {
				fenceCharacter, fenceLength = 0, 0
			}
			continue
		}
		if markerCharacter != 0 {
			fenceCharacter, fenceLength = markerCharacter, markerLength
			result.WriteString(codeTransform(line))
			continue
		}
		result.WriteString(mapInlineCode(line, textTransform, codeTransform))
	}
	return result.String()
}

func mapInlineCode(line string, textTransform, codeTransform func(string) string) string {
	var result strings.Builder
	for index := 0; index < len(line); {
		next := strings.IndexByte(line[index:], '`')
		if next < 0 {
			result.WriteString(textTransform(line[index:]))
			break
		}
		next += index
		result.WriteString(textTransform(line[index:next]))
		run := 1
		for next+run < len(line) && line[next+run] == '`' {
			run++
		}
		marker := strings.Repeat("`", run)
		end := strings.Index(line[next+run:], marker)
		if end < 0 {
			result.WriteString(codeTransform(line[next:]))
			break
		}
		end += next + run + run
		result.WriteString(codeTransform(line[next:end]))
		index = end
	}
	return result.String()
}

func maskMarkdownCode(value string) string {
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' {
			return character
		}
		return ' '
	}, value)
}

func embedKnowledgeAssets(source, absolutePath, body string, assets map[string]knowledgeAsset) (string, []knowledgeAsset, error) {
	embedded := []knowledgeAsset{}
	result := transformMarkdownOutsideCode(body, func(segment string) string {
		return markdownTarget.ReplaceAllStringFunc(segment, func(value string) string {
			parts := markdownTarget.FindStringSubmatch(value)
			if len(parts) != 4 || !strings.HasPrefix(parts[1], "![") {
				return value
			}
			destination := strings.Trim(parts[2], "<>")
			if index := strings.IndexAny(destination, "?#"); index >= 0 {
				destination = destination[:index]
			}
			if destination == "" || strings.Contains(destination, "://") || strings.HasPrefix(destination, "/") || strings.HasPrefix(destination, "~/") || windowsLocalPath.MatchString(destination) {
				return value
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(absolutePath), filepath.FromSlash(destination)))
			relative, err := filepath.Rel(source, resolved)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				return value
			}
			asset, ok := assets[resolved]
			if !ok || asset.MIME == "application/octet-stream" || len(asset.Data) > 2<<20 {
				return value
			}
			embedded = append(embedded, asset)
			dataURL := "data:" + asset.MIME + ";base64," + base64.StdEncoding.EncodeToString(asset.Data)
			return parts[1] + dataURL + parts[3]
		})
	})
	return result, embedded, nil
}

func planUnreferencedAssets(
	projects map[string]legacyProject,
	assets []knowledgeAsset,
	referenced map[string]bool,
	planned, existingByID map[string]domain.KnowledgeEntry,
	existingByKey map[string]domain.KnowledgeEntry,
	pendingByKey map[string]domain.KnowledgeProposal,
	plan *Plan,
) error {
	groups := map[string][]knowledgeAsset{}
	for _, asset := range assets {
		groups[asset.Hash] = append(groups[asset.Hash], asset)
	}
	hashes := make([]string, 0, len(groups))
	for hash := range groups {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	for _, hash := range hashes {
		if referenced[hash] {
			continue
		}
		group := groups[hash]
		representative := group[0]
		scope, projectID, projectKey := "global", "", ""
		segments := strings.Split(representative.Relative, "/")
		if len(segments) >= 3 && segments[0] == "projects" {
			project, ok := projects[segments[1]]
			if !ok {
				plan.Report.Skipped = append(plan.Report.Skipped, representative.Relative+": unknown project for asset")
				continue
			}
			scope, projectID, projectKey = "project", project.TargetID, segments[1]
		}
		parentID := ensureFolders(scope, projectID, projectKey, "assets", "", planned, existingByID, existingByKey, pendingByKey, plan)
		slug := "asset-" + hash[:12]
		id := stableID("knowledge_aha1_asset", scope, projectKey, hash)
		key := knowledgeKey(scope, projectID, parentID, slug)
		if current, ok := existingByKey[key]; ok {
			id = current.ID
		}
		paths := make([]string, 0, len(group))
		for _, asset := range group {
			paths = append(paths, asset.Relative)
		}
		sort.Strings(paths)
		body := "AHA1 未被正文引用的知识资产。原始路径：\n\n- `" + strings.Join(paths, "`\n- `") + "`\n\n"
		body += "![" + filepath.Base(representative.Relative) + "](data:" + representative.MIME + ";base64," + base64.StdEncoding.EncodeToString(representative.Data) + ")"
		now := time.Now().UTC()
		evidence, _ := json.Marshal(map[string]any{
			"importer": "aha1-knowledge-v1", "source_paths": paths, "source_sha256": hash,
			"duplicate_physical_files": len(group),
		})
		entry := domain.KnowledgeEntry{
			ID: id, Scope: scope, ProjectID: projectID, ParentID: parentID, Slug: slug, SortOrder: 900,
			Type: "practice", Title: "AHA1 资源：" + filepath.Base(representative.Relative), Body: body,
			Status: domain.KnowledgeCandidate, Confidence: 1, Revision: 1, EvidenceJSON: string(evidence),
			CreatedAt: now, UpdatedAt: now,
		}
		entry.ContentHash = knowledgeContentHash(entry.Title, entry.Body)
		if _, pending := pendingByKey[key]; pending {
			plan.Report.KnowledgePending++
			plan.Report.Skipped = append(plan.Report.Skipped, representative.Relative+": target has a pending asset proposal")
			continue
		}
		action, normalized := knowledgeAction(entry, existingByID)
		plan.knowledge = append(plan.knowledge, plannedKnowledge{Action: action, Entry: normalized})
		planned[normalized.ID] = normalized
		existingByID[normalized.ID] = normalized
		existingByKey[key] = normalized
		incrementKnowledgeAction(plan, action)
	}
	return nil
}

func logicalDocumentPath(relative string) string {
	segments := strings.Split(filepath.ToSlash(relative), "/")
	for index, segment := range segments {
		if index == len(segments)-1 {
			base := strings.TrimSuffix(segment, path.Ext(segment))
			if strings.EqualFold(base, "index") {
				segments[index] = "overview.md"
			} else {
				segments[index] = knowledgeSlug(base) + ".md"
			}
			continue
		}
		segments[index] = knowledgeSlug(segment)
	}
	return path.Join(segments...)
}

func isWorklogPath(relative string) bool {
	value := strings.TrimPrefix(filepath.ToSlash(relative), "./")
	return strings.HasPrefix(value, "worklog/") || strings.Contains(value, "/worklog/")
}

func directoryPrefixes(directory string) []string {
	directory = strings.Trim(filepath.ToSlash(directory), "/.")
	if directory == "" {
		return nil
	}
	parts := strings.Split(directory, "/")
	result := make([]string, 0, len(parts))
	for index := range parts {
		result = append(result, strings.Join(parts[:index+1], "/"))
	}
	return result
}

func metadataText(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func metadataFloat(metadata map[string]any, key string, fallback float64) float64 {
	value := metadataText(metadata, key)
	if value == "" {
		return fallback
	}
	result, err := strconv.ParseFloat(value, 64)
	if err != nil || result < 0 || result > 1 {
		return fallback
	}
	return result
}

func metadataTime(metadata map[string]any, key string) time.Time {
	value := metadataText(metadata, key)
	result, _ := time.Parse(time.RFC3339, value)
	return result
}

func firstHeading(body string) string {
	match := headingPattern.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func fileTime(filePath string) time.Time {
	info, err := os.Stat(filePath)
	if err != nil {
		return time.Now().UTC()
	}
	return info.ModTime().UTC()
}

func normalizeRepository(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "git@")
	value = strings.TrimPrefix(value, "ssh://")
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	if index := strings.Index(value, "@"); index >= 0 {
		value = value[index+1:]
	}
	if colon := strings.Index(value, ":"); colon >= 0 && !strings.Contains(value[:colon], "/") {
		value = value[:colon] + "/" + value[colon+1:]
	}
	value = strings.TrimSuffix(strings.TrimSuffix(value, "/"), ".git")
	return strings.Trim(value, "/")
}

func stableID(prefix string, values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:12])
}

func knowledgeRootID(scope, projectID string) string {
	if scope == "global" {
		return "knowledge_root_global"
	}
	return "knowledge_root_project_" + projectID
}

func knowledgeSlug(value string) string {
	result := store.KnowledgeSlug(value, "legacy")
	if result == "" {
		return "legacy"
	}
	return result
}

func disambiguatedSlug(slug, id string) string {
	suffix := id
	if index := strings.LastIndex(id, "_"); index >= 0 {
		suffix = id[index+1:]
	}
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return strings.Trim(slug+"-"+suffix, "-")
}

func folderTitle(value string) string {
	switch strings.ToLower(value) {
	case "general":
		return "AHA1 通用知识"
	case "personal":
		return "AHA1 个人知识"
	case "capture":
		return "AHA1 原始捕获"
	case "navigation":
		return "项目导航"
	case "worklog":
		return "历史工作记录"
	case "solutions":
		return "解决方案"
	case "wiki":
		return "知识文档"
	case "modules":
		return "模块索引"
	case "flows":
		return "流程索引"
	case "tasks":
		return "任务记录"
	default:
		return value
	}
}

func knowledgeContentHash(title, body string) string {
	return hashBytes([]byte(strings.TrimSpace(title) + "\n" + strings.TrimSpace(body)))
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func knowledgeKey(scope, projectID, parentID, slug string) string {
	return strings.Join([]string{scope, projectID, parentID, slug}, "\x00")
}

func evidenceSourcePath(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	var evidence struct {
		SourcePath string `json:"source_path"`
	}
	if json.Unmarshal([]byte(value), &evidence) != nil {
		return ""
	}
	return evidence.SourcePath
}

func knowledgeEqual(left, right domain.KnowledgeEntry) bool {
	return left.Scope == right.Scope && left.ProjectID == right.ProjectID && left.ParentID == right.ParentID && left.Slug == right.Slug &&
		left.SortOrder == right.SortOrder && left.IsIndex == right.IsIndex && left.Type == right.Type && left.Title == right.Title &&
		left.Body == right.Body && left.Status == right.Status && left.ProductLineID == right.ProductLineID && left.Confidence == right.Confidence && left.EvidenceJSON == right.EvidenceJSON
}

func proposalEqual(left, right domain.KnowledgeProposal) bool {
	return left.EntryID == right.EntryID && left.BaseRevision == right.BaseRevision && left.Status == right.Status && knowledgeEqual(left.Proposed, right.Proposed)
}

func skillMetadataEqual(left, right domain.Skill) bool {
	return left.PackageSlug == right.PackageSlug && left.Scope == right.Scope && left.ProjectID == right.ProjectID && left.Name == right.Name && left.Description == right.Description && left.Instructions == right.Instructions && left.Status == right.Status && left.Enabled == right.Enabled
}

func skillFilesEqual(left, right []domain.SkillFile) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy, rightCopy := append([]domain.SkillFile{}, left...), append([]domain.SkillFile{}, right...)
	sort.Slice(leftCopy, func(i, j int) bool { return leftCopy[i].Path < leftCopy[j].Path })
	sort.Slice(rightCopy, func(i, j int) bool { return rightCopy[i].Path < rightCopy[j].Path })
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}

func uniqueProposals(values []domain.KnowledgeProposal) []domain.KnowledgeProposal {
	seen := map[string]bool{}
	result := make([]domain.KnowledgeProposal, 0, len(values))
	for _, value := range values {
		if seen[value.ID] {
			continue
		}
		seen[value.ID] = true
		result = append(result, value)
	}
	return result
}

func uniqueStrings(values []string) []string {
	result := values[:0]
	for index, value := range values {
		if index > 0 && value == values[index-1] {
			continue
		}
		result = append(result, value)
	}
	return result
}

func entryDepth(entry domain.KnowledgeEntry, entries map[string]domain.KnowledgeEntry) int {
	depth := 0
	seen := map[string]bool{entry.ID: true}
	for entry.ParentID != "" {
		parent, ok := entries[entry.ParentID]
		if !ok || seen[parent.ID] {
			break
		}
		seen[parent.ID] = true
		depth++
		entry = parent
	}
	return depth
}

func sourceSnapshot(source string) (int, int64, string, error) {
	type item struct {
		path string
		size int64
		hash string
	}
	items := []item{}
	err := filepath.WalkDir(source, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" && filePath != source {
				return fs.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, filePath)
		if err != nil {
			return err
		}
		items = append(items, item{path: filepath.ToSlash(relative), size: int64(len(data)), hash: hashBytes(data)})
		return nil
	})
	if err != nil {
		return 0, 0, "", err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].path < items[j].path })
	manifest := sha256.New()
	var bytes int64
	for _, item := range items {
		bytes += item.size
		_, _ = fmt.Fprintf(manifest, "%s\x00%d\x00%s\n", item.path, item.size, item.hash)
	}
	return len(items), bytes, hex.EncodeToString(manifest.Sum(nil)), nil
}

func scanKnowledgeAssets(source string) ([]knowledgeAsset, error) {
	result := []knowledgeAsset{}
	err := filepath.WalkDir(source, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".png", ".svg", ".jpg", ".jpeg", ".gif", ".webp":
			if !strings.Contains(filepath.ToSlash(filePath), "/skills/") {
				data, err := os.ReadFile(filePath)
				if err != nil {
					return err
				}
				relative, err := filepath.Rel(source, filePath)
				if err != nil {
					return err
				}
				mimeType := assetMIME(filepath.Ext(entry.Name()))
				if !validAssetData(mimeType, data) {
					return fmt.Errorf("knowledge asset failed content validation: %s", filepath.ToSlash(relative))
				}
				result = append(result, knowledgeAsset{
					Absolute: filePath, Relative: filepath.ToSlash(relative), Hash: hashBytes(data),
					MIME: mimeType, Data: data,
				})
			}
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Relative < result[j].Relative })
	return result, err
}

func assetMIME(extension string) string {
	switch strings.ToLower(extension) {
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func validAssetData(mimeType string, data []byte) bool {
	switch mimeType {
	case "image/png":
		return bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n"))
	case "image/jpeg":
		return len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff
	case "image/gif":
		return bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))
	case "image/webp":
		return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
	case "image/svg+xml":
		return utf8.Valid(data) && strings.Contains(strings.ToLower(string(data)), "<svg") && !unsafeSVG.Match(data)
	default:
		return false
	}
}

// Verify checks the invariants that matter after an import. It intentionally
// reads through the Store API rather than relying on importer bookkeeping.
func Verify(ctx context.Context, target Target) error {
	projects, err := target.ListProjects(ctx)
	if err != nil {
		return err
	}
	scopes := []struct{ scope, projectID string }{{scope: "global"}}
	for _, project := range projects {
		scopes = append(scopes, struct{ scope, projectID string }{scope: "project", projectID: project.ID})
	}
	for _, scope := range scopes {
		entries, err := target.ListKnowledge(ctx, scope.scope, scope.projectID, nil)
		if err != nil {
			return err
		}
		roots, byID, siblings := 0, map[string]domain.KnowledgeEntry{}, map[string]bool{}
		for _, entry := range entries {
			byID[entry.ID] = entry
			if entry.IsIndex {
				roots++
			}
			key := entry.ParentID + "\x00" + entry.Slug
			if entry.Slug != "" && siblings[key] {
				return fmt.Errorf("duplicate knowledge sibling slug %s", entry.Slug)
			}
			siblings[key] = true
		}
		if roots != 1 {
			return fmt.Errorf("knowledge scope %s/%s has %d roots", scope.scope, scope.projectID, roots)
		}
		for _, entry := range entries {
			if entry.IsIndex {
				continue
			}
			if _, ok := byID[entry.ParentID]; !ok {
				return fmt.Errorf("knowledge %s has an orphan parent", entry.ID)
			}
			if entry.Slug != "" && !store.ValidKnowledgeSlug(entry.Slug) {
				return fmt.Errorf("knowledge %s has invalid slug %s", entry.ID, entry.Slug)
			}
			seen := map[string]bool{entry.ID: true}
			current := entry
			for current.ParentID != "" {
				parent := byID[current.ParentID]
				if seen[parent.ID] {
					return fmt.Errorf("knowledge %s is in a cycle", entry.ID)
				}
				seen[parent.ID] = true
				current = parent
			}
		}
	}
	return nil
}
