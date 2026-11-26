package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const viewerWorktreeListLimit = 256 << 10

type viewerWorktree struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Branch      string `json:"branch,omitempty"`
	Head        string `json:"head,omitempty"`
	Main        bool   `json:"main"`
	Default     bool   `json:"default"`
	Task        bool   `json:"task"`
	Unavailable string `json:"unavailable,omitempty"`
	dir         string
}

// Filesystem-side results are internal. They can contain absolute paths on
// either the control-plane host or the workspace host.
type viewerWorktreePaths struct {
	Root    string   `json:"root"`
	Current string   `json:"current"`
	Common  string   `json:"common"`
	Status  []string `json:"status"`
}

func viewerWorktreeID(dir string) string {
	hash := sha256.Sum256([]byte(dir))
	return hex.EncodeToString(hash[:])
}

func validViewerWorktreeID(id string) bool {
	return len(id) == 64 && validViewerSHA(id)
}

func parseViewerWorktrees(data []byte) []viewerWorktree {
	var items []viewerWorktree
	for index, record := range strings.Split(string(data), "\x00\x00") {
		fields := strings.Split(record, "\x00")
		dir, ok := strings.CutPrefix(fields[0], "worktree ")
		if !ok || dir == "" || len(dir) > 4096 || !utf8.ValidString(dir) || slices.Contains(fields[1:], "bare") {
			continue
		}
		item := viewerWorktree{ID: viewerWorktreeID(dir), Name: path.Base(filepath.ToSlash(dir)), Main: index == 0, dir: dir}
		for _, field := range fields[1:] {
			if head, ok := strings.CutPrefix(field, "HEAD "); ok && validViewerSHA(head) && strings.Trim(head, "0") != "" {
				item.Head = head[:7]
			}
			if branch, ok := strings.CutPrefix(field, "branch refs/heads/"); ok {
				item.Branch = branch
			}
		}
		items = append(items, item)
	}
	return items
}

func selectViewerWorktree(ctx context.Context, source viewerFS, req WorkspaceViewerRequest) ([]viewerWorktree, viewerFS, func(), error) {
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	git := viewerGit{source: source}
	out, err := git.checked(probe, viewerWorktreeListLimit, "worktree", "list", "--porcelain", "-z")
	if err != nil || out.truncated {
		if req.Worktree != "" {
			return nil, nil, nil, ErrViewerWorktree
		}
		return nil, nil, nil, nil
	}
	items := parseViewerWorktrees(out.data)
	dirs := make([]string, len(items))
	for i, item := range items {
		dirs[i] = item.dir
	}
	check, err := source.checkWorktreePaths(probe, req.workspaceRoot, dirs)
	if err != nil || len(check.Status) != len(items) {
		if req.Worktree != "" {
			return nil, nil, nil, ErrViewerWorktreeMissing
		}
		return nil, nil, nil, nil
	}
	visible := items[:0]
	var selected *viewerWorktree
	for i, item := range items {
		status := check.Status[i]
		item.Default = item.dir == check.Current
		item.Task = item.Default && req.taskCheckout
		if status == "outside" && !item.Default {
			continue
		}
		if item.ID == req.Worktree && status != "outside" {
			selected = &item
		}
		if !item.Default {
			item.Unavailable = status
		}
		visible = append(visible, item)
	}
	if req.Worktree == "" {
		return visible, nil, nil, nil
	}
	if selected == nil {
		return nil, nil, nil, ErrViewerWorktree
	}
	if selected.Unavailable != "" {
		return nil, nil, nil, ErrViewerWorktreeMissing
	}
	if selected.Default {
		return visible, nil, nil, nil
	}
	checkout, closeCheckout, err := source.openWorktree(ctx, check, selected.dir)
	return visible, checkout, closeCheckout, err
}

