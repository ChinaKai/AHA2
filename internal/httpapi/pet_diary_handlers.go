package httpapi

import "net/http"

func (s *Server) listPetDiary(writer http.ResponseWriter, request *http.Request) {
	items, err := s.app.PetDiary(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pet_diary_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "entries": items})
}
