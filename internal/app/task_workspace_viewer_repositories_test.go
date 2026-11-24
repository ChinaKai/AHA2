package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestWorkspaceViewerRepositories(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, dir string, source viewerFS) {
		const child = "modules/application"
		const nested = child + "/sdk"
		const deep = nested + "/tools"
		makeRepo := func(rel, branch, text string) {
			folder := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(folder, 0700); err != nil {
				t.Fatal(err)
			}
			runViewerFixtureGit(t, folder, "init", "-b", branch)
			writeViewerFixture(t, folder, "shared.txt", text+"\n")
			runViewerFixtureGit(t, folder, "add", "shared.txt")
			runViewerFixtureGit(t, folder, "commit", "-m", text)
		}
		makeRepo("", "main", "parent data")
		makeRepo(child, "feature", "child data")
		makeRepo(nested, "trunk", "nested data")
		makeRepo(deep, "main", "deep repository")
		childDir := filepath.Join(dir, filepath.FromSlash(child))
		nestedDir := filepath.Join(dir, filepath.FromSlash(nested))
		runViewerFixtureGit(t, nestedDir, "checkout", "--detach")
		childHead := runViewerFixtureGit(t, childDir, "rev-parse", "HEAD")
		nestedHead := runViewerFixtureGit(t, nestedDir, "rev-parse", "HEAD")
		writeViewerFixture(t, dir, ".gitmodules", "[submodule \"application\"]\n path = "+child+"\n url = ./unused\n[submodule \"missing\"]\n path = modules/missing\n url = ./unused\n")
		runViewerFixtureGit(t, dir, "add", ".gitmodules")
		runViewerFixtureGit(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+childHead+","+child)
		runViewerFixtureGit(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+childHead+",modules/missing")
		runViewerFixtureGit(t, dir, "commit", "-m", "Add submodule pointers")
		runViewerFixtureGit(t, dir, "submodule", "absorbgitdirs", child)
		if err := os.MkdirAll(filepath.Join(dir, "modules/missing"), 0700); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		runViewerFixtureGit(t, outside, "init", "-b", "main")
		writeViewerFixture(t, outside, "shared.txt", "outside\n")
		runViewerFixtureGit(t, outside, "add", ".")
		runViewerFixtureGit(t, outside, "commit", "-m", "outside metadata")
		if err := os.Mkdir(filepath.Join(dir, "external"), 0700); err != nil {
			t.Fatal(err)
		}
		writeViewerFixture(t, dir, "external/.git", "gitdir: "+filepath.Join(outside, ".git")+"\n")
		linkAllowed := os.Symlink(outside, filepath.Join(dir, "linked")) == nil
		if linkAllowed {
			// Even a registered Git link cannot select a symlink target.
			runViewerFixtureGit(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+childHead+",linked")
		}
		if err := os.Mkdir(filepath.Join(dir, "ordinary"), 0700); err != nil {
			t.Fatal(err)
		}
		writeViewerFixture(t, childDir, "shared.txt", "working change in child\n")
		writeViewerFixture(t, childDir, ".env", "hidden\n")
		rootIndex := filepath.Join(dir, ".git", "index")
		childIndex := filepath.Join(dir, ".git", "modules", "application", "index")
		indexBefore := map[string]os.FileInfo{}
		for _, index := range []string{rootIndex, childIndex} {
			info, err := os.Stat(index)
			if err != nil {
				t.Fatal(err)
			}
			indexBefore[index] = info
		}

		task := domain.Task{Title: "task", TargetBranch: "main"}
		view := func(req WorkspaceViewerRequest) (any, error) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			return readWorkspaceView(ctx, source, task, "Workspace", "parent-repository", req)
		}
		read := func(req WorkspaceViewerRequest) any {
			t.Helper()
			value, err := view(req)
			if err != nil {
				t.Fatalf("%+v: %v", req, err)
			}
			return value
		}
		state := read(WorkspaceViewerRequest{View: "state"}).(viewerState)
		wantIDs := []string{"", "external", child, nested, deep, "modules/missing"}
		if linkAllowed {
			wantIDs = slices.Insert(wantIDs, 2, "linked")
		}
		ids := []string{}
		for _, item := range state.Items {
			ids = append(ids, item.ID)
			switch item.ID {
			case "":
				if item.Name != "parent-repository" || !item.Readable || item.Branch != "main" {
					t.Fatalf("root repository: %+v", item)
				}
			case child:
				if item.Name != "application" || item.Path != child || !item.Initialized || !item.Readable || item.Head != childHead || item.Branch != "feature" || item.Detached {
					t.Fatalf("submodule: %+v", item)
				}
			case nested:
				if !item.Readable || item.Branch != "" || !item.Detached || item.Head != nestedHead {
					t.Fatalf("nested repository: %+v", item)
				}
			case "modules/missing", "linked":
				if item.Initialized || item.Readable {
					t.Fatalf("unavailable repository: %+v", item)
				}
			case "external":
				if !item.Initialized || item.Readable || item.Head != "" {
					t.Fatalf("external Git metadata: %+v", item)
				}
			}
		}
		if !slices.Equal(ids, wantIDs) || state.viewerRepositories.Truncated {
			t.Fatalf("repository list: %+v", state.viewerRepositories)
		}
		if got := read(WorkspaceViewerRequest{View: "state", Summary: true}).(viewerState); got.Version != state.Version {
			t.Fatalf("root summary version %s, full %s", got.Version, state.Version)
		}

		state = read(WorkspaceViewerRequest{View: "state", Repo: child, Base: "main"}).(viewerState)
		if !state.IsGit || state.Head != childHead || state.Base != "HEAD" || !state.BaseFallback || !state.BaseAtHead ||
			len(state.Commits) != 1 || state.Commits[0].ID != childHead ||
			!slices.ContainsFunc(state.Uncommitted, func(c viewerChange) bool { return c.Path == "shared.txt" }) {
			t.Fatalf("child state: %+v", state)
		}
		unavailable := read(WorkspaceViewerRequest{View: "state", Repo: "modules/missing"}).(viewerState)
		if !unavailable.RepositoryUnavailable || unavailable.IsGit || len(unavailable.Commits) != 0 || len(unavailable.Items) == 0 {
			t.Fatalf("unavailable state: %+v", unavailable)
		}

		list := read(WorkspaceViewerRequest{View: "tree", Repo: child}).(viewerTree)
		if !slices.ContainsFunc(list.Entries, func(e viewerEntry) bool { return e.Path == ".env" && e.Masked }) ||
			slices.ContainsFunc(list.Entries, func(e viewerEntry) bool { return e.Path == ".git" || strings.HasPrefix(e.Path, child) }) {
			t.Fatalf("child tree: %+v", list)
		}
		for repo, want := range map[string]string{"": "parent data\n", child: "working change in child\n", nested: "nested data\n", deep: "deep repository\n"} {
			if file := read(WorkspaceViewerRequest{View: "file", Repo: repo, Path: "shared.txt"}).(viewerContent); file.Text != want {
				t.Fatalf("file in %q: %+v", repo, file)
			}
		}
		diff := read(WorkspaceViewerRequest{View: "diff", Repo: child, Path: "shared.txt", Scope: "uncommitted"}).(viewerContent)
		if !strings.Contains(diff.Text, "-child data\n+working change in child\n") {
			t.Fatalf("child working diff: %+v", diff)
		}
		diff = read(WorkspaceViewerRequest{View: "diff", Repo: nested, Path: "shared.txt", Scope: "commit", Commit: nestedHead}).(viewerContent)
		if !strings.Contains(diff.Text, "+nested data\n") {
			t.Fatalf("nested commit diff: %+v", diff)
		}
		assertIndexUnchanged := func(index string) {
			t.Helper()
			after, err := os.Stat(index)
			before := indexBefore[index]
			if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
				t.Fatalf("viewer modified index %q: %v", index, err)
			}
		}
		assertIndexUnchanged(childIndex)
		runViewerFixtureGit(t, childDir, "branch", "main", childHead)
		runViewerFixtureGit(t, childDir, "add", "shared.txt")
		runViewerFixtureGit(t, childDir, "commit", "-m", "child change")
		state = read(WorkspaceViewerRequest{View: "state", Repo: child, Base: "parent-only"}).(viewerState)
		if state.Base != "main" || !state.BaseFallback || state.BaseAtHead || state.Ahead != 1 || len(state.Commits) != 1 {
			t.Fatalf("child default branch fallback: %+v", state)
		}
		diff = read(WorkspaceViewerRequest{View: "diff", Repo: child, Base: "parent-only", Path: "shared.txt", Scope: "base"}).(viewerContent)
		if !strings.Contains(diff.Text, "-child data\n+working change in child\n") {
			t.Fatalf("child baseline diff: %+v", diff)
		}
		for _, tc := range []struct {
			req WorkspaceViewerRequest
			err error
		}{
			{WorkspaceViewerRequest{View: "state", Repo: "../outside"}, ErrViewerRepository},
			{WorkspaceViewerRequest{View: "tree", Repo: "ordinary"}, ErrViewerRepository},
			{WorkspaceViewerRequest{View: "file", Repo: "modules/missing", Path: "shared.txt"}, ErrViewerRepoMissing},
			{WorkspaceViewerRequest{View: "diff", Repo: "external", Path: "shared.txt", Scope: "commit", Commit: childHead}, ErrViewerRepoMissing},
			{WorkspaceViewerRequest{View: "file", Repo: child, Path: "../shared.txt"}, ErrViewerPath},
			{WorkspaceViewerRequest{View: "file", Repo: child, Path: ".git"}, ErrViewerPath},
			{WorkspaceViewerRequest{View: "file", Repo: child, Path: ".aha2-context/task.md"}, ErrViewerPath},
			{WorkspaceViewerRequest{View: "diff", Repo: child, Path: "shared.txt", Scope: "commit", Commit: nestedHead}, ErrViewerRevision},
		} {
			if _, err := view(tc.req); !errors.Is(err, tc.err) {
				t.Fatalf("%+v: got %v, want %v", tc.req, err, tc.err)
			}
		}
		if linkAllowed {
			if _, err := view(WorkspaceViewerRequest{View: "file", Repo: "linked", Path: "shared.txt"}); !errors.Is(err, ErrViewerRepoMissing) {
				t.Fatalf("symlink selection: %v", err)
			}
		}
		assertIndexUnchanged(rootIndex)
	})
}

