package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentproxy"
	"github.com/ChinaKai/AHA2/internal/domain"
)

// The helper is a single-file Node script embedded in this executable. It is
// uploaded into the workspace's shared context, so many Tasks reuse one upload,
// and a running helper keeps working if a newer upload replaces the file (the
// running process already read the script). No prebuilt binary is shipped for
// any platform: the only requirement is that the workspace has a usable node.

const (
	agentProxyShareDirectory = "share"
	agentProxyDirectoryName  = "agent-proxy"
	agentProxyManifestName   = "manifest.json"
	// agentProxyScriptPrefix is also the prefix the upload script prunes, so a
	// prebuilt helper left by an older AHA2 release is removed on upgrade.
	agentProxyScriptPrefix = "aha2-agent-proxy"
	// agentProxyManifestLimit bounds the manifest read. A real manifest is a
	// few hundred bytes; anything larger is not one, and must be refused before
	// it is buffered rather than parsed.
	agentProxyManifestLimit = 4096
	// agentProxyHelperOverride points at a prebuilt helper to upload instead of
	// the embedded script. It stays first for operators who need a custom
	// runtime; without it the helper is always the embedded Node script.
	agentProxyHelperOverride = "AHA2_AGENT_PROXY_HELPER"
	// agentProxyRuntimeExec marks an explicitly supplied executable, which the
	// workspace runs directly rather than through an interpreter.
	agentProxyRuntimeExec = "exec"
)

// AgentProxyHelper is a materialised helper on a workspace.
type AgentProxyHelper struct {
	Path string
	// Runtime is agentproxy.NodeHelperRuntime for the embedded script, which is
	// started as `<Interpreter> <Path>`, or agentProxyRuntimeExec for an
	// operator-supplied executable started directly.
	Runtime string
	// Interpreter is the workspace-side node binary that runs Path when Runtime
	// is "node".
	Interpreter string
	// Note explains a fallback location, so the Turn log says why the shared
	// directory was not used instead of leaving it to be guessed.
	Note string
}

func (helper AgentProxyHelper) Command(protocol int) string {
	if helper.Runtime == agentproxy.NodeHelperRuntime {
		return shellQuote(helper.Interpreter) + " " + shellQuote(helper.Path) +
			" --stdio --protocol " + strconv.Itoa(protocol)
	}
	return shellQuote(helper.Path) + " --stdio --protocol " + strconv.Itoa(protocol)
}

type agentProxyManifest struct {
	Protocol  int    `json:"protocol"`
	Version   string `json:"version"`
	Runtime   string `json:"runtime"`
	SHA256    string `json:"sha256"`
	UpdatedAt string `json:"updated_at"`
}

// agentProxySource is what gets uploaded: the embedded script or an explicitly
// supplied helper file. The digest is computed once per process, because it is
// compared with the workspace manifest on every Turn.
type agentProxySource struct {
	runtime     string
	sha256      string
	extension   string
	interpreter string
	open        func() (io.ReadCloser, error)
}

// EnsureAgentProxyHelper makes sure a runnable helper exists and returns it.
//
// The selection order is part of the contract, and every rejected fallback is
// reported in the returned error rather than silently skipped:
//
//  1. AHA2_AGENT_PROXY_HELPER, when set, is uploaded and executed directly.
//  2. Otherwise the embedded Node script is used, but only after the workspace
//     proves it has a node binary: a node-script backend's shebang is a hard
//     guarantee, `command -v node` is the fallback.
//
// If neither can be prepared the caller logs this error and falls back to the
// reverse tunnel and then the LAN address.
func EnsureAgentProxyHelper(ctx context.Context, item domain.Workspace, workDir, taskID, version, backend string) (AgentProxyHelper, error) {
	if IsWindowsWorkspace(item) {
		return AgentProxyHelper{}, fmt.Errorf("Agent API stdio 代理暂不支持 Windows 工作区")
	}
	runner := RunnerFor(item)
	if override := strings.TrimSpace(os.Getenv(agentProxyHelperOverride)); override != "" {
		source, err := agentProxyExecSource(override)
		if err != nil {
			return AgentProxyHelper{}, fmt.Errorf("%s=%s：%w", agentProxyHelperOverride, override, err)
		}
		return ensureAgentProxy(ctx, item, runner, workDir, taskID, version, source)
	}
	interpreter, err := detectAgentProxyNode(ctx, runner, backend)
	if err != nil {
		return AgentProxyHelper{}, fmt.Errorf("Node helper 不可用：%w", err)
	}
	script, digest := agentproxy.NodeHelper()
	return ensureAgentProxy(ctx, item, runner, workDir, taskID, version, agentProxySource{
		runtime:     agentproxy.NodeHelperRuntime,
		sha256:      digest,
		extension:   ".js",
		interpreter: interpreter,
		open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(script)), nil
		},
	})
}

