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

// agentAPIStdioDetectTimeout bounds the whole stdio detection stage: helper
// materialisation (which may upload the script), up to three SSH attempts, and
// the health probe. Detection runs inside another request, so a workspace that
// accepts SSH but never answers must not hold the endpoint open indefinitely.
const agentAPIStdioDetectTimeout = 90 * time.Second

func (s *Service) AgentAPISettings(ctx context.Context) (domain.AgentAPISettings, error) {
	settings, err := s.store.AgentAPISettings(ctx)
	if err != nil {
		return domain.AgentAPISettings{}, err
	}
	// StartupURL is the address derived from the listen address: HTTPS when AHA
	// is configured behind a reverse proxy, loopback otherwise. It is the address
	// the automatic paths target; the stored URL is only the troubleshooting
	// override and must never displace it silently.
	settings.StartupURL = s.agentAPIURL
	// Defensive: a value could arrive from sync or a database written by an older
	// build between migrations. An address outside the boundary is dropped here
	// too, so resolution can never inject one just because it was stored.
	if settings.URL != "" {
		if value, err := normalizeAgentAPIBaseURL(settings.URL); err == nil {
			settings.URL = value
		} else {
			settings.URL = ""
		}
	}
	settings.EffectiveURL = settings.URL
	if settings.EffectiveURL == "" {
		settings.EffectiveURL = settings.StartupURL
	}
	return settings, nil
}

// UpdateAgentAPISettings writes the hidden manual override.
//
// Only https:// and loopback http:// survive normalization, so the override can
// not become a second way to reach AHA over plaintext LAN. Saving a value also
// retires the migration note: the operator has made a deliberate choice, and a
// stale notice about an address they replaced would be noise.
func (s *Service) UpdateAgentAPISettings(ctx context.Context, input domain.AgentAPISettings) (domain.AgentAPISettings, error) {
	value, err := normalizeAgentAPIBaseURL(input.URL)
	if err != nil {
		return domain.AgentAPISettings{}, err
	}
	input.URL = value
	input.MigrationNote = ""
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
	value, err := normalizeAgentAPIBaseURL(rawURL)
	if err != nil {
		return "", "", err
	}
	if value == "" {
		return "", "", fmt.Errorf("手动模式必须填写 Agent API URL")
	}
	return mode, value, nil
}

// AgentAPIURLForWorkspace resolves the address a Turn injects into the workspace.
//
// A successful automatic detection wins, then the workspace's legacy manual
// value, then the global override. Detection is what records the automatic
// choice; when it has not run the fallbacks keep the Turn working rather than
// guessing at a path that was never verified.
//
// An SSH workspace whose automatic choice has never been verified is detected
// here, before the Turn starts. The loopback fallback only works when sshd
// allows a reverse tunnel; when it does not, the Turn would otherwise fail on a
// path detection could have replaced with the controlled LAN fallback. A
// successful result is stored so later Turns reuse it; a failed probe leaves
// the stored state and the normal fallback untouched.
func (s *Service) AgentAPIURLForWorkspace(ctx context.Context, item domain.Workspace) (string, error) {
	settings, err := s.AgentAPISettings(ctx)
	if err != nil {
		return "", err
	}
	if item.AgentAPIStatus == "ready" && item.AgentAPIResolvedURL != "" && !agentAPIDetectionUsedStdio(item) {
		// Detection may have selected the controlled SSH fallback (AHA's own LAN
		// address over plaintext after the tunnel failed), so the stored result is
		// validated against the probe boundary rather than the config boundary.
		return normalizeAgentAPIProbeResult(item, item.AgentAPIResolvedURL)
	}
	// A stdio detection result is not a cached address: it records that the
	// helper carried the probe to AHA's loopback, while the reverse tunnel may
	// never have been tested. If stdio stops working later (node removed, helper
	// blocked), trusting the stored loopback would pick the tunnel without
	// checking it. Falling through re-runs detection, which then selects the
	// verified tunnel or LAN path.
	if normalizeAgentAPIMode(item.AgentAPIMode) == "manual" && item.AgentAPIURL != "" {
		return normalizeAgentAPIBaseURL(item.AgentAPIURL)
	}
	if value, ok := s.detectAgentAPIForTurn(ctx, item); ok {
		return value, nil
	}
	return settings.EffectiveURL, nil
}

