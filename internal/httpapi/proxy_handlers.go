package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/proxyconfig"
)

func (s *Server) proxySettings(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.ProxySettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "proxy_settings_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "proxy": item})
}

func (s *Server) updateProxySettings(writer http.ResponseWriter, request *http.Request) {
	var payload domain.ProxySettings
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	item, err := proxyconfig.Normalize(payload)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_proxy", "message": err.Error()})
		return
	}
	item.UpdatedAt = time.Now().UTC()
	item, err = s.store.UpdateProxySettings(request.Context(), item)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "update_proxy_failed")
		return
	}
	s.audit(request, "proxy.update", "settings", "proxy", nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "proxy": item})
}

func (s *Server) testProxySettings(writer http.ResponseWriter, request *http.Request) {
	var payload domain.ProxySettings
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	item, err := proxyconfig.Normalize(payload)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_proxy", "message": err.Error()})
		return
	}
	client, err := proxyconfig.Client(nil, item)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_proxy", "message": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {"aha-proxy-probe"},
		"redirect_uri": {"http://localhost:1455/auth/callback"},
		"client_id":    {"app_EMoamEEZ73f0CkXaXp7hrann"}, "code_verifier": {strings.Repeat("a", 43)},
	}
	probe, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://auth.openai.com/oauth/token", strings.NewReader(form.Encode()))
	probe.Header.Set("User-Agent", "codex_cli_rs")
	probe.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	started := time.Now()
	response, err := client.Do(probe)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "proxy_test_failed", "message": err.Error(),
		})
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	if response.StatusCode == http.StatusForbidden {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "proxy_region_rejected",
			"message": "代理出口仍被 OpenAI 地区策略拒绝（HTTP 403）",
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "status_code": response.StatusCode, "elapsed_ms": time.Since(started).Milliseconds(),
	})
}
