package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// Single-switch settings endpoints. The manager switch is the only control the
// owner sees; the payload additionally carries backend-computed readiness,
// legacy inventory and verified migration/import metadata so the Web UI never
// has to guess why the switch is hidden or disabled.
//
// The legacy knowledge tables are never read or written by these handlers,
// except by the migration endpoint that exports from them.

type knowledgeVaultSettingsView struct {
	knowledgevault.Status
	Readiness           knowledgevault.Readiness        `json:"readiness"`
	Legacy              domain.LegacyKnowledgeInventory `json:"legacy"`
	Configured          bool                            `json:"configured"`
	ImportSource        string                          `json:"import_source,omitempty"`
	MigrationVerifiedAt time.Time                       `json:"migration_verified_at,omitempty"`
	ImportVerifiedAt    time.Time                       `json:"import_verified_at,omitempty"`
	LastBackupPath      string                          `json:"last_backup_path,omitempty"`
	SettingsUpdatedAt   time.Time                       `json:"settings_updated_at,omitempty"`
}

func (s *Server) knowledgeVaultView(ctx context.Context) (knowledgeVaultSettingsView, knowledgevault.Readiness, error) {
	status := s.knowledgeVault.Status(ctx)
	settings, err := s.store.KnowledgeVaultSettings(ctx)
	if err != nil {
		return knowledgeVaultSettingsView{}, knowledgevault.Readiness{}, err
	}
	inventory, err := s.store.LegacyKnowledgeInventory(ctx)
	if err != nil {
		return knowledgeVaultSettingsView{}, knowledgevault.Readiness{}, err
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return knowledgeVaultSettingsView{}, knowledgevault.Readiness{}, err
	}
	known := make([]string, 0, len(projects))
	for _, project := range projects {
		// Channel hosts and the pet's system project do not own knowledge
		// spaces, so they are never "unbound" projects in readiness.
		if domain.IsSystemManagedProject(project) {
			continue
		}
		known = append(known, project.ID)
	}
	readiness := knowledgevault.EvaluateReadiness(knowledgevault.ReadinessInput{
		VaultInitialized:  status.VaultInitialized,
		VaultWritable:     status.VaultWritable,
		IndexReady:        status.IndexReady,
		IndexWritable:     status.IndexWritable,
		IndexStale:        status.IndexStale,
		IndexError:        status.IndexError,
		EntryCount:        status.EntryCount,
		IndexWarnings:     status.IndexWarnings,
		ProjectBindings:   status.ProjectBindings,
		KnownProjects:     known,
		Configured:        settings.Configured,
		Enabled:           status.Enabled,
		LegacyEntries:     inventory.Entries,
		LegacySkills:      inventory.Skills,
		LegacySkillDirs:   inventory.SkillPackages,
		MigrationVerified: !settings.MigrationVerifiedAt.IsZero(),
		ImportVerified:    !settings.ImportVerifiedAt.IsZero(),
	})
	view := knowledgeVaultSettingsView{
		Status: status, Readiness: readiness, Legacy: inventory,
		Configured: settings.Configured, ImportSource: settings.ImportSource,
		MigrationVerifiedAt: settings.MigrationVerifiedAt,
		ImportVerifiedAt:    settings.ImportVerifiedAt,
		LastBackupPath:      settings.LastBackupPath,
		SettingsUpdatedAt:   settings.UpdatedAt,
	}
	return view, readiness, nil
}

func (s *Server) writeKnowledgeVaultView(writer http.ResponseWriter, request *http.Request, extra map[string]any) {
	view, _, err := s.knowledgeVaultView(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "knowledge_vault_status_failed", "message": err.Error(),
		})
		return
	}
	payload := map[string]any{"ok": true, "settings": view}
	for key, value := range extra {
		payload[key] = value
	}
	writeJSON(writer, http.StatusOK, payload)
}

func (s *Server) knowledgeVaultSettings(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	s.writeKnowledgeVaultView(writer, request, nil)
}

