package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Keep the discovery budgets and rules in sync with the POSIX worker.
const (
	viewerRepoTimeout       = 5 * time.Second
	viewerRepoMetadataLimit = 8 << 20
	viewerGitlinkLimit      = 8 << 20
)

type viewerRepository struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Head        string `json:"head"`
	Branch      string `json:"branch"`
	Detached    bool   `json:"detached"`
	Initialized bool   `json:"initialized"`
	Readable    bool   `json:"readable"`
}

type viewerRepositories struct {
	Items           []viewerRepository `json:"repositories,omitempty"`
	Truncated       bool               `json:"repositoriesTruncated,omitempty"`
	DeadlineReached bool               `json:"repositoryDeadlineReached,omitempty"`
	SizeReached     bool               `json:"repositorySizeReached,omitempty"`
}

func validViewerRepositoryPath(rel string) bool {
	return validViewerPath(rel, false) && !viewerSensitivePath(rel)
}

func readViewerRepositoryState(ctx context.Context, source viewerFS, task domain.Task, name, repositoryName string,
	req WorkspaceViewerRequest, repos, changeRepos viewerRepositories, selected viewerRepository, worktrees []viewerWorktree,
) (viewerState, error) {
	var state viewerState
	if req.Repo != "" && !selected.Readable {
		if req.Branch != "" {
			return state, ErrViewerRepoMissing
		}
		state = viewerState{Name: name, TaskTitle: task.Title, TaskCode: task.Code, RepositoryUnavailable: true}
	} else {
		var err error
		state, err = readViewerState(ctx, source, task, name, req, changeRepos)
		if err != nil {
			return state, err
		}
	}
	state.viewerRepositories = repos
	state.Worktrees = worktrees
	if len(repos.Items) > 0 {
		if repositoryName != "" && repositoryName != "." && repositoryName != string(filepath.Separator) {
			state.Items[0].Name = repositoryName
		}
	}
	// Polling also detects checkout additions, removals and initialization.
	metadata, err := json.Marshal([]any{repos, worktrees})
	if err != nil {
		return state, err
	}
	state.Version = fmt.Sprintf("%x", sha256.Sum256(append([]byte(state.Version), metadata...)))
	if req.Summary {
		return viewerState{IsGit: state.IsGit, Version: state.Version}, nil
	}
	return state, nil
}

func (source nativeViewerFS) openRepository(ctx context.Context, rel string) (viewerFS, func(), error) {
	repo, err := source.repositoryDir(rel)
	if err != nil {
		return nil, nil, err
	}
	if _, readable := source.checkRepositoryMarker(ctx, &repo, rel, func() []string { return source.moduleDirs(ctx) }); !readable {
		repo.root.Close()
		return nil, nil, ErrViewerRepoMissing
	}
	return repo, func() { repo.root.Close() }, nil
}

func (source nativeViewerFS) checkRepositoryMarker(ctx context.Context, repo *nativeViewerFS, rel string, moduleDirs func() []string) (initialized, readable bool) {
	info, err := repo.root.Lstat(".git")
	if err != nil || !(info.IsDir() || info.Mode().IsRegular()) {
		return false, false
	}
	if rel == "" || info.IsDir() {
		return true, true
	}
	content, err := repo.file(ctx, ".git")
	raw, ok := strings.CutPrefix(strings.TrimRight(content.Text, "\r\n"), "gitdir: ")
	if err != nil || !ok || raw == "" || len(raw) > 4096 || strings.ContainsAny(raw, "\r\n\x00") {
		return true, false
	}
	target := raw
	if !filepath.IsAbs(target) {
		target = filepath.Join(repo.dir, target)
	}
	target = filepath.Clean(target)
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil || target != canonical {
		return true, false
	}
	for _, modules := range moduleDirs() {
		if viewerModuleTarget(modules, target) {
			repo.gitDir = target
			return true, true
		}
	}
	for parent := path.Dir(rel); parent != "."; parent = path.Dir(parent) {
		marker := path.Join(parent, ".git")
		if info, err := source.info(marker); err == nil && info.IsDir() &&
			viewerModuleTarget(filepath.Join(source.dir, filepath.FromSlash(marker), "modules"), target) {
			repo.gitDir = target
			return true, true
		}
	}
	return true, false
}

// An absorbed submodule can live under either the shared Git directory
// or the linked worktree's own Git directory.
func (source nativeViewerFS) moduleDirs(ctx context.Context) []string {
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	git := viewerGit{source: source}
	var dirs []string
	for _, flag := range []string{"--git-common-dir", "--git-dir"} {
		out, err := git.checked(probe, 4096, "rev-parse", "--path-format=absolute", flag)
		if err == nil && !out.truncated {
			if dir := strings.TrimSuffix(string(out.data), "\n"); filepath.IsAbs(dir) {
				dirs = append(dirs, filepath.Join(dir, "modules"))
			}
		}
	}
	return dirs
}

