package knowledgevault

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Automatic commits are deliberately conservative. They only ever stage
// explicit paths that passed every gate below, and they never amend, force or
// touch anything outside the vault root.

const (
	// maxAutoCommitFileBytes bounds one text file. Larger files are usually
	// exports, databases or media and must be committed by the owner on purpose.
	maxAutoCommitFileBytes = 1 << 20
	// maxAutoCommitFiles bounds one auto-commit batch so a large external copy
	// cannot turn into an unbounded git process.
	maxAutoCommitFiles = 20
	// maxAutoCommitBlockedReport keeps the status payload small while still
	// naming enough files for the owner to act on.
	maxAutoCommitBlockedReport = 50
	maxAutoCommitMessageFiles  = 20
)

var (
	// ErrAutoCommitStale means the worktree changed between the quiet-period
	// check and the commit. Nothing was staged; the next cycle re-reads it.
	ErrAutoCommitStale = errors.New("知识库文件在提交前又发生了变化")
)

// BlockedChange is one path the auto-commit gate refused. Reason is a stable
// machine key (binary, secret, too_large, symlink, invalid_path, unmerged); the
// Web UI translates it. File contents are never included.
type BlockedChange struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// AutoCommitScan is the read-only view used both by the syncer's quiet-period
// detection and by the settings status. Eligible is already capped to one
// batch; Pending counts every changed path git reported, blocked ones included.
type AutoCommitScan struct {
	Clean        bool            `json:"clean"`
	Unmerged     bool            `json:"unmerged"`
	Pending      int             `json:"pending"`
	Eligible     []string        `json:"eligible,omitempty"`
	Blocked      []BlockedChange `json:"blocked,omitempty"`
	BlockedTotal int             `json:"blocked_total"`
	Fingerprint  string          `json:"-"`
}

// AutoCommitResult is the outcome of one gated batch.
type AutoCommitResult struct {
	Committed    bool            `json:"committed"`
	Commit       string          `json:"commit,omitempty"`
	Files        []string        `json:"files,omitempty"`
	Blocked      []BlockedChange `json:"blocked,omitempty"`
	BlockedTotal int             `json:"blocked_total"`
	Pending      int             `json:"pending"`
	Message      string          `json:"message,omitempty"`
}

// worktreeChangeSet is the content-accurate change view. `git status` alone
// reports a file as modified when only its stat cache is stale (for example a
// CRLF rewrite that the eol=lf filter would normalize back to the same blob),
// so the fingerprint is built from `git diff --raw` blob ids plus untracked
// content hashes instead.
type worktreeChangeSet struct {
	paths       []string
	unmerged    []string
	fingerprint string
}

func (g *Git) worktreeChanges(ctx context.Context) (worktreeChangeSet, error) {
	set := worktreeChangeSet{}
	if !g.hasCommit(ctx) {
		output, err := g.commandRaw(ctx, "status", "--porcelain", "-z", "--untracked-files=all")
		if err != nil {
			return set, err
		}
		for _, record := range splitStatusRecords(output) {
			if isUnmergedCode(record.code) {
				set.unmerged = append(set.unmerged, record.paths[0])
			}
			set.paths = append(set.paths, record.paths...)
		}
		set.paths = uniqueSortedPaths(set.paths)
		set.fingerprint = hashBytes([]byte(output))
		return set, nil
	}

	raw, err := g.commandRaw(ctx, "diff", "--raw", "--no-abbrev", "--no-renames", "-z", "HEAD")
	if err != nil {
		return set, err
	}
	diffNames, err := g.commandRaw(ctx, "diff", "--name-only", "--no-renames", "-z", "HEAD")
	if err != nil {
		return set, err
	}
	untrackedRaw, err := g.commandRaw(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return set, err
	}
	unmergedRaw, err := g.commandRaw(ctx, "ls-files", "-u", "-z")
	if err != nil {
		return set, err
	}
	untracked := splitNullList(untrackedRaw)
	set.paths = uniqueSortedPaths(append(splitNullList(diffNames), untracked...))
	set.unmerged = uniqueSortedPaths(unmergedPaths(unmergedRaw))

	hashInput := make([]string, 0, len(untracked))
	signatures := make(map[string]string, len(untracked))
	for _, path := range untracked {
		absolute := filepath.Join(g.vaultPath, filepath.FromSlash(path))
		info, statErr := os.Lstat(absolute)
		switch {
		case statErr != nil:
			signatures[path] = "missing"
		case !info.Mode().IsRegular():
			// Never hand a FIFO/device/socket to hash-object: opening it can
			// block forever. Size and mode still participate in the fingerprint.
			signatures[path] = fmt.Sprintf("mode:%d:size:%d:mtime:%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		case info.Size() > maxAutoCommitFileBytes:
			// Large files are blocked by the commit gate; hashing them every
			// poll would read gigabytes and starve knowledge writes.
			signatures[path] = fmt.Sprintf("large:size:%d:mtime:%d", info.Size(), info.ModTime().UnixNano())
		default:
			if !strings.Contains(path, "\n") {
				hashInput = append(hashInput, path)
			}
		}
	}
	hashes := g.hashWorktreePaths(ctx, hashInput)
	var fingerprint strings.Builder
	fingerprint.WriteString(raw)
	fingerprint.WriteByte(0)
	for _, path := range untracked {
		fingerprint.WriteString(path)
		fingerprint.WriteByte('=')
		if signature, ok := signatures[path]; ok {
			fingerprint.WriteString(signature)
		} else {
			fingerprint.WriteString(hashes[path])
		}
		fingerprint.WriteByte(0)
	}
	set.fingerprint = hashBytes([]byte(fingerprint.String()))
	return set, nil
}

func uniqueSortedPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func unmergedPaths(output string) []string {
	result := []string{}
	for _, record := range splitNullList(output) {
		if index := strings.IndexByte(record, '\t'); index >= 0 {
			result = append(result, record[index+1:])
		}
	}
	return result
}

func (g *Git) hashWorktreePaths(ctx context.Context, paths []string) map[string]string {
	result := map[string]string{}
	if len(paths) == 0 {
		return result
	}
	output, err := g.commandInputRaw(ctx, "hash-object", strings.Join(paths, "\n")+"\n", "--stdin-paths")
	if err != nil {
		return result
	}
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) != len(paths) {
		return result
	}
	for index, path := range paths {
		result[path] = strings.TrimSpace(lines[index])
	}
	return result
}