// detectAgentAPIForTurn probes an SSH workspace whose automatic address has not
// been verified, and reports the address when detection found one.
//
// Detection is deliberately limited to "auto" on SSH: manual and native/WSL
// workspaces already have a working path, and probing them here would add a
// workspace round trip without changing the outcome. On success the verified
// result is cached on the workspace, because the probe costs a workspace
// connection and every Turn would otherwise repeat it. A write failure is
// ignored: the probe already proved this address works.
func (s *Service) detectAgentAPIForTurn(ctx context.Context, item domain.Workspace) (string, bool) {
	if normalizeAgentAPIMode(item.AgentAPIMode) != "auto" || item.Transport != "ssh" {
		return "", false
	}
	detected := s.DetectWorkspaceAgentAPI(ctx, item)
	if detected.AgentAPIStatus != "ready" || detected.AgentAPIResolvedURL == "" {
		return "", false
	}
	// Validate before caching: a result outside the probe boundary is not usable,
	// and the caller must keep its normal fallback rather than fail on it.
	value, err := normalizeAgentAPIProbeResult(detected, detected.AgentAPIResolvedURL)
	if err != nil {
		return "", false
	}
	detected.UpdatedAt = time.Now().UTC()
	_ = s.store.UpdateWorkspaceAgentAPIDetection(ctx, detected)
	return value, true
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
	if len(candidates) == 0 {
		return markAgentAPIProbeFailure(item, "没有可测试的 Agent API 地址")
	}
	failures := make([]string, 0, len(candidates)+1)
	stages := make([]string, 0, len(candidates)+1)
	seenStages := map[string]bool{}
	// The stdio transport is tried first for every SSH workspace: it needs only
	// SSH itself, not sshd port forwarding or a reachable LAN address. A failure
	// here is recorded and the tunnel/LAN candidates run exactly as before.
	stdioFailure := ""
	if base, ok := agentAPIStdioDetectionBase(item, settings); ok {
		budget := agentAPIStdioDetectTimeout
		if s.agentAPIStdioProbeBudget > 0 {
			budget = s.agentAPIStdioProbeBudget
		}
		stdioCtx, cancel := context.WithTimeout(ctx, budget)
		result, stdioErr := s.probeAgentAPIOverStdioForDetection(stdioCtx, item, base)
		cancel()
		if stdioErr == nil {
			item.AgentAPIResolvedURL = base
			item.AgentAPIStatus = "ready"
			item.AgentAPIError = ""
			// A working automatic path supersedes the migration warning.
			item.AgentAPIMigrationNote = ""
			reachability := map[string]any{
				"status": "ready", "url": base, "via": "stdio", "runtime": result.Runtime,
			}
			// The proxy URL is the address that actually answered during this
			// detection. It is an observation, not a stored endpoint: every Turn
			// starts a new helper on a new port.
			if result.URL != "" {
				reachability["proxy_url"] = result.URL
			}
			item.Capabilities["agent_api"] = reachability
			return item
		}
		stdioFailure = truncateAgentAPIError(stdioErr.Error())
		stages = append(stages, "SSH stdio")
		failures = append(failures, "SSH stdio："+stdioErr.Error())
	}
	// tunnelFailure makes the rejected tunnel visible even when a later LAN
	// candidate wins: the fallback log names why the preferred path was not used
	// instead of only which address was picked.
	tunnelFailure := ""
	for _, candidate := range candidates {
		// Probe the way a Turn will reach it, tunnel included. A bare probe of a
		// remote workspace's configured loopback address always fails, because that
		// address is the workspace itself; the tunnel is what makes it work. Testing
		// without it reported a fault for workspaces that were fine.
		err := s.probeAgentAPI(ctx, item, candidate)
		if err == nil {
			item.AgentAPIResolvedURL = candidate
			item.AgentAPIStatus = "ready"
			item.AgentAPIError = ""
			// A working automatic path supersedes the migration warning.
			item.AgentAPIMigrationNote = ""
			// Record how it was reached, not just that it was. For a remote workspace
			// the stored address is AHA's own loopback — the value that *triggers* the
			// tunnel — so presenting it as an address the workspace dials would claim
			// something untrue. The address is kept as-is because the tunnel gate keys
			// off exactly this loopback form; only the label distinguishes them.
			reachability := map[string]any{
				"status": "ready", "url": candidate, "via": agentAPIReachability(item, candidate),
			}
			if stdioFailure != "" {
				reachability["stdio_error"] = stdioFailure
			}
			if tunnelFailure != "" {
				reachability["tunnel_error"] = tunnelFailure
			}
			item.Capabilities["agent_api"] = reachability
			return item
		}
		// Name the step that failed (tunnel, LAN plaintext, override). Without this
		// the operator sees only the last error and cannot tell which path was even
		// attempted. The stage summary comes first so truncation cannot hide which
		// paths were tried. Probe errors never carry the token.
		label := agentAPIProbeCandidateLabel(item, settings, candidate)
		// The tunnel is any loopback candidate on an SSH workspace, including one
		// that came from the stored override: the display label says "覆盖地址"
		// there, but the probe still had to go through the reverse forward. Keying
		// off the label lost the refusal reason in exactly that case.
		if tunnelFailure == "" && agentAPICandidateIsTunnel(item, candidate) {
			tunnelFailure = fmt.Sprintf("隧道（%s）：%s", candidate, truncateAgentAPIError(err.Error()))
		}
		if !seenStages[label] {
			seenStages[label] = true
			stages = append(stages, label)
		}
		failures = append(failures, fmt.Sprintf("%s（%s）：%s", label, candidate, err.Error()))
	}
	summary := strings.Join(stages, " → ") + " 均不可达"
	return markAgentAPIProbeFailure(item, truncateAgentAPIError(summary+"；"+strings.Join(failures, "；")))
}

