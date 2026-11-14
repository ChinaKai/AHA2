package knowledgevault

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// Windows device names are invalid as file or directory names on every
// platform this repository ships to. Reserving them here keeps an exported
// vault copy-able to Windows even when the export ran on Linux.
var windowsReservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// Reserved directory names inside a project (or next to the vault root) may
// not be used as a knowledge document or group folder.
var reservedVaultNames = map[string]bool{
	".trash": true, ".aha": true, ".git": true, ".obsidian": true,
	"skills": true, "inbox": true, "index": true,
}

func isWindowsReserved(name string) bool {
	base := strings.TrimSpace(name)
	if index := strings.IndexByte(base, '.'); index >= 0 {
		base = base[:index]
	}
	return windowsReservedNames[strings.ToLower(strings.TrimSpace(base))]
}

// Slugify converts a title or display name into a stable, portable file name
// segment. It deliberately stays ASCII: the vault may be opened from Windows,
// and a deterministic hash fallback keeps non-ASCII titles stable instead of
// silently collapsing them to an empty string.
func Slugify(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '.' || r == '/' || r == '\\':
			if builder.Len() > 0 && !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		default:
			if builder.Len() > 0 && !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
		return "doc-" + hex.EncodeToString(sum[:])[:10]
	}
	if len(result) > 80 {
		sum := sha256.Sum256([]byte(result))
		result = strings.TrimRight(result[:70], "-") + "-" + hex.EncodeToString(sum[:])[:8]
	}
	if isWindowsReserved(result) || reservedVaultNames[result] {
		result = "doc-" + result
	}
	return result
}

// SafeSegment turns arbitrary text into a single path segment. Unlike
// Slugify it preserves non-ASCII letters so imported human titles remain
// recognizable; callers that want canonical slugs should use Slugify.
func SafeSegment(value, fallback string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == '<' || r == '>' || r == ':' || r == '"' || r == '|' || r == '?' || r == '*':
			return '-'
		case r < 0x20 || r == 0x7f:
			return '-'
		default:
			return r
		}
	}, value)
	value = strings.Trim(value, " .")
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || value == "." || value == ".." {
		value = strings.TrimSpace(fallback)
	}
	if value == "" || value == "." || value == ".." {
		value = "untitled"
	}
	if isWindowsReserved(value) || reservedVaultNames[strings.ToLower(value)] {
		value = "_" + value
	}
	if len([]rune(value)) > 100 {
		sum := sha256.Sum256([]byte(value))
		runes := []rune(value)
		value = strings.TrimRight(string(runes[:90]), " .") + "-" + hex.EncodeToString(sum[:])[:8]
	}
	return value
}

// ValidateRelativePath rejects anything that could escape the vault root or
// produce a path Windows cannot represent.
func ValidateRelativePath(relative string) error {
	if relative == "" {
		return fmt.Errorf("path is empty")
	}
	if strings.ContainsRune(relative, 0) {
		return fmt.Errorf("path contains NUL")
	}
	if strings.ContainsRune(relative, '\\') {
		return fmt.Errorf("backslashes are not allowed in vault paths: %s", relative)
	}
	relative = filepath.ToSlash(relative)
	if strings.HasPrefix(relative, "/") || filepath.IsAbs(relative) {
		return fmt.Errorf("absolute paths are not allowed: %s", relative)
	}
	if len(relative) >= 2 && relative[1] == ':' {
		return fmt.Errorf("drive-letter paths are not allowed: %s", relative)
	}
	for _, segment := range strings.Split(relative, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("invalid path segment %q in %s", segment, relative)
		}
		if strings.ContainsAny(segment, `<>:"|?*`) {
			return fmt.Errorf("invalid character in path segment %q", segment)
		}
		if strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
			return fmt.Errorf("path segment may not end with a dot or space: %q", segment)
		}
		if isWindowsReserved(segment) {
			return fmt.Errorf("Windows reserved name in path segment %q", segment)
		}
	}
	return nil
}

// PathGuard allocates paths inside one vault root. It rejects traversal,
// tracks case-insensitive collisions, and gives every colliding identity a
// deterministic suffix derived from its stable identity.
type PathGuard struct {
	root     string
	occupied map[string]string
}

func NewPathGuard(root string) *PathGuard {
	return &PathGuard{root: filepath.Clean(root), occupied: map[string]string{}}
}

// Reserved registers a path that knowledge files may not take.
func (g *PathGuard) Reserved(relative, identity string) {
	key := strings.ToLower(filepath.ToSlash(filepath.Clean(relative)))
	if key != "" {
		g.occupied[key] = identity
	}
}

// Allocate returns a safe relative path. When a case-insensitive collision is
// found, it appends a short identity hash before the extension; repeated calls
// with the same identity return the same path.
func (g *PathGuard) Allocate(relative, identity string) (string, bool, error) {
	relative = filepath.ToSlash(filepath.Clean(relative))
	if err := ValidateRelativePath(relative); err != nil {
		return "", false, err
	}
	key := strings.ToLower(relative)
	if existing, ok := g.occupied[key]; ok {
		if existing == identity {
			return relative, false, nil
		}
		return g.collisionVariant(relative, identity)
	}
	if err := g.ensureInside(relative); err != nil {
		return "", false, err
	}
	g.occupied[key] = identity
	return relative, false, nil
}

func (g *PathGuard) collisionVariant(relative, identity string) (string, bool, error) {
	dir, name := filepath.Split(relative)
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	sum := sha256.Sum256([]byte(identity))
	suffix := hex.EncodeToString(sum[:])[:8]
	for attempt := 0; attempt < 100; attempt++ {
		candidateName := stem + "-" + suffix
		if attempt > 0 {
			candidateName = fmt.Sprintf("%s-%s-%d", stem, suffix, attempt+1)
		}
		candidate := filepath.ToSlash(filepath.Join(dir, candidateName+extension))
		key := strings.ToLower(candidate)
		if existing, ok := g.occupied[key]; ok {
			if existing == identity {
				return candidate, false, nil
			}
			continue
		}
		if err := g.ensureInside(candidate); err != nil {
			return "", false, err
		}
		g.occupied[key] = identity
		return candidate, true, nil
	}
	return "", false, fmt.Errorf("could not resolve path collision for %s", relative)
}

func (g *PathGuard) ensureInside(relative string) error {
	absolute := filepath.Join(g.root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(g.root, absolute)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path escapes vault root: %s", relative)
	}
	return nil
}

// IsInsideRoot reports whether absolute is contained by root after resolving
// symlinks. It is used when scanning an existing vault so a symlinked folder
// cannot pull files from outside into the index.
func IsInsideRoot(root, absolute string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	realPath, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil {
		return false
	}
	return rel == "." || (!filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
