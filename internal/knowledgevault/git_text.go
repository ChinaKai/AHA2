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

// lineEndingGitConfig is passed as command-scoped `-c` overrides to every git
// command the migration relies on. The repository-local policy is written
// first, but pinning the same values per command keeps the decision stable if
// a machine-wide core.autocrlf=true (the Git for Windows default) is present,
// or if the repo-local setting is changed between two preflight steps.
// safecrlf is disabled for the migration's own commands because rewriting
// CRLF blobs to LF is exactly what it is asked to do.
var lineEndingGitConfig = []string{"core.autocrlf=false", "core.safecrlf=false"}

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

// CRLFCounts is the read-only line-ending inventory of a vault. Only records
// without an explicit owner line-ending policy are counted as migration
// candidates; files whose attributes say `-text` or `eol=crlf` are the owner's
// deliberate choice and are never rewritten.
type CRLFCounts struct {
	// IndexCRLF counts tracked blobs that contain CRLF. Creating an `eol=lf`
	// policy while such blobs exist makes every one of them look modified and
	// the next auto-commit would rewrite them in bulk.
	IndexCRLF int `json:"index_crlf"`
	// IndexMixed counts blobs with both CRLF and LF. Git reports `i/mixed`;
	// they are just as unsafe to touch automatically as `i/crlf`.
	IndexMixed int `json:"index_mixed"`
	// ExplicitCRLF counts files whose attributes mark them binary (`-text`,
	// including `binary`/`-crlf`). Git never reports those as modified and
	// renormalize never stages them, so they are safe to leave alone.
	// `eol=crlf` is deliberately NOT included: its CRLF blob is reported as
	// modified and would be rewritten by the next ordinary add.
	ExplicitCRLF int `json:"explicit_crlf"`
	// WorktreeCRLF counts checked-out files with CRLF or mixed endings. When
	// the index is already LF those are clean under the new policy (the clean
	// filter normalizes them), so they are reported but do not block it.
	WorktreeCRLF int `json:"worktree_crlf"`
}

// NeedsRewrite reports whether automatic migration would change blobs. Mixed
// endings are included: the owner may have edited a note on Windows, and a
// silent normalization would rewrite their content.
func (c CRLFCounts) NeedsRewrite() int { return c.IndexCRLF + c.IndexMixed }

// GitTextPolicyResult reports what EnsureTextPolicy changed so the settings
// audit log can distinguish a first migration from a no-op upgrade check.
type GitTextPolicyResult struct {
	AttributesChanged bool   `json:"attributes_changed"`
	AttributesExisted bool   `json:"attributes_existed"`
	AttributesPath    string `json:"attributes_path"`
	Autocrlf          string `json:"autocrlf"`
	CRLFCounts
}

// GitAttributesContent returns the policy file AHA2 writes on a fresh vault.
func GitAttributesContent() string {
	lines := append([]string{}, gitAttributesRequiredLines...)
	lines = append(lines, gitAttributesBinaryLines...)
	return strings.Join(lines, "\n") + "\n"
}

// CountCRLF reports the line-ending inventory of the vault. It is a read-only
// `git ls-files --eol` and includes untracked files, so an upgrade also notices
// a CRLF document that has not been committed yet.
func (g *Git) CountCRLF(ctx context.Context) (CRLFCounts, error) {
	return g.countCRLF(ctx)
}

// EnsureTextPolicy pins the repository-local core.autocrlf setting and, only
// when no .gitattributes exists yet, writes the AHA2 LF policy. An existing
// policy file is owner-authored state and is never rewritten, not even to add
// missing default lines. It is idempotent.
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
	path := filepath.Join(g.vaultPath, gitAttributesFile)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return result, fmt.Errorf("%s 不是普通文件", gitAttributesFile)
		}
		// Never overwrite an existing policy file. The owner may have authored
		// rules that AHA2 must not reorder or extend behind their back.
		result.AttributesExisted = true
		if _, err := g.command(ctx, "config", "--local", "core.autocrlf", "false"); err != nil {
			return result, err
		}
		result.Autocrlf = "false"
		counts, err := g.countCRLF(ctx)
		if err != nil {
			return result, err
		}
		result.CRLFCounts = counts
		return result, nil
	} else if !os.IsNotExist(err) {
		return result, err
	}

	counts, err := g.countCRLF(ctx)
	if err != nil {
		return result, err
	}
	result.CRLFCounts = counts
	if result.NeedsRewrite() > 0 {
		// Do not create the LF policy yet: with CRLF or mixed blobs in the index
		// it would make them all look modified and the next commit would rewrite
		// them in bulk. The preflight reports the count and leaves the vault
		// untouched.
		return result, nil
	}
	if _, err := g.command(ctx, "config", "--local", "core.autocrlf", "false"); err != nil {
		return result, err
	}
	result.Autocrlf = "false"
	if err := writeVaultFileIfChanged(g.vaultPath, path, []byte(GitAttributesContent()), 0o644); err != nil {
		return result, err
	}
	result.AttributesChanged = true
	return result, nil
}