// probeAgentAPI is the detection side of the tunnel path; tests substitute the
// field so they can observe the order without a real SSH host.
func (s *Service) probeAgentAPI(ctx context.Context, item domain.Workspace, candidate string) error {
	if s.agentAPIProbeFn != nil {
		return s.agentAPIProbeFn(ctx, item, candidate)
	}
	return workspacepkg.ProbeAgentAPIThroughForward(ctx, item, candidate)
}

// agentAPIDetectionUsedStdio reports whether the recorded detection result was
// produced by the stdio transport. Its address is AHA's loopback, reached
// through the helper, so it says nothing about the reverse tunnel.
func agentAPIDetectionUsedStdio(item domain.Workspace) bool {
	probe, _ := item.Capabilities["agent_api"].(map[string]any)
	via, _ := probe["via"].(string)
	return via == "stdio"
}

// probeAgentAPIOverStdioForDetection is the detection side of the stdio path.
// It materialises the helper in the workspace root (there is no Task work
// directory in this flow), reusing one left by an earlier Turn or detection
// when its version and digest still match.
func (s *Service) probeAgentAPIOverStdioForDetection(ctx context.Context, item domain.Workspace, baseURL string) (workspacepkg.AgentAPIStdioResult, error) {
	if s.agentAPIStdioProbeFn != nil {
		return s.agentAPIStdioProbeFn(ctx, item, baseURL)
	}
	// No backend hint: detection has no runtime snapshot. The workspace's PATH
	// is the only node source here; a Turn may still find node through its
	// backend shebang, which is why a failed detection is not proof that stdio
	// will fail for every Turn.
	return workspacepkg.ProbeAgentAPIOverStdio(ctx, item, baseURL, item.RootPath, "", s.buildVersion, "")
}

// agentAPIBaseURL is the address the workspace is configured to reach. A manual
// override wins over the derived startup address, matching the resolution order
// a Turn already used.
func agentAPIBaseURL(item domain.Workspace, settings domain.AgentAPISettings) string {
	if normalizeAgentAPIMode(item.AgentAPIMode) == "manual" && item.AgentAPIURL != "" {
		return item.AgentAPIURL
	}
	return settings.EffectiveURL
}

