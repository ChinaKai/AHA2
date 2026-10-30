package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"time"
)

// ClaudeProjectSlug converts a working directory into the directory name Claude
// uses for that project's transcripts: every character that is not a letter,
// digit or dash becomes a dash (verified against Claude Code 2.1.207, where
// both "/tmp/a_b" and "/tmp/a.b" collapse to "-tmp-a-b").
func ClaudeProjectSlug(workDir string) string {
	value := strings.TrimSpace(workDir)
	var builder strings.Builder
	builder.Grow(len(value))
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' {
			builder.WriteRune(character)
			continue
		}
		builder.WriteByte('-')
	}
	return builder.String()
}

// DefaultClaudeConfigDir returns the config directory Claude uses on the machine
// this process runs on when CLAUDE_CONFIG_DIR is unset, i.e. ~/.claude there.
//
// Use ClaudeDefaultConfigDirOn for a workspace that is not this host: a WSL or SSH
// workspace has its own home directory, and the control plane's own home does not
// exist on it.
func DefaultClaudeConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return path.Join(strings.ReplaceAll(home, `\`, "/"), ".claude")
}

// ClaudeDefaultConfigDirOn returns the config directory Claude uses on the
// workspace host when CLAUDE_CONFIG_DIR is unset.
//
// The host is asked rather than assumed, because the transcript of a backend
// session lives in that host's home directory, not in the control plane's. A WSL
// or SSH workspace has its own home, and guessing the control plane's own home
// would look in a directory that does not exist there.
func ClaudeDefaultConfigDirOn(ctx context.Context, runner Runner) string {
	if IsWindowsRunner(runner) {
		// A Windows host has no POSIX shell; its home is the user profile.
		result, err := runner.Run(ctx, Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				`[Console]::Out.Write($env:USERPROFILE)`},
			Timeout: 20 * time.Second,
		}, func(string) {})
		if err != nil || result.ExitCode != 0 {
			return ""
		}
		profile := strings.TrimSpace(strings.Trim(result.Stdout, "\x00"))
		if profile == "" {
			return ""
		}
		return windowsRemotePathJoin(profile, ".claude")
	}
	result, err := runner.Run(ctx, Command{
		Executable: "sh",
		Args:       []string{"-c", `printf '%s' "${HOME:-}/.claude"`},
		Timeout:    15 * time.Second,
	}, func(string) {})
	if err == nil && result.ExitCode == 0 {
		if directory := strings.TrimSpace(result.Stdout); directory != "" && directory != "/.claude" {
			return directory
		}
	}
	return DefaultClaudeConfigDir()
}

// ClaudeTranscriptExists reports whether the transcript for a session already
// exists under the given config directory.
//
// A session can only be resumed from the config directory that holds its
// transcript. AHA gives new sessions their own isolated directory, but a session
// started before that isolation existed lives in the backend's own default
// directory, so resuming it there is the only way to continue it.
func ClaudeTranscriptExists(ctx context.Context, runner Runner, configDir, workDir, sessionID string) bool {
	if strings.TrimSpace(configDir) == "" || strings.TrimSpace(sessionID) == "" {
		return false
	}
	target := path.Join(configDir, "projects", ClaudeProjectSlug(workDir), sessionID+".jsonl")
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				fmt.Sprintf(`if (Test-Path -LiteralPath %s -PathType Leaf) { 'present' }`, powershellQuote(target))},
			Timeout: 20 * time.Second,
		}
	} else {
		command = Command{Executable: "test", Args: []string{"-f", target}, Timeout: 15 * time.Second}
	}
	result, err := runner.Run(ctx, command, func(string) {})
	if err != nil || result.ExitCode != 0 {
		return false
	}
	if IsWindowsRunner(runner) {
		return strings.TrimSpace(result.Stdout) == "present"
	}
	return true
}

// ClaudeTranscriptLastAssistantText recovers the last user-visible assistant
// text from a session transcript. It is a fallback for the case where the CLI
// exits cleanly without emitting a final result: the transcript can already
// contain the reply AHA never received. It never returns an older turn's text,
// and it returns "" when the final record is a tool call/result, so an
// incomplete turn is not reported as a successful reply.
func ClaudeTranscriptLastAssistantText(ctx context.Context, runner Runner, configDir, workDir, sessionID string, since time.Time) string {
	return lastClaudeAssistantText(claudeTranscriptTail(ctx, runner, configDir, workDir, sessionID), since)
}

// ClaudeTranscriptLastErrorText returns the most recent provider error written
// to the session transcript after since. The CLI sometimes reports a provider
// failure only as a synthetic assistant message in the transcript, while the
// process exits non-zero; using that text keeps the original error visible
// instead of a generic "claude exited with code N".
func ClaudeTranscriptLastErrorText(ctx context.Context, runner Runner, configDir, workDir, sessionID string, since time.Time) string {
	return lastClaudeErrorText(claudeTranscriptTail(ctx, runner, configDir, workDir, sessionID), since)
}

func claudeTranscriptTail(ctx context.Context, runner Runner, configDir, workDir, sessionID string) string {
	if strings.TrimSpace(configDir) == "" {
		return ""
	}
	projectDir := path.Join(configDir, "projects", ClaudeProjectSlug(workDir))
	target := ""
	if strings.TrimSpace(sessionID) != "" {
		target = path.Join(projectDir, sessionID+".jsonl")
	}
	if target == "" {
		target = newestClaudeTranscript(ctx, runner, projectDir)
	}
	if target == "" {
		return ""
	}
	output := readClaudeTranscriptTail(ctx, runner, target)
	if strings.TrimSpace(output) == "" && strings.TrimSpace(sessionID) != "" {
		// The CLI can write the transcript under a session id it never emitted
		// (for example when the provider rejects the first request). Fall back
		// to the newest transcript in this project so the error is still shown.
		if newest := newestClaudeTranscript(ctx, runner, projectDir); newest != "" && newest != target {
			output = readClaudeTranscriptTail(ctx, runner, newest)
		}
	}
	return output
}

func readClaudeTranscriptTail(ctx context.Context, runner Runner, target string) string {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				fmt.Sprintf(`Get-Content -LiteralPath %s -Tail 400 -Encoding UTF8 -ErrorAction Stop`, powershellQuote(target))},
			Timeout: 20 * time.Second,
		}
	} else {
		command = Command{Executable: "tail", Args: []string{"-n", "400", target}, Timeout: 15 * time.Second}
	}
	// A no-op handler rather than nil keeps simple test runners that forward
	// every stdout line safe; real runners ignore the handler when nil anyway.
	result, err := runner.Run(ctx, command, func(string) {})
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	return result.Stdout
}

// powershellQuote embeds a path in a -Command script as a single-quoted
// PowerShell literal. Passing a path as a trailing argv to -Command does not
// bind it to a param() block, which made transcript checks silently look at an
// empty path on native Windows.
func powershellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func newestClaudeTranscript(ctx context.Context, runner Runner, projectDir string) string {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				fmt.Sprintf(`Get-ChildItem -LiteralPath %s -Filter *.jsonl -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending | Select-Object -First 1 -ExpandProperty FullName`, powershellQuote(projectDir))},
			Timeout: 20 * time.Second,
		}
	} else {
		command = Command{
			Executable: "sh",
			Args:       []string{"-c", `ls -1t "$1"/*.jsonl 2>/dev/null | head -n 1`, "sh", projectDir},
			Timeout:    15 * time.Second,
		}
	}
	result, err := runner.Run(ctx, command, func(string) {})
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(strings.Trim(result.Stdout, "\x00"))
}

// lastClaudeAssistantText walks the transcript tail backwards and decides what
// the final visible record was. Metadata records are skipped; the first real
// record decides. An assistant record with text and no tool_use is the only
// recoverable reply; a tool_use/tool_result or a user prompt means the turn did
// not end with text, so an older assistant text must not be reused.
func lastClaudeAssistantText(output string, since time.Time) string {
	lines := strings.Split(output, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		var record struct {
			Type              string `json:"type"`
			Timestamp         string `json:"timestamp"`
			IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
			IsSidechain       bool   `json:"isSidechain"`
			Message           struct {
				Model   string `json:"model"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		switch record.Type {
		case "last-prompt", "mode", "ai-title", "queue-operation", "attachment", "summary", "system":
			continue
		}
		if record.IsSidechain {
			return ""
		}
		if record.Type != "assistant" {
			return ""
		}
		if record.IsAPIErrorMessage || record.Message.Model == "<synthetic>" {
			return ""
		}
		if record.Timestamp != "" {
			timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp)
			if err == nil && timestamp.Before(since) {
				return ""
			}
		}
		texts := make([]string, 0, len(record.Message.Content))
		hasTool := false
		for _, block := range record.Message.Content {
			switch block.Type {
			case "text":
				if strings.TrimSpace(block.Text) != "" {
					texts = append(texts, block.Text)
				}
			case "tool_use":
				hasTool = true
			}
		}
		if hasTool || len(texts) == 0 {
			return ""
		}
		return strings.TrimSpace(strings.Join(texts, "\n"))
	}
	return ""
}

func lastClaudeErrorText(output string, since time.Time) string {
	lines := strings.Split(output, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		var record struct {
			Type              string `json:"type"`
			Timestamp         string `json:"timestamp"`
			IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
			IsSidechain       bool   `json:"isSidechain"`
			Error             string `json:"error"`
			Subtype           string `json:"subtype"`
			Result            string `json:"result"`
			Message           struct {
				Model   string `json:"model"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		if record.IsSidechain {
			continue
		}
		if record.Timestamp != "" {
			timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp)
			if err == nil && timestamp.Before(since) {
				return ""
			}
		}
		text := assistantRecordText(record.Message.Content)
		if record.IsAPIErrorMessage || record.Message.Model == "<synthetic>" {
			if text != "" {
				return text
			}
		}
		if record.Type == "result" {
			if message := strings.TrimSpace(record.Error); message != "" && message != "unknown" {
				return message
			}
			if strings.TrimSpace(record.Result) != "" && record.Subtype != "success" {
				return record.Result
			}
		}
	}
	return ""
}

func assistantRecordText(content []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
