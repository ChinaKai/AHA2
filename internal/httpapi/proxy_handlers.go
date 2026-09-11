package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/outboundproxy"
	"github.com/ChinaKai/AHA2/internal/proxyconfig"
)

func (s *Server) proxySettings(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.ProxySettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "proxy_settings_failed")
		return
	}
	response := map[string]any{"ok": true, "proxy": item}
	if s.outboundProxy != nil {
		response["managed"] = s.outboundProxy.View(request.Context())
	}
	writeJSON(writer, http.StatusOK, response)
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
	if s.outboundProxy != nil {
		item, err = s.outboundProxy.UpdateSettings(request.Context(), item)
	} else {
		item, err = s.store.UpdateProxySettings(request.Context(), item)
	}
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "update_proxy_failed", "message": err.Error()})
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
	var client *http.Client
	if item.Mode == "managed_hysteria2" {
		if s.outboundProxy == nil {
			writeError(writer, http.StatusServiceUnavailable, "managed_proxy_unavailable")
			return
		}
		client, err = s.outboundProxy.ClientFor(request.Context(), nil, item)
	} else {
		client, err = proxyconfig.Client(nil, item)
	}
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_proxy", "message": err.Error()})
		return
	}
	statusCode, elapsed, err := runProxyProbe(request.Context(), client)
	if err != nil {
		message := err.Error()
		if item.Mode == "managed_hysteria2" {
			message = "managed proxy connection failed"
		}
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "proxy_test_failed", "message": message,
		})
		return
	}
	if statusCode == http.StatusForbidden {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "proxy_region_rejected",
			"message": "代理出口仍被 OpenAI 地区策略拒绝（HTTP 403）",
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "status_code": statusCode, "elapsed_ms": elapsed,
	})
}

func runProxyProbe(ctx context.Context, client *http.Client) (int, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
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
		return 0, time.Since(started).Milliseconds(), err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, time.Since(started).Milliseconds(), nil
}

func (s *Server) testProxyNode(writer http.ResponseWriter, request *http.Request) {
	if s.outboundProxy == nil {
		writeError(writer, http.StatusServiceUnavailable, "managed_proxy_unavailable")
		return
	}
	client, cleanup, err := s.outboundProxy.ClientForNode(
		request.Context(), nil, request.PathValue("id"), request.PathValue("node_id"),
	)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_proxy_node", "message": "代理节点不可用"})
		return
	}
	defer cleanup()
	statusCode, elapsed, err := runProxyProbe(request.Context(), client)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "proxy_node_test_failed", "message": "代理节点连接失败"})
		return
	}
	if statusCode == http.StatusForbidden {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "proxy_region_rejected", "message": "代理出口被 OpenAI 地区策略拒绝（HTTP 403）"})
		return
	}
	s.audit(request, "proxy.node.test", "settings", request.PathValue("id"), map[string]any{"status_code": statusCode, "elapsed_ms": elapsed})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "status_code": statusCode, "elapsed_ms": elapsed})
}

func (s *Server) importProxySubscription(writer http.ResponseWriter, request *http.Request) {
	if s.outboundProxy == nil {
		writeError(writer, http.StatusServiceUnavailable, "managed_proxy_unavailable")
		return
	}
	var payload struct {
		ProfileID           string `json:"profile_id"`
		Name                string `json:"name"`
		SubscriptionURL     string `json:"subscription_url"`
		SubscriptionYAML    string `json:"subscription_yaml"`
		SelectedNodeID      string `json:"selected_node_id"`
		RefreshIntervalMins int    `json:"refresh_interval_minutes"`
	}
	if err := decodeJSONLimit(request, &payload, 5<<20); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	item, err := s.outboundProxy.Import(request.Context(), outboundproxy.ImportInput{
		ProfileID: payload.ProfileID, Name: payload.Name,
		Content: payload.SubscriptionYAML, URL: payload.SubscriptionURL,
		SelectedNodeID: payload.SelectedNodeID, RefreshIntervalMinutes: payload.RefreshIntervalMins,
	})
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_subscription", "message": err.Error()})
		return
	}
	s.audit(request, "proxy.subscription.import", "settings", "proxy", nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "proxy": item, "managed": s.outboundProxy.View(request.Context())})
}

func (s *Server) refreshProxyProfile(writer http.ResponseWriter, request *http.Request) {
	if s.outboundProxy == nil {
		writeError(writer, http.StatusServiceUnavailable, "managed_proxy_unavailable")
		return
	}
	profileID := request.PathValue("id")
	if err := s.outboundProxy.RefreshProfile(request.Context(), profileID); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "subscription_refresh_failed", "message": err.Error()})
		return
	}
	item, err := s.store.ProxySettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "proxy_settings_failed")
		return
	}
	s.audit(request, "proxy.profile.refresh", "settings", profileID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "proxy": item, "managed": s.outboundProxy.View(request.Context())})
}

func (s *Server) patchProxyProfile(writer http.ResponseWriter, request *http.Request) {
	if s.outboundProxy == nil {
		writeError(writer, http.StatusServiceUnavailable, "managed_proxy_unavailable")
		return
	}
	var payload struct {
		Name                   *string `json:"name"`
		SelectedNodeID         *string `json:"selected_node_id"`
		RefreshIntervalMinutes *int    `json:"refresh_interval_minutes"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	profileID := request.PathValue("id")
	item, err := s.outboundProxy.PatchProfile(request.Context(), profileID, outboundproxy.ProfilePatch{
		Name: payload.Name, SelectedNodeID: payload.SelectedNodeID, RefreshIntervalMinutes: payload.RefreshIntervalMinutes,
	})
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "update_proxy_profile_failed", "message": err.Error()})
		return
	}
	s.audit(request, "proxy.profile.update", "settings", profileID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "proxy": item, "managed": s.outboundProxy.View(request.Context())})
}

func (s *Server) activateProxyProfile(writer http.ResponseWriter, request *http.Request) {
	if s.outboundProxy == nil {
		writeError(writer, http.StatusServiceUnavailable, "managed_proxy_unavailable")
		return
	}
	profileID := request.PathValue("id")
	item, err := s.outboundProxy.ActivateProfile(request.Context(), profileID)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "activate_proxy_profile_failed", "message": err.Error()})
		return
	}
	s.audit(request, "proxy.profile.activate", "settings", profileID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "proxy": item, "managed": s.outboundProxy.View(request.Context())})
}

func (s *Server) deleteProxyProfile(writer http.ResponseWriter, request *http.Request) {
	if s.outboundProxy == nil {
		writeError(writer, http.StatusServiceUnavailable, "managed_proxy_unavailable")
		return
	}
	profileID := request.PathValue("id")
	item, err := s.outboundProxy.DeleteProfile(request.Context(), profileID)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "delete_proxy_profile_failed", "message": err.Error()})
		return
	}
	s.audit(request, "proxy.profile.delete", "settings", profileID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "proxy": item, "managed": s.outboundProxy.View(request.Context())})
}

func (s *Server) refreshProxySubscription(writer http.ResponseWriter, request *http.Request) {
	if s.outboundProxy == nil {
		writeError(writer, http.StatusServiceUnavailable, "managed_proxy_unavailable")
		return
	}
	if err := s.outboundProxy.Refresh(request.Context()); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "subscription_refresh_failed", "message": err.Error()})
		return
	}
	item, err := s.store.ProxySettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "proxy_settings_failed")
		return
	}
	s.audit(request, "proxy.subscription.refresh", "settings", "proxy", nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "proxy": item, "managed": s.outboundProxy.View(request.Context())})
}
