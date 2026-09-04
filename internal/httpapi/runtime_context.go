package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
) (runtimeContextSample, bool) {
	if session.ProviderSession == "" {
		return runtimeContextSample{}, false
	}
	if path, ok := codexSessionPath(session.ProviderSession, item); ok {
		var sample runtimeContextSample
		found := scanJSONLinesReverse(path, runtimeContextScanLines, func(record map[string]any) bool {
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
		item.RootPath,
		session.ProviderSession,
	)
}

func codexRuntimeContextFromRunner(
	ctx context.Context,
	runner workspace.Runner,
	workDir, sessionID string,
) (runtimeContextSample, bool) {
	result, err := runner.Run(ctx, workspace.Command{
		Executable: "sh",
		Args: []string{
			"-c",
			`file=$(find "$HOME/.codex/sessions" -type f -name "*$1*.jsonl" 2>/dev/null | head -n 1); ` +
				`[ -n "$file" ] || exit 1; tail -n 4000 "$file"`,
			"aha2-context", sessionID,
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

func codexSessionPath(sessionID string, workspace domain.Workspace) (string, bool) {
	var homes []string
	if workspace.Transport == "native" {
		if home, err := os.UserHomeDir(); err == nil {
			homes = append(homes, home)
		}
	} else if workspace.Transport == "wsl" && workspace.Distro != "" {
		if home := linuxHomeFromWorkspace(workspace.RootPath); home != "" {
			suffix := strings.ReplaceAll(strings.TrimPrefix(home, "/"), "/", `\`)
			homes = append(homes,
				`\\wsl.localhost\`+workspace.Distro+`\`+suffix,
				`\\wsl$\`+workspace.Distro+`\`+suffix,
			)
		}
	}
	var newest string
	var newestTime int64
	for _, home := range homes {
		pattern := filepath.Join(home, ".codex", "sessions", "*", "*", "*", "*"+sessionID+"*.jsonl")
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
