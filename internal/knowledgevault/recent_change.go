package knowledgevault

import (
	"context"
	"strings"
	"time"
)

// Change kinds reported by Reader.Recent. "content_changed" is false only for
// none: the working tree is byte-identical to HEAD and the last commit that
// touched the path did not change its blob either, so the diff preview would
// have nothing to show. Everything we cannot prove unchanged stays unknown and
// remains visible by default instead of being dropped silently.
const (
	RecentChangeUncommitted = "uncommitted"
	RecentChangeCommitted   = "committed"
	RecentChangeNone        = "none"
	RecentChangeUnknown     = "unknown"
)

// recentCandidate is one indexed document as seen by the change detector.
type recentCandidate struct {
	path       string
	modifiedAt time.Time
	updatedAt  time.Time
	exists     bool
}

// fallbackTime is the timestamp Recent used before content detection existed:
// the file mtime, then the frontmatter updated_at.
func (c recentCandidate) fallbackTime() time.Time {
	if !c.modifiedAt.IsZero() {
		return c.modifiedAt
	}
	return c.updatedAt
}

// recentContentChange is the per-path verdict. changedAt is the timestamp the
// feed sorts on: mtime for uncommitted/none/unknown rows, the commit time for
// committed rows.
type recentContentChange struct {
	kind          string
	changedAt     time.Time
	lastCommit    time.Time
	hasLastCommit bool
}

func (c recentContentChange) contentChanged() bool { return c.kind != RecentChangeNone }

// recentCommitTouch is the newest commit that changed the path's tree entry.
type recentCommitTouch struct {
	committedAt    time.Time
	hasParent      bool
	contentChanged bool
}

// recentContentChanges classifies every candidate with a bounded number of git
// processes: one status, one ls-tree, one hash-object batch and one log scan.
// Any failure degrades that path to unknown rather than reporting "unchanged".
func (r *Reader) recentContentChanges(ctx context.Context, candidates []recentCandidate) map[string]recentContentChange {
	result := make(map[string]recentContentChange, len(candidates))
	for _, candidate := range candidates {
		result[candidate.path] = recentContentChange{kind: RecentChangeUnknown, changedAt: candidate.fallbackTime()}
	}
	if len(candidates) == 0 {
		return result
	}
	git := &Git{vaultPath: r.config.VaultPath, timeout: gitCommandTimeout}
	if _, err := git.gitBinary(); err != nil {
		return result
	}
	if !git.isRepository(ctx) || !git.hasCommit(ctx) {
		return result
	}
	headBlobs, headOK := git.recentHeadBlobs(ctx)
	if !headOK {
		return result
	}
	status, _ := git.recentPorcelainStatus(ctx)

	hashTargets := make([]recentCandidate, 0, len(candidates))
	trackedPaths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, tracked := headBlobs[candidate.path]; !tracked {
			continue
		}
		trackedPaths = append(trackedPaths, candidate.path)
		// --stdin-paths reads one path per line; a literal newline in the name
		// cannot be represented, so those files stay unknown.
		if candidate.exists && !strings.Contains(candidate.path, "\n") {
			hashTargets = append(hashTargets, candidate)
		}
	}
	worktreeHashes, hashesOK := git.recentWorktreeHashes(ctx, hashTargets)
	lastCommits := map[string]recentCommitTouch{}
	commitsOK := true
	if len(trackedPaths) > 0 {
		lastCommits, commitsOK = git.recentLastCommits(ctx)
	}

	for _, candidate := range candidates {
		change := recentContentChange{kind: RecentChangeUnknown, changedAt: candidate.fallbackTime()}
		headSHA, tracked := headBlobs[candidate.path]
		code, inStatus := status[candidate.path]
		hash, hashed := worktreeHashes[candidate.path]
		commit, sawCommit := lastCommits[candidate.path]
		if sawCommit {
			change.lastCommit = commit.committedAt
			change.hasLastCommit = true
		}
		switch {
		case !candidate.exists && tracked:
			// The indexed document was deleted from the worktree.
			change.kind = RecentChangeUncommitted
		case inStatus && code == "??":
			// A new file has no content baseline, but its content is new.
			change.kind = RecentChangeUncommitted
		case inStatus && !tracked:
			// Staged addition: HEAD has no blob to compare with.
			change.kind = RecentChangeUncommitted
		case inStatus && tracked:
			// Status can report a mode-only change. The blob comparison decides
			// whether there is an actual content diff to show.
			switch {
			case hashed && hash == headSHA:
				change.kind = RecentChangeNone
			case hashed:
				change.kind = RecentChangeUncommitted
			}
		case !candidate.exists:
			// Missing and untracked: nothing can be compared.
		case !tracked:
			// Not in HEAD and not reported by status: ignored or otherwise
			// untracked. Treat it as unknown instead of claiming a change.
		case !hashesOK || !hashed:
			// The hash batch could not compare this path.
			change.kind = RecentChangeUnknown
		case hash != headSHA:
			change.kind = RecentChangeUncommitted
		case !commitsOK || !sawCommit:
			// Clean content but no commit record (shallow clone, merge-only
			// change, log failure). Unknown keeps it visible.
			change.kind = RecentChangeUnknown
		case !commit.hasParent:
			// Root commit: the file was introduced, but a diff has no baseline
			// to compare against, so the change view cannot render a delta.
			change.kind = RecentChangeNone
		case commit.contentChanged:
			change.kind = RecentChangeCommitted
			change.changedAt = commit.committedAt
		default:
			// The newest commit only touched mode/rename, so HEAD has no
			// viewable content delta for this path.
			change.kind = RecentChangeNone
		}
		result[candidate.path] = change
	}
	return result
}

