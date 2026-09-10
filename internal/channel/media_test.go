package channel

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func mediaTestService(t *testing.T) (*Service, domain.ChannelInstance, RuntimeClaims) {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	secretStore, err := secrets.Open(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	owner := domain.Owner{ID: "owner-inbound", Username: "owner", PasswordHash: "hash", CreatedAt: now}
	if err := database.CreateOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	provider := domain.Provider{ID: "provider-stub", Name: "Stub", AuthStyle: "none", CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{ID: "env-stub", Name: "Stub", ProviderID: provider.ID, Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{ID: "model-stub", DisplayName: "Stub", ProviderID: provider.ID, Source: "provider", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{ID: "feishu", ProviderKey: "feishu", DisplayName: "飞书", ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1, ExecutablePath: "test", ExecutableSHA256: "test", Manifest: map[string]any{}, InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	service := New(Config{Store: database, App: appService, Secrets: secretStore, DataDir: dataDir, RuntimeDevice: "device-local"})
	instance, err := service.CreateInstance(ctx, owner.ID, plugin.ID, "Inbox", "create-inbox")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.BindChannelOwnerIdentity(ctx, domain.ChannelIdentityLink{ID: "channel-owner", InstanceID: instance.ID, OwnerID: owner.ID, ExternalUserID: "owner-open", Role: "owner", Status: "active", LinkedAt: now}); err != nil {
		t.Fatal(err)
	}
	instance.Status, instance.UpdatedAt = "ready", now
	instance, err = database.UpdateChannelInstance(ctx, instance, instance.Revision)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = appService.Shutdown(context.Background()) })
	claims := RuntimeClaims{InstanceID: instance.ID, Scopes: map[string]bool{}}
	for _, scope := range runtimeScopes() {
		claims.Scopes[scope] = true
	}
	return service, instance, claims
}

func TestChannelMediaInboundAuthorizationAndAttachmentBinding(t *testing.T) {
	service, instance, claims := mediaTestService(t)
	ctx := context.Background()
	clock := time.Now().UTC()
	service.now = func() time.Time { return clock }
	envelope := domain.ChannelInboundEnvelope{SchemaVersion: 1, RequestID: "media-event", InstanceID: instance.ID, ExternalEventID: "media-event", EventType: "message", OccurredAt: clock, ChatType: "p2p", ExternalChatID: "owner-chat", ExternalSenderID: "owner-open", ExternalMessageID: "source-message", Resources: []map[string]any{{"type": "file", "file_key": "source-file", "file_name": "report.txt"}}}
	unauthorized := envelope
	unauthorized.ExternalEventID, unauthorized.ExternalSenderID = "not-owner-event", "not-owner"
	if _, _, err := service.ReceiveInbound(ctx, claims, unauthorized); err != nil {
		t.Fatal(err)
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	unmentioned := envelope
	unmentioned.ExternalEventID, unmentioned.ChatType, unmentioned.ExternalSenderID = "unmentioned-media", "group", "participant"
	if _, _, err := service.ReceiveInbound(ctx, claims, unmentioned); err != nil {
		t.Fatal(err)
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	commands, err := service.ClaimCommands(ctx, claims, 20)
	if err != nil || len(commands) != 0 {
		t.Fatalf("non-owner created media commands=%#v err=%v", commands, err)
	}
	receipt, _, err := service.ReceiveInbound(ctx, claims, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	commands, err = service.ClaimCommands(ctx, claims, 20)
	if err != nil || len(commands) != 1 || commands[0].Kind != "download_resource" {
		t.Fatalf("commands=%#v err=%v", commands, err)
	}
	command := commands[0]
	conversation, lookupErr := service.store.ChannelConversation(ctx, stringField(command.Payload, "conversation_id"))
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	if _, _, _, routeErr := service.inboundAttachments(ctx, receipt, conversation, "changed-task", envelope); routeErr != errMediaRouteChanged {
		t.Fatalf("target change was not rejected: %v", routeErr)
	}
	if err := service.CompleteCommand(ctx, claims, command.ID, command.LeaseID, true, map[string]any{"attachment_id": "foreign"}, ""); err == nil {
		t.Fatal("plugin forged completed attachment")
	}
	foreign := claims
	foreign.InstanceID = "another-instance"
	if _, err := service.ReceiveMediaAttachment(ctx, foreign, command.ID, command.LeaseID, "report.txt", []byte("private")); err == nil {
		t.Fatal("cross-instance upload accepted")
	}
	if _, err := service.ReceiveMediaAttachment(ctx, claims, command.ID, "expired-lease", "report.txt", []byte("private")); err == nil {
		t.Fatal("invalid lease accepted")
	}
	attachment, err := service.ReceiveMediaAttachment(ctx, claims, command.ID, command.LeaseID, "report.txt", []byte("report content"))
	if err != nil {
		t.Fatal(err)
	}
	retried, err := service.ReceiveMediaAttachment(ctx, claims, command.ID, command.LeaseID, "report.txt", []byte("report content"))
	if err != nil || retried.ID != attachment.ID {
		t.Fatalf("upload retry=%#v err=%v", retried, err)
	}
	clock = clock.Add(3 * time.Second)
	service = New(Config{Store: service.store, App: service.application, Secrets: service.secrets, DataDir: t.TempDir(), RuntimeDevice: "device-local"})
	service.now = func() time.Time { return clock }
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	stored, duplicate, err := service.ReceiveInbound(ctx, claims, envelope)
	if err != nil || !duplicate || stored.ID != receipt.ID || stored.State != "processed" {
		t.Fatalf("receipt=%#v duplicate=%v err=%v", stored, duplicate, err)
	}
	bound, err := service.store.Attachment(ctx, attachment.TaskID, attachment.ID)
	if err != nil || bound.MessageID == "" {
		t.Fatalf("attachment not bound=%#v err=%v", bound, err)
	}
	items, err := service.store.ListTaskAttachments(ctx, attachment.TaskID)
	if err != nil || len(items) != 1 {
		t.Fatalf("attachments=%#v err=%v", items, err)
	}
}

func TestChannelMediaImageValidationAndCommandRetry(t *testing.T) {
	service, instance, claims := mediaTestService(t)
	ctx := context.Background()
	clock := time.Now().UTC()
	service.now = func() time.Time { return clock }
	envelope := domain.ChannelInboundEnvelope{SchemaVersion: 1, InstanceID: instance.ID, ExternalEventID: "image-event", EventType: "message", OccurredAt: clock, ChatType: "p2p", ExternalChatID: "owner-chat", ExternalSenderID: "owner-open", ExternalMessageID: "image-message", Resources: []map[string]any{{"type": "image", "file_key": "image-key"}}}
	if _, _, err := service.ReceiveInbound(ctx, claims, envelope); err != nil {
		t.Fatal(err)
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	commands, err := service.ClaimCommands(ctx, claims, 1)
	if err != nil || len(commands) != 1 {
		t.Fatalf("commands=%#v err=%v", commands, err)
	}
	command := commands[0]
	if _, err := service.ReceiveMediaAttachment(ctx, claims, command.ID, command.LeaseID, "fake.png", []byte("not an image")); err == nil {
		t.Fatal("fake image accepted")
	}
	if err := service.CompleteCommand(ctx, claims, command.ID, command.LeaseID, false, nil, "media_transfer_http_403"); err != nil {
		t.Fatal(err)
	}
	retriedCommand, err := service.store.ChannelCommand(ctx, instance.ID, command.ID)
	if err != nil || retriedCommand.LastErrorCode != "media_transfer_http_403" {
		t.Fatalf("retry diagnostic=%#v err=%v", retriedCommand, err)
	}
	clock = clock.Add(4 * time.Second)
	commands, err = service.ClaimCommands(ctx, claims, 1)
	if err != nil || len(commands) != 1 || commands[0].Attempts != 2 {
		t.Fatalf("retry=%#v err=%v", commands, err)
	}
	if _, err := service.ReceiveMediaAttachment(ctx, claims, command.ID, command.LeaseID, "image.png", []byte("not an image")); err == nil {
		t.Fatal("stale lease accepted after retry")
	}
	command = commands[0]
	png := []byte("\x89PNG\r\n\x1a\nfixture")
	oversized := make([]byte, MaxMediaImageBytes+1)
	copy(oversized, png)
	if _, err := service.ReceiveMediaAttachment(ctx, claims, command.ID, command.LeaseID, "large.png", oversized); err == nil || err.Error() != "resource_size_invalid" {
		t.Fatalf("oversized image accepted: %v", err)
	}
	if _, err := service.ReceiveMediaAttachment(ctx, claims, command.ID, command.LeaseID, "image.png", png); err != nil {
		t.Fatal(err)
	}
}

func TestChannelMediaFailureNoticeRetainsSanitizedDiagnostic(t *testing.T) {
	for _, scenario := range []struct {
		code, want string
		attempts   int
	}{
		{"media_download_provider_99991672", "media_download_provider_99991672", 5},
		{"resource_size_invalid", "resource_size_invalid", 1},
		{"private-url secret", "resource_download_failed", 5},
	} {
		t.Run(scenario.want, func(t *testing.T) {
			service, instance, claims := mediaTestService(t)
			ctx := context.Background()
			clock := time.Now().UTC()
			service.now = func() time.Time { return clock }
			envelope := domain.ChannelInboundEnvelope{SchemaVersion: 1, InstanceID: instance.ID, ExternalEventID: "diagnostic-event", EventType: "message", OccurredAt: clock, ChatType: "p2p", ExternalChatID: "owner-chat", ExternalSenderID: "owner-open", ExternalMessageID: "message", Resources: []map[string]any{{"type": "image", "file_key": "resource"}}}
			receipt, _, err := service.ReceiveInbound(ctx, claims, envelope)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.processInboundBatch(ctx); err != nil {
				t.Fatal(err)
			}
			var command domain.ChannelPluginCommand
			for attempt := 0; attempt < scenario.attempts; attempt++ {
				commands, err := service.ClaimCommands(ctx, claims, 1)
				if err != nil || len(commands) != 1 {
					t.Fatalf("claim=%#v err=%v", commands, err)
				}
				command = commands[0]
				if err := service.CompleteCommand(ctx, claims, command.ID, command.LeaseID, false, nil, scenario.code); err != nil {
					t.Fatal(err)
				}
				clock = clock.Add(70 * time.Second)
			}
			command, err = service.store.ChannelCommand(ctx, instance.ID, command.ID)
			if err != nil || command.State != "failed" || command.LastErrorCode != scenario.want {
				t.Fatalf("command=%#v err=%v", command, err)
			}
			conversation, err := service.store.ChannelConversation(ctx, stringField(command.Payload, "conversation_id"))
			if err != nil {
				t.Fatal(err)
			}
			ids, warning, pending, err := service.inboundAttachments(ctx, receipt, conversation, stringField(command.Payload, "task_id"), envelope)
			if err != nil || pending || len(ids) != 0 || !strings.Contains(warning, scenario.want) || strings.Contains(warning, "private-url") {
				t.Fatalf("warning=%q pending=%v err=%v", warning, pending, err)
			}
		})
	}
}
