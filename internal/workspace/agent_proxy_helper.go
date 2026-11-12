package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentproxy"
	"github.com/ChinaKai/AHA2/internal/domain"
)

// The helper is this same aha2 binary: the hidden "agent-proxy" subcommand is
// the workspace side of the tunnel. Materialising it under the workspace's
// shared context means many Tasks reuse one upload, and a running helper keeps
// working if a newer upload replaces the file (POSIX rename keeps the inode).

const (
	agentProxyShareDirectory = "share"
	agentProxyDirectoryName  = "agent-proxy"
	agentProxyManifestName   = "manifest.json"
	agentProxyBinaryPrefix   = "aha2-agent-proxy"
	// agentProxyManifestLimit bounds the manifest read. A real manifest is a
	// few hundred bytes; anything larger is not one, and must be refused before
	// it is buffered rather than parsed.
	agentProxyManifestLimit = 4096
	// agentProxyHelperOverride points at a prebuilt Linux helper. It exists for
	// cross-architecture hosts (a Windows AHA cannot copy its own aha2.exe to a
	// Linux workspace) and for tests.
	agentProxyHelperOverride = "AHA2_AGENT_PROXY_HELPER"
)

// AgentProxyHelper is a materialised helper executable on a workspace.
type AgentProxyHelper struct {
	Path string
	// Note explains a fallback location, so the Turn log says why the shared
	// directory was not used instead of leaving it to be guessed.
	Note string
}

