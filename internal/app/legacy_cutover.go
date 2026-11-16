package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

// LegacyExportResult reports how many legacy entries were written into one
// project space. Exportable counts substantive rows (non-index, non-navigation);
// Written counts successful file writes. Cutover compares the exported ID set
// with the whole legacy inventory and aborts on any mismatch.
type LegacyExportResult struct {
	ProjectID  string   `json:"project_id"`
	Exportable int      `json:"exportable"`
	Written    int      `json:"written"`
	EntryIDs   []string `json:"entry_ids,omitempty"`
}

// ExportLegacyKnowledgeToProjectSpace copies the project's legacy knowledge
// entries into its vault space as ordinary documents. The dedicated channel
// library's root page is created by space initialization; root/index rows are
// therefore not exported again. The operation is idempotent through the
// per-entry idempotency key.
func (s *Service) ExportLegacyKnowledgeToProjectSpace(ctx context.Context, projectID string) (LegacyExportResult, error) {
	result := LegacyExportResult{ProjectID: strings.TrimSpace(projectID)}
	if !s.projectKnowledgeSpaceEnabled() || !s.knowledgeVault.Enabled() {
		return result, knowledgeSpaceError("knowledge_vault_disabled", "文件知识库尚未启用，不能执行旧库导出", knowledgevault.ErrDisabled)
	}
	project, err := s.store.Project(ctx, result.ProjectID)
	if err != nil {
		return result, err
	}
	if _, err := s.knowledgeVault.ProvisionProjectSpace(ctx, project.ID, project.Name); err != nil {
		return result, knowledgeSpaceError("knowledge_space_init_failed", "知识空间初始化失败："+err.Error(), err)
	}
	entries, err := s.store.ListKnowledge(ctx, "project", project.ID, nil)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if entry.IsIndex || strings.TrimSpace(entry.Title) == "" {
			continue
		}
		// Navigation roots and category pages are schema seeds, not knowledge;
		// the inventory excludes them too, so exporting them would make the
		// safety count disagree with what actually needs preserving.
		if strings.EqualFold(strings.TrimSpace(entry.Type), "navigation") {
			continue
		}
		result.Exportable++
		canonicalType, _ := knowledgevault.NormalizeType(entry.Type, "")
		if canonicalType == "" {
			canonicalType = knowledgevault.TypePractice
		}
		writeResult, writeErr := s.knowledgeVault.WriteKnowledge(ctx, knowledgevault.WriteRequest{
			Mode: knowledgevault.WriteModeCreate, Scope: knowledgevault.WriteScopeProject,
			ProjectID: project.ID, Type: canonicalType,
			Title: legacyExportTitle(entry.Title), Summary: legacyExportSummary("", entry.Body),
			Body: legacyExportBody(entry.Body), DuplicateAck: true,
			ForceEntryID: entry.ID, IdempotencyKey: "legacy-export:" + entry.ID,
			Writer: "migration", SkipBudget: true,
		})
		if writeErr != nil {
			return result, fmt.Errorf("export legacy knowledge %s: %w", entry.ID, writeErr)
		}
		result.Written++
		result.EntryIDs = append(result.EntryIDs, writeResult.ID)
	}
	return result, nil
}

func firstStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func legacyExportTitle(value string) string {
	title := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
	runes := []rune(title)
	if len(runes) < 3 {
		title = string(runes) + "（旧库）"
	}
	if len([]rune(title)) > 300 {
		title = string([]rune(title)[:300])
	}
	return title
}

func legacyExportSummary(summary, body string) string {
	value := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(summary, "\r", " "), "\n", " "))
	if value == "" {
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(strings.TrimLeft(line, "#"))
			if len([]rune(line)) >= 20 {
				value = line
				break
			}
		}
	}
	for len([]rune(value)) < 20 {
		value += "旧库迁移说明"
	}
	if len([]rune(value)) > 300 {
		value = string([]rune(value)[:300])
	}
	return value
}

func legacyExportBody(body string) string {
	body = strings.TrimSpace(body)
	for len([]rune(body)) < 20 {
		body += "\n\n（迁移自旧知识库）"
	}
	return body
}

