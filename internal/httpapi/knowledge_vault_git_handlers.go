package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// Owner-only manual Git management for the file vault. Every operation is
// explicit; nothing here is scheduled and no failure triggers a fallback
// merge, force push or reset.

func (s *Server) knowledgeVaultGitStatus(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	status, err := s.knowledgeVault.Git().Status(request.Context())
	if err != nil {
		s.writeKnowledgeVaultGitError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "git": status})
}

func (s *Server) updateKnowledgeVaultGitRemote(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	var payload struct {
		Remote string `json:"remote"`
		Branch string `json:"branch"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := s.knowledgeVault.Git().SetRemote(request.Context(), strings.TrimSpace(payload.Remote), strings.TrimSpace(payload.Branch))
	if err != nil {
		s.writeKnowledgeVaultGitError(writer, err)
		return
	}
	s.resumeKnowledgeVaultAutoSync()
	// Saving a remote only retries the loop; it does not touch a diverged
	// branch. Keep open sync notifications so an unresolved conflict is not
	// hidden from the pet until a pull/push/sync actually succeeds.
	s.audit(request, "knowledge_vault.git_remote", "knowledge_vault_git", "origin", map[string]any{
		"branch": result.Status.Branch, "remote": result.Status.Remote, "remote_set": result.Status.RemoteSet,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "git": result.Status, "message": result.Message,
	})
}

func (s *Server) initializeKnowledgeVaultGit(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	result, err := s.knowledgeVault.Git().Init(request.Context())
	if err != nil {
		s.writeKnowledgeVaultGitError(writer, err)
		return
	}
	s.resumeKnowledgeVaultAutoSync()
	// Re-running init on an existing repository leaves any divergence in
	// place, so it cannot prove a conflict notification is stale. The next
	// successful pull/push/sync closes it through the recovery path.
	s.audit(request, "knowledge_vault.git_init", "knowledge_vault_git", "vault", map[string]any{
		"branch": result.Status.Branch, "created": result.Created,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "git": result.Status, "message": result.Message, "created": result.Created,
	})
}

func (s *Server) commitKnowledgeVaultGit(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	message := ""
	var payload struct {
		Message string `json:"message"`
	}
	if err := decodeJSON(request, &payload); err != nil && !errors.Is(err, io.EOF) {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	message = strings.TrimSpace(payload.Message)
	result, err := s.knowledgeVault.Git().Commit(request.Context(), message)
	if err != nil {
		s.writeKnowledgeVaultGitError(writer, err)
		return
	}
	// A manual commit usually resolves a local unmerged state; let auto-sync
	// try again on the next tick.
	s.resumeKnowledgeVaultAutoSync()
	s.audit(request, "knowledge_vault.git_commit", "knowledge_vault_git", "vault", map[string]any{
		"branch": result.Status.Branch, "commit": result.Status.LastCommit,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "git": result.Status, "message": result.Message,
	})
}

func (s *Server) pullKnowledgeVaultGit(writer http.ResponseWriter, request *http.Request) {
	s.runKnowledgeVaultGitOperation(writer, request, "knowledge_vault.git_pull", true, func(git *knowledgevault.Git) (knowledgevault.GitOpResult, error) {
		return git.Pull(request.Context())
	})
}

func (s *Server) pushKnowledgeVaultGit(writer http.ResponseWriter, request *http.Request) {
	s.runKnowledgeVaultGitOperation(writer, request, "knowledge_vault.git_push", false, func(git *knowledgevault.Git) (knowledgevault.GitOpResult, error) {
		return git.Push(request.Context())
	})
}

func (s *Server) syncKnowledgeVaultGit(writer http.ResponseWriter, request *http.Request) {
	s.runKnowledgeVaultGitOperation(writer, request, "knowledge_vault.git_sync", true, func(git *knowledgevault.Git) (knowledgevault.GitOpResult, error) {
		return git.Sync(request.Context())
	})
}

// runKnowledgeVaultGitOperation always reports the Git outcome, but a pull or
// sync is not ready until the index has been rebuilt from the new files. A
// rebuild failure is returned as an error with the Git status attached instead
// of a misleading success.
func (s *Server) runKnowledgeVaultGitOperation(writer http.ResponseWriter, request *http.Request, action string, rebuild bool, run func(*knowledgevault.Git) (knowledgevault.GitOpResult, error)) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	result, err := run(s.knowledgeVault.Git())
	if err != nil {
		s.writeKnowledgeVaultGitError(writer, err)
		return
	}
	s.resumeKnowledgeVaultAutoSync()
	s.resolveKnowledgeVaultSyncNotifications(request.Context())
	extra := map[string]any{"git": result.Status, "message": result.Message}
	if rebuild {
		report, rebuildErr := s.knowledgeVault.Rebuild(request.Context())
		if rebuildErr != nil {
			// The Git operation succeeded but the projection did not: report the
			// failure and let the settings panel show needs_rebuild/error.
			s.audit(request, action, "knowledge_vault_git", "vault", map[string]any{
				"branch": result.Status.Branch, "index_rebuilt": false,
			})
			writeJSON(writer, http.StatusInternalServerError, map[string]any{
				"ok": false, "error": "knowledge_vault_rebuild_failed",
				"message": "Git 操作完成，但索引重建失败：" + rebuildErr.Error(),
				"git":     result.Status, "ready": false,
			})
			return
		}
		extra["rebuild"] = report
	}
	s.audit(request, action, "knowledge_vault_git", "vault", map[string]any{
		"branch": result.Status.Branch, "ahead": result.Status.Ahead, "behind": result.Status.Behind,
		"index_rebuilt": rebuild,
	})
	if view, _, viewErr := s.knowledgeVaultView(request.Context()); viewErr == nil {
		extra["settings"] = view
	}
	extra["ok"] = true
	writeJSON(writer, http.StatusOK, extra)
}

func (s *Server) writeKnowledgeVaultGitError(writer http.ResponseWriter, err error) {
	var validation *knowledgevault.GitValidationError
	var commandErr *knowledgevault.GitCommandError
	code := "knowledge_vault_git_failed"
	statusCode := http.StatusInternalServerError
	switch {
	case errors.Is(err, knowledgevault.ErrDisabled):
		writeKnowledgeVaultDisabled(writer)
		return
	case errors.Is(err, knowledgevault.ErrGitUnavailable):
		statusCode, code = http.StatusConflict, "git_unavailable"
	case errors.Is(err, knowledgevault.ErrVaultNotInitialized):
		statusCode, code = http.StatusConflict, "knowledge_vault_not_initialized"
	case errors.Is(err, knowledgevault.ErrNotGitRepository):
		statusCode, code = http.StatusConflict, "git_not_initialized"
	case errors.Is(err, knowledgevault.ErrGitRemoteMissing):
		statusCode, code = http.StatusConflict, "git_remote_missing"
	case errors.Is(err, knowledgevault.ErrGitRemoteBranchMissing):
		statusCode, code = http.StatusConflict, "git_remote_branch_missing"
	case errors.Is(err, knowledgevault.ErrGitDirty):
		statusCode, code = http.StatusConflict, "git_worktree_dirty"
	case errors.Is(err, knowledgevault.ErrGitDiverged):
		statusCode, code = http.StatusConflict, "git_not_fast_forward"
	case errors.Is(err, knowledgevault.ErrGitPushRejected):
		statusCode, code = http.StatusConflict, "git_push_rejected"
	case errors.Is(err, knowledgevault.ErrGitNoCommit):
		statusCode, code = http.StatusConflict, "git_no_commit"
	case errors.As(err, &validation):
		statusCode, code = http.StatusBadRequest, "invalid_git_"+validation.Field
	case errors.As(err, &commandErr):
		statusCode, code = http.StatusBadGateway, "git_command_failed"
	}
	writeJSON(writer, statusCode, map[string]any{"ok": false, "error": code, "message": err.Error()})
}
