package channel

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	// channelHeartbeatTimeout is the longest a running plugin may go without a
	// health report before the supervisor treats it as hung. The plugin sends a
	// heartbeat every 30s.
	channelHeartbeatTimeout = 90 * time.Second
	// Startup includes building the Feishu client, fetching bot identity and
	// connecting the WebSocket, so it gets a wider grace period than a running
	// plugin.
	channelHeartbeatStartupGrace = 2 * channelHeartbeatTimeout
)

type managedPluginProcess struct {
	command   *exec.Cmd
	cancel    context.CancelFunc
	cleanup   func()
	startedAt time.Time
}

type pluginBootstrap struct {
	Schema         string `json:"schema"`
	Protocol       string `json:"protocol"`
	RuntimeBaseURL string `json:"runtime_base_url"`
	PluginID       string `json:"plugin_id"`
	ProviderKey    string `json:"provider_key"`
	InstanceID     string `json:"instance_id"`
	Capability     string `json:"capability"`
	AppID          string `json:"app_id,omitempty"`
	AppSecret      string `json:"app_secret,omitempty"`
	TenantBrand    string `json:"tenant_brand,omitempty"`
	Registration   bool   `json:"registration"`
}

type secretIPCMessage struct {
	Schema        string `json:"schema"`
	Type          string `json:"type"`
	InstanceID    string `json:"instance_id"`
	OnboardingID  string `json:"onboarding_id"`
	CommandID     string `json:"command_id"`
	LeaseID       string `json:"lease_id"`
	URL           string `json:"url,omitempty"`
	ExpireIn      int    `json:"expire_in,omitempty"`
	AppID         string `json:"app_id,omitempty"`
	AppSecret     string `json:"app_secret,omitempty"`
	ScannerOpenID string `json:"scanner_open_id,omitempty"`
	TenantBrand   string `json:"tenant_brand,omitempty"`
}

func (s *Service) reconcileProcesses(ctx context.Context) {
	s.processMu.Lock()
	ready := s.runtimeBaseURL != ""
	s.processMu.Unlock()
	if !ready {
		return
	}
	instances, err := s.store.ChannelInstances(ctx, "")
	if err != nil {
		return
	}
	for _, instance := range instances {
		if instance.EffectiveAvailability != "available" || (instance.Status != "onboarding" && instance.Status != "ready" && instance.Status != "degraded") {
			s.stopPluginProcess(instance.ID)
			continue
		}
		s.processMu.Lock()
		process := s.processes[instance.ID]
		retryAt := s.processRetry[instance.ID]
		s.processMu.Unlock()
		now := s.now().UTC()
		if process != nil {
			// Registration-only plugin runs do not start the channel heartbeat;
			// they are supervised by the onboarding flow and must not be
			// restarted by the liveness watchdog.
			if instance.Status != "onboarding" && channelHeartbeatSupported(instance.Config) && channelHeartbeatStale(process.startedAt, instance.LastSeenAt, now) {
				s.logger.Warn("channel plugin heartbeat timed out", "instance_id", instance.ID)
				_ = s.store.UpdateChannelInstanceHealth(ctx, instance.ID, "degraded", "channel_heartbeat_timeout", now)
				s.stopPluginProcess(instance.ID)
				s.scheduleProcessRetry(instance.ID, now)
			}
			continue
		}
		if retryAt.After(now) {
			continue
		}
		if instance.Status == "onboarding" && instance.CredentialConfigured {
			if _, ownerErr := s.store.ChannelOwnerIdentity(ctx, instance.ID); ownerErr != nil {
				continue
			}
		}
		if err := s.ensurePluginProcess(instance.ID, ""); err != nil {
			s.logger.Warn("channel plugin process start failed", "instance_id", instance.ID, "error", err)
		}
	}
}

// channelHeartbeatSupported keeps the liveness watchdog from restarting an
// older plugin that has not opted into heartbeats.
func channelHeartbeatSupported(config map[string]any) bool {
	value, _ := config["runtime_heartbeat_supported"].(string)
	return strings.TrimSpace(value) == "1"
}

// channelHeartbeatStale reports whether a running plugin has stopped reporting
// health. Startup gets a wider grace period before the first report is due.
func channelHeartbeatStale(startedAt, lastSeenAt, now time.Time) bool {
	if startedAt.IsZero() || now.Sub(startedAt) < channelHeartbeatStartupGrace {
		return false
	}
	if lastSeenAt.IsZero() {
		return true
	}
	return lastSeenAt.Before(startedAt) || now.Sub(lastSeenAt) > channelHeartbeatTimeout
}

