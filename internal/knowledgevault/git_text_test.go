package knowledgevault

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitOutputBytes(t *testing.T, directory string, args ...string) []byte {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return output
}

func TestEnsureTextPolicyWritesAttributesAndRepoLocalAutocrlf(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	// Init applies the policy for fresh vaults; this test exercises the
	// standalone upgrade path (a repo created before the policy existed).
	testGitCommand(t, config.VaultPath, "init", "-b", "main")
	result, err := git.EnsureTextPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AttributesChanged || result.Autocrlf != "false" {
		t.Fatalf("unexpected policy result: %+v", result)
	}
	content, err := os.ReadFile(filepath.Join(config.VaultPath, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"* text=auto eol=lf", "*.png -text", "*.pdf -text", "*.sqlite -text"} {
		if !strings.Contains(string(content), required+"\n") {
			t.Fatalf(".gitattributes missing %q:\n%s", required, content)
		}
	}
	if configured := testGitCommand(t, config.VaultPath, "config", "--local", "--get", "core.autocrlf"); configured != "false" {
		t.Fatalf("core.autocrlf = %q, want false", configured)
	}

	again, err := git.EnsureTextPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again.AttributesChanged {
		t.Fatal("second policy application rewrote an already-correct .gitattributes")
	}
	if !again.AttributesExisted {
		t.Fatalf("second policy application did not report the existing file: %+v", again)
	}

	// An owner-authored file is never rewritten, not even to add AHA2 defaults.
	path := filepath.Join(config.VaultPath, ".gitattributes")
	owner := "*.custom text\n"
	if err := os.WriteFile(path, []byte(owner), 0o644); err != nil {
		t.Fatal(err)
	}
	ownerResult, err := git.EnsureTextPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ownerResult.AttributesChanged || !ownerResult.AttributesExisted {
		t.Fatalf("owner policy file was modified: %+v", ownerResult)
	}
	content, _ = os.ReadFile(path)
	if string(content) != owner {
		t.Fatalf("owner .gitattributes was rewritten:\n%s", content)
	}
}

func TestEnsureTextPolicyReportsCRLFBlobsWithoutWritingPolicy(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	testGitCommand(t, config.VaultPath, "init", "-b", "main")
	testGitCommand(t, config.VaultPath, "config", "core.autocrlf", "false")
	writeGitTestFile(t, config.VaultPath, "global/general/crlf.md", "# CRLF\r\n\r\nbody\r\n")
	testGitCommand(t, config.VaultPath, "add", "-A")
	testGitCommand(t, config.VaultPath, "commit", "-m", "crlf baseline")
	blobBefore := gitOutputBytes(t, config.VaultPath, "show", "HEAD:global/general/crlf.md")

	result, err := git.EnsureTextPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.IndexCRLF != 1 {
		t.Fatalf("index CRLF count = %d, want 1: %+v", result.IndexCRLF, result)
	}
	if result.AttributesChanged || result.AttributesExisted {
		t.Fatalf("policy was written over CRLF blobs: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, ".gitattributes")); !os.IsNotExist(err) {
		t.Fatalf("policy file appeared despite CRLF blobs: %v", err)
	}
	if blobAfter := gitOutputBytes(t, config.VaultPath, "show", "HEAD:global/general/crlf.md"); string(blobAfter) != string(blobBefore) {
		t.Fatalf("policy check rewrote the CRLF blob: %q -> %q", blobBefore, blobAfter)
	}
}

func TestRenormalizeCommitsOnceAndSecondRunIsNoop(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	// A repo from before the policy existed: no .gitattributes, CRLF blobs.
	testGitCommand(t, config.VaultPath, "init", "-b", "main")
	testGitCommand(t, config.VaultPath, "config", "core.autocrlf", "false")
	writeGitTestFile(t, config.VaultPath, "global/general/crlf.md", "# CRLF\r\n\r\nbody\r\n")
	testGitCommand(t, config.VaultPath, "add", "-A")
	testGitCommand(t, config.VaultPath, "commit", "-m", "crlf baseline")
	blobBefore := gitOutputBytes(t, config.VaultPath, "show", "HEAD:global/general/crlf.md")
	if !strings.Contains(string(blobBefore), "\r\n") {
		t.Fatalf("test baseline is not CRLF: %q", blobBefore)
	}

	// Renormalize is the explicit migration entry point. The auto-sync
	// preflight refuses to create the policy over CRLF blobs, so a caller that
	// wants the migration supplies the policy file directly.
	writeGitTestFile(t, config.VaultPath, ".gitattributes", GitAttributesContent())
	headBefore := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")
	result, err := git.Renormalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ran || !result.Committed || result.Files == 0 {
		t.Fatalf("unexpected first renormalize: %+v", result)
	}
	if result.Before != headBefore {
		t.Fatalf("backup HEAD = %s, want %s", result.Before, headBefore)
	}
	if result.Tag == "" {
		t.Fatal("renormalize did not record a backup tag")
	}
	if tag := testGitCommand(t, config.VaultPath, "rev-parse", "refs/tags/"+lineEndingBackupTag); tag != headBefore {
		t.Fatalf("backup tag points at %s, want %s", tag, headBefore)
	}
	blobAfter := gitOutputBytes(t, config.VaultPath, "show", "HEAD:global/general/crlf.md")
	if strings.Contains(string(blobAfter), "\r\n") {
		t.Fatalf("renormalized blob still has CRLF: %q", blobAfter)
	}
	if dirty := testGitCommand(t, config.VaultPath, "status", "--porcelain"); dirty != "" {
		t.Fatalf("worktree not clean after renormalize:\n%s", dirty)
	}

	headAfter := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")
	second, err := git.Renormalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Ran || second.Committed || second.Files != 0 {
		t.Fatalf("second renormalize was not a no-op: %+v", second)
	}
	if head := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD"); head != headAfter {
		t.Fatalf("second renormalize changed HEAD: %s -> %s", headAfter, head)
	}
}

func TestRenormalizeAllLFVaultCommitsPolicyWithoutMigration(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	// Upgrade fixture: policy file missing, all blobs already LF.
	if err := os.Remove(filepath.Join(config.VaultPath, ".gitattributes")); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit(ctx, "drop policy for upgrade fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := git.EnsureTextPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	headBefore := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")
	result, err := git.Renormalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The policy file still needs a commit, but no blob was renormalized: no
	// migration claim, no backup tag, no line-ending state file.
	if result.Ran || !result.Committed || result.Files != 0 {
		t.Fatalf("all-LF vault reported a migration: %+v", result)
	}
	if result.Tag != "" {
		t.Fatalf("all-LF vault created a backup tag: %+v", result)
	}
	if strings.Contains(result.Message, "已规范化") {
		t.Fatalf("all-LF vault claimed renormalization: %q", result.Message)
	}
	headAfter := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")
	if headAfter == headBefore {
		t.Fatal("policy commit did not move HEAD")
	}
	commitMessage := testGitCommand(t, config.VaultPath, "log", "-1", "--format=%s")
	if !strings.Contains(commitMessage, "Add knowledge vault LF line-ending policy") {
		t.Fatalf("policy commit message = %q", commitMessage)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, stateDirectory, lineEndingStateFile)); !os.IsNotExist(err) {
		t.Fatalf("all-LF vault recorded a line-ending migration: %v", err)
	}
	if dirty := testGitCommand(t, config.VaultPath, "status", "--porcelain"); dirty != "" {
		t.Fatalf("worktree not clean after the policy commit:\n%s", dirty)
	}
	second, err := git.Renormalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Committed || second.Files != 0 {
		t.Fatalf("second renormalize was not a no-op: %+v", second)
	}
}

