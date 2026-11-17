package knowledgevault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Line-ending policy for the vault repository.
//
// The vault is edited by humans (VSCode, Obsidian) on Windows and Linux and is
// also written by AHA2. A system-level core.autocrlf=true would otherwise make
// the same document look modified depending on which host touched it last, so
// the repository pins LF for every text file and treats common binary formats
// as binary explicitly. These settings are repo-local and never touch global
// git configuration.

const (
	gitAttributesFile = ".gitattributes"
	// lineEndingBackupTag is created once, immediately before the one-time
	// renormalization commit. It is a plain local tag: the owner can compare or
	// reset to it manually, and nothing in AHA2 ever deletes it.
	lineEndingBackupTag = "aha-pre-lf-renormalize"
	lineEndingStateFile = "line-endings.json"
)

// gitAttributesRequiredLines is the text policy AHA2 guarantees. Existing
// owner rules are preserved; only missing lines are appended.
var gitAttributesRequiredLines = []string{
	"* text=auto eol=lf",
}

// gitAttributesBinaryLines marks formats that must never be line-converted.
// The list is intentionally explicit instead of relying on content sniffing:
// .gitattributes has to decide before git reads the blob.
var gitAttributesBinaryLines = []string{
	"*.png -text", "*.jpg -text", "*.jpeg -text", "*.gif -text", "*.webp -text",
	"*.ico -text", "*.bmp -text", "*.tif -text", "*.tiff -text", "*.psd -text",
	"*.pdf -text", "*.zip -text", "*.gz -text", "*.tgz -text", "*.bz2 -text",
	"*.xz -text", "*.7z -text", "*.rar -text", "*.tar -text", "*.zst -text",
	"*.mp3 -text", "*.wav -text", "*.flac -text", "*.ogg -text", "*.m4a -text",
	"*.mp4 -text", "*.mov -text", "*.avi -text", "*.mkv -text", "*.webm -text",
	"*.doc -text", "*.docx -text", "*.xls -text", "*.xlsx -text", "*.ppt -text",
	"*.pptx -text", "*.odt -text", "*.ods -text", "*.odp -text",
	"*.sqlite -text", "*.sqlite3 -text", "*.db -text", "*.bin -text",
	"*.exe -text", "*.dll -text", "*.so -text", "*.dylib -text", "*.class -text",
	"*.jar -text", "*.war -text", "*.wasm -text", "*.woff -text", "*.woff2 -text",
	"*.ttf -text", "*.otf -text", "*.eot -text", "*.icns -text",
}

// GitTextPolicyResult reports what EnsureTextPolicy changed so the settings
// audit log can distinguish a first migration from a no-op upgrade check.
type GitTextPolicyResult struct {
	AttributesChanged bool   `json:"attributes_changed"`
	AttributesPath    string `json:"attributes_path"`
	Autocrlf          string `json:"autocrlf"`
}

// GitAttributesContent returns the policy file AHA2 writes on a fresh vault.
func GitAttributesContent() string {
	lines := append([]string{}, gitAttributesRequiredLines...)
	lines = append(lines, gitAttributesBinaryLines...)
	return strings.Join(lines, "\n") + "\n"
}

// EnsureTextPolicy writes the missing text policy lines and pins the
// repository-local core.autocrlf setting. It is idempotent and preserves any
// owner-authored rules in an existing .gitattributes.
func (g *Git) EnsureTextPolicy(ctx context.Context) (GitTextPolicyResult, error) {
	unlock := g.lockRepo()
	defer unlock()
	if err := g.requireRepository(ctx); err != nil {
		return GitTextPolicyResult{}, err
	}
	return g.ensureTextPolicy(ctx)
}

// ensureTextPolicy is the lock-free implementation. Callers either hold the
// repository lock (Renormalize/Init) or go through EnsureTextPolicy.
func (g *Git) ensureTextPolicy(ctx context.Context) (GitTextPolicyResult, error) {
	result := GitTextPolicyResult{AttributesPath: filepath.Join(g.vaultPath, gitAttributesFile)}
	if err := g.requireRepository(ctx); err != nil {
		return result, err
	}

	if _, err := g.command(ctx, "config", "--local", "core.autocrlf", "false"); err != nil {
		return result, err
	}
	result.Autocrlf = "false"

	path := filepath.Join(g.vaultPath, gitAttributesFile)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	ahaLines := append(append([]string{}, gitAttributesRequiredLines...), gitAttributesBinaryLines...)
	content := string(existing)
	changed := false
	if strings.TrimSpace(content) == "" {
		content = GitAttributesContent()
		changed = true
	} else {
		// gitattributes is last-match-wins. AHA2's defaults must therefore come
		// first so an owner rule such as `*.md text eol=crlf` or a custom
		// binary extension keeps winning. Move any AHA2-owned line that an
		// older version appended to the end back to the front.
		ahaSet := make(map[string]bool, len(ahaLines))
		for _, line := range ahaLines {
			ahaSet[line] = true
		}
		authorLines := make([]string, 0, 32)
		for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
			if ahaSet[strings.TrimSpace(line)] {
				continue
			}
			authorLines = append(authorLines, line)
		}
		for len(authorLines) > 0 && strings.TrimSpace(authorLines[0]) == "" {
			authorLines = authorLines[1:]
		}
		for len(authorLines) > 0 && strings.TrimSpace(authorLines[len(authorLines)-1]) == "" {
			authorLines = authorLines[:len(authorLines)-1]
		}
		authorText := strings.Join(authorLines, "\n")
		merged := strings.Join(ahaLines, "\n")
		if authorText != "" {
			merged += "\n" + authorText
		}
		merged += "\n"
		if merged != content {
			content = merged
			changed = true
		}
	}
	if changed {
		if err := writeVaultFileIfChanged(g.vaultPath, path, []byte(content), 0o644); err != nil {
			return result, err
		}
	}
	result.AttributesChanged = changed
	return result, nil
}