func ensureAgentProxy(ctx context.Context, item domain.Workspace, runner Runner, workDir, taskID, version string, source agentProxySource) (AgentProxyHelper, error) {
	targetName := agentProxyHelperName(version, source.sha256, source.extension)
	manifest := agentProxyManifest{
		Protocol:  agentproxy.Protocol,
		Version:   version,
		Runtime:   source.runtime,
		SHA256:    source.sha256,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	type candidate struct {
		dir  string
		note string
	}
	candidates := []candidate{
		{dir: JoinRemotePath(item, workDir, ahaContextDirectoryName, agentProxyShareDirectory, agentProxyDirectoryName)},
		// Inside the Task scratch on purpose: RemoveTaskScratch deletes that whole
		// directory when the Task closes, so a degraded fallback does not leave
		// one helper copy behind per Task.
		{dir: JoinRemotePath(item, workDir, ahaContextDirectoryName, taskID, scratchDirectoryName, agentProxyDirectoryName),
			note: "共享目录不可用，改用本 Task 的 scratch 目录（随 Task 收尾清理）"},
	}
	if tempDir, tempErr := agentProxyTempDirectory(ctx, runner); tempErr == nil {
		candidates = append(candidates, candidate{dir: tempDir, note: "工作区目录不可用，改用工作区临时目录"})
	}

	var failures []string
	for _, entry := range candidates {
		helperPath, err := ensureAgentProxyAt(ctx, runner, entry.dir, targetName, source, manifest)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s：%v", entry.dir, err))
			continue
		}
		if err := probeAgentProxyHelper(ctx, runner, helperPath, source); err != nil {
			failures = append(failures, fmt.Sprintf("%s：%v", helperPath, err))
			continue
		}
		note := entry.note
		if len(failures) > 0 {
			note = strings.Join(append([]string{note}, failures...), "；")
		}
		return AgentProxyHelper{
			Path: helperPath, Runtime: source.runtime,
			Interpreter: source.interpreter, Note: note,
		}, nil
	}
	return AgentProxyHelper{}, fmt.Errorf("无法在工作区放置 Agent API helper：%s", strings.Join(failures, "；"))
}

func agentProxyHelperName(version, digest, extension string) string {
	short := digest
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%s-%s-%s%s", agentProxyScriptPrefix, sanitizeAgentProxyVersion(version), short, extension)
}

func sanitizeAgentProxyVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "dev"
	}
	cleaned := strings.Map(func(character rune) rune {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9', character == '.', character == '-', character == '_':
			return character
		default:
			return '-'
		}
	}, value)
	cleaned = strings.Trim(cleaned, ".-")
	if cleaned == "" {
		return "dev"
	}
	return cleaned
}

var agentProxyExecSourceCache struct {
	sync.Mutex
	path   string
	size   int64
	mtime  int64
	digest string
}

func agentProxyExecSource(helperPath string) (agentProxySource, error) {
	info, err := os.Stat(helperPath)
	if err != nil {
		return agentProxySource{}, fmt.Errorf("读取 helper 源文件：%w", err)
	}
	if info.IsDir() {
		return agentProxySource{}, fmt.Errorf("helper 源文件是目录")
	}
	digest, err := agentProxyExecDigest(helperPath, info)
	if err != nil {
		return agentProxySource{}, err
	}
	return agentProxySource{
		runtime: agentProxyRuntimeExec, sha256: digest,
		open: func() (io.ReadCloser, error) { return os.Open(helperPath) },
	}, nil
}