// ScanAutoCommit reads the content-accurate change set once and classifies
// every change. It takes no manager lock: git only observes atomically renamed
// files, and a status refresh must not block a knowledge write while it hashes
// untracked content.
func (g *Git) ScanAutoCommit(ctx context.Context) (AutoCommitScan, error) {
	return g.scanAutoCommit(ctx)
}

func (g *Git) scanAutoCommit(ctx context.Context) (AutoCommitScan, error) {
	changes, err := g.worktreeChanges(ctx)
	if err != nil {
		return AutoCommitScan{}, err
	}
	scan := AutoCommitScan{Fingerprint: changes.fingerprint, Unmerged: len(changes.unmerged) > 0}
	seen := map[string]bool{}
	addBlocked := func(path, reason string) {
		path = filepath.ToSlash(path)
		if seen[path] {
			return
		}
		seen[path] = true
		scan.BlockedTotal++
		if len(scan.Blocked) >= maxAutoCommitBlockedReport {
			return
		}
		scan.Blocked = append(scan.Blocked, BlockedChange{Path: path, Reason: reason})
	}
	for _, path := range changes.unmerged {
		addBlocked(path, "unmerged")
	}
	pending := 0
	eligibleRecords := 0
	for _, path := range changes.paths {
		exclusion := autoCommitPathExclusion(path)
		if exclusion == "internal" {
			continue
		}
		pending++
		if exclusion != "" {
			addBlocked(path, exclusion)
			continue
		}
		absolute := filepath.Join(g.vaultPath, filepath.FromSlash(path))
		// A deleted path cannot be resolved by EvalSymlinks; ValidateRelativePath
		// above already proved it is a lexical vault path, and git only reports
		// deletions of tracked entries.
		if _, statErr := os.Lstat(absolute); statErr == nil && !IsInsideRoot(g.vaultPath, absolute) {
			addBlocked(path, "invalid_path")
			continue
		}
		if reason := g.classifyAutoCommitFile(absolute); reason != "" {
			addBlocked(path, reason)
			continue
		}
		if eligibleRecords >= maxAutoCommitFiles {
			// Over the batch size: it stays pending and is picked up by the
			// next cycle instead of growing one unbounded commit.
			continue
		}
		eligibleRecords++
		scan.Eligible = append(scan.Eligible, path)
	}
	scan.Pending = pending
	scan.Clean = pending == 0 && !scan.Unmerged
	return scan, nil
}

// classifyAutoCommitFile returns a stable block reason for one existing path.
// Deleted paths are always eligible: removing a document is a normal vault
// operation and carries no content to leak.
func (g *Git) classifyAutoCommitFile(absolute string) string {
	info, err := os.Lstat(absolute)
	switch {
	case os.IsNotExist(err):
		return ""
	case err != nil:
		return "unreadable"
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink"
	case info.IsDir():
		return "invalid_path"
	case !info.Mode().IsRegular():
		// FIFOs, sockets and device nodes can block a read forever; never open
		// them during a background poll.
		return "not_regular"
	}
	if info.Size() > maxAutoCommitFileBytes {
		return "too_large"
	}
	if isAutoCommitBinaryPath(absolute) {
		return "binary"
	}
	if reason := autoCommitBlockedPath(absolute); reason != "" {
		return reason
	}
	content, tooLarge, err := readBoundedRegularFile(absolute, maxAutoCommitFileBytes)
	if err != nil {
		return "unreadable"
	}
	if tooLarge {
		return "too_large"
	}
	if looksBinary(content) {
		return "binary"
	}
	if containsSecretMaterial(content) {
		return "secret"
	}
	return ""
}