// countCRLF parses `git ls-files --eol`. A record looks like
// `i/crlf w/crlf attr/-text\tpath` (columns padded with spaces); the attr
// column may itself contain spaces (`text eol=crlf`). Only `-text` records are
// excluded from the migration counts: git never reports them as modified.
// `eol=crlf` records with CRLF blobs stay in the counts and trigger the
// preflight pause, because any ordinary add would rewrite their blob to LF.
func (g *Git) countCRLF(ctx context.Context) (CRLFCounts, error) {
	counts := CRLFCounts{}
	output, err := g.commandRawConfig(ctx, lineEndingGitConfig, "ls-files", "--eol", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return counts, err
	}
	for _, record := range splitNullList(output) {
		tab := strings.IndexByte(record, '\t')
		if tab < 0 {
			continue
		}
		fields := strings.Fields(record[:tab])
		if len(fields) < 2 {
			continue
		}
		explicit := false
		for index, field := range fields[2:] {
			if index == 0 {
				field = strings.TrimPrefix(field, "attr/")
			}
			// Only `-text` (including `binary`/`-crlf`) is a real exclusion:
			// git treats those files as binary, never reports them modified and
			// `git add --renormalize` never stages them. `eol=crlf` is NOT
			// safe to exclude: git reports the CRLF blob as modified and the
			// next ordinary `git add` (including the quiet-period auto-commit)
			// rewrites the index blob to LF. It must stay in the rewrite count
			// so the preflight pauses instead.
			if field == "-text" {
				explicit = true
				break
			}
		}
		indexCRLF := fields[0] == "i/crlf" || fields[0] == "i/mixed"
		worktreeCRLF := fields[1] == "w/crlf" || fields[1] == "w/mixed"
		if explicit {
			if indexCRLF || worktreeCRLF {
				counts.ExplicitCRLF++
			}
			continue
		}
		switch fields[0] {
		case "i/crlf":
			counts.IndexCRLF++
		case "i/mixed":
			counts.IndexMixed++
		}
		if worktreeCRLF {
			counts.WorktreeCRLF++
		}
	}
	return counts, nil
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
	before, err := g.commandConfig(ctx, lineEndingGitConfig, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return result, err
	}
	result.Before = strings.TrimSpace(before)

	// Stage the policy file explicitly (renormalize only covers tracked files)
	// and then let git convert every tracked blob through the new filters.
	if _, err := g.commandConfig(ctx, lineEndingGitConfig, "add", "--", literalPathspec(gitAttributesFile)); err != nil {
		return result, err
	}
	if _, err := g.commandConfig(ctx, lineEndingGitConfig, "add", "--renormalize", "."); err != nil {
		return result, err
	}
	staged, err := g.commandRawConfig(ctx, lineEndingGitConfig, "diff", "--cached", "--name-only", "-z", "HEAD")
	if err != nil {
		return result, err
	}
	names := splitNullList(staged)
	if len(names) == 0 {
		result.Message = "行尾已经是 LF，无需规范化"
		return result, nil
	}
	// The policy file is staged explicitly above, so it is always part of the
	// first commit. It is not a renormalized document: an all-LF vault needs a
	// policy commit but no line-ending migration, and must not create a backup
	// tag or claim that it rewrote files.
	renormalized := make([]string, 0, len(names))
	for _, name := range names {
		if name != gitAttributesFile {
			renormalized = append(renormalized, name)
		}
	}
	result.Files = len(renormalized)
	message := "Add knowledge vault LF line-ending policy\n\nNo tracked blob needed renormalization: * text=auto eol=lf, core.autocrlf=false.\n"
	if len(renormalized) > 0 {
		result.Ran = true
		if tagErr := g.ensureLineEndingBackupTag(ctx); tagErr != nil {
			// The exact pre-migration HEAD is still recorded in Before and in
			// .aha/line-endings.json, so the missing convenience tag is a warning,
			// not a reason to leave the migration half done.
			result.Warning = "备份 tag 创建失败：" + tagErr.Error()
		} else {
			result.Tag = lineEndingBackupTag
		}
		message = fmt.Sprintf("Normalize knowledge vault line endings to LF\n\nOne-time migration: * text=auto eol=lf, core.autocrlf=false.\nBefore: %s\nFiles: %d\n", result.Before, result.Files)
	}
	if _, err := g.commandConfig(ctx, lineEndingGitConfig, "commit", "-q", "-m", message); err != nil {
		// The staged index and working tree are intentionally left in place:
		// the next preflight or owner action can inspect them, and nothing here
		// resets or overwrites the owner's files.
		return result, err
	}
	after, err := g.commandConfig(ctx, lineEndingGitConfig, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return result, err
	}
	result.After = strings.TrimSpace(after)
	result.Committed = true
	if !result.Ran {
		result.Message = "行尾策略已写入；现有文件已经是 LF，无需规范化"
		return result, nil
	}
	result.Message = fmt.Sprintf("已规范化 %d 个文件的行尾", result.Files)
	if err := g.recordLineEndingState(result); err != nil {
		return result, err
	}
	return result, nil
}

func (g *Git) ensureLineEndingBackupTag(ctx context.Context) error {
	if _, err := g.commandConfig(ctx, lineEndingGitConfig, "rev-parse", "--verify", "--quiet", "refs/tags/"+lineEndingBackupTag); err == nil {
		return nil
	}
	_, err := g.commandConfig(ctx, lineEndingGitConfig, "tag", lineEndingBackupTag, "HEAD")
	return err
}

func (g *Git) requireNoGitOperation(ctx context.Context) error {
	gitDir := ""
	if output, err := g.commandConfig(ctx, lineEndingGitConfig, "rev-parse", "--git-dir"); err == nil {
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
	output, err := g.commandRawConfig(ctx, lineEndingGitConfig, "ls-files", "-u", "-z")
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
	output, err := g.commandRawConfig(ctx, lineEndingGitConfig, "status", "--porcelain", "-z", "--untracked-files=all")
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
	changed, err := g.commandRawConfig(ctx, lineEndingGitConfig, args[0], args[1:]...)
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
