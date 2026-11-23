package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
)

func (s *Server) onboarding(writer http.ResponseWriter, request *http.Request) {
	view, err := s.app.Onboarding(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "onboarding_load_failed")
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (s *Server) dismissOnboarding(writer http.ResponseWriter, request *http.Request) {
	if err := s.store.DismissOnboarding(request.Context(), time.Now().UTC()); err != nil {
		writeError(writer, http.StatusInternalServerError, "onboarding_dismiss_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) createOnboardingWorkspace(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Name     string  `json:"name"`
		RootPath *string `json:"root_path"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	workspace, err := s.app.CreateOnboardingWorkspace(request.Context(), payload.Name, payload.RootPath)
	if err != nil {
		if errors.Is(err, app.ErrOnboardingDirectory) {
			writeError(writer, http.StatusBadRequest, app.ErrOnboardingDirectory.Error())
		} else {
			writeError(writer, http.StatusInternalServerError, "onboarding_workspace_create_failed")
		}
		return
	}
	s.audit(request, "workspace.onboarding", "workspace", workspace.ID, map[string]any{"project_id": workspace.ProjectID})
	writeJSON(writer, http.StatusOK, map[string]any{"workspace": workspace})
}

func (s *Server) createOnboardingTask(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		WorkspaceID string `json:"workspace_id"`
		ModelID     string `json:"model_id"`
		Title       string `json:"title"`
		Request     string `json:"request"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	task, err := s.app.CreateOnboardingTask(request.Context(), payload.WorkspaceID, payload.ModelID, payload.Title, payload.Request)
	if err != nil {
		for _, reason := range []error{app.ErrOnboardingWorkspace, app.ErrOnboardingDirectory, app.ErrOnboardingModel, app.ErrOnboardingCLI, app.ErrOnboardingPermissions} {
			if errors.Is(err, reason) {
				writeError(writer, http.StatusConflict, reason.Error())
				return
			}
		}
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "onboarding_task_create_failed", "message": err.Error()})
		return
	}
	s.audit(request, "task.onboarding", "task", task.ID, map[string]any{"project_id": task.ProjectID})
	writeJSON(writer, http.StatusOK, map[string]any{"task": task})
}