// AutoCommit stages and commits one gated batch. expectedFingerprint guards
// against a file changing after the quiet period; a mismatch returns
// ErrAutoCommitStale without staging anything.
func (g *Git) AutoCommit(ctx context.Context, reason, expectedFingerprint string) (AutoCommitResult, error) {
	unlockRepo := g.lockRepo()
	defer unlockRepo()
	unlockWrites := g.lockWriterBlocks()
	defer unlockWrites()
	return g.autoCommitLocked(ctx, reason, expectedFingerprint)
}

func (g *Git) autoCommitLocked(ctx context.Context, reason, expectedFingerprint string) (AutoCommitResult, error) {
	result := AutoCommitResult{}
	if err := g.requireRepository(ctx); err != nil {
		return result, err
	}
	if !g.hasCommit(ctx) {
		return result, ErrGitNoCommit
	}
	scan, err := g.scanAutoCommit(ctx)
	if err != nil {
		return result, err
	}
	result.Blocked, result.Pending, result.BlockedTotal = scan.Blocked, scan.Pending, scan.BlockedTotal
	if scan.Unmerged {
		return result, ErrGitUnmerged
	}
	if expectedFingerprint != "" && scan.Fingerprint != expectedFingerprint {
		return result, ErrAutoCommitStale
	}
	if scan.Clean || len(scan.Eligible) == 0 {
		result.Message = "没有可自动提交的文本改动"
		return result, nil
	}
	// Stage only paths git can still resolve. A deletion that the owner already
	// staged is absent from both the index and the worktree; `git add` would
	// reject its pathspec, yet it is already exactly what the commit needs.
	indexOutput, err := g.commandRaw(ctx, "ls-files", "-z")
	if err != nil {
		return result, err
	}
	indexed := map[string]bool{}
	for _, path := range splitNullList(indexOutput) {
		indexed[path] = true
	}
	stagedPaths := make([]string, 0, len(scan.Eligible))
	for _, path := range scan.Eligible {
		if _, statErr := os.Lstat(filepath.Join(g.vaultPath, filepath.FromSlash(path))); statErr == nil || indexed[path] {
			stagedPaths = append(stagedPaths, path)
		}
	}
	if len(stagedPaths) > 0 {
		stageArgs := append([]string{"add", "-A", "--"}, autoCommitPathspecs(stagedPaths)...)
		if _, err := g.command(ctx, stageArgs[0], stageArgs[1:]...); err != nil {
			return result, err
		}
	}
	staged, err := g.commandRaw(ctx, "diff", "--cached", "--name-only", "-z", "HEAD")
	if err != nil {
		return result, err
	}
	eligible := make(map[string]bool, len(scan.Eligible))
	for _, path := range scan.Eligible {
		eligible[path] = true
	}
	files := []string{}
	for _, path := range splitNullList(staged) {
		if eligible[path] {
			files = append(files, path)
		}
	}
	if len(files) == 0 {
		result.Message = "没有可自动提交的文本改动"
		return result, nil
	}
	message := autoCommitMessage(reason, files)
	commitArgs := append([]string{"commit", "-q", "-m", message, "--"}, autoCommitPathspecs(scan.Eligible)...)
	if _, err := g.command(ctx, commitArgs[0], commitArgs[1:]...); err != nil {
		return result, err
	}
	head, err := g.command(ctx, "rev-parse", "--short", "HEAD")
	if err != nil {
		return result, err
	}
	result.Committed = true
	result.Commit = strings.TrimSpace(head)
	result.Files = files
	result.Message = fmt.Sprintf("已自动提交 %d 个文件", len(files))
	after, scanErr := g.scanAutoCommit(ctx)
	if scanErr == nil {
		result.Pending = after.Pending
		result.Blocked = after.Blocked
		result.BlockedTotal = after.BlockedTotal
	}
	return result, nil
}

func autoCommitPathspecs(paths []string) []string {
	unique := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		unique = append(unique, literalPathspec(path))
	}
	return unique
}

