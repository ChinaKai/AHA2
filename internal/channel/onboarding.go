package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func (s *Service) StartOnboarding(ctx context.Context, ownerID, ownerSessionID, instanceID, idempotencyKey string) (domain.ChannelOnboardingSession, error) {
	instance, err := s.store.ChannelInstance(ctx, instanceID)
	if err != nil || instance.OwnerID != ownerID {
		if err == nil {
			err = sql.ErrNoRows
		}
		return domain.ChannelOnboardingSession{}, err
	}
	plugin, err := s.store.ChannelPlugin(ctx, instance.PluginID)
	if err != nil || !plugin.Available {
		return domain.ChannelOnboardingSession{}, fmt.Errorf("one_click_registration_unavailable")
	}
	s.processMu.Lock()
	runtimeReady := s.runtimeBaseURL != "" && s.runCtx != nil
	s.processMu.Unlock()
	if !runtimeReady {
		return domain.ChannelOnboardingSession{}, fmt.Errorf("channel runtime is not ready")
	}
	now := s.now().UTC()
	onboarding := domain.ChannelOnboardingSession{
		ID: stableID("channel_onboarding", ownerID, instance.ID, idempotencyKey), InstanceID: instance.ID, OwnerSessionID: ownerSessionID, Mode: "register_app",
		Status: "pending", Step: "starting_registration", ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	if instance.CredentialConfigured {
		onboarding.Mode = "existing_app"
	}
	if existing, existingErr := s.store.ChannelOnboarding(ctx, onboarding.ID); existingErr == nil {
		if existing.InstanceID != instance.ID || existing.OwnerSessionID != ownerSessionID {
			return domain.ChannelOnboardingSession{}, fmt.Errorf("idempotency key was already used with a different request")
		}
		if existing.Status == "pending" || existing.Status == "qr_ready" {
			scopes := onboardingScopes()
			if instance.CredentialConfigured {
				scopes = reauthorizationScopes()
			}
			raw, _, issueErr := s.IssueCapability(ctx, instance.ID, scopes, 20*time.Minute)
			if issueErr == nil {
				s.stopPluginProcess(instance.ID)
				_ = s.ensurePluginProcess(instance.ID, raw)
			}
		}
		return existing, nil
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		return domain.ChannelOnboardingSession{}, existingErr
	}
	refs, err := s.store.CancelActiveChannelOnboardings(ctx, instance.ID, now)
	if err != nil {
		return domain.ChannelOnboardingSession{}, err
	}
	if s.secrets != nil && len(refs) > 0 {
		_ = s.secrets.DeleteMany(refs)
	}
	command, err := s.EnqueueCommand(ctx, instance.ID, "register_app", "register_app:"+onboarding.ID, map[string]any{
		"onboarding_id": onboarding.ID, "app_name": instance.Name, "app_id": instance.AppID,
		"create_only": !instance.CredentialConfigured, "minimal_preset": true,
	})
	if err != nil {
		return domain.ChannelOnboardingSession{}, err
	}
	onboarding.RegistrationCommandID = command.ID
	if err := s.store.CreateChannelOnboarding(ctx, onboarding); err != nil {
		return domain.ChannelOnboardingSession{}, err
	}
	instance.Status, instance.UpdatedAt = "onboarding", now
	if _, err := s.store.UpdateChannelInstance(ctx, instance, instance.Revision); err != nil {
		return domain.ChannelOnboardingSession{}, err
	}
	scopes := onboardingScopes()
	if instance.CredentialConfigured {
		scopes = reauthorizationScopes()
	}
	raw, _, err := s.IssueCapability(ctx, instance.ID, scopes, 20*time.Minute)
	if err != nil {
		return domain.ChannelOnboardingSession{}, err
	}
	s.stopPluginProcess(instance.ID)
	if err := s.ensurePluginProcess(instance.ID, raw); err != nil {
		_ = s.RevokeCapabilities(ctx, instance.ID)
		return domain.ChannelOnboardingSession{}, err
	}
	return onboarding, nil
}

func onboardingScopes() []string {
	return []string{"channel.command.claim", "channel.command.progress", "channel.command.complete", "channel.registration.store", "channel.health.write"}
}

func runtimeScopes() []string {
	return []string{"channel.command.claim", "channel.command.progress", "channel.command.complete", "channel.inbound.write", "channel.delivery.claim", "channel.delivery.ack", "channel.health.write", "channel.media.upload", "channel.media.read"}
}

func reauthorizationScopes() []string {
	return uniqueScopes(append(append([]string{}, runtimeScopes()...), onboardingScopes()...))
}

func (s *Service) OwnerOnboarding(ctx context.Context, ownerID, ownerSessionID, id string) (domain.ChannelOnboardingSession, error) {
	item, err := s.store.ChannelOnboarding(ctx, id)
	if err != nil {
		return domain.ChannelOnboardingSession{}, err
	}
	instance, err := s.store.ChannelInstance(ctx, item.InstanceID)
	if err != nil || instance.OwnerID != ownerID || item.OwnerSessionID != ownerSessionID {
		return domain.ChannelOnboardingSession{}, sql.ErrNoRows
	}
	if item.Status == "pending" || item.Status == "qr_ready" {
		if !item.ExpiresAt.After(s.now().UTC()) {
			item.Status, item.Step = "expired", "expired"
		} else if item.VerificationURLRef != "" && s.secrets != nil {
			item.VerificationURL, _ = s.secrets.Get(item.VerificationURLRef)
		}
	}
	return item, nil
}

func (s *Service) CancelOnboarding(ctx context.Context, ownerID, ownerSessionID, id string) (domain.ChannelOnboardingSession, error) {
	item, err := s.OwnerOnboarding(ctx, ownerID, ownerSessionID, id)
	if err != nil {
		return domain.ChannelOnboardingSession{}, err
	}
	item, err = s.store.CancelChannelOnboarding(ctx, id, ownerSessionID, s.now().UTC())
	if err != nil {
		return domain.ChannelOnboardingSession{}, err
	}
	if s.secrets != nil {
		_ = s.secrets.DeleteMany([]string{item.VerificationURLRef, item.SecretStageRef})
	}
	s.stopPluginProcess(item.InstanceID)
	_ = s.RevokeCapabilities(ctx, item.InstanceID)
	return item, nil
}

func (s *Service) completeRegistrationFromIPC(ctx context.Context, message secretIPCMessage) error {
	if message.OnboardingID == "" || message.CommandID == "" || message.LeaseID == "" || strings.TrimSpace(message.AppID) == "" || strings.TrimSpace(message.AppSecret) == "" || strings.TrimSpace(message.ScannerOpenID) == "" {
		return fmt.Errorf("invalid registration result")
	}
	onboarding, err := s.store.ChannelOnboarding(ctx, message.OnboardingID)
	if err != nil || onboarding.RegistrationCommandID != message.CommandID {
		return fmt.Errorf("registration result scope mismatch")
	}
	ref := "channel/" + onboarding.InstanceID + "/feishu/app_secret/" + onboarding.ID
	previous, _ := s.store.ChannelInstance(ctx, onboarding.InstanceID)
	if s.secrets == nil {
		return fmt.Errorf("secret store is unavailable")
	}
	if err := s.secrets.PutMany(map[string]string{ref: message.AppSecret}); err != nil {
		return err
	}
	_, _, err = s.store.CompleteChannelRegistration(ctx, onboarding.ID, message.CommandID, message.AppID, ref, message.ScannerOpenID, message.TenantBrand, s.now().UTC())
	if err != nil {
		_ = s.secrets.DeleteMany([]string{ref})
		return err
	}
	if previous.CredentialRef != "" && previous.CredentialRef != ref {
		_ = s.secrets.DeleteMany([]string{previous.CredentialRef})
	}
	if err := s.store.CompleteChannelCommand(ctx, onboarding.InstanceID, message.CommandID, message.LeaseID, true, map[string]any{"status": "registered"}, "", s.now().UTC()); err != nil && !errors.Is(err, store.ErrChannelRevision) {
		return err
	}
	_ = s.secrets.DeleteMany([]string{onboarding.VerificationURLRef})
	return nil
}
