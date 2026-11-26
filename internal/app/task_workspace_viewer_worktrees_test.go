package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type viewerWorktreeProbeFixture struct {
	viewerFS
	listDelay, pathsDelay time.Duration
	listExit              int
	pathsErr              error
}

const viewerProbeCheckout = "/checkout"

func (source viewerWorktreeProbeFixture) git(ctx context.Context, _ int, _ bool, _ ...string) (viewerGitOutput, error) {
	select {
	case <-ctx.Done():
		return viewerGitOutput{}, ctx.Err()
	case <-time.After(source.listDelay):
		return viewerGitOutput{exit: source.listExit, data: []byte("worktree " + viewerProbeCheckout + "\x00\x00")}, nil
	}
}

func (source viewerWorktreeProbeFixture) checkWorktreePaths(ctx context.Context, _ string, _ []string) (viewerWorktreePaths, error) {
	select {
	case <-ctx.Done():
		return viewerWorktreePaths{}, ctx.Err()
	case <-time.After(source.pathsDelay):
		return viewerWorktreePaths{Current: viewerProbeCheckout, Status: []string{""}}, source.pathsErr
	}
}

func TestWorkspaceViewerWorktreeProbes(t *testing.T) {
	transport := fmt.Errorf("worker connection failed: %w", ErrViewerTransport)
	for _, tc := range []struct {
		name   string
		source viewerWorktreeProbeFixture
		want   error
	}{
		{"git list fails", viewerWorktreeProbeFixture{listExit: 128}, ErrViewerGit},
		{"git list times out", viewerWorktreeProbeFixture{listDelay: 6 * time.Second}, ErrViewerGit},
		{"path validation fails", viewerWorktreeProbeFixture{pathsErr: ErrViewerGit}, ErrViewerGit},
		{"path validation transport fails", viewerWorktreeProbeFixture{pathsErr: transport}, transport},
		{"path validation times out", viewerWorktreeProbeFixture{pathsDelay: 6 * time.Second}, ErrViewerGit},
		{"stages have independent budgets", viewerWorktreeProbeFixture{listDelay: 4 * time.Second, pathsDelay: 4 * time.Second}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				req := WorkspaceViewerRequest{Worktree: viewerWorktreeID(viewerProbeCheckout)}
				items, _, _, err := selectViewerWorktree(t.Context(), tc.source, req)
				if err != tc.want || (tc.want == nil && len(items) != 1) {
					t.Fatalf("explicit worktree: got %d worktrees and %v, want %v", len(items), err, tc.want)
				}
				if tc.want != nil {
					items, _, _, err = selectViewerWorktree(t.Context(), tc.source, WorkspaceViewerRequest{})
					if err != nil || len(items) != 0 {
						t.Fatalf("default view: got %d worktrees and %v", len(items), err)
					}
				}
			})
		})
	}
}

