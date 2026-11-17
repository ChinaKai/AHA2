package knowledgevault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Manual Git management is deliberately small and boring: every command runs
// with a fixed argument list, cwd set to the vault and a hard timeout. There is
// no shell, no force, no merge and no reset. The only supported remotes are SSH
// (or local paths in tests), so the owner's normal SSH agent/default key is the
// single authentication path.

const (
	defaultGitBranch   = "main"
	gitCommandTimeout  = 45 * time.Second
	gitCommitIdentity  = "AHA2"
	gitCommitEmail     = "aha2@localhost"
	gitIgnoreBody      = "# Local index/state is regenerated from the vault files.\n.aha/\n.obsidian/\n*.tmp\n.DS_Store\nThumbs.db\n"
	gitSyncStateFile   = "git-sync.json"
	gitCommitMessage   = "Update knowledge vault from AHA2 Web"
	maxGitMessageRunes = 200
)

var (
	ErrGitUnavailable         = errors.New("git is not available")
	ErrNotGitRepository       = errors.New("vault is not a git repository")
	ErrGitRemoteMissing       = errors.New("git remote is not configured")
	ErrGitRemoteBranchMissing = errors.New("git remote branch does not exist")
	ErrGitDirty               = errors.New("git worktree has uncommitted changes")
	ErrGitDiverged            = errors.New("git branches have diverged")
	ErrGitPushRejected        = errors.New("git push was rejected")
	ErrGitNoCommit            = errors.New("git repository has no commit")
	ErrGitCommand             = errors.New("git command failed")
)

// GitValidationError marks input the API must reject before starting git.
type GitValidationError struct {
	Field   string
	Message string
}

func (e *GitValidationError) Error() string { return e.Message }

// GitCommandError carries a short, redacted first-line summary. Raw command
// arguments and environment never leave this package.
type GitCommandError struct {
	Op     string
	Detail string
}

func (e *GitCommandError) Error() string {
	if e.Detail == "" {
		return "git " + e.Op + " 失败"
	}
	return "git " + e.Op + " 失败：" + e.Detail
}

func (e *GitCommandError) Unwrap() error { return ErrGitCommand }

// GitStatus is the read model for the settings Git section. Remote stays
// masked for backwards compatibility. RemoteFull carries the SSH remote (user,
// host and repository path only, never credentials) so the authenticated
// settings page can show and copy the repository the owner configured.
type GitStatus struct {
	Available     bool   `json:"available"`
	Initialized   bool   `json:"initialized"`
	Branch        string `json:"branch"`
	CurrentBranch string `json:"current_branch,omitempty"`
	Remote        string `json:"remote,omitempty"`
	RemoteFull    string `json:"remote_full,omitempty"`
	RemoteSet     bool   `json:"remote_set"`
	Clean         bool   `json:"clean"`
	Changed       int    `json:"changed"`
	Tracking      bool   `json:"tracking"`
	Ahead         int    `json:"ahead"`
	Behind        int    `json:"behind"`
	HasCommits    bool   `json:"has_commits"`
	LastCommit    string `json:"last_commit,omitempty"`
	LastCommitMsg string `json:"last_commit_message,omitempty"`
	LastCommitAt  string `json:"last_commit_at,omitempty"`
	LastSyncAt    string `json:"last_sync_at,omitempty"`
}

// GitOpResult returns the refreshed status so the page never has to follow an
// operation with a second status request.
type GitOpResult struct {
	Status  GitStatus `json:"status"`
	Message string    `json:"message,omitempty"`
	Created bool      `json:"created,omitempty"`
}

type Git struct {
	vaultPath        string
	allowLocalRemote bool
	timeout          time.Duration
	// mu serializes every mutating git operation (manual sync, auto commit,
	// renormalize, init) for this vault. It is shared by all Git views that
	// Manager.Git returns. Nil means "standalone view" (read-only tests).
	mu *sync.Mutex
	// manager lets auto-commit wait for an in-flight vault write before it
	// reads the worktree. It is nil for standalone views.
	manager *Manager
}

// Git returns a view over the vault directory. Tests enable local remotes
// through Config.AllowLocalRemote; production keeps the SSH-only rule.
func (m *Manager) Git() *Git {
	return &Git{
		vaultPath: m.config.VaultPath, allowLocalRemote: m.config.AllowLocalRemote,
		timeout: gitCommandTimeout, mu: &m.gitMu, manager: m,
	}
}

// lockRepo takes the vault-wide git mutex. Every mutating entry point uses it
// so a manual push and the background auto-sync can never run concurrently.
func (g *Git) lockRepo() func() {
	if g.mu == nil {
		return func() {}
	}
	g.mu.Lock()
	return g.mu.Unlock
}

// lockWriterBlocks new git work until no knowledge write holds the manager
// write lock. Writes publish files with an atomic rename, so this closes the
// remaining window where a multi-file skill update could be committed half
// written.
func (g *Git) lockWriterBlocks() func() {
	if g.manager == nil {
		return func() {}
	}
	g.manager.mu.RLock()
	return g.manager.mu.RUnlock
}

