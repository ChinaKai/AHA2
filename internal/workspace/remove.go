package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// SupportsLocalRemoval reports whether this process can delete files inside
// the workspace directly. WSL and SSH workspaces live on another host, so a
// fallback delete must never run locally for them.
func SupportsLocalRemoval(item domain.Workspace) bool {
	if item.Locality == "remote" || item.Transport == "ssh" || item.Transport == "wsl" {
		return false
	}
	return true
}

// LocalLongPath returns the extended-length form of a local path on Windows
// (\\?\C:\... or \\?\UNC\server\share\...), and the path unchanged on other
// platforms. Extended-length paths bypass the legacy MAX_PATH limit.
func LocalLongPath(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("empty path")
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("path is not absolute: %q", value)
	}
	if runtime.GOOS != "windows" {
		return filepath.Clean(trimmed), nil
	}
	return windowsExtendedPath(filepath.Clean(trimmed))
}

// RemoveAllLongPath removes value and everything under it. On Windows it uses
// the extended-length path form so nested files can still be deleted when the
// full paths exceed MAX_PATH (260 characters). Callers must perform the
// worktree safety checks (clean status, merged HEAD, unlocked, no submodules)
// before calling this; it deliberately has no notion of git state.
func RemoveAllLongPath(value string) error {
	target, err := LocalLongPath(value)
	if err != nil {
		return fmt.Errorf("remove %q: %w", value, err)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove %q: %w", value, err)
	}
	return nil
}

// windowsExtendedPath is the host-independent conversion behind
// LocalLongPath. Keeping the Windows path rules out of filesystem calls makes
// them testable on any platform. Extended-length paths are not normalized by
// Windows, so "." and ".." segments are resolved (or rejected) here.
func windowsExtendedPath(value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", fmt.Errorf("empty path")
	}
	unified := strings.ReplaceAll(raw, "/", `\`)
	if strings.HasPrefix(unified, `\\?\`) || strings.HasPrefix(unified, `\\.\`) {
		return unified, nil
	}
	if strings.HasPrefix(unified, `\\`) {
		return windowsExtendedUNC(unified, raw)
	}
	if len(unified) < 3 || unified[1] != ':' || !isASCIILetter(unified[0]) || unified[2] != '\\' {
		return "", fmt.Errorf("not an absolute Windows path: %q", raw)
	}
	drive := strings.ToUpper(unified[:1])
	return `\\?\` + drive + `:\` + cleanWindowsSegments(unified[3:]), nil
}

func windowsExtendedUNC(value, raw string) (string, error) {
	parts := strings.Split(strings.TrimPrefix(value, `\\`), `\`)
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("invalid UNC path %q", raw)
		default:
			clean = append(clean, part)
		}
	}
	if len(clean) < 2 {
		return "", fmt.Errorf("UNC path lacks a share name: %q", raw)
	}
	return `\\?\UNC\` + strings.Join(clean, `\`), nil
}

func cleanWindowsSegments(rest string) string {
	parts := strings.Split(rest, `\`)
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(clean) > 0 {
				clean = clean[:len(clean)-1]
			}
		default:
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, `\`)
}

func isASCIILetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}
