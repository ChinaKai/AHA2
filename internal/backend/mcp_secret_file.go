package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

// MCP server env values and headers are credentials for that server. They used
// to be rendered into argv (Claude --mcp-config JSON, Codex -c overrides), which
// any local process could read from the OS process list. These helpers keep
// them in a generated file that only the execution host can read, so the
// command line carries a path instead of the secret.
//
// Cleanup and restore failures are returned to the adapter, which fails the
// Turn instead of reporting a success while a credential file may remain.

// mcpSecretFile is a generated file on the execution host that holds rendered
// MCP configuration. It is removed when the backend run ends.
type mcpSecretFile struct {
	request Request
	path    string
	local   bool
}

func isLocalRunner(runner workspace.Runner) bool {
	_, local := runner.(workspace.LocalRunner)
	return local
}

// claudeMCPConfigFileName is stable for one workspace/backend config directory,
// so a file left by a killed process is overwritten by the next run instead of
// accumulating under a new random name.
func claudeMCPConfigFileName(request Request) string {
	key := strings.TrimSpace(request.WorkDir) + "\x00" + strings.TrimSpace(request.ClaudeConfigDir)
	sum := sha256.Sum256([]byte(key))
	return "aha2-mcp-claude-" + hex.EncodeToString(sum[:12]) + ".json"
}

// claudeMCPConfigPath returns the deterministic, AHA-owned location of this
// run's Claude MCP config. Local runs use a fixed directory under the user's
// temp root (owner-private on Windows); remote runs use the workspace runtime
// directory. Only files with this exact name are touched.
func claudeMCPConfigPath(request Request) (string, bool) {
	local := isLocalRunner(request.Runner)
	name := claudeMCPConfigFileName(request)
	if local {
		return filepath.Join(os.TempDir(), "aha2-mcp-config", name), true
	}
	return joinRemotePath(request.WorkDir, ".aha2-context", "runtime", "mcp-config", name), false
}

// writeClaudeMCPConfig replaces any stale config from a killed run, then writes
// the new content atomically and owner-only.
func writeClaudeMCPConfig(ctx context.Context, request Request, content string) (*mcpSecretFile, error) {
	path, local := claudeMCPConfigPath(request)
	if local {
		if err := removeLocalFile(path); err != nil {
			return nil, fmt.Errorf("remove stale MCP config: %w", err)
		}
		if err := writeLocalFileAtomic(path, content); err != nil {
			return nil, fmt.Errorf("write MCP config: %w", err)
		}
	} else {
		if err := workspace.RemoveRemoteFile(ctx, request.Runner, path); err != nil {
			return nil, fmt.Errorf("remove stale MCP config: %w", err)
		}
		if err := workspace.WriteRemoteTextFileAtomic(ctx, request.Runner, path, content); err != nil {
			return nil, fmt.Errorf("write MCP config: %w", err)
		}
	}
	return &mcpSecretFile{request: request, path: path, local: local}, nil
}

