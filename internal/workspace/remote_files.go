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

// windowsScript embeds the target into the script as a single-quoted
// PowerShell literal. powershell.exe -Command does not bind extra argv
// elements to a param() block; it appends them to the command text, so passing
// the target separately silently leaves $Target empty (and may try to execute
// the path as a command).
func windowsScript(template, target string) string {
	return strings.ReplaceAll(template, "__AHA2_TARGET__", psQuote(target))
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

const windowsEnsureDirectoryScript = `$Target = __AHA2_TARGET__
[IO.Directory]::CreateDirectory($Target) | Out-Null`

func EnsureRemoteDirectory(ctx context.Context, runner Runner, directory string) error {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				windowsScript(windowsEnsureDirectoryScript, directory)},
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

const windowsWriteFileScript = `$Target = __AHA2_TARGET__
$parent = [IO.Path]::GetDirectoryName($Target)
if ($parent) { [IO.Directory]::CreateDirectory($parent) | Out-Null }
$inputStream = [Console]::OpenStandardInput()
$outputStream = [IO.File]::Open($Target, [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::None)
try { $inputStream.CopyTo($outputStream) } finally { $outputStream.Dispose() }`

func WriteRemoteTextFile(ctx context.Context, runner Runner, target, content string) error {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				windowsScript(windowsWriteFileScript, target)},
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

const windowsRemoveFileScript = `$Target = __AHA2_TARGET__
Remove-Item -LiteralPath $Target -Force -ErrorAction SilentlyContinue`

// RemoveRemoteFile deletes one file on the execution host. It is used for
// short-lived generated configuration that may carry credentials, so removal
// must happen on the host where the file was written rather than locally.
func RemoveRemoteFile(ctx context.Context, runner Runner, target string) error {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				windowsScript(windowsRemoveFileScript, target)},
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

const windowsReadFileScript = `$Target = __AHA2_TARGET__
[Console]::Out.Write([IO.File]::ReadAllText($Target))`

func ReadRemoteTextFile(ctx context.Context, runner Runner, target string) (string, error) {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				windowsScript(windowsReadFileScript, target)},
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

// The Windows scripts are kept as package constants so the test suite can run
// the exact production script against real PowerShell, instead of mocking the
// exit code. Only a definite FileNotFoundException or
// DirectoryNotFoundException is reported as "missing" (exit 3). Every other
// failure, including an unreachable UNC path or a sharing violation, exits 1
// with the exception text so a caller never mistakes it for an absent file.
const windowsReadFileIfExistsTemplate = `$Target = __AHA2_TARGET__
try {
  [Console]::Out.Write([IO.File]::ReadAllText($Target))
} catch [System.IO.FileNotFoundException] {
  exit 3
} catch [System.IO.DirectoryNotFoundException] {
  exit 3
} catch {
  [Console]::Error.Write($_.Exception.Message)
  exit 1
}`

func windowsReadFileIfExistsScript(target string) string {
	return windowsScript(windowsReadFileIfExistsTemplate, target)
}

// The temporary name is deterministic (<target>.tmp) and removed before every
// write, so a process killed before the rename cannot leave a random secret
// file that the next run does not know about.
const windowsWriteFileAtomicTemplate = `$Target = __AHA2_TARGET__
$parent = [IO.Path]::GetDirectoryName($Target)
if ($parent) { [IO.Directory]::CreateDirectory($parent) | Out-Null }
$tmp = $Target + '.tmp'
Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
try {
  $inputStream = [Console]::OpenStandardInput()
  $outputStream = [IO.File]::Open($tmp, [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::None)
  try { $inputStream.CopyTo($outputStream) } finally { $outputStream.Dispose() }
  Move-Item -LiteralPath $tmp -Destination $Target -Force
} catch {
  Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
  throw
}`

func windowsWriteFileAtomicScript(target string) string {
	return windowsScript(windowsWriteFileAtomicTemplate, target)
}

const posixReadFileIfExistsScript = `if [ ! -e "$1" ]; then
  dir=$(dirname "$1")
  if [ -d "$dir" ] && [ ! -x "$dir" ]; then
    printf '%s\n' "permission denied: $1" >&2
    exit 1
  fi
  exit 3
fi
cat "$1"`

const posixWriteFileAtomicScript = `umask 077
tmp="$1.tmp"
rm -f -- "$tmp" || exit 1
dir=$(dirname "$1")
mkdir -p "$dir" || exit 1
trap 'rm -f -- "$tmp"' EXIT
cat > "$tmp" || exit 1
chmod 600 "$tmp" || exit 1
mv -f "$tmp" "$1"`

// ReadRemoteTextFileIfExists reads a file on the execution host and reports
// whether it existed. Unlike ReadRemoteTextFile, a missing file is distinct
// from a read failure, so a caller that is about to overwrite a generated
// config never treats a permission or transport error as "no original file".
func ReadRemoteTextFileIfExists(ctx context.Context, runner Runner, target string) (string, bool, error) {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				windowsReadFileIfExistsScript(target)},
			Timeout: 20 * time.Second,
		}
	} else {
		command = Command{
			Executable: "sh",
			Args:       []string{"-c", posixReadFileIfExistsScript, "aha-read-file", target},
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

// WriteRemoteTextFileAtomic writes content through a deterministic temporary
// file in the target directory and renames it into place. A crash can
// therefore leave the old complete file or the new complete file, but never a
// truncated one; a stale <target>.tmp is removed before the next write.
// POSIX uses umask 077 and an explicit chmod 600; Windows keeps the target
// ACL of the owning user.
func WriteRemoteTextFileAtomic(ctx context.Context, runner Runner, target, content string) error {
	var command Command
	if IsWindowsRunner(runner) {
		command = Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				windowsWriteFileAtomicScript(target)},
			Stdin: content, Timeout: 20 * time.Second,
		}
	} else {
		command = Command{
			Executable: "sh",
			Args:       []string{"-c", posixWriteFileAtomicScript, "aha-write-atomic", target},
			Stdin:      content, Timeout: 20 * time.Second,
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
