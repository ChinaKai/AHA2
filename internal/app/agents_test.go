package app

import (
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
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
