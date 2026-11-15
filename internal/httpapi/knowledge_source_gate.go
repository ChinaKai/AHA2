package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// The legacy knowledge API is a second source of truth. Once a project reads
// the file vault, serving or mutating the legacy tables from these routes would
// contradict the source the Agent actually uses. These codes are part of the
// diagnostic contract: the Web page shows the message instead of silently
// rendering stale knowledge.
const (
	knowledgeLegacyReadDisabled       = "knowledge_legacy_disabled"
	knowledgeLegacyManagementDisabled = "knowledge_legacy_management_disabled"
)

// knowledgeVaultReadActive reports whether the given scope reads from the
// vault. It is the same gate the app uses for Agent turns, so the legacy HTTP
// API cannot disagree with the live source. Unbound and read-fallback projects
// keep the historical legacy behavior.
func (s *Server) knowledgeVaultReadActive(scope, projectID string) bool {
	return s != nil && s.app != nil && s.app.KnowledgeVaultReadEnabled(scope, projectID)
}

func writeKnowledgeVaultReadConflict(writer http.ResponseWriter) {
	writeErrorDetail(writer, http.StatusConflict, knowledgeLegacyReadDisabled,
		"知识库已启用文件知识库（vault），旧知识接口在该项目上已停用；请使用 /api/v1/knowledge-vault 的 libraries、tree 和 doc 接口。")
}

func writeKnowledgeVaultManagementConflict(writer http.ResponseWriter) {
	writeErrorDetail(writer, http.StatusConflict, knowledgeLegacyManagementDisabled,
		"知识库已启用文件知识库（vault），旧库知识库管理接口不再修改旧表；请在 vault 中管理知识库。")
}

// knowledgeLibraryVaultActive reports whether a legacy library row belongs to a
// project that reads the vault. Either the container project or the bound
// project is enough: unbinding or deleting a library mutates both sides.
func (s *Server) knowledgeLibraryVaultActive(ctx context.Context, library domain.KnowledgeLibrary) bool {
	if s == nil || s.app == nil {
		return false
	}
	for _, projectID := range []string{library.ContainerProjectID, library.BoundProjectID} {
		if strings.TrimSpace(projectID) != "" && s.app.KnowledgeVaultReadEnabled("project", projectID) {
			return true
		}
	}
	return false
}

// knowledgeLibraryVaultActiveByID looks the library up first. A missing row is
// not a vault conflict: the store keeps returning its historical not-found.
func (s *Server) knowledgeLibraryVaultActiveByID(ctx context.Context, id string) bool {
	if s == nil || s.app == nil || s.store == nil {
		return false
	}
	library, err := s.store.KnowledgeLibrary(ctx, id)
	if err != nil {
		return false
	}
	return s.knowledgeLibraryVaultActive(ctx, library)
}
