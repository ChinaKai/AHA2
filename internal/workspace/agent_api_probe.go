package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func ProbeAgentAPI(ctx context.Context, item domain.Workspace, baseURL string) error {
	return probeAgentAPIWithRunner(ctx, item, baseURL, RunnerFor(item))
}

func probeAgentAPIWithRunner(ctx context.Context, item domain.Workspace, baseURL string, runner Runner) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("Agent API URL 为空")
	}
	healthURL := baseURL + "/healthz"
	var body string
	if item.Transport == "native" {
		client := &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 4097))
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK || len(data) > 4096 {
			return fmt.Errorf("healthz 返回 HTTP %d", response.StatusCode)
		}
		body = string(data)
	} else {
		result, err := runDetectionCommand(ctx, item, runner, Command{
			Executable: "sh",
			Args: []string{"-c", `set -eu
export NO_PROXY='*' no_proxy='*'
probe_url="$1/healthz"
if command -v curl >/dev/null 2>&1; then
  exec curl --noproxy '*' -fsS --connect-timeout 3 --max-time 5 "$probe_url"
fi
if command -v wget >/dev/null 2>&1; then
  exec wget -qO- -T 5 "$probe_url"
fi
if command -v python3 >/dev/null 2>&1; then
  exec python3 -c 'import sys,urllib.request; print(urllib.request.urlopen(sys.argv[1], timeout=5).read(4097).decode())' "$probe_url"
fi
if command -v python >/dev/null 2>&1; then
  exec python -c 'import sys,urllib.request; print(urllib.request.urlopen(sys.argv[1], timeout=5).read(4097).decode())' "$probe_url"
fi
echo 'curl、wget 或 Python 均不可用' >&2
exit 127`, "aha-agent-api-probe", baseURL},
			Dir: item.RootPath, Timeout: 8 * time.Second, OutputLimit: 4096,
		})
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			detail := strings.TrimSpace(result.Stderr)
			if detail == "" {
				detail = fmt.Sprintf("exit code %d", result.ExitCode)
			}
			return fmt.Errorf("healthz 请求失败: %s", detail)
		}
		body = result.Stdout
	}
	var health struct {
		OK      bool   `json:"ok"`
		Service string `json:"service"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &health); err != nil || !health.OK || health.Service != "aha2" {
		return fmt.Errorf("healthz 响应不是 AHA2")
	}
	return nil
}

func AgentAPIHostCandidates(ctx context.Context, item domain.Workspace) []string {
	return agentAPIHostCandidatesWithRunner(ctx, item, RunnerFor(item))
}

func agentAPIHostCandidatesWithRunner(ctx context.Context, item domain.Workspace, runner Runner) []string {
	if item.Transport != "wsl" && item.Transport != "ssh" {
		return nil
	}
	script := `set -eu
if [ "${1:-}" = "ssh" ]; then
  set -- ${SSH_CONNECTION:-}
  [ "$#" -ge 1 ] && printf '%s\n' "$1"
  exit 0
fi
if command -v ip >/dev/null 2>&1; then
  ip route show default 2>/dev/null | awk 'NR==1 {print $3}'
fi
awk '/^nameserver[[:space:]]+/ {print $2; exit}' /etc/resolv.conf 2>/dev/null || true`
	result, err := runDetectionCommand(ctx, item, runner, Command{
		Executable: "sh", Args: []string{"-c", script, "aha-agent-api-hosts", item.Transport},
		Dir: item.RootPath, Timeout: 8 * time.Second, OutputLimit: 4096,
	})
	if err != nil || result.ExitCode != 0 {
		return nil
	}
	seen := map[string]bool{}
	resultHosts := []string{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		host := strings.TrimSpace(line)
		if host == "" || seen[host] {
			continue
		}
		address := net.ParseIP(host)
		if address == nil || address.IsUnspecified() {
			continue
		}
		host = address.String()
		if seen[host] {
			continue
		}
		seen[host] = true
		resultHosts = append(resultHosts, host)
	}
	return resultHosts
}