func TestWorkspaceViewerWorktrees(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, dir string, source viewerFS) {
		runViewerFixtureGit(t, dir, "init", "-b", "main")
		writeViewerFixture(t, dir, ".git/info/exclude", "/.aha2-worktrees/\n")
		writeViewerFixture(t, dir, "readme.txt", "initial\n")
		runViewerFixtureGit(t, dir, "add", ".")
		runViewerFixtureGit(t, dir, "commit", "-m", "initial")
		base := runViewerFixtureGit(t, dir, "rev-parse", "HEAD")
		taskDir := filepath.Join(t.TempDir(), "task")
		runViewerFixtureGit(t, dir, "worktree", "add", "-b", "task", taskDir)
		topic := filepath.Join(dir, ".aha2-worktrees", "topic")
		runViewerFixtureGit(t, dir, "worktree", "add", "-b", "topic", topic)
		writeViewerFixture(t, topic, "readme.txt", "topic commit\n")
		runViewerFixtureGit(t, topic, "commit", "-am", "topic commit")
		writeViewerFixture(t, dir, "readme.txt", "main commit\n")
		runViewerFixtureGit(t, dir, "commit", "-am", "main commit")
		head := runViewerFixtureGit(t, dir, "rev-parse", "HEAD")
		writeViewerFixture(t, dir, "readme.txt", "main edit\n")
		writeViewerFixture(t, dir, ".env", "hidden\n")
		writeViewerFixture(t, taskDir, "readme.txt", "task commit\n")
		runViewerFixtureGit(t, taskDir, "commit", "-am", "task commit")
		writeViewerFixture(t, taskDir, "task-only.txt", "task only\n")

		other := t.TempDir()
		outside := filepath.Join(other, "outside")
		missing := filepath.Join(dir, ".aha2-worktrees", "missing")
		unreadable := filepath.Join(dir, ".aha2-worktrees", "unreadable")
		replaced := filepath.Join(dir, ".aha2-worktrees", "replaced")
		for name, checkout := range map[string]string{
			"outside": outside, "missing": missing, "unreadable": unreadable, "replaced": replaced,
		} {
			runViewerFixtureGit(t, dir, "worktree", "add", "-b", name, checkout)
		}
		if err := os.RemoveAll(missing); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(unreadable, ".git")); err != nil {
			t.Fatal(err)
		}
		runViewerFixtureGit(t, unreadable, "init", "-b", "unrelated")
		if err := os.Rename(replaced, filepath.Join(other, "moved")); err != nil {
			t.Fatal(err)
		}
		hasSymlink := os.Symlink(filepath.Join(other, "moved"), replaced) == nil

		switch original := source.(type) {
		case nativeViewerFS:
			handle, err := os.OpenRoot(taskDir)
			if err != nil {
				t.Fatal(err)
			}
			defer handle.Close()
			source = nativeViewerFS{root: handle, dir: taskDir}
		case remoteViewerFS:
			original.root = taskDir
			source = original
		}
		task := domain.Task{BaseCommit: base, TargetBranch: "main"}
		view := func(req WorkspaceViewerRequest) (any, error) {
			req.workspaceRoot, req.taskCheckout = dir, true
			return readWorkspaceView(t.Context(), source, task, "fixture", "fixture", req)
		}
		readState := func(worktree string) viewerState {
			t.Helper()
			value, err := view(WorkspaceViewerRequest{View: "state", Worktree: worktree})
			if err != nil {
				t.Fatal(err)
			}
			return value.(viewerState)
		}
		state := readState("")
		if state.Base != base || state.Branch != "task" || state.Ahead != 1 || state.Behind != 0 ||
			len(state.Worktrees) < 4 || !state.Worktrees[0].Main || state.Worktrees[0].Head != head[:7] {
			t.Fatalf("task state: %+v", state)
		}
		byName := map[string]viewerWorktree{}
		for _, item := range state.Worktrees {
			byName[item.Name] = item
		}
		if !byName["task"].Default || !byName["task"].Task || byName["missing"].Unavailable != "missing" ||
			byName["unreadable"].Unavailable != "unreadable" || byName["outside"].ID != "" ||
			(hasSymlink && byName["replaced"].ID != "") {
			t.Fatalf("worktrees: %+v", state.Worktrees)
		}
		metadata, err := json.Marshal(state.Worktrees)
		if err != nil || strings.Contains(string(metadata), filepath.ToSlash(dir)) {
			t.Fatalf("metadata includes a checkout path: %s, %v", metadata, err)
		}
		for _, dir := range []string{outside, replaced, taskDir} {
			if dir == replaced && !hasSymlink {
				continue
			}
			if _, err := view(WorkspaceViewerRequest{View: "file", Path: "readme.txt", Worktree: viewerWorktreeID(filepath.ToSlash(dir))}); !errors.Is(err, ErrViewerWorktree) {
				t.Fatalf("out-of-scope worktree: %v", err)
			}
		}
		for _, name := range []string{"missing", "unreadable"} {
			if _, err := view(WorkspaceViewerRequest{View: "state", Worktree: byName[name].ID}); !errors.Is(err, ErrViewerWorktreeMissing) {
				t.Fatalf("%s worktree: %v", name, err)
			}
		}
		mainID := state.Worktrees[0].ID
		main := readState(mainID)
		if main.Base != "" || main.BaselineAvailable || main.Branch != "main" || main.Head != head ||
			main.Ahead != 0 || main.Behind != 0 || len(main.Commits) != 2 || len(main.Uncommitted) != 2 {
			t.Fatalf("main state: %+v", main)
		}
		topicState := readState(byName["topic"].ID)
		if topicState.Base != "main" || topicState.Branch != "topic" || topicState.Ahead != 1 || topicState.Behind != 1 {
			t.Fatalf("topic state: %+v", topicState)
		}
		if again := readState(""); again.Base != base || again.Branch != "task" {
			t.Fatalf("restored task state: %+v", again)
		}
		for _, test := range []struct {
			req  WorkspaceViewerRequest
			text string
		}{
			{WorkspaceViewerRequest{View: "file", Path: "readme.txt"}, "main edit\n"},
			{WorkspaceViewerRequest{View: "diff", Scope: "uncommitted", Path: "readme.txt"}, "+main edit"},
		} {
			test.req.Worktree = mainID
			got, err := view(test.req)
			if err != nil || !strings.Contains(got.(viewerContent).Text, test.text) {
				t.Fatalf("%s: %+v, %v", test.req.View, got, err)
			}
		}
		tree, err := view(WorkspaceViewerRequest{View: "tree", Worktree: mainID})
		if err != nil || slices.ContainsFunc(tree.(viewerTree).Entries, func(entry viewerEntry) bool { return entry.Path == "task-only.txt" }) {
			t.Fatalf("main file tree: %+v, %v", tree, err)
		}
		got, err := view(WorkspaceViewerRequest{View: "file", Path: ".env", Worktree: mainID})
		if err != nil || got != (viewerContent{Placeholder: "masked"}) {
			t.Fatalf("masked file: %+v, %v", got, err)
		}
	})
}

