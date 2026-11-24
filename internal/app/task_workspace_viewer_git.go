package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	viewerCommitLimit = 100
	viewerCommitFiles = 300
	viewerHistorySize = 8 << 20
)

type viewerGitOutput struct {
	data      []byte
	exit      int
	truncated bool
}

func (source nativeViewerFS) git(ctx context.Context, limit int, args ...string) (viewerGitOutput, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = source.dir
	for _, item := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(item), "GIT_") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	cmd.Env = append(cmd.Env, "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_ATTR_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "GIT_TERMINAL_PROMPT=0")
	stdout, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return viewerGitOutput{}, ErrViewerGit
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, int64(limit)+1))
	truncated := len(data) > limit
	if truncated || readErr != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
	if ctx.Err() != nil || readErr != nil {
		return viewerGitOutput{}, ErrViewerGit
	}
	return viewerGitOutput{data: data[:min(len(data), limit)], exit: cmd.ProcessState.ExitCode(), truncated: truncated}, nil
}

type viewerGit struct {
	source viewerFS
	config []string
}

func (git viewerGit) run(ctx context.Context, limit int, args ...string) (viewerGitOutput, error) {
	command := []string{"--no-pager", "--git-dir=.git", "--work-tree=.", "-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false", "-c", "core.quotePath=false", "-c", "color.ui=never"}
	command = append(command, git.config...)
	command = append(command, args...)
	return git.source.git(ctx, limit, command...)
}

func (git viewerGit) checked(ctx context.Context, limit int, args ...string) (viewerGitOutput, error) {
	out, err := git.run(ctx, limit, args...)
	if err == nil && out.exit != 0 && !out.truncated {
		err = ErrViewerGit
	}
	return out, err
}

var viewerFilterKey = regexp.MustCompile(`(?i)^filter\..+\.(clean|smudge|process|required)$`)

func (git *viewerGit) disableFilters(ctx context.Context) error {
	// status and worktree diff may otherwise start repository-configured
	// filters. Empty command values and required=false disable each driver.
	out, err := git.run(ctx, 128<<10, "config", "--null", "--name-only", "--get-regexp", `^filter\.`)
	if err != nil {
		return err
	}
	if out.truncated || (out.exit != 0 && out.exit != 1) {
		return ErrViewerGit
	}
	seen := map[string]bool{}
	for key := range strings.SplitSeq(string(out.data), "\x00") {
		if viewerFilterKey.MatchString(key) && !seen[key] {
			if strings.Contains(key, "=") {
				return ErrViewerGit
			}
			seen[key] = true
			value := ""
			if strings.HasSuffix(strings.ToLower(key), ".required") {
				value = "false"
			}
			git.config = append(git.config, "-c", key+"="+value)
		}
	}
	return nil
}

type viewerChange struct {
	Path    string `json:"path"`
	Status  string `json:"status,omitempty"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary,omitempty"`
	Masked  bool   `json:"masked,omitempty"`
}

type viewerCommit struct {
	ID        string         `json:"id"`
	Short     string         `json:"short"`
	Subject   string         `json:"subject"`
	Date      int64          `json:"date"`
	Files     []viewerChange `json:"files"`
	Truncated bool           `json:"truncated"`
}

type viewerState struct {
	Name        string         `json:"name"`
	TaskTitle   string         `json:"taskTitle"`
	TaskCode    string         `json:"taskCode"`
	IsGit       bool           `json:"isGit"`
	Version     string         `json:"version,omitempty"`
	Branch      string         `json:"branch,omitempty"`
	Head        string         `json:"head,omitempty"`
	Base        string         `json:"base,omitempty"`
	MergeBase   string         `json:"mergeBase,omitempty"`
	Ahead       int            `json:"ahead"`
	Behind      int            `json:"behind"`
	Branches    []string       `json:"branches"`
	Uncommitted []viewerChange `json:"uncommitted"`
	BaseChanges []viewerChange `json:"baseChanges"`
	Commits     []viewerCommit `json:"commits"`
	Truncated   bool           `json:"truncated"`
}

