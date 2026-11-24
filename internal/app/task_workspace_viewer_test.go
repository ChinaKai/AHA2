package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspace"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func viewerFixture(t *testing.T) (string, nativeViewerFS) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return dir, nativeViewerFS{root: root, dir: dir}
}

func writeViewerFixture(t *testing.T, dir, path, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, path), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func runViewerFixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestWorkspaceViewerFiles(t *testing.T) {
	dir, native := viewerFixture(t)
	outside := t.TempDir()
	writeViewerFixture(t, outside, "outside.txt", "outside")
	writeViewerFixture(t, dir, "readme.txt", "hello\n")
	writeViewerFixture(t, dir, ".env.local", "hidden")
	writeViewerFixture(t, dir, "binary.dat", "\x00abc")
	writeViewerFixture(t, dir, "large.txt", strings.Repeat("x", viewerFileLimit+1))
	if err := os.Symlink(outside, filepath.Join(dir, "outside")); err != nil && runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, ".env.local"), filepath.Join(dir, "alias.txt")); err != nil && runtime.GOOS != "windows" {
		t.Fatal(err)
	}

	sources := map[string]viewerFS{"native": native}
	if _, err := exec.LookPath("python3"); err == nil && runtime.GOOS != "windows" {
		// Run exactly the POSIX worker and framing used by SSH/WSL against
		// this fixture, with no dependence on an SSH account.
		sources["remote worker"] = remoteViewerFS{root: dir, runner: workspace.LocalRunner{}}
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			read := func(view, path string) (any, error) {
				return readWorkspaceView(t.Context(), source, domain.Task{}, "fixture", "fixture", WorkspaceViewerRequest{View: view, Path: path, Scope: "uncommitted"})
			}
			result, err := read("state", "")
			if err != nil || result.(viewerState).IsGit {
				t.Fatalf("non-Git state: %+v %v", result, err)
			}
			rawTree, err := read("tree", "")
			if err != nil {
				t.Fatal(err)
			}
			masked := false
			for _, entry := range rawTree.(viewerTree).Entries {
				masked = masked || entry.Path == ".env.local" && entry.Masked
				if entry.Path == "readme.txt" && (entry.Kind != "file" || entry.Size != 6 || entry.Modified.IsZero()) {
					t.Fatalf("tree metadata: %+v", entry)
				}
			}
			if !masked {
				t.Fatal("tree did not identify sensitive entry")
			}
			for path, expected := range map[string]viewerContent{
				"readme.txt": {Text: "hello\n"}, ".env.local": {Placeholder: "masked"},
				"large.txt": {Placeholder: "too_large"}, "binary.dat": {Placeholder: "binary"},
			} {
				result, err := read("file", path)
				if err != nil || result != expected {
					t.Errorf("%q = %+v, %v, want %+v", path, result, err, expected)
				}
			}
			paths := []string{"../secret.txt", "dir/../secret.txt", "/etc/passwd", "C:/secret", "dir\\secret", ".GiT/config", ".aha2-context/config"}
			if runtime.GOOS != "windows" {
				paths = append(paths, "outside/outside.txt", "alias.txt")
			}
			for _, path := range paths {
				if _, err := read("file", path); !errors.Is(err, ErrViewerPath) {
					t.Errorf("%q returned %v, want invalid path", path, err)
				}
			}
			if _, err := read("diff", "readme.txt"); !errors.Is(err, ErrViewerNotGit) {
				t.Errorf("non-Git diff returned %v", err)
			}
		})
	}
}

func TestWorkspaceViewerGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	dir, native := viewerFixture(t)
	runViewerFixtureGit(t, dir, "init", "-b", "main")
	writeViewerFixture(t, dir, "readme.txt", "one\n")
	writeViewerFixture(t, dir, ".env", "initial hidden\n")
	runViewerFixtureGit(t, dir, "add", ".")
	runViewerFixtureGit(t, dir, "commit", "-m", "base")
	base := runViewerFixtureGit(t, dir, "rev-parse", "HEAD")

	worktree := filepath.Join(t.TempDir(), "worktree")
	runViewerFixtureGit(t, dir, "worktree", "add", "-b", "task", worktree)
	writeViewerFixture(t, worktree, "readme.txt", "one\ntwo\n")
	writeViewerFixture(t, worktree, ".env", "still hidden\n")
	runViewerFixtureGit(t, worktree, "commit", "-am", "task change")
	head := runViewerFixtureGit(t, worktree, "rev-parse", "HEAD")
	writeViewerFixture(t, worktree, "readme.txt", "one \ntwo\n")
	writeViewerFixture(t, worktree, "untracked.txt", strings.Repeat("new\n", viewerLineLimit+1))
	writeViewerFixture(t, worktree, "big.txt", strings.Repeat("line with enough bytes\n", 110_000))
	runViewerFixtureGit(t, worktree, "add", "big.txt")
	indexPath := runViewerFixtureGit(t, worktree, "rev-parse", "--path-format=absolute", "--git-path", "index")
	indexBefore, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(worktree)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source := nativeViewerFS{root: root, dir: worktree}
	task := domain.Task{Title: "viewer", BaseCommit: base, TargetBranch: "main"}
	state, err := readViewerState(t.Context(), source, task, "test", WorkspaceViewerRequest{}, viewerRepositories{})
	if err != nil || !state.IsGit || state.Head != head || state.Branch != "task" || state.Base != base ||
		state.Ahead != 1 || state.Behind != 0 || state.MergeBase != base || len(state.Branches) != 2 ||
		len(state.Commits) != 1 || len(state.Uncommitted) != 3 || len(state.BaseChanges) != 2 {
		t.Fatalf("state: %+v, err: %v", state, err)
	}
	commit := state.Commits[0]
	if commit.ID != head || commit.Subject != "task change" || len(commit.Files) != 2 ||
		commit.Files[0] != (viewerChange{Path: ".env", Masked: true}) ||
		commit.Files[1] != (viewerChange{Path: "readme.txt", Added: 1}) {
		t.Fatalf("commit: %+v", commit)
	}
	for _, scope := range []string{"commit", "base"} {
		req := WorkspaceViewerRequest{View: "diff", Path: "readme.txt", Scope: scope, Commit: head}
		result, err := readWorkspaceView(t.Context(), source, task, "", "", req)
		if err != nil || !strings.Contains(result.(viewerContent).Text, "+two") {
			t.Fatalf("%s diff: %+v %v", scope, result, err)
		}
		req.Path = ".env"
		result, err = readWorkspaceView(t.Context(), source, task, "", "", req)
		if err != nil || result != (viewerContent{Placeholder: "masked"}) {
			t.Fatalf("%s diff mask: %+v %v", scope, result, err)
		}
	}
	whitespace := WorkspaceViewerRequest{Path: "readme.txt", Scope: "uncommitted", IgnoreWhitespace: true}
	if result, err := readViewerDiff(t.Context(), source, task, whitespace, viewerRepositories{}); err != nil || result.Text != "" {
		t.Fatalf("whitespace diff: %+v %v", result, err)
	}
	whitespace.IgnoreWhitespace = false
	if result, err := readViewerDiff(t.Context(), source, task, whitespace, viewerRepositories{}); err != nil || !strings.Contains(result.Text, "+one ") {
		t.Fatalf("working tree diff: %+v %v", result, err)
	}
	for _, path := range []string{"untracked.txt", "big.txt"} {
		result, err := readViewerDiff(t.Context(), source, task, WorkspaceViewerRequest{Path: path, Scope: "uncommitted"}, viewerRepositories{})
		if err != nil || !result.Truncated || result.Text == "" || len(result.Text) > viewerDiffLimit {
			t.Fatalf("%s: length=%d truncated=%v err=%v", path, len(result.Text), result.Truncated, err)
		}
		if path == "untracked.txt" && strings.Count(result.Text, "+new\n") != viewerLineLimit {
			t.Fatal("untracked diff did not stop at the line limit")
		}
	}
	for _, req := range []WorkspaceViewerRequest{
		{Path: "readme.txt", Scope: "commit", Commit: "HEAD~1"},
		{Path: "readme.txt", Scope: "base", Base: "--no-index"},
	} {
		if _, err := readViewerDiff(t.Context(), source, task, req, viewerRepositories{}); !errors.Is(err, ErrViewerRevision) {
			t.Errorf("invalid revision: %v", err)
		}
	}
	if _, err := readViewerState(t.Context(), native, domain.Task{}, "main", WorkspaceViewerRequest{Summary: true}, viewerRepositories{}); err != nil {
		t.Fatal(err)
	}
	indexAfter, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(indexBefore, indexAfter) || !indexBefore.ModTime().Equal(indexAfter.ModTime()) {
		t.Fatal("viewing a worktree changed its Git index")
	}
}

func testViewerGitSources(t *testing.T, test func(*testing.T, string, viewerFS)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	t.Run("native", func(t *testing.T) {
		dir, source := viewerFixture(t)
		test(t, dir, source)
	})
	t.Run("ssh", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX worker requires a Unix test host")
		}
		for _, name := range []string{"sh", "python3"} {
			if _, err := exec.LookPath(name); err != nil {
				t.Skipf("%s is unavailable", name)
			}
		}
		dir := t.TempDir()
		test(t, dir, remoteViewerFS{root: dir, runner: viewerSSHFixture(t)})
	})
}