func TestWorkspaceViewerLinkedWorktreeSubmodule(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, worktree string, source viewerFS) {
		isolateViewerGitConfig(t)
		const child = "libs/sub"
		submodule := t.TempDir()
		runViewerFixtureGit(t, submodule, "init", "-b", "main")
		writeViewerFixture(t, submodule, "source.txt", "original\n")
		runViewerFixtureGit(t, submodule, "add", "source.txt")
		runViewerFixtureGit(t, submodule, "commit", "-m", "Submodule source")
		head := runViewerFixtureGit(t, submodule, "rev-parse", "HEAD")

		main := t.TempDir()
		runViewerFixtureGit(t, main, "init", "-b", "main")
		runViewerFixtureGit(t, main, "-c", "protocol.file.allow=always", "submodule", "add", submodule, child)
		runViewerFixtureGit(t, main, "commit", "-m", "Add submodule")
		runViewerFixtureGit(t, main, "worktree", "add", "--detach", worktree, "HEAD")
		runViewerFixtureGit(t, worktree, "-c", "protocol.file.allow=always", "submodule", "update", "--init")
		childDir := filepath.Join(worktree, filepath.FromSlash(child))
		actualGitDir := runViewerFixtureGit(t, childDir, "rev-parse", "--absolute-git-dir")
		worktreeGitDir := runViewerFixtureGit(t, worktree, "rev-parse", "--absolute-git-dir")
		common := runViewerFixtureGit(t, worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if !viewerModuleTarget(filepath.Join(worktreeGitDir, "modules"), actualGitDir) ||
			viewerModuleTarget(filepath.Join(common, "modules"), actualGitDir) {
			t.Fatalf("expected worktree-local submodule metadata: gitdir=%q, worktree gitdir=%q, common=%q", actualGitDir, worktreeGitDir, common)
		}
		writeViewerFixture(t, childDir, "source.txt", "modified in worktree\n")

		read := func(req WorkspaceViewerRequest) any {
			t.Helper()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			value, err := readWorkspaceView(ctx, source, domain.Task{}, "Linked worktree", "original-repository", req)
			if err != nil {
				t.Fatalf("%+v: %v", req, err)
			}
			return value
		}
		state := read(WorkspaceViewerRequest{View: "state"}).(viewerState)
		index := slices.IndexFunc(state.Items, func(repo viewerRepository) bool { return repo.ID == child })
		if index < 0 {
			t.Fatalf("submodule stayed out of the repository list: %+v", state.Items)
		}
		if repo := state.Items[index]; !repo.Initialized || !repo.Readable || !repo.Detached || repo.Head != head {
			t.Fatalf("linked worktree submodule: %+v (Git directory %s)", repo, actualGitDir)
		}
		t.Logf("%s initialized=true readable=true with metadata under the worktree Git directory", child)
		file := read(WorkspaceViewerRequest{View: "file", Repo: child, Path: "source.txt"}).(viewerContent)
		if file.Text != "modified in worktree\n" {
			t.Fatalf("submodule file: %+v", file)
		}
		diff := read(WorkspaceViewerRequest{View: "diff", Repo: child, Path: "source.txt", Scope: "uncommitted"}).(viewerContent)
		if !strings.Contains(diff.Text, "-original\n+modified in worktree\n") {
			t.Fatalf("submodule diff: %+v", diff)
		}
		t.Log("read the submodule file and its uncommitted diff")

		const ref = "refs/custom/topic"
		runViewerFixtureGit(t, childDir, "update-ref", ref, head)
		runViewerFixtureGit(t, childDir, "symbolic-ref", "HEAD", ref)
		state = read(WorkspaceViewerRequest{View: "state"}).(viewerState)
		index = slices.IndexFunc(state.Items, func(repo viewerRepository) bool { return repo.ID == child })
		if index < 0 || state.Items[index].Branch != ref || state.Items[index].Detached {
			t.Fatalf("symbolic HEAD outside refs/heads: %+v", state.Items)
		}
	})
}