func (g *Git) gitBinary() (string, error) {
	path, err := exec.LookPath("git")
	if err != nil || strings.TrimSpace(path) == "" {
		return "", ErrGitUnavailable
	}
	return path, nil
}

func (g *Git) command(ctx context.Context, op string, args ...string) (string, error) {
	text, err := g.commandRaw(ctx, op, args...)
	return strings.TrimSpace(text), err
}

// commandRaw leaves the output untouched. Diff parsing needs the leading
// space of context lines and the exact last line, so it cannot use the
// trimmed convenience wrapper used by status/log commands.
func (g *Git) commandRaw(ctx context.Context, op string, args ...string) (string, error) {
	return g.commandInputRaw(ctx, op, "", args...)
}

// commandInputRaw is commandRaw with stdin support. The Recent change feed
// feeds hundreds of vault-relative paths to one `git hash-object --stdin-paths`
// process instead of starting git once per document.
func (g *Git) commandInputRaw(ctx context.Context, op, input string, args ...string) (string, error) {
	gitPath, err := g.gitBinary()
	if err != nil {
		return "", err
	}
	timeout := g.timeout
	if timeout <= 0 {
		timeout = gitCommandTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, gitPath, append([]string{"-C", g.vaultPath, op}, args...)...)
	command.Env = gitEnvironment()
	if input != "" {
		command.Stdin = strings.NewReader(input)
	}
	output, runErr := command.CombinedOutput()
	text := string(output)
	if runErr == nil {
		return text, nil
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return text, &GitCommandError{Op: op, Detail: "操作超时"}
	}
	return text, &GitCommandError{Op: op, Detail: sanitizeGitOutput(text)}
}

func gitEnvironment() []string {
	env := append([]string{}, os.Environ()...)
	env = setGitEnv(env, "GIT_AUTHOR_NAME", gitCommitIdentity)
	env = setGitEnv(env, "GIT_AUTHOR_EMAIL", gitCommitEmail)
	env = setGitEnv(env, "GIT_COMMITTER_NAME", gitCommitIdentity)
	env = setGitEnv(env, "GIT_COMMITTER_EMAIL", gitCommitEmail)
	env = setGitEnv(env, "GIT_TERMINAL_PROMPT", "0")
	env = setGitEnv(env, "SSH_ASKPASS_REQUIRE", "never")
	if strings.TrimSpace(os.Getenv("GIT_SSH_COMMAND")) == "" {
		// BatchMode makes a missing key/agent fail immediately instead of
		// waiting for an interactive prompt the Web process can never answer.
		env = setGitEnv(env, "GIT_SSH_COMMAND", "ssh -o BatchMode=yes -o ConnectTimeout=15")
	}
	return env
}

func setGitEnv(env []string, key, value string) []string {
	prefix := key + "="
	found := false
	for index, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[index] = prefix + value
			found = true
		}
	}
	if !found {
		env = append(env, prefix+value)
	}
	return env
}

var credentialURLPattern = regexp.MustCompile(`(?i)\b(https?|ftp)://[^@\s/]+@`)

func sanitizeGitOutput(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	value = credentialURLPattern.ReplaceAllString(value, "$1://***@")
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) > 320 {
		runes := []rune(value)
		value = string(runes[:320]) + "…"
	}
	return value
}

func redactGitError(err error, remote string) error {
	var commandErr *GitCommandError
	if err == nil || remote == "" || !errors.As(err, &commandErr) {
		return err
	}
	copy := *commandErr
	copy.Detail = strings.ReplaceAll(copy.Detail, remote, MaskGitRemote(remote))
	return &copy
}

// MaskGitRemote hides the repository path while keeping user/host visible so
// the owner can still tell which remote is configured.
func MaskGitRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(remote), "ssh://") {
		if parsed, err := url.Parse(remote); err == nil {
			host := parsed.Host
			if parsed.User != nil && parsed.User.Username() != "" {
				host = parsed.User.Username() + "@" + host
			}
			return "ssh://" + host + "/…"
		}
		return "ssh://…"
	}
	if at := strings.Index(remote, "@"); at > 0 {
		rest := remote[at+1:]
		if colon := strings.Index(rest, ":"); colon > 0 {
			return remote[:at+1] + rest[:colon] + ":…"
		}
	}
	return "…"
}

// FullGitRemote returns the complete SSH remote for the authenticated settings
// page: user, host and repository path only. Non-SSH remotes (the test-only
// local override) stay hidden, and any credential material is dropped even
// though ValidateGitRemote already rejects credential URLs.
func FullGitRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(remote), "ssh://") {
		parsed, err := url.Parse(remote)
		if err != nil || parsed.Host == "" || parsed.Path == "" {
			return ""
		}
		clean := &url.URL{Scheme: "ssh", Host: parsed.Host, Path: parsed.Path}
		if parsed.User != nil {
			if username := parsed.User.Username(); username != "" {
				clean.User = url.User(username)
			}
		}
		return clean.String()
	}
	// Only show scp-style remotes that pass the same validation used when the
	// owner saves one, so a manually configured path cannot smuggle query or
	// credential text into the settings page.
	if scpRemotePattern.MatchString(remote) && ValidateGitRemote(remote, false) == nil {
		return remote
	}
	return ""
}