func (s *Server) updateKnowledgeVaultSettings(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	var payload struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if payload.Enabled == nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_enabled", "enabled is required")
		return
	}
	enabled := *payload.Enabled
	view, readiness, err := s.knowledgeVaultView(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "knowledge_vault_status_failed", "message": err.Error(),
		})
		return
	}
	if enabled {
		if !readiness.CanEnable {
			// blocked (needs_migration / needs_rebuild / error) must not be
			// enabled. Degraded is allowed and carries its warning.
			writeJSON(writer, http.StatusConflict, map[string]any{
				"ok": false, "error": "knowledge_vault_not_ready",
				"message": readiness.Message, "missing": readiness.Missing,
				"settings": view,
			})
			return
		}
	} else if !readiness.RollbackAllowed {
		// Once the instance has committed to the vault (configured switch,
		// verified migration/import, or a live vault) the legacy database is a
		// read-only backup. Disabling it directly through the API would undo the
		// owner's no-rollback decision, so the backend refuses the transition
		// instead of relying on the disabled Web switch alone. This check is
		// deliberately independent of the runtime switch: a vault whose
		// directory/index failed to open starts disabled while the DB still says
		// enabled=1/configured=1, and a close in that state would persist a
		// legacy fallback for the next restart. The payload keeps the full
		// settings view so older clients still render their error path.
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "knowledge_vault_rollback_locked",
			"message":  "文件知识库已迁移或启用，不能关闭回退到旧知识库。",
			"settings": view,
		})
		return
	}
	previousEnabled, previousSource := s.knowledgeVault.Enabled(), s.knowledgeVault.Source()
	if err := s.knowledgeVault.SetEnabled(request.Context(), enabled); err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "knowledge_vault_toggle_failed", "message": err.Error(),
		})
		return
	}
	if err := s.store.SetKnowledgeVaultSwitch(request.Context(), enabled); err != nil {
		s.rollbackKnowledgeVaultSwitch(previousEnabled, previousSource)
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_settings_failed")
		return
	}
	s.knowledgeVault.SetSource(knowledgevault.SourceDatabase)
	s.audit(request, "knowledge_vault.toggle", "knowledge_vault_settings", "1", map[string]any{"enabled": enabled})
	s.writeKnowledgeVaultView(writer, request, nil)
}

func (s *Server) initializeKnowledgeVault(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	// Init creates an empty vault with optional samples. On an upgrade that
	// still has legacy knowledge it would hide that knowledge behind a new
	// switch, so the owner must migrate or import instead.
	settings, settingsErr := s.store.KnowledgeVaultSettings(request.Context())
	if settingsErr != nil {
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_settings_failed")
		return
	}
	inventory, inventoryErr := s.store.LegacyKnowledgeInventory(request.Context())
	if inventoryErr != nil {
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_settings_failed")
		return
	}
	if inventory.HasLegacy() && !settings.VaultPopulated() {
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "knowledge_vault_migration_required",
			"message": "检测到旧知识库；请先迁移旧知识或导入已有知识库，不能直接创建空知识库。",
		})
		return
	}
	// The body is optional; a missing one means "seed the samples".
	samples := true
	var payload struct {
		Samples *bool `json:"samples"`
	}
	if err := decodeJSON(request, &payload); err != nil && !errors.Is(err, io.EOF) {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if payload.Samples != nil {
		samples = *payload.Samples
	}
	previousEnabled, previousSource := s.knowledgeVault.Enabled(), s.knowledgeVault.Source()
	result, err := s.knowledgeVault.Initialize(request.Context(), samples)
	if errors.Is(err, knowledgevault.ErrVaultExists) {
		writeError(writer, http.StatusConflict, "knowledge_vault_exists")
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "knowledge_vault_init_failed", "message": err.Error(),
		})
		return
	}
	// Initialization implies "on": the owner asked for a usable vault.
	if err := s.store.SetKnowledgeVaultSwitch(request.Context(), true); err != nil {
		s.rollbackKnowledgeVaultSwitch(previousEnabled, previousSource)
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_settings_failed")
		return
	}
	s.knowledgeVault.SetSource(knowledgevault.SourceDatabase)
	s.audit(request, "knowledge_vault.init", "knowledge_vault", result.VaultPath, map[string]any{
		"samples": result.Samples, "enabled": true,
	})
	s.writeKnowledgeVaultView(writer, request, map[string]any{
		"created": result.Created, "skipped": result.Skipped,
		"samples": result.Samples, "rebuild": result.Rebuild,
	})
}

func (s *Server) rebuildKnowledgeVaultIndex(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	report, err := s.knowledgeVault.Rebuild(request.Context())
	switch {
	case errors.Is(err, knowledgevault.ErrVaultNotInitialized):
		writeError(writer, http.StatusConflict, "knowledge_vault_not_initialized")
		return
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "knowledge_vault_rebuild_failed", "message": err.Error(),
		})
		return
	}
	s.audit(request, "knowledge_vault.rebuild", "knowledge_vault", report.VaultPath, map[string]any{
		"entries": report.Entries, "warnings": len(report.Warnings),
	})
	s.writeKnowledgeVaultView(writer, request, map[string]any{"rebuild": report})
}

