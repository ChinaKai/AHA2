package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// Phase 3 settings endpoints. They only control the file-backed vault: the
// legacy knowledge tables are never read or written here.

func (s *Server) knowledgeVaultSettings(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "settings": s.knowledgeVault.Status(request.Context()),
	})
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
	previousEnabled, previousSource := s.knowledgeVault.Enabled(), s.knowledgeVault.Source()
	if err := s.knowledgeVault.SetEnabled(request.Context(), *payload.Enabled); err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "knowledge_vault_toggle_failed", "message": err.Error(),
		})
		return
	}
	if _, err := s.store.UpdateKnowledgeVaultSettings(request.Context(), domain.KnowledgeVaultSettings{
		Enabled: *payload.Enabled, Configured: true, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		s.rollbackKnowledgeVaultSwitch(previousEnabled, previousSource)
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_settings_failed")
		return
	}
	s.knowledgeVault.SetSource(knowledgevault.SourceDatabase)
	s.audit(request, "knowledge_vault.toggle", "knowledge_vault_settings", "1", map[string]any{"enabled": *payload.Enabled})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "settings": s.knowledgeVault.Status(request.Context()),
	})
}

func (s *Server) initializeKnowledgeVault(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
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
	if _, err := s.store.UpdateKnowledgeVaultSettings(request.Context(), domain.KnowledgeVaultSettings{
		Enabled: true, Configured: true, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		s.rollbackKnowledgeVaultSwitch(previousEnabled, previousSource)
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_settings_failed")
		return
	}
	s.knowledgeVault.SetSource(knowledgevault.SourceDatabase)
	s.audit(request, "knowledge_vault.init", "knowledge_vault", result.VaultPath, map[string]any{
		"samples": result.Samples, "enabled": true,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "settings": s.knowledgeVault.Status(request.Context()),
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
	case errors.Is(err, knowledgevault.ErrDisabled):
		writeError(writer, http.StatusConflict, "knowledge_vault_disabled")
		return
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
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "settings": s.knowledgeVault.Status(request.Context()), "rebuild": report,
	})
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
