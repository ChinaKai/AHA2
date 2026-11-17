package knowledgevault

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Root knowledge libraries are plain top-level directories of the vault. They
// are managed by the filesystem, own no projects row, and are indexed with
// scope "root" and an empty project_id. The reserved vault containers and the
// feature folders are not libraries.
var rootLibraryReservedNames = map[string]bool{
	"global": true, "projects": true, "archive": true, "orphans": true,
	"skills": true, "inbox": true,
}

// RootLibraryDirectories lists the top-level root library directory names in
// stable order. Hidden directories, symlinks and reserved containers are
// skipped.
func RootLibraryDirectories(vaultPath string) []string {
	vaultPath = strings.TrimSpace(vaultPath)
	if vaultPath == "" {
		return nil
	}
	entries, err := os.ReadDir(vaultPath)
	if err != nil {
		return nil
	}
	result := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !rootLibraryNameAllowed(name) {
			continue
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func rootLibraryNameAllowed(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return false
	}
	return !rootLibraryReservedNames[strings.ToLower(name)]
}

func isRootLibraryDirectory(vaultPath, name string) bool {
	if !rootLibraryNameAllowed(name) {
		return false
	}
	info, err := os.Lstat(filepath.Join(vaultPath, name))
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

// rootLibraryPath reports whether a vault-relative path belongs to a root
// library. A root library is exactly one top-level directory, so the path must
// have at least two segments (the directory and its content).
func rootLibraryPath(vaultPath, relative string) (string, bool) {
	segments := strings.Split(filepath.ToSlash(relative), "/")
	if len(segments) < 2 || segments[0] == "" {
		return "", false
	}
	if !isRootLibraryDirectory(vaultPath, segments[0]) {
		return "", false
	}
	return segments[0], true
}

// RootLibraryIndexRelative returns the navigation page path of one root
// library. The page is generated on disk but deliberately stays out of the
// index and search, like the projects/ and archive/ container pages.
func RootLibraryIndexRelative(directory string) string {
	return path.Join(directory, "index.md")
}

func rootLibraryTitle(vaultPath, directory string) string {
	raw, err := os.ReadFile(filepath.Join(vaultPath, filepath.FromSlash(RootLibraryIndexRelative(directory))))
	if err == nil {
		document := ParseDocument(string(raw))
		if title := strings.TrimSpace(document.Frontmatter.Title); title != "" {
			return title
		}
		if title := FirstHeading(document.Body); title != "" {
			return title
		}
	}
	return humanizeSegment(directory)
}
