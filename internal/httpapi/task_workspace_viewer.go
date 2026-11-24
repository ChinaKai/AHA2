package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"

	"github.com/ChinaKai/AHA2/internal/app"
)

func workspaceViewerMethodNotAllowed(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Allow", "GET, HEAD")
	writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
}

func (s *Server) taskWorkspaceView(writer http.ResponseWriter, request *http.Request) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || query.Has("root") {
		writeError(writer, http.StatusBadRequest, "workspace_viewer_invalid_path")
		return
	}
	if len(query["branch"]) > 1 {
		writeError(writer, http.StatusBadRequest, app.ErrViewerBranch.Error())
		return
	}
	value, err := s.app.ReadTaskWorkspace(request.Context(), request.PathValue("id"), app.WorkspaceViewerRequest{
		View: request.PathValue("view"), Path: query.Get("path"), Scope: query.Get("scope"),
		Base: query.Get("base"), Commit: query.Get("commit"), Repo: query.Get("repo"), Branch: query.Get("branch"), Summary: query.Get("summary") == "1",
		Availability: query.Get("availability") == "1", IgnoreWhitespace: query.Get("ignore_whitespace") == "1",
	})
	if err == nil {
		writeJSON(writer, http.StatusOK, value)
		return
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(writer, http.StatusNotFound, "task_not_found")
	case errors.Is(err, app.ErrViewerUnavailable), errors.Is(err, app.ErrViewerMissing), errors.Is(err, app.ErrViewerRepoMissing):
		writeError(writer, http.StatusNotFound, err.Error())
	case errors.Is(err, app.ErrViewerPath), errors.Is(err, app.ErrViewerRevision), errors.Is(err, app.ErrViewerNotGit),
		errors.Is(err, app.ErrViewerRepository), errors.Is(err, app.ErrViewerBranch):
		writeError(writer, http.StatusBadRequest, err.Error())
	case errors.Is(err, app.ErrViewerTransport), errors.Is(err, app.ErrViewerGit), errors.Is(err, app.ErrViewerRepoScan):
		writeError(writer, http.StatusServiceUnavailable, err.Error())
	default:
		writeError(writer, http.StatusInternalServerError, "workspace_viewer_failed")
	}
}