// RenormalizeResult describes the one-time line-ending migration.
type RenormalizeResult struct {
	Ran       bool   `json:"ran"`
	Committed bool   `json:"committed"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
	Files     int    `json:"files"`
	Tag       string `json:"tag,omitempty"`
	Warning   string `json:"warning,omitempty"`
	Message   string `json:"message,omitempty"`
}

var (
	// ErrGitOperationInProgress refuses to renormalize during a merge, rebase,
	// cherry-pick, revert or bisect. The migration never tries to resolve or
	// abort an operation the owner started.
	ErrGitOperationInProgress = errors.New("git 仓库有进行中的合并/变基/拣选操作")
	// ErrGitUnmerged refuses to renormalize while index entries are unmerged.
	ErrGitUnmerged = errors.New("git 仓库存在未解决的冲突")
	// ErrGitDirtyForRenormalize refuses to renormalize when any file other than
	// the policy file has uncommitted changes. Callers must commit those first
	// (the preflight does) instead of overwriting the owner's work.
	ErrGitDirtyForRenormalize = errors.New("git 工作区有未提交改动，未执行行尾规范化")
)

// Renormalize applies the current .gitattributes to every tracked file with
// one `git add --renormalize .` and commits the result once. It is safe to run
// repeatedly: after the first successful migration a second run stages
// nothing and makes no commit. Any precondition failure returns before a
// commit, leaving the working tree and index exactly as they were.
func (g *Git) Renormalize(ctx context.Context) (RenormalizeResult, error) {
	result := RenormalizeResult{}
	if err := g.requireVault(ctx); err != nil {
		return result, err
	}
	unlock := g.lockRepo()
	defer unlock()
	if !g.hasCommit(ctx) {
		return result, ErrGitNoCommit
	}
	if err := g.requireNoGitOperation(ctx); err != nil {
		return result, err
	}
	if err := g.requireNoUnmerged(ctx); err != nil {
		return result, err
	}
	if err := g.requireCleanExceptAttributes(ctx); err != nil {
		return result, err
	}
	attributesPath := filepath.Join(g.vaultPath, gitAttributesFile)
	if _, err := os.Stat(attributesPath); err != nil {
		if os.IsNotExist(err) {
			return result, fmt.Errorf("缺少 %s；请先执行文本策略", gitAttributesFile)
		}
		return result, err
	}
	before, err := g.command(ctx, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return result, err
	}
	result.Before = strings.TrimSpace(before)

	// Stage the policy file explicitly (renormalize only covers tracked files)
	// and then let git convert every tracked blob through the new filters.
	if _, err := g.command(ctx, "add", "--", literalPathspec(gitAttributesFile)); err != nil {
		return result, err
	}
	if _, err := g.command(ctx, "add", "--renormalize", "."); err != nil {
		return result, err
	}
	staged, err := g.commandRaw(ctx, "diff", "--cached", "--name-only", "-z", "HEAD")
	if err != nil {
		return result, err
	}
	names := splitNullList(staged)
	result.Files = len(names)
	if len(names) == 0 {
		result.Message = "行尾已经是 LF，无需规范化"
		return result, nil
	}
	result.Ran = true
	if tagErr := g.ensureLineEndingBackupTag(ctx); tagErr != nil {
		// The exact pre-migration HEAD is still recorded in Before and in
		// .aha/line-endings.json, so the missing convenience tag is a warning,
		// not a reason to leave the migration half done.
		result.Warning = "备份 tag 创建失败：" + tagErr.Error()
	} else {
		result.Tag = lineEndingBackupTag
	}
	message := fmt.Sprintf("Normalize knowledge vault line endings to LF\n\nOne-time migration: * text=auto eol=lf, core.autocrlf=false.\nBefore: %s\nFiles: %d\n", result.Before, result.Files)
	if _, err := g.command(ctx, "commit", "-q", "-m", message); err != nil {
		// The staged index and working tree are intentionally left in place:
		// the next preflight or owner action can inspect them, and nothing here
		// resets or overwrites the owner's files.
		return result, err
	}
	after, err := g.command(ctx, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return result, err
	}
	result.After = strings.TrimSpace(after)
	result.Committed = true
	result.Message = fmt.Sprintf("已规范化 %d 个文件的行尾", result.Files)
	if err := g.recordLineEndingState(result); err != nil {
		return result, err
	}
	return result, nil
}

func (g *Git) ensureLineEndingBackupTag(ctx context.Context) error {
	if _, err := g.command(ctx, "rev-parse", "--verify", "--quiet", "refs/tags/"+lineEndingBackupTag); err == nil {
		return nil
	}
	_, err := g.command(ctx, "tag", lineEndingBackupTag, "HEAD")
	return err
}

func (g *Git) requireNoGitOperation(ctx context.Context) error {
	gitDir := ""
	if output, err := g.command(ctx, "rev-parse", "--git-dir"); err == nil {
		gitDir = strings.TrimSpace(output)
	}
	if gitDir == "" {
		gitDir = filepath.Join(g.vaultPath, ".git")
	} else if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(g.vaultPath, gitDir)
	}
	for _, marker := range []string{
		"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG",
		"rebase-merge", "rebase-apply", "sequencer",
	} {
		if _, err := os.Lstat(filepath.Join(gitDir, marker)); err == nil {
			return ErrGitOperationInProgress
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (g *Git) requireNoUnmerged(ctx context.Context) error {
	output, err := g.commandRaw(ctx, "ls-files", "-u", "-z")
	if err != nil {
		return err
	}
	if len(splitNullList(output)) > 0 {
		return ErrGitUnmerged
	}
	return nil
}

// requireCleanExceptAttributes permits exactly the policy file the migration
// itself just wrote and files whose only difference is CRLF. Writing
// `* text=auto eol=lf` makes every previously committed CRLF blob look
// modified; those are the renormalization targets. A file with a real content
// edit still fails, so the migration can never swallow the owner's work.
func (g *Git) requireCleanExceptAttributes(ctx context.Context) error {
	output, err := g.commandRaw(ctx, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	var lineEndingOnly []string
	for _, record := range splitStatusRecords(output) {
		if len(record.paths) == 1 && record.paths[0] == gitAttributesFile {
			continue
		}
		// AHA/git/editor metadata is never committed by auto-sync, so it must
		// not block the migration even when a vault's .gitignore predates it.
		if autoCommitPathExclusion(record.paths[0]) == "internal" {
			continue
		}
		if strings.Contains(record.code, "?") || strings.Contains(record.code, "D") ||
			strings.Contains(record.code, "R") || strings.Contains(record.code, "C") {
			return fmt.Errorf("%w：%s", ErrGitDirtyForRenormalize, record.paths[0])
		}
		// A staged change (index differs from HEAD) is never ours; the
		// migration refuses instead of committing the owner's staged edit.
		if code := record.code; code[0] != ' ' && code[0] != '?' {
			return fmt.Errorf("%w：%s", ErrGitDirtyForRenormalize, record.paths[0])
		}
		for _, path := range record.paths {
			if path == gitAttributesFile {
				continue
			}
			lineEndingOnly = append(lineEndingOnly, path)
		}
	}
	if len(lineEndingOnly) == 0 {
		return nil
	}
	args := append([]string{"diff", "--ignore-cr-at-eol", "--name-only", "-z", "--"}, autoCommitPathspecs(lineEndingOnly)...)
	changed, err := g.commandRaw(ctx, args[0], args[1:]...)
	if err != nil {
		return err
	}
	if names := splitNullList(changed); len(names) > 0 {
		return fmt.Errorf("%w：%s", ErrGitDirtyForRenormalize, names[0])
	}
	return nil
}

func splitNullList(value string) []string {
	value = strings.TrimSuffix(value, "\x00")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, "\x00")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

type porcelainRecord struct {
	code  string
	paths []string
}

// splitStatusRecords parses `git status --porcelain -z` output. A rename or
// copy record carries the destination first and the source as a second NUL
// field; both are returned so staging and commit pathspecs cover the deletion.
func splitStatusRecords(output string) []porcelainRecord {
	fields := strings.Split(output, "\x00")
	result := make([]porcelainRecord, 0, len(fields))
	for index := 0; index < len(fields); index++ {
		record := fields[index]
		if len(record) < 3 {
			continue
		}
		item := porcelainRecord{code: record[:2], paths: []string{record[3:]}}
		code := item.code
		if code[0] == 'R' || code[0] == 'C' || code[1] == 'R' || code[1] == 'C' {
			if index+1 < len(fields) {
				item.paths = append(item.paths, fields[index+1])
				index++
			}
		}
		result = append(result, item)
	}
	return result
}

func (g *Git) recordLineEndingState(result RenormalizeResult) error {
	state := struct {
		Version      int    `json:"version"`
		NormalizedAt string `json:"normalized_at"`
		Before       string `json:"before"`
		After        string `json:"after"`
		Files        int    `json:"files"`
		Tag          string `json:"tag,omitempty"`
		Warning      string `json:"warning,omitempty"`
	}{
		Version: 1, NormalizedAt: time.Now().UTC().Format(time.RFC3339),
		Before: result.Before, After: result.After, Files: result.Files, Tag: result.Tag,
		Warning: result.Warning,
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return writeVaultFileIfChanged(g.vaultPath, filepath.Join(g.vaultPath, stateDirectory, lineEndingStateFile), payload, 0o644)
}