func TestRenormalizeRefusesRealEditsAndMergeStateWithoutResetting(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/note.md", "# Note\n")
	if _, err := git.Commit(ctx, "note"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/note.md", "# Note edited\n")
	if _, err := git.EnsureTextPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	before := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")
	_, err := git.Renormalize(ctx)
	if !errors.Is(err, ErrGitDirtyForRenormalize) {
		t.Fatalf("renormalize with a real edit = %v, want ErrGitDirtyForRenormalize", err)
	}
	if after := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD"); after != before {
		t.Fatalf("refused renormalize changed HEAD: %s -> %s", before, after)
	}
	if content, _ := os.ReadFile(filepath.Join(config.VaultPath, "global/general/note.md")); string(content) != "# Note edited\n" {
		t.Fatalf("refused renormalize overwrote the owner edit: %q", content)
	}

	// A merge marker must stop the migration even when the tree is clean.
	writeGitTestFile(t, config.VaultPath, "global/general/note.md", "# Note\n")
	if _, err := git.Commit(ctx, "restore"); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(config.VaultPath, ".git")
	if err := os.WriteFile(filepath.Join(gitDir, "MERGE_HEAD"), []byte(strings.Repeat("a", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	headBeforeMerge := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")
	if _, err := git.Renormalize(ctx); !errors.Is(err, ErrGitOperationInProgress) {
		t.Fatalf("renormalize during merge = %v, want ErrGitOperationInProgress", err)
	}
	if _, err := os.Stat(filepath.Join(gitDir, "MERGE_HEAD")); err != nil {
		t.Fatalf("refused renormalize removed the merge marker: %v", err)
	}
	if head := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD"); head != headBeforeMerge {
		t.Fatalf("refused renormalize changed HEAD: %s -> %s", headBeforeMerge, head)
	}
}

func TestEnsureTextPolicyNeverOverwritesExistingAttributes(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	testGitCommand(t, config.VaultPath, "init", "-b", "main")
	owner := strings.Join([]string{
		"*.md text eol=crlf",
		"*.custom -text",
		"*.png text eol=lf",
		"",
	}, "\n")
	writeGitTestFile(t, config.VaultPath, ".gitattributes", owner)
	writeGitTestFile(t, config.VaultPath, "note.md", "# Note\n")
	writeGitTestFile(t, config.VaultPath, "asset.custom", "payload")
	writeGitTestFile(t, config.VaultPath, "logo.png", "png")
	result, err := git.EnsureTextPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AttributesExisted || result.AttributesChanged {
		t.Fatalf("existing policy was not left alone: %+v", result)
	}
	content, err := os.ReadFile(filepath.Join(config.VaultPath, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != owner {
		t.Fatalf("existing .gitattributes was rewritten:\n%s", content)
	}
	if eol := gitCheckAttr(t, config.VaultPath, "eol", "note.md"); eol != "crlf" {
		t.Fatalf("author eol rule lost: %q", eol)
	}
	if text := gitCheckAttr(t, config.VaultPath, "text", "asset.custom"); text != "unset" {
		t.Fatalf("author custom binary rule lost: %q", text)
	}
	if text := gitCheckAttr(t, config.VaultPath, "text", "logo.png"); text != "set" {
		t.Fatalf("author override of a default binary extension lost: %q", text)
	}
	if eol := gitCheckAttr(t, config.VaultPath, "eol", "logo.png"); eol != "lf" {
		t.Fatalf("author eol override lost: %q", eol)
	}
	// A second call is still a no-op.
	again, err := git.EnsureTextPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again.AttributesChanged || !again.AttributesExisted {
		t.Fatalf("second policy call changed the owner file: %+v", again)
	}
}

func gitCheckAttr(t *testing.T, directory, attr, target string) string {
	t.Helper()
	output := testGitCommand(t, directory, "check-attr", attr, "--", target)
	parts := strings.SplitN(output, ": ", 3)
	if len(parts) != 3 {
		t.Fatalf("unexpected check-attr output for %s %s: %q", attr, target, output)
	}
	return parts[2]
}
