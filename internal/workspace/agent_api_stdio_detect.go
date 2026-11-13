package workspace

import (
	"context"
	"fmt"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// AgentAPIStdioResult is what a successful stdio detection observed. URL is the
// ephemeral workspace-side loopback address the helper announced during this
// probe; it is evidence for the detection report, not a value to persist for
// later Turns (each Turn starts its own helper on a fresh port).
type AgentAPIStdioResult struct {
	URL     string
	Runtime string
	Helper  string
}

// ProbeAgentAPIOverStdio verifies the Agent API over the SSH session's stdio.
//
// It uses the same helper materialisation and session setup as a Turn
// (EnsureAgentProxyHelper, then StartAgentAPIProxy) and then makes a real
// /healthz request *from inside the workspace* through the proxy URL. Uploading
// the helper is not itself proof: only the health response is.
//
// The proxy owns an SSH session whose child is the remote helper, so closing it
// here is what leaves no helper process behind. That happens on every path,
// including a failed health check.
func ProbeAgentAPIOverStdio(ctx context.Context, item domain.Workspace, baseURL, workDir, taskID, version, backend string) (AgentAPIStdioResult, error) {
	target, scheme, ok := AgentAPIProxyTarget(baseURL)
	if !ok {
		return AgentAPIStdioResult{}, fmt.Errorf("Agent API 地址 %q 不是可代理的 loopback 地址", baseURL)
	}
	// The helper lands under <workDir>/.aha2-context/share/agent-proxy. A Turn
	// gets the ignore rule from context materialisation; detection creates the
	// directory on its own, so it applies the same rule first. Failure is not
	// fatal: the ignore is cosmetic and the helper can still run.
	_ = ensureContextIgnored(ctx, item, workDir)
	helper, err := EnsureAgentProxyHelper(ctx, item, workDir, taskID, version, backend)
	if err != nil {
		return AgentAPIStdioResult{}, fmt.Errorf("准备 helper：%w", err)
	}
	proxy, err := StartAgentAPIProxy(ctx, item, helper, target, scheme)
	if err != nil {
		return AgentAPIStdioResult{}, err
	}
	result := AgentAPIStdioResult{URL: proxy.URL, Runtime: proxy.Runtime, Helper: helper.Path}
	defer func() { _ = proxy.Close() }()
	if err := ProbeAgentAPI(ctx, item, proxy.URL); err != nil {
		return result, fmt.Errorf("通过 stdio 代理探测 healthz：%w", err)
	}
	return result, nil
}
