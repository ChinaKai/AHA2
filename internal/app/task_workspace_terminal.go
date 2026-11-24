package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspace"
	"github.com/ChinaKai/AHA2/internal/workspaceterminal"
)

var ErrWorkspaceTerminalUnsupported = errors.New("workspace_terminal_unsupported")

type WorkspaceTerminalTarget struct {
	item domain.Workspace
	root string
}

func (s *Service) WorkspaceTerminalTarget(ctx context.Context, taskID, workspaceID string) (WorkspaceTerminalTarget, error) {
	task, item, root, err := s.viewerWorkspace(ctx, taskID, workspaceID)
	if err != nil {
		return WorkspaceTerminalTarget{}, err
	}
	if task.ReadOnly || item.ReadOnly || item.Transport == "wsl" {
		return WorkspaceTerminalTarget{}, ErrWorkspaceTerminalUnsupported
	}
	switch {
	case item.Transport == "ssh" || item.Locality == "remote":
		if item.SSHHost == "" || item.SSHUser == "" {
			return WorkspaceTerminalTarget{}, ErrWorkspaceTerminalUnsupported
		}
	case item.Transport == "" || item.Transport == "native":
		if !workspaceterminal.LocalSupported() || !filepath.IsAbs(root) {
			return WorkspaceTerminalTarget{}, ErrWorkspaceTerminalUnsupported
		}
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil {
			return WorkspaceTerminalTarget{}, ErrViewerUnavailable
		}
		if info, err := os.Stat(canonical); err != nil || !info.IsDir() {
			return WorkspaceTerminalTarget{}, ErrViewerUnavailable
		}
		root = canonical
	default:
		return WorkspaceTerminalTarget{}, ErrWorkspaceTerminalUnsupported
	}
	return WorkspaceTerminalTarget{item: item, root: root}, nil
}

func (target WorkspaceTerminalTarget) Directory() string { return target.root }

func (target WorkspaceTerminalTarget) WorkspaceID() string { return target.item.ID }

func (target WorkspaceTerminalTarget) UsesSSH() bool {
	return target.item.Transport == "ssh" || target.item.Locality == "remote"
}

func (target WorkspaceTerminalTarget) Scope(login domain.Session) workspaceterminal.Scope {
	item := target.item
	return workspaceterminal.Scope{
		OwnerID: login.OwnerID, LoginID: login.ID, WorkspaceID: item.ID,
		Target: sha256.Sum256(fmt.Appendf(nil, "%q", []string{
			target.root, item.Transport, item.Locality, item.SSHHost, item.SSHUser, strconv.Itoa(item.SSHPort),
			item.SSHAuth, item.SSHCredentialRef, item.Platform,
		})),
	}
}

func (s *Service) StartWorkspaceTerminal(ctx context.Context, target WorkspaceTerminalTarget, cols, rows int) (workspaceterminal.Process, error) {
	item := target.item
	if target.UsesSSH() {
		if item.SSHCredentialRef != "" {
			if s.secrets == nil {
				return nil, errors.New("workspace SSH credential unavailable")
			}
			password, ok := s.secrets.Get(item.SSHCredentialRef)
			if !ok {
				return nil, errors.New("workspace SSH credential unavailable")
			}
			item.SSHPassword = password
		}
		runner := workspace.RunnerFor(item).(*workspace.SSHRunner)
		process, err := runner.StartTerminal(ctx, target.root, cols, rows)
		if err != nil {
			return nil, err
		}
		return process, nil
	}
	return workspaceterminal.StartLocal(target.root, cols, rows)
}