func TestWorkspaceViewerCommitRanges(t *testing.T) {
	testViewerGitSources(t, func(t *testing.T, dir string, source viewerFS) {
		runViewerFixtureGit(t, dir, "init", "-b", "topic")
		for _, value := range []string{"one\n", "one\ntwo\n", "one\ntwo\nthree\n"} {
			writeViewerFixture(t, dir, "readme.txt", value)
			runViewerFixtureGit(t, dir, "add", ".")
			runViewerFixtureGit(t, dir, "commit", "-m", strings.TrimSpace(value))
		}
		base := runViewerFixtureGit(t, dir, "rev-parse", "HEAD^")
		head := runViewerFixtureGit(t, dir, "rev-parse", "HEAD")
		unrelated := runViewerFixtureGit(t, dir, "commit-tree", head+"^{tree}", "-m", "unrelated")
		for _, tc := range []struct {
			name, base  string
			filter      bool
			available   bool
			commitCount int
		}{
			{"full branch with a base", base, false, true, 3},
			{"after base", base, true, true, 1},
			{"base equals HEAD", head, true, true, 0},
			{"no base", "", true, false, 3},
			{"missing base object", strings.Repeat("f", 40), true, false, 3},
			{"unrelated base", unrelated, true, false, 3},
		} {
			t.Run(tc.name, func(t *testing.T) {
				state, err := readViewerState(t.Context(), source, domain.Task{BaseCommit: tc.base},
					"fixture", WorkspaceViewerRequest{BaselineCommits: tc.filter}, viewerRepositories{})
				if err != nil || state.BaselineAvailable != tc.available || len(state.Commits) != tc.commitCount {
					t.Fatalf("range: %+v, %v", state, err)
				}
				if len(state.Commits) != 0 && (state.Commits[0].ID != head ||
					!slices.Equal(state.Commits[0].Files, []viewerChange{{Path: "readme.txt", Added: 1}})) {
					t.Fatalf("commit files: %+v", state.Commits[0])
				}
				if tc.base == base && (state.Ahead != 1 || state.Behind != 0 || len(state.BaseChanges) != 1) {
					t.Fatalf("range changed comparison: %+v", state)
				}
			})
		}
	})
}
