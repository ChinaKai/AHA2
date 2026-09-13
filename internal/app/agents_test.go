package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/prompt"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestBackendSessionIdentityChangedIgnoresPerTurnSettings(t *testing.T) {
	base := domain.RuntimeConfigSnapshot{
		WorkspaceID: "workspace-1", Backend: "codex", ModelID: "model-1", WireModel: "gpt-1",
		EnvGroupID: "env-1", EnvGroupRevision: 2, CodexAccountID: "account-1",
		ProxyEnabled: true, ReasoningEffort: "medium", PermissionsJSON: `{"filesystem":"workspace-write","approval":"never"}`,
	}
	tests := []struct {
		name   string
		mutate func(*domain.RuntimeConfigSnapshot)
		want   bool
	}{
		{name: "reasoning effort", mutate: func(snapshot *domain.RuntimeConfigSnapshot) { snapshot.ReasoningEffort = "high" }},
		{name: "proxy", mutate: func(snapshot *domain.RuntimeConfigSnapshot) { snapshot.ProxyEnabled = false }},
		{name: "stream idle timeout", mutate: func(snapshot *domain.RuntimeConfigSnapshot) { snapshot.StreamIdleTimeoutMS = 120000 }},
		{name: "stream retries", mutate: func(snapshot *domain.RuntimeConfigSnapshot) { snapshot.StreamMaxRetries = 2 }},
		{name: "permissions", mutate: func(snapshot *domain.RuntimeConfigSnapshot) {
			snapshot.PermissionsJSON = `{"filesystem":"read-only","approval":"auto"}`
		}},
		{name: "backend", mutate: func(snapshot *domain.RuntimeConfigSnapshot) { snapshot.Backend = "claude" }, want: true},
		{name: "model", mutate: func(snapshot *domain.RuntimeConfigSnapshot) { snapshot.ModelID = "model-2" }, want: true},
		{name: "wire model", mutate: func(snapshot *domain.RuntimeConfigSnapshot) { snapshot.WireModel = "gpt-2" }, want: true},
		{name: "environment group", mutate: func(snapshot *domain.RuntimeConfigSnapshot) { snapshot.EnvGroupID = "env-2" }, want: true},
		{name: "environment revision", mutate: func(snapshot *domain.RuntimeConfigSnapshot) { snapshot.EnvGroupRevision++ }, want: true},
		{name: "account", mutate: func(snapshot *domain.RuntimeConfigSnapshot) { snapshot.CodexAccountID = "account-2" }, want: true},
		{name: "workspace", mutate: func(snapshot *domain.RuntimeConfigSnapshot) { snapshot.WorkspaceID = "workspace-2" }, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next := base
			test.mutate(&next)
			if got := backendSessionIdentityChanged(base, next); got != test.want {
				t.Fatalf("backendSessionIdentityChanged()=%t want %t", got, test.want)
			}
		})
	}
}

func TestInboxInstructionRendersPerMessageChannelActor(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instruction, err := prompt.NewEngine(database).RenderInboxBatch(ctx, inboxTemplateItems([]domain.AgentInboxItem{
		{
			Sequence: 1, SourceKind: "owner_message", SourceAgentID: "owner", Content: "请检查蓝牙连接",
			Payload: map[string]any{"channel_context": map[string]any{
				"chat_display_name": "APP 与固件联调群",
				"actor":             map[string]any{"display_name": "张三", "role": "participant"},
				"mentions":          []any{map[string]any{"display_name": "李四"}},
			}},
		},
		{
			Sequence: 2, SourceKind: "owner_message", SourceAgentID: "owner", Content: "我补充日志",
			Payload: map[string]any{"channel_context": map[string]any{
				"chat_display_name": "APP 与固件联调群",
				"actor":             map[string]any{"display_name": "王五", "role": "participant"},
			}},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"owner_message from 张三", "Channel sender: 张三", "Mentioned participants: 李四",
		"owner_message from 王五", "Channel sender: 王五", "我补充日志",
	} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("instruction missing %q: %s", expected, instruction)
		}
	}
}