// recentPorcelainStatus returns path -> two-letter porcelain code. One call
// covers staged, unstaged and untracked files.
func (g *Git) recentPorcelainStatus(ctx context.Context) (map[string]string, bool) {
	output, err := g.commandRaw(ctx, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return map[string]string{}, false
	}
	result := map[string]string{}
	fields := strings.Split(output, "\x00")
	for index := 0; index < len(fields); index++ {
		record := fields[index]
		if len(record) < 3 {
			continue
		}
		code := record[:2]
		result[record[3:]] = code
		if code[0] == 'R' || code[0] == 'C' || code[1] == 'R' || code[1] == 'C' {
			// Rename/copy records carry the original path as a second field.
			index++
		}
	}
	return result, true
}

// recentHeadBlobs returns the HEAD-tree blob id for every tracked path.
func (g *Git) recentHeadBlobs(ctx context.Context) (map[string]string, bool) {
	output, err := g.commandRaw(ctx, "ls-tree", "-r", "-z", "HEAD")
	if err != nil {
		return map[string]string{}, false
	}
	result := map[string]string{}
	for _, record := range strings.Split(output, "\x00") {
		tree, relative, ok := strings.Cut(record, "\t")
		if !ok {
			continue
		}
		meta := strings.Fields(tree)
		if len(meta) < 3 || meta[1] != "blob" {
			continue
		}
		result[relative] = meta[2]
	}
	return result, true
}

// recentWorktreeHashes hashes every existing tracked file in one git process.
// Paths are vault-relative and were copied from the read-only index.
func (g *Git) recentWorktreeHashes(ctx context.Context, candidates []recentCandidate) (map[string]string, bool) {
	result := make(map[string]string, len(candidates))
	if len(candidates) == 0 {
		return result, true
	}
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.path)
	}
	output, err := g.commandInputRaw(ctx, "hash-object", strings.Join(paths, "\n")+"\n", "--stdin-paths")
	if err != nil {
		return result, false
	}
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) != len(paths) {
		return map[string]string{}, false
	}
	for index, path := range paths {
		hash := strings.TrimSpace(lines[index])
		if !diffHashPattern.MatchString(hash) {
			return map[string]string{}, false
		}
		result[path] = hash
	}
	return result, true
}

// recentLastCommits scans the commit history once and keeps the newest record
// that touched each path. It replaces one `git log -1 -- <path>` per document.
// The explicit %x1e prefix matters: -z suppresses the default record separator,
// so without it every commit would fold into one block and inherit the newest
// commit's parent state.
func (g *Git) recentLastCommits(ctx context.Context) (map[string]recentCommitTouch, bool) {
	output, err := g.commandRaw(ctx, "log", "--format=%x1e%H%x1f%cI%x1f%P", "--raw", "--no-abbrev", "-z", "--no-color")
	if err != nil {
		return map[string]recentCommitTouch{}, false
	}
	result := map[string]recentCommitTouch{}
	// -z keeps non-ASCII paths literal; each commit block starts with the
	// record separator and the format fields are unit-separated.
	for _, block := range strings.Split(output, "\x1e") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		fields := strings.Split(block, "\x00")
		header := strings.Split(strings.TrimSpace(fields[0]), "\x1f")
		if len(header) < 3 {
			continue
		}
		committedAt, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(header[1]))
		if parseErr != nil {
			continue
		}
		hasParent := strings.TrimSpace(header[2]) != ""
		for index := 1; index+1 < len(fields); {
			meta := strings.TrimLeft(fields[index], "\n")
			if !strings.HasPrefix(meta, ":") {
				index++
				continue
			}
			tokens := strings.Fields(meta)
			if len(tokens) < 5 || tokens[4] == "" {
				break
			}
			status := tokens[4]
			oldSHA, newSHA := tokens[2], tokens[3]
			path := fields[index+1]
			next := index + 2
			if status[0] == 'R' || status[0] == 'C' {
				if index+2 >= len(fields) {
					break
				}
				// Raw rename records list the source first, then the target.
				path = fields[index+2]
				next = index + 3
			}
			if _, seen := result[path]; !seen {
				result[path] = recentCommitTouch{
					committedAt: committedAt, hasParent: hasParent,
					contentChanged: rawRecordChangedContent(status[0], oldSHA, newSHA),
				}
			}
			index = next
		}
	}
	return result, true
}

// rawRecordChangedContent reports whether a raw diff record carries a viewable
// content delta. Deletions never describe a path that still exists in HEAD.
func rawRecordChangedContent(status byte, oldSHA, newSHA string) bool {
	if isZeroHash(newSHA) {
		return false
	}
	if status == 'A' {
		return true
	}
	return oldSHA != newSHA
}

func isZeroHash(value string) bool {
	return value != "" && strings.Trim(value, "0") == ""
}
