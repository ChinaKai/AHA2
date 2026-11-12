package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	ahaContextDirectoryName = ".aha2-context"
	scratchDirectoryName    = "scratch"
)

// TaskScratchDir returns the Task-level scratch root:
// <task-workdir>/.aha2-context/<task-id>/scratch. Task IDs are validated as a
// single path segment so a malformed identifier can never point creation or
// cleanup outside the Task context directory.
func TaskScratchDir(item domain.Workspace, workDir, taskID string) (string, error) {
	if err := validateScratchSegment(taskID, "task"); err != nil {
		return "", err
	}
	return scratchPath(item, workDir, taskID)
}

// AgentScratchDir returns the only writable Context location for one Agent:
// <task-workdir>/.aha2-context/<task-id>/scratch/<agent-id>. Sibling <agent-id>
// and shared-* directories remain read-only AHA-generated Context.
func AgentScratchDir(item domain.Workspace, workDir, taskID, agentID string) (string, error) {
	if err := validateScratchSegment(agentID, "agent"); err != nil {
		return "", err
	}
	root, err := TaskScratchDir(item, workDir, taskID)
	if err != nil {
		return "", err
	}
	if item.Transport == "native" {
		return filepath.Join(root, agentID), nil
	}
	return JoinRemotePath(item, root, agentID), nil
}

func scratchPath(item domain.Workspace, workDir, taskID string) (string, error) {
	if strings.TrimSpace(workDir) == "" {
		return "", fmt.Errorf("task scratch: task workdir is empty")
	}
	if item.Transport == "native" {
		return filepath.Join(filepath.Clean(workDir), ahaContextDirectoryName, taskID, scratchDirectoryName), nil
	}
	return JoinRemotePath(item, workDir, ahaContextDirectoryName, taskID, scratchDirectoryName), nil
}

func validateScratchSegment(value, kind string) error {
	if value == "" || value == "." || value == ".." {
		return fmt.Errorf("task scratch: invalid %s id %q", kind, value)
	}
	for _, character := range value {
		if character == '/' || character == '\\' || character == ':' ||
			character < 0x20 || character == 0x7f {
			return fmt.Errorf("task scratch: invalid %s id %q", kind, value)
		}
	}
	return nil
}

// EnsureScratchDir creates the Agent scratch directory with owner-only
// permissions and returns its host path. It is idempotent: an existing
// directory is tightened to 0700 rather than recreated, so files kept across
// Turns survive.
func EnsureScratchDir(ctx context.Context, item domain.Workspace, workDir, taskID, agentID string) (string, error) {
	directory, err := AgentScratchDir(item, workDir, taskID, agentID)
	if err != nil {
		return "", err
	}
	if item.Transport == "native" {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return "", fmt.Errorf("create task scratch: %w", err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(directory, 0o700); err != nil {
				return "", fmt.Errorf("secure task scratch: %w", err)
			}
		}
		return directory, nil
	}
	runner := retryRemoteContextRunner(item, RunnerFor(item))
	if err := ensureRemoteScratchDir(ctx, item, runner, directory); err != nil {
		return "", fmt.Errorf("create task scratch: %w", err)
	}
	return directory, nil
}

func ensureRemoteScratchDir(ctx context.Context, item domain.Workspace, runner Runner, directory string) error {
	if IsWindowsWorkspace(item) {
		result, err := runner.Run(ctx, Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				windowsScript(windowsEnsureDirectoryScript, directory)},
			Timeout: 30 * time.Second,
		}, nil)
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("create remote scratch: %s", remoteCommandDetail(result))
		}
		return nil
	}
	result, err := runner.Run(ctx, Command{
		Executable: "sh",
		Args: []string{"-c", `set -eu; umask 077; mkdir -p -- "$1"; chmod 700 -- "$1"`,
			"aha-scratch-create", directory},
		Timeout: 30 * time.Second,
	}, nil)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("create remote scratch: %s", remoteCommandDetail(result))
	}
	return nil
}

// RemoveTaskScratch deletes exactly <task-workdir>/.aha2-context/<task-id>/scratch.
// It refuses invalid identifiers and resolved paths that leave the Task context
// directory, so a symlinked .aha2-context or a malformed Task ID cannot delete
// workspace files or user data.
func RemoveTaskScratch(ctx context.Context, item domain.Workspace, workDir, taskID string) error {
	directory, err := TaskScratchDir(item, workDir, taskID)
	if err != nil {
		return err
	}
	if item.Transport == "native" {
		return removeLocalTaskScratch(workDir, taskID, directory)
	}
	runner := retryRemoteContextRunner(item, RunnerFor(item))
	return removeRemoteTaskScratch(ctx, item, runner, workDir, taskID)
}