func readViewerState(ctx context.Context, source viewerFS, task domain.Task, name string, req WorkspaceViewerRequest) (viewerState, error) {
	state := viewerState{Name: name, TaskTitle: task.Title, TaskCode: task.Code, Branches: []string{}, Uncommitted: []viewerChange{}, BaseChanges: []viewerChange{}, Commits: []viewerCommit{}}
	var err error
	state.IsGit, err = source.gitMarker(ctx)
	if !state.IsGit || err != nil {
		return state, err
	}
	git := viewerGit{source: source}
	if err := git.disableFilters(ctx); err != nil {
		return state, err
	}
	status, err := git.checked(ctx, viewerDiffLimit, "status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all", "--no-renames", "--ignore-submodules=all")
	if err != nil {
		return state, err
	}
	refs, rawRefs, err := git.branches(ctx)
	if err != nil {
		return state, err
	}
	hash := sha256.New()
	hash.Write(status.data)
	hash.Write(rawRefs)
	state.Version = fmt.Sprintf("%x", hash.Sum(nil))
	if req.Summary {
		return viewerState{IsGit: true, Version: state.Version}, nil
	}
	state.Truncated = status.truncated
	state.readStatus(status.records())
	for name := range strings.SplitSeq(string(rawRefs), "\n") {
		if branch, _, ok := strings.Cut(name, "\x00"); ok {
			state.Branches = append(state.Branches, branch)
		}
	}
	base, baseline, err := git.baseline(ctx, task, refs, state.Branch, req.Base)
	if err != nil {
		return state, err
	}
	state.Base = base
	if state.Head == "" {
		return state, nil
	}

	history := state.Head
	if baseline != "" {
		history = baseline + ".." + state.Head
		counts, err := git.checked(ctx, 1024, "rev-list", "--left-right", "--count", baseline+"..."+state.Head)
		if err != nil {
			return state, err
		}
		_, _ = fmt.Sscan(string(counts.data), &state.Behind, &state.Ahead)
		state.MergeBase, err = git.mergeBase(ctx, baseline, state.Head)
		if err != nil {
			return state, err
		}
		if state.MergeBase != "" {
			out, err := git.checked(ctx, viewerHistorySize, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", "--ignore-submodules=all", state.MergeBase, state.Head, "--")
			if err != nil {
				return state, err
			}
			state.BaseChanges, state.Truncated = viewerChanges(out.records(), viewerListLimit, state.Truncated || out.truncated)
		}
	}

	log, err := git.checked(ctx, viewerHistorySize, "log", "--no-show-signature", "--max-count="+strconv.Itoa(viewerCommitLimit+1),
		"--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", "--ignore-submodules=all",
		"--diff-merges=first-parent", "--format=tformat:%x1e%H%x00%h%x00%ct%x00%s", history, "--")
	if err != nil {
		return state, err
	}
	state.readLog(log.records())
	state.Truncated = state.Truncated || log.truncated
	return state, nil
}

func (out viewerGitOutput) records() []string {
	parts := strings.Split(string(out.data), "\x00")
	// The final item is empty or an incomplete record.
	return parts[:len(parts)-1]
}

func (state *viewerState) readStatus(records []string) {
	for _, record := range records {
		switch {
		case strings.HasPrefix(record, "# branch.oid "):
			value := strings.TrimPrefix(record, "# branch.oid ")
			if validViewerSHA(value) {
				state.Head = value
			}
		case strings.HasPrefix(record, "# branch.head "):
			state.Branch = strings.TrimPrefix(record, "# branch.head ")
			if state.Branch == "(detached)" {
				state.Branch = ""
			}
		case len(record) > 2:
			kind := record[0]
			var rel, status string
			switch kind {
			case '?':
				rel, status = record[2:], "??"
			case '1', 'u':
				fields := 9
				if kind == 'u' {
					fields = 11
				}
				parts := strings.SplitN(record, " ", fields)
				if len(parts) == fields {
					rel, status = parts[fields-1], parts[1]
				}
			}
			if validViewerPath(rel, false) {
				if len(state.Uncommitted) == viewerListLimit {
					state.Truncated = true
					return
				}
				state.Uncommitted = append(state.Uncommitted, viewerChange{Path: rel, Status: status, Masked: viewerSensitivePath(rel)})
			}
		}
	}
}

func (state *viewerState) readLog(records []string) {
	var item *viewerCommit
	for i := 0; i < len(records); i++ {
		value := records[i]
		if hash, ok := strings.CutPrefix(value, "\x1e"); ok && validViewerSHA(hash) {
			if i+3 >= len(records) || len(state.Commits) == viewerCommitLimit {
				state.Truncated = true
				return
			}
			date, _ := strconv.ParseInt(records[i+2], 10, 64)
			subject := records[i+3]
			if len(subject) > 1000 {
				subject = subject[:1000]
			}
			state.Commits = append(state.Commits, viewerCommit{ID: hash, Short: records[i+1], Date: date, Subject: subject, Files: []viewerChange{}})
			item = &state.Commits[len(state.Commits)-1]
			i += 3
		} else if item != nil {
			if change, ok := viewerStat(value); ok {
				if len(item.Files) == viewerCommitFiles {
					item.Truncated = true
				} else {
					item.Files = append(item.Files, change)
				}
			}
		}
	}
}

func viewerChanges(records []string, limit int, truncated bool) ([]viewerChange, bool) {
	changes := []viewerChange{}
	for _, record := range records {
		if change, ok := viewerStat(record); ok {
			if len(changes) == limit {
				return changes, true
			}
			changes = append(changes, change)
		}
	}
	return changes, truncated
}

func viewerStat(record string) (viewerChange, bool) {
	fields := strings.SplitN(strings.TrimPrefix(record, "\n"), "\t", 3)
	if len(fields) != 3 || !validViewerPath(fields[2], false) {
		return viewerChange{}, false
	}
	change := viewerChange{Path: fields[2], Masked: viewerSensitivePath(fields[2])}
	if !change.Masked {
		change.Added, _ = strconv.Atoi(fields[0])
		change.Deleted, _ = strconv.Atoi(fields[1])
		change.Binary = fields[0] == "-"
	}
	return change, true
}

func (git viewerGit) branches(ctx context.Context) (map[string]string, []byte, error) {
	out, err := git.checked(ctx, 512<<10, "for-each-ref", "--sort=refname", "--count=500",
		"--format=%(refname:strip=2)%00%(objectname)", "refs/heads")
	if err != nil {
		return nil, nil, err
	}
	refs := map[string]string{}
	for line := range strings.SplitSeq(string(out.data), "\n") {
		if name, id, ok := strings.Cut(line, "\x00"); ok && validViewerSHA(id) {
			refs[name] = id
		}
	}
	return refs, out.data, nil
}

func (git viewerGit) baseline(ctx context.Context, task domain.Task, refs map[string]string, branch, requested string) (string, string, error) {
	if id := refs[requested]; requested != "" && id != "" {
		return requested, id, nil
	}
	if requested != "" && requested != task.BaseCommit {
		return "", "", ErrViewerRevision
	}
	if validViewerSHA(task.BaseCommit) {
		out, err := git.run(ctx, 1024, "rev-parse", "--verify", "--quiet", "--end-of-options", task.BaseCommit+"^{commit}")
		if err != nil {
			return "", "", err
		}
		if id := strings.TrimSpace(string(out.data)); out.exit == 0 && validViewerSHA(id) {
			return task.BaseCommit, id, nil
		}
	}
	if requested != "" {
		return "", "", ErrViewerRevision
	}
	for _, name := range []string{task.TargetBranch, "main", "master", "trunk"} {
		if id := refs[name]; name != "" && name != branch && id != "" {
			return name, id, nil
		}
	}
	return "", "", nil
}

func (git viewerGit) mergeBase(ctx context.Context, base, head string) (string, error) {
	out, err := git.run(ctx, 1024, "merge-base", base, head)
	if err != nil {
		return "", err
	}
	if out.exit == 1 {
		return "", nil // Unrelated histories.
	}
	value := strings.TrimSpace(string(out.data))
	if out.exit != 0 || !validViewerSHA(value) {
		return "", ErrViewerGit
	}
	return value, nil
}

func validViewerSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f')
	}) == -1
}

