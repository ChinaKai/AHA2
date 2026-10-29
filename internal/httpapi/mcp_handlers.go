package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Server) listMCPServers(writer http.ResponseWriter, request *http.Request) {
	items, err := s.app.MCPServers(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "mcp_list_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "servers": items})
}

func (s *Server) saveMCPServer(writer http.ResponseWriter, request *http.Request) {
	var item domain.MCPServer
	if err := json.NewDecoder(request.Body).Decode(&item); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	saved, err := s.app.SaveMCPServer(request.Context(), item)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "mcp_save_failed", "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "server": saved})
}

func (s *Server) deleteMCPServer(writer http.ResponseWriter, request *http.Request) {
	if err := s.app.DeleteMCPServer(request.Context(), request.PathValue("id")); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "mcp_delete_failed", "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) setTaskMCPServers(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		ServerIDs []string `json:"server_ids"`
	}
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	task, err := s.app.SetTaskMCPServers(request.Context(), request.PathValue("id"), payload.ServerIDs)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "task_mcp_failed", "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "task": task})
}

func (s *Server) testMCPServer(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(request.Body).Decode(&payload)
	item, err := s.app.TestMCPServer(request.Context(), payload.ID)
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{"ok": false, "message": err.Error(), "server": item})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "server": item})
}

func (s *Server) mcpServerDetail(writer http.ResponseWriter, request *http.Request) {
	item, err := s.app.MCPServerDetail(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "mcp_not_found")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "server": item})
}
