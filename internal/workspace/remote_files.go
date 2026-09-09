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

func remoteCommandDetail(result Result) string {
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = fmt.Sprintf("exit code %d", result.ExitCode)
	}
	return detail
}
