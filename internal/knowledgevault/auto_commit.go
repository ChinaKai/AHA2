package knowledgevault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	secretScanBytes            = 64 << 10
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
	Clean       bool            `json:"clean"`
	Unmerged    bool            `json:"unmerged"`
	Pending     int             `json:"pending"`
	Eligible    []string        `json:"eligible,omitempty"`
	Blocked     []BlockedChange `json:"blocked,omitempty"`
	Fingerprint string          `json:"-"`
}

// AutoCommitResult is the outcome of one gated batch.
type AutoCommitResult struct {
	Committed bool            `json:"committed"`
	Commit    string          `json:"commit,omitempty"`
	Files     []string        `json:"files,omitempty"`
	Blocked   []BlockedChange `json:"blocked,omitempty"`
	Pending   int             `json:"pending"`
	Message   string          `json:"message,omitempty"`
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
	for _, path := range untracked {
		if strings.Contains(path, "\n") {
			continue
		}
		if info, statErr := os.Lstat(filepath.Join(g.vaultPath, filepath.FromSlash(path))); statErr == nil && !info.IsDir() {
			hashInput = append(hashInput, path)
		}
	}
	hashes := g.hashWorktreePaths(ctx, hashInput)
	var fingerprint strings.Builder
	fingerprint.WriteString(raw)
	fingerprint.WriteByte(0)
	for _, path := range untracked {
		fingerprint.WriteString(path)
		fingerprint.WriteByte('=')
		fingerprint.WriteString(hashes[path])
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
// every change. It is safe to call concurrently with a knowledge write: git
// only observes atomically renamed files. No lock is taken so status refreshes
// stay cheap.
func (g *Git) ScanAutoCommit(ctx context.Context) (AutoCommitScan, error) {
	unlock := g.lockWriterBlocks()
	defer unlock()
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
	content, err := readPrefix(absolute, secretScanBytes)
	if err != nil {
		return "unreadable"
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
	result.Blocked, result.Pending = scan.Blocked, scan.Pending
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
	if utf8.RuneCountInString(path) > 240 {
		runes := []rune(path)
		return string(runes[:240]) + "…"
	}
	return path
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

var secretPathPatterns = []string{
	".env", ".netrc", ".git-credentials", ".npmrc", ".pypirc",
	"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "credentials.json",
}

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
	if strings.HasPrefix(base, ".env.") || base == ".env" {
		return "secret"
	}
	for _, pattern := range secretPathPatterns {
		if base == pattern || strings.HasPrefix(base, pattern+".") {
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

var (
	privateKeyPattern   = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)
	awsKeyPattern       = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	githubTokenPattern  = regexp.MustCompile(`\b(ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`)
	slackTokenPattern   = regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)
	anthropicKeyPattern = regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`)
)

func containsSecretMaterial(content []byte) bool {
	if len(content) == 0 {
		return false
	}
	return privateKeyPattern.Match(content) || awsKeyPattern.Match(content) ||
		githubTokenPattern.Match(content) || slackTokenPattern.Match(content) ||
		anthropicKeyPattern.Match(content)
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

func readPrefix(path string, limit int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	buffer := make([]byte, limit)
	read, err := file.Read(buffer)
	if err != nil && read == 0 {
		return nil, err
	}
	return buffer[:read], nil
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
