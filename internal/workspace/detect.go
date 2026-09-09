package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	probeReady           = "ready"
	probeUnavailable     = "unavailable"
	probeNotInstalled    = "not_installed"
	probeNotRepository   = "not_repository"
	probeExecutionFailed = "execution_failed"
)

type DetectionError struct {
	Code    string
	Message string
}

func (err *DetectionError) Error() string { return err.Message }

func DetectionErrorCode(err error) string {
	var detectionErr *DetectionError
	if errors.As(err, &detectionErr) && detectionErr.Code != "" {
		return detectionErr.Code
	}
	return "workspace_detection_failed"
}

func RunnerFor(item domain.Workspace) Runner {
	switch {
	case item.Transport == "wsl":
		return WSLRunner{Distro: item.Distro}
	case item.Transport == "ssh" || item.Locality == "remote":
		return &SSHRunner{
			Host: item.SSHHost, User: item.SSHUser, Port: item.SSHPort,
			Auth: item.SSHAuth, Password: item.SSHPassword, Platform: item.Platform,
		}
	default:
		return LocalRunner{}
	}
}

func Detect(ctx context.Context, item domain.Workspace) (domain.Workspace, error) {
	if item.Transport == "ssh" {
		item.Platform = ""
	}
	return detectWithRunner(ctx, item, RunnerFor(item))
}

func detectWithRunner(ctx context.Context, item domain.Workspace, runner Runner) (domain.Workspace, error) {
	now := time.Now().UTC()
	detectedPlatform := strings.TrimSpace(item.Platform)
	item.LastDetectedAt, item.UpdatedAt = now, now
	// Detection is a fresh snapshot. Never carry a previous successful probe
	// into a failed or partial run.
	item.Platform = ""
	item.Health = "unknown"
	item.Capabilities = map[string]any{}
	item.Repository = map[string]any{}

	accessResult, accessErr := probeWorkspaceAccess(ctx, item, runner)
	if accessErr != nil || accessResult.ExitCode != 0 {
		message := "Workspace 不可访问: " + safeProbeDetail(item, accessResult, accessErr)
		item.Health = "error"
		item.Capabilities["workspace"] = probeResult(probeUnavailable, message)
		return item, &DetectionError{Code: "workspace_unavailable", Message: message}
	}
	item.Capabilities["workspace"] = probeResult(probeReady, "")
	if platformRunner, ok := runner.(interface{ DetectedPlatform() string }); ok {
		if platform := strings.TrimSpace(platformRunner.DetectedPlatform()); platform != "" {
			detectedPlatform = platform
		}
	}

	degraded := false
	if item.Transport == "ssh" && detectedPlatform != "" {
		item.Platform = detectedPlatform
		item.Capabilities["platform"] = probeResult(probeReady, "")
	} else if item.Locality == "local" && runtime.GOOS == "windows" && item.Transport != "wsl" {
		item.Platform = runtime.GOOS + "/" + runtime.GOARCH
		item.Capabilities["platform"] = probeResult(probeReady, "")
	} else {
		result, err := runDetectionCommand(ctx, item, runner, Command{Executable: "uname", Args: []string{"-srm"}, Dir: item.RootPath, Timeout: 15 * time.Second})
		if err == nil && result.ExitCode == 0 && strings.TrimSpace(result.Stdout) != "" {
			item.Platform = strings.TrimSpace(result.Stdout)
			item.Capabilities["platform"] = probeResult(probeReady, "")
		} else {
			message := "平台检测执行失败: " + safeProbeDetail(item, result, err)
			item.Capabilities["platform"] = probeResult(probeExecutionFailed, message)
			degraded = true
		}
	}

	if probeGit(ctx, runner, &item) {
		degraded = true
	}
	item.Capabilities["git"] = item.Repository["is_git"] == true
	item.Capabilities["git"] = item.Repository["is_git"] == true
	for _, executable := range []string{"codex", "claude"} {
		var failed bool
		item.Capabilities[executable], failed = probeBackend(ctx, runner, item, executable)
		degraded = degraded || failed
	}
	item.Health = "ready"
	if degraded {
		item.Health = "degraded"
	}
	return item, nil
}

func probeWorkspaceAccess(ctx context.Context, item domain.Workspace, runner Runner) (Result, error) {
	if item.Transport == "ssh" || item.Transport == "wsl" || item.Locality == "remote" {
		return runDetectionCommand(ctx, item, runner, Command{Executable: "pwd", Dir: item.RootPath, Timeout: 15 * time.Second})
	}
	root := strings.TrimSpace(item.RootPath)
	if root == "" {
		return Result{ExitCode: -1}, fmt.Errorf("workspace root path is empty")
	}
	info, err := os.Stat(root)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	if !info.IsDir() {
		return Result{ExitCode: -1}, fmt.Errorf("workspace root is not a directory")
	}
	directory, err := os.Open(root)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	defer directory.Close()
	if _, err := directory.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return Result{ExitCode: -1}, err
	}
	return Result{}, nil
}

