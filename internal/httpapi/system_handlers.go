package httpapi

import (
	"net/http"
	"runtime"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

func (s *Server) systemInfo(writer http.ResponseWriter, request *http.Request) {
	available := runtime.GOOS == "windows" && workspace.WSLExecutableFound()
	distros := []string{}
	if available {
		if list := workspace.WSLDistros(); list != nil {
			distros = list
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true,
		"system": map[string]any{
			"os": runtime.GOOS, "arch": runtime.GOARCH,
			"wsl_available": available, "wsl_distros": distros,
			"version": s.version, "started_at": s.startedAt,
		},
	})
}
