package httpapi

import (
	"net/http"
	"strings"

	"github.com/ChinaKai/AHA2/internal/store"
)

// offlineCompactionInfo is the read-only guide behind the data management
// dialog. AHA2 keeps serving while the owner reads it, so it deliberately does
// not expose a POST or preflight that could start a VACUUM in-process. The
// owner stops the service and runs the printed CLI command instead.
func (s *Server) offlineCompactionInfo(writer http.ResponseWriter, request *http.Request) {
	dataDir := strings.TrimSpace(s.store.DataDir())
	if dataDir == "" {
		writeError(writer, http.StatusServiceUnavailable, "data_dir_unavailable")
		return
	}
	info, err := store.OfflineCompactionInfoFor(dataDir)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "offline_compaction_info_failed", "message": err.Error(),
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "info": info})
}
