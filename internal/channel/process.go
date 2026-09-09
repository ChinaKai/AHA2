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

type managedPluginProcess struct {
	command *exec.Cmd
	cancel  context.CancelFunc
	cleanup func()
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
		_, running := s.processes[instance.ID]
		retryAt := s.processRetry[instance.ID]
		s.processMu.Unlock()
		if running || retryAt.After(s.now().UTC()) {
			continue
		}
		if instance.Status == "onboarding" && instance.CredentialConfigured {
			if _, ownerErr := s.store.ChannelOwnerIdentity(ctx, instance.ID); ownerErr != nil {
				continue
			}
		}
		if instance.ProviderKey == "feishu" && instance.CredentialConfigured && instance.OwnerBound {
			key := stableID("configure_menu", instance.ID, instance.CredentialRef, "v6")
			_, _ = s.EnqueueCommand(ctx, instance.ID, "configure_menu", key, map[string]any{"version": 6})
		}
		if err := s.ensurePluginProcess(instance.ID, ""); err != nil {
			s.logger.Warn("channel plugin process start failed", "instance_id", instance.ID, "error", err)
		}
	}
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
	s.processes[instanceID] = &managedPluginProcess{command: command, cancel: cancel, cleanup: cleanup}
	s.processMu.Unlock()
	go func() {
		defer stdin.Close()
		_ = json.NewEncoder(stdin).Encode(bootstrap)
	}()
	go s.consumePluginSecretIPC(processCtx, instanceID, stdout)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() {
		_ = command.Wait()
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

func (s *Service) registrationProcessRequired(ctx context.Context, instance domain.ChannelInstance) (bool, error) {
	if instance.Status != "onboarding" {
		return false, nil
	}
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
			if s.completeRegistrationFromIPC(ctx, message) == nil {
				go func() {
					time.Sleep(100 * time.Millisecond)
					s.stopPluginProcess(instanceID)
				}()
			}
		}
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
	ref := "channel/" + message.InstanceID + "/onboarding/" + message.OnboardingID + "/verification_url"
	if s.secrets == nil || s.secrets.PutMany(map[string]string{ref: message.URL}) != nil {
		return
	}
	now := s.now().UTC()
	if s.store.UpdateChannelOnboardingQR(ctx, onboarding.ID, message.CommandID, ref, now.Add(time.Duration(expireIn)*time.Second), now) != nil {
		_ = s.secrets.DeleteMany([]string{ref})
		return
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
