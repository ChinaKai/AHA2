package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

// TestPetRecoverKnowledgeSyncClearsPauseWithoutClosingNotifications drives the
// whole loop: a real conflict pauses auto-sync, the pet tool clears the pause
// but leaves the notification open, a still-diverged next cycle pauses again,
// and only a successful sync after the divergence is gone closes the
// notification.
func TestPetRecoverKnowledgeSyncClearsPauseWithoutClosingNotifications(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	vaultConfig := knowledgevault.DefaultConfig(dataDir)
	vaultConfig.AllowLocalRemote = true
	if _, err := knowledgevault.Bootstrap(vaultConfig, knowledgevault.InitOptions{VaultName: "Recovery Vault", Commit: false}); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(vaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, err := manager.Git().Init(ctx); err != nil {
		t.Fatal(err)
	}

	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secretStore, err := secrets.Open(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	appService.SetKnowledgeVault(manager, true)
	t.Cleanup(func() { _ = appService.Shutdown(context.Background()) })
	syncer := knowledgevault.NewSyncer(manager, database, knowledgevault.SyncerConfig{
		Emit: func(event knowledgevault.SyncEvent) {
			appService.RecordKnowledgeSyncEvent(ctx, event.Kind, event.Detail)
		},
		Recovered: func() {
			_, _ = appService.ResolveKnowledgeSyncNotifications(ctx)
		},
	})
	appService.SetKnowledgeSyncRecovery(syncer)
	authService := auth.NewService(database, "setup-test", time.Hour)
	if err := syncer.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	capabilities := agentapi.NewCapabilities()
	petToken, err := capabilities.Issue("task_pet", "main", "turn-pet", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := capabilities.Issue("task_other", "main", "turn-other", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: appService,
		KnowledgeVault: manager, KnowledgeVaultSync: syncer,
		AgentCapabilities: capabilities,
	}).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	registerOwner(t, client, server.URL)

	remote := filepath.Join(t.TempDir(), "remote.git")
	gitHelper(t, t.TempDir(), "init", "--bare", "-b", "main", remote)
	if _, err := manager.Git().SetRemote(ctx, remote, "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vaultConfig.VaultPath, "base.md"), []byte("# Base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Git().Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Git().Push(ctx); err != nil {
		t.Fatal(err)
	}
	syncer.RunOnce(ctx)
	if status := syncer.Status(); status.Paused {
		t.Fatalf("clean baseline paused: %+v", status)
	}

	// Diverge the remote and the local vault.
	clone := filepath.Join(t.TempDir(), "other-writer")
	gitHelper(t, t.TempDir(), "clone", remote, clone)
	if err := os.WriteFile(filepath.Join(clone, "remote.md"), []byte("# Remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitHelper(t, clone, "add", "-A")
	gitHelper(t, clone, "commit", "-m", "remote side")
	gitHelper(t, clone, "push", "origin", "main")
	if err := os.WriteFile(filepath.Join(vaultConfig.VaultPath, "local.md"), []byte("# Local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Git().Commit(ctx, "local side"); err != nil {
		t.Fatal(err)
	}
	syncer.RunOnce(ctx)
	if status := syncer.Status(); !status.Paused || !status.Conflict || status.PauseReason != "conflict" {
		t.Fatalf("expected conflict pause: %+v", status)
	}
	state := petWorkStateHTTP(t, client, server.URL)
	if len(state.Notifications) != 1 {
		t.Fatalf("work state notifications=%#v", state.Notifications)
	}
	notificationID := state.Notifications[0].ID
	if state.Notifications[0].Kind != knowledgevault.SyncEventConflict {
		t.Fatalf("notification kind=%q", state.Notifications[0].Kind)
	}

	// Only the pet Task's capability may recover install-wide sync state.
	rejectText, rejected := petMCPToolCall(t, client, server.URL, "", "aha_recover_knowledge_sync", map[string]any{})
	if !rejected || !strings.Contains(rejectText, "宠物 Task") {
		t.Fatalf("unidentified recovery text=%q rejected=%v", rejectText, rejected)
	}
	rejectText, rejected = petMCPToolCall(t, client, server.URL, otherToken, "aha_recover_knowledge_sync", map[string]any{})
	if !rejected || !strings.Contains(rejectText, "宠物 Task") {
		t.Fatalf("foreign-task recovery text=%q rejected=%v", rejectText, rejected)
	}
	if status := syncer.Status(); !status.Paused {
		t.Fatalf("rejected recovery resumed sync: %+v", status)
	}

	// Recovery clears the pause but must not close the unresolved notification.
	raw := petMCPToolTextForToken(t, client, server.URL, petToken, "aha_recover_knowledge_sync", map[string]any{})
	var result app.KnowledgeSyncRecoveryResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode recovery: %v\n%s", err, raw)
	}
	if !result.OK || !result.Recovered || !result.Before.Paused || result.After.Paused {
		t.Fatalf("recovery result=%s", raw)
	}
	if status := syncer.Status(); status.Paused || !status.Enabled {
		t.Fatalf("recovery must clear the pause and keep auto-sync enabled: %+v", status)
	}
	state = petWorkStateHTTP(t, client, server.URL)
	if len(state.Notifications) != 1 || state.Notifications[0].ID != notificationID {
		t.Fatalf("recovery closed an unresolved notification: %#v", state.Notifications)
	}

	// The divergence is still there: the next cycle pauses again, and the
	// conflict de-duplication key keeps the original notification only.
	syncer.RunOnce(ctx)
	if status := syncer.Status(); !status.Paused || !status.Conflict || status.PauseReason != "conflict" {
		t.Fatalf("still-diverged cycle did not pause again: %+v", status)
	}
	state = petWorkStateHTTP(t, client, server.URL)
	if len(state.Notifications) != 1 || state.Notifications[0].ID != notificationID {
		t.Fatalf("re-pause notifications=%#v", state.Notifications)
	}

	// Fix the divergence, recover again, and let a successful cycle prove it:
	// only then does the Recovered callback close the notification.
	gitHelper(t, vaultConfig.VaultPath, "fetch", "origin", "main")
	gitHelper(t, vaultConfig.VaultPath, "merge", "--no-edit", "origin/main")
	// While paused the worktree changed: a blocked binary file adds one pending
	// item that the pause-time snapshot never saw. Recovery must report that
	// live value on both sides instead of regressing after.pending to the old
	// snapshot. A blocked file does not stop the push below.
	if err := os.WriteFile(filepath.Join(vaultConfig.VaultPath, "pause-window.png"), []byte("not a real png\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw = petMCPToolTextForToken(t, client, server.URL, petToken, "aha_recover_knowledge_sync", map[string]any{})
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode second recovery: %v\n%s", err, raw)
	}
	if !result.Recovered || result.After.Paused {
		t.Fatalf("second recovery result=%s", raw)
	}
	if !result.Before.PendingKnown || result.Before.Pending != 1 {
		t.Fatalf("before pending=%+v, want the live scan value 1", result.Before)
	}
	if !result.After.PendingKnown || result.After.Pending != 1 {
		t.Fatalf("after pending=%+v, want the live scan value 1", result.After)
	}
	syncer.RunOnce(ctx)
	if status := syncer.Status(); status.Paused || status.Conflict {
		t.Fatalf("successful sync did not clear the pause: %+v", status)
	}
	if state = petWorkStateHTTP(t, client, server.URL); len(state.Notifications) != 0 {
		t.Fatalf("notifications survived a successful sync: %#v", state.Notifications)
	}
}
