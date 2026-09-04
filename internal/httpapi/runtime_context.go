package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

const runtimeContextScanLines = 4000

type runtimeContextSample struct {
	InputTokens   float64
	ContextWindow int64
}

func codexRuntimeContext(
	ctx context.Context,
	session domain.BackendSession,
	item domain.Workspace,
	workDir string,
) (runtimeContextSample, bool) {
	if session.ProviderSession == "" {
		return runtimeContextSample{}, false
	}
	if sessionPath, ok := backendSessionArtifactPath(session, item, workDir); ok {
		var sample runtimeContextSample
		found := scanJSONLinesReverse(sessionPath, runtimeContextScanLines, func(record map[string]any) bool {
			var ok bool
			sample, ok = codexContextSample(record)
			return ok
		})
		return sample, found
	}
	if item.Transport != "wsl" && item.Transport != "ssh" && item.Locality != "remote" {
		return runtimeContextSample{}, false
	}
	return codexRuntimeContextFromRunner(
		ctx,
		workspace.RunnerFor(item),
		workDir,
		remoteBackendSessionRoot(session, workDir),
		session.ProviderSession,
	)
}

func codexRuntimeContextFromRunner(
	ctx context.Context,
	runner workspace.Runner,
	workDir, root, sessionID string,
) (runtimeContextSample, bool) {
	result, err := runner.Run(ctx, workspace.Command{
		Executable: "sh",
		Args: []string{
			"-c",
			`root="$1"; [ "$root" = "__HOME_CODEX__" ] && root="$HOME/.codex/sessions"; ` +
				`file=$(find "$root" -type f -name "*$2*.jsonl" 2>/dev/null | head -n 1); ` +
				`[ -n "$file" ] || exit 1; tail -n 4000 "$file"`,
			"aha2-context", root, sessionID,
		},
		Dir: workDir, Timeout: 20 * time.Second,
	}, nil)
	if err != nil || result.ExitCode != 0 {
		return runtimeContextSample{}, false
	}
	var sample runtimeContextSample
	found := scanJSONBytesReverse([]byte(result.Stdout), runtimeContextScanLines, func(record map[string]any) bool {
		var ok bool
		sample, ok = codexContextSample(record)
		return ok
	})
	return sample, found
}

func codexContextSample(record map[string]any) (runtimeContextSample, bool) {
	payload, _ := record["payload"].(map[string]any)
	info, _ := payload["info"].(map[string]any)
	if payload["type"] != "token_count" || len(info) == 0 {
		return runtimeContextSample{}, false
	}
	usage, _ := info["last_token_usage"].(map[string]any)
	input := usageNumber(usage, "input_tokens")
	window := int64(usageNumber(info, "model_context_window"))
	if input <= 0 || window <= 0 {
		return runtimeContextSample{}, false
	}
	return runtimeContextSample{InputTokens: input, ContextWindow: window}, true
}

func backendSessionArtifactSize(
	ctx context.Context,
	session domain.BackendSession,
	item domain.Workspace,
	workDir string,
) (int64, bool) {
	if session.ProviderSession == "" {
		return 0, false
	}
	if sessionPath, ok := backendSessionArtifactPath(session, item, workDir); ok {
		if info, err := os.Stat(sessionPath); err == nil {
			return info.Size(), true
		}
	}
	if item.Transport != "wsl" && item.Transport != "ssh" && item.Locality != "remote" {
		return 0, false
	}
	return backendSessionArtifactSizeFromRunner(
		ctx,
		workspace.RunnerFor(item),
		workDir,
		remoteBackendSessionRoot(session, workDir),
		session.ProviderSession,
	)
}

func backendSessionArtifactSizeFromRunner(
	ctx context.Context,
	runner workspace.Runner,
	workDir, root, sessionID string,
) (int64, bool) {
	result, err := runner.Run(ctx, workspace.Command{
		Executable: "sh",
		Args: []string{
			"-c",
			`root="$1"; case "$root" in ` +
				`"__HOME_CODEX__") root="$HOME/.codex/sessions" ;; ` +
				`"__HOME_CLAUDE__") root="$HOME/.claude/projects" ;; esac; ` +
				`file=$(find "$root" -type f -name "*$2*.jsonl" 2>/dev/null | head -n 1); ` +
				`[ -n "$file" ] || exit 1; wc -c < "$file"`,
			"aha2-session-file", root, sessionID,
		},
		Dir: workDir, Timeout: 20 * time.Second,
	}, nil)
	if err != nil || result.ExitCode != 0 {
		return 0, false
	}
	size, err := strconv.ParseInt(strings.TrimSpace(result.Stdout), 10, 64)
	return size, err == nil
}

