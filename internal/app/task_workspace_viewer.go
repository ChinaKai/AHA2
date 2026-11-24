package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

const (
	viewerFileLimit = 1 << 20
	viewerDiffLimit = 2 << 20
	viewerLineLimit = 5000
	viewerListLimit = 5000
)

var (
	ErrViewerUnavailable = errors.New("workspace_viewer_unavailable")
	ErrViewerTransport   = errors.New("workspace_viewer_transport_unsupported")
	ErrViewerPath        = errors.New("workspace_viewer_invalid_path")
	ErrViewerMissing     = errors.New("workspace_viewer_path_not_found")
	ErrViewerGit         = errors.New("workspace_viewer_git_failed")
	ErrViewerNotGit      = errors.New("workspace_viewer_not_git")
	ErrViewerRevision    = errors.New("workspace_viewer_invalid_revision")
	ErrViewerBranch      = errors.New("workspace_viewer_invalid_branch")
	ErrViewerRepository  = errors.New("workspace_viewer_invalid_repository")
	ErrViewerRepoScan    = errors.New("workspace_viewer_repository_discovery_incomplete")
	ErrViewerRepoMissing = errors.New("workspace_viewer_repository_unavailable")
)

type WorkspaceViewerRequest struct {
	View, Path, Scope, Base, Commit, Repo, Branch string
	Summary, Availability, IgnoreWhitespace       bool
}

type viewerEntry struct {
	Path     string    `json:"path"`
	Kind     string    `json:"kind"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"mtime"`
	Masked   bool      `json:"masked,omitempty"`
}

type viewerTree struct {
	Entries   []viewerEntry `json:"entries"`
	Truncated bool          `json:"truncated"`
}