var (
	scpRemotePattern  = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)@([A-Za-z0-9][A-Za-z0-9._-]*):(.+)$`)
	sshUserPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	remotePathPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+$`)
)

// ValidateGitRemote enforces the SSH-only contract. The path is also checked
// for shell metacharacters even though no shell is ever involved, so a hostile
// value can never become a git option or a surprise refspec.
func ValidateGitRemote(remote string, allowLocal bool) error {
	remote = strings.TrimSpace(remote)
	invalid := func() error {
		return &GitValidationError{Field: "remote", Message: "SSH remote 格式无效：请使用 git@host:path 或 ssh://user@host/path"}
	}
	if remote == "" || len(remote) > 512 {
		return invalid()
	}
	for _, char := range remote {
		if char <= 0x20 || char == 0x7f {
			return invalid()
		}
	}
	if allowLocal {
		localPath := remote
		if strings.HasPrefix(strings.ToLower(localPath), "file://") {
			parsed, err := url.Parse(localPath)
			if err != nil || parsed.Host != "" {
				return invalid()
			}
			localPath = parsed.Path
		}
		if filepath.IsAbs(localPath) {
			cleaned := filepath.Clean(localPath)
			if cleaned != localPath || strings.Contains(localPath, "..") {
				return invalid()
			}
			return nil
		}
	}
	if strings.HasPrefix(strings.ToLower(remote), "ssh://") {
		parsed, err := url.Parse(remote)
		if err != nil || !strings.EqualFold(parsed.Scheme, "ssh") || parsed.Host == "" ||
			parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
			return invalid()
		}
		if parsed.User != nil {
			if _, hasPassword := parsed.User.Password(); hasPassword || !sshUserPattern.MatchString(parsed.User.Username()) {
				return invalid()
			}
		}
		if parsed.Hostname() == "" || !remotePathPattern.MatchString(parsed.Path) ||
			!safeRemotePath(parsed.Path) || strings.Trim(parsed.Path, "/") == "" {
			return invalid()
		}
		return nil
	}
	matches := scpRemotePattern.FindStringSubmatch(remote)
	if matches == nil || !remotePathPattern.MatchString(matches[3]) || !safeRemotePath(matches[3]) || strings.Trim(matches[3], "/") == "" {
		return invalid()
	}
	return nil
}

func safeRemotePath(path string) bool {
	if strings.HasPrefix(path, "-") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == ".." {
			return false
		}
	}
	return true
}

// ValidateGitBranch mirrors git check-ref-format's important rules without
// starting a process. Branch names become refspecs, so a leading "-" (option
// injection) and ".." (path traversal) are both refused.
func ValidateGitBranch(branch string) error {
	branch = strings.TrimSpace(branch)
	invalid := func() error {
		return &GitValidationError{Field: "branch", Message: "Git 分支名无效"}
	}
	if branch == "" || len(branch) > 200 || branch == "@" || strings.HasPrefix(branch, "-") ||
		strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".") ||
		strings.Contains(branch, "..") || strings.Contains(branch, "//") || strings.Contains(branch, "\\") ||
		strings.Contains(branch, "@{") {
		return invalid()
	}
	for _, char := range branch {
		if char <= 0x20 || char == 0x7f || strings.ContainsRune("~^:?*[", char) {
			return invalid()
		}
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return invalid()
		}
	}
	return nil
}