// cleanup removes the generated file even when the run context was cancelled.
func (file *mcpSecretFile) cleanup(ctx context.Context) error {
	if file == nil || file.path == "" {
		return nil
	}
	if file.local {
		return removeLocalFile(file.path)
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return workspace.RemoveRemoteFile(cleanupCtx, file.request.Runner, file.path)
}

// codexMCPConfigFile is the per-session CODEX_HOME/config.toml with the MCP
// registrations temporarily merged in. Codex has no --mcp-config file flag, so
// its config file is the only place an MCP secret can live without appearing in
// argv. The previous content is restored when the run ends.
type codexMCPConfigFile struct {
	request    Request
	path       string
	backupPath string
	original   string
	existed    bool
	local      bool
}

const codexMCPConfigBackupSuffix = ".aha2-orig"

func codexConfigPath(home string, local bool) string {
	if local {
		return filepath.Join(home, "config.toml")
	}
	return joinRemotePath(home, "config.toml")
}

func prepareCodexMCPConfig(ctx context.Context, request Request, content string) (*codexMCPConfigFile, error) {
	home := strings.TrimSpace(request.Environment["CODEX_HOME"])
	if home == "" {
		return nil, fmt.Errorf("prepare Codex MCP configuration: CODEX_HOME is not set")
	}
	local := isLocalRunner(request.Runner)
	path := codexConfigPath(home, local)
	backupPath := path + codexMCPConfigBackupSuffix
	if err := recoverCodexConfigBackup(ctx, request, path, backupPath, local); err != nil {
		return nil, err
	}
	original, existed, err := readExecutionFile(ctx, request, path, local)
	if err != nil {
		return nil, fmt.Errorf("read Codex config: %w", err)
	}
	// A previous run may have crashed after writing the generated block. Remove
	// it instead of failing forever on the duplicate TOML table it left behind.
	original = stripGeneratedCodexMCPConfig(original)
	if strings.Contains(original, "[mcp_servers.") {
		// The isolated session home is AHA-owned; a pre-existing MCP table means
		// appending would create a duplicate TOML key. Refuse rather than put the
		// secret back on the command line.
		return nil, fmt.Errorf("Codex session config already defines mcp_servers")
	}
	merged := original
	if merged != "" && !strings.HasSuffix(merged, "\n") {
		merged += "\n"
	}
	merged += content
	if existed {
		if err := writeExecutionFileAtomic(ctx, request, backupPath, original, local); err != nil {
			return nil, fmt.Errorf("back up Codex config: %w", err)
		}
	}
	if err := writeExecutionFileAtomic(ctx, request, path, merged, local); err != nil {
		return nil, fmt.Errorf("write Codex MCP configuration: %w", err)
	}
	return &codexMCPConfigFile{
		request: request, path: path, backupPath: backupPath,
		original: original, existed: existed && strings.TrimSpace(original) != "", local: local,
	}, nil
}

// recoverCodexConfigBackup handles a backup left by a crashed run. The backup
// is authoritative only when the live config is missing or still contains the
// generated block; otherwise the live config was edited and must not be
// overwritten.
func recoverCodexConfigBackup(ctx context.Context, request Request, path, backupPath string, local bool) error {
	backup, hasBackup, err := readExecutionFile(ctx, request, backupPath, local)
	if err != nil {
		return fmt.Errorf("read Codex config backup: %w", err)
	}
	if !hasBackup {
		return nil
	}
	current, hasCurrent, err := readExecutionFile(ctx, request, path, local)
	if err != nil {
		return fmt.Errorf("read Codex config: %w", err)
	}
	if hasCurrent && markerLineIndex(current, codexMCPConfigBegin) < 0 {
		if current == backup {
			return removeExecutionFile(ctx, request, backupPath, local)
		}
		return fmt.Errorf("Codex config backup %s exists but the current config has no generated MCP block; refusing to overwrite it", backupPath)
	}
	if err := writeExecutionFileAtomic(ctx, request, path, backup, local); err != nil {
		return fmt.Errorf("restore Codex config from backup: %w", err)
	}
	return removeExecutionFile(ctx, request, backupPath, local)
}

const (
	codexMCPConfigBegin = "# AHA2 MCP configuration (generated; removed after the run)"
	codexMCPConfigEnd   = "# AHA2 MCP configuration end"
)

// stripGeneratedCodexMCPConfig removes a block left by an earlier run that did
// not reach cleanup. Without it, the next run would see a duplicate
// [mcp_servers.*] table and could not start.
func stripGeneratedCodexMCPConfig(content string) string {
	for {
		begin := markerLineIndex(content, codexMCPConfigBegin)
		if begin < 0 {
			return content
		}
		rest := content[begin:]
		end := markerLineIndex(rest, codexMCPConfigEnd)
		if end < 0 {
			// A partial block ends at EOF.
			return strings.TrimRight(content[:begin], "\n") + "\n"
		}
		tail := rest[end+len(codexMCPConfigEnd):]
		content = strings.TrimRight(content[:begin], "\n") + "\n" + strings.TrimLeft(tail, "\n")
	}
}

// markerLineIndex finds marker only at the start of a line. MCP env values are
// escaped onto one line, so a value that happens to contain the marker text
// cannot be mistaken for the generated block.
func markerLineIndex(content, marker string) int {
	offset := 0
	for offset < len(content) {
		index := strings.Index(content[offset:], marker)
		if index < 0 {
			return -1
		}
		index += offset
		if index == 0 || content[index-1] == '\n' {
			return index
		}
		offset = index + len(marker)
	}
	return -1
}

// cleanup restores the previous config. It only overwrites the live file when
// the generated block is still present; if another writer removed it, the
// backup is kept and the caller fails the Turn instead of losing their edit.
func (file *codexMCPConfigFile) cleanup(ctx context.Context) error {
	if file == nil || file.path == "" {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	current, exists, err := readExecutionFile(cleanupCtx, file.request, file.path, file.local)
	if err != nil {
		return fmt.Errorf("read Codex config for restore: %w", err)
	}
	if exists && markerLineIndex(current, codexMCPConfigBegin) < 0 {
		return fmt.Errorf("Codex config was changed during the run; generated MCP block is missing and the original was left at %s", file.backupPath)
	}
	switch {
	case file.existed:
		if err := writeExecutionFileAtomic(cleanupCtx, file.request, file.path, file.original, file.local); err != nil {
			return fmt.Errorf("restore Codex config: %w", err)
		}
	case exists:
		if err := removeExecutionFile(cleanupCtx, file.request, file.path, file.local); err != nil {
			return fmt.Errorf("remove generated Codex config: %w", err)
		}
	}
	if err := removeExecutionFile(cleanupCtx, file.request, file.backupPath, file.local); err != nil {
		return fmt.Errorf("remove Codex config backup: %w", err)
	}
	return nil
}

func readExecutionFile(ctx context.Context, request Request, path string, local bool) (string, bool, error) {
	if local {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return string(data), true, nil
	}
	return workspace.ReadRemoteTextFileIfExists(ctx, request.Runner, path)
}

func writeExecutionFileAtomic(ctx context.Context, request Request, path, content string, local bool) error {
	if local {
		return writeLocalFileAtomic(path, content)
	}
	return workspace.WriteRemoteTextFileAtomic(ctx, request.Runner, path, content)
}

func removeExecutionFile(ctx context.Context, request Request, path string, local bool) error {
	if local {
		return removeLocalFile(path)
	}
	return workspace.RemoveRemoteFile(ctx, request.Runner, path)
}

// writeLocalFileAtomic writes through a temporary file in the target directory
// and renames it into place, so a crash cannot leave a truncated config.
func writeLocalFileAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dir, 0o700)
	}
	file, err := os.CreateTemp(dir, ".aha2-tmp-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func removeLocalFile(path string) error {
	err := os.Remove(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return err
}

// codexMCPConfigTOML renders all neutral MCP registrations as Codex config
// tables. The backend reads them from $CODEX_HOME/config.toml, so neither the
// server command/args nor its env and headers reach the process command line.
func codexMCPConfigTOML(servers []MCPServer) string {
	var output strings.Builder
	for _, server := range servers {
		name := codexMCPKey(server.Name)
		if name == "" {
			continue
		}
		prefix := "mcp_servers." + name
		switch strings.ToLower(strings.TrimSpace(server.Transport)) {
		case "stdio":
			if strings.TrimSpace(server.Command) == "" {
				continue
			}
			output.WriteString("\n[" + prefix + "]\n")
			output.WriteString("default_tools_approval_mode = " + tomlString("approve") + "\n")
			output.WriteString("command = " + tomlString(server.Command) + "\n")
			if len(server.Args) > 0 {
				values := make([]string, 0, len(server.Args))
				for _, value := range server.Args {
					values = append(values, tomlString(value))
				}
				output.WriteString("args = [" + strings.Join(values, ", ") + "]\n")
			}
			writeCodexMCPTable(&output, prefix+".env", server.Env)
		case "http":
			if strings.TrimSpace(server.URL) == "" {
				continue
			}
			output.WriteString("\n[" + prefix + "]\n")
			output.WriteString("default_tools_approval_mode = " + tomlString("approve") + "\n")
			output.WriteString("url = " + tomlString(server.URL) + "\n")
			bearerEnv := strings.TrimSpace(server.BearerTokenEnv)
			if bearerEnv != "" {
				output.WriteString("bearer_token_env_var = " + tomlString(bearerEnv) + "\n")
			}
			headers := server.Headers
			if bearerEnv != "" {
				headers = headersWithoutAuthorization(server.Headers)
			}
			writeCodexMCPTable(&output, prefix+".http_headers", headers)
		}
	}
	body := strings.TrimLeft(output.String(), "\n")
	if body == "" {
		return ""
	}
	return "\n" + codexMCPConfigBegin + "\n" + body + codexMCPConfigEnd + "\n"
}

func writeCodexMCPTable(output *strings.Builder, table string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	output.WriteString("\n[" + table + "]\n")
	for _, key := range keys {
		output.WriteString(tomlString(key) + " = " + tomlString(values[key]) + "\n")
	}
}

// joinRemotePath joins an execution-host path without losing UNC semantics.
// path.Join collapses a leading "//server/share" to "/server/share", which
// silently changes the host; keep the double slash for UNC inputs.
func joinRemotePath(root string, values ...string) string {
	base := strings.ReplaceAll(strings.TrimSpace(root), `\`, "/")
	if strings.HasPrefix(base, "//") {
		return "//" + pathpkg.Join(append([]string{strings.TrimPrefix(base, "//")}, values...)...)
	}
	return pathpkg.Join(append([]string{base}, values...)...)
}