func TestWorkspaceViewerBranchSelection(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, dir string, source viewerFS) {
		runViewerFixtureGit(t, dir, "init", "-b", "main")
		commit := func(text, subject string) string {
			writeViewerFixture(t, dir, "readme.txt", text+"\n")
			runViewerFixtureGit(t, dir, "add", "readme.txt")
			runViewerFixtureGit(t, dir, "commit", "-m", subject)
			return runViewerFixtureGit(t, dir, "rev-parse", "HEAD")
		}
		base := commit("base", "base")
		runViewerFixtureGit(t, dir, "switch", "-c", "origin/topic")
		local := commit("local change", "local subject")
		runViewerFixtureGit(t, dir, "switch", "-c", "remote-fixture", base)
		subject := strings.Repeat("long commit title ", 80) + "end"
		remote := commit("remote change", subject)
		runViewerFixtureGit(t, dir, "update-ref", "refs/remotes/origin/topic", remote)
		runViewerFixtureGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/topic")
		runViewerFixtureGit(t, dir, "branch", "team/topic", local)
		runViewerFixtureGit(t, dir, "tag", "refs/heads/team", remote)
		runViewerFixtureGit(t, dir, "switch", "main")
		runViewerFixtureGit(t, dir, "branch", "-D", "remote-fixture")
		orphanTree := runViewerFixtureGit(t, dir, "rev-parse", local+"^{tree}")
		orphan := runViewerFixtureGit(t, dir, "commit-tree", orphanTree, "-m", "unrelated history")
		const orphanRef = "refs/heads/unrelated"
		runViewerFixtureGit(t, dir, "update-ref", orphanRef, orphan)
		writeViewerFixture(t, dir, "readme.txt", "uncommitted\n")
		index := filepath.Join(dir, ".git", "index")
		before, err := os.ReadFile(index)
		if err != nil {
			t.Fatal(err)
		}

		task := domain.Task{BaseCommit: base, TargetBranch: "main"}
		view := func(req WorkspaceViewerRequest) (any, error) {
			t.Helper()
			return readWorkspaceView(t.Context(), source, task, "fixture", "fixture", req)
		}
		state := func(ref string, summary bool) viewerState {
			t.Helper()
			result, err := view(WorkspaceViewerRequest{View: "state", Branch: ref, Summary: summary})
			if err != nil {
				t.Fatal(err)
			}
			return result.(viewerState)
		}
		current := state("", false)
		if current.Head != base || current.Branch != "main" || current.CheckedOutBranch != "main" || len(current.Uncommitted) != 1 {
			t.Fatalf("default state: %+v", current)
		}
		const localRef = "refs/heads/origin/topic"
		const remoteRef = "refs/remotes/origin/topic"
		for _, ref := range []string{localRef, remoteRef} {
			if !slices.Contains(current.BranchOptions, viewerBranch{Ref: ref, Name: "origin/topic"}) {
				t.Fatalf("branch %s missing: %+v", ref, current.BranchOptions)
			}
		}
		if slices.Contains(current.BranchOptions, viewerBranch{Ref: "refs/remotes/origin/HEAD", Name: "origin/HEAD"}) {
			t.Fatal("symbolic remote HEAD was offered as a branch")
		}
		for _, option := range []struct{ ref, head, change string }{
			{localRef, local, "local change"}, {remoteRef, remote, "remote change"},
		} {
			selected := state(option.ref, false)
			if selected.Head != option.head || selected.CheckedOutBranch != "main" || len(selected.Uncommitted) != 0 ||
				selected.MergeBase != base || selected.Ahead != 1 || len(selected.BaseChanges) != 1 || len(selected.Commits) != 1 ||
				selected.Commits[0].ID != option.head || selected.NoCommonAncestor {
				t.Fatalf("%s: %+v", option.ref, selected)
			}
			for _, scope := range []string{"base", "commit"} {
				result, err := view(WorkspaceViewerRequest{View: "diff", Scope: scope, Path: "readme.txt", Branch: option.ref, Commit: option.head})
				if err != nil || !strings.Contains(result.(viewerContent).Text, "+"+option.change) {
					t.Fatalf("%s diff at %s: %+v, %v", scope, option.ref, result, err)
				}
			}
		}
		unrelated := state(orphanRef, false)
		if !unrelated.NoCommonAncestor || unrelated.Base != base || unrelated.MergeBase != "" || unrelated.Ahead != 0 ||
			unrelated.Behind != 0 || len(unrelated.BaseChanges) != 0 || len(unrelated.Commits) != 1 ||
			unrelated.Commits[0].ID != orphan || len(unrelated.Commits[0].Files) != 1 {
			t.Fatalf("unrelated history: %+v", unrelated)
		}
		if _, err := view(WorkspaceViewerRequest{View: "diff", Scope: "base", Path: "readme.txt", Branch: orphanRef}); !errors.Is(err, ErrViewerRevision) {
			t.Fatalf("base diff for unrelated history: %v", err)
		}
		result, err := view(WorkspaceViewerRequest{View: "diff", Scope: "commit", Path: "readme.txt", Branch: orphanRef, Commit: orphan})
		if err != nil || !strings.Contains(result.(viewerContent).Text, "+local change") {
			t.Fatalf("commit diff for unrelated history: %+v, %v", result, err)
		}
		latest := state(remoteRef, false).Commits[0]
		if latest.Short != remote[:7] || latest.Subject != subject || latest.Author != "Test" || latest.Date <= 0 {
			t.Fatalf("commit metadata: %+v", latest)
		}
		for _, ref := range []string{"HEAD", "--all", remoteRef + "^", "refs/tags/refs/heads/team", "refs/heads/team",
			"refs/heads/team/*", "refs/heads/x\x00y", "refs/heads/x@{1}", "refs/heads/team/../topic", "refs/remotes/origin/HEAD"} {
			if _, err := view(WorkspaceViewerRequest{View: "state", Branch: ref}); !errors.Is(err, ErrViewerBranch) {
				t.Errorf("%q: expected branch error, got %v", ref, err)
			}
		}
		for _, req := range []WorkspaceViewerRequest{
			{View: "diff", Scope: "commit", Path: "readme.txt", Branch: remoteRef, Commit: local},
			{View: "state", Branch: remoteRef, Base: "origin/HEAD"},
		} {
			if _, err := view(req); !errors.Is(err, ErrViewerRevision) {
				t.Errorf("%+v: expected revision error, got %v", req, err)
			}
		}
		if _, err := view(WorkspaceViewerRequest{View: "diff", Scope: "uncommitted", Path: "readme.txt", Branch: remoteRef}); !errors.Is(err, ErrViewerBranch) {
			t.Fatalf("uncommitted diff on selected branch: %v", err)
		}
		version := state(remoteRef, true).Version
		runViewerFixtureGit(t, dir, "update-ref", remoteRef, local)
		if state(remoteRef, true).Version == version {
			t.Fatal("polling did not detect the remote-tracking ref move")
		}
		runViewerFixtureGit(t, dir, "update-ref", "-d", remoteRef)
		for _, req := range []WorkspaceViewerRequest{
			{View: "state", Branch: remoteRef}, {View: "state", Branch: remoteRef, Summary: true},
			{View: "diff", Scope: "base", Path: ".env", Branch: remoteRef},
		} {
			if _, err := view(req); !errors.Is(err, ErrViewerBranch) {
				t.Errorf("deleted branch: %+v: %v", req, err)
			}
		}
		after, err := os.ReadFile(index)
		text, contentErr := os.ReadFile(filepath.Join(dir, "readme.txt"))
		if err != nil || contentErr != nil || !bytes.Equal(before, after) || string(text) != "uncommitted\n" ||
			runViewerFixtureGit(t, dir, "symbolic-ref", "HEAD") != "refs/heads/main" {
			t.Fatal("branch viewing changed the index, working copy or HEAD")
		}
	})
}

