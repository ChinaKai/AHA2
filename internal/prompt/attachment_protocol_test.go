package prompt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestAttachmentProtocolIsRequiredAcrossTasksAndChannels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	for _, templateID := range []string{"channel.web", "channel.external-channel"} {
		if err := engine.UpdateTemplate(ctx, templateID, "Custom channel without attachment instructions.", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.UpdateTemplate(ctx, "protocol.agent-api", "Remove attachment rules", time.Now()); err == nil {
		t.Fatal("managed attachment protocol allowed an override")
	}
	channels := []struct {
		name    string
		context map[string]any
	}{
		{name: "web"},
		{name: "takeover", context: map[string]any{"endpoint": domain.ChannelEndpointAssistantDM, "route": map[string]any{"mode": "task_route"}}},
		{name: "assistant", context: map[string]any{"endpoint": domain.ChannelEndpointAssistantDM}},
		{name: "group", context: map[string]any{"endpoint": domain.ChannelEndpointGroupDigitalHuman}},
	}
	for _, taskID := range []string{"task-first", "task-new"} {
		for _, backend := range []string{"codex", "claude"} {
			for _, channel := range channels {
				t.Run(taskID+"/"+backend+"/"+channel.name, func(t *testing.T) {
					input := BuildInput{
						Workspace:      domain.Workspace{RootPath: t.TempDir(), Transport: "native"},
						Task:           domain.Task{ID: taskID, CollaborationMode: "single"},
						Agent:          domain.TaskAgent{AgentID: "main", Role: "main"},
						Snapshot:       domain.RuntimeConfigSnapshot{Backend: backend},
						AgentAPIURL:    "http://127.0.0.1:8766",
						ChannelContext: channel.context,
					}
					result, err := engine.Build(ctx, input)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(result.EffectivePrompt, attachmentDeliveryProtocol) {
						t.Fatal("mandatory attachment protocol missing without Memory or Skills")
					}
					foundResource := false
					for _, resource := range result.SharedManifest {
						if resource.ID == "agent-api" {
							foundResource = true
							if !strings.Contains(resource.Content, attachmentDeliveryProtocol) {
								t.Fatal("attachment instructions missing from API resource")
							}
							if !strings.Contains(resource.Path, taskID) {
								t.Fatal("API resource is not scoped to the current Task")
							}
						}
					}
					if !foundResource {
						t.Fatal("Task API resource missing")
					}
				})
			}
		}
	}
}

func TestAttachmentProtocolIncludesPublicationAndReceiptBoundaries(t *testing.T) {
	t.Parallel()
	for _, required := range []string{
		"actual file bytes", "Image generation output", "POST /api/v1/agent/turn/attachments",
		"attachment.id", "POST /api/v1/agent/turn/messages", "attachment_ids",
		"Upload alone does not publish a message", "Never reuse an attachment ID from another Task",
		"confirmed receipt or Owner feedback", "group progress updates do not send attachments",
		"not automatically mirrored to Feishu",
	} {
		if !strings.Contains(attachmentDeliveryProtocol, required) {
			t.Errorf("attachment protocol is missing %q", required)
		}
	}
}
