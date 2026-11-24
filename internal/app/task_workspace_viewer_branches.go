package app

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	viewerBranchTimeout = 5 * time.Second
	viewerLocalRefs     = "refs/heads/"
	viewerRemoteRefs    = "refs/remotes/"
	viewerRefFormat     = "--format=%(refname)%00%(objectname)%00%(symref)"
)

type viewerBranch struct {
	Ref  string `json:"ref"`
	Name string `json:"name"`
}

func validViewerBranchRef(ref string) bool {
	name, local := strings.CutPrefix(ref, viewerLocalRefs)
	if !local {
		var remote bool
		name, remote = strings.CutPrefix(ref, viewerRemoteRefs)
		if !remote || !strings.Contains(name, "/") {
			return false
		}
	}
	if len(ref) > 4096 || !utf8.ValidString(ref) || strings.Contains(ref, "..") ||
		strings.Contains(ref, "@{") || strings.HasSuffix(ref, ".") ||
		strings.ContainsAny(ref, "~^:?*[\\") {
		return false
	}
	for _, ch := range ref {
		if ch <= ' ' || ch == 0x7f {
			return false
		}
	}
	for part := range strings.SplitSeq(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func viewerBranchName(ref string) string {
	if name, ok := strings.CutPrefix(ref, viewerLocalRefs); ok {
		return name
	}
	return strings.TrimPrefix(ref, viewerRemoteRefs)
}

func readViewerBranchRecord(line string) (ref, id string, ok bool) {
	ref, rest, ok := strings.Cut(line, "\x00")
	id, target, separated := strings.Cut(rest, "\x00")
	return ref, id, ok && separated && target == "" && validViewerBranchRef(ref) && validViewerSHA(id)
}

func (git viewerGit) selectedBranch(ctx context.Context, ref string) (string, error) {
	if !validViewerBranchRef(ref) {
		return "", ErrViewerBranch
	}
	out, err := git.checked(ctx, 8192, "for-each-ref", "--sort=refname", "--count=1", viewerRefFormat+"%00%(objecttype)", "--", ref)
	if err != nil {
		return "", err
	}
	if out.truncated {
		return "", ErrViewerGit
	}
	// Git's ref filter also accepts namespace prefixes. Only an exact,
	// non-symbolic branch match can supply a revision to subsequent commands.
	record, commit := strings.CutSuffix(string(out.data), "\x00commit\n")
	found, id, ok := readViewerBranchRecord(record)
	if !commit || !ok || found != ref {
		return "", ErrViewerBranch
	}
	return id, nil
}

func (git viewerGit) remoteBranches(ctx context.Context) ([]viewerBranch, viewerGitOutput, error) {
	out, err := git.checked(ctx, 512<<10, "for-each-ref", "--sort=refname", "--count=501", viewerRefFormat, viewerRemoteRefs)
	if err != nil {
		return nil, out, err
	}
	options := []viewerBranch{}
	for line := range strings.SplitSeq(string(out.data), "\n") {
		if ref, _, ok := readViewerBranchRecord(line); ok {
			if len(options) == 500 {
				out.truncated = true
				break
			}
			options = append(options, viewerBranch{Ref: ref, Name: viewerBranchName(ref)})
		}
	}
	return options, out, nil
}

func (git viewerGit) checkedOutBranch(ctx context.Context) (string, error) {
	out, err := git.run(ctx, 8192, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return "", err
	}
	if out.exit == 1 && !out.truncated {
		return "", nil // Detached HEAD.
	}
	if out.exit != 0 || out.truncated {
		return "", ErrViewerGit
	}
	return strings.TrimPrefix(strings.TrimSuffix(string(out.data), "\n"), viewerLocalRefs), nil
}
