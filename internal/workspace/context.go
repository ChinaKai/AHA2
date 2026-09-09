package workspace

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func MaterializeContext(
	ctx context.Context,
	item domain.Workspace,
	workDir string,
	root string,
	files map[string]string,
) error {
	if !isContextMaterializationRoot(root) {
		return fmt.Errorf("context reset-or-write stage: refusing to reset invalid context root: %s", root)
	}
	if item.Transport == "native" {
		if err := ensureContextIgnored(ctx, item, workDir); err != nil {
			return err
		}
		return materializeLocalContext(root, files)
	}
	runner := retryRemoteContextRunner(item, RunnerFor(item))
	if err := ensureRemoteContextIgnored(ctx, item, workDir, runner); err != nil {
		return err
	}
	return materializeRemoteContext(ctx, item, runner, root, files)
}

// PruneSharedContexts removes obsolete immutable shared Context snapshots next
// to currentRoot. The boundary is deliberately narrower than .aha2-context:
// only direct children of the same Task directory whose names match the
// generated shared snapshot format are eligible for removal.
func PruneSharedContexts(
	ctx context.Context,
	item domain.Workspace,
	workDir string,
	currentRoot string,
	keepRoots []string,
) error {
	currentRoot = cleanContextPath(item, currentRoot)
	taskRoot, currentName, err := sharedContextLocation(item, currentRoot)
	if err != nil {
		return fmt.Errorf("context prune stage: %w", err)
	}
	if !taskContextRootBelongsToWorkDir(item, workDir, taskRoot) {
		return fmt.Errorf("context prune stage: refusing to prune outside workspace Context root: %s", taskRoot)
	}
	keepNames := make(map[string]struct{}, len(keepRoots)+1)
	keepNames[contextNameKey(item, currentName)] = struct{}{}
	for _, root := range keepRoots {
		root = cleanContextPath(item, root)
		parent, name, err := sharedContextLocation(item, root)
		if err != nil || !sameContextPath(item, parent, taskRoot) {
			continue
		}
		keepNames[contextNameKey(item, name)] = struct{}{}
	}

	if item.Transport == "native" {
		if err := pruneLocalSharedContexts(taskRoot, keepNames); err != nil {
			return fmt.Errorf("context prune stage: %w", err)
		}
		return nil
	}
	runner := retryRemoteContextRunner(item, RunnerFor(item))
	if err := pruneRemoteSharedContexts(ctx, item, workDir, runner, taskRoot, keepNames); err != nil {
		return fmt.Errorf("context prune stage: %w", err)
	}
	return nil
}

func cleanContextPath(item domain.Workspace, value string) string {
	if item.Transport == "native" {
		return filepath.Clean(value)
	}
	value = strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(value, "//") {
		return "//" + strings.TrimPrefix(path.Clean(strings.TrimPrefix(value, "//")), "/")
	}
	return path.Clean(value)
}

func sharedContextLocation(item domain.Workspace, root string) (string, string, error) {
	var taskRoot, name string
	if item.Transport == "native" {
		taskRoot, name = filepath.Dir(root), filepath.Base(root)
	} else {
		name = path.Base(root)
		if strings.HasPrefix(root, "//") {
			taskRoot = "//" + strings.TrimPrefix(path.Dir(strings.TrimPrefix(root, "//")), "/")
		} else {
			taskRoot = path.Dir(root)
		}
	}
	if !isSharedContextName(name) || !isContextTaskRoot(taskRoot) {
		return "", "", fmt.Errorf("refusing to prune invalid shared context root: %s", root)
	}
	return taskRoot, name, nil
}

func isContextTaskRoot(root string) bool {
	cleaned := strings.Trim(strings.ReplaceAll(filepath.Clean(root), "\\", "/"), "/")
	parts := strings.Split(cleaned, "/")
	return len(parts) >= 2 && parts[len(parts)-2] == ".aha2-context" &&
		parts[len(parts)-1] != "" && parts[len(parts)-1] != "runtime"
}

