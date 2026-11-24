package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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
