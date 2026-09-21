package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"runtime"
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

// claudeContextSample reads the context a Claude request actually occupied.
//
// A Claude transcript records usage per assistant message as the tokens of that
// one request, so the newest such record is the current occupancy. This is the
// value the context page needs. It is deliberately not derived from a turn's
// usage: a turn records the CLI's session cumulative, which after a few turns
// dwarfs the window and would report nonsense.
//
// The HTTP handler addresses both the isolated session directory and the default
// one through backendSessionArtifactPath, so a local read needs no extra roots;
// the runner path below covers the case where neither is readable from here.
func claudeContextSample(record map[string]any) (runtimeContextSample, bool) {
	message, _ := record["message"].(map[string]any)
	if message == nil {
		return runtimeContextSample{}, false
	}
	usage, _ := message["usage"].(map[string]any)
	if len(usage) == 0 {
		return runtimeContextSample{}, false
	}
	// A single request counts its whole input as uncached plus cache reads and
	// cache writes.
	input := usageNumber(usage, "input_tokens") +
		usageNumber(usage, "cache_read_input_tokens") +
		usageNumber(usage, "cache_creation_input_tokens")
	if input <= 0 {
		return runtimeContextSample{}, false
	}
	return runtimeContextSample{InputTokens: input}, true
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
	command := workspace.Command{
		Executable: "sh",
		Args: []string{
			"-c",
			`root="$1"; [ "$root" = "__HOME_CODEX__" ] && root="$HOME/.codex/sessions"; ` +
				`file=$(find "$root" -type f -name "*$2*.jsonl" 2>/dev/null | head -n 1); ` +
				`[ -n "$file" ] || exit 1; tail -n 4000 "$file"`,
			"aha2-context", root, sessionID,
		},
		Dir: workDir, Timeout: 20 * time.Second,
	}
	if workspace.IsWindowsRunner(runner) {
		command = workspace.Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `param([string]$Root, [string]$Session)
if ($Root -eq '__HOME_CODEX__') { $Root = Join-Path $HOME '.codex\sessions' }
$file = Get-ChildItem -LiteralPath $Root -Recurse -File -Filter "*$Session*.jsonl" -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $file) { exit 1 }
Get-Content -LiteralPath $file.FullName -Tail 4000`, root, sessionID},
			Dir: workDir, Timeout: 20 * time.Second,
		}
	}
	result, err := runner.Run(ctx, command, nil)
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

// claudeRuntimeContext samples the newest request's context occupancy from the
// session transcript.
//
// The session may live in the isolated per-session directory or the default one,
// so the host is asked to search both; reading only the default is what made an
// Env-provider session look like it had no transcript at all.
func claudeRuntimeContext(
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
			sample, ok = claudeContextSample(record)
			return ok
		})
		return sample, found
	}
	if item.Transport != "wsl" && item.Transport != "ssh" && item.Locality != "remote" {
		return runtimeContextSample{}, false
	}
	return claudeRuntimeContextFromRunner(
		ctx,
		workspace.RunnerFor(item),
		workDir,
		remoteBackendSessionRoot(session, workDir),
		isolatedClaudeSessionRoot(session, item, workDir),
		session.ProviderSession,
	)
}

