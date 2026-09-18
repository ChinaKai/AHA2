package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// networkSettings reports the listener configuration.
//
// ListenAddress is the persisted override, empty when the launch default stands.
// StartupListenAddress is what this process was actually launched with; the two
// differ until the server is restarted, and the UI says so rather than implying a
// saved change is already in effect.
func (s *Server) networkSettings(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.NetworkSettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "network_settings_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true,
		"network": map[string]any{
			"listen_address":         item.ListenAddress,
			"startup_listen_address": s.startupListenAddress,
			"restart_required":       item.ListenAddress != "" && item.ListenAddress != s.startupListenAddress,
		},
	})
}

func (s *Server) updateNetworkSettings(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		ListenAddress string `json:"listen_address"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	// An empty value clears the override and returns to the launch default, which
	// is a meaningful choice rather than a validation failure.
	value := strings.TrimSpace(payload.ListenAddress)
	if value != "" {
		if err := store.ValidateListenAddress(value); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_listen_address")
			return
		}
	}
	item, err := s.store.UpdateNetworkSettings(request.Context(), domain.NetworkSettings{ListenAddress: value, UpdatedAt: time.Now().UTC()})
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "update_network_settings_failed")
		return
	}
	s.audit(request, "network.listen_address.update", "settings", "network", map[string]any{
		"listen_address": item.ListenAddress,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true,
		"network": map[string]any{
			"listen_address":         item.ListenAddress,
			"startup_listen_address": s.startupListenAddress,
			"restart_required":       item.ListenAddress != "" && item.ListenAddress != s.startupListenAddress,
		},
	})
}