type viewerContent struct {
	Text        string `json:"text"`
	Placeholder string `json:"placeholder,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

type viewerFS interface {
	available(context.Context) error
	gitMarker(context.Context) (bool, error)
	tree(context.Context, string) (viewerTree, error)
	file(context.Context, string) (viewerContent, error)
	git(ctx context.Context, limit int, readLineEndings bool, args ...string) (viewerGitOutput, error)
	repositories(context.Context) (viewerRepositories, error)
	openRepository(context.Context, string) (viewerFS, func(), error)
}

type WorkspaceViewerTargetOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Service) WorkspaceViewerTargets(ctx context.Context, taskID string) ([]WorkspaceViewerTargetOption, error) {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	items, err := s.store.ListWorkspaces(ctx, task.ProjectID)
	if err != nil {
		return nil, err
	}
	options := make([]WorkspaceViewerTargetOption, 0, len(items))
	for _, item := range items {
		id := item.ID
		if id == task.WorkspaceID {
			id = ""
		}
		options = append(options, WorkspaceViewerTargetOption{ID: id, Name: item.Name})
	}
	return options, nil
}

func (s *Service) viewerWorkspace(ctx context.Context, taskID, workspaceID string) (domain.Task, domain.Workspace, string, error) {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.Task{}, domain.Workspace{}, "", err
	}
	useTaskRoot := workspaceID == "" || workspaceID == task.WorkspaceID
	if useTaskRoot {
		workspaceID = task.WorkspaceID
	}
	item, err := s.store.Workspace(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Task{}, domain.Workspace{}, "", ErrViewerUnavailable
	}
	if err != nil {
		return domain.Task{}, domain.Workspace{}, "", err
	}
	if item.ProjectID != task.ProjectID {
		return domain.Task{}, domain.Workspace{}, "", ErrViewerUnavailable
	}
	for _, lookup := range []struct {
		id   string
		find func(context.Context, string) (domain.ChannelInstance, error)
	}{{task.ID, s.store.RetiredChannelInstanceForTask}, {item.ID, s.store.RetiredChannelInstanceForWorkspace}} {
		if _, err := lookup.find(ctx, lookup.id); err == nil {
			return domain.Task{}, domain.Workspace{}, "", ErrViewerUnavailable
		} else if !errors.Is(err, sql.ErrNoRows) {
			return domain.Task{}, domain.Workspace{}, "", err
		}
	}
	root := item.RootPath
	if useTaskRoot {
		// An unprepared or removed worktree cannot fall back to the parent repo.
		if task.TaskWorkspacePath != "" {
			root = task.TaskWorkspacePath
		} else if task.Isolation == "worktree" {
			return domain.Task{}, domain.Workspace{}, "", ErrViewerUnavailable
		}
	} else {
		task.BaseCommit, task.TargetBranch = "", ""
	}
	if strings.TrimSpace(root) == "" {
		return domain.Task{}, domain.Workspace{}, "", ErrViewerUnavailable
	}

	return task, item, root, nil
}

func (s *Service) ReadTaskWorkspace(ctx context.Context, taskID string, req WorkspaceViewerRequest) (any, error) {
	return s.ReadWorkspaceViewerTarget(ctx, taskID, "", req)
}

func (s *Service) ReadWorkspaceViewerTarget(ctx context.Context, taskID, workspaceID string, req WorkspaceViewerRequest) (any, error) {
	task, item, root, err := s.viewerWorkspace(ctx, taskID, workspaceID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var source viewerFS
	switch {
	case item.Transport == "wsl", item.Transport == "ssh", item.Locality == "remote":
		if workspace.IsWindowsWorkspace(item) {
			return nil, ErrViewerTransport
		}
		if item.SSHCredentialRef != "" && s.secrets != nil {
			item.SSHPassword, _ = s.secrets.Get(item.SSHCredentialRef)
		}
		if !path.IsAbs(root) {
			return nil, ErrViewerUnavailable
		}
		source = remoteViewerFS{root: root, runner: workspace.RunnerFor(item)}
	default:
		if !filepath.IsAbs(root) {
			return nil, ErrViewerUnavailable
		}
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, ErrViewerUnavailable
		}
		dir, err := os.OpenRoot(canonical)
		if err != nil {
			return nil, ErrViewerUnavailable
		}
		defer dir.Close()
		source = nativeViewerFS{root: dir, dir: canonical}
	}
	// The task checkout can be named after its worktree. Use the original
	// repository directory for the root selector even when the workspace has an alias.
	return readWorkspaceView(ctx, source, task, item.Name, filepath.Base(item.RootPath), req)
}

func readWorkspaceView(ctx context.Context, source viewerFS, task domain.Task, name, repositoryName string, req WorkspaceViewerRequest) (any, error) {
	if req.Branch != "" && (!validViewerBranchRef(req.Branch) || (req.View != "state" && req.View != "diff") ||
		req.Availability || req.Scope == "uncommitted") {
		return nil, ErrViewerBranch
	}
	switch req.View {
	case "state":
		if req.Availability && req.Repo == "" {
			return map[string]bool{"available": true}, source.available(ctx)
		}
	case "tree", "file", "diff":
		if !validViewerPath(req.Path, req.View == "tree") {
			return nil, ErrViewerPath
		}
	default:
		return nil, ErrViewerPath
	}

	var repos viewerRepositories
	var selected viewerRepository
	if req.Repo != "" && !validViewerRepositoryPath(req.Repo) {
		return nil, ErrViewerRepository
	}
	if req.View == "state" || req.Repo != "" {
		var err error
		repos, err = source.repositories(ctx)
		if err != nil {
			return nil, err
		}
	}
	if req.Repo != "" {
		index := slices.IndexFunc(repos.Items, func(repo viewerRepository) bool { return repo.ID == req.Repo })
		if index < 0 {
			if repos.Truncated {
				return nil, ErrViewerRepoScan
			}
			return nil, ErrViewerRepository
		}
		selected = repos.Items[index]
		if selected.Readable {
			repo, closeRepo, err := source.openRepository(ctx, selected.Path)
			if err != nil {
				return nil, err
			}
			defer closeRepo()
			source = repo
		} else if req.View != "state" || req.Availability {
			return nil, ErrViewerRepoMissing
		}
	}
	if req.View == "state" {
		if req.Availability {
			return map[string]bool{"available": true}, source.available(ctx)
		}
		return readViewerRepositoryState(ctx, source, task, name, repositoryName, req, repos, selected)
	}

	if req.View == "tree" {
		if viewerSensitivePath(req.Path) {
			return viewerTree{Entries: []viewerEntry{}}, nil
		}
		tree, err := source.tree(ctx, req.Path)
		if err != nil {
			return nil, err
		}
		entries := make([]viewerEntry, 0, len(tree.Entries))
		for _, entry := range tree.Entries {
			if !validViewerPath(entry.Path, false) {
				continue
			}
			entry.Masked = viewerSensitivePath(entry.Path)
			entries = append(entries, entry)
		}
		slices.SortFunc(entries, func(a, b viewerEntry) int {
			if (a.Kind == "directory") != (b.Kind == "directory") {
				if a.Kind == "directory" {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Path, b.Path)
		})
		tree.Entries = entries
		return tree, nil
	}

	if req.View == "file" {
		if viewerSensitivePath(req.Path) {
			return viewerContent{Placeholder: "masked"}, nil
		}
		return source.file(ctx, req.Path)
	}
	return readViewerDiff(ctx, source, task, req, repos)
}

func validViewerPath(value string, allowRoot bool) bool {
	if value == "" {
		return allowRoot
	}
	if len(value) > 4096 || !utf8.ValidString(value) || !fs.ValidPath(value) || strings.ContainsAny(value, "\\:\x00") {
		return false
	}
	for part := range strings.SplitSeq(value, "/") {
		// Windows normalizes trailing dots/spaces. Apply the same boundary to
		// each transport so metadata paths cannot acquire an alias.
		if part == "." || part == ".." || strings.TrimRight(part, ". ") != part {
			return false
		}
		switch strings.ToLower(part) {
		case ".git", ".aha2-context":
			return false
		}
	}
	return true
}

func viewerSensitivePath(value string) bool {
	for component := range strings.SplitSeq(strings.ToLower(value), "/") {
		switch component {
		case ".env", ".envrc", ".ssh", ".gnupg", ".aws", ".kube", ".npmrc", ".netrc", ".pypirc", ".git-credentials",
			".docker", "credentials", "credentials.json", "secrets", "secrets.json", "token", "tokens", "auth.json", "id_rsa", "id_ed25519", "id_ecdsa":
			return true
		}
		for _, prefix := range []string{".env.", "credential.", "credentials.", "secret.", "secrets.", "token.", "tokens."} {
			if strings.HasPrefix(component, prefix) {
				return true
			}
		}
		switch path.Ext(component) {
		case ".env", ".pem", ".key", ".p12", ".pfx", ".kdbx", ".jks":
			return true
		}
	}
	return false
}

type nativeViewerFS struct {
	root   *os.Root
	dir    string
	gitDir string // Validated target of a submodule's .git file.
}

func (source nativeViewerFS) available(_ context.Context) error { return nil }

func (source nativeViewerFS) gitMarker(_ context.Context) (bool, error) {
	info, err := source.root.Lstat(".git")
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil || !(info.IsDir() || info.Mode().IsRegular()) {
		return false, ErrViewerGit
	}
	return true, nil
}

func (source nativeViewerFS) info(rel string) (os.FileInfo, error) {
	if rel == "" {
		return source.root.Stat(".")
	}
	name := ""
	var info os.FileInfo
	for part := range strings.SplitSeq(rel, "/") {
		name = path.Join(name, part)
		current, err := source.root.Lstat(filepath.FromSlash(name))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrViewerMissing
		}
		if err != nil || current.Mode()&os.ModeSymlink != 0 {
			return nil, ErrViewerPath
		}
		info = current
	}
	return info, nil
}

func (source nativeViewerFS) openDirectory(rel string) (*os.File, error) {
	info, err := source.info(rel)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, ErrViewerPath
	}
	name := rel
	if name == "" {
		name = "."
	}
	dir, err := source.root.OpenRoot(filepath.FromSlash(name))
	if err != nil {
		return nil, ErrViewerPath
	}
	defer dir.Close()
	actual, err := dir.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		return nil, ErrViewerPath
	}
	handle, err := dir.Open(".")
	if err != nil {
		return nil, ErrViewerPath
	}
	return handle, nil
}

func (source nativeViewerFS) tree(_ context.Context, rel string) (viewerTree, error) {
	handle, err := source.openDirectory(rel)
	if err != nil {
		return viewerTree{}, err
	}
	defer handle.Close()
	children, err := handle.ReadDir(viewerListLimit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return viewerTree{}, ErrViewerPath
	}
	result := viewerTree{Entries: []viewerEntry{}, Truncated: len(children) > viewerListLimit}
	for _, child := range children[:min(len(children), viewerListLimit)] {
		info, err := child.Info()
		if err != nil {
			continue
		}
		kind := "other"
		switch {
		case info.IsDir():
			kind = "directory"
		case info.Mode().IsRegular():
			kind = "file"
		case info.Mode()&os.ModeSymlink != 0:
			kind = "symlink"
		}
		result.Entries = append(result.Entries, viewerEntry{Path: path.Join(rel, child.Name()), Kind: kind, Size: info.Size(), Modified: info.ModTime()})
	}
	return result, nil
}

func (source nativeViewerFS) file(_ context.Context, rel string) (viewerContent, error) {
	info, err := source.info(rel)
	if err != nil {
		return viewerContent{}, err
	}
	if !info.Mode().IsRegular() {
		return viewerContent{}, ErrViewerPath
	}
	if info.Size() > viewerFileLimit {
		return viewerContent{Placeholder: "too_large"}, nil
	}
	file, err := source.root.OpenFile(filepath.FromSlash(rel), os.O_RDONLY|viewerNonblock, 0)
	if err != nil {
		return viewerContent{}, ErrViewerPath
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return viewerContent{}, ErrViewerPath
	}
	data, err := io.ReadAll(io.LimitReader(file, viewerFileLimit+1))
	if err != nil {
		return viewerContent{}, ErrViewerPath
	}
	return viewerText(data), nil
}

func viewerText(data []byte) viewerContent {
	switch {
	case len(data) > viewerFileLimit:
		return viewerContent{Placeholder: "too_large"}
	case bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data):
		return viewerContent{Placeholder: "binary"}
	default:
		return viewerContent{Text: string(data)}
	}
}