// autoCommitMessage is stable and auditable: one subject plus the reason and
// the committed paths. It never includes file content or remote credentials.
func autoCommitMessage(reason string, files []string) string {
	label := "external change"
	switch reason {
	case "aha":
		label = "AHA2 write"
	case "preflight":
		label = "preflight"
	}
	sorted := append([]string{}, files...)
	sort.Strings(sorted)
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("knowledge: auto-commit %d file(s) (%s)\n\n", len(sorted), label))
	builder.WriteString("Auto-sync: " + label + "\n")
	builder.WriteString("Files:\n")
	for index, path := range sorted {
		if index >= maxAutoCommitMessageFiles {
			builder.WriteString(fmt.Sprintf("  ... %d more\n", len(sorted)-index))
			break
		}
		builder.WriteString("  " + sanitizeCommitPath(path) + "\n")
	}
	return builder.String()
}

func sanitizeCommitPath(path string) string {
	cleaned := strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f || (char >= 0x80 && char <= 0x9f) {
			return -1
		}
		return char
	}, path)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "(invalid path)"
	}
	if utf8.RuneCountInString(cleaned) > 240 {
		runes := []rune(cleaned)
		return string(runes[:240]) + "…"
	}
	return cleaned
}

// autoCommitPathExclusion reports "internal" for AHA/git metadata, which is
// never committed, and a block reason for path shapes that must never enter a
// batch. Paths outside the vault root are rejected by the caller.
func autoCommitPathExclusion(path string) string {
	path = filepath.ToSlash(path)
	for _, prefix := range []string{".aha/", ".git/", ".obsidian/"} {
		if path == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(path, prefix) {
			return "internal"
		}
	}
	if err := ValidateRelativePath(path); err != nil {
		return "invalid_path"
	}
	return ""
}

var secretBasenames = []string{
	".netrc", ".git-credentials", ".npmrc", ".pypirc", "credentials.json",
}

// secretIdentityNames are private key files; the matching public key
// (`id_rsa.pub`) is harmless and must stay auto-committable.
var secretIdentityNames = []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"}

// autoCommitBinaryExtensions mirrors the -text rules in .gitattributes so a
// binary file whose first bytes happen to look like text is still refused.
var autoCommitBinaryExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".ico": true, ".bmp": true, ".tif": true, ".tiff": true, ".psd": true,
	".pdf": true, ".zip": true, ".gz": true, ".tgz": true, ".bz2": true,
	".xz": true, ".7z": true, ".rar": true, ".tar": true, ".zst": true,
	".mp3": true, ".wav": true, ".flac": true, ".ogg": true, ".m4a": true,
	".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".webm": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true,
	".pptx": true, ".odt": true, ".ods": true, ".odp": true,
	".sqlite": true, ".sqlite3": true, ".db": true, ".bin": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".class": true,
	".jar": true, ".war": true, ".wasm": true, ".woff": true, ".woff2": true,
	".ttf": true, ".otf": true, ".eot": true, ".icns": true,
}

func isAutoCommitBinaryPath(path string) bool {
	return autoCommitBinaryExtensions[strings.ToLower(filepath.Ext(path))]
}

func autoCommitBlockedPath(path string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := lower
	if index := strings.LastIndex(lower, "/"); index >= 0 {
		base = lower[index+1:]
	}
	if base == ".env" {
		return "secret"
	}
	if strings.HasPrefix(base, ".env.") {
		// Templates carry no live values and are common in a knowledge vault.
		switch strings.TrimPrefix(base, ".env.") {
		case "example", "sample", "template", "dist", "md":
		default:
			return "secret"
		}
	}
	for _, name := range secretBasenames {
		if base == name {
			return "secret"
		}
	}
	for _, name := range secretIdentityNames {
		if base == name {
			return "secret"
		}
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".keystore", ".jks", ".ppk"} {
		if strings.HasSuffix(base, suffix) {
			return "secret"
		}
	}
	return ""
}

// containsSecretMaterial reuses the exact patterns the Agent write path already
// rejects, so the background commit gate cannot be looser than the front door.
func containsSecretMaterial(content []byte) bool {
	if len(content) == 0 {
		return false
	}
	for _, pattern := range forbiddenKnowledgePatterns {
		if pattern.Match(content) {
			return true
		}
	}
	return false
}

func looksBinary(content []byte) bool {
	if len(content) == 0 {
		return false
	}
	if len(content) >= 2 && ((content[0] == 0xFF && content[1] == 0xFE) || (content[0] == 0xFE && content[1] == 0xFF)) {
		return true
	}
	for _, value := range content {
		if value == 0 {
			return true
		}
	}
	return false
}

// readBoundedRegularFile reads at most limit bytes and reports when the file
// grew past the limit. LimitReader + ReadAll avoids the short-read trap of a
// single Read call.
func readBoundedRegularFile(path string, limit int) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(content) > limit {
		return content[:limit], true, nil
	}
	return content, false, nil
}

func isUnmergedCode(code string) bool {
	if strings.Contains(code, "U") {
		return true
	}
	switch code {
	case "DD", "AA":
		return true
	}
	return false
}