func taskContextRootBelongsToWorkDir(item domain.Workspace, workDir, taskRoot string) bool {
	if item.Transport == "native" {
		absoluteWorkDir, err := filepath.Abs(workDir)
		if err != nil {
			return false
		}
		absoluteTaskRoot, err := filepath.Abs(taskRoot)
		if err != nil {
			return false
		}
		return filepath.Clean(filepath.Dir(absoluteTaskRoot)) == filepath.Join(filepath.Clean(absoluteWorkDir), ".aha2-context")
	}
	workDir = path.Clean(strings.ReplaceAll(workDir, "\\", "/"))
	expectedParent := path.Join(workDir, ".aha2-context")
	return sameContextPath(item, path.Dir(taskRoot), expectedParent)
}

func isSharedContextName(name string) bool {
	const prefix = "shared-"
	if !strings.HasPrefix(name, prefix) || len(name) != len(prefix)+sha256HexLength {
		return false
	}
	for _, value := range name[len(prefix):] {
		if value < '0' || value > '9' {
			if value < 'a' || value > 'f' {
				return false
			}
		}
	}
	return true
}

const sha256HexLength = 64

func contextNameKey(item domain.Workspace, name string) string {
	if item.Transport == "native" || IsWindowsWorkspace(item) {
		return strings.ToLower(name)
	}
	return name
}

func sameContextPath(item domain.Workspace, left, right string) bool {
	if item.Transport == "native" || IsWindowsWorkspace(item) {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func pruneLocalSharedContexts(taskRoot string, keepNames map[string]struct{}) error {
	entries, err := os.ReadDir(taskRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !isSharedContextName(entry.Name()) {
			continue
		}
		if _, keep := keepNames[strings.ToLower(entry.Name())]; keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(taskRoot, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func pruneRemoteSharedContexts(
	ctx context.Context,
	item domain.Workspace,
	workDir string,
	runner Runner,
	taskRoot string,
	keepNames map[string]struct{},
) error {
	names := make([]string, 0, len(keepNames))
	for name := range keepNames {
		names = append(names, name)
	}
	sort.Strings(names)
	if IsWindowsWorkspace(item) {
		result, err := runner.Run(ctx, Command{
			Executable: "powershell.exe",
			Args: append([]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `param([string]$TaskRoot,[string[]]$Keep)
if (-not (Test-Path -LiteralPath $TaskRoot -PathType Container)) { exit 0 }
Get-ChildItem -LiteralPath $TaskRoot -Directory | Where-Object {
  $_.Name -match '^shared-[0-9a-f]{64}$' -and $Keep -notcontains $_.Name
} | Remove-Item -Recurse -Force`, taskRoot}, names...),
			Dir: workDir, Timeout: 30 * time.Second,
		}, nil)
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("prune remote Windows Context: %s", remoteCommandDetail(result))
		}
		return nil
	}
	result, err := runner.Run(ctx, Command{
		Executable: "sh",
		Args: append([]string{"-c", `set -eu
task_root=$1
shift
[ -d "$task_root" ] || exit 0
for candidate in "$task_root"/shared-*; do
  [ -d "$candidate" ] || continue
  name=${candidate##*/}
  suffix=${name#shared-}
  [ ${#suffix} -eq 64 ] || continue
  case "$suffix" in *[!0-9a-f]*) continue ;; esac
  keep=false
  for retained in "$@"; do
    if [ "$name" = "$retained" ]; then keep=true; break; fi
  done
  [ "$keep" = true ] || rm -rf -- "$candidate"
done`, "aha-context-prune", taskRoot}, names...),
		Dir: workDir, Timeout: 30 * time.Second,
	}, nil)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("prune remote Context: %s", strings.TrimSpace(result.Stderr))
	}
	return nil
}

func materializeRemoteContext(ctx context.Context, item domain.Workspace, runner Runner, root string, files map[string]string) error {
	err := func() error {
		root = path.Clean(strings.ReplaceAll(root, "\\", "/"))
		if !isContextMaterializationRoot(root) {
			return fmt.Errorf("refusing to reset invalid context root: %s", root)
		}
		if IsWindowsWorkspace(item) {
			return materializeWindowsRemoteContext(ctx, runner, root, files)
		}
		archive, err := remoteContextArchive(root, files)
		if err != nil {
			return err
		}
		runner = retryRemoteContextRunner(item, runner)
		result, err := runner.Run(ctx, Command{
			Executable: "sh",
			Args: []string{"-c", `set -eu; umask 077; rm -rf -- "$1"; mkdir -p -- "$1"; tar -xf - -C "$1"`,
				"aha-context-reset-or-write", root},
			Stdin:   archive,
			Timeout: 30 * time.Second,
		}, nil)
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("materialize context %s: %s", root, strings.TrimSpace(result.Stderr))
		}
		return nil
	}()
	if err != nil {
		return fmt.Errorf("context reset-or-write stage: %w", err)
	}
	return nil
}

func materializeWindowsRemoteContext(ctx context.Context, runner Runner, root string, files map[string]string) error {
	reset := Command{
		Executable: "powershell.exe",
		Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `param([string]$Root)
if (Test-Path -LiteralPath $Root) { Remove-Item -LiteralPath $Root -Recurse -Force }
[IO.Directory]::CreateDirectory($Root) | Out-Null`, root},
		Timeout: 30 * time.Second,
	}
	result, err := runner.Run(ctx, reset, nil)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("reset remote context: %s", remoteCommandDetail(result))
	}
	targets := make([]string, 0, len(files))
	validated := make(map[string]string, len(files))
	for target, content := range files {
		target = path.Clean(strings.ReplaceAll(target, "\\", "/"))
		if target == root || !strings.HasPrefix(strings.ToLower(target), strings.ToLower(root+"/")) {
			return fmt.Errorf("context path escapes root: %s", target)
		}
		targets = append(targets, target)
		validated[target] = content
	}
	sort.Strings(targets)
	for _, target := range targets {
		if err := WriteRemoteTextFile(ctx, runner, target, validated[target]); err != nil {
			return fmt.Errorf("write context file %s: %w", target, err)
		}
	}
	return nil
}

