package app

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/ChinaKai/AHA2/internal/agentproxy"
	"github.com/ChinaKai/AHA2/internal/domain"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

// Paths a Turn can use to reach the Agent API. They are recorded so the failure
// of one layer names the layer that replaced it instead of leaving the operator
// to infer it from a bare URL.
const (
	agentAPIPathDirect        = "direct"
	agentAPIPathStdioNode     = "stdio-node"
	agentAPIPathStdioOverride = "stdio-override"
	agentAPIPathTunnel        = "tunnel"
	agentAPIPathLAN           = "lan"
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
	// Path names the transport that was selected.
	Path string
	// Note records a fallback that the operator should see in the log.
	Note string
}

// prepareAgentAPIForTurn resolves the address the Turn's Agent will use.
//
// The layers are tried in order and every rejected layer is logged with its
// reason before the next one runs:
//
//  1. the stdio helper (AHA2_AGENT_PROXY_HELPER override, else the embedded
//     Node script), which replaces the SSH channel itself with the Agent API.
//     It is the default for every SSH workspace, not an opt-in switch;
//  2. the existing SSH reverse-tunnel -> LAN plaintext resolution.
//
// Anything that goes wrong while setting the stdio transport up is logged and
// the previous resolution runs, so a workspace without a usable helper behaves
// exactly as it did before the stdio transport existed.
func (s *Service) prepareAgentAPIForTurn(ctx context.Context, item domain.Workspace, task domain.Task, workDir, backend string) (agentAPITurnSetup, error) {
	if item.Transport != "ssh" {
		url, err := s.AgentAPIURLForWorkspace(ctx, item)
		return agentAPITurnSetup{URL: url, Path: agentAPIPathDirect}, err
	}
	settings, err := s.AgentAPISettings(ctx)
	if err != nil {
		return agentAPITurnSetup{}, err
	}
	base := agentAPIBaseURL(item, settings)
	target, scheme, ok := workspacepkg.AgentAPIProxyTarget(base)
	if !ok {
		// Not loopback: the workspace can dial this address itself, so the
		// tunnel would add a hop and a failure mode without removing one.
		url, err := s.AgentAPIURLForWorkspace(ctx, item)
		return agentAPITurnSetup{URL: url, Path: agentAPIPathDirect}, err
	}
	project, err := s.startAgentAPIProxyForTurn(ctx, item, task, workDir, target, scheme, s.backendBinary(backend))
	if err == nil {
		path := agentAPIPathStdioNode
		if project.Runtime != "" && project.Runtime != agentproxy.NodeHelperRuntime {
			path = agentAPIPathStdioOverride
		}
		return agentAPITurnSetup{URL: project.URL, Proxy: project, Path: path}, nil
	}
	log.Printf("Agent API stdio 代理不可用，尝试 SSH 反向隧道 task=%s workspace=%s target=%s：%v",
		task.ID, item.ID, target, err)
	url, resolveErr := s.AgentAPIURLForWorkspace(ctx, item)
	path := agentAPIPathForURL(item, url)
	switch path {
	case agentAPIPathLAN:
		detail := s.agentAPIFallbackDetail(ctx, item)
		if detail != "" {
			log.Printf("Agent API SSH 反向隧道不可用（%s），回退 LAN 明文 task=%s workspace=%s url=%s",
				detail, task.ID, item.ID, url)
		} else {
			log.Printf("Agent API 使用已解析的 LAN 地址 task=%s workspace=%s url=%s", task.ID, item.ID, url)
		}
	case agentAPIPathTunnel:
		log.Printf("Agent API 回退到 SSH 反向隧道 task=%s workspace=%s url=%s", task.ID, item.ID, url)
	default:
		log.Printf("Agent API 回退到直连地址 task=%s workspace=%s url=%s", task.ID, item.ID, url)
	}
	return agentAPITurnSetup{URL: url, Path: path, Note: err.Error()}, resolveErr
}