type agentProxyManifest struct {
	Protocol  int    `json:"protocol"`
	Version   string `json:"version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	SHA256    string `json:"sha256"`
	UpdatedAt string `json:"updated_at"`
}

// EnsureAgentProxyHelper makes sure a runnable helper exists and returns its
// workspace path.
//
// The shared directory is tried first. A workspace that cannot be written to,
// or that mounts the directory noexec, falls back to the per-Task context and
// then to the system temp directory; each fallback is reported in Note rather
// than silently degrading.
func EnsureAgentProxyHelper(ctx context.Context, item domain.Workspace, workDir, taskID, version string) (AgentProxyHelper, error) {
	if IsWindowsWorkspace(item) {
		return AgentProxyHelper{}, fmt.Errorf("Agent API stdio 代理暂不支持 Windows 工作区")
	}
	source, err := agentProxySourceBinary()
	if err != nil {
		return AgentProxyHelper{}, err
	}
	runner := RunnerFor(item)
	remoteOS, remoteArch, err := agentProxyRemotePlatform(ctx, runner)
	if err != nil {
		return AgentProxyHelper{}, err
	}
	// An explicit helper is taken at its word: the operator is the only one who
	// knows what they built, and the `version` probe below still proves it runs
	// on this workspace. Comparing it to the AHA process platform is what made
	// the override useless for a Windows host and a Linux workspace.
	if !source.explicit && (remoteOS != source.os || remoteArch != source.arch) {
		return AgentProxyHelper{}, fmt.Errorf(
			"helper 架构不匹配：本机 %s/%s，工作区 %s/%s；可通过 %s 指定预编译的 Linux helper",
			source.os, source.arch, remoteOS, remoteArch, agentProxyHelperOverride,
		)
	}
	targetName := agentProxyBinaryName(version, source.sha256)
	manifest := agentProxyManifest{
		Protocol:  agentproxy.Protocol,
		Version:   version,
		OS:        remoteOS,
		Arch:      remoteArch,
		SHA256:    source.sha256,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	type candidate struct {
		dir  string
		note string
	}
	candidates := []candidate{
		{dir: JoinRemotePath(item, workDir, ahaContextDirectoryName, agentProxyShareDirectory, agentProxyDirectoryName)},
		{dir: JoinRemotePath(item, workDir, ahaContextDirectoryName, taskID, agentProxyDirectoryName),
			note: "共享目录不可用，改用本 Task 的上下文目录"},
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
		if err := probeAgentProxyHelper(ctx, runner, helperPath); err != nil {
			failures = append(failures, fmt.Sprintf("%s：%v", helperPath, err))
			continue
		}
		note := entry.note
		if len(failures) > 0 {
			note = strings.Join(append([]string{note}, failures...), "；")
		}
		return AgentProxyHelper{Path: helperPath, Note: note}, nil
	}
	return AgentProxyHelper{}, fmt.Errorf("无法在工作区放置 Agent API helper：%s", strings.Join(failures, "；"))
}

func agentProxyBinaryName(version, digest string) string {
	short := digest
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%s-%s-%s", agentProxyBinaryPrefix, sanitizeAgentProxyVersion(version), short)
}

type agentProxySource struct {
	path   string
	sha256 string
	os     string
	arch   string
	size   int64
	// explicit marks a binary supplied through AHA2_AGENT_PROXY_HELPER. Its
	// platform is whatever the operator built, so it is recorded as the
	// workspace's platform instead of being compared with this process.
	explicit bool
}

var agentProxySourceCache struct {
	sync.Mutex
	key    string
	source agentProxySource
}

// agentProxySourceBinary hashes the helper binary once per process. The upload
// itself re-reads the file; only the digest is worth caching, and it must not be
// recomputed for every Turn of every Task.
func agentProxySourceBinary() (agentProxySource, error) {
	path := strings.TrimSpace(os.Getenv(agentProxyHelperOverride))
	explicit := path != ""
	if path == "" {
		executable, err := os.Executable()
		if err != nil {
			return agentProxySource{}, fmt.Errorf("定位当前 aha2 可执行文件：%w", err)
		}
		path = executable
	}
	info, err := os.Stat(path)
	if err != nil {
		return agentProxySource{}, fmt.Errorf("读取 helper 源文件：%w", err)
	}
	// The key includes whether the binary was supplied explicitly: the same path
	// read through the override and as the running executable carries different
	// platform rules.
	key := fmt.Sprintf("%s|%d|%d|%t", path, info.Size(), info.ModTime().UnixNano(), explicit)
	agentProxySourceCache.Lock()
	cached, cachedKey := agentProxySourceCache.source, agentProxySourceCache.key
	agentProxySourceCache.Unlock()
	if cachedKey == key {
		return cached, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return agentProxySource{}, fmt.Errorf("打开 helper 源文件：%w", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return agentProxySource{}, fmt.Errorf("计算 helper 哈希：%w", err)
	}
	source := agentProxySource{
		path: path, sha256: hex.EncodeToString(digest.Sum(nil)),
		os: runtime.GOOS, arch: runtime.GOARCH, size: info.Size(), explicit: explicit,
	}
	agentProxySourceCache.Lock()
	agentProxySourceCache.key, agentProxySourceCache.source = key, source
	agentProxySourceCache.Unlock()
	return source, nil
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

func agentProxyRemotePlatform(ctx context.Context, runner Runner) (string, string, error) {
	if detect, ok := runner.(interface{ DetectedPlatform() string }); ok {
		if value := strings.TrimSpace(detect.DetectedPlatform()); value != "" {
			if osName, arch, ok := parseAgentProxyPlatform(value); ok {
				return osName, arch, nil
			}
		}
	}
	result, err := runner.Run(ctx, Command{
		Executable: "sh",
		Args:       []string{"-c", `printf '%s %s' "$(uname -s)" "$(uname -m)"`},
		Timeout:    15 * time.Second, OutputLimit: 512,
	}, nil)
	if err != nil {
		return "", "", fmt.Errorf("探测工作区平台：%w", err)
	}
	if result.ExitCode != 0 {
		return "", "", fmt.Errorf("探测工作区平台失败：%s", remoteCommandDetail(result))
	}
	osName, arch, ok := parseAgentProxyPlatform(result.Stdout)
	if !ok {
		return "", "", fmt.Errorf("无法识别工作区平台 %q", strings.TrimSpace(result.Stdout))
	}
	return osName, arch, nil
}

// parseAgentProxyPlatform accepts the detected "Linux 6.8.0 x86_64" form, the
// plain "Linux x86_64" probe output, and the stored "linux/arm64" form.
func parseAgentProxyPlatform(value string) (string, string, bool) {
	fields := strings.Fields(strings.ToLower(value))
	if len(fields) == 0 {
		return "", "", false
	}
	osName, arch := normalizeAgentProxyOS(fields[0]), ""
	if osName == "" {
		return "", "", false
	}
	// Stored platform strings are "os/arch"; uname output never has a slash.
	if _, rest, found := strings.Cut(fields[0], "/"); found {
		arch = normalizeAgentProxyArch(rest)
	}
	if arch == "" {
		for _, field := range fields[1:] {
			if arch = normalizeAgentProxyArch(field); arch != "" {
				break
			}
		}
	}
	if arch == "" {
		return "", "", false
	}
	return osName, arch, true
}

func normalizeAgentProxyOS(value string) string {
	switch {
	case strings.HasPrefix(value, "linux"):
		return "linux"
	case strings.HasPrefix(value, "darwin"):
		return "darwin"
	default:
		return ""
	}
}

func normalizeAgentProxyArch(value string) string {
	switch value {
	case "x86_64", "amd64", "x64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return ""
	}
}

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
// a manifest alone is a claim, and a half-written or replaced binary must not be
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
		previous.OS == manifest.OS && previous.Arch == manifest.Arch && previous.SHA256 == manifest.SHA256 {
		if hash, err := agentProxyRemoteHash(ctx, runner, target); err == nil && hash == manifest.SHA256 {
			return target, nil
		}
	}

	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	file, err := os.Open(source.path)
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
		if name := agentProxyBinaryName(previous.Version, previous.SHA256); name != targetName {
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
# Keep the current binary and the generation the previous manifest named; drop
# anything older so a workspace does not accumulate one copy per version.
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

func probeAgentProxyHelper(ctx context.Context, runner Runner, helperPath string) error {
	result, err := runner.Run(ctx, Command{
		Executable: helperPath, Args: []string{"version"}, Timeout: 15 * time.Second, OutputLimit: 512,
	}, nil)
	if err != nil {
		return fmt.Errorf("执行 helper 失败（可能是 noexec 挂载）：%w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("helper 无法执行（exit %d）：%s", result.ExitCode, remoteCommandDetail(result))
	}
	if !strings.Contains(result.Stdout, "aha2") {
		return fmt.Errorf("helper 输出异常：%q", strings.TrimSpace(result.Stdout))
	}
	return nil
}
