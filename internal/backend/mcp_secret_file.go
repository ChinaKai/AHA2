package backend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
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

// mcpSecretFile is a private file on the execution host that holds rendered MCP
// configuration. It is removed when the backend run ends.
type mcpSecretFile struct {
	request Request
	path    string
	// dir is the temporary directory owned by this process for a local run.
	// Empty for a remote run, where the file lives under the workspace runtime
	// directory.
	dir string
}

// writeMCPSecretFile renders content into owner-only storage and returns the
// path the backend should read.
//
// A local run uses os.MkdirTemp, which creates a 0700 directory. On Windows that
// directory is under %TEMP% inside the user profile, whose inherited ACL admits
// only the user, SYSTEM and Administrators; on POSIX the file itself is 0600.
// A remote run writes under the workspace runtime directory, where the POSIX
// writer applies umask 077 and a Windows workspace inherits the owning user's
// ACL.
func writeMCPSecretFile(ctx context.Context, request Request, name, content string) (*mcpSecretFile, error) {
	if _, local := request.Runner.(workspace.LocalRunner); local {
		dir, err := os.MkdirTemp("", "aha2-mcp-")
		if err != nil {
			return nil, fmt.Errorf("create MCP config directory: %w", err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("write MCP config: %w", err)
		}
		return &mcpSecretFile{request: request, path: path, dir: dir}, nil
	}
	dir := pathpkg.Join(remoteSlashPath(request.WorkDir), ".aha2-context", "runtime", "mcp-config", randomName())
	path := pathpkg.Join(dir, name)
	if err := workspace.WriteRemoteTextFile(ctx, request.Runner, path, content); err != nil {
		return nil, fmt.Errorf("write MCP config: %w", err)
	}
	return &mcpSecretFile{request: request, path: path}, nil
}

// cleanup removes the file even when the run context was cancelled. The
// workspace runtime directory keeps only the empty parent afterwards.
func (file *mcpSecretFile) cleanup(ctx context.Context) {
	if file == nil || file.path == "" {
		return
	}
	if _, local := file.request.Runner.(workspace.LocalRunner); local {
		_ = os.Remove(file.path)
		if file.dir != "" {
			_ = os.Remove(file.dir)
		}
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = workspace.RemoveRemoteFile(cleanupCtx, file.request.Runner, file.path)
}

// codexMCPConfigFile is the per-session CODEX_HOME/config.toml with the MCP
// registrations temporarily merged in. Codex has no --mcp-config file flag, so
// its config file is the only place an MCP secret can live without appearing in
// argv. The previous content is restored when the run ends.
type codexMCPConfigFile struct {
	request  Request
	path     string
	original string
	existed  bool
}

func prepareCodexMCPConfig(ctx context.Context, request Request, content string) (*codexMCPConfigFile, error) {
	home := strings.TrimSpace(request.Environment["CODEX_HOME"])
	if home == "" {
		return nil, fmt.Errorf("prepare Codex MCP configuration: CODEX_HOME is not set")
	}
	local := false
	if _, ok := request.Runner.(workspace.LocalRunner); ok {
		local = true
	}
	path := pathpkg.Join(remoteSlashPath(home), "config.toml")
	if local {
		path = filepath.Join(home, "config.toml")
	}
	original, existed, err := readCodexConfig(ctx, request, path, local)
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
	if err := writeCodexConfig(ctx, request, path, merged, local); err != nil {
		return nil, fmt.Errorf("write Codex MCP configuration: %w", err)
	}
	return &codexMCPConfigFile{
		request: request, path: path, original: original, existed: existed && strings.TrimSpace(original) != "",
	}, nil
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

func (file *codexMCPConfigFile) cleanup(ctx context.Context) {
	if file == nil || file.path == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	local := false
	if _, ok := file.request.Runner.(workspace.LocalRunner); ok {
		local = true
	}
	if !file.existed {
		if local {
			_ = os.Remove(file.path)
		} else {
			_ = workspace.RemoveRemoteFile(cleanupCtx, file.request.Runner, file.path)
		}
		return
	}
	_ = writeCodexConfig(cleanupCtx, file.request, file.path, file.original, local)
}

func readCodexConfig(ctx context.Context, request Request, path string, local bool) (string, bool, error) {
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
	data, err := workspace.ReadRemoteTextFile(ctx, request.Runner, path)
	if err != nil {
		// An empty CODEX_HOME has no config.toml yet. A missing file is not an
		// error here; the generated content creates it.
		return "", false, nil
	}
	return data, true, nil
}

func writeCodexConfig(ctx context.Context, request Request, path, content string, local bool) error {
	if local {
		return os.WriteFile(path, []byte(content), 0o600)
	}
	return workspace.WriteRemoteTextFile(ctx, request.Runner, path, content)
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

func remoteSlashPath(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
}

func randomName() string {
	var buffer [8]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer[:])
}