func TestWorkspaceViewerDiscoversMoreThan64Repositories(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, dir string, source viewerFS) {
		runViewerFixtureGit(t, dir, "init", "-b", "main")
		writeViewerFixture(t, dir, "root.txt", "root\n")
		runViewerFixtureGit(t, dir, "add", ".")
		runViewerFixtureGit(t, dir, "commit", "-m", "root")
		head := runViewerFixtureGit(t, dir, "rev-parse", "HEAD")
		var links strings.Builder
		const linksCount = 70
		for i := 0; i < linksCount; i++ {
			fmt.Fprintf(&links, "160000 %s\tmodule-%02d\n", head, i)
		}
		cmd := exec.CommandContext(t.Context(), "git", "update-index", "--index-info")
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(links.String())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Git link fixture: %v: %s", err, output)
		}
		repos, err := source.repositories(t.Context())
		if err != nil || len(repos.Items) != linksCount+1 || repos.Truncated ||
			repos.Items[0].ID != "" || repos.Items[0].Name != filepath.Base(dir) || repos.Items[linksCount].ID != "module-69" {
			t.Fatalf("complete repository discovery: %+v, %v", repos, err)
		}
	})
}

func TestWorkspaceViewerGitlinkChanges(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, dir string, source viewerFS) {
		isolateViewerGitConfig(t)
		const child = "libs/child [module]"
		upstream := t.TempDir()
		runViewerFixtureGit(t, upstream, "init", "-b", "main")
		writeViewerFixture(t, upstream, "code.txt", "one\n")
		runViewerFixtureGit(t, upstream, "add", "code.txt")
		runViewerFixtureGit(t, upstream, "commit", "-m", "First child version")
		old := runViewerFixtureGit(t, upstream, "rev-parse", "HEAD")
		runViewerFixtureGit(t, dir, "init", "-b", "main")
		runViewerFixtureGit(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", upstream, child)
		runViewerFixtureGit(t, dir, "config", "submodule."+child+".ignore", "all")
		runViewerFixtureGit(t, dir, "config", "diff.submodule", "log")
		runViewerFixtureGit(t, dir, "commit", "-m", "Add child")
		parentBase := runViewerFixtureGit(t, dir, "rev-parse", "HEAD")
		childDir := filepath.Join(dir, filepath.FromSlash(child))
		writeViewerFixture(t, childDir, "code.txt", "two\n")
		runViewerFixtureGit(t, childDir, "add", "code.txt")
		runViewerFixtureGit(t, childDir, "commit", "-m", "Second child version")
		newHead := runViewerFixtureGit(t, childDir, "rev-parse", "HEAD")
		runViewerFixtureGit(t, dir, "add", "--force", "--", child)
		runViewerFixtureGit(t, dir, "commit", "-m", "Update only the child pointer")
		commit := runViewerFixtureGit(t, dir, "rev-parse", "HEAD")

		read := func(req WorkspaceViewerRequest) any {
			t.Helper()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			value, err := readWorkspaceView(ctx, source, domain.Task{BaseCommit: parentBase}, "Workspace", "parent-repository", req)
			if err != nil {
				t.Fatalf("%+v: %v", req, err)
			}
			return value
		}
		assertLink := func(changes []viewerChange, want viewerGitlink) {
			t.Helper()
			if len(changes) != 1 || changes[0].Path != child || changes[0].Gitlink == nil || *changes[0].Gitlink != want {
				t.Fatalf("gitlink changes: %+v, want %+v under %s", changes, want, child)
			}
		}
		assertPreview := func(scope, prior, next string) {
			t.Helper()
			diff := read(WorkspaceViewerRequest{View: "diff", Path: child, Scope: scope, Commit: commit}).(viewerContent)
			if !strings.Contains(diff.Text, "-Subproject commit "+prior) || !strings.Contains(diff.Text, "+Subproject commit "+next) ||
				strings.Contains(diff.Text, "code.txt") || strings.Contains(diff.Text, "Second child version") {
				t.Fatalf("%s pointer preview: %q", scope, diff.Text)
			}
		}
		state := read(WorkspaceViewerRequest{View: "state"}).(viewerState)
		if len(state.Commits) != 1 {
			t.Fatalf("pointer-only commit: %+v", state.Commits)
		}
		for _, changes := range [][]viewerChange{state.Commits[0].Files, state.BaseChanges} {
			assertLink(changes, viewerGitlink{Old: old, New: newHead})
		}
		assertPreview("commit", old, newHead)
		assertPreview("base", old, newHead)
		t.Log("pointer-only commit and baseline both list the changed gitlink and show its full IDs")

		writeViewerFixture(t, childDir, "code.txt", "dirty child worktree\n")
		writeViewerFixture(t, childDir, "untracked.txt", "untracked child data\n")
		dirty := read(WorkspaceViewerRequest{View: "state"}).(viewerState)
		if len(dirty.Uncommitted) != 0 || dirty.Version != state.Version {
			t.Fatalf("child worktree dirt leaked into parent state: %+v", dirty.Uncommitted)
		}
		runViewerFixtureGit(t, childDir, "checkout", "--force", "--detach", old)
		state = read(WorkspaceViewerRequest{View: "state"}).(viewerState)
		assertLink(state.Uncommitted, viewerGitlink{Old: newHead, New: old})
		assertPreview("uncommitted", newHead, old)
		summary := read(WorkspaceViewerRequest{View: "state", Summary: true}).(viewerState)
		if summary.Version == dirty.Version || summary.Version != state.Version {
			t.Fatalf("summary did not track the checked-out pointer")
		}
		runViewerFixtureGit(t, dir, "add", "--force", "--", child)
		state = read(WorkspaceViewerRequest{View: "state"}).(viewerState)
		assertLink(state.Uncommitted, viewerGitlink{Old: newHead, New: old})
		assertPreview("uncommitted", newHead, old)
		runViewerFixtureGit(t, childDir, "checkout", "--detach", newHead)
		state = read(WorkspaceViewerRequest{View: "state"}).(viewerState)
		assertLink(state.Uncommitted, viewerGitlink{Old: newHead, Index: old, New: newHead})
		assertPreview("uncommitted", newHead, old)
		assertPreview("uncommitted", old, newHead)
		t.Log("worktree-only, staged, and divergent index/worktree pointers show without child file changes")
	})
}