func TestWorkspaceViewerBranchLimits(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, dir string, source viewerFS) {
		runViewerFixtureGit(t, dir, "init", "-b", "main")
		runViewerFixtureGit(t, dir, "commit", "--allow-empty", "-m", "base")
		head := runViewerFixtureGit(t, dir, "rev-parse", "HEAD")
		var updates strings.Builder
		for i := 0; i < viewerBranchLimit; i++ {
			if i < viewerBranchLimit-1 {
				fmt.Fprintf(&updates, "create refs/heads/a/%03d %s\n", i, head)
			}
			fmt.Fprintf(&updates, "create refs/remotes/origin/b/%03d %s\n", i, head)
		}
		cmd := exec.CommandContext(t.Context(), "git", "update-ref", "--stdin")
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(updates.String())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("seed branch refs: %v: %s", err, output)
		}
		read := func(summary bool) viewerState {
			t.Helper()
			state, err := readViewerState(t.Context(), source, domain.Task{}, "fixture", WorkspaceViewerRequest{Summary: summary}, viewerRepositories{})
			if err != nil {
				t.Fatal(err)
			}
			return state
		}
		exact := read(false)
		if exact.LocalRefsTruncated || exact.RemoteRefsTruncated || len(exact.Branches) != viewerBranchLimit ||
			!slices.Contains(exact.Branches, "main") || len(exact.BranchOptions) != 2*viewerBranchLimit {
			t.Fatal("a list at the ref limit was truncated or incomplete")
		}
		runViewerFixtureGit(t, dir, "update-ref", "refs/heads/a/499", head)
		local := read(false)
		if !local.LocalRefsTruncated || local.RemoteRefsTruncated || local.Truncated || len(local.Branches) != viewerBranchLimit ||
			slices.Contains(local.Branches, "main") || len(local.BranchOptions) != 2*viewerBranchLimit || local.CheckedOutBranch != "main" {
			t.Fatal("local truncation did not preserve the checked-out branch and list bounds")
		}
		if version := read(true).Version; version == exact.Version || version != local.Version {
			t.Fatal("summary did not detect local branch truncation")
		}
		runViewerFixtureGit(t, dir, "update-ref", "refs/remotes/origin/b/500", head)
		runViewerFixtureGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/b/000")
		both := read(false)
		if !both.LocalRefsTruncated || !both.RemoteRefsTruncated || both.Truncated || len(both.BranchOptions) > 2*viewerBranchLimit ||
			slices.Contains(both.BranchOptions, viewerBranch{Ref: "refs/remotes/origin/HEAD", Name: "origin/HEAD"}) {
			t.Fatal("remote truncation was missed when a symbolic ref consumed a scan slot")
		}
	})
}

