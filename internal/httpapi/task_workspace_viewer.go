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

func (s *Server) workspaceView(writer http.ResponseWriter, request *http.Request) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || query.Has("root") {
		writeError(writer, http.StatusBadRequest, "workspace_viewer_invalid_path")
		return
	}
	if len(query["branch"]) > 1 {
		writeError(writer, http.StatusBadRequest, app.ErrViewerBranch.Error())
		return
	}
	if values := query["worktree"]; len(values) > 1 || (len(values) == 1 && values[0] == "") {
		writeError(writer, http.StatusBadRequest, app.ErrViewerWorktree.Error())
		return
	}
	value, err := s.app.ReadWorkspaceViewerTarget(request.Context(), request.PathValue("id"), request.PathValue("workspace"), app.WorkspaceViewerRequest{
		View: request.PathValue("view"), Path: query.Get("path"), Scope: query.Get("scope"),
		Base: query.Get("base"), Commit: query.Get("commit"), Repo: query.Get("repo"), Branch: query.Get("branch"), Summary: query.Get("summary") == "1",
		Availability: query.Get("availability") == "1", IgnoreWhitespace: query.Get("ignore_whitespace") == "1",
		Worktree: query.Get("worktree"), BaselineCommits: query.Get("baseline_commits") == "1",
	})
	if err == nil {
		writeJSON(writer, http.StatusOK, value)
		return
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(writer, http.StatusNotFound, "task_not_found")
	case errors.Is(err, app.ErrViewerUnavailable), errors.Is(err, app.ErrViewerMissing), errors.Is(err, app.ErrViewerRepoMissing), errors.Is(err, app.ErrViewerWorktreeMissing):
		writeError(writer, http.StatusNotFound, err.Error())
	case errors.Is(err, app.ErrViewerPath), errors.Is(err, app.ErrViewerRevision), errors.Is(err, app.ErrViewerNotGit),
		errors.Is(err, app.ErrViewerRepository), errors.Is(err, app.ErrViewerBranch), errors.Is(err, app.ErrViewerWorktree):
		writeError(writer, http.StatusBadRequest, err.Error())
	case errors.Is(err, app.ErrViewerTransport), errors.Is(err, app.ErrViewerGit), errors.Is(err, app.ErrViewerRepoScan):
		writeError(writer, http.StatusServiceUnavailable, err.Error())
	default:
		writeError(writer, http.StatusInternalServerError, "workspace_viewer_failed")
	}
}

func (s *Server) workspaceViewerTargets(writer http.ResponseWriter, request *http.Request) {
	options, err := s.app.WorkspaceViewerTargets(request.Context(), request.PathValue("id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(writer, http.StatusNotFound, "task_not_found")
	case err != nil:
		writeError(writer, http.StatusInternalServerError, "workspace_viewer_failed")
	default:
		writeJSON(writer, http.StatusOK, map[string]any{"workspaces": options})
	}
}