func viewerModuleTarget(modules, target string) bool {
	rel, err := filepath.Rel(modules, target)
	return err == nil && rel != "." && filepath.IsLocal(rel)
}

func (source nativeViewerFS) repositoryDir(rel string) (nativeViewerFS, error) {
	if !validViewerRepositoryPath(rel) {
		return nativeViewerFS{}, ErrViewerRepository
	}
	info, err := source.info(rel)
	if err != nil || !info.IsDir() {
		return nativeViewerFS{}, ErrViewerRepoMissing
	}
	dir, err := source.root.OpenRoot(filepath.FromSlash(rel))
	if err != nil {
		return nativeViewerFS{}, ErrViewerRepoMissing
	}
	actual, err := dir.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		dir.Close()
		return nativeViewerFS{}, ErrViewerRepoMissing
	}
	return nativeViewerFS{root: dir, dir: filepath.Join(source.dir, filepath.FromSlash(rel))}, nil
}

func (source nativeViewerFS) repositories(ctx context.Context) (viewerRepositories, error) {
	marker, err := source.gitMarker(ctx)
	if !marker || err != nil {
		return viewerRepositories{}, err
	}
	scan, cancel := context.WithTimeout(ctx, viewerRepoTimeout)
	defer cancel()
	var result viewerRepositories
	moduleDirs := sync.OnceValue(func() []string { return source.moduleDirs(scan) })
	size := 0
	pending := []string{""}
	seen := map[string]bool{}
	for len(pending) > 0 && scan.Err() == nil {
		rel := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[rel] {
			continue
		}
		seen[rel] = true
		repo := source
		if rel != "" {
			repo, _ = source.repositoryDir(rel)
		}
		item, children, truncated := source.repositoryDetails(scan, repo, rel, moduleDirs)
		if rel != "" && repo.root != nil {
			repo.root.Close()
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return viewerRepositories{}, err
		}
		size += len(encoded) + 1
		if size > viewerRepoMetadataLimit {
			result.SizeReached = true
			break
		}
		result.Items = append(result.Items, item)
		result.Truncated = result.Truncated || truncated
		for _, child := range slices.Backward(children) {
			if fullPath := path.Join(rel, child); validViewerRepositoryPath(fullPath) {
				pending = append(pending, fullPath)
			}
		}
	}
	result.DeadlineReached = scan.Err() != nil
	result.Truncated = result.Truncated || result.DeadlineReached || result.SizeReached
	if ctx.Err() != nil || len(result.Items) == 0 || !result.Items[0].Readable {
		return viewerRepositories{}, ErrViewerGit
	}
	return result, nil
}

func (source nativeViewerFS) repositoryDetails(ctx context.Context, repo nativeViewerFS, rel string, moduleDirs func() []string) (viewerRepository, []string, bool) {
	item := viewerRepository{ID: rel, Path: rel, Name: path.Base(rel)}
	if rel == "" {
		item.Name = filepath.Base(source.dir)
	}
	if repo.root == nil {
		return item, nil, false
	}
	var markerOK bool
	item.Initialized, markerOK = source.checkRepositoryMarker(ctx, &repo, rel, moduleDirs)
	if !markerOK {
		return item, nil, false
	}
	git := viewerGit{source: repo}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	head, headErr := git.head(probe)
	branch, branchErr := git.run(probe, 4096, "symbolic-ref", "--quiet", "HEAD")
	cancel()
	item.Readable = headErr == nil && branchErr == nil && !branch.truncated && (branch.exit == 0 || branch.exit == 1)
	if !item.Readable {
		return item, nil, false
	}
	item.Head = head
	item.Branch = strings.TrimPrefix(strings.TrimSuffix(string(branch.data), "\n"), "refs/heads/")
	item.Detached = branch.exit == 1 && head != ""
	children, truncated := repo.registeredGitlinks(ctx)
	return item, children, truncated
}

// Discovery follows the Git index at each repository boundary. Ordinary
// directories and unregistered nested checkouts are not discovery inputs.
func (source nativeViewerFS) registeredGitlinks(ctx context.Context) ([]string, bool) {
	children := map[string]bool{}
	git := viewerGit{source: source}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	index, err := git.checked(probe, viewerGitlinkLimit, "ls-files", "--stage", "-z", "--")
	cancel()
	truncated := err != nil || index.truncated
	for _, record := range index.records() {
		if header, rel, ok := strings.Cut(record, "\t"); ok && strings.HasPrefix(header, "160000 ") && validViewerRepositoryPath(rel) {
			children[rel] = true
		}
	}

	paths := make([]string, 0, len(children))
	for child := range children {
		paths = append(paths, child)
	}
	slices.Sort(paths)
	return paths, truncated
}