func remoteContextArchive(root string, files map[string]string) (string, error) {
	validated := make(map[string]string, len(files))
	directories := map[string]struct{}{}
	for target, content := range files {
		target = path.Clean(strings.ReplaceAll(target, "\\", "/"))
		if target == root || !strings.HasPrefix(target, root+"/") {
			return "", fmt.Errorf("context path escapes root: %s", target)
		}
		relative := strings.TrimPrefix(target, root+"/")
		validated[relative] = content
		for directory := path.Dir(relative); directory != "."; directory = path.Dir(directory) {
			directories[directory] = struct{}{}
		}
	}

	directoryNames := make([]string, 0, len(directories))
	for directory := range directories {
		directoryNames = append(directoryNames, directory)
	}
	sort.Strings(directoryNames)
	fileNames := make([]string, 0, len(validated))
	for name := range validated {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)

	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, directory := range directoryNames {
		if err := writer.WriteHeader(&tar.Header{Name: directory + "/", Mode: 0o700, Typeflag: tar.TypeDir}); err != nil {
			return "", fmt.Errorf("build context archive directory %s: %w", directory, err)
		}
	}
	for _, name := range fileNames {
		content := validated[name]
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			return "", fmt.Errorf("build context archive file %s: %w", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			return "", fmt.Errorf("write context archive file %s: %w", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("finish context archive: %w", err)
	}
	return buffer.String(), nil
}

type contextDeadlineRetryRunner struct {
	runner  Runner
	retried bool
}

func retryRemoteContextRunner(item domain.Workspace, runner Runner) Runner {
	if item.Transport != "wsl" {
		return runner
	}
	if _, ok := runner.(*contextDeadlineRetryRunner); ok {
		return runner
	}
	return &contextDeadlineRetryRunner{runner: runner}
}

func (runner *contextDeadlineRetryRunner) Run(ctx context.Context, command Command, onLine LineHandler) (Result, error) {
	result, err := runner.runner.Run(ctx, command, onLine)
	if runner.retried || !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return result, err
	}
	runner.retried = true
	return runner.runner.Run(ctx, command, onLine)
}

func isContextMaterializationRoot(root string) bool {
	cleaned := strings.Trim(strings.ReplaceAll(filepath.Clean(root), "\\", "/"), "/")
	parts := strings.Split(cleaned, "/")
	return len(parts) >= 3 && parts[len(parts)-3] == ".aha2-context" && parts[len(parts)-2] != "" && parts[len(parts)-1] != ""
}

func ensureContextIgnored(ctx context.Context, item domain.Workspace, workDir string) error {
	if item.Transport == "native" {
		err := func() error {
			output, err := exec.CommandContext(ctx, "git", "-C", workDir, "rev-parse", "--git-path", "info/exclude").Output()
			if err != nil {
				return nil
			}
			exclude := strings.TrimSpace(string(output))
			if !filepath.IsAbs(exclude) {
				exclude = filepath.Join(workDir, exclude)
			}
			data, _ := os.ReadFile(exclude)
			if hasIgnoreLine(string(data), ".aha2-context/") {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(exclude), 0o700); err != nil {
				return err
			}
			file, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = file.WriteString("\n.aha2-context/\n")
			return err
		}()
		if err != nil {
			return fmt.Errorf("context ignore stage: %w", err)
		}
		return nil
	}
	return ensureRemoteContextIgnored(ctx, item, workDir, RunnerFor(item))
}

func ensureRemoteContextIgnored(ctx context.Context, item domain.Workspace, workDir string, runner Runner) error {
	err := func() error {
		workDir = path.Clean(strings.ReplaceAll(workDir, "\\", "/"))
		runner = retryRemoteContextRunner(item, runner)
		gitPath, err := runner.Run(ctx, Command{
			Executable: "git",
			Args:       []string{"-C", workDir, "rev-parse", "--git-path", "info/exclude"},
			Dir:        workDir,
			Timeout:    20 * time.Second,
		}, nil)
		// Context materialization also supports ordinary folders. Missing Git or a
		// non-repository directory therefore means there is no Git exclude file to
		// update, not that context creation should fail.
		if err != nil {
			return fmt.Errorf("probe Git exclude: %w", err)
		}
		if gitPath.ExitCode != 0 {
			return nil
		}
		exclude := path.Clean(strings.ReplaceAll(strings.TrimSpace(gitPath.Stdout), "\\", "/"))
		if exclude == "." || exclude == "" {
			return nil
		}
		if !remotePathIsAbs(item, exclude) {
			exclude = path.Join(workDir, exclude)
		}
		if IsWindowsWorkspace(item) {
			result, err := runner.Run(ctx, Command{
				Executable: "powershell.exe",
				Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `param([string]$Target)
$parent = [IO.Path]::GetDirectoryName($Target)
if ($parent) { [IO.Directory]::CreateDirectory($parent) | Out-Null }
$present = (Test-Path -LiteralPath $Target) -and ((Get-Content -LiteralPath $Target) -contains '.aha2-context/')
if (-not $present) { [IO.File]::AppendAllText($Target, [Environment]::NewLine + '.aha2-context/' + [Environment]::NewLine) }`, exclude},
				Dir: workDir, Timeout: 20 * time.Second,
			}, nil)
			if err != nil {
				return fmt.Errorf("configure context ignore: %w", err)
			}
			if result.ExitCode != 0 {
				return fmt.Errorf("configure context ignore: %s", strings.TrimSpace(result.Stderr))
			}
			return nil
		}
		result, err := runner.Run(ctx, Command{
			Executable: "sh",
			Args: []string{"-c", `umask 077; if ! grep -qxF '.aha2-context/' "$1" 2>/dev/null; then ` +
				`mkdir -p "$(dirname "$1")"; printf '\n.aha2-context/\n' >> "$1"; fi`,
				"aha-context-ignore", exclude},
			Dir:     workDir,
			Timeout: 20 * time.Second,
		}, nil)
		if err != nil {
			return fmt.Errorf("configure context ignore: %w", err)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("configure context ignore: %s", strings.TrimSpace(result.Stderr))
		}
		return nil
	}()
	if err != nil {
		return fmt.Errorf("context ignore stage: %w", err)
	}
	return nil
}

func hasIgnoreLine(content, expected string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == expected {
			return true
		}
	}
	return false
}

func materializeLocalContext(root string, files map[string]string) error {
	err := func() error {
		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		if !isContextMaterializationRoot(root) {
			return fmt.Errorf("refusing to reset invalid context root: %s", root)
		}
		validated := make(map[string]string, len(files))
		for file, content := range files {
			target, err := filepath.Abs(file)
			if err != nil {
				return err
			}
			prefix := root + string(os.PathSeparator)
			if target == root || !strings.HasPrefix(strings.ToLower(target), strings.ToLower(prefix)) {
				return fmt.Errorf("context path escapes root: %s", file)
			}
			validated[target] = content
		}
		if err := os.RemoveAll(root); err != nil {
			return err
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
		for target, content := range validated {
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
				return err
			}
		}
		return nil
	}()
	if err != nil {
		return fmt.Errorf("context reset-or-write stage: %w", err)
	}
	return nil
}