type viewerBudgetSource struct {
	viewerFS
	check func(context.Context, string, bool) error
}

func (source viewerBudgetSource) repositories(ctx context.Context) (viewerRepositories, error) {
	if err := source.check(ctx, "repositories", false); err != nil {
		return viewerRepositories{}, err
	}
	return source.viewerFS.repositories(ctx)
}

func (source viewerBudgetSource) git(ctx context.Context, limit int, readLineEndings bool, args ...string) (viewerGitOutput, error) {
	branch := slices.Contains(args, "for-each-ref")
	if err := source.check(ctx, args[len(args)-1], branch); err != nil {
		return viewerGitOutput{}, err
	}
	return source.viewerFS.git(ctx, limit, readLineEndings, args...)
}

func TestWorkspaceViewerBranchBudget(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	dir, native := viewerFixture(t)
	runViewerFixtureGit(t, dir, "init", "-b", "main")
	writeViewerFixture(t, dir, "readme.txt", "base\n")
	runViewerFixtureGit(t, dir, "add", "readme.txt")
	runViewerFixtureGit(t, dir, "commit", "-m", "base")
	head := runViewerFixtureGit(t, dir, "rev-parse", "HEAD")
	const ref = "refs/heads/topic"
	runViewerFixtureGit(t, dir, "update-ref", ref, head)
	task := domain.Task{BaseCommit: head}

	for _, tc := range []struct {
		name string
		req  WorkspaceViewerRequest
		refs []string
		fail bool
	}{
		{"state", WorkspaceViewerRequest{View: "state", Branch: ref}, []string{ref, viewerLocalRefs, viewerRemoteRefs}, false},
		{"base diff", WorkspaceViewerRequest{View: "diff", Scope: "base", Path: "readme.txt", Branch: ref}, []string{ref, viewerLocalRefs}, false},
		{"commit diff", WorkspaceViewerRequest{View: "diff", Scope: "commit", Commit: head, Path: "readme.txt", Branch: ref}, []string{ref}, false},
		{"enumeration error", WorkspaceViewerRequest{View: "state", Branch: ref}, []string{ref, viewerLocalRefs}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			deadline, _ := ctx.Deadline()
			var branchDeadline time.Time
			var refs []string
			var otherReads int
			source := viewerBudgetSource{viewerFS: native, check: func(callCtx context.Context, step string, branch bool) error {
				got, _ := callCtx.Deadline()
				if !branch {
					otherReads++
					if got != deadline {
						t.Errorf("%s used %v; want the original request deadline %v", step, got, deadline)
					}
					return nil
				}
				refs = append(refs, step)
				if branchDeadline.IsZero() {
					branchDeadline = got
				}
				if got != branchDeadline || got.Before(start.Add(5*time.Second)) || got.After(time.Now().Add(5*time.Second)) {
					t.Errorf("%s did not use the shared five-second branch deadline: %v", step, got)
				}
				if tc.fail && step == viewerLocalRefs {
					return ErrViewerGit
				}
				return nil
			}}
			result, err := readWorkspaceView(ctx, source, task, "fixture", "fixture", tc.req)
			if tc.fail {
				partial, _ := result.(viewerState)
				if !errors.Is(err, ErrViewerGit) || partial.Version != "" {
					t.Fatalf("enumeration error returned result=%v err=%v", result, err)
				}
			} else if err != nil || otherReads == 0 {
				t.Fatalf("ordinary reads=%d err=%v", otherReads, err)
			}
			if !slices.Equal(refs, tc.refs) {
				t.Errorf("branch queries %v; want %v", refs, tc.refs)
			}
		})
	}
}

func isolateViewerGitConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// Give the fixture and the viewer's config query the same isolated user config.
	config := filepath.Join(home, ".gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	return config
}

func TestWorkspaceViewerDiffLeavesIndexUntouched(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, dir string, source viewerFS) {
		isolateViewerGitConfig(t)
		runViewerFixtureGit(t, dir, "init", "-b", "main")
		runViewerFixtureGit(t, dir, "config", "diff.autoRefreshIndex", "true")
		const name = "unchanged.txt"
		writeViewerFixture(t, dir, name, "unchanged\n")
		runViewerFixtureGit(t, dir, "add", name)
		runViewerFixtureGit(t, dir, "commit", "-m", "base")

		gitDir := filepath.Join(dir, ".git")
		hook := filepath.Join(gitDir, "hooks", "post-index-change")
		if err := os.WriteFile(hook, []byte("#!/bin/sh\nprintf 'ran\\n' > hook-ran\n"), 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		dirtyTime := info.ModTime().Add(-2 * time.Second)
		if err := os.Chtimes(path, dirtyTime, dirtyTime); err != nil {
			t.Fatal(err)
		}
		index := filepath.Join(gitDir, "index")
		before, err := os.ReadFile(index)
		if err != nil {
			t.Fatal(err)
		}
		beforeInfo, err := os.Stat(index)
		if err != nil {
			t.Fatal(err)
		}

		got, err := readViewerDiff(t.Context(), source, domain.Task{}, WorkspaceViewerRequest{Path: name, Scope: "uncommitted"}, viewerRepositories{})
		if err != nil || got != (viewerContent{}) {
			t.Fatalf("expected no content change: %+v %v", got, err)
		}
		after, err := os.ReadFile(index)
		if err != nil {
			t.Fatal(err)
		}
		afterInfo, err := os.Stat(index)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) || !os.SameFile(beforeInfo, afterInfo) || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
			t.Error("viewing an unchanged file with different timestamps rewrote the index")
		}
		if _, err := os.Stat(filepath.Join(dir, "hook-ran")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("unexpected post-index-change hook result: %v", err)
		}
	})
}

func TestWorkspaceViewerDiffKeepsConfiguredLineEndings(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, dir string, source viewerFS) {
		config := isolateViewerGitConfig(t)
		runViewerFixtureGit(t, dir, "config", "--global", "core.autocrlf", "true")
		runViewerFixtureGit(t, dir, "init", "-b", "main")
		const name = "readme.txt"
		const original = "first\r\nsecond\r\nthird\r\nfourth\r\nfifth\r\n"
		writeViewerFixture(t, dir, name, original)
		runViewerFixtureGit(t, dir, "add", name)
		runViewerFixtureGit(t, dir, "commit", "-m", "base")
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		runViewerFixtureGit(t, dir, "checkout", "--", name)
		checkedOut, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(checkedOut) != original {
			t.Fatalf("test checkout must use CRLF: %q %v", checkedOut, err)
		}
		writeViewerFixture(t, dir, name, strings.Replace(original, "third", "changed", 1))
		normal := runViewerFixtureGit(t, dir, "diff", "--numstat", "HEAD", "--", name)
		if normal != "1\t1\t"+name {
			t.Fatalf("expected one changed line in the normal Git diff: %q", normal)
		}

		// Global callbacks must still be unavailable to the viewer's Git commands.
		writeViewerFixture(t, dir, ".gitattributes", "*.txt diff=viewer filter=viewer\n")
		if err := os.WriteFile(config, []byte("[core]\n autocrlf = true\n eol = crlf\n fsmonitor = ./callback.sh\n"+
			"[diff]\n external = ./callback.sh\n[diff \"viewer\"]\n command = ./callback.sh\n textconv = ./callback.sh\n"+
			"[filter \"viewer\"]\n clean = ./callback.sh\n process = ./callback.sh\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "callback.sh"), []byte("#!/bin/sh\nprintf 'ran\\n' > callback-ran\n"), 0700); err != nil {
			t.Fatal(err)
		}
		got, err := readViewerDiff(t.Context(), source, domain.Task{}, WorkspaceViewerRequest{Path: name, Scope: "uncommitted"}, viewerRepositories{})
		if err != nil || !strings.Contains(got.Text, "\n-third\n+changed\n") || strings.Count(got.Text, "\n+") != 2 || strings.Count(got.Text, "\n-") != 2 || strings.Contains(got.Text, "\r") {
			t.Errorf("expected a one-line diff: %+v %v", got, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "callback-ran")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("unexpected global callback result: %v", err)
		}
	})
}