// migrateKnowledgeVault is the legacy upgrade path. It backs up the old
// database and skill packages, exports knowledge into the vault, rebuilds and
// verifies the index, records the verification, then turns the vault on. The
// old data is never deleted.
func (s *Server) migrateKnowledgeVault(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	previousEnabled, previousSource := s.knowledgeVault.Enabled(), s.knowledgeVault.Source()
	report, err := knowledgevault.MigrateLegacyKnowledge(request.Context(), s.knowledgeVault.Config(), knowledgevault.LegacyMigrationOptions{
		DataDir:      s.store.DataDir(),
		DatabasePath: s.store.DatabasePath(),
	})
	switch {
	case errors.Is(err, knowledgevault.ErrNoLegacyKnowledge):
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "knowledge_vault_no_legacy_knowledge",
			"message": err.Error(),
		})
		return
	case errors.Is(err, knowledgevault.ErrTargetNotEmpty):
		// The vault may already be the live knowledge source without verified
		// metadata. Never export the legacy database over it; the owner keeps
		// the current files and the old database as a rollback copy.
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "knowledge_vault_target_not_empty",
			"message": "目标文件知识库已有条目或页面；迁移不会覆盖现有文件，也不会删除旧知识库。请继续使用当前知识库，或先备份并移走现有目录后再迁移。",
		})
		return
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "knowledge_vault_migration_failed", "message": err.Error(),
			"report": report,
		})
		return
	}
	if err := s.finishKnowledgeVaultPopulation(request.Context(), report.VerifiedAt, report.BackupPath, knowledgevault.MigrationSourceLegacy, true); err != nil {
		s.rollbackKnowledgeVaultSwitch(previousEnabled, previousSource)
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "knowledge_vault_enable_failed", "message": err.Error(),
			"report": report,
		})
		return
	}
	s.audit(request, "knowledge_vault.migrate", "knowledge_vault", report.VaultPath, map[string]any{
		"entries": report.VerifiedEntries, "skills": report.VerifiedSkills, "backup": report.BackupPath,
	})
	s.writeKnowledgeVaultView(writer, request, map[string]any{"migration": report})
}

