package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
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
	if err := ensureContextIgnored(ctx, item, workDir); err != nil {
		return err
	}
	if item.Transport == "native" {
		return materializeLocalContext(root, files)
	}
	runner := RunnerFor(item)
	root = path.Clean(strings.ReplaceAll(root, "\\", "/"))
	for target, content := range files {
		target = path.Clean(strings.ReplaceAll(target, "\\", "/"))
		if target != root && !strings.HasPrefix(target, root+"/") {
			return fmt.Errorf("context path escapes root: %s", target)
		}
		result, err := runner.Run(ctx, Command{
			Executable: "sh",
			Args:       []string{"-c", `umask 077; mkdir -p "$(dirname "$1")" && cat > "$1"`, "aha-context-write", target},
			Stdin:      content, Timeout: 30 * time.Second,
		}, nil)
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("write context %s: %s", target, strings.TrimSpace(result.Stderr))
		}
	}
	return nil
}

func ensureContextIgnored(ctx context.Context, item domain.Workspace, workDir string) error {
	if item.Transport == "native" {
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
	}
	result, err := RunnerFor(item).Run(ctx, Command{
		Executable: "sh",
		Args: []string{"-c", `exclude="$(git -C "$1" rev-parse --git-path info/exclude 2>/dev/null || true)"; ` +
			`if [ -n "$exclude" ] && ! grep -qxF '.aha2-context/' "$exclude" 2>/dev/null; then ` +
			`mkdir -p "$(dirname "$exclude")"; printf '\n.aha2-context/\n' >> "$exclude"; fi`,
			"aha-context-ignore", workDir},
		Timeout: 20 * time.Second,
	}, nil)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("configure context ignore: %s", strings.TrimSpace(result.Stderr))
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
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	for file, content := range files {
		target, err := filepath.Abs(file)
		if err != nil {
			return err
		}
		prefix := root + string(os.PathSeparator)
		if target != root && !strings.HasPrefix(strings.ToLower(target), strings.ToLower(prefix)) {
			return fmt.Errorf("context path escapes root: %s", file)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}
