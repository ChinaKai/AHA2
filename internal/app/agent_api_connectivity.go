package app

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

func (s *Service) AgentAPISettings(ctx context.Context) (domain.AgentAPISettings, error) {
	settings, err := s.store.AgentAPISettings(ctx)
	if err != nil {
		return domain.AgentAPISettings{}, err
	}
	settings.StartupURL = s.agentAPIURL
	settings.StartupAllowInsecure = s.agentAPIAllowInsecure
	settings.EffectiveURL = settings.URL
	if settings.EffectiveURL == "" {
		settings.EffectiveURL = settings.StartupURL
	}
	settings.EffectiveAllowInsecure = settings.AllowInsecure || settings.URL == "" && settings.StartupAllowInsecure
	return settings, nil
}

func (s *Service) UpdateAgentAPISettings(ctx context.Context, input domain.AgentAPISettings) (domain.AgentAPISettings, error) {
	value, err := normalizeAgentAPIBaseURL(input.URL, input.AllowInsecure)
	if err != nil {
		return domain.AgentAPISettings{}, err
	}
	input.URL = value
	input.UpdatedAt = time.Now().UTC()
	if _, err := s.store.UpdateAgentAPISettings(ctx, input); err != nil {
		return domain.AgentAPISettings{}, err
	}
	if err := s.store.ResetWorkspaceAgentAPIDetection(ctx, input.UpdatedAt); err != nil {
		return domain.AgentAPISettings{}, err
	}
	return s.AgentAPISettings(ctx)
}

func (s *Service) ValidateWorkspaceAgentAPI(ctx context.Context, mode, rawURL string) (string, string, error) {
	mode = normalizeAgentAPIMode(mode)
	if mode != "manual" {
		return mode, "", nil
	}
	settings, err := s.AgentAPISettings(ctx)
	if err != nil {
		return "", "", err
	}
	value, err := normalizeAgentAPIBaseURL(rawURL, settings.EffectiveAllowInsecure)
	if err != nil {
		return "", "", err
	}
	if value == "" {
		return "", "", fmt.Errorf("手动模式必须填写 Agent API URL")
	}
	return mode, value, nil
}

func (s *Service) AgentAPIURLForWorkspace(ctx context.Context, item domain.Workspace) (string, error) {
	settings, err := s.AgentAPISettings(ctx)
	if err != nil {
		return "", err
	}
	switch normalizeAgentAPIMode(item.AgentAPIMode) {
	case "manual":
		if item.AgentAPIURL != "" {
			return normalizeAgentAPIBaseURL(item.AgentAPIURL, settings.EffectiveAllowInsecure)
		}
	case "auto":
		if item.AgentAPIStatus == "ready" && item.AgentAPIResolvedURL != "" {
			return item.AgentAPIResolvedURL, nil
		}
	}
	return settings.EffectiveURL, nil
}

func (s *Service) DetectWorkspaceAgentAPI(ctx context.Context, item domain.Workspace) domain.Workspace {
	now := time.Now().UTC()
	item.AgentAPILastCheckedAt = now
	item.AgentAPIResolvedURL = ""
	item.AgentAPIStatus = "error"
	item.AgentAPIError = ""
	if item.Capabilities == nil {
		item.Capabilities = map[string]any{}
	}
	settings, err := s.AgentAPISettings(ctx)
	if err != nil {
		return markAgentAPIProbeFailure(item, "读取 Agent API 设置失败")
	}
	candidates := s.agentAPIProbeCandidates(ctx, item, settings)
	lastError := "没有可测试的 Agent API 地址"
	for _, candidate := range candidates {
		if err := workspacepkg.ProbeAgentAPI(ctx, item, candidate); err == nil {
			item.AgentAPIResolvedURL = candidate
			item.AgentAPIStatus = "ready"
			item.AgentAPIError = ""
			item.Capabilities["agent_api"] = map[string]any{"status": "ready", "url": candidate}
			return item
		} else {
			lastError = err.Error()
		}
	}
	return markAgentAPIProbeFailure(item, truncateAgentAPIError(lastError))
}

func markAgentAPIProbeFailure(item domain.Workspace, message string) domain.Workspace {
	item.AgentAPIStatus = "error"
	item.AgentAPIError = message
	item.Capabilities["agent_api"] = map[string]any{"status": "unavailable", "error": message}
	if item.Health == "ready" {
		item.Health = "degraded"
	}
	return item
}

func (s *Service) agentAPIProbeCandidates(ctx context.Context, item domain.Workspace, settings domain.AgentAPISettings) []string {
	mode := normalizeAgentAPIMode(item.AgentAPIMode)
	if mode == "manual" {
		return uniqueAgentAPIURLs(item.AgentAPIURL)
	}
	if mode == "global" {
		return uniqueAgentAPIURLs(settings.EffectiveURL)
	}
	result := uniqueAgentAPIURLs(settings.EffectiveURL)
	parsed, err := url.Parse(settings.EffectiveURL)
	if err != nil || parsed.Hostname() == "" {
		return result
	}
	if parsed.Scheme == "http" && (item.Transport == "native" || item.Transport == "wsl") {
		result = appendUniqueAgentAPIURL(result, agentAPIURLWithHost(parsed, "127.0.0.1"))
	}
	if parsed.Scheme != "http" || !settings.EffectiveAllowInsecure || item.Transport == "native" {
		return result
	}
	for _, host := range append(workspacepkg.AgentAPIHostCandidates(ctx, item), localAgentAPIHosts()...) {
		result = appendUniqueAgentAPIURL(result, agentAPIURLWithHost(parsed, host))
		if len(result) >= 8 {
			break
		}
	}
	return result
}

func normalizeAgentAPIMode(value string) string {
	switch strings.TrimSpace(value) {
	case "global", "manual":
		return strings.TrimSpace(value)
	default:
		return "auto"
	}
}

func normalizeAgentAPIBaseURL(raw string, allowInsecure bool) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("Agent API URL 必须是无凭据的 http:// 或 https:// 基址")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("Agent API URL 不能包含路径、查询或片段")
	}
	if parsed.Scheme == "http" && !agentAPIHostIsLoopback(parsed.Hostname()) && !allowInsecure {
		return "", fmt.Errorf("非 loopback HTTP Agent API 仅允许用于明确受信的开发网络")
	}
	return value, nil
}

func agentAPIHostIsLoopback(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	address := net.ParseIP(strings.TrimSpace(host))
	return address != nil && address.IsLoopback()
}

func localAgentAPIHosts() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	result := []string{}
	seen := map[string]bool{}
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := networkInterface.Addrs()
		for _, raw := range addresses {
			address, _, err := net.ParseCIDR(raw.String())
			if err != nil || address == nil || address.To4() == nil || address.IsLoopback() || address.IsLinkLocalUnicast() {
				continue
			}
			host := address.String()
			if !seen[host] {
				seen[host] = true
				result = append(result, host)
			}
		}
	}
	return result
}

func agentAPIURLWithHost(base *url.URL, host string) string {
	port := base.Port()
	hostPort := host
	if port != "" {
		hostPort = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		hostPort = "[" + host + "]"
	}
	return base.Scheme + "://" + hostPort
}

func uniqueAgentAPIURLs(values ...string) []string {
	result := []string{}
	for _, value := range values {
		result = appendUniqueAgentAPIURL(result, value)
	}
	return result
}

func appendUniqueAgentAPIURL(values []string, value string) []string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}

func truncateAgentAPIError(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 240 {
		return string(runes[:239]) + "…"
	}
	return value
}