// importKnowledgeVault is the existing-vault path: copy a local directory or
// clone a Git remote, rebuild the index, verify manifest/project bindings and
// only then turn the vault on. A non-empty target is rejected, never merged.
func (s *Server) importKnowledgeVault(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	var payload struct {
		Kind   string `json:"kind"`
		Path   string `json:"path"`
		Remote string `json:"remote"`
		Branch string `json:"branch"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	kind := strings.ToLower(strings.TrimSpace(payload.Kind))
	if kind != "local" && kind != "git" {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_import_kind", "kind must be local or git")
		return
	}
	projects, err := s.store.ListProjects(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_status_failed")
		return
	}
	known := make([]string, 0, len(projects))
	for _, project := range projects {
		// Channel hosts and the pet's system project do not own knowledge
		// spaces, so they are never "unbound" projects in readiness.
		if domain.IsSystemManagedProject(project) {
			continue
		}
		known = append(known, project.ID)
	}
	previousEnabled, previousSource := s.knowledgeVault.Enabled(), s.knowledgeVault.Source()
	report, err := knowledgevault.ImportExistingVault(request.Context(), s.knowledgeVault.Config(), knowledgevault.ExistingVaultImportOptions{
		Kind: kind, Path: payload.Path, Remote: payload.Remote, Branch: payload.Branch,
		KnownProjects: known,
	})
	var validation *knowledgevault.GitValidationError
	switch {
	case errors.Is(err, knowledgevault.ErrTargetNotEmpty):
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "knowledge_vault_target_not_empty",
			"message": "目标知识库目录已有文件；请先移走旧目录，导入不会自动合并。",
		})
		return
	case errors.Is(err, knowledgevault.ErrImportSourceMissing):
		writeError(writer, http.StatusBadRequest, "knowledge_vault_import_source_missing")
		return
	case errors.Is(err, knowledgevault.ErrImportManifestMissing):
		writeError(writer, http.StatusBadRequest, "knowledge_vault_manifest_missing")
		return
	case errors.As(err, &validation):
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_git_"+validation.Field, validation.Message)
		return
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "knowledge_vault_import_failed", "message": err.Error(),
			"report": report,
		})
		return
	}
	if err := s.finishKnowledgeVaultPopulation(request.Context(), report.VerifiedAt, "", knowledgevault.ImportSourceExisting, false); err != nil {
		s.rollbackKnowledgeVaultSwitch(previousEnabled, previousSource)
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "knowledge_vault_enable_failed", "message": err.Error(),
			"report": report,
		})
		return
	}
	s.audit(request, "knowledge_vault.import", "knowledge_vault", report.VaultPath, map[string]any{
		"kind": report.Kind, "entries": report.IndexedEntries, "files": report.ImportedFiles,
	})
	s.writeKnowledgeVaultView(writer, request, map[string]any{"import": report})
}

// finishKnowledgeVaultPopulation records verified population first, then
// refreshes the manager's reader from the new files/index and turns the switch
// on. Rebuild also covers the case where the manager was already enabled with
// an empty reader: without it the freshly imported vault would only become
// visible after a manual rebuild. If the switch cannot open the freshly built
// vault the caller rolls the in-memory state back; the verification timestamp
// intentionally remains because the files and index did verify.
func (s *Server) finishKnowledgeVaultPopulation(ctx context.Context, verifiedAt time.Time, backupPath, source string, legacyMigration bool) error {
	if err := s.store.RecordKnowledgeVaultImport(ctx, source, verifiedAt, backupPath, legacyMigration); err != nil {
		return err
	}
	if _, err := s.knowledgeVault.Rebuild(ctx); err != nil {
		return fmt.Errorf("refresh knowledge vault after population: %w", err)
	}
	if err := s.knowledgeVault.SetEnabled(ctx, true); err != nil {
		return err
	}
	s.knowledgeVault.SetSource(knowledgevault.SourceDatabase)
	return nil
}

// rollbackKnowledgeVaultSwitch restores the in-memory switch when the database
// write failed. A failed rollback is logged but not fatal: the next settings
// read reports the actual state.
func (s *Server) rollbackKnowledgeVaultSwitch(enabled bool, source string) {
	if err := s.knowledgeVault.SetEnabled(context.Background(), enabled); err != nil {
		if s.logger != nil {
			s.logger.Error("rollback knowledge vault switch failed", "enabled", enabled, "error", err)
		}
	}
	s.knowledgeVault.SetSource(source)
}

// cutoverLegacyKnowledge is the one-way removal of the pre-vault knowledge
// tables. It requires an explicit owner confirmation, exports the dedicated
// library into its project space first, and aborts before any drop when the
// backup or export verification fails.
func (s *Server) cutoverLegacyKnowledge(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil || s.app == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	var payload struct {
		ProjectID string `json:"project_id"`
		BackupDir string `json:"backup_dir"`
		Confirm   bool   `json:"confirm"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if !payload.Confirm {
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "knowledge_cutover_confirmation_required",
			"message": "旧库删除不可回退；请先备份并导出，再以 confirm=true 执行",
		})
		return
	}
	projectID := strings.TrimSpace(payload.ProjectID)
	if projectID == "" {
		writeError(writer, http.StatusBadRequest, "project_id_required")
		return
	}
	report, err := s.app.CutoverLegacyKnowledge(request.Context(), projectID, strings.TrimSpace(payload.BackupDir))
	if err != nil {
		// Project/space failures carry a stable client-facing code (for
		// example project_id_archived); generic cutover failures stay under
		// knowledge_cutover_failed.
		var spaceErr *app.KnowledgeSpaceError
		if errors.As(err, &spaceErr) {
			writeJSON(writer, http.StatusConflict, map[string]any{
				"ok": false, "error": spaceErr.Code, "message": spaceErr.Message,
			})
			return
		}
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "knowledge_cutover_failed", "message": err.Error(),
		})
		return
	}
	if report.BackupPath != "" {
		if settings, settingsErr := s.store.KnowledgeVaultSettings(request.Context()); settingsErr == nil {
			settings.LastBackupPath = report.BackupPath
			_, _ = s.store.UpdateKnowledgeVaultSettings(request.Context(), settings)
		}
	}
	s.audit(request, "knowledge_vault.cutover_legacy", "knowledge_vault", projectID, map[string]any{
		"backup_path": report.BackupPath, "backed_up_rows": report.BackedUpRows,
		"tables": report.TablesDropped,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "cutover": report})
}