func agentProxyExecDigest(helperPath string, info os.FileInfo) (string, error) {
	key := info.Size()
	modified := info.ModTime().UnixNano()
	agentProxyExecSourceCache.Lock()
	cached, cachedPath, cachedSize, cachedModified := agentProxyExecSourceCache.digest,
		agentProxyExecSourceCache.path, agentProxyExecSourceCache.size, agentProxyExecSourceCache.mtime
	agentProxyExecSourceCache.Unlock()
	if cached != "" && cachedPath == helperPath && cachedSize == key && cachedModified == modified {
		return cached, nil
	}
	file, err := os.Open(helperPath)
	if err != nil {
		return "", fmt.Errorf("打开 helper 源文件：%w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("计算 helper 哈希：%w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	agentProxyExecSourceCache.Lock()
	agentProxyExecSourceCache.path, agentProxyExecSourceCache.size = helperPath, key
	agentProxyExecSourceCache.mtime, agentProxyExecSourceCache.digest = modified, digest
	agentProxyExecSourceCache.Unlock()
	return digest, nil
}

// detectAgentProxyNode finds the node binary to run the embedded script with.
//
// A backend that is itself a Node script is the strongest signal: the workspace
// is already executing that interpreter, so its shebang is taken as a hard
// guarantee even when `node` is not on PATH. Otherwise the workspace is asked
// for `node` on PATH. The error names both attempts, because the caller turns it
// into the visible reason for leaving the stdio transport.
func detectAgentProxyNode(ctx context.Context, runner Runner, backend string) (string, error) {
	result, err := runner.Run(ctx, Command{
		Executable: "sh",
		Args:       []string{"-c", agentProxyNodeProbeScript, "aha2-agent-proxy-node", backend},
		Timeout:    15 * time.Second, OutputLimit: 512,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("探测工作区 node：%w", err)
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("探测脚本 exit %d", result.ExitCode)
		}
		return "", fmt.Errorf("%s", detail)
	}
	interpreter := strings.TrimSpace(result.Stdout)
	if interpreter == "" {
		return "", fmt.Errorf("探测脚本没有返回 node 路径")
	}
	return interpreter, nil
}

// agentProxyNodeProbeScript reads the backend's shebang first, then falls back
// to PATH. It prints the interpreter path on success and a reason on failure.
// The first argument after the script name becomes $0, so the backend is $1.
const agentProxyNodeProbeScript = `set -u
backend=$1

resolve() {
  command -v "$1" 2>/dev/null
}

node_from_shebang() {
  candidate=$1
  first=$(head -c 256 "$candidate" 2>/dev/null | head -n 1) || return 1
  case $first in
    '#!'*) ;;
    *) return 1 ;;
  esac
  line=${first#??}
  set -- $line
  [ $# -gt 0 ] || return 1
  interpreter=$1
  shift
  if [ "$(basename "$interpreter")" = env ]; then
    interpreter=
    while [ $# -gt 0 ]; do
      case $1 in
        -S|-i|-u) shift ;;
        -*|*=*) shift ;;
        *) interpreter=$1; break ;;
      esac
    done
  fi
  [ -n "$interpreter" ] || return 1
  case $(basename "$interpreter" 2>/dev/null) in
    node|nodejs) ;;
    *) return 1 ;;
  esac
  case $interpreter in
    */*) [ -x "$interpreter" ] || return 1
         printf '%s' "$interpreter"; return 0 ;;
    *) resolved=$(resolve "$interpreter" || true)
       [ -n "$resolved" ] || return 1
       printf '%s' "$resolved"; return 0 ;;
  esac
}

if [ -n "$backend" ]; then
  candidate=$backend
  case $candidate in
    */*) [ -f "$candidate" ] || candidate= ;;
    *) candidate=$(resolve "$backend" || true) ;;
  esac
  if [ -n "$candidate" ] && [ -f "$candidate" ]; then
    if found=$(node_from_shebang "$candidate"); then
      printf '%s' "$found"; exit 0
    fi
  fi
fi

if found=$(resolve node); then
  printf '%s' "$found"; exit 0
fi

if [ -n "$backend" ]; then
  echo "工作区里没有 node：backend $backend 不是 node 脚本，且 PATH 中找不到 node" >&2
else
  echo "工作区里没有 node：未提供 backend 命令，且 PATH 中找不到 node" >&2
fi
exit 3
`

func agentProxyTempDirectory(ctx context.Context, runner Runner) (string, error) {
	result, err := runner.Run(ctx, Command{
		Executable: "sh",
		Args:       []string{"-c", `printf '/tmp/aha2-agent-proxy-%s' "$(id -u)"`},
		Timeout:    15 * time.Second, OutputLimit: 256,
	}, nil)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(result.Stdout)
	if result.ExitCode != 0 || value == "" {
		return "", fmt.Errorf("无法确定工作区临时目录")
	}
	return value, nil
}

// ensureAgentProxyAt materialises the helper in one directory and returns its
// path. Reuse is decided by the manifest plus a fresh hash of the file itself:
// a manifest alone is a claim, and a half-written or replaced file must not be
// executed just because the manifest still names it.
func ensureAgentProxyAt(
	ctx context.Context,
	runner Runner,
	directory, targetName string,
	source agentProxySource,
	manifest agentProxyManifest,
) (string, error) {
	target := path.Join(directory, targetName)
	previous, _ := readAgentProxyManifest(ctx, runner, directory)
	if previous != nil && previous.Protocol == manifest.Protocol && previous.Version == manifest.Version &&
		previous.Runtime == manifest.Runtime && previous.SHA256 == manifest.SHA256 {
		if hash, err := agentProxyRemoteHash(ctx, runner, target); err == nil && hash == manifest.SHA256 {
			return target, nil
		}
	}

	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	file, err := source.open()
	if err != nil {
		return "", fmt.Errorf("打开 helper 源文件：%w", err)
	}
	defer file.Close()
	streamer, ok := runner.(*SSHRunner)
	if !ok {
		return "", fmt.Errorf("SSH 传输不支持上传 helper")
	}
	// The generation being replaced is kept: an already running helper may still
	// execute that inode, and keeping it costs one file.
	previousPath := ""
	if previous != nil && previous.SHA256 != "" {
		if name := agentProxyHelperName(previous.Version, previous.SHA256, agentProxyExtension(previous.Runtime)); name != targetName {
			previousPath = path.Join(directory, name)
		}
	}
	// The first argument after `sh -c <script>` becomes $0, so the script's
	// own name is passed explicitly and the five parameters follow it.
	result, err := streamer.runScriptWithStdin(ctx, agentProxyUploadScript,
		[]string{"aha2-agent-proxy-upload", directory, target, manifest.SHA256, string(encoded), previousPath},
		file, 5*time.Minute, 8192)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("%s", remoteCommandDetail(result))
	}
	return target, nil
}

func agentProxyExtension(runtime string) string {
	if runtime == agentproxy.NodeHelperRuntime {
		return ".js"
	}
	return ""
}

// agentProxyUploadScript writes the helper from stdin, verifies its digest,
// renames it into place, and only then replaces the manifest.
//
// The order matters under concurrency: several Tasks may upload the same
// version at once, and a reader must never see a manifest that promises a file
// which is not there yet. Renames are atomic, so a helper already running from
// the old inode is unaffected.
const agentProxyUploadScript = `set -eu
dir=$1
target=$2
want=$3
manifest=$4
previous=$5
umask 077
if [ -L "$dir" ]; then
  echo "AHA2_AGENT_PROXY_DIR_SYMLINK: $dir" >&2
  exit 6
fi
mkdir -p "$dir"
if [ ! -O "$dir" ]; then
  echo "AHA2_AGENT_PROXY_DIR_NOT_OWNED: $dir" >&2
  exit 6
fi
tmp=$(mktemp "$dir/.upload.XXXXXX" 2>/dev/null) || tmp="$dir/.upload.$$"
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"
chmod 700 "$tmp"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp" | awk '{print $1}')
else
  got=$(shasum -a 256 "$tmp" | awk '{print $1}')
fi
if [ "$got" != "$want" ]; then
  echo "AHA2_AGENT_PROXY_SHA_MISMATCH got=$got want=$want" >&2
  exit 4
fi
mv -f "$tmp" "$target"
# Keep the current helper and the generation the previous manifest named; drop
# anything older so a workspace does not accumulate one copy per version. The
# glob also removes a prebuilt helper left by an older AHA2 release.
for candidate in "$dir"/aha2-agent-proxy-*; do
  [ -f "$candidate" ] || continue
  [ "$candidate" = "$target" ] && continue
  [ -n "$previous" ] && [ "$candidate" = "$previous" ] && continue
  rm -f "$candidate"
done
manifest_tmp=$(mktemp "$dir/.manifest.XXXXXX" 2>/dev/null) || manifest_tmp="$dir/.manifest.$$"
printf '%s' "$manifest" > "$manifest_tmp"
mv -f "$manifest_tmp" "$dir/manifest.json"
`

// readAgentProxyManifest reads the manifest with a size check on the workspace
// side. Reading it unbounded would let a workspace hand the control plane a
// buffer of its choosing at the cost of one large file.
func readAgentProxyManifest(ctx context.Context, runner Runner, directory string) (*agentProxyManifest, error) {
	result, err := runner.Run(ctx, Command{
		Executable: "sh",
		Args: []string{"-c", `set -eu
target=$1
if [ ! -e "$target" ]; then exit 3; fi
bytes=$(wc -c < "$target") || exit 1
if [ "$bytes" -gt ` + strconv.Itoa(agentProxyManifestLimit) + ` ]; then
  printf 'manifest too large: %s bytes\n' "$bytes" >&2
  exit 4
fi
cat "$target"`, "aha-agent-proxy-manifest", path.Join(directory, agentProxyManifestName)},
		Timeout: 20 * time.Second, OutputLimit: agentProxyManifestLimit,
	}, nil)
	if err != nil {
		return nil, err
	}
	if result.ExitCode == 3 {
		return nil, nil
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("%s", remoteCommandDetail(result))
	}
	var manifest agentProxyManifest
	if err := json.Unmarshal([]byte(result.Stdout), &manifest); err != nil {
		return nil, fmt.Errorf("解析 manifest：%w", err)
	}
	return &manifest, nil
}

func agentProxyRemoteHash(ctx context.Context, runner Runner, helperPath string) (string, error) {
	result, err := runner.Run(ctx, Command{
		Executable: "sh",
		Args: []string{"-c", `set -eu
if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
else shasum -a 256 "$1" | awk '{print $1}'
fi`, "aha-agent-proxy-hash", helperPath},
		Timeout: 30 * time.Second, OutputLimit: 256,
	}, nil)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("%s", remoteCommandDetail(result))
	}
	return strings.TrimSpace(result.Stdout), nil
}

// probeAgentProxyHelper proves the helper actually runs on this workspace.
// For the Node script this runs the detected interpreter, so a workspace whose
// `node` is a broken shim or is mounted noexec is rejected here rather than at
// the first Agent API request.
func probeAgentProxyHelper(ctx context.Context, runner Runner, helperPath string, source agentProxySource) error {
	command := Command{Executable: helperPath, Args: []string{"version"}, Timeout: 15 * time.Second, OutputLimit: 512}
	want := "aha2"
	if source.runtime == agentproxy.NodeHelperRuntime {
		command = Command{Executable: source.interpreter, Args: []string{helperPath, "--version"}, Timeout: 15 * time.Second, OutputLimit: 512}
		want = "protocol=" + strconv.Itoa(agentproxy.Protocol)
	}
	result, err := runner.Run(ctx, command, nil)
	if err != nil {
		return fmt.Errorf("执行 helper 失败（可能是 noexec 挂载）：%w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("helper 无法执行（exit %d）：%s", result.ExitCode, remoteCommandDetail(result))
	}
	if !strings.Contains(result.Stdout, want) {
		return fmt.Errorf("helper 输出异常：%q", strings.TrimSpace(result.Stdout))
	}
	return nil
}