func removeLocalTaskScratch(workDir, taskID, directory string) error {
	absoluteWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("task scratch cleanup: %w", err)
	}
	absoluteScratch, err := filepath.Abs(directory)
	if err != nil {
		return fmt.Errorf("task scratch cleanup: %w", err)
	}
	expected := filepath.Join(absoluteWorkDir, ahaContextDirectoryName, taskID, scratchDirectoryName)
	if !sameLocalPath(absoluteScratch, expected) {
		return fmt.Errorf("task scratch cleanup: refusing to remove path outside Task boundary: %s", directory)
	}
	info, err := os.Lstat(absoluteScratch)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("task scratch cleanup: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("task scratch cleanup: refusing to remove non-directory: %s", directory)
	}
	// Resolve both sides before deleting: a symlinked .aha2-context must not
	// redirect RemoveAll into user data.
	realWorkDir, err := filepath.EvalSymlinks(absoluteWorkDir)
	if err != nil {
		return fmt.Errorf("task scratch cleanup: resolve workdir: %w", err)
	}
	realScratch, err := filepath.EvalSymlinks(absoluteScratch)
	if err != nil {
		return fmt.Errorf("task scratch cleanup: resolve scratch: %w", err)
	}
	if !sameLocalPath(realScratch, filepath.Join(realWorkDir, ahaContextDirectoryName, taskID, scratchDirectoryName)) {
		return fmt.Errorf("task scratch cleanup: refusing to remove path outside Task boundary: %s", realScratch)
	}
	return RemoveAllLongPath(realScratch)
}

func sameLocalPath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func removeRemoteTaskScratch(ctx context.Context, item domain.Workspace, runner Runner, workDir, taskID string) error {
	if IsWindowsWorkspace(item) {
		script := strings.NewReplacer(
			"__AHA2_TARGET__", psQuote(workDir),
			"__AHA2_TASK__", psQuote(taskID),
		).Replace(windowsRemoveScratchScript)
		result, err := runner.Run(ctx, Command{
			Executable: "powershell.exe",
			Args:       []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script},
			Timeout:    30 * time.Second,
		}, nil)
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("remove remote scratch: %s", remoteCommandDetail(result))
		}
		return nil
	}
	result, err := runner.Run(ctx, Command{
		Executable: "sh",
		Args: []string{"-c", `set -eu
work=$1
task=$2
scratch=$work/.aha2-context/$task/scratch
[ -e "$scratch" ] || exit 0
[ -d "$scratch" ] || { printf '%s\n' 'refusing to remove non-directory scratch' >&2; exit 64; }
real_work=$(cd "$work" && pwd -P)
real_scratch=$(cd "$scratch" && pwd -P)
expected=$real_work/.aha2-context/$task/scratch
[ "$real_scratch" = "$expected" ] || { printf '%s\n' 'refusing to remove scratch outside task boundary' >&2; exit 64; }
rm -rf -- "$scratch"`, "aha-scratch-remove", workDir, taskID},
		Dir:     workDir,
		Timeout: 30 * time.Second,
	}, nil)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("remove remote scratch: %s", remoteCommandDetail(result))
	}
	return nil
}

// The Windows remote guard embeds both values because powershell.exe -Command
// does not bind extra argv elements to a param() block. Reparse points on any
// component are refused rather than followed.
const windowsRemoveScratchScript = `$Work = __AHA2_TARGET__
$Task = __AHA2_TASK__
$Context = Join-Path $Work '.aha2-context'
$Scratch = Join-Path (Join-Path $Context $Task) 'scratch'
if (-not (Test-Path -LiteralPath $Scratch -PathType Container)) { exit 0 }
foreach ($Path in @($Context, (Join-Path $Context $Task), $Scratch)) {
  if (Test-Path -LiteralPath $Path) {
    $Item = Get-Item -LiteralPath $Path -Force
    if (($Item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
      [Console]::Error.WriteLine('refusing to remove scratch through a reparse point: ' + $Path)
      exit 64
    }
  }
}
$RealWork = [IO.Path]::GetFullPath($Work).TrimEnd('\')
$RealScratch = [IO.Path]::GetFullPath($Scratch).TrimEnd('\')
$Expected = [IO.Path]::GetFullPath((Join-Path (Join-Path (Join-Path $RealWork '.aha2-context') $Task) 'scratch')).TrimEnd('\')
if (-not [string]::Equals($RealScratch, $Expected, [StringComparison]::OrdinalIgnoreCase)) {
  [Console]::Error.WriteLine('refusing to remove scratch outside task boundary: ' + $RealScratch)
  exit 64
}
Remove-Item -LiteralPath $Scratch -Recurse -Force`