func (git viewerGit) head(ctx context.Context) (string, error) {
	out, err := git.run(ctx, 1024, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return "", err
	}
	if out.exit == 1 {
		return "", nil // Unborn branch.
	}
	value := strings.TrimSpace(string(out.data))
	if out.exit != 0 || !validViewerSHA(value) {
		return "", ErrViewerGit
	}
	return value, nil
}

func readViewerDiff(ctx context.Context, source viewerFS, task domain.Task, req WorkspaceViewerRequest) (viewerContent, error) {
	if req.Scope != "uncommitted" && req.Scope != "base" && req.Scope != "commit" {
		return viewerContent{}, ErrViewerRevision
	}
	isGit, err := source.gitMarker(ctx)
	if err != nil {
		return viewerContent{}, err
	}
	if !isGit {
		return viewerContent{}, ErrViewerNotGit
	}
	if viewerSensitivePath(req.Path) {
		return viewerContent{Placeholder: "masked"}, nil
	}
	git := viewerGit{source: source}
	head, err := git.head(ctx)
	if err != nil {
		return viewerContent{}, err
	}

	args := []string{"--no-ext-diff", "--no-textconv", "--no-renames", "--ignore-submodules=all", "--no-color", "--unified=3",
		"--src-prefix=a/", "--dst-prefix=b/"}
	if req.IgnoreWhitespace {
		args = append(args, "--ignore-all-space")
	}
	// Literal paths may also match a directory. Exclude its descendants so
	// this request yields exactly one file, even across directory/file swaps.
	paths := []string{"--", ":(top,literal)" + req.Path, ":(top,exclude,literal)" + req.Path + "/"}
	var command []string
	switch req.Scope {
	case "uncommitted":
		tracked, err := git.checked(ctx, viewerDiffLimit, "ls-files", "-z", "--cached", paths[0], paths[1], paths[2])
		if err != nil {
			return viewerContent{}, err
		}
		if len(tracked.data) == 0 {
			if head != "" {
				prior, err := git.checked(ctx, viewerDiffLimit, "--literal-pathspecs", "ls-tree", "-z", "--full-tree", head, "--", req.Path)
				if err != nil {
					return viewerContent{}, err
				}
				if len(prior.data) == 0 {
					return readViewerUntracked(ctx, source, req.Path)
				}
			} else {
				return readViewerUntracked(ctx, source, req.Path)
			}
		}
		if err := git.disableFilters(ctx); err != nil {
			return viewerContent{}, err
		}
		command = append([]string{"diff"}, args...)
		if head == "" {
			out, err := git.checked(ctx, 1024, "hash-object", "-t", "tree", "--stdin")
			if err != nil {
				return viewerContent{}, err
			}
			head = strings.TrimSpace(string(out.data))
			if !validViewerSHA(head) {
				return viewerContent{}, ErrViewerGit
			}
		}
		command = append(command, head)
	case "base":
		refs, _, err := git.branches(ctx)
		if err != nil {
			return viewerContent{}, err
		}
		branch := ""
		if req.Base == "" {
			out, err := git.run(ctx, 4096, "symbolic-ref", "--quiet", "--short", "HEAD")
			if err != nil {
				return viewerContent{}, err
			}
			branch = strings.TrimSpace(string(out.data))
		}
		_, baseline, err := git.baseline(ctx, task, refs, branch, req.Base)
		if err != nil {
			return viewerContent{}, err
		}
		if head == "" || baseline == "" {
			return viewerContent{}, ErrViewerRevision
		}
		merge, err := git.mergeBase(ctx, baseline, head)
		if err != nil {
			return viewerContent{}, err
		}
		if merge == "" {
			return viewerContent{}, ErrViewerRevision
		}
		command = append(append([]string{"diff"}, args...), merge, head)
	case "commit":
		if head == "" || !validViewerSHA(req.Commit) {
			return viewerContent{}, ErrViewerRevision
		}
		reachable, err := git.run(ctx, 1024, "merge-base", "--is-ancestor", req.Commit, head)
		if err != nil {
			return viewerContent{}, err
		}
		if reachable.exit != 0 {
			return viewerContent{}, ErrViewerRevision
		}
		command = append(append([]string{"show", "--format=", "--no-show-signature", "--root", "--diff-merges=first-parent"}, args...), req.Commit)
	}
	out, err := git.checked(ctx, viewerDiffLimit, append(command, paths...)...)
	if err != nil {
		return viewerContent{}, err
	}
	if out.truncated {
		if last := bytes.LastIndexByte(out.data, '\n'); last >= 0 {
			out.data = out.data[:last+1]
		}
	}
	return viewerContent{Text: string(out.data), Truncated: out.truncated}, nil
}

func readViewerUntracked(ctx context.Context, source viewerFS, rel string) (viewerContent, error) {
	content, err := source.file(ctx, rel)
	if err != nil || content.Placeholder != "" {
		return content, err
	}
	if content.Text == "" {
		return viewerContent{Text: "new file"}, nil
	}
	lineCount := strings.Count(content.Text, "\n")
	if !strings.HasSuffix(content.Text, "\n") {
		lineCount++
	}
	var diff strings.Builder
	fmt.Fprintf(&diff, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", strconv.Quote(rel), lineCount)
	for line := 0; line < min(lineCount, viewerLineLimit); line++ {
		value, rest, ended := strings.Cut(content.Text, "\n")
		diff.WriteByte('+')
		diff.WriteString(value)
		diff.WriteByte('\n')
		if !ended {
			diff.WriteString("\\ No newline at end of file\n")
		}
		content.Text = rest
	}
	return viewerContent{Text: diff.String(), Truncated: lineCount > viewerLineLimit}, nil
}
