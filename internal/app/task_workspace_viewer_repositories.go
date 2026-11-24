package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Keep the limits and repository discovery rules in sync with the POSIX worker.
const (
	viewerRepoLimit     = 64
	viewerRepoDepth     = 2 // Repository boundaries below the workspace root.
	viewerRepoScanLimit = 512
	viewerRepoPathDepth = 12
	viewerGitlinkLimit  = 8 << 20
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
	Items     []viewerRepository `json:"repositories,omitempty"`
	Truncated bool               `json:"repositoriesTruncated,omitempty"`
}

func validViewerRepositoryPath(rel string) bool {
	return validViewerPath(rel, false) && !viewerSensitivePath(rel)
}

func readViewerRepositoryState(ctx context.Context, source viewerFS, task domain.Task, name string,
	req WorkspaceViewerRequest, repos viewerRepositories, selected viewerRepository,
) (viewerState, error) {
	var state viewerState
	if req.Repo != "" && !selected.Readable {
		state = viewerState{Name: name, TaskTitle: task.Title, TaskCode: task.Code, RepositoryUnavailable: true}
	} else {
		var err error
		state, err = readViewerState(ctx, source, task, name, req)
		if err != nil {
			return state, err
		}
	}
	state.viewerRepositories = repos
	if len(repos.Items) > 0 {
		state.Items[0].Name = name
		// Polling also detects repository additions, removals and initialization.
		metadata, err := json.Marshal(repos)
		if err != nil {
			return state, err
		}
		state.Version = fmt.Sprintf("%x", sha256.Sum256(append([]byte(state.Version), metadata...)))
	}
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
	if _, readable := source.checkRepositoryMarker(ctx, &repo, rel); !readable {
		repo.root.Close()
		return nil, nil, ErrViewerRepoMissing
	}
	return repo, func() { repo.root.Close() }, nil
}

func (source nativeViewerFS) checkRepositoryMarker(ctx context.Context, repo *nativeViewerFS, rel string) (initialized, readable bool) {
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
	// An absorbed submodule can live under either the shared Git directory
	// or the linked worktree's own Git directory.
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	git := viewerGit{source: source}
	for _, flag := range []string{"--git-common-dir", "--git-dir"} {
		out, err := git.checked(probe, 4096, "rev-parse", "--path-format=absolute", flag)
		if err != nil || out.truncated {
			continue
		}
		dir := strings.TrimSuffix(string(out.data), "\n")
		if filepath.IsAbs(dir) && viewerModuleTarget(filepath.Join(dir, "modules"), target) {
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
	deadline := time.Now().Add(5 * time.Second)
	result := viewerRepositories{}
	scanBudget := viewerRepoScanLimit
	var visit func(nativeViewerFS, string, int)
	visit = func(repo nativeViewerFS, rel string, depth int) {
		metadata := viewerRepository{ID: rel, Path: rel}
		if rel != "" {
			metadata.Name = path.Base(rel)
		}
		markerOK := false
		if repo.root != nil {
			metadata.Initialized, markerOK = source.checkRepositoryMarker(ctx, &repo, rel)
		}
		if markerOK {
			git := viewerGit{source: repo}
			probe, cancel := context.WithTimeout(ctx, 2*time.Second)
			head, headErr := git.head(probe)
			branch, branchErr := git.run(probe, 4096, "symbolic-ref", "--quiet", "HEAD")
			cancel()
			metadata.Readable = headErr == nil && branchErr == nil && !branch.truncated && (branch.exit == 0 || branch.exit == 1)
			if metadata.Readable {
				metadata.Head = head
				metadata.Branch = strings.TrimPrefix(strings.TrimSuffix(string(branch.data), "\n"), "refs/heads/")
				metadata.Detached = branch.exit == 1 && head != ""
			}
		}
		result.Items = append(result.Items, metadata)
		if !metadata.Readable {
			return
		}

		children, truncated := repo.repositoryChildren(ctx, &scanBudget, deadline)
		result.Truncated = result.Truncated || truncated
		for _, child := range children {
			if depth == viewerRepoDepth || len(result.Items) == viewerRepoLimit || time.Now().After(deadline) {
				result.Truncated = true
				break
			}
			fullPath := path.Join(rel, child)
			if !validViewerRepositoryPath(fullPath) {
				continue
			}
			childRepo, err := source.repositoryDir(fullPath)
			visit(childRepo, fullPath, depth+1)
			if err == nil {
				childRepo.root.Close()
			}
		}
	}
	visit(source, "", 0)
	if ctx.Err() != nil || !result.Items[0].Readable {
		return viewerRepositories{}, ErrViewerGit
	}
	return result, nil
}

func (source nativeViewerFS) repositoryChildren(ctx context.Context, scanBudget *int, deadline time.Time) ([]string, bool) {
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

	// A repository can also be checked out inside a workspace without being
	// registered as a submodule. Stop walking when we reach another Git marker.
	queue := []string{""}
	for len(queue) > 0 {
		if ctx.Err() != nil || *scanBudget == 0 || time.Now().After(deadline) {
			truncated = true
			break
		}
		*scanBudget = *scanBudget - 1
		rel := queue[0]
		queue = queue[1:]
		listing, err := source.tree(ctx, rel)
		if err != nil {
			truncated = true
			continue
		}
		truncated = truncated || listing.Truncated
		for _, entry := range listing.Entries {
			if entry.Kind != "directory" || !validViewerRepositoryPath(entry.Path) || children[entry.Path] {
				continue
			}
			info, err := source.info(path.Join(entry.Path, ".git"))
			switch {
			case err == nil:
				if info.IsDir() || info.Mode().IsRegular() {
					children[entry.Path] = true
				}
			case errors.Is(err, ErrViewerMissing):
				if strings.Count(entry.Path, "/")+1 == viewerRepoPathDepth || len(queue) >= *scanBudget {
					truncated = true
				} else {
					queue = append(queue, entry.Path)
				}
			}
		}
	}
	paths := make([]string, 0, len(children))
	for child := range children {
		paths = append(paths, child)
	}
	slices.Sort(paths)
	return paths, truncated
}