// CutoverLegacyKnowledge is the one-way end of the legacy library: export the
// project's entries, back up and verify the database, then drop the old
// knowledge tables. VerifyExport is evaluated inside the store cutover after
// the backup, so a failed export leaves every table intact.
func (s *Service) CutoverLegacyKnowledge(ctx context.Context, projectID string, backupDir string) (store.LegacyCutoverReport, error) {
	if applied, err := s.store.LegacyCutoverApplied(ctx); err != nil {
		return store.LegacyCutoverReport{}, err
	} else if applied {
		// Repeated startup/support calls must not try to export from tables
		// that were already removed.
		return store.LegacyCutoverReport{Applied: true}, nil
	}
	policies, err := s.store.CountChannelKnowledgePolicies(ctx)
	if err != nil {
		return store.LegacyCutoverReport{}, err
	}
	if policies > 0 {
		bindings, bindErr := s.store.CountKnowledgeBindings(ctx)
		if bindErr != nil {
			return store.LegacyCutoverReport{}, bindErr
		}
		if bindings == 0 {
			return store.LegacyCutoverReport{}, fmt.Errorf("旧渠道仍有 %d 条 per-entry knowledge policy，但没有 knowledge_binding；请先为渠道配置空间绑定再切黑", policies)
		}
	}
	exported, err := s.ExportLegacyKnowledgeToProjectSpace(ctx, projectID)
	if err != nil {
		return store.LegacyCutoverReport{}, err
	}
	allSubstantive, err := s.store.LegacySubstantiveEntryIDs(ctx)
	if err != nil {
		return store.LegacyCutoverReport{}, err
	}
	exportedIDs := make(map[string]bool, len(exported.EntryIDs))
	for _, id := range exported.EntryIDs {
		exportedIDs[id] = true
	}
	missing := make([]string, 0)
	for id := range allSubstantive {
		if !exportedIDs[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 || len(exportedIDs) != len(allSubstantive) {
		// Refuse to drop substantive legacy documents that this export did
		// not account for. Comparing ID sets (not a total that mixes index,
		// navigation, and real documents) is what makes this safe.
		return store.LegacyCutoverReport{}, fmt.Errorf("旧库仍有 %d 条实质知识未导出（目标导出 %d 条）；已中止切黑，示例 ID：%s",
			len(missing), len(exportedIDs), strings.Join(firstStrings(missing, 3), ", "))
	}
	report, err := s.store.CutoverLegacyKnowledge(ctx, store.LegacyCutoverOptions{
		BackupDir: backupDir,
		Now: func() time.Time {
			return time.Now().UTC()
		},
		VerifyExport: func(context.Context) error {
			if exported.Written != exported.Exportable {
				return fmt.Errorf("exported %d of %d legacy entries", exported.Written, exported.Exportable)
			}
			return nil
		},
	})
	if err != nil {
		return report, err
	}
	// The current process must fail closed immediately: the tables are gone,
	// so any later read that still believes legacy knowledge exists would
	// error halfway through a Turn instead of routing to the vault.
	s.SetLegacyKnowledgeDisabled(true)
	if _, err := s.knowledgeVault.Rebuild(ctx); err != nil {
		return report, fmt.Errorf("rebuild knowledge index after cutover: %w", err)
	}
	return report, nil
}

// LegacyKnowledgeCutoverAvailable reports whether the legacy tables are still
// present. The Web settings page uses it only to explain the one-way migration;
// it never offers a rollback.
func (s *Service) LegacyKnowledgeCutoverAvailable(ctx context.Context) (bool, error) {
	applied, err := s.store.LegacyCutoverApplied(ctx)
	if err != nil {
		return false, err
	}
	return !applied, nil
}

// IsLegacyKnowledgeTableMissing reports a query failure caused by the cutover
// so callers can render "未迁移/已切黑" instead of a panic or an empty success.
func IsLegacyKnowledgeTableMissing(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table: knowledge_entries") ||
		strings.Contains(message, "no such table: knowledge_libraries")
}
