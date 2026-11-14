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
	s.audit(request, "knowledge_vault.git_commit", "knowledge_vault_git", "vault", map[string]any{
		"branch": result.Status.Branch, "commit": result.Status.LastCommit,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "git": result.Status, "message": result.Message,
	})
}

func (s *Server) pullKnowledgeVaultGit(writer http.ResponseWriter, request *http.Request) {
	s.runKnowledgeVaultGitOperation(writer, request, "knowledge_vault.git_pull", func(git *knowledgevault.Git) (knowledgevault.GitOpResult, error) {
		return git.Pull(request.Context())
	})
}

func (s *Server) pushKnowledgeVaultGit(writer http.ResponseWriter, request *http.Request) {
	s.runKnowledgeVaultGitOperation(writer, request, "knowledge_vault.git_push", func(git *knowledgevault.Git) (knowledgevault.GitOpResult, error) {
		return git.Push(request.Context())
	})
}

func (s *Server) syncKnowledgeVaultGit(writer http.ResponseWriter, request *http.Request) {
	s.runKnowledgeVaultGitOperation(writer, request, "knowledge_vault.git_sync", func(git *knowledgevault.Git) (knowledgevault.GitOpResult, error) {
		return git.Sync(request.Context())
	})
}

func (s *Server) runKnowledgeVaultGitOperation(writer http.ResponseWriter, request *http.Request, action string, run func(*knowledgevault.Git) (knowledgevault.GitOpResult, error)) {
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	result, err := run(s.knowledgeVault.Git())
	if err != nil {
		s.writeKnowledgeVaultGitError(writer, err)
		return
	}
	s.audit(request, action, "knowledge_vault_git", "vault", map[string]any{
		"branch": result.Status.Branch, "ahead": result.Status.Ahead, "behind": result.Status.Behind,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "git": result.Status, "message": result.Message,
	})
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
