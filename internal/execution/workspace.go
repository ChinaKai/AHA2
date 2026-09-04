package execution

import (
	"context"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

type WorkspacePreparer struct{}

func (WorkspacePreparer) Prepare(ctx context.Context, item domain.Workspace, taskID, targetBranch, taskBranch, isolation, worktreeDir string) (app.PreparedWorkspace, error) {
	prepared, err := workspace.PrepareTaskWorkspace(ctx, item, taskID, targetBranch, taskBranch, isolation, worktreeDir)
	return app.PreparedWorkspace{Path: prepared.Path, BaseCommit: prepared.BaseCommit, Branch: prepared.Branch}, err
}
