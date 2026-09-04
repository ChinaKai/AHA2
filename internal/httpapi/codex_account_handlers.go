package httpapi

import (
	"net/http"
)

func (s *Server) listCodexAccounts(writer http.ResponseWriter, request *http.Request) {
	if s.codexAccounts == nil {
		writeError(writer, http.StatusServiceUnavailable, "codex_account_runtime_unavailable")
		return
	}
	items, err := s.codexAccounts.List(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_codex_accounts_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "accounts": items})
}

func (s *Server) importLocalCodexAccount(writer http.ResponseWriter, request *http.Request) {
	if s.codexAccounts == nil {
		writeError(writer, http.StatusServiceUnavailable, "codex_account_runtime_unavailable")
		return
	}
	account, err := s.codexAccounts.ImportLocal(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "codex_import_failed", "message": err.Error()})
		return
	}
	s.audit(request, "codex_account.import_local", "codex_account", account.ID, nil)
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "account": account})
}

func (s *Server) importCodexAccount(writer http.ResponseWriter, request *http.Request) {
	if s.codexAccounts == nil {
		writeError(writer, http.StatusServiceUnavailable, "codex_account_runtime_unavailable")
		return
	}
	var payload struct {
		Label    string `json:"label"`
		AuthJSON string `json:"auth_json"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	account, err := s.codexAccounts.Import(request.Context(), []byte(payload.AuthJSON), payload.Label)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "codex_import_failed", "message": err.Error()})
		return
	}
	s.audit(request, "codex_account.import", "codex_account", account.ID, nil)
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "account": account})
}

func (s *Server) startCodexAccountLogin(writer http.ResponseWriter, request *http.Request) {
	if s.codexAccounts == nil {
		writeError(writer, http.StatusServiceUnavailable, "codex_account_runtime_unavailable")
		return
	}
	var payload struct {
		ProxyEnabled bool `json:"proxy_enabled"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	login, err := s.codexAccounts.StartOAuthLogin(payload.ProxyEnabled)
	if err != nil {
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "codex_login_start_failed", "message": err.Error()})
		return
	}
	s.audit(request, "codex_account.login_start", "codex_login", login.ID, map[string]any{"proxy_enabled": payload.ProxyEnabled})
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "login": login})
}

func (s *Server) submitCodexAccountCallback(writer http.ResponseWriter, request *http.Request) {
	if s.codexAccounts == nil {
		writeError(writer, http.StatusServiceUnavailable, "codex_account_runtime_unavailable")
		return
	}
	var payload struct {
		CallbackURL string `json:"callback_url"`
		Label       string `json:"label"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	login, err := s.codexAccounts.SubmitCallback(request.Context(), request.PathValue("id"), payload.CallbackURL, payload.Label)
	if err != nil {
		s.audit(request, "codex_account.login_failed", "codex_login", request.PathValue("id"), map[string]any{"message": err.Error()})
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "codex_callback_failed", "message": err.Error()})
		return
	}
	accountID := ""
	if login.Account != nil {
		accountID = login.Account.ID
	}
	s.audit(request, "codex_account.login_complete", "codex_account", accountID, map[string]any{"login_id": login.ID})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "login": login, "account": login.Account})
}

func (s *Server) codexAccountLoginStatus(writer http.ResponseWriter, request *http.Request) {
	if s.codexAccounts == nil {
		writeError(writer, http.StatusServiceUnavailable, "codex_account_runtime_unavailable")
		return
	}
	login, err := s.codexAccounts.Login(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "codex_login_not_found")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "login": login})
}

func (s *Server) cancelCodexAccountLogin(writer http.ResponseWriter, request *http.Request) {
	if s.codexAccounts == nil {
		writeError(writer, http.StatusServiceUnavailable, "codex_account_runtime_unavailable")
		return
	}
	if err := s.codexAccounts.CancelLogin(request.PathValue("id")); err != nil {
		writeError(writer, http.StatusNotFound, "codex_login_not_found")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) deleteCodexAccount(writer http.ResponseWriter, request *http.Request) {
	if s.codexAccounts == nil {
		writeError(writer, http.StatusServiceUnavailable, "codex_account_runtime_unavailable")
		return
	}
	id := request.PathValue("id")
	if err := s.codexAccounts.Delete(request.Context(), id); err != nil {
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "delete_codex_account_failed", "message": err.Error()})
		return
	}
	s.audit(request, "codex_account.delete", "codex_account", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) refreshCodexAccount(writer http.ResponseWriter, request *http.Request) {
	if s.codexAccounts == nil {
		writeError(writer, http.StatusServiceUnavailable, "codex_account_runtime_unavailable")
		return
	}
	result, err := s.codexAccounts.RefreshAccount(request.Context(), request.PathValue("id"))
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{
			"ok": false, "error": "codex_account_refresh_failed", "message": err.Error(),
		})
		return
	}
	s.audit(request, "codex_account.refresh", "codex_account", result.Account.ID, map[string]any{
		"warnings": len(result.Warnings),
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "account": result.Account, "warnings": result.Warnings,
	})
}