func TestWorkspaceViewerGitReadsOnlySystemLineEndingConfig(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, dir string, source viewerFS) {
		config := isolateViewerGitConfig(t)
		system := filepath.Join(t.TempDir(), "gitconfig")
		t.Setenv("GIT_CONFIG_NOSYSTEM", "0")
		t.Setenv("GIT_CONFIG_SYSTEM", system)
		if err := os.WriteFile(system, []byte("[core]\n autocrlf = false\n eol = LF\n pager = fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config, []byte("[core]\n autocrlf = input\n"), 0600); err != nil {
			t.Fatal(err)
		}
		runViewerFixtureGit(t, dir, "init", "-b", "main")
		out, err := source.git(t.Context(), 4096, true, "--no-pager", "config", "--null", "--get-regexp", "^core\\.(autocrlf|eol|pager)$")
		if err != nil || out.exit != 0 || out.truncated || string(out.data) != "core.autocrlf\ninput\x00core.eol\nlf\x00" {
			t.Fatalf("expected only normalized line ending settings: %q, exit=%d, %v", out.data, out.exit, err)
		}
	})
}

func TestWorkspaceViewerGitConfigProbeFallback(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, dir string, source viewerFS) {
		config := isolateViewerGitConfig(t)
		runViewerFixtureGit(t, dir, "init", "-b", "main")
		const name = "readme.txt"
		writeViewerFixture(t, dir, name, "old\n")
		runViewerFixtureGit(t, dir, "add", name)
		runViewerFixtureGit(t, dir, "commit", "-m", "base")
		writeViewerFixture(t, dir, name, "new\n")

		for _, tc := range []struct{ name, cfg string }{
			{"parse error", "[core\n"},
			{"invalid values", "[core]\n autocrlf = invalid\n eol = invalid\n"},
			{"timeout", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if err := os.Remove(config); err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
				if tc.name == "timeout" {
					mkfifo, err := exec.LookPath("mkfifo")
					if err != nil || runtime.GOOS == "windows" {
						t.Skip("FIFO fixture requires a POSIX host")
					}
					if err := exec.CommandContext(t.Context(), mkfifo, config).Run(); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(config, []byte(tc.cfg), 0600); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				got, err := readViewerDiff(ctx, source, domain.Task{}, WorkspaceViewerRequest{Path: name, Scope: "uncommitted"}, viewerRepositories{})
				if err != nil || !strings.Contains(got.Text, "\n-old\n+new\n") || ctx.Err() != nil {
					t.Fatalf("bad user config must not stop the protected diff: %+v %v", got, err)
				}
			})
		}
	})
}

func TestWorkspaceViewerDisablesGitCallbacks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	dir, source := viewerFixture(t)
	runViewerFixtureGit(t, dir, "init", "-b", "main")
	writeViewerFixture(t, dir, "hello.txt", "one\n")
	runViewerFixtureGit(t, dir, "add", ".")
	runViewerFixtureGit(t, dir, "commit", "-m", "base")
	writeViewerFixture(t, dir, ".gitattributes", "*.txt filter=sideeffect diff=sideeffect\n")
	writeViewerFixture(t, dir, "hello.txt", "two\n")
	// If invoked, any of these commands makes a marker in the fixture.
	for _, key := range []string{"core.fsmonitor", "filter.sideeffect.clean", "filter.sideeffect.process", "diff.sideeffect.command", "diff.sideeffect.textconv"} {
		runViewerFixtureGit(t, dir, "config", key, "echo run > callback-ran")
	}
	if _, err := readViewerState(t.Context(), source, domain.Task{}, "test", WorkspaceViewerRequest{}, viewerRepositories{}); err != nil {
		t.Fatal(err)
	}
	if _, err := readViewerDiff(t.Context(), source, domain.Task{}, WorkspaceViewerRequest{Path: "hello.txt", Scope: "uncommitted"}, viewerRepositories{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "callback-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Git ran a repository-configured callback: %v", err)
	}
}

func TestWorkspaceViewerCommitLimits(t *testing.T) {
	var fields []string
	for i := 0; i <= viewerCommitLimit; i++ {
		fields = append(fields, "\x1e"+strings.Repeat("a", 40), "a", "1", "subject")
		for f := 0; f <= viewerCommitFiles; f++ {
			fields = append(fields, "1\t0\tfile")
		}
	}
	var state viewerState
	state.readLog(fields)
	if len(state.Commits) != viewerCommitLimit || !state.Truncated {
		t.Fatalf("commits=%d truncated=%v", len(state.Commits), state.Truncated)
	}
	for _, commit := range state.Commits {
		if len(commit.Files) != viewerCommitFiles || !commit.Truncated {
			t.Fatalf("files=%d truncated=%v", len(commit.Files), commit.Truncated)
		}
	}
}