func (source nativeViewerFS) checkWorktreePaths(ctx context.Context, workspaceRoot string, dirs []string) (viewerWorktreePaths, error) {
	check := viewerWorktreePaths{Current: filepath.ToSlash(source.dir), Status: make([]string, len(dirs))}
	check.Root, _ = filepath.EvalSymlinks(workspaceRoot)
	git := viewerGit{source: source}
	out, err := git.checked(ctx, 8192, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || out.truncated {
		return check, ErrViewerGit
	}
	common := filepath.Clean(strings.TrimSuffix(string(out.data), "\n"))
	if canonical, err := filepath.EvalSymlinks(common); err == nil && filepath.IsAbs(common) && canonical == common {
		check.Common = common
	}
	for i, dir := range dirs {
		if ctx.Err() != nil {
			return check, ErrViewerGit
		}
		if checkout, status := openNativeViewerWorktree(check, dir); status == "" {
			checkout.root.Close()
		} else {
			check.Status[i] = status
		}
	}
	return check, nil
}

func (source nativeViewerFS) openWorktree(_ context.Context, check viewerWorktreePaths, dir string) (viewerFS, func(), error) {
	checkout, status := openNativeViewerWorktree(check, dir)
	if status != "" {
		return nil, nil, ErrViewerWorktreeMissing
	}
	return checkout, func() { checkout.root.Close() }, nil
}

func openNativeViewerWorktree(check viewerWorktreePaths, dir string) (nativeViewerFS, string) {
	dir = filepath.FromSlash(dir)
	rel, err := filepath.Rel(check.Root, dir)
	if err != nil || !filepath.IsAbs(check.Root) || !filepath.IsAbs(dir) ||
		(rel != "." && !validViewerRepositoryPath(filepath.ToSlash(rel))) {
		return nativeViewerFS{}, "outside"
	}
	real, err := filepath.EvalSymlinks(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nativeViewerFS{}, "missing"
	}
	if err != nil {
		return nativeViewerFS{}, "unreadable"
	}
	if real != dir {
		return nativeViewerFS{}, "outside"
	}
	workspace, err := os.OpenRoot(check.Root)
	if err != nil {
		return nativeViewerFS{}, "unreadable"
	}
	defer workspace.Close()
	parent := nativeViewerFS{root: workspace, dir: check.Root}
	relative := filepath.ToSlash(rel)
	if relative == "." {
		relative = ""
	}
	info, err := parent.info(relative)
	if err != nil || !info.IsDir() {
		return nativeViewerFS{}, "unreadable"
	}
	handle, err := workspace.OpenRoot(rel)
	if err != nil {
		return nativeViewerFS{}, "unreadable"
	}
	checkout := nativeViewerFS{root: handle, dir: dir}
	actual, err := handle.Stat(".")
	onDisk, statErr := os.Stat(dir)
	real, realErr := filepath.EvalSymlinks(dir)
	if err == nil && statErr == nil && realErr == nil && real == dir && os.SameFile(info, actual) && os.SameFile(onDisk, actual) {
		checkout.gitDir = checkout.worktreeGitDir(check.Common)
		if checkout.gitDir != "" {
			return checkout, ""
		}
	}
	handle.Close()
	return nativeViewerFS{}, "unreadable"
}

func (source nativeViewerFS) worktreeGitDir(common string) string {
	if common == "" {
		return ""
	}
	marker, err := source.root.Lstat(".git")
	if err != nil {
		return ""
	}
	target := filepath.Join(source.dir, ".git")
	if !marker.IsDir() {
		content, err := source.file(context.Background(), ".git")
		raw, ok := strings.CutPrefix(strings.TrimRight(content.Text, "\r\n"), "gitdir: ")
		if err != nil || !ok {
			return ""
		}
		target = resolveViewerGitPath(source.dir, raw)
		if filepath.Dir(target) != filepath.Join(common, "worktrees") {
			return ""
		}
	}
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil || canonical != target || (marker.IsDir() && target != common) {
		return ""
	}
	dir, err := os.OpenRoot(target)
	if err != nil {
		return ""
	}
	defer dir.Close()
	admin := nativeViewerFS{root: dir}
	if !marker.IsDir() {
		for file, expected := range map[string]string{"commondir": common, "gitdir": filepath.Join(source.dir, ".git")} {
			content, err := admin.file(context.Background(), file)
			if err != nil || resolveViewerGitPath(target, strings.TrimRight(content.Text, "\r\n")) != expected {
				return ""
			}
		}
	}
	head, err := admin.file(context.Background(), "HEAD")
	if err != nil || head.Text == "" {
		return ""
	}
	return target
}

func resolveViewerGitPath(dir, value string) string {
	if value == "" || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	value = filepath.FromSlash(value)
	if !filepath.IsAbs(value) {
		value = filepath.Join(dir, value)
	}
	return filepath.Clean(value)
}
