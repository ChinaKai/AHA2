package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	viewerCommitLimit  = 100
	viewerCommitFiles  = 300
	viewerHistorySize  = 8 << 20
	viewerSubjectLimit = 4 << 10
	viewerAuthorLimit  = 1 << 10

	viewerGitConfigTimeout = 500 * time.Millisecond
	viewerGitConfigLimit   = 4096
)

type viewerGitOutput struct {
	data      []byte
	exit      int
	truncated bool
}

func (source nativeViewerFS) git(ctx context.Context, limit int, readLineEndings bool, args ...string) (viewerGitOutput, error) {
	handle, err := source.root.Open(".")
	if err != nil {
		return viewerGitOutput{}, ErrViewerGit
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		return viewerGitOutput{}, ErrViewerGit
	}
	canonical, err := filepath.EvalSymlinks(source.dir)
	actual, statErr := os.Stat(source.dir)
	if err != nil || statErr != nil || canonical != source.dir || !os.SameFile(info, actual) {
		return viewerGitOutput{}, ErrViewerGit
	}
	// Linux can start Git in the opened directory even if a path component
	// changes between validation and the subprocess's chdir.
	dir := source.dir
	if runtime.GOOS == "linux" {
		fdPath := fmt.Sprintf("/proc/self/fd/%d", handle.Fd())
		if fdInfo, err := os.Stat(fdPath); err == nil && os.SameFile(info, fdInfo) {
			dir = fdPath
		}
	}
	var env []string
	for _, item := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(item), "GIT_") {
			env = append(env, item)
		}
	}
	gitDir := ".git"
	if source.gitDir != "" {
		gitDir = source.gitDir
	}
	args = append([]string{"--git-dir=" + gitDir, "-c", "core.hooksPath=" + os.DevNull}, args...)
	if readLineEndings {
		args = append(viewerGitLineEndingArgs(ctx, dir, gitDir, env), args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(env, "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_ATTR_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "GIT_TERMINAL_PROMPT=0")
	return runNativeViewerGit(ctx, cmd, limit)
}

func viewerGitLineEndingArgs(ctx context.Context, dir, gitDir string, env []string) []string {
	// The fixed config query consults normal config files. Preserve explicit
	// config locations for this query only. The diff still runs isolated.
	ctx, cancel := context.WithTimeout(ctx, viewerGitConfigTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--no-pager", "--git-dir="+gitDir, "--work-tree=.",
		"config", "--includes", "--null", "--get-regexp", `^core\.(autocrlf|eol)$`)
	cmd.Dir = dir
	cmd.Env = append([]string(nil), env...)
	for _, key := range []string{"GIT_CONFIG_SYSTEM", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	out, err := runNativeViewerGit(ctx, cmd, viewerGitConfigLimit)
	if err != nil || out.exit != 0 || out.truncated {
		return nil
	}

	// Git reads config scopes in precedence order. Use the last value for
	// each key, and pass it back only if it is an allowed Git enum value.
	var autocrlf, eol string
	for _, record := range out.records() {
		key, value, _ := strings.Cut(record, "\n")
		switch key {
		case "core.autocrlf":
			autocrlf = strings.ToLower(value)
		case "core.eol":
			eol = strings.ToLower(value)
		}
	}
	var args []string
	switch autocrlf {
	case "true", "false", "input":
		args = append(args, "-c", "core.autocrlf="+autocrlf)
	}
	switch eol {
	case "lf", "crlf", "native":
		args = append(args, "-c", "core.eol="+eol)
	}
	return args
}

func runNativeViewerGit(ctx context.Context, cmd *exec.Cmd, limit int) (viewerGitOutput, error) {
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
	command := []string{"--no-pager", "--work-tree=.", "-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false", "-c", "core.quotePath=false", "-c", "color.ui=never",
		// git diff can rewrite the index and invoke post-index-change even
		// with GIT_OPTIONAL_LOCKS=0. These args also reach the remote worker.
		"-c", "diff.autoRefreshIndex=false"}
	command = append(command, git.config...)
	command = append(command, args...)
	readLineEndings := args[0] == "status" || args[0] == "diff"
	return git.source.git(ctx, limit, readLineEndings, command...)
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
	Path    string         `json:"path"`
	Status  string         `json:"status,omitempty"`
	Added   int            `json:"added"`
	Deleted int            `json:"deleted"`
	Binary  bool           `json:"binary,omitempty"`
	Masked  bool           `json:"masked,omitempty"`
	Gitlink *viewerGitlink `json:"gitlink,omitempty"`
}

type viewerCommit struct {
	ID        string         `json:"id"`
	Short     string         `json:"short"`
	Subject   string         `json:"subject"`
	Author    string         `json:"author"`
	Date      int64          `json:"date"`
	Files     []viewerChange `json:"files"`
	Truncated bool           `json:"truncated"`
}

type viewerState struct {
	viewerRepositories
	RepositoryUnavailable bool           `json:"repositoryUnavailable,omitempty"`
	BaseFallback          bool           `json:"baseFallback,omitempty"`
	BaseAtHead            bool           `json:"baseAtHead,omitempty"`
	Name                  string         `json:"name"`
	TaskTitle             string         `json:"taskTitle"`
	TaskCode              string         `json:"taskCode"`
	IsGit                 bool           `json:"isGit"`
	Version               string         `json:"version,omitempty"`
	Branch                string         `json:"branch,omitempty"`
	CheckedOutBranch      string         `json:"checkedOutBranch,omitempty"`
	BranchOptions         []viewerBranch `json:"branchOptions"`
	LocalRefsTruncated    bool           `json:"localBranchesTruncated,omitempty"`
	RemoteRefsTruncated   bool           `json:"remoteBranchesTruncated,omitempty"`
	Head                  string         `json:"head,omitempty"`
	Base                  string         `json:"base,omitempty"`
	MergeBase             string         `json:"mergeBase,omitempty"`
	NoCommonAncestor      bool           `json:"noCommonAncestor,omitempty"`
	Ahead                 int            `json:"ahead"`
	Behind                int            `json:"behind"`
	Branches              []string       `json:"branches"`
	Uncommitted           []viewerChange `json:"uncommitted"`
	BaseChanges           []viewerChange `json:"baseChanges"`
	Commits               []viewerCommit `json:"commits"`
	Truncated             bool           `json:"truncated"`
}

func readViewerState(ctx context.Context, source viewerFS, task domain.Task, name string, req WorkspaceViewerRequest, repos viewerRepositories) (viewerState, error) {
	state := viewerState{Name: name, TaskTitle: task.Title, TaskCode: task.Code, Branches: []string{}, Uncommitted: []viewerChange{}, BaseChanges: []viewerChange{}, Commits: []viewerCommit{}}
	var err error
	state.IsGit, err = source.gitMarker(ctx)
	if err == nil && !state.IsGit && req.Branch != "" {
		return state, ErrViewerNotGit
	}
	if !state.IsGit || err != nil {
		return state, err
	}
	git := viewerGit{source: source}
	hash := sha256.New()
	baselineBranch := ""
	if req.Branch == "" {
		if err := git.disableFilters(ctx); err != nil {
			return state, err
		}
		status, err := git.checked(ctx, viewerDiffLimit, "status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all", "--no-renames", "--ignore-submodules=all")
		if err != nil {
			return state, err
		}
		staged, err := git.stagedRaw(ctx, viewerHistorySize, "--")
		if err != nil {
			return state, err
		}
		hash.Write(status.data)
		hash.Write(staged.data)
		if !req.Summary {
			state.Truncated = status.truncated || staged.truncated
			state.readStatus(status.records())
			state.CheckedOutBranch = state.Branch
			baselineBranch = state.Branch
			if err := state.readUncommittedGitlinks(ctx, git, staged, repos.checkedOutHeads(req.Repo)); err != nil {
				return state, err
			}
		}
	}
	branchCtx, cancelBranches := context.WithTimeout(ctx, viewerBranchTimeout)
	defer cancelBranches()
	if req.Branch != "" {
		state.Head, err = git.selectedBranch(branchCtx, req.Branch)
		if err != nil {
			return state, err
		}
	}
	refs, local, err := git.branches(branchCtx)
	if err != nil {
		return state, err
	}
	remoteOptions, remote, err := git.remoteBranches(branchCtx)
	if err != nil {
		return state, err
	}
	cancelBranches()
	if req.Branch != "" {
		state.CheckedOutBranch, err = git.checkedOutBranch(ctx)
		if err != nil {
			return state, err
		}
		state.Branch = viewerBranchName(req.Branch)
		if name, local := strings.CutPrefix(req.Branch, viewerLocalRefs); local {
			baselineBranch = name
		}
		hash.Write([]byte(state.Head + "\x00" + state.CheckedOutBranch))
	}
	hash.Write(local.data)
	hash.Write(remote.data)
	fmt.Fprintf(hash, "%t%t", local.truncated, remote.truncated)
	state.Version = fmt.Sprintf("%x", hash.Sum(nil))
	if req.Summary {
		return viewerState{IsGit: true, Version: state.Version}, nil
	}
	state.LocalRefsTruncated = local.truncated
	state.RemoteRefsTruncated = remote.truncated
	state.BranchOptions = []viewerBranch{}
	for _, line := range viewerLocalBranchRecords(local.data) {
		if ref, _, ok := strings.Cut(line, "\x00"); ok {
			state.Branches = append(state.Branches, viewerBranchName(ref))
		}
		if ref, _, ok := readViewerBranchRecord(line); ok {
			state.BranchOptions = append(state.BranchOptions, viewerBranch{Ref: ref, Name: viewerBranchName(ref)})
		}
	}
	state.BranchOptions = append(state.BranchOptions, remoteOptions...)
	base, baseline, fallback, err := git.comparisonBase(ctx, task, refs, baselineBranch, state.Head, req)
	if err != nil {
		return state, err
	}
	state.Base = base
	state.BaseFallback = fallback
	state.BaseAtHead = req.Repo != "" && base == "HEAD" && baseline == ""
	if state.Head == "" {
		return state, nil
	}

	history := state.Head
	if baseline != "" {
		state.MergeBase, err = git.mergeBase(ctx, baseline, state.Head)
		if err != nil {
			return state, err
		}
		state.NoCommonAncestor = req.Branch != "" && state.MergeBase == ""
		if !state.NoCommonAncestor {
			history = baseline + ".." + state.Head
			counts, err := git.checked(ctx, 1024, "rev-list", "--left-right", "--count", baseline+"..."+state.Head)
			if err != nil {
				return state, err
			}
			_, _ = fmt.Sscan(string(counts.data), &state.Behind, &state.Ahead)
		}
		if state.MergeBase != "" {
			out, err := git.checked(ctx, viewerHistorySize, "diff", "--raw", "--numstat", "-z", "--no-abbrev", "--no-renames", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--submodule=short", state.MergeBase, state.Head, "--")
			if err != nil {
				return state, err
			}
			state.BaseChanges, state.Truncated = viewerChanges(out.records(), viewerListLimit, state.Truncated || out.truncated)
		}
	}

	log, err := git.checked(ctx, viewerHistorySize, "log", "--no-show-signature", "--max-count="+strconv.Itoa(viewerCommitLimit+1),
		"--raw", "--numstat", "-z", "--no-abbrev", "--no-renames", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--submodule=short",
		"--diff-merges=first-parent", "--format=tformat:%x1e%H%x00%ct%x00%an%x00%s", history, "--")
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
	var gitlinks map[string]*viewerGitlink
	for i := 0; i < len(records); i++ {
		value := records[i]
		if hash, ok := strings.CutPrefix(value, "\x1e"); ok && validViewerSHA(hash) {
			if i+3 >= len(records) || len(state.Commits) == viewerCommitLimit {
				state.Truncated = true
				return
			}
			date, _ := strconv.ParseInt(records[i+1], 10, 64)
			state.Commits = append(state.Commits, viewerCommit{
				ID: hash, Short: hash[:7], Date: date, Files: []viewerChange{},
				Author: viewerLimitCommitText(records[i+2], viewerAuthorLimit), Subject: viewerLimitCommitText(records[i+3], viewerSubjectLimit),
			})
			item = &state.Commits[len(state.Commits)-1]
			gitlinks = map[string]*viewerGitlink{}
			i += 3
		} else if item != nil {
			if viewerReadRaw(records, &i, gitlinks) {
				continue
			}
			if change, ok := viewerStat(value, gitlinks); ok {
				if len(item.Files) == viewerCommitFiles {
					item.Truncated = true
				} else {
					item.Files = append(item.Files, change)
				}
			}
		}
	}
}

func viewerLimitCommitText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	count, end := 0, 0
	for offset := range value {
		if count == limit {
			return value[:end] + "…"
		}
		end = offset
		count++
	}
	return value
}

func viewerChanges(records []string, limit int, truncated bool) ([]viewerChange, bool) {
	changes := []viewerChange{}
	gitlinks := map[string]*viewerGitlink{}
	for i := 0; i < len(records); i++ {
		if viewerReadRaw(records, &i, gitlinks) {
			continue
		}
		if change, ok := viewerStat(records[i], gitlinks); ok {
			if len(changes) == limit {
				return changes, true
			}
			changes = append(changes, change)
		}
	}
	return changes, truncated
}

// Raw records are followed by a NUL-delimited path, even for regular
// files. Consume it here so a path can never be parsed as a commit header.
func viewerReadRaw(records []string, i *int, gitlinks map[string]*viewerGitlink) bool {
	header := strings.TrimPrefix(records[*i], "\n")
	if !strings.HasPrefix(header, ":") {
		return false
	}
	*i = *i + 1
	if *i < len(records) {
		rel := records[*i]
		if link := viewerRawGitlink(header, rel); link != nil {
			gitlinks[rel] = link
		}
	}
	return true
}

func viewerStat(record string, gitlinks map[string]*viewerGitlink) (viewerChange, bool) {
	fields := strings.SplitN(strings.TrimPrefix(record, "\n"), "\t", 3)
	if len(fields) != 3 || !validViewerPath(fields[2], false) {
		return viewerChange{}, false
	}
	change := viewerChange{Path: fields[2], Masked: viewerSensitivePath(fields[2])}
	if !change.Masked {
		change.Gitlink = gitlinks[change.Path]
		change.Added, _ = strconv.Atoi(fields[0])
		change.Deleted, _ = strconv.Atoi(fields[1])
		change.Binary = fields[0] == "-"
	}
	return change, true
}

func (git viewerGit) branches(ctx context.Context) (map[string]string, viewerGitOutput, error) {
	out, err := git.checked(ctx, 512<<10, "for-each-ref", "--sort=refname", "--count="+strconv.Itoa(viewerBranchLimit+1),
		viewerRefFormat, viewerLocalRefs)
	if err != nil {
		return nil, out, err
	}
	records := viewerBranchRecords(out.data)
	out.truncated = out.truncated || len(records) > viewerBranchLimit
	refs := map[string]string{}
	for _, line := range records[:min(len(records), viewerBranchLimit)] {
		ref, rest, ok := strings.Cut(line, "\x00")
		id, _, separated := strings.Cut(rest, "\x00")
		if ok && separated && validViewerSHA(id) {
			refs[viewerBranchName(ref)] = id
		}
	}
	return refs, out, nil
}

func viewerLocalBranchRecords(data []byte) []string {
	records := viewerBranchRecords(data)
	return records[:min(len(records), viewerBranchLimit)]
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

func (git viewerGit) comparisonBase(ctx context.Context, task domain.Task, refs map[string]string, branch, head string, req WorkspaceViewerRequest) (string, string, bool, error) {
	if req.Repo == "" {
		name, id, err := git.baseline(ctx, task, refs, branch, req.Base)
		return name, id, false, err
	}
	if id := refs[req.Base]; req.Base != "" && id != "" {
		return req.Base, id, false, nil
	}
	fallback := req.Base != ""
	for _, name := range []string{task.TargetBranch, "main", "master", "trunk"} {
		if id := refs[name]; name != "" && name != branch && id != "" && id != head {
			return name, id, fallback, nil
		}
	}
	// Keep showing recent history when HEAD is the only useful baseline.
	if head != "" {
		return "HEAD", "", fallback, nil
	}
	return "", "", fallback, nil
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

func readViewerDiff(ctx context.Context, source viewerFS, task domain.Task, req WorkspaceViewerRequest, repos viewerRepositories) (viewerContent, error) {
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
	git := viewerGit{source: source}
	var head string
	if req.Branch == "" {
		if viewerSensitivePath(req.Path) {
			return viewerContent{Placeholder: "masked"}, nil
		}
		head, err = git.head(ctx)
	}
	branchCtx, cancelBranches := context.WithTimeout(ctx, viewerBranchTimeout)
	defer cancelBranches()
	if req.Branch != "" {
		if req.Scope == "uncommitted" {
			return viewerContent{}, ErrViewerBranch
		}
		head, err = git.selectedBranch(branchCtx, req.Branch)
	}
	if err != nil {
		return viewerContent{}, err
	}
	if viewerSensitivePath(req.Path) {
		return viewerContent{Placeholder: "masked"}, nil
	}
	if req.Scope != "base" {
		cancelBranches()
	}

	args := []string{"--no-ext-diff", "--no-textconv", "--no-renames", "--submodule=short", "--no-color", "--unified=3",
		"--src-prefix=a/", "--dst-prefix=b/"}
	if req.IgnoreWhitespace {
		args = append(args, "--ignore-all-space")
	}
	// Literal paths may also match a directory. Exclude its descendants so
	// this request yields exactly one file, even across directory/file swaps.
	// A literal path with a trailing slash also excludes the gitlink itself.
	escaped := strings.NewReplacer("*", `\*`, "?", `\?`, "[", `\[`, "]", `\]`).Replace(req.Path)
	paths := []string{"--", ":(top,literal)" + req.Path, ":(top,exclude,glob)" + escaped + "/**"}
	var command []string
	var pointerPreview string
	switch req.Scope {
	case "uncommitted":
		tracked, err := git.checked(ctx, viewerDiffLimit, append([]string{"ls-files", "-z", "--stage"}, paths...)...)
		if err != nil {
			return viewerContent{}, err
		}
		staged, err := git.stagedRaw(ctx, viewerDiffLimit, paths...)
		if err != nil {
			return viewerContent{}, err
		}
		records := staged.records()
		var link *viewerGitlink
		if len(records) >= 2 && records[1] == req.Path {
			link = viewerRawGitlink(records[0], req.Path)
		}
		for _, record := range tracked.records() {
			rel, index := viewerIndexGitlink(record)
			if index == "" || rel != req.Path {
				continue
			}
			if len(repos.Items) == 0 {
				repos, err = source.repositories(ctx)
				if err != nil {
					return viewerContent{}, err
				}
			}
			link = viewerWorktreeGitlink(link, index, repos.checkedOutHeads(req.Repo)[rel])
		}
		if link != nil {
			pointerPreview = link.preview()
		}
		if len(tracked.data) == 0 && len(staged.data) == 0 {
			return readViewerUntracked(ctx, source, req.Path)
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
		command = append(command, "--ignore-submodules=all", head)
	case "base":
		refs, _, err := git.branches(branchCtx)
		cancelBranches()
		if err != nil {
			return viewerContent{}, err
		}
		branch := ""
		if req.Branch != "" {
			if name, local := strings.CutPrefix(req.Branch, viewerLocalRefs); local {
				branch = name
			}
		} else if req.Base == "" || req.Repo != "" {
			out, err := git.run(ctx, 4096, "symbolic-ref", "--quiet", "--short", "HEAD")
			if err != nil {
				return viewerContent{}, err
			}
			branch = strings.TrimSpace(string(out.data))
		}
		_, baseline, _, err := git.comparisonBase(ctx, task, refs, branch, head, req)
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
		command = append(append([]string{"diff"}, args...), "--ignore-submodules=none", merge, head)
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
		command = append(append([]string{"show", "--format=", "--no-show-signature", "--root", "--diff-merges=first-parent"}, args...), "--ignore-submodules=none", req.Commit)
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
	return viewerContent{Text: pointerPreview + string(out.data), Truncated: out.truncated}, nil
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
