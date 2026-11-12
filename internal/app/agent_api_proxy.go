package app

import (
	"context"
	"fmt"
	"log"

	"github.com/ChinaKai/AHA2/internal/domain"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

// agentAPITurnSetup is how one Turn reaches AHA from inside its workspace.
type agentAPITurnSetup struct {
	// URL is the base address rendered into the prompt and into the Agent's
	// environment.
	URL string
	// Proxy, when non-nil, owns an SSH connection that must be closed when the
	// Turn ends. Its lifetime is the Turn's: the remote helper is a child of
	// that session, so closing the session stops it.
	Proxy *workspacepkg.AgentAPIProxy
	// Note records a fallback that the operator should see in the log.
	Note string
}

// prepareAgentAPIForTurn resolves the address the Turn's Agent will use.
//
// With the stdio tunnel enabled, a loopback address is replaced by a port on the
// workspace's own loopback carried over SSH — the case that otherwise needs a
// reverse tunnel or the plaintext LAN fallback. Anything that goes wrong while
// setting the tunnel up is logged and the previous resolution runs, so enabling
// the switch can only change the path when the new one is actually available.
func (s *Service) prepareAgentAPIForTurn(ctx context.Context, item domain.Workspace, task domain.Task, workDir string) (agentAPITurnSetup, error) {
	if item.Transport != "ssh" {
		url, err := s.AgentAPIURLForWorkspace(ctx, item)
		return agentAPITurnSetup{URL: url}, err
	}
	settings, err := s.AgentAPISettings(ctx)
	if err != nil {
		return agentAPITurnSetup{}, err
	}
	if !settings.StdioProxy {
		url, err := s.AgentAPIURLForWorkspace(ctx, item)
		return agentAPITurnSetup{URL: url}, err
	}
	base := settings.EffectiveURL
	if normalizeAgentAPIMode(item.AgentAPIMode) == "manual" && item.AgentAPIURL != "" {
		base = item.AgentAPIURL
	}
	target, ok := workspacepkg.AgentAPIProxyTarget(base)
	if !ok {
		// Not loopback: the workspace can dial this address itself, so the
		// tunnel would add a hop and a failure mode without removing one.
		url, err := s.AgentAPIURLForWorkspace(ctx, item)
		return agentAPITurnSetup{URL: url}, err
	}
	project, err := s.startAgentAPIProxy(ctx, item, task, workDir, target)
	if err != nil {
		log.Printf("Agent API stdio 代理不可用，回退到反向隧道/LAN 流程 task=%s workspace=%s target=%s: %v",
			task.ID, item.ID, target, err)
		url, resolveErr := s.AgentAPIURLForWorkspace(ctx, item)
		return agentAPITurnSetup{URL: url, Note: err.Error()}, resolveErr
	}
	return agentAPITurnSetup{URL: project.URL, Proxy: project}, nil
}

// startAgentAPIProxy materialises the helper and opens the tunnel. The helper
// path is cached on the workspace, so only the first Turn of a version pays for
// the upload.
func (s *Service) startAgentAPIProxy(ctx context.Context, item domain.Workspace, task domain.Task, workDir, target string) (*workspacepkg.AgentAPIProxy, error) {
	helper, err := workspacepkg.EnsureAgentProxyHelper(ctx, item, workDir, task.ID, s.buildVersion)
	if err != nil {
		return nil, fmt.Errorf("准备 helper：%w", err)
	}
	if helper.Note != "" {
		log.Printf("Agent API helper 使用回退位置 task=%s path=%s：%s", task.ID, helper.Path, helper.Note)
	}
	proxy, err := workspacepkg.StartAgentAPIProxy(ctx, item, helper.Path, target)
	if err != nil {
		return nil, err
	}
	return proxy, nil
}
