package httpapi

import (
	"net/http"
	"strings"
)

func (s *Server) listPetDiary(writer http.ResponseWriter, request *http.Request) {
	items, err := s.app.PetDiary(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pet_diary_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "entries": items})
}

func (s *Server) petWorkbench(writer http.ResponseWriter, request *http.Request) {
	view, err := s.app.PetWorkbench(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pet_workbench_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "diary": view.Diary, "core": view.Core, "worklog": view.Worklog,
		"approvals": view.Approvals, "task": view.Task, "updated_at": view.UpdatedAt,
	})
}

func (s *Server) savePetWorkbenchDocument(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	section := request.PathValue("section")
	name := request.PathValue("name")
	if err := s.app.PetSaveWorkbenchDocument(request.Context(), section, name, input.Content); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "save_pet_document_failed", "message": err.Error()})
		return
	}
	s.audit(request, "pet.workbench.document.save", "pet_workspace", section+"/"+name, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) resolvePetApproval(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Decision       string `json:"decision"`
		Note           string `json:"note"`
		DecisionReason string `json:"decision_reason"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	reason := strings.TrimSpace(input.DecisionReason)
	if reason == "" {
		reason = input.Note
	}
	item, err := s.app.ResolvePetApproval(request.Context(), request.PathValue("id"), input.Decision, reason)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "已经处理过") {
			status = http.StatusConflict
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": "pet_approval_resolve_failed", "message": err.Error()})
		return
	}
	s.audit(request, "pet.approval.resolve", "pet_approval", item.ID, map[string]any{"status": item.Status})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "approval": item})
}
