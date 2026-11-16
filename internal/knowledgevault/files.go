package knowledgevault

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// maxVaultPreviewSize bounds one Web preview. Larger files are offered as a
// download instead of being loaded into JSON.
const maxVaultPreviewSize = 2 << 20

// ErrFileTooLarge reports a file that should be downloaded instead of
// previewed.
var ErrFileTooLarge = errors.New("knowledge vault file is too large to preview")

// ErrBinaryFile reports a non-text file. The Web UI offers a download.
var ErrBinaryFile = errors.New("knowledge vault file is binary")

// VaultFileEntry is one real directory entry in the vault tree. The Web
// directory reads the filesystem, not the index, so a file that has not been
// indexed yet is still visible and readable.
type VaultFileEntry struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	AbsolutePath string `json:"absolute_path"`
	IsDir        bool   `json:"is_dir"`
	Size         int64  `json:"size"`
	Modified     string `json:"modified,omitempty"`
	Kind         string `json:"kind"`
	Hidden       bool   `json:"hidden,omitempty"`
}

// VaultFileList is one directory listing.
type VaultFileList struct {
	Path    string           `json:"path"`
	Entries []VaultFileEntry `json:"entries"`
	Count   int              `json:"count"`
	Warning string           `json:"warning,omitempty"`
}

// VaultFile is one text file preview.
type VaultFile struct {
	Path         string `json:"path"`
	AbsolutePath string `json:"absolute_path"`
	Name         string `json:"name"`
	Title        string `json:"title,omitempty"`
	Kind         string `json:"kind"`
	Content      string `json:"content"`
	Size         int64  `json:"size"`
	ModifiedAt   string `json:"modified_at,omitempty"`
}

func vaultFileKind(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown":
		return "markdown"
	case ".txt", ".log", ".csv", ".yaml", ".yml", ".json", ".toml", ".ini", ".sh", ".py", ".js", ".ts", ".mjs", ".css", ".html":
		return "text"
	default:
		return "binary"
	}
}

// resolveVaultEntry maps a vault-relative path to an absolute path and rejects
// escapes and symlinks. The root path ("") resolves to the vault itself.
func (r *Reader) resolveVaultEntry(relative string) (string, string, os.FileInfo, error) {
	relative = strings.TrimSpace(strings.ReplaceAll(relative, "\\", "/"))
	relative = strings.TrimPrefix(relative, "/")
	clean := path.Clean(relative)
	if clean == "." {
		clean = ""
	}
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", "", nil, ErrDocumentNotFound
	}
	// Dot paths are hidden from the directory view and are not readable by
	// direct path either: .git, .aha and the manifest are implementation
	// details, not knowledge.
	for _, segment := range strings.Split(clean, "/") {
		if vaultEntryHidden(segment) {
			return "", "", nil, ErrDocumentNotFound
		}
	}
	absolute := r.config.VaultPath
	if clean != "" {
		absolute = filepath.Join(r.config.VaultPath, filepath.FromSlash(clean))
	}
	if !IsInsideRoot(r.config.VaultPath, absolute) {
		return "", "", nil, ErrDocumentNotFound
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil, ErrDocumentNotFound
		}
		return "", "", nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", "", nil, ErrDocumentNotFound
	}
	return absolute, clean, info, nil
}

func vaultEntryHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

// Files lists one real directory of the vault. Dot directories and the
// manifest are hidden by construction; symlinks are refused.
func (r *Reader) Files(relative string) (VaultFileList, error) {
	result := VaultFileList{Path: "", Entries: []VaultFileEntry{}}
	if !r.vaultReady {
		return result, ErrReaderNotReady
	}
	absolute, clean, info, err := r.resolveVaultEntry(relative)
	if err != nil {
		return result, err
	}
	if !info.IsDir() {
		return result, ErrDocumentNotFound
	}
	result.Path = clean
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if vaultEntryHidden(name) {
			continue
		}
		full := filepath.Join(absolute, name)
		entryInfo, infoErr := os.Lstat(full)
		if infoErr != nil || entryInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		relativePath := name
		if clean != "" {
			relativePath = path.Join(clean, name)
		}
		item := VaultFileEntry{
			Name: name, Path: relativePath, AbsolutePath: full, IsDir: entryInfo.IsDir(),
			Size: entryInfo.Size(), Kind: "folder",
		}
		if !entryInfo.IsDir() {
			item.Kind = vaultFileKind(name)
		}
		if !entryInfo.ModTime().IsZero() {
			item.Modified = entryInfo.ModTime().UTC().Format(time.RFC3339)
		}
		result.Entries = append(result.Entries, item)
	}
	sort.SliceStable(result.Entries, func(left, right int) bool {
		a, b := result.Entries[left], result.Entries[right]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	result.Count = len(result.Entries)
	return result, nil
}

// File reads one text file directly from disk. Markdown frontmatter is parsed
// only for the title; the returned content stays the raw file so external edits
// are visible without an index rebuild.
func (r *Reader) File(relative string) (VaultFile, error) {
	if !r.vaultReady {
		return VaultFile{}, ErrReaderNotReady
	}
	absolute, clean, info, err := r.resolveVaultEntry(relative)
	if err != nil {
		return VaultFile{}, err
	}
	if info.IsDir() {
		return VaultFile{}, ErrDocumentNotFound
	}
	if !info.Mode().IsRegular() {
		return VaultFile{}, ErrDocumentNotFound
	}
	kind := vaultFileKind(clean)
	if kind == "binary" {
		return VaultFile{}, ErrBinaryFile
	}
	if info.Size() > maxVaultPreviewSize {
		return VaultFile{}, ErrFileTooLarge
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return VaultFile{}, err
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return VaultFile{}, ErrBinaryFile
	}
	content := string(data)
	result := VaultFile{
		Path: clean, AbsolutePath: absolute, Name: path.Base(clean), Kind: kind, Content: content, Size: info.Size(),
	}
	if !info.ModTime().IsZero() {
		result.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
	}
	if kind == "markdown" {
		document := ParseDocument(content)
		title := strings.TrimSpace(document.Frontmatter.Title)
		if title == "" {
			title = FirstHeading(document.Body)
		}
		result.Title = title
	}
	return result, nil
}

// FilePath resolves a real file for download/opening. It performs the same
// vault-escape and symlink checks as File.
func (r *Reader) FilePath(relative string) (string, os.FileInfo, error) {
	if !r.vaultReady {
		return "", nil, ErrReaderNotReady
	}
	absolute, _, info, err := r.resolveVaultEntry(relative)
	if err != nil {
		return "", nil, err
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return "", nil, ErrDocumentNotFound
	}
	return absolute, info, nil
}

// VaultPath is the absolute vault root, used by the Web "copy path" action.
func (r *Reader) VaultPath() string {
	if r == nil {
		return ""
	}
	return r.config.VaultPath
}

// CopyPathFor returns the absolute path of one vault entry for the clipboard.
func (r *Reader) CopyPathFor(relative string) (string, error) {
	absolute, _, _, err := r.resolveVaultEntry(relative)
	if err != nil {
		return "", err
	}
	return absolute, nil
}
