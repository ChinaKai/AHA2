package knowledgevault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// recentGitFixture exports the standard seed vault, commits it as the root
// commit, and opens the same read-only reader the HTTP server uses. Every seed
// file therefore has no viewable content delta until a test changes one.
func recentGitFixture(t *testing.T) (Config, *Reader, context.Context) {
	t.Helper()
	config, index, ctx := exportSeedVault(t)
	// Rebuild before committing so the regenerated navigation pages are part
	// of the root commit; otherwise the clean worktree would look modified.
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	testGitCommand(t, config.VaultPath, "init", "-b", "main")
	testGitCommand(t, config.VaultPath, "config", "user.name", "Git Test")
	testGitCommand(t, config.VaultPath, "config", "user.email", "git-test@example.com")
	testGitCommand(t, config.VaultPath, "add", "-A")
	testGitCommand(t, config.VaultPath, "commit", "-m", "seed")
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReader(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return config, reader, ctx
}

func recentItemByPath(items []RecentDocument, relative string) (RecentDocument, bool) {
	for _, item := range items {
		if item.Path == relative {
			return item, true
		}
	}
	return RecentDocument{}, false
}

const recentModuleA = "projects/demo-project/navigation-group/module-a.md"
const recentModuleB = "projects/demo-project/navigation-group/module-b.md"

func TestRecentHidesTimestampOnlyRootCommitFiles(t *testing.T) {
	config, reader, ctx := recentGitFixture(t)
	target := filepath.Join(config.VaultPath, filepath.FromSlash(recentModuleA))
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(target, future, future); err != nil {
		t.Fatal(err)
	}

	recent, err := reader.Recent(ctx, "", 120, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent.Items) != 0 {
		t.Fatalf("mtime-only vault produced items: %+v", recent.Items)
	}

	all, err := reader.Recent(ctx, "", 120, true)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := recentItemByPath(all.Items, recentModuleA)
	if !ok {
		t.Fatalf("include_unchanged lost %s: %+v", recentModuleA, all.Items)
	}
	if item.ContentChanged || item.ChangeKind != RecentChangeNone {
		t.Fatalf("root commit item = %+v, want none", item)
	}
	if item.LastCommitAt == "" {
		t.Fatalf("root commit item has no last_commit_at: %+v", item)
	}
	if len(all.Items) == 0 || all.Items[0].Path != recentModuleA {
		t.Fatalf("include_unchanged did not sort by mtime: %+v", all.Items)
	}
}

func TestRecentShowsUncommittedContentChange(t *testing.T) {
	config, reader, ctx := recentGitFixture(t)
	target := filepath.Join(config.VaultPath, filepath.FromSlash(recentModuleB))
	if err := os.WriteFile(target, []byte("---\nid: module-b\ntype: decision\nstatus: verified\ntitle: 模块 B 决策\n---\n# 模块 B\n\n改过的正文。\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	recent, err := reader.Recent(ctx, "", 120, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent.Items) != 1 || recent.Items[0].Path != recentModuleB {
		t.Fatalf("uncommitted recent = %+v", recent.Items)
	}
	item := recent.Items[0]
	if !item.ContentChanged || item.ChangeKind != RecentChangeUncommitted {
		t.Fatalf("uncommitted item = %+v", item)
	}
	if item.ChangedAt == "" || item.ChangedAt != item.ModifiedAt {
		t.Fatalf("uncommitted changed_at = %q, modified_at = %q", item.ChangedAt, item.ModifiedAt)
	}
}

func TestRecentShowsCommittedChangeAndIgnoresLaterTouch(t *testing.T) {
	config, reader, ctx := recentGitFixture(t)
	target := filepath.Join(config.VaultPath, filepath.FromSlash(recentModuleA))
	if err := os.WriteFile(target, []byte("---\nid: module-a\ntype: reference\nstatus: verified\ntitle: 模块 A\n---\n# 模块 A\n\n提交过的正文。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGitCommand(t, config.VaultPath, "commit", "-am", "change module a")

	// A later timestamp-only touch must not turn an unchanged file into a
	// change row, nor hide the committed one.
	touched := filepath.Join(config.VaultPath, filepath.FromSlash(recentModuleB))
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(touched, future, future); err != nil {
		t.Fatal(err)
	}

	recent, err := reader.Recent(ctx, "", 120, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent.Items) != 1 || recent.Items[0].Path != recentModuleA {
		t.Fatalf("committed recent = %+v", recent.Items)
	}
	item := recent.Items[0]
	if !item.ContentChanged || item.ChangeKind != RecentChangeCommitted {
		t.Fatalf("committed item = %+v", item)
	}
	if item.LastCommitAt == "" || item.ChangedAt != item.LastCommitAt {
		t.Fatalf("committed changed_at = %q, last_commit_at = %q", item.ChangedAt, item.LastCommitAt)
	}

	all, err := reader.Recent(ctx, "", 120, true)
	if err != nil {
		t.Fatal(err)
	}
	touchedItem, ok := recentItemByPath(all.Items, recentModuleB)
	if !ok || touchedItem.ContentChanged || touchedItem.ChangeKind != RecentChangeNone {
		t.Fatalf("touched include_unchanged item = %+v", touchedItem)
	}
}

func TestRecentContentChangesSpecialCases(t *testing.T) {
	config, _, ctx := recentGitFixture(t)

	untracked := filepath.Join(config.VaultPath, "global", "general", "untracked.md")
	if err := os.MkdirAll(filepath.Dir(untracked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(untracked, []byte("# 未跟踪\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := &Reader{config: config}
	changes := reader.recentContentChanges(ctx, []recentCandidate{
		{path: "global/general/untracked.md", exists: true},
		{path: recentModuleA, exists: false},
	})
	if change := changes["global/general/untracked.md"]; change.kind != RecentChangeUncommitted || !change.contentChanged() {
		t.Fatalf("untracked change = %+v", change)
	}
	if change := changes[recentModuleA]; change.kind != RecentChangeUncommitted || !change.contentChanged() {
		t.Fatalf("deleted change = %+v", change)
	}

	// A mode-only status entry keeps the same blob, so it is not a content
	// change even though git reports the path as modified.
	if err := os.Chmod(filepath.Join(config.VaultPath, filepath.FromSlash(recentModuleB)), 0o755); err != nil {
		t.Fatal(err)
	}
	modeChanges := reader.recentContentChanges(ctx, []recentCandidate{{path: recentModuleB, exists: true}})
	if change := modeChanges[recentModuleB]; change.kind != RecentChangeNone || change.contentChanged() {
		t.Fatalf("mode-only change = %+v", change)
	}

	nonGit := &Reader{config: DefaultConfig(t.TempDir())}
	nonGitChanges := nonGit.recentContentChanges(ctx, []recentCandidate{{path: "a.md"}})
	if change := nonGitChanges["a.md"]; change.kind != RecentChangeUnknown || !change.contentChanged() {
		t.Fatalf("non-git change = %+v", change)
	}
}