func TestWorkspaceViewerCommitTextLimits(t *testing.T) {
	id := strings.Repeat("a", 40)
	subject := strings.Repeat("界", 100_000)
	author := strings.Repeat("🦊", viewerAuthorLimit+1)
	var state viewerState
	state.readLog([]string{"\x1e" + id, "1", author, subject, "1\t0\tfile",
		"\x1e" + id, "2", strings.Repeat("作", viewerAuthorLimit), strings.Repeat("题", viewerSubjectLimit)})
	if len(state.Commits) != 2 || state.Truncated || len(state.Commits[0].Files) != 1 {
		t.Fatal("metadata limits changed history or file parsing")
	}
	for _, field := range []struct {
		value, want string
		limit       int
	}{
		{state.Commits[0].Subject, strings.Repeat("界", viewerSubjectLimit-1) + "…", viewerSubjectLimit},
		{state.Commits[0].Author, strings.Repeat("🦊", viewerAuthorLimit-1) + "…", viewerAuthorLimit},
		{state.Commits[1].Subject, strings.Repeat("题", viewerSubjectLimit), viewerSubjectLimit},
		{state.Commits[1].Author, strings.Repeat("作", viewerAuthorLimit), viewerAuthorLimit},
	} {
		if !utf8.ValidString(field.value) || utf8.RuneCountInString(field.value) != field.limit || field.value != field.want {
			t.Errorf("unexpected metadata at the %d-character limit; got %d characters", field.limit, utf8.RuneCountInString(field.value))
		}
	}
}

func TestWorkspaceViewerOverSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX worker requires a Unix test host")
	}
	for _, command := range []string{"sh", "python3", "git"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is unavailable", command)
		}
	}
	dir := filepath.Join(t.TempDir(), "worktree quote's 文件")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	runViewerFixtureGit(t, dir, "init", "-b", "main")
	writeViewerFixture(t, dir, "README.md", "base line\n")
	runViewerFixtureGit(t, dir, "add", ".")
	runViewerFixtureGit(t, dir, "commit", "-m", "base")
	writeViewerFixture(t, dir, "README.md", "working line\n")
	source := remoteViewerFS{root: dir, runner: viewerSSHFixture(t)}
	state, err := readViewerState(t.Context(), source, domain.Task{}, "remote", WorkspaceViewerRequest{}, viewerRepositories{})
	if err != nil || !state.IsGit || len(state.Uncommitted) != 1 || len(state.Commits) != 1 {
		t.Fatalf("SSH state: %+v, %v", state, err)
	}
	value, err := readViewerDiff(t.Context(), source, domain.Task{}, WorkspaceViewerRequest{Path: "README.md", Scope: "uncommitted"}, viewerRepositories{})
	if err != nil || !strings.Contains(value.Text, "+working line") {
		t.Fatalf("SSH diff: %+v, %v", value, err)
	}
}

func viewerSSHFixture(t *testing.T) workspace.Runner {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	password := rand.Text()
	config := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, supplied []byte) (*ssh.Permissions, error) {
		if subtle.ConstantTimeCompare([]byte(password), supplied) != 1 {
			return nil, errors.New("invalid test credentials")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	pin := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(pin, []byte(knownhosts.Line([]string{listener.Addr().String()}, signer.PublicKey())+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, channels, requests, err := ssh.NewServerConn(connection, config)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(requests)
				for peer := range channels {
					if peer.ChannelType() != "session" {
						_ = peer.Reject(ssh.UnknownChannelType, "expected a session")
						continue
					}
					channel, requests, err := peer.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for request := range requests {
							var payload struct{ Command string }
							if request.Type != "exec" || ssh.Unmarshal(request.Payload, &payload) != nil || payload.Command != "sh -s" {
								_ = request.Reply(false, nil)
								continue
							}
							_ = request.Reply(true, nil)
							cmd := exec.CommandContext(ctx, "sh", "-s")
							cmd.Stdin, cmd.Stdout, cmd.Stderr = channel, channel, channel.Stderr()
							err := cmd.Run()
							status := uint32(0)
							if err != nil {
								status = 1
							}
							_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
							return
						}
					}()
				}
			}()
		}
	}()
	return &workspace.SSHRunner{Host: host, Port: port, User: "viewer", Auth: "password", Password: password,
		Platform: "linux/amd64", KnownHostsPaths: []string{pin}, PrivateKeyPaths: []string{pin}}
}
