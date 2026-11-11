package workspace

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func IsWindowsWorkspace(item domain.Workspace) bool {
	return item.Transport == "ssh" && isWindowsPlatform(item.Platform)
}

func IsWindowsRunner(runner Runner) bool {
	platformRunner, ok := runner.(interface{ DetectedPlatform() string })
	return ok && isWindowsPlatform(platformRunner.DetectedPlatform())
}

func remotePathIsAbs(item domain.Workspace, value string) bool {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	if IsWindowsWorkspace(item) {
		return strings.HasPrefix(value, "//") || len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && value[2] == '/'
	}
	return strings.HasPrefix(value, "/")
}

func windowsRemotePathDir(value string) string {
	value = strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(value, "//") {
		return `\\` + strings.ReplaceAll(path.Dir(strings.TrimPrefix(value, "//")), "/", `\`)
	}
	return strings.ReplaceAll(path.Dir(value), "/", `\`)
}

func windowsRemotePathJoin(root, value string) string {
	root = strings.ReplaceAll(root, `\`, "/")
	if strings.HasPrefix(root, "//") {
		return `\\` + strings.ReplaceAll(path.Join(strings.TrimPrefix(root, "//"), value), "/", `\`)
	}
	return strings.ReplaceAll(path.Join(root, value), "/", `\`)
}

func JoinRemotePath(item domain.Workspace, root string, values ...string) string {
	for _, value := range values {
		if IsWindowsWorkspace(item) {
			root = windowsRemotePathJoin(root, value)
		} else {
			root = path.Join(root, value)
		}
	}
	return root
}

func EnsureRemoteDirectory(ctx context.Context, runner Runner, directory string) error {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				`param([string]$Directory) [IO.Directory]::CreateDirectory($Directory) | Out-Null`, directory},
			Timeout: 20 * time.Second,
		}
	} else {
		command = Command{Executable: "mkdir", Args: []string{"-p", directory}, Timeout: 20 * time.Second}
	}
	result, err := runner.Run(ctx, command, nil)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("create remote directory: %s", remoteCommandDetail(result))
	}
	return nil
}

func WriteRemoteTextFile(ctx context.Context, runner Runner, target, content string) error {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `param([string]$Target)
$parent = [IO.Path]::GetDirectoryName($Target)
if ($parent) { [IO.Directory]::CreateDirectory($parent) | Out-Null }
$inputStream = [Console]::OpenStandardInput()
$outputStream = [IO.File]::Open($Target, [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::None)
try { $inputStream.CopyTo($outputStream) } finally { $outputStream.Dispose() }`, target},
			Stdin: content, Timeout: 20 * time.Second,
		}
	} else {
		command = Command{
			Executable: "sh", Args: []string{"-c", `umask 077; mkdir -p "$(dirname "$1")"; cat > "$1"`, "aha-write-file", target},
			Stdin: content, Timeout: 20 * time.Second,
		}
	}
	result, err := runner.Run(ctx, command, nil)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("write remote file: %s", remoteCommandDetail(result))
	}
	return nil
}

// RemoveRemoteFile deletes one file on the execution host. It is used for
// short-lived generated configuration that may carry credentials, so removal
// must happen on the host where the file was written rather than locally.
func RemoveRemoteFile(ctx context.Context, runner Runner, target string) error {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				`param([string]$Target) Remove-Item -LiteralPath $Target -Force -ErrorAction SilentlyContinue`, target},
			Timeout: 20 * time.Second,
		}
	} else {
		command = Command{
			Executable: "sh", Args: []string{"-c", `rm -f -- "$1"`, "aha-remove-file", target},
			Timeout: 20 * time.Second,
		}
	}
	result, err := runner.Run(ctx, command, nil)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("remove remote file: %s", remoteCommandDetail(result))
	}
	return nil
}

func ReadRemoteTextFile(ctx context.Context, runner Runner, target string) (string, error) {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				`param([string]$Target) [Console]::Out.Write([IO.File]::ReadAllText($Target))`, target},
			Timeout: 20 * time.Second,
		}
	} else {
		command = Command{Executable: "cat", Args: []string{target}, Timeout: 20 * time.Second}
	}
	result, err := runner.Run(ctx, command, nil)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("read remote file: %s", remoteCommandDetail(result))
	}
	return result.Stdout, nil
}

// ReadRemoteTextFileIfExists reads a file on the execution host and reports
// whether it existed. Unlike ReadRemoteTextFile, a missing file is distinct
// from a read failure, so a caller that is about to overwrite a generated
// config never treats a permission or transport error as "no original file".
func ReadRemoteTextFileIfExists(ctx context.Context, runner Runner, target string) (string, bool, error) {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `param([string]$Target)
if (-not (Test-Path -LiteralPath $Target -PathType Leaf)) { exit 3 }
[Console]::Out.Write([IO.File]::ReadAllText($Target))`, target},
			Timeout: 20 * time.Second,
		}
	} else {
		command = Command{
			Executable: "sh",
			Args:       []string{"-c", `if [ ! -e "$1" ]; then exit 3; fi; cat -- "$1"`, "aha-read-file", target},
			Timeout:    20 * time.Second,
		}
	}
	result, err := runner.Run(ctx, command, nil)
	if err != nil {
		return "", false, err
	}
	if result.ExitCode == 3 {
		return "", false, nil
	}
	if result.ExitCode != 0 {
		return "", false, fmt.Errorf("read remote file: %s", remoteCommandDetail(result))
	}
	return result.Stdout, true, nil
}

// WriteRemoteTextFileAtomic writes content through a temporary file in the
// target directory and renames it into place. A crash can therefore leave the
// old complete file or the new complete file, but never a truncated one.
// POSIX uses umask 077 and an explicit chmod 600; Windows keeps the target
// ACL of the owning user.
func WriteRemoteTextFileAtomic(ctx context.Context, runner Runner, target, content string) error {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `param([string]$Target)
$parent = [IO.Path]::GetDirectoryName($Target)
if ($parent) { [IO.Directory]::CreateDirectory($parent) | Out-Null }
$tmp = Join-Path $parent ('.aha2-tmp-' + [Guid]::NewGuid().ToString('N'))
try {
  $inputStream = [Console]::OpenStandardInput()
  $outputStream = [IO.File]::Open($tmp, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
  try { $inputStream.CopyTo($outputStream) } finally { $outputStream.Dispose() }
  Move-Item -LiteralPath $tmp -Destination $Target -Force
} catch {
  Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
  throw
}`, target},
			Stdin: content, Timeout: 20 * time.Second,
		}
	} else {
		command = Command{
			Executable: "sh",
			Args: []string{"-c", `umask 077
dir=$(dirname "$1")
mkdir -p "$dir" || exit 1
tmp=$(mktemp "$dir/.aha2-tmp.XXXXXX") || exit 1
trap 'rm -f -- "$tmp"' EXIT
cat > "$tmp" || exit 1
chmod 600 "$tmp" || exit 1
mv -f "$tmp" "$1"`, "aha-write-atomic", target},
			Stdin: content, Timeout: 20 * time.Second,
		}
	}
	result, err := runner.Run(ctx, command, nil)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("write remote file: %s", remoteCommandDetail(result))
	}
	return nil
}

func remoteCommandDetail(result Result) string {
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = fmt.Sprintf("exit code %d", result.ExitCode)
	}
	return detail
}
