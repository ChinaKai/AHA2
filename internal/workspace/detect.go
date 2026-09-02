package workspace

import (
	"context"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func RunnerFor(item domain.Workspace) Runner {
	switch {
	case item.Transport == "wsl":
		return WSLRunner{Distro: item.Distro}
	case item.Transport == "ssh" || item.Locality == "remote":
		return SSHRunner{Host: item.SSHHost, User: item.SSHUser, Port: item.SSHPort}
	default:
		return LocalRunner{}
	}
}

func Detect(ctx context.Context, item domain.Workspace) (domain.Workspace, error) {
	runner := RunnerFor(item)
	now := time.Now().UTC()
	item.LastDetectedAt, item.UpdatedAt = now, now
	item.Capabilities = map[string]any{}
	item.Repository = map[string]any{}
	platformCommand := Command{Executable: "uname", Args: []string{"-srm"}, Dir: item.RootPath, Timeout: 15 * time.Second}
	if item.Locality == "local" && runtime.GOOS == "windows" && item.Transport != "wsl" {
		item.Platform = runtime.GOOS + "/" + runtime.GOARCH
	} else if result, err := runner.Run(ctx, platformCommand, nil); err == nil && result.ExitCode == 0 {
		item.Platform = strings.TrimSpace(result.Stdout)
	}
	gitResult, gitErr := runner.Run(ctx, Command{
		Executable: "git", Args: []string{"-C", item.RootPath, "rev-parse", "--show-toplevel"},
		Timeout: 15 * time.Second,
	}, nil)
	if gitErr == nil && gitResult.ExitCode == 0 && sameWorkspaceRoot(item, strings.TrimSpace(gitResult.Stdout)) {
		item.Repository["is_git"] = true
		item.Repository["root"] = strings.TrimSpace(gitResult.Stdout)
		branchResult, _ := runner.Run(ctx, Command{
			Executable: "git", Args: []string{"-C", item.RootPath, "branch", "--show-current"},
			Timeout: 15 * time.Second,
		}, nil)
		item.Repository["branch"] = strings.TrimSpace(branchResult.Stdout)
	} else {
		item.Repository["is_git"] = false
	}
	item.Capabilities["codex"] = probeBackend(ctx, runner, item.RootPath, "codex")
	item.Capabilities["claude"] = probeBackend(ctx, runner, item.RootPath, "claude")
	item.Capabilities["git"] = item.Repository["is_git"]
	item.Health = "ready"
	if item.Platform == "" {
		item.Health = "degraded"
	}
	return item, nil
}

// probeBackend checks whether a backend CLI (codex / claude) is installed and
// runnable on the workspace host.
func probeBackend(ctx context.Context, runner Runner, rootPath, executable string) map[string]any {
	result, err := runner.Run(ctx, Command{
		Executable: executable, Args: []string{"--version"}, Dir: rootPath, Timeout: 20 * time.Second,
	}, nil)
	if err == nil && result.ExitCode == 0 {
		return map[string]any{"status": "ready", "version": strings.TrimSpace(result.Stdout)}
	}
	return map[string]any{"status": "unavailable"}
}

func sameWorkspaceRoot(item domain.Workspace, gitRoot string) bool {
	workspaceRoot := strings.TrimSpace(item.RootPath)
	if workspaceRoot == "" || gitRoot == "" {
		return false
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
