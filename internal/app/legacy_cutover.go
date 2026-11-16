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
// project space. Exportable counts non-index rows; Written counts successful
// file writes. A mismatch aborts the cutover.
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
	exported, err := s.ExportLegacyKnowledgeToProjectSpace(ctx, projectID)
	if err != nil {
		return store.LegacyCutoverReport{}, err
	}
	inventory, err := s.store.LegacyKnowledgeInventory(ctx)
	if err != nil {
		return store.LegacyCutoverReport{}, err
	}
	// Refuse to drop substantive legacy documents that this export did not
	// account for. Navigation roots and index rows are schema seeds, not
	// knowledge; the dedicated library's root is recreated by the space
	// initializer.
	if inventory.Entries > exported.Exportable {
		return store.LegacyCutoverReport{}, fmt.Errorf("旧库仍有 %d 条实质知识，但只导出 %d 条；已中止切黑", inventory.Entries, exported.Exportable)
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