func TestWorkspaceViewerDiscoversRepositoriesAfterWideAndDeepPaths(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, dir string, source viewerFS) {
		runViewerFixtureGit(t, dir, "init", "-b", "main")
		for i := range 520 {
			if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("folder-%03d", i)), 0700); err != nil {
				t.Fatal(err)
			}
		}
		deep := "folder-519/" + strings.Repeat("layer/", 13) + "repository"
		folder := filepath.Join(dir, filepath.FromSlash(deep))
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		runViewerFixtureGit(t, folder, "init", "-b", "main")
		writeViewerFixture(t, folder, "deep.txt", "deep data")
		value, err := readWorkspaceView(t.Context(), source, domain.Task{}, "Workspace", "parent-repository", WorkspaceViewerRequest{View: "state"})
		if err != nil {
			t.Fatal(err)
		}
		state := value.(viewerState)
		if len(state.Items) != 2 || state.viewerRepositories.Truncated || state.Items[1].ID != deep || !state.Items[1].Readable {
			t.Fatalf("wide and deep scan: %+v", state.viewerRepositories)
		}
		got, err := readWorkspaceView(t.Context(), source, domain.Task{}, "Workspace", "parent-repository",
			WorkspaceViewerRequest{View: "file", Repo: deep, Path: "deep.txt"})
		if err != nil || got.(viewerContent).Text != "deep data" {
			t.Fatalf("read discovered repository: %+v, %v", got, err)
		}
	})
}