func claudeRuntimeContextFromRunner(
	ctx context.Context,
	runner workspace.Runner,
	workDir, root, isolatedRoot, sessionID string,
) (runtimeContextSample, bool) {
	// $2 is the isolated per-session directory, empty when there is none; the
	// default directory is searched after it.
	command := workspace.Command{
		Executable: "sh",
		Args: []string{
			"-c",
			`root="$1"; case "$root" in ` +
				`"__HOME_CLAUDE__") root="$HOME/.claude/projects" ;; esac; ` +
				`dirs="$2"; [ -n "$2" ] || dirs="$root"; [ -d "$root" ] && dirs="$dirs
$root"; ` +
				`file=$(printf '%s\n' "$dirs" | while read -r dir; do ` +
				`[ -n "$dir" ] && find "$dir" -type f -name "*$3*.jsonl" 2>/dev/null | head -n 1; done | head -n 1); ` +
				`[ -n "$file" ] || exit 1; tail -n 4000 "$file"`,
			"aha2-claude-context", root, isolatedRoot, sessionID,
		},
		Dir: workDir, Timeout: 20 * time.Second,
	}
	if workspace.IsWindowsRunner(runner) {
		command = workspace.Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `param([string]$Root, [string]$Isolated, [string]$Session)
if ($Root -eq '__HOME_CLAUDE__') { $Root = Join-Path $HOME '.claude\projects' }
$roots = @()
if ($Isolated) { $roots += $Isolated }
if ($Root -and $Root -ne $Isolated) { $roots += $Root }
$file = $null
foreach ($candidate in $roots) {
  $file = Get-ChildItem -LiteralPath $candidate -Recurse -File -Filter "*$Session*.jsonl" -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($file) { break }
}
if (-not $file) { exit 1 }
Get-Content -LiteralPath $file.FullName -Tail 4000`, root, isolatedRoot, sessionID},
			Dir: workDir, Timeout: 20 * time.Second,
		}
	}
	result, err := runner.Run(ctx, command, nil)
	if err != nil || result.ExitCode != 0 {
		return runtimeContextSample{}, false
	}
	var sample runtimeContextSample
	found := scanJSONBytesReverse([]byte(result.Stdout), runtimeContextScanLines, func(record map[string]any) bool {
		var ok bool
		sample, ok = claudeContextSample(record)
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
	// The isolated per-session directory is not reachable from here, so ask the
	// host to look in both it and the default directory. Searching only the
	// default is what reported every Env-provider session as unavailable.
	return backendSessionArtifactSizeFromRunner(
		ctx,
		workspace.RunnerFor(item),
		workDir,
		remoteBackendSessionRoot(session, workDir),
		isolatedClaudeSessionRoot(session, item, workDir),
		session.ProviderSession,
	)
}

func backendSessionArtifactSizeFromRunner(
	ctx context.Context,
	runner workspace.Runner,
	workDir, root, isolatedRoot, sessionID string,
) (int64, bool) {
	// $2 is the isolated per-session directory, empty when there is none. The
	// default directory comes second so a native-source session, which keeps the
	// operator's own config, is still found.
	command := workspace.Command{
		Executable: "sh",
		Args: []string{
			"-c",
			`root="$1"; case "$root" in ` +
				`"__HOME_CODEX__") root="$HOME/.codex/sessions" ;; ` +
				`"__HOME_CLAUDE__") root="$HOME/.claude/projects" ;; esac; ` +
				`dirs="$2"; [ -n "$2" ] || dirs="$root"; [ -d "$root" ] && dirs="$dirs
$root"; ` +
				`file=$(printf '%s\n' "$dirs" | while read -r dir; do ` +
				`[ -n "$dir" ] && find "$dir" -type f -name "*$3*.jsonl" 2>/dev/null | head -n 1; done | head -n 1); ` +
				`[ -n "$file" ] || exit 1; wc -c < "$file"`,
			"aha2-session-file", root, isolatedRoot, sessionID,
		},
		Dir: workDir, Timeout: 20 * time.Second,
	}
	if workspace.IsWindowsRunner(runner) {
		command = workspace.Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `param([string]$Root, [string]$Isolated, [string]$Session)
if ($Root -eq '__HOME_CODEX__') { $Root = Join-Path $HOME '.codex\sessions' }
if ($Root -eq '__HOME_CLAUDE__') { $Root = Join-Path $HOME '.claude\projects' }
$roots = @()
if ($Isolated) { $roots += $Isolated }
if ($Root -and $Root -ne $Isolated) { $roots += $Root }
$file = $null
foreach ($candidate in $roots) {
  $file = Get-ChildItem -LiteralPath $candidate -Recurse -File -Filter "*$Session*.jsonl" -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($file) { break }
}
if (-not $file) { exit 1 }
[Console]::Out.Write($file.Length)`, root, isolatedRoot, sessionID},
			Dir: workDir, Timeout: 20 * time.Second,
		}
	}
	result, err := runner.Run(ctx, command, nil)
	if err != nil || result.ExitCode != 0 {
		return 0, false
	}
	size, err := strconv.ParseInt(strings.TrimSpace(result.Stdout), 10, 64)
	return size, err == nil
}

// runningOnWindows reports whether this process is the Windows build, which is
// the only one that addresses a WSL workspace through \\wsl.localhost.
func runningOnWindows() bool {
	return runtime.GOOS == "windows"
}

func backendSessionArtifactPath(
	session domain.BackendSession,
	item domain.Workspace,
	workDir string,
) (string, bool) {
	// The isolated session directory comes first: it is where an Env-provider
	// session writes, and it is the only place its transcript exists.
	var candidates []string
	if root := isolatedClaudeSessionRoot(session, item, workDir); root != "" {
		candidates = append(candidates, root)
	}
	if item.Transport == "native" {
		if root := nativeBackendSessionRoot(session, workDir); root != "" {
			candidates = append(candidates, strings.ReplaceAll(root, `\`, "/"))
		}
	} else if item.Transport == "wsl" && item.Distro != "" {
		if root := linuxBackendSessionRoot(session, item.RootPath, workDir); root != "" {
			candidates = append(candidates, root)
		}
	}
	var roots []string
	for _, root := range candidates {
		// A Linux path may only be rewritten as a Windows UNC path when AHA itself
		// is running on Windows. A WSL workspace is reachable from there through
		// \\wsl.localhost; from inside WSL the same path is simply local, and
		// rewriting it produces a path that no host can resolve -- which is what
		// stopped every Claude session from being found.
		if runningOnWindows() && item.Transport == "wsl" && item.Distro != "" && strings.HasPrefix(root, "/") {
			suffix := strings.ReplaceAll(strings.TrimPrefix(root, "/"), "/", `\`)
			roots = append(roots,
				`\\wsl.localhost\`+item.Distro+`\`+suffix,
				`\\wsl$\`+item.Distro+`\`+suffix,
			)
			continue
		}
		roots = append(roots, filepath.FromSlash(root))
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

// isolatedClaudeSessionRoot returns the per-session config directory AHA gives a
// Claude Env-provider session, where that session's transcript actually lives.
//
// It is empty for a native-source session: those keep the operator's default
// config directory, which nativeBackendSessionRoot / linuxBackendSessionRoot
// already cover. Both locations must be searched — reading only the default
// reported every Env-provider session as "session file unavailable", and
// reading only the isolated directory would hide native-source transcripts.
func isolatedClaudeSessionRoot(session domain.BackendSession, item domain.Workspace, workDir string) string {
	if session.Backend != "claude" {
		return ""
	}
	directory := workspace.ClaudeConfigDir(item, workDir, session.ID)
	if strings.TrimSpace(directory) == "" {
		return ""
	}
	return path.Join(strings.ReplaceAll(directory, `\`, "/"), "projects")
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
