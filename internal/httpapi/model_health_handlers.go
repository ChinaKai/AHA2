package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/gateway"
)

// modelHealthTimeout matches the manual protocol probe budget: one outbound
// request through a configured proxy can take several seconds.
const modelHealthTimeout = 25 * time.Second

// checkModelHealth is the Owner-authenticated per-model health check. It reads
// the model's provider credential out of the Secret Store, sends one minimal
// real request on the model's configured wire protocol, and returns the
// classified account usability. The API Key never enters the response.
func (s *Server) checkModelHealth(writer http.ResponseWriter, request *http.Request) {
	model, err := s.store.Model(request.Context(), strings.TrimSpace(request.PathValue("id")))
	if err != nil {
		writeError(writer, http.StatusNotFound, "model_not_found")
		return
	}
	provider, err := s.store.Provider(request.Context(), model.ProviderID)
	if err != nil {
		writeError(writer, http.StatusNotFound, "provider_not_found")
		return
	}
	apiKey := ""
	if provider.CredentialRef != "" && s.secrets != nil {
		apiKey, _ = s.secrets.Get(provider.CredentialRef)
	}
	wireAPI := strings.TrimSpace(model.WireAPI)
	if wireAPI == "" {
		// Older records predate the wire_api field. Match the UI's default:
		// Claude backend uses Anthropic Messages, everything else Responses.
		if model.Backend == "claude" {
			wireAPI = "anthropic_messages"
		} else {
			wireAPI = "responses"
		}
	}
	health := gateway.CheckModelHealth(request.Context(), nil,
		provider.BaseURL, provider.AnthropicBaseURL, apiKey, provider.AuthStyle,
		model.WireModel, wireAPI, modelHealthTimeout)
	// The health result is evidence, not configuration: persist nothing and do
	// not disable the model. Audit only the classification and the already
	// redacted endpoint, never the response body or the key.
	auditData := map[string]any{
		"wire_api": wireAPI, "status": health.Status, "http_status": health.HTTPStatus,
		"endpoint": health.Endpoint, "retry_after_seconds": health.RetryAfterSeconds,
	}
	if health.Detail != "" {
		auditData["detail"] = health.Detail
	}
	s.audit(request, "model.health_check", "model", model.ID, auditData)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "health": health})
}