// scheduleProcessRetry backs off a supervisor-initiated restart so a plugin
// that cannot reach the runtime is not killed and restarted every tick.
func (s *Service) scheduleProcessRetry(instanceID string, now time.Time) {
	s.processMu.Lock()
	s.processFailures[instanceID]++
	failures := s.processFailures[instanceID]
	delay := time.Second << minInt(failures-1, 8)
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	s.processRetry[instanceID] = now.Add(delay)
	s.processMu.Unlock()
}

func (s *Service) ensurePluginProcess(instanceID, rawCapability string) error {
	s.processMu.Lock()
	if _, ok := s.processes[instanceID]; ok {
		s.processMu.Unlock()
		return nil
	}
	runCtx, runtimeBaseURL := s.runCtx, s.runtimeBaseURL
	s.processMu.Unlock()
	if runCtx == nil || runtimeBaseURL == "" {
		return fmt.Errorf("channel runtime is not ready")
	}
	instance, err := s.store.ChannelInstance(runCtx, instanceID)
	if err != nil {
		return err
	}
	plugin, err := s.store.ChannelPlugin(runCtx, instance.PluginID)
	if err != nil || !plugin.Available {
		return fmt.Errorf("channel plugin is unavailable")
	}
	if rawCapability == "" {
		scopes := runtimeScopes()
		ttl := 24 * time.Hour
		if instance.Status == "onboarding" && !instance.CredentialConfigured {
			scopes, ttl = onboardingScopes(), 20*time.Minute
		}
		rawCapability, _, err = s.IssueCapability(runCtx, instance.ID, scopes, ttl)
		if err != nil {
			return err
		}
	}
	bootstrap := pluginBootstrap{
		Schema: "channel-secret/v1", Protocol: "channel-runtime/v1", RuntimeBaseURL: runtimeBaseURL,
		PluginID: plugin.ID, ProviderKey: plugin.ProviderKey, InstanceID: instance.ID, Capability: rawCapability,
		AppID: instance.AppID, TenantBrand: strings.TrimSpace(fmt.Sprint(instance.Config["tenant_brand"])),
	}
	bootstrap.Registration, err = s.registrationProcessRequired(runCtx, instance)
	if err != nil {
		return err
	}
	if instance.CredentialConfigured && instance.CredentialRef != "" && s.secrets != nil {
		bootstrap.AppSecret, _ = s.secrets.Get(instance.CredentialRef)
		if bootstrap.AppSecret == "" {
			return fmt.Errorf("channel credential is unavailable")
		}
	}
	processCtx, cancel := context.WithCancel(runCtx)
	command := exec.CommandContext(processCtx, plugin.ExecutablePath, "--channel-runtime")
	command.Dir = filepath.Dir(plugin.ExecutablePath)
	command.Env = minimalPluginEnv()
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		cancel()
		return err
	}
	if err := command.Start(); err != nil {
		cancel()
		return err
	}
	cleanup, err := attachPluginProcess(command)
	if err != nil {
		cancel()
		_ = command.Process.Kill()
		return fmt.Errorf("attach channel plugin process lifecycle: %w", err)
	}
	s.processMu.Lock()
	if existing := s.processes[instanceID]; existing != nil {
		s.processMu.Unlock()
		cancel()
		_ = command.Process.Kill()
		cleanup()
		return nil
	}
	s.processes[instanceID] = &managedPluginProcess{command: command, cancel: cancel, cleanup: cleanup, startedAt: s.now().UTC()}
	s.processMu.Unlock()
	go func() {
		defer stdin.Close()
		_ = json.NewEncoder(stdin).Encode(bootstrap)
	}()
	ipcDone := make(chan struct{})
	go func() {
		defer close(ipcDone)
		s.consumePluginSecretIPC(processCtx, instanceID, stdout)
	}()
	go s.consumePluginStderr(instanceID, stderr)
	go func() {
		// Stdout carries the one-time registration result, including the secret
		// that makes the new app usable. Drain it before Wait tears down the pipe
		// and before this goroutine cancels processCtx.
		_ = waitForPluginExit(ipcDone, command.Wait)
		unexpected := processCtx.Err() == nil
		cleanup()
		cancel()
		s.processMu.Lock()
		if current := s.processes[instanceID]; current != nil && current.command == command {
			delete(s.processes, instanceID)
		}
		s.processMu.Unlock()
		shouldBackoff := unexpected
		if unexpected {
			if current, currentErr := s.store.ChannelInstance(context.Background(), instanceID); currentErr == nil && current.Status == "onboarding" && current.CredentialConfigured && current.OwnerBound {
				shouldBackoff = false
			}
		}
		if shouldBackoff {
			s.processMu.Lock()
			s.processFailures[instanceID]++
			failures := s.processFailures[instanceID]
			delay := time.Second << minInt(failures-1, 8)
			if delay > 5*time.Minute {
				delay = 5 * time.Minute
			}
			s.processRetry[instanceID] = s.now().UTC().Add(delay)
			s.processMu.Unlock()
			s.logger.Warn("channel plugin process exited", "instance_id", instanceID)
			_ = s.store.UpdateChannelInstanceHealth(context.Background(), instanceID, "degraded", "plugin_process_exited", s.now().UTC())
		}
	}()
	return nil
}

