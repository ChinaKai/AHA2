package knowledgevault

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDocumentDiffShowsLastCommitThenUncommittedChange(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	const relative = "global/general/diff-note.md"
	writeGitTestFile(t, config.VaultPath, relative, "# Note\n\nfirst line\nsecond line\n")
	if _, err := git.Commit(ctx, "add diff note"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, relative, "# Note\n\nfirst line\nsecond line changed\nthird line\n")
	if _, err := git.Commit(ctx, "change diff note"); err != nil {
		t.Fatal(err)
	}
	commit := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")

	diff, err := git.DocumentDiff(ctx, relative)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Available || diff.Baseline != commit || diff.Commit != commit {
		t.Fatalf("unexpected committed diff: %+v", diff)
	}
	if diff.CommittedAt == "" || diff.Truncated {
		t.Fatalf("committed diff metadata: %+v", diff)
	}
	if diff.Added != 2 || diff.Removed != 1 {
		t.Fatalf("added/removed = %d/%d, want 2/1", diff.Added, diff.Removed)
	}
	if len(diff.Lines) < 3 || diff.Lines[0].Kind != "hunk" {
		t.Fatalf("diff must start with a hunk header: %+v", diff.Lines)
	}
	var addedText, removedText []string
	maxOld, maxNew := 0, 0
	for _, line := range diff.Lines {
		switch line.Kind {
		case "add":
			if line.NewNo == 0 || line.OldNo != 0 {
				t.Fatalf("add line numbers: %+v", line)
			}
			addedText = append(addedText, line.Text)
			if line.NewNo > maxNew {
				maxNew = line.NewNo
			}
		case "remove":
			if line.OldNo == 0 || line.NewNo != 0 {
				t.Fatalf("remove line numbers: %+v", line)
			}
			removedText = append(removedText, line.Text)
			if line.OldNo > maxOld {
				maxOld = line.OldNo
			}
		case "context":
			if line.OldNo == 0 || line.NewNo == 0 {
				t.Fatalf("context line numbers: %+v", line)
			}
			if line.OldNo > maxOld {
				maxOld = line.OldNo
			}
			if line.NewNo > maxNew {
				maxNew = line.NewNo
			}
		}
	}
	// The trailing newline of git's output must not turn into a context row
	// one line past the end of the file.
	if maxOld != 4 || maxNew != 5 {
		t.Fatalf("line numbers run to old=%d new=%d, want 4/5: %+v", maxOld, maxNew, diff.Lines)
	}
	if strings.Join(addedText, "\n") != "second line changed\nthird line" || strings.Join(removedText, "\n") != "second line" {
		t.Fatalf("diff bodies added=%q removed=%q", addedText, removedText)
	}

	// An unstaged edit beats the last commit and is reported as uncommitted.
	writeGitTestFile(t, config.VaultPath, relative, "# Note\n\nfirst line\nsecond line replaced\nthird line\n")
	diff, err = git.DocumentDiff(ctx, relative)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Available || diff.Baseline != "uncommitted" || diff.Commit != "" || diff.CommittedAt != "" {
		t.Fatalf("unexpected uncommitted diff: %+v", diff)
	}
	if diff.Added != 1 || diff.Removed != 1 {
		t.Fatalf("uncommitted added/removed = %d/%d, want 1/1", diff.Added, diff.Removed)
	}

	// A staged edit is still the latest change even though `git diff` alone
	// would be empty.
	testGitCommand(t, config.VaultPath, "add", "--", relative)
	diff, err = git.DocumentDiff(ctx, relative)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Available || diff.Baseline != "uncommitted" || diff.Added != 1 || diff.Removed != 1 {
		t.Fatalf("staged change was not reported: %+v", diff)
	}
}

func TestDocumentDiffUnavailableCasesAreExplicit(t *testing.T) {
	ctx := context.Background()

	// Not a repository at all.
	config, git := newGitTestVault(t)
	const relative = "global/general/no-repo.md"
	writeGitTestFile(t, config.VaultPath, relative, "# No repo\n")
	diff, err := git.DocumentDiff(ctx, relative)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Available || !strings.Contains(diff.Reason, "不是 git 仓库") || diff.Lines == nil {
		t.Fatalf("not-a-repository diff: %+v", diff)
	}

	// Repository without any commit.
	empty := t.TempDir()
	testGitCommand(t, empty, "init", "-b", "main")
	writeGitTestFile(t, empty, "note.md", "# Note\n")
	diff, err = (&Git{vaultPath: empty, timeout: gitCommandTimeout}).DocumentDiff(ctx, "note.md")
	if err != nil {
		t.Fatal(err)
	}
	if diff.Available || !strings.Contains(diff.Reason, "没有任何提交") {
		t.Fatalf("no-commit diff: %+v", diff)
	}

	// A file that was never added to git.
	config, git = newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/untracked.md", "# Untracked\n")
	diff, err = git.DocumentDiff(ctx, "global/general/untracked.md")
	if err != nil {
		t.Fatal(err)
	}
	if diff.Available || !strings.Contains(diff.Reason, "尚未纳入 git") {
		t.Fatalf("untracked diff: %+v", diff)
	}

	// The file's only version is the repository's root commit, so it has no
	// parent to compare against.
	root := t.TempDir()
	testGitCommand(t, root, "init", "-b", "main")
	writeGitTestFile(t, root, "root.md", "# Root\n")
	testGitCommand(t, root, "add", "-A")
	testGitCommand(t, root, "commit", "-m", "root")
	diff, err = (&Git{vaultPath: root, timeout: gitCommandTimeout}).DocumentDiff(ctx, "root.md")
	if err != nil {
		t.Fatal(err)
	}
	if diff.Available || !strings.Contains(diff.Reason, "只有一次提交") {
		t.Fatalf("root-commit diff: %+v", diff)
	}
}

func TestDocumentDiffRejectsPathsOutsideTheVault(t *testing.T) {
	_, git := newGitTestVault(t)
	for _, relative := range []string{"../escape.md", "/etc/passwd", "global/../escape.md", ""} {
		if _, err := git.DocumentDiff(context.Background(), relative); !errors.Is(err, ErrDocumentNotFound) {
			t.Fatalf("path %q error = %v, want ErrDocumentNotFound", relative, err)
		}
	}
}

func TestDocumentDiffTruncatesLargeChanges(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	const relative = "global/general/large.md"
	var body strings.Builder
	for index := 0; index < diffMaxLines+80; index++ {
		body.WriteString("line ")
		body.WriteString(strings.Repeat("x", index%7))
		body.WriteString("\n")
	}
	writeGitTestFile(t, config.VaultPath, relative, body.String())
	if _, err := git.Commit(ctx, "add large note"); err != nil {
		t.Fatal(err)
	}
	diff, err := git.DocumentDiff(ctx, relative)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Available || !diff.Truncated {
		t.Fatalf("large diff should be truncated: available=%v truncated=%v lines=%d", diff.Available, diff.Truncated, len(diff.Lines))
	}
	if len(diff.Lines) > diffMaxLines {
		t.Fatalf("large diff returned %d lines, limit is %d", len(diff.Lines), diffMaxLines)
	}
}