// agentAPIStdioDetectionBase reports whether stdio detection applies and which
// base address it should proxy. It mirrors the Turn gate: only SSH, and only a
// loopback base, because a workspace that can dial any other address itself
// gains nothing from an extra tunnel.
func agentAPIStdioDetectionBase(item domain.Workspace, settings domain.AgentAPISettings) (string, bool) {
	if item.Transport != "ssh" {
		return "", false
	}
	base := agentAPIBaseURL(item, settings)
	if _, _, ok := workspacepkg.AgentAPIProxyTarget(base); !ok {
		return "", false
	}
	return base, true
}

// agentAPIProbeCandidateLabel names the step a candidate belongs to, so a
// failure report distinguishes 隧道 → LAN 明文 → 覆盖地址.
func agentAPIProbeCandidateLabel(item domain.Workspace, settings domain.AgentAPISettings, candidate string) string {
	candidate = strings.TrimRight(strings.TrimSpace(candidate), "/")
	if settings.URL != "" && strings.EqualFold(strings.TrimRight(strings.TrimSpace(settings.URL), "/"), candidate) {
		return "覆盖地址"
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Hostname() == "" {
		return "候选"
	}
	if item.Transport != "ssh" {
		return "直连"
	}
	if agentAPIHostIsLoopback(parsed.Hostname()) {
		return "隧道"
	}
	if parsed.Scheme == "http" {
		return "LAN 明文"
	}
	return "直连"
}

// agentAPICandidateIsTunnel reports whether this candidate is reachable only
// through the reverse tunnel: an SSH workspace and an AHA-loopback address.
// The display label cannot answer this, because an override address carries the
// "覆盖地址" label even when it is loopback.
func agentAPICandidateIsTunnel(item domain.Workspace, candidate string) bool {
	if item.Transport != "ssh" {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(candidate))
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	return agentAPIHostIsLoopback(parsed.Hostname())
}

// agentAPIReachability describes how the workspace reaches AHA, for display.
//
// "tunnel" means the address is AHA's loopback, which the workspace cannot dial
// itself: it is the target the reverse tunnel forwards to, and the Agent is given
// a per-Turn port instead. Reporting that address without this distinction reads
// as "the workspace connects to 127.0.0.1", which is never true for a remote
// workspace and sends anyone debugging it looking in the wrong place.
func agentAPIReachability(item domain.Workspace, resolvedURL string) string {
	if item.Transport != "ssh" {
		return "direct"
	}
	parsed, err := url.Parse(resolvedURL)
	if err != nil {
		return "direct"
	}
	if agentAPIHostIsLoopback(parsed.Hostname()) {
		return "tunnel"
	}
	return "direct"
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

// agentAPIProbeCandidates lists the addresses detection will try, in order.
//
// The order is the connection policy: the transport's own path first (native
// loopback, an SSH reverse tunnel to AHA's loopback, WSL localhost forwarding),
// then the hidden manual override as a troubleshooting fallback. Every candidate
// goes through normalizeAgentAPIBaseURL, so a plaintext address off loopback can
// never be selected automatically — with one controlled exception: an SSH
// workspace whose tunnel probe failed may fall back to AHA's own LAN address
// over plaintext, built only from this host's addresses in the SSH branch below.
// No stored setting, startup value, native, or WSL path can reach that relaxation.
func (s *Service) agentAPIProbeCandidates(ctx context.Context, item domain.Workspace, settings domain.AgentAPISettings) []string {
	result := []string{}
	appendCandidate := func(raw string) {
		value, err := normalizeAgentAPIBaseURL(raw)
		if err == nil && value != "" {
			result = appendUniqueAgentAPIURL(result, value)
		}
	}
	// appendPlaintextLANCandidate is reachable only from the SSH fallback: the
	// relaxed validator accepts a private LAN plaintext address, and the caller
	// only ever supplies AHA's own private IPv4 addresses.
	appendPlaintextLANCandidate := func(raw string) {
		value, err := normalizeAgentAPIPlaintextCandidate(raw)
		if err == nil && value != "" {
			result = appendUniqueAgentAPIURL(result, value)
		}
	}

	base := strings.TrimSpace(s.agentAPIURL)
	parsed, parseErr := url.Parse(base)
	loopbackBase := parseErr == nil && parsed.Hostname() != "" && agentAPIHostIsLoopback(parsed.Hostname())
	switch item.Transport {
	case "native":
		appendCandidate(base)
	case "wsl":
		// AHA runs on the Windows host. WSL2 forwards loopback to the host, so
		// 127.0.0.1 and localhost are the paths that work without exposing AHA to
		// the network. The host gateway is only usable when the base is HTTPS:
		// plaintext on a non-loopback address is not a permitted state.
		if loopbackBase && parsed.Scheme == "http" {
			appendCandidate(agentAPIURLWithHost(parsed, "127.0.0.1"))
			appendCandidate(agentAPIURLWithHost(parsed, "localhost"))
		} else {
			appendCandidate(base)
		}
		if parseErr == nil && parsed.Scheme == "https" {
			for _, host := range s.agentAPIWorkspaceHostCandidates(ctx, item) {
				appendCandidate(agentAPIURLWithHost(parsed, host))
			}
		}
	case "ssh":
		// 1. AHA's loopback is the first attempt; probing it opens the reverse
		// tunnel, which is the preferred path when the workspace's sshd allows it.
		appendCandidate(base)
		// 2. Controlled plaintext fallback. Only an SSH workspace with a loopback
		// HTTP base can need it: the tunnel just failed, so the remaining path is
		// AHA's own LAN address. Both sources describe this host — its interface
		// addresses, and the address the workspace's sshd sees AHA connect from.
		// Native/WSL run on this host and have their own paths; a stored or startup
		// plaintext address never reaches this branch.
		if loopbackBase && parsed.Scheme == "http" {
			hosts := append([]string{}, s.agentAPIWorkspaceHostCandidates(ctx, item)...)
			hosts = append(hosts, s.agentAPILocalHosts()...)
			for _, host := range hosts {
				// Bound detection time on hosts with many interfaces; probing each
				// candidate costs a workspace-side request.
				if len(result) >= 8 {
					break
				}
				appendPlaintextLANCandidate(agentAPIURLWithHost(parsed, host))
			}
		}
	default:
		appendCandidate(base)
	}

	// Hidden manual override, tried only after the automatic paths.
	if normalizeAgentAPIMode(item.AgentAPIMode) == "manual" {
		appendCandidate(item.AgentAPIURL)
	}
	appendCandidate(settings.URL)
	return result
}

// normalizeAgentAPIMode maps a stored mode onto the two that still exist.
//
// "global" meant "inherit the global default", which was a second way to express
// what auto already does: both end at the same effective URL, and the global value
// is itself derived from the listen address. It is folded into "auto" here rather
// than rejected, so a row that still carries it keeps working if a migration has
// not run yet — but its effective behaviour is unchanged, because the resolution
// chain already fell through to the same place.
func normalizeAgentAPIMode(value string) string {
	switch strings.TrimSpace(value) {
	case "manual":
		return "manual"
	default:
		return "auto"
	}
}

// normalizeAgentAPIBaseURL enforces the Agent API address boundary: https://
// anywhere, plaintext http:// only on loopback.
//
// There is deliberately no "allow insecure" escape hatch. A plaintext token on a
// shared network is interceptable even when the network is believed to be
// trusted, and the reverse tunnel already removes the need for a LAN address on
// remote workspaces.
func normalizeAgentAPIBaseURL(raw string) (string, error) {
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
	if parsed.Scheme == "http" && !agentAPIHostIsLoopback(parsed.Hostname()) {
		return "", fmt.Errorf("非 loopback 的明文 HTTP Agent API 地址已禁用；请使用 https:// 或 loopback 地址")
	}
	return value, nil
}

// normalizeAgentAPIPlaintextCandidate is the probe-only relaxation of the Agent
// API boundary. It accepts a plaintext address off loopback, but only a private
// IPv4 literal: candidates are built from AHA's own interface enumeration or the
// SSH peer's view, never from a hostname, a public address, or a user-supplied
// URL. It must not be used for stored settings, startup configuration, or
// native/WSL candidates.
func normalizeAgentAPIPlaintextCandidate(raw string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Scheme != "http" {
		return "", fmt.Errorf("Agent API 探测候选必须是无凭据的 http:// 基址")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("Agent API 探测候选不能包含路径、查询或片段")
	}
	address := net.ParseIP(parsed.Hostname())
	if address == nil || address.To4() == nil || address.IsLoopback() || address.IsUnspecified() || address.IsLinkLocalUnicast() || !address.IsPrivate() {
		return "", fmt.Errorf("Agent API 明文探测候选必须是本机私网 LAN IPv4 地址")
	}
	return value, nil
}

// normalizeAgentAPIProbeResult validates a stored automatic-detection result.
//
// Only SSH may carry the controlled plaintext LAN fallback; every other
// transport and every configured value stays under the strict boundary.
func normalizeAgentAPIProbeResult(item domain.Workspace, raw string) (string, error) {
	value, err := normalizeAgentAPIBaseURL(raw)
	if err == nil || item.Transport != "ssh" {
		return value, err
	}
	return normalizeAgentAPIPlaintextCandidate(raw)
}

func (s *Service) agentAPILocalHosts() []string {
	if s.agentAPILocalHostsFn != nil {
		return s.agentAPILocalHostsFn()
	}
	return localAgentAPIHosts()
}

func (s *Service) agentAPIWorkspaceHostCandidates(ctx context.Context, item domain.Workspace) []string {
	if s.agentAPIWorkspaceHostsFn != nil {
		return s.agentAPIWorkspaceHostsFn(ctx, item)
	}
	return workspacepkg.AgentAPIHostCandidates(ctx, item)
}

// localAgentAPIHosts lists the IPv4 addresses this host can be reached at from
// the LAN. Loopback and link-local addresses are excluded: the former is already
// the tunnel candidate, and neither is what a remote workspace needs.
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

func agentAPIHostIsLoopback(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	address := net.ParseIP(strings.TrimSpace(host))
	return address != nil && address.IsLoopback()
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

// agentAPICapabilityTTL sizes the Agent API credential to the work it authorises.
//
// A credential must not expire before the unit of work it serves. The previous
// fixed four hours was shorter than the default ten-hour execution limit (and far
// shorter than the 168-hour maximum), so a long Turn would lose API access partway
// through — every call would start failing while the Turn itself kept running,
// which reads as the Agent misbehaving rather than as an expired credential.
func (s *Service) agentAPICapabilityTTL(ctx context.Context) time.Duration {
	seconds := domain.DefaultBackendTurnTimeoutSeconds
	if settings, err := s.store.BackendSettings(ctx); err == nil && settings.TurnTimeoutSeconds > 0 {
		seconds = settings.TurnTimeoutSeconds
	}
	ttl := time.Duration(seconds) * time.Second
	// A margin covers the gap between issuing the token and the backend process
	// starting, plus the tail of a Turn that runs to its limit.
	ttl += 15 * time.Minute
	if ttl < time.Hour {
		ttl = time.Hour
	}
	return ttl
}

// agentAPIReverseForward decides whether this Turn needs AHA carried into the
// workspace.
//
// Only a transport that both cannot dial AHA and can carry a forward qualifies.
// A native workspace runs on this host and reaches loopback directly; a workspace
// whose configured address already reaches AHA needs nothing. This is best-effort
// by design: when no forward is possible the Turn still runs with its configured
// address, which is exactly today's behaviour.
func agentAPIReverseForward(item domain.Workspace, agentAPIURL string) *workspacepkg.ReverseForward {
	if item.Transport != "ssh" {
		return nil
	}
	parsed, err := url.Parse(agentAPIURL)
	if err != nil || parsed.Hostname() == "" {
		return nil
	}
	// An address on the workspace's own loopback cannot be reached by the workspace
	// itself, so it must be replaced. Any other address is assumed reachable: AHA
	// cannot know otherwise from here, and guessing wrong would break a working setup.
	if !agentAPIHostIsLoopback(parsed.Hostname()) {
		return nil
	}
	return &workspacepkg.ReverseForward{
		Target:  net.JoinHostPort(parsed.Hostname(), parsed.Port()),
		EnvName: "AHA2_AGENT_API_URL",
	}
}