func backendSessionArtifactPath(
	session domain.BackendSession,
	item domain.Workspace,
	workDir string,
) (string, bool) {
	var roots []string
	if item.Transport == "native" {
		root := nativeBackendSessionRoot(session, workDir)
		if root != "" {
			roots = append(roots, root)
		}
	} else if item.Transport == "wsl" && item.Distro != "" {
		root := linuxBackendSessionRoot(session, item.RootPath, workDir)
		if root != "" {
			suffix := strings.ReplaceAll(strings.TrimPrefix(root, "/"), "/", `\`)
			roots = append(roots,
				`\\wsl.localhost\`+item.Distro+`\`+suffix,
				`\\wsl$\`+item.Distro+`\`+suffix,
			)
		}
	}
	var newest string
	var newestTime int64
	for _, root := range roots {
		pattern := backendSessionGlob(root, session.Backend, session.ProviderSession)
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				continue
			}
			if newest == "" || info.ModTime().UnixNano() > newestTime {
				newest = match
				newestTime = info.ModTime().UnixNano()
			}
		}
	}
	return newest, newest != ""
}

func nativeBackendSessionRoot(session domain.BackendSession, workDir string) string {
	if session.Backend == "codex" && session.CodexAccountID != "" {
		return filepath.Join(workDir, ".aha2-context", "runtime", "codex-auth", session.ID, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if session.Backend == "claude" {
		return filepath.Join(home, ".claude", "projects")
	}
	return filepath.Join(home, ".codex", "sessions")
}

func linuxBackendSessionRoot(session domain.BackendSession, workspaceRoot, workDir string) string {
	if session.Backend == "codex" && session.CodexAccountID != "" {
		return path.Join(strings.ReplaceAll(workDir, `\`, "/"), ".aha2-context", "runtime", "codex-auth", session.ID, "sessions")
	}
	home := linuxHomeFromWorkspace(workspaceRoot)
	if home == "" {
		return ""
	}
	if session.Backend == "claude" {
		return path.Join(home, ".claude", "projects")
	}
	return path.Join(home, ".codex", "sessions")
}

func remoteBackendSessionRoot(session domain.BackendSession, workDir string) string {
	if session.Backend == "codex" && session.CodexAccountID != "" {
		return path.Join(strings.ReplaceAll(workDir, `\`, "/"), ".aha2-context", "runtime", "codex-auth", session.ID, "sessions")
	}
	if session.Backend == "claude" {
		return "__HOME_CLAUDE__"
	}
	return "__HOME_CODEX__"
}

func backendSessionGlob(root, backend, sessionID string) string {
	if backend == "claude" {
		return filepath.Join(root, "*", "*"+sessionID+"*.jsonl")
	}
	return filepath.Join(root, "*", "*", "*", "*"+sessionID+"*.jsonl")
}

func linuxHomeFromWorkspace(root string) string {
	clean := strings.Trim(strings.ReplaceAll(root, `\`, "/"), "/")
	parts := strings.Split(clean, "/")
	if len(parts) >= 2 && parts[0] == "home" {
		return "/home/" + parts[1]
	}
	if len(parts) >= 1 && parts[0] == "root" {
		return "/root"
	}
	return ""
}

func scanJSONLinesReverse(path string, limit int, visit func(map[string]any) bool) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false
	}
	const blockSize int64 = 64 * 1024
	offset := info.Size()
	var carry []byte
	scanned := 0
	for offset > 0 && scanned < limit {
		size := blockSize
		if offset < size {
			size = offset
		}
		offset -= size
		block := make([]byte, size)
		if _, err := file.ReadAt(block, offset); err != nil {
			return false
		}
		data := append(block, carry...)
		lines := bytes.Split(data, []byte{'\n'})
		carry = append(carry[:0], lines[0]...)
		for index := len(lines) - 1; index >= 1 && scanned < limit; index-- {
			line := bytes.TrimSpace(lines[index])
			if len(line) == 0 {
				continue
			}
			scanned++
			var record map[string]any
			if json.Unmarshal(line, &record) == nil && visit(record) {
				return true
			}
		}
	}
	if scanned < limit && len(bytes.TrimSpace(carry)) > 0 {
		var record map[string]any
		return json.Unmarshal(bytes.TrimSpace(carry), &record) == nil && visit(record)
	}
	return false
}

func scanJSONBytesReverse(data []byte, limit int, visit func(map[string]any) bool) bool {
	lines := bytes.Split(data, []byte{'\n'})
	scanned := 0
	for index := len(lines) - 1; index >= 0 && scanned < limit; index-- {
		line := bytes.TrimSpace(lines[index])
		if len(line) == 0 {
			continue
		}
		scanned++
		var record map[string]any
		if json.Unmarshal(line, &record) == nil && visit(record) {
			return true
		}
	}
	return false
}