// startAgentAPIProxyForTurn is the test seam around the SSH stdio setup. A nil
// hook uses the real transport; tests substitute it so the fallback order can be
// exercised without an SSH host.
func (s *Service) startAgentAPIProxyForTurn(ctx context.Context, item domain.Workspace, task domain.Task, workDir, target, scheme, backendBinary string) (*workspacepkg.AgentAPIProxy, error) {
	if s.agentAPIProxyStartFn != nil {
		return s.agentAPIProxyStartFn(ctx, item, task, workDir, target, scheme, backendBinary)
	}
	return s.startAgentAPIProxy(ctx, item, task, workDir, target, scheme, backendBinary)
}

// startAgentAPIProxy materialises the helper and opens the tunnel. The helper
// path is cached on the workspace, so only the first Turn of a version pays for
// the upload.
func (s *Service) startAgentAPIProxy(ctx context.Context, item domain.Workspace, task domain.Task, workDir, target, scheme, backend string) (*workspacepkg.AgentAPIProxy, error) {
	helper, err := workspacepkg.EnsureAgentProxyHelper(ctx, item, workDir, task.ID, s.buildVersion, backend)
	if err != nil {
		return nil, fmt.Errorf("准备 helper：%w", err)
	}
	if helper.Note != "" {
		log.Printf("Agent API helper 使用回退位置 task=%s path=%s：%s", task.ID, helper.Path, helper.Note)
	}
	proxy, err := workspacepkg.StartAgentAPIProxy(ctx, item, helper, target, scheme)
	if err != nil {
		return nil, err
	}
	return proxy, nil
}

// backendBinary resolves the command a backend adapter will run. Executors that
// cannot report one return an empty string, which makes node detection fall back
// to PATH instead of failing.
func (s *Service) backendBinary(name string) string {
	resolver, ok := s.executor.(BackendBinaryResolver)
	if !ok || resolver == nil {
		return ""
	}
	return strings.TrimSpace(resolver.BackendBinary(name))
}

// agentAPIPathForURL classifies the resolved address. It mirrors the Turn-side
// tunnel gate: a loopback address on an SSH workspace is only reachable through
// the reverse tunnel, anything else is dialed directly (plaintext LAN included).
func agentAPIPathForURL(item domain.Workspace, resolved string) string {
	if item.Transport != "ssh" {
		return agentAPIPathDirect
	}
	parsed, err := url.Parse(strings.TrimSpace(resolved))
	if err != nil || parsed.Hostname() == "" {
		return agentAPIPathDirect
	}
	if agentAPIHostIsLoopback(parsed.Hostname()) {
		return agentAPIPathTunnel
	}
	if strings.EqualFold(parsed.Scheme, "http") {
		return agentAPIPathLAN
	}
	return agentAPIPathDirect
}

// agentAPIFallbackDetail returns the reason detection recorded for the failed
// tunnel/LAN candidates. It is best effort: a missing or stale workspace row
// must not turn a fallback into a failure.
func (s *Service) agentAPIFallbackDetail(ctx context.Context, item domain.Workspace) string {
	if s.store == nil || item.ID == "" {
		return ""
	}
	refreshed, err := s.store.Workspace(ctx, item.ID)
	if err != nil {
		return ""
	}
	// A successful LAN fallback leaves AgentAPIError empty, so the tunnel probe
	// result is read from the capability record detection keeps alongside it.
	if capabilities, ok := refreshed.Capabilities["agent_api"].(map[string]any); ok {
		if tunnelError, ok := capabilities["tunnel_error"].(string); ok && strings.TrimSpace(tunnelError) != "" {
			return truncateForLog(strings.TrimSpace(tunnelError), 400)
		}
	}
	detail := strings.TrimSpace(refreshed.AgentAPIError)
	if detail == "" {
		return ""
	}
	return truncateForLog(detail, 400)
}

func truncateForLog(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
