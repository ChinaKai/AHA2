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
	if err := engine.UpdateTemplate(ctx, "protocol.agent-api", "Use `agent-api.md` only when needed.", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := engine.UpdateTemplate(ctx, "protocol.attachment-delivery", "Remove attachment rules", time.Now()); err == nil {
		t.Fatal("managed attachment protocol allowed an override")
	}
	attachmentProtocol := templateContent(t, engine, ctx, "protocol.attachment-delivery")
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
					if !strings.Contains(result.EffectivePrompt, attachmentProtocol) {
						t.Fatal("mandatory attachment protocol missing without Memory or Skills")
					}
					foundResource, foundAttachment := false, false
					for _, resource := range result.SharedManifest {
						if !strings.Contains(resource.Path, taskID) {
							t.Fatalf("shared resource %s is not scoped to the current Task", resource.ID)
						}
						switch resource.ID {
						case "agent-api":
							foundResource = true
						case "attachment-protocol":
							foundAttachment = true
							// The resident protocol only points at the file, so the
							// procedure must survive in the File for every Task.
							if !strings.Contains(resource.Content, "Upload alone does not publish") {
								t.Fatal("attachment instructions missing from the attachment resource")
							}
						}
					}
					if !foundResource {
						t.Fatal("Task API resource missing")
					}
					if !foundAttachment {
						t.Fatal("Task attachment resource missing")
					}
				})
			}
		}
	}
}

func TestAttachmentProtocolIncludesPublicationAndReceiptBoundaries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	// The procedure is now split: the resident template keeps the safety
	// invariants, and the resource carries the steps. Assert both halves so
	// moving a line out of the resident template cannot silently drop it.
	engine := NewEngine(database)
	attachmentProtocol := templateContent(t, engine, ctx, "protocol.attachment-delivery") +
		templateContent(t, engine, ctx, "resource.attachment-protocol")
	for _, required := range []string{
		"actual file bytes", "generated previews", "POST /api/v1/agent/turn/attachments",
		"attachment.id", "POST /api/v1/agent/turn/messages", "attachment_ids",
		"Upload alone does not publish a message", "Never reuse or guess an attachment ID from another Task",
		"confirmed result or Owner feedback", "Group progress updates do not send attachments",
		"not automatically mirrored to an external channel",
	} {
		if !strings.Contains(attachmentProtocol, required) {
			t.Errorf("attachment protocol is missing %q", required)
		}
	}
}

func templateContent(t *testing.T, engine *Engine, ctx context.Context, id string) string {
	t.Helper()
	templates, err := engine.Templates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range templates {
		if item.ID == id {
			return item.Content
		}
	}
	t.Fatalf("template %s not found", id)
	return ""
}
