package workspace

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/ChinaKai/AHA2/internal/agentproxy"
	"github.com/ChinaKai/AHA2/internal/domain"
)

// Agent API over SSH stdio.
//
// The existing fallback for a workspace that cannot reach AHA is a reverse
// tunnel, which needs sshd to allow port forwarding. When it does not, the only
// remaining path is AHA's LAN address in plaintext — which is exactly what this
// transport replaces. Here the SSH session's own stdin/stdout carry a
// multiplexed stream, so "SSH works" is the only requirement: no sshd setting,
// no extra listening port on either side, no plaintext on the LAN.
//
// The helper is the same aha2 binary invoked as a hidden subcommand, materialised
// into the workspace's shared context directory.

// AgentProxyAttempts bounds how often a failed handshake is retried before the
// Turn falls back. A transient dial failure is worth one more try; a workspace
// that refuses or misreports must not cost the Turn its whole timeout.
const AgentProxyAttempts = 3

// AgentProxyReadyTimeout bounds the handshake of one attempt.
const AgentProxyReadyTimeout = 15 * time.Second

// AgentAPIProxy is a running stdio tunnel. Its URL is the Agent API base the
// workspace must use for the duration of the Turn.
type AgentAPIProxy struct {
	URL     string
	Target  string
	Helper  string
	Attempt int

	session    *agentproxy.Session
	ssh        *ssh.Session
	connection *ssh.Client
	closeOnce  sync.Once
	closeErr   error
}

// Close ends the tunnel and releases the remote helper. Closing the SSH session
// is what stops the helper process: there is no orphan to hunt for afterwards.
func (proxy *AgentAPIProxy) Close() error {
	if proxy == nil {
		return nil
	}
	proxy.closeOnce.Do(func() {
		proxy.closeErr = proxy.session.Close()
		if proxy.ssh != nil {
			_ = proxy.ssh.Close()
		}
		if proxy.connection != nil {
			_ = proxy.connection.Close()
		}
	})
	return proxy.closeErr
}

// StartAgentAPIProxy starts the helper on the workspace and bridges its
// loopback port back to target.
//
// Each attempt uses its own SSH connection. A fresh connection is what makes a
// retry meaningful: a helper that failed to start leaves its session dead, and
// reusing it would only replay the same failure.
func StartAgentAPIProxy(ctx context.Context, item domain.Workspace, helperPath, target, scheme string) (*AgentAPIProxy, error) {
	runner, ok := RunnerFor(item).(*SSHRunner)
	if !ok || item.Transport != "ssh" {
		return nil, fmt.Errorf("Agent API stdio 代理仅支持 SSH 工作区")
	}
	command := shellQuote(helperPath) + fmt.Sprintf(" agent-proxy --stdio --protocol %d", agentproxy.Protocol)
	backoff := 200 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= AgentProxyAttempts; attempt++ {
		proxy, err := startAgentAPIProxyOnce(ctx, runner, command, helperPath, target, scheme, attempt)
		if err == nil {
			return proxy, nil
		}
		lastErr = err
		if attempt == AgentProxyAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w（最后一次错误：%v）", ctx.Err(), lastErr)
		case <-time.After(backoff):
		}
		backoff *= 3
	}
	return nil, fmt.Errorf("Agent API stdio 代理启动失败，已尝试 %d 次：%w", AgentProxyAttempts, lastErr)
}

func startAgentAPIProxyOnce(ctx context.Context, runner *SSHRunner, command, helperPath, target, scheme string, attempt int) (*AgentAPIProxy, error) {
	connection, err := runner.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败：%w", err)
	}
	session, err := connection.NewSession()
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("打开 SSH 会话失败：%w", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = connection.Close()
		return nil, fmt.Errorf("获取 helper stdin 失败：%w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = connection.Close()
		return nil, fmt.Errorf("获取 helper stdout 失败：%w", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		_ = connection.Close()
		return nil, fmt.Errorf("获取 helper stderr 失败：%w", err)
	}
	// stderr must be drained or a chatty helper blocks on a full channel while
	// the handshake is still in progress.
	diagnostics := &cappedBuffer{limit: 4096}
	go func() { _, _ = io.Copy(diagnostics, stderr) }()

	if err := session.Start(command); err != nil {
		_ = session.Close()
		_ = connection.Close()
		return nil, fmt.Errorf("启动远端 helper 失败：%w", err)
	}
	tunnel, err := agentproxy.StartSession(agentproxy.Stdio{
		Stdin:  stdin,
		Stdout: stdout,
		Wait:   session.Wait,
		Close:  func() error { return connection.Close() },
	}, target, scheme, AgentProxyReadyTimeout)
	if err != nil {
		_ = session.Close()
		_ = connection.Close()
		detail := strings.TrimSpace(diagnostics.String())
		if detail != "" {
			return nil, fmt.Errorf("%w（helper stderr：%s）", err, truncateForLog(detail, 300))
		}
		return nil, err
	}
	return &AgentAPIProxy{
		URL: tunnel.URL, Target: target, Helper: helperPath,
		Attempt: attempt, session: tunnel, ssh: session, connection: connection,
	}, nil
}

type cappedBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (buffer *cappedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.data = append(buffer.data, value...)
	if len(buffer.data) > buffer.limit {
		buffer.data = buffer.data[len(buffer.data)-buffer.limit:]
	}
	return len(value), nil
}

func (buffer *cappedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.data)
}

func truncateForLog(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// AgentAPIProxyTarget derives the AHA-side dial target and scheme from the Agent
// API base URL the workspace is configured with.
//
// Only loopback addresses qualify: any other address is already reachable from
// the workspace, so a tunnel would add a failure mode without removing one. The
// scheme comes back with the target because the tunnel carries bytes, not HTTP:
// an https base URL keeps TLS between the agent and AHA instead of silently
// downgrading to plaintext on the same port.
func AgentAPIProxyTarget(baseURL string) (string, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Hostname() == "" {
		return "", "", false
	}
	if !agentAPIHostIsLoopback(parsed.Hostname()) {
		return "", "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", false
	}
	port := parsed.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(parsed.Hostname(), port), scheme, true
}
