package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

type viewerGitlink struct {
	Old   string `json:"old,omitempty"`
	New   string `json:"new,omitempty"`
	Index string `json:"index,omitempty"`
}

func viewerGitlinkOID(mode, id string) string {
	if mode != "160000" || !validViewerSHA(id) || strings.Trim(id, "0") == "" {
		return ""
	}
	return id
}

func viewerRawGitlink(header, rel string) *viewerGitlink {
	if !validViewerPath(rel, false) {
		return nil
	}
	fields := strings.Fields(header)
	if len(fields) != 5 {
		return nil
	}
	old := viewerGitlinkOID(strings.TrimPrefix(fields[0], ":"), fields[2])
	next := viewerGitlinkOID(fields[1], fields[3])
	if old == "" && next == "" {
		return nil
	}
	return &viewerGitlink{Old: old, New: next}
}

func viewerIndexGitlink(record string) (rel, id string) {
	header, rel, ok := strings.Cut(record, "\t")
	if !ok {
		return "", ""
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[2] != "0" {
		return "", ""
	}
	return rel, viewerGitlinkOID(fields[0], fields[1])
}

func (git viewerGit) stagedRaw(ctx context.Context, limit int, paths ...string) (viewerGitOutput, error) {
	return git.checked(ctx, limit, append([]string{"diff", "--cached", "--raw", "-z", "--no-abbrev",
		"--no-renames", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--submodule=short"}, paths...)...)
}

func (repos viewerRepositories) checkedOutHeads(repo string) map[string]string {
	heads := map[string]string{}
	for _, item := range repos.Items {
		rel := item.Path
		if repo != "" {
			var ok bool
			rel, ok = strings.CutPrefix(rel, repo+"/")
			if !ok {
				continue
			}
		}
		if rel != "" && item.Readable && validViewerSHA(item.Head) {
			heads[rel] = item.Head
		}
	}
	return heads
}

func viewerWorktreeGitlink(staged *viewerGitlink, index, checkout string) *viewerGitlink {
	if index == "" || checkout == "" || checkout == index {
		return staged
	}
	if staged == nil {
		return &viewerGitlink{Old: index, New: checkout}
	}
	return &viewerGitlink{Old: staged.Old, Index: index, New: checkout}
}

func (state *viewerState) readUncommittedGitlinks(ctx context.Context, git viewerGit, staged viewerGitOutput, heads map[string]string) error {
	changes := map[string]viewerChange{}
	for i, records := 0, staged.records(); i+1 < len(records); i += 2 {
		header, rel := records[i], records[i+1]
		if link := viewerRawGitlink(header, rel); link != nil {
			change := viewerChange{Path: rel, Masked: viewerSensitivePath(rel)}
			if !change.Masked {
				change.Gitlink = link
			}
			changes[rel] = change
		}
	}
	if len(heads) > 0 {
		// Use the checked-out HEADs from validated discovery. Git worktree
		// diff and status keep ignoring submodules so they cannot follow an
		// unchecked child Git directory or read child worktree files.
		index, err := git.checked(ctx, viewerGitlinkLimit, "ls-files", "--stage", "-z", "--")
		if err != nil {
			return err
		}
		state.Truncated = state.Truncated || index.truncated
		for _, record := range index.records() {
			rel, oid := viewerIndexGitlink(record)
			if checkout := heads[rel]; oid != "" && checkout != "" && checkout != oid {
				changes[rel] = viewerChange{Path: rel, Gitlink: viewerWorktreeGitlink(changes[rel].Gitlink, oid, checkout)}
			}
		}
	}
	if len(changes) == 0 {
		return nil
	}
	for i, change := range state.Uncommitted {
		if replacement, ok := changes[change.Path]; ok {
			replacement.Status = change.Status
			state.Uncommitted[i] = replacement
			delete(changes, change.Path)
		}
	}
	paths := make([]string, 0, len(changes))
	for rel := range changes {
		paths = append(paths, rel)
	}
	slices.Sort(paths)
	for _, rel := range paths {
		if len(state.Uncommitted) == viewerListLimit {
			state.Truncated = true
			break
		}
		state.Uncommitted = append(state.Uncommitted, changes[rel])
	}
	return nil
}

func (link viewerGitlink) preview() string {
	var text strings.Builder
	write := func(old, next string) {
		fmt.Fprintf(&text, "@@ -1,%d +1,%d @@\n", viewerGitlinkLineCount(old), viewerGitlinkLineCount(next))
		if old != "" {
			fmt.Fprintf(&text, "-Subproject commit %s\n", old)
		}
		if next != "" {
			fmt.Fprintf(&text, "+Subproject commit %s\n", next)
		}
	}
	if link.Index == "" {
		write(link.Old, link.New)
	} else {
		text.WriteString("Index\n")
		write(link.Old, link.Index)
		text.WriteString("Worktree\n")
		write(link.Index, link.New)
	}
	return text.String()
}

func viewerGitlinkLineCount(oid string) int {
	if oid == "" {
		return 0
	}
	return 1
}