func (g *Git) isRepository(ctx context.Context) bool {
	if _, err := os.Stat(filepath.Join(g.vaultPath, ".git")); err != nil {
		return false
	}
	output, err := g.command(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	top, topErr := filepath.Abs(strings.TrimSpace(output))
	vault, vaultErr := filepath.Abs(g.vaultPath)
	return topErr == nil && vaultErr == nil && filepath.Clean(top) == filepath.Clean(vault)
}

func (g *Git) hasCommit(ctx context.Context) bool {
	output, err := g.command(ctx, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil && strings.TrimSpace(output) != ""
}

func (g *Git) configuredBranch(ctx context.Context) string {
	if output, err := g.command(ctx, "config", "--get", "aha.branch"); err == nil {
		branch := strings.TrimSpace(output)
		if ValidateGitBranch(branch) == nil {
			return branch
		}
	}
	if output, err := g.command(ctx, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		branch := strings.TrimSpace(output)
		if ValidateGitBranch(branch) == nil {
			return branch
		}
	}
	return defaultGitBranch
}

func (g *Git) remoteURL(ctx context.Context) (string, error) {
	output, err := g.command(ctx, "remote", "get-url", "origin")
	if err != nil {
		var commandErr *GitCommandError
		if errors.As(err, &commandErr) {
			return "", ErrGitRemoteMissing
		}
		return "", err
	}
	remote := strings.TrimSpace(strings.Split(output, "\n")[0])
	if remote == "" {
		return "", ErrGitRemoteMissing
	}
	return remote, nil
}

// fetchOrigin refreshes origin/<branch> without touching the local branch or
// the worktree. The explicit refspec makes the update independent of the
// remote's configured fetch rules; the '+' only rewinds the remote-tracking
// ref, never local history.
func (g *Git) fetchOrigin(ctx context.Context, branch string) error {
	remote, err := g.remoteURL(ctx)
	if err != nil {
		return err
	}
	_, err = g.command(ctx, "fetch", "--no-tags", "origin",
		"+refs/heads/"+branch+":refs/remotes/origin/"+branch)
	return redactGitError(err, remote)
}

// isClean reports content changes, not git's stat cache: a CRLF rewrite that
// the eol=lf clean filter normalizes back to the same blob is clean even
// though `git status` may mention the path.
func (g *Git) isClean(ctx context.Context) (bool, int, error) {
	changes, err := g.worktreeChanges(ctx)
	if err != nil {
		return false, 0, err
	}
	count := 0
	for _, path := range changes.paths {
		if autoCommitPathExclusion(path) == "internal" {
			continue
		}
		count++
	}
	return count == 0, count, nil
}

func (g *Git) requireClean(ctx context.Context) error {
	clean, changed, err := g.isClean(ctx)
	if err != nil {
		return err
	}
	if !clean {
		return fmt.Errorf("%w：有 %d 个未提交修改，请先提交", ErrGitDirty, changed)
	}
	return nil
}

func (g *Git) requireRepository(ctx context.Context) error {
	if _, err := g.gitBinary(); err != nil {
		return err
	}
	if !g.isRepository(ctx) {
		return ErrNotGitRepository
	}
	return nil
}

func (g *Git) requireVault(ctx context.Context) error {
	if !VaultInitialized(g.vaultPath) {
		return ErrVaultNotInitialized
	}
	return g.requireRepository(ctx)
}

// DocumentDiff bounds the preview payload. A knowledge document is text, but a
// generated or hand-edited file can still produce a huge unified diff, so the
// read API stops at whichever limit is reached first.
const (
	diffMaxLines = 400
	diffMaxBytes = 64 << 10
)

// DiffLine is one rendered row of a unified diff. "hunk" rows carry the
// @@ header plus its old/new ranges; context/add/remove rows carry their
// one-based line numbers on each side (zero when that side has no line).
type DiffLine struct {
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	OldNo    int    `json:"old_no"`
	NewNo    int    `json:"new_no"`
	OldStart int    `json:"old_start,omitempty"`
	OldLines int    `json:"old_lines,omitempty"`
	NewStart int    `json:"new_start,omitempty"`
	NewLines int    `json:"new_lines,omitempty"`
}

// DocDiff is the read-only "latest change" view for one document. An
// unavailable diff is a normal 200 response: the Web preview falls back to the
// full document body and shows Reason instead of treating the reader as broken.
type DocDiff struct {
	Available   bool       `json:"available"`
	Reason      string     `json:"reason,omitempty"`
	Baseline    string     `json:"baseline,omitempty"`
	Commit      string     `json:"commit,omitempty"`
	CommittedAt string     `json:"committed_at,omitempty"`
	Truncated   bool       `json:"truncated"`
	Added       int        `json:"added"`
	Removed     int        `json:"removed"`
	Lines       []DiffLine `json:"lines"`
}

var (
	diffHunkPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
	diffHashPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
)

func unavailableDiff(reason string) DocDiff {
	return DocDiff{Lines: []DiffLine{}, Reason: reason}
}

// literalPathspec stops git from expanding bracket characters in a vault file
// name as a glob. Without it "note[1].md" also matches "note1.md", so a preview
// could silently show a sibling file's diff and commit baseline.
func literalPathspec(relativePath string) string {
	return ":(literal)" + relativePath
}

// DocumentDiff compares a vault-relative document path with its latest
// baseline. Uncommitted changes (staged or not) win; otherwise the last commit
// that touched the file is compared with its parent. The caller must pass a
// path that came from the read-only index, never a raw client parameter.
func (g *Git) DocumentDiff(ctx context.Context, relativePath string) (DocDiff, error) {
	relativePath = filepath.ToSlash(strings.TrimSpace(relativePath))
	if err := ValidateRelativePath(relativePath); err != nil {
		return DocDiff{}, ErrDocumentNotFound
	}
	absolute := filepath.Join(g.vaultPath, filepath.FromSlash(relativePath))
	if !IsInsideRoot(g.vaultPath, absolute) {
		return DocDiff{}, ErrDocumentNotFound
	}
	if _, err := g.gitBinary(); err != nil {
		return unavailableDiff("当前环境没有可用的 git 命令"), nil
	}
	if !g.isRepository(ctx) {
		return unavailableDiff("知识库不是 git 仓库"), nil
	}
	if !g.hasCommit(ctx) {
		return unavailableDiff("知识库仓库还没有任何提交"), nil
	}
	pathspec := literalPathspec(relativePath)
	// A file that was never added to git has no baseline at all. Checking
	// ls-files first also keeps the untracked file's content out of the diff.
	if _, err := g.command(ctx, "ls-files", "--error-unmatch", "--", pathspec); err != nil {
		return unavailableDiff("文件尚未纳入 git 版本管理"), nil
	}

	// HEAD covers both staged and unstaged edits. The older "git diff -- path"
	// form would silently miss a change the owner already staged.
	output, err := g.commandRaw(ctx, "diff", "--no-color", "--no-ext-diff", "HEAD", "--", pathspec)
	if err != nil {
		return unavailableDiff("读取 git 变更失败"), nil
	}
	if strings.TrimSpace(output) != "" {
		result := DocDiff{Available: true, Baseline: "uncommitted", Lines: []DiffLine{}}
		applyUnifiedDiff(&result, output)
		return result, nil
	}

	commit, err := g.command(ctx, "log", "-1", "--format=%H", "--", pathspec)
	if err != nil || strings.TrimSpace(commit) == "" {
		return unavailableDiff("文件没有提交历史"), nil
	}
	commit = strings.TrimSpace(strings.Split(commit, "\n")[0])
	if !diffHashPattern.MatchString(commit) {
		return unavailableDiff("无法确定该文件的基线提交"), nil
	}
	if _, err := g.command(ctx, "rev-parse", "--verify", "--quiet", commit+"^"); err != nil {
		return unavailableDiff("文件只有一次提交，没有可比较的历史版本"), nil
	}
	committedAt := ""
	if value, err := g.command(ctx, "show", "-s", "--format=%cI", commit); err == nil {
		committedAt = strings.TrimSpace(value)
	}
	output, err = g.commandRaw(ctx, "diff", "--no-color", "--no-ext-diff", commit+"^", commit, "--", pathspec)
	if err != nil {
		return unavailableDiff("读取 git 变更失败"), nil
	}
	result := DocDiff{
		Available: true, Baseline: commit, Commit: commit,
		CommittedAt: committedAt, Lines: []DiffLine{},
	}
	applyUnifiedDiff(&result, output)
	return result, nil
}

func applyUnifiedDiff(result *DocDiff, output string) {
	if strings.Contains(output, "Binary files ") && strings.Contains(output, " differ") {
		result.Available = false
		result.Reason = "二进制文件无法显示文本差异"
		return
	}
	result.Lines, result.Truncated, result.Added, result.Removed = parseUnifiedDiff(output)
}

// parseUnifiedDiff turns git's unified diff into rows the Web preview can
// render without reimplementing diff parsing in TypeScript. File headers and
// the "no newline" marker are dropped; the first non-hunk rows appear before
// any @@ header and are ignored by the inHunk guard.
func parseUnifiedDiff(output string) (lines []DiffLine, truncated bool, added, removed int) {
	lines = []DiffLine{}
	bytes := 0
	oldLine, newLine := 0, 0
	inHunk := false
	appendLine := func(line DiffLine) bool {
		size := len(line.Text) + 32
		if len(lines) >= diffMaxLines || bytes+size > diffMaxBytes {
			truncated = true
			return false
		}
		bytes += size
		lines = append(lines, line)
		return true
	}
	for _, raw := range strings.Split(output, "\n") {
		raw = strings.TrimSuffix(raw, "\r")
		if strings.HasPrefix(raw, "@@ -") {
			match := diffHunkPattern.FindStringSubmatch(raw)
			if match == nil {
				inHunk = false
				continue
			}
			oldStart := atoiOrZero(match[1])
			newStart := atoiOrZero(match[3])
			oldLines, newLines := 1, 1
			if match[2] != "" {
				oldLines = atoiOrZero(match[2])
			}
			if match[4] != "" {
				newLines = atoiOrZero(match[4])
			}
			oldLine, newLine = oldStart, newStart
			inHunk = true
			if !appendLine(DiffLine{
				Kind: "hunk", Text: raw,
				OldStart: oldStart, OldLines: oldLines, NewStart: newStart, NewLines: newLines,
			}) {
				break
			}
			continue
		}
		if !inHunk {
			continue
		}
		if strings.HasPrefix(raw, `\`) {
			continue
		}
		var line DiffLine
		switch {
		case strings.HasPrefix(raw, "+"):
			line = DiffLine{Kind: "add", Text: raw[1:], NewNo: newLine}
			newLine++
			added++
		case strings.HasPrefix(raw, "-"):
			line = DiffLine{Kind: "remove", Text: raw[1:], OldNo: oldLine}
			oldLine++
			removed++
		case strings.HasPrefix(raw, " "):
			// A blank file line arrives as a single space. A truly empty
			// string is only the trailing newline and must not become a
			// phantom context row past the end of the hunk.
			line = DiffLine{Kind: "context", Text: raw[1:], OldNo: oldLine, NewNo: newLine}
			oldLine++
			newLine++
		default:
			continue
		}
		if !appendLine(line) {
			break
		}
	}
	return lines, truncated, added, removed
}

func atoiOrZero(value string) int {
	number, err := strconv.Atoi(value)
	if err != nil || number < 0 {
		return 0
	}
	return number
}

// Status never fails just because git is missing or the vault is not a repo;
// it reports that state so the panel can offer "初始化".
func (g *Git) Status(ctx context.Context) (GitStatus, error) {
	status := GitStatus{Branch: defaultGitBranch, Clean: true}
	if _, err := g.gitBinary(); err != nil {
		return status, nil
	}
	status.Available = true
	if !g.isRepository(ctx) {
		return status, nil
	}
	status.Initialized = true
	status.Branch = g.configuredBranch(ctx)
	if current, err := g.command(ctx, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		status.CurrentBranch = strings.TrimSpace(current)
	}
	status.HasCommits = g.hasCommit(ctx)

	if remote, err := g.remoteURL(ctx); err == nil {
		status.Remote = MaskGitRemote(remote)
		status.RemoteFull = FullGitRemote(remote)
		status.RemoteSet = true
	}

	clean, changed, err := g.isClean(ctx)
	if err != nil {
		return status, err
	}
	status.Clean, status.Changed = clean, changed

	if status.HasCommits {
		if output, err := g.command(ctx, "log", "-1", "--format=%h%x1f%s%x1f%cI"); err == nil {
			parts := strings.SplitN(output, "\x1f", 3)
			if len(parts) == 3 {
				status.LastCommit = sanitizeGitOutput(parts[0])
				status.LastCommitMsg = sanitizeGitOutput(parts[1])
				status.LastCommitAt = sanitizeGitOutput(parts[2])
			}
		}
	}

	// Ahead/behind only make sense against the remote-tracking ref, and a first
	// push has no such ref yet. Tracking is reported separately so the UI can
	// still enable that first push.
	if status.RemoteSet && status.HasCommits {
		remoteRef := "refs/remotes/origin/" + status.Branch
		if _, err := g.command(ctx, "rev-parse", "--verify", "--quiet", remoteRef); err == nil {
			status.Tracking = true
			if output, err := g.command(ctx, "rev-list", "--left-right", "--count", remoteRef+"...HEAD"); err == nil {
				var behind, ahead int
				if _, scanErr := fmt.Sscan(output, &behind, &ahead); scanErr == nil {
					status.Behind, status.Ahead = behind, ahead
				}
			}
		}
	}

	status.LastSyncAt = g.readLastSync()
	return status, nil
}

func (g *Git) readLastSync() string {
	data, err := os.ReadFile(filepath.Join(g.vaultPath, stateDirectory, gitSyncStateFile))
	if err != nil {
		return ""
	}
	var state struct {
		LastSyncAt string `json:"last_sync_at"`
	}
	if json.Unmarshal(data, &state) != nil {
		return ""
	}
	if _, err := time.Parse(time.RFC3339, state.LastSyncAt); err != nil {
		return ""
	}
	return state.LastSyncAt
}

func (g *Git) recordSync() error {
	payload, err := json.MarshalIndent(map[string]string{
		"last_sync_at": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return writeVaultFileIfChanged(g.vaultPath, filepath.Join(g.vaultPath, stateDirectory, gitSyncStateFile), payload, 0o644)
}

func ensureGitignore(vaultPath string) error {
	path := filepath.Join(vaultPath, ".gitignore")
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeVaultFileIfChanged(vaultPath, path, []byte(gitIgnoreBody), 0o644)
}

// Init is idempotent: an existing repository is left alone, except that a
// missing .gitignore is restored and a repository stuck without a first commit
// is completed.
func (g *Git) Init(ctx context.Context) (GitOpResult, error) {
	unlock := g.lockRepo()
	defer unlock()
	if err := g.requireVault(ctx); err == nil {
		// Already a repository.
	} else if errors.Is(err, ErrNotGitRepository) {
		if _, initErr := g.command(ctx, "init", "-b", defaultGitBranch); initErr != nil {
			// Older git may not know -b. A plain init plus symbolic-ref keeps
			// the same observable result without forcing a version check.
			if _, fallbackErr := g.command(ctx, "init"); fallbackErr != nil {
				return GitOpResult{}, fallbackErr
			}
		}
		if !g.isRepository(ctx) {
			return GitOpResult{}, ErrNotGitRepository
		}
	} else {
		return GitOpResult{}, err
	}

	if branch := g.configuredBranch(ctx); branch != "" {
		if _, err := g.command(ctx, "config", "aha.branch", branch); err != nil {
			return GitOpResult{}, err
		}
	}
	if !g.hasCommit(ctx) {
		branch := g.configuredBranch(ctx)
		if _, err := g.command(ctx, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
			return GitOpResult{}, err
		}
	}
	if err := ensureGitignore(g.vaultPath); err != nil {
		return GitOpResult{}, err
	}
	// Pin the text policy before the first commit so a fresh vault never
	// records platform-dependent line endings. The upgrade path applies the
	// same policy through the auto-sync preflight.
	if _, err := g.ensureTextPolicy(ctx); err != nil {
		return GitOpResult{}, err
	}
	created := false
	if !g.hasCommit(ctx) {
		if _, err := g.command(ctx, "add", "-A"); err != nil {
			return GitOpResult{}, err
		}
		if _, err := g.command(ctx, "commit", "-q", "-m", "Initialize AHA2 knowledge vault"); err != nil {
			return GitOpResult{}, err
		}
		created = true
	}
	status, err := g.Status(ctx)
	if err != nil {
		return GitOpResult{}, err
	}
	message := "Git 仓库已就绪"
	if created {
		message = "Git 仓库已初始化并完成首次提交"
	}
	return GitOpResult{Status: status, Message: message, Created: created}, nil
}

// SetRemote validates and stores the SSH remote and branch target. Passing an
// empty remote keeps the current remote so the owner can change only branch.
func (g *Git) SetRemote(ctx context.Context, remote, branch string) (GitOpResult, error) {
	unlock := g.lockRepo()
	defer unlock()
	if err := g.requireVault(ctx); err != nil {
		return GitOpResult{}, err
	}
	remote = strings.TrimSpace(remote)
	branch = strings.TrimSpace(branch)
	if remote == "" && branch == "" {
		return GitOpResult{}, &GitValidationError{Field: "remote", Message: "remote 或 branch 至少填写一项"}
	}
	if remote != "" {
		if err := ValidateGitRemote(remote, g.allowLocalRemote); err != nil {
			return GitOpResult{}, err
		}
	}
	if branch != "" {
		if err := ValidateGitBranch(branch); err != nil {
			return GitOpResult{}, err
		}
	} else {
		branch = g.configuredBranch(ctx)
	}
	if remote == "" {
		current, err := g.remoteURL(ctx)
		if err != nil {
			return GitOpResult{}, err
		}
		remote = current
	}
	if _, err := g.remoteURL(ctx); err == nil {
		if _, err := g.command(ctx, "remote", "set-url", "origin", remote); err != nil {
			return GitOpResult{}, redactGitError(err, remote)
		}
	} else {
		if _, err := g.command(ctx, "remote", "add", "origin", remote); err != nil {
			return GitOpResult{}, redactGitError(err, remote)
		}
	}
	if _, err := g.command(ctx, "config", "aha.branch", branch); err != nil {
		return GitOpResult{}, err
	}
	if !g.hasCommit(ctx) {
		if _, err := g.command(ctx, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
			return GitOpResult{}, err
		}
	}
	status, err := g.Status(ctx)
	if err != nil {
		return GitOpResult{}, err
	}
	return GitOpResult{Status: status, Message: "SSH remote 与分支已保存"}, nil
}

func normalizeCommitMessage(message string) (string, error) {
	message = strings.ReplaceAll(message, "\r\n", "\n")
	message = strings.ReplaceAll(message, "\r", "\n")
	message = strings.TrimSpace(message)
	if message == "" {
		return gitCommitMessage, nil
	}
	if utf8.RuneCountInString(message) > maxGitMessageRunes {
		return "", &GitValidationError{Field: "message", Message: "提交说明过长（最多 200 字）"}
	}
	for _, char := range message {
		if char == '\n' || char == '\t' {
			continue
		}
		if char < 0x20 || char == 0x7f {
			return "", &GitValidationError{Field: "message", Message: "提交说明包含无效字符"}
		}
	}
	return message, nil
}

func (g *Git) Commit(ctx context.Context, message string) (GitOpResult, error) {
	unlock := g.lockRepo()
	defer unlock()
	if err := g.requireVault(ctx); err != nil {
		return GitOpResult{}, err
	}
	message, err := normalizeCommitMessage(message)
	if err != nil {
		return GitOpResult{}, err
	}
	if _, err := g.command(ctx, "add", "-A"); err != nil {
		return GitOpResult{}, err
	}
	if _, err := g.command(ctx, "commit", "-q", "-m", message); err != nil {
		var commandErr *GitCommandError
		if errors.As(err, &commandErr) {
			detail := strings.ToLower(commandErr.Detail)
			if strings.Contains(detail, "nothing to commit") || strings.Contains(detail, "no changes added") {
				status, statusErr := g.Status(ctx)
				if statusErr != nil {
					return GitOpResult{}, statusErr
				}
				return GitOpResult{Status: status, Message: "没有需要提交的修改"}, nil
			}
		}
		return GitOpResult{}, err
	}
	status, err := g.Status(ctx)
	if err != nil {
		return GitOpResult{}, err
	}
	messageText := "已提交"
	if status.LastCommit != "" {
		messageText += " " + status.LastCommit
	}
	return GitOpResult{Status: status, Message: messageText}, nil
}

func (g *Git) Pull(ctx context.Context) (GitOpResult, error) {
	unlock := g.lockRepo()
	defer unlock()
	return g.pull(ctx, true)
}

func (g *Git) pull(ctx context.Context, record bool) (GitOpResult, error) {
	return g.pullWithPolicy(ctx, record, true)
}

// pullAllowDirty is the auto-sync variant. Blocked untracked files must not
// prevent pulling remote commits, so it skips the clean-worktree gate; git
// itself still refuses a pull that would overwrite a local file.
func (g *Git) pullAllowDirty(ctx context.Context, record bool) (GitOpResult, error) {
	return g.pullWithPolicy(ctx, record, false)
}

func (g *Git) pullWithPolicy(ctx context.Context, record, requireClean bool) (GitOpResult, error) {
	if err := g.requireVault(ctx); err != nil {
		return GitOpResult{}, err
	}
	remote, err := g.remoteURL(ctx)
	if err != nil {
		return GitOpResult{}, err
	}
	if requireClean {
		if err := g.requireClean(ctx); err != nil {
			return GitOpResult{}, err
		}
	}
	branch := g.configuredBranch(ctx)
	if _, err := g.command(ctx, "pull", "--ff-only", "origin", branch); err != nil {
		err = redactGitError(err, remote)
		var commandErr *GitCommandError
		if errors.As(err, &commandErr) {
			detail := strings.ToLower(commandErr.Detail)
			if strings.Contains(detail, "not possible to fast-forward") ||
				strings.Contains(detail, "not possible to fast forward") ||
				strings.Contains(detail, "diverging branches") {
				return GitOpResult{}, fmt.Errorf("%w：远程与本地已分叉，未执行合并；请手动处理", ErrGitDiverged)
			}
			if strings.Contains(detail, "couldn't find remote ref") ||
				strings.Contains(detail, "could not find remote ref") ||
				(strings.Contains(detail, "remote branch") && strings.Contains(detail, "not found")) {
				return GitOpResult{}, ErrGitRemoteBranchMissing
			}
		}
		return GitOpResult{}, err
	}
	if record {
		if err := g.recordSync(); err != nil {
			return GitOpResult{}, err
		}
	}
	status, err := g.Status(ctx)
	if err != nil {
		return GitOpResult{}, err
	}
	return GitOpResult{Status: status, Message: "已拉取远程更新"}, nil
}

func (g *Git) Push(ctx context.Context) (GitOpResult, error) {
	unlock := g.lockRepo()
	defer unlock()
	return g.push(ctx, true)
}

func (g *Git) push(ctx context.Context, record bool) (GitOpResult, error) {
	if err := g.requireVault(ctx); err != nil {
		return GitOpResult{}, err
	}
	remote, err := g.remoteURL(ctx)
	if err != nil {
		return GitOpResult{}, err
	}
	if !g.hasCommit(ctx) {
		return GitOpResult{}, ErrGitNoCommit
	}
	branch := g.configuredBranch(ctx)
	if _, err := g.command(ctx, "push", "--set-upstream", "origin", "HEAD:refs/heads/"+branch); err != nil {
		err = redactGitError(err, remote)
		var commandErr *GitCommandError
		if errors.As(err, &commandErr) {
			detail := strings.ToLower(commandErr.Detail)
			if strings.Contains(detail, "non-fast-forward") || strings.Contains(detail, "fetch first") ||
				strings.Contains(detail, "rejected") {
				return GitOpResult{}, fmt.Errorf("%w：远程有新的提交，请先拉取；未执行强推", ErrGitPushRejected)
			}
		}
		return GitOpResult{}, err
	}
	if record {
		if err := g.recordSync(); err != nil {
			return GitOpResult{}, err
		}
	}
	status, err := g.Status(ctx)
	if err != nil {
		return GitOpResult{}, err
	}
	return GitOpResult{Status: status, Message: "已推送到远程"}, nil
}

// Sync is pull --ff-only followed by push. It refuses a dirty worktree instead
// of auto-committing the owner's edits, and it never forces or merges.
func (g *Git) Sync(ctx context.Context) (GitOpResult, error) {
	unlock := g.lockRepo()
	defer unlock()
	if err := g.requireVault(ctx); err != nil {
		return GitOpResult{}, err
	}
	if _, err := g.remoteURL(ctx); err != nil {
		return GitOpResult{}, err
	}
	if err := g.requireClean(ctx); err != nil {
		return GitOpResult{}, err
	}
	if _, err := g.pull(ctx, false); err != nil {
		return GitOpResult{}, err
	}
	if _, err := g.push(ctx, false); err != nil {
		return GitOpResult{}, err
	}
	if err := g.recordSync(); err != nil {
		return GitOpResult{}, err
	}
	status, err := g.Status(ctx)
	if err != nil {
		return GitOpResult{}, err
	}
	return GitOpResult{Status: status, Message: "已同步"}, nil
}
