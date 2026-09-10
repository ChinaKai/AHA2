package httpapi

import (
	"net/http"
	"runtime"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

func (s *Server) systemInfo(writer http.ResponseWriter, request *http.Request) {
	s.systemMu.Lock()
	if s.systemCache != nil && time.Since(s.systemCachedAt) < 30*time.Second {
		cached := s.systemCache
		s.systemMu.Unlock()
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "system": cached})
		return
	}
	s.systemMu.Unlock()
	available := runtime.GOOS == "windows" && workspace.WSLExecutableFound()
	distros := []string{}
	if available {
		if list := workspace.WSLDistros(); list != nil {
			distros = list
		}
	}
	snapshot := map[string]any{
		"os": runtime.GOOS, "arch": runtime.GOARCH,
		"wsl_available": available, "wsl_distros": distros,
		"version": s.version, "started_at": s.startedAt,
	}
	s.systemMu.Lock()
	s.systemCache = snapshot
	s.systemCachedAt = time.Now()
	s.systemMu.Unlock()
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "system": snapshot})
}
