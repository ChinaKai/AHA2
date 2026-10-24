package workspace

import (
	"context"
	"os"
	"path/filepath"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// DSHHomeDir returns the isolated DeepSeek Harness home for one AHA backend
// session. Session transcripts, credentials and profile patches therefore
// cannot collide with the operator's normal DSH installation.
func DSHHomeDir(item domain.Workspace, workDir, sessionID string) string {
	if item.Transport == "native" {
		return filepath.Join(workDir, ".aha2-context", "runtime", "dsh-home", sessionID)
	}
	return JoinRemotePath(item, workDir, ".aha2-context", "runtime", "dsh-home", sessionID)
}

func EnsureDSHHomeDir(ctx context.Context, item domain.Workspace, runner Runner, directory string) error {
	if item.Transport == "native" {
		return os.MkdirAll(directory, 0o700)
	}
	return EnsureRemoteDirectory(ctx, runner, directory)
}