// probeGit returns true when Git itself failed to execute. A normal non-Git
// folder is a valid workspace and does not degrade workspace health.
func probeGit(ctx context.Context, runner Runner, item *domain.Workspace) bool {
	result, err := runDetectionCommand(ctx, *item, runner, Command{
		Executable: "git", Args: []string{"-C", item.RootPath, "rev-parse", "--show-toplevel"}, Timeout: 15 * time.Second,
	})
	item.Repository["is_git"] = false
	if err == nil && result.ExitCode == 0 {
		root := strings.TrimSpace(result.Stdout)
		if !sameWorkspaceRoot(*item, root) {
			item.Repository["status"] = probeNotRepository
			item.Repository["message"] = "Workspace 路径不是 Git 仓库根目录"
			return false
		}
		item.Repository["is_git"] = true
		item.Repository["status"] = probeReady
		item.Repository["root"] = root
		branchResult, branchErr := runDetectionCommand(ctx, *item, runner, Command{
			Executable: "git", Args: []string{"-C", item.RootPath, "branch", "--show-current"}, Timeout: 15 * time.Second,
		})
		if branchErr == nil && branchResult.ExitCode == 0 {
			item.Repository["branch"] = strings.TrimSpace(branchResult.Stdout)
			return false
		}
		message := "Git 分支检测执行失败: " + safeProbeDetail(*item, branchResult, branchErr)
		item.Repository["status"] = probeExecutionFailed
		item.Repository["error"] = message
		return true
	}
	if isNotRepository(result) {
		item.Repository["status"] = probeNotRepository
		item.Repository["message"] = "该目录不是 Git 仓库"
		return false
	}
	message := "Git 检测执行失败: " + safeProbeDetail(*item, result, err)
	item.Repository["status"] = probeExecutionFailed
	item.Repository["error"] = message
	return true
}

// probeBackend checks whether a backend CLI is installed and runnable. The
// boolean result reports an execution failure; a missing optional CLI is not a
// degraded workspace by itself.
func probeBackend(ctx context.Context, runner Runner, item domain.Workspace, executable string) (map[string]any, bool) {
	result, err := runDetectionCommand(ctx, item, runner, Command{
		Executable: executable, Args: []string{"--version"}, Dir: item.RootPath, Timeout: 20 * time.Second,
	})
	if err == nil && result.ExitCode == 0 {
		version := strings.TrimSpace(result.Stdout)
		if version == "" {
			version = strings.TrimSpace(result.Stderr)
		}
		return map[string]any{"status": probeReady, "version": safeDisplayText(item, version)}, false
	}
	if commandUnavailable(result, err) {
		return probeResult(probeNotInstalled, ""), false
	}
	message := strings.ToUpper(executable[:1]) + executable[1:] + " 检测执行失败: " + safeProbeDetail(item, result, err)
	return probeResult(probeExecutionFailed, message), true
}

// Detection probes are read-only, so retrying one WSL deadline is safe and
// absorbs the common cold-start case without making general Runner commands
// repeat side effects. SSH and an already-cancelled parent context never retry.
func runDetectionCommand(ctx context.Context, item domain.Workspace, runner Runner, command Command) (Result, error) {
	result, err := runner.Run(ctx, command, nil)
	if item.Transport != "wsl" || !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return result, err
	}
	return runner.Run(ctx, command, nil)
}

func probeResult(status, message string) map[string]any {
	result := map[string]any{"status": status}
	if message != "" {
		result["error"] = message
	}
	return result
}

func isNotRepository(result Result) bool {
	text := strings.ToLower(result.Stdout + " " + result.Stderr)
	return strings.Contains(text, "not a git repository") || strings.Contains(text, "不是 git 仓库")
}

func commandUnavailable(result Result, err error) bool {
	if errors.Is(err, exec.ErrNotFound) || result.ExitCode == 127 || result.ExitCode == 9009 {
		return true
	}
	text := strings.ToLower(result.Stderr)
	if err != nil {
		text += " " + strings.ToLower(err.Error())
	}
	return strings.Contains(text, "executable file not found") ||
		strings.Contains(text, "command not found") ||
		strings.Contains(text, "aha2_command_not_found:") ||
		strings.Contains(text, "not recognized as an internal or external command")
}

func safeProbeDetail(item domain.Workspace, result Result, err error) string {
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" && err != nil {
		detail = err.Error()
	}
	if detail == "" {
		detail = fmt.Sprintf("exit code %d", result.ExitCode)
	}
	return safeDisplayText(item, detail)
}

func safeDisplayText(item domain.Workspace, value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if item.SSHPassword != "" {
		value = strings.ReplaceAll(value, item.SSHPassword, "[已隐藏]")
	}
	runes := []rune(value)
	if len(runes) > 240 {
		value = string(runes[:239]) + "…"
	}
	return value
}

func sameWorkspaceRoot(item domain.Workspace, gitRoot string) bool {
	workspaceRoot := strings.TrimSpace(item.RootPath)
	if workspaceRoot == "" || gitRoot == "" {
		return false
	}
	if IsWindowsWorkspace(item) {
		workspaceRoot = path.Clean(strings.ReplaceAll(workspaceRoot, "\\", "/"))
		gitRoot = path.Clean(strings.ReplaceAll(gitRoot, "\\", "/"))
		return strings.EqualFold(workspaceRoot, gitRoot)
	}
	if item.Locality == "remote" || item.Transport == "ssh" || item.Transport == "wsl" {
		return path.Clean(strings.ReplaceAll(workspaceRoot, "\\", "/")) == path.Clean(strings.ReplaceAll(gitRoot, "\\", "/"))
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	gitRoot = filepath.Clean(strings.ReplaceAll(gitRoot, "/", string(filepath.Separator)))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(workspaceRoot, gitRoot)
	}
	return workspaceRoot == gitRoot
}

func FindLocalCodex() string {
	path, _ := exec.LookPath("codex")
	return path
}