func waitForPluginExit(ipcDone <-chan struct{}, wait func() error) error {
	<-ipcDone
	return wait()
}

func (s *Service) registrationProcessRequired(ctx context.Context, instance domain.ChannelInstance) (bool, error) {
	_, err := s.store.ActiveChannelOnboarding(ctx, instance.ID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func minimalPluginEnv() []string {
	allowed := map[string]bool{"PATH": true, "SYSTEMROOT": true, "WINDIR": true, "TEMP": true, "TMP": true, "HOME": true, "USERPROFILE": true}
	result := []string{}
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && allowed[strings.ToUpper(key)] {
			result = append(result, item)
		}
	}
	return result
}

func (s *Service) consumePluginSecretIPC(ctx context.Context, instanceID string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var message secretIPCMessage
		if json.Unmarshal(scanner.Bytes(), &message) != nil || message.Schema != "channel-secret/v1" || message.InstanceID != instanceID {
			continue
		}
		switch message.Type {
		case "verification_url":
			s.handleVerificationURL(ctx, message)
		case "registration_result":
			if err := s.completeRegistrationFromIPC(ctx, message); err == nil {
				go func() {
					time.Sleep(100 * time.Millisecond)
					s.stopPluginProcess(instanceID)
				}()
			} else {
				s.logger.Warn("channel registration result persistence failed", "instance_id", instanceID, "error", err)
			}
		}
	}
}

// consumePluginStderr keeps plugin failures observable. The previous code
// discarded stderr, which made a dead WebSocket or an expired capability
// invisible in the control plane. Keep the volume bounded: the first lines and
// every hundredth line after that.
func (s *Service) consumePluginStderr(instanceID string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	lines := 0
	for scanner.Scan() {
		lines++
		if lines > 20 && lines%100 != 0 {
			continue
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if len(line) > 1000 {
			line = line[:1000] + "…"
		}
		s.logger.Warn("channel plugin stderr", "instance_id", instanceID, "line", line)
	}
}

func (s *Service) handleVerificationURL(ctx context.Context, message secretIPCMessage) {
	if message.OnboardingID == "" || message.CommandID == "" || message.LeaseID == "" || !strings.HasPrefix(message.URL, "https://") {
		return
	}
	onboarding, err := s.store.ChannelOnboarding(ctx, message.OnboardingID)
	if err != nil || onboarding.InstanceID != message.InstanceID || onboarding.RegistrationCommandID != message.CommandID {
		return
	}
	expireIn := message.ExpireIn
	if expireIn < 1 || expireIn > 600 {
		expireIn = 600
	}
	ref := "channel/" + message.InstanceID + "/onboarding/" + message.OnboardingID + "/verification_url/" + stableID("lease", message.LeaseID)
	if s.secrets == nil || s.secrets.PutMany(map[string]string{ref: message.URL}) != nil {
		return
	}
	now := s.now().UTC()
	if s.store.UpdateChannelOnboardingQR(ctx, onboarding.ID, message.CommandID, message.LeaseID, ref, now.Add(time.Duration(expireIn)*time.Second), now) != nil {
		_ = s.secrets.DeleteMany([]string{ref})
		return
	}
	if onboarding.VerificationURLRef != "" && onboarding.VerificationURLRef != ref {
		_ = s.secrets.DeleteMany([]string{onboarding.VerificationURLRef})
	}
	_ = s.store.UpdateChannelCommandProgress(ctx, message.InstanceID, message.CommandID, message.LeaseID, map[string]any{"status": "qr_ready", "verification_url_ref": ref, "expires_at": now.Add(time.Duration(expireIn) * time.Second)}, now.Add(30*time.Second))
}

func (s *Service) stopPluginProcess(instanceID string) {
	s.processMu.Lock()
	process := s.processes[instanceID]
	if process != nil {
		delete(s.processes, instanceID)
	}
	s.processMu.Unlock()
	if process != nil {
		process.cancel()
		if process.command.Process != nil {
			_ = process.command.Process.Kill()
		}
	}
}
