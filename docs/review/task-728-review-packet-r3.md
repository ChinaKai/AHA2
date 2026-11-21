# task-728 review packet r3：M4 修复 + M1/M3 判别性测试补强

- 上一版：实现 `2a8f1e5`，材料 `5c7a110`（r2，复审结论 REQUEST_CHANGES：M4 + S1/S3）
- 本版：实现 `ee276b2`，分支 `aha/task_07933ff5b7797feffa4c`，worktree `.aha2-worktrees/task_07933ff5b7797feffa4c`
- 没有 push、没有 merge、没有 complete/finalize
- 本包覆盖 `2a8f1e5..ee276b2` 的源码 delta（不含 `docs/review`），19,418 字节，sha256 `5288c7a32bc2310097ee9171540fa981652438c887c596b83104c02d9d41ef19`
- 还原：取文末 `~~~~diff` 与 `~~~~` 之间的内容，就是 `git diff 2a8f1e5 ee276b2 -- . ':!docs/review'`（已用 `cmp` 核对）。

## M4（必须修）：远程 Workspace 的登录状态阻断本机 Claude 聊天

- 判定规则改为“只采信实际执行主机”：
  1. 宠物 Workspace 就是 Web 聊天真正执行的地方。只要它有 `status=ready` 的 Claude 探测，就**只**用这份探测：`logged_in` 可用、`not_logged_in` 不可用、`unknown` 可用（交给 CLI 报真实结果）。
  2. 宠物 Workspace 不存在或没有可用探测时，只聚合**本机 native** Workspace：`Transport == "native"`，`Locality` 为空或 `local`。任意一个 `logged_in` 即视为可用；没有任何正证据、但存在本机 `not_logged_in` 才判不可用。
  3. SSH（`Locality=remote`）、WSL（`Transport=wsl`）和其他设备的 Workspace 一律不参与：它们有自己的登录账号，不能代表本机。
  4. 聚合结果与列表顺序无关：不再“先到先得”，而是在本机候选里先找正证据，再找负证据。
- 聊天入口（`askAssistantBackend`）仍然执行同一个可用性判断，但现在远程负探测不会再把本机聊天挡在门外。
- 新增 `TestClaudeNativeAvailabilityIgnoresRemoteProbe`，8 个场景：
  - 远程 `not_logged_in` 在前、本机 `logged_in` 在后 → 可用；
  - 本机 `logged_in` 在前、远程 `not_logged_in` 在后 → 可用（顺序颠倒）；
  - 只有远程负探测、没有本机探测 → 可用（不阻断）；
  - 本机 `not_logged_in` + 远程 `logged_in` → 不可用（远程正证据不算本机）；
  - 只有 WSL `not_logged_in` → 可用（WSL 是另一个登录）；
  - 宠物 Workspace 无探测 + 远程负 + 本机正 → 可用；
  - 宠物 Workspace 自己有 `not_logged_in` + 本机其他 Workspace `logged_in` → 不可用（执行主机探测优先）；
  - 宠物 Workspace 无探测 + 只有远程负 → 可用。
- 判定辅助函数拆成 `claudeNativeHostWorkspace`（只放行本机 native）和 `claudeProbeAvailability`（单份探测的解析），便于审查规则边界。

## 非阻塞建议

### S1：profile 更新测试没有判别力
- 新增 `TestStaleProfileUpdateCannotRevertModel`：读到 `(A,A)` 的旧快照，另一个保存把库改成 `(B,B)`，然后用旧快照调用 `Store.UpdateOwnerAssistant` 只改形象。断言库里仍是 `(B,B)`，形象已更新。旧实现（store 按快照写 `model_id`）会在这里把 A 写回去并失败。
- 原有 CAS 撕裂快照测试保留，仍然覆盖“读到 `(B,A)` → 保存完成 `(B,B)` → 归一化不得回写”。

### S3：凭据刷新只测了成功
- `TestAssistantChatPersistsRefreshedCodexCredentialOnFailure`：执行器刷新 token 后返回错误；聊天回“我暂时没连上模型”，账号密文仍写入刷新后的 token。
- `TestAssistantChatPersistsRefreshedCodexCredentialOnCancel`：执行器刷新 token 后阻塞等 ctx；测试确认它已刷新后取消聊天；即使聊天被取消，`SyncProfile` 仍把刷新后的 token 写回。
- 加上原有的成功路径测试，三条退出路径（成功/失败/取消）都覆盖。

## 执行的命令与退出码（全部实跑）

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `go build ./...` | 0 | 通过 |
| `go vet ./internal/app ./internal/store ./internal/httpapi ./cmd/aha` | 0 | 通过 |
| app 全包：`go test -c` 后 `env -u GOTMPDIR TMPDIR=/tmp/task-728-app app.test -test.count=1 -test.timeout=30m` | 0 | 0 FAIL |
| httpapi 全包：同法，`TMPDIR=/tmp/task-728-http` | 0 | 0 FAIL |
| store 全包：同法，`TMPDIR=/tmp/task-728-store` | 0 | 0 FAIL |
| `npm test`（web） | 0 | 620/620 |

定向新增/修改用例：`TestClaudeNativeAvailabilityIgnoresRemoteProbe`（8 子用例）、`TestStaleProfileUpdateCannotRevertModel`、`TestAssistantChatPersistsRefreshedCodexCredentialOnFailure`、`TestAssistantChatPersistsRefreshedCodexCredentialOnCancel`。M4 的混合场景测试也在 app 全包中通过。

## 未验证缺口

- 仍没有真实 Claude Code 登录或 Codex 官方账号；M4 的判定规则由 Workspace 夹具覆盖，真实远程主机上的探测结果未实测。
- 浏览器验证仍是 Playwright + API 夹具，不是真实 Go 服务；Web 端没有改代码，620 测试与只读契约沿用 r2 结果。
- 评审在只读沙箱里不能启动 Go 测试；以上是 builder 本机实跑。
- 宠物 Workspace 自身是远程时，它的探测被视为权威（聊天也在那里执行）；这条路径没有真实远程宠物 Workspace 夹具。

## 本版 delta（2a8f1e5..ee276b2，排除 docs/review）

~~~~diff
diff --git a/internal/app/assistant.go b/internal/app/assistant.go
index fc2d13c..379fd7a 100644
--- a/internal/app/assistant.go
+++ b/internal/app/assistant.go
@@ -302,49 +302,84 @@ func (s *Service) providerAPIKey(provider domain.Provider) string {
 }
 
 // claudeNativeAvailability reads the Workspace probe the picker already uses.
-// With no probe there is no evidence either way, so it stays available and the
-// CLI reports a real authentication error instead of the settings page
-// inventing one.
+// With no probe on the execution host there is no evidence either way, so it
+// stays available and the CLI reports a real authentication error instead of
+// the settings page inventing one.
 func (s *Service) claudeNativeAvailability(ctx context.Context) (bool, string) {
 	workspaces, err := s.store.ListWorkspaces(ctx, "")
 	if err != nil {
 		return true, ""
 	}
-	ordered := make([]domain.Workspace, 0, len(workspaces))
-	for _, workspace := range workspaces {
-		if workspace.ID == petWorkspaceID {
-			ordered = append(ordered, workspace)
-		}
-	}
+	// The Pet Workspace is where chat actually runs. If it has its own probe,
+	// that probe is authoritative even if it belongs to a remote host.
 	for _, workspace := range workspaces {
 		if workspace.ID != petWorkspaceID {
-			ordered = append(ordered, workspace)
+			continue
+		}
+		if ok, available, reason := claudeProbeAvailability(workspace); ok {
+			return available, reason
 		}
 	}
-	for _, workspace := range ordered {
-		probe, ok := workspace.Capabilities["claude"].(map[string]any)
-		if !ok {
+	// Only local native Workspaces share the default Claude login with the
+	// local Pet Workspace. A remote Workspace's not_logged_in must never block
+	// a working local account, and list order must not decide it either.
+	loggedIn, notLoggedIn := false, false
+	for _, workspace := range workspaces {
+		if workspace.ID == petWorkspaceID || !claudeNativeHostWorkspace(workspace) {
 			continue
 		}
-		status, _ := probe["status"].(string)
-		if status != "ready" {
+		ok, available, _ := claudeProbeAvailability(workspace)
+		if !ok {
 			continue
 		}
-		switch authStatus, _ := probe["auth_status"].(string); authStatus {
-		case "not_logged_in":
-			return false, "尚未登录 Claude Code，请先执行 claude auth login"
-		case "logged_in":
-			return true, ""
-		default:
-			// "unknown" is not evidence of a failure. Keeping it available lets
-			// the CLI report the real authentication result instead of the
-			// settings page blocking a login it could not verify.
-			return true, ""
+		if available {
+			loggedIn = true
+		} else {
+			notLoggedIn = true
 		}
 	}
+	if loggedIn {
+		return true, ""
+	}
+	if notLoggedIn {
+		return false, "尚未登录 Claude Code，请先执行 claude auth login"
+	}
 	return true, ""
 }
 
+// claudeNativeHostWorkspace reports whether a Workspace describes the login of
+// the machine that runs the local pet chat: a local native Workspace. WSL and
+// SSH Workspaces have their own logged-in account and are not evidence about
+// this host.
+func claudeNativeHostWorkspace(workspace domain.Workspace) bool {
+	if workspace.Locality != "" && workspace.Locality != "local" {
+		return false
+	}
+	return workspace.Transport == "native"
+}
+
+// claudeProbeAvailability reads one Workspace's Claude probe. ok=false means
+// the Workspace has no successful probe and contributes no evidence.
+func claudeProbeAvailability(workspace domain.Workspace) (ok, available bool, reason string) {
+	probe, found := workspace.Capabilities["claude"].(map[string]any)
+	if !found {
+		return false, true, ""
+	}
+	if status, _ := probe["status"].(string); status != "ready" {
+		return false, true, ""
+	}
+	switch authStatus, _ := probe["auth_status"].(string); authStatus {
+	case "not_logged_in":
+		return true, false, "尚未登录 Claude Code，请先执行 claude auth login"
+	case "logged_in":
+		return true, true, ""
+	default:
+		// "unknown" is not evidence of a failure. Keeping it available lets the
+		// CLI report the real authentication result.
+		return true, true, ""
+	}
+}
+
 func (s *Service) UpdateOwnerAssistant(ctx context.Context, modelID, name, character string) (OwnerAssistantView, error) {
 	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
 	if err != nil {
diff --git a/internal/app/assistant_unified_model_test.go b/internal/app/assistant_unified_model_test.go
index dba1d51..2acfc7f 100644
--- a/internal/app/assistant_unified_model_test.go
+++ b/internal/app/assistant_unified_model_test.go
@@ -3,11 +3,13 @@ package app
 import (
 	"context"
 	"encoding/json"
+	"errors"
 	"os"
 	"path/filepath"
 	"strings"
 	"sync"
 	"testing"
+	"time"
 
 	"github.com/ChinaKai/AHA2/internal/codexaccount"
 	"github.com/ChinaKai/AHA2/internal/domain"
@@ -51,11 +53,15 @@ type refreshingCodexAuthExecutor struct {
 	mu             sync.Mutex
 	seenTokens     []string
 	refreshedToken string
+	failErr        error
+	// blockUntilCancel makes Execute wait for its context after refreshing, so
+	// a canceled chat still exercises the credential write-back path.
+	blockUntilCancel bool
+	refreshed        chan struct{}
+	refreshedOnce    sync.Once
 }
 
-func (executor *refreshingCodexAuthExecutor) Execute(_ context.Context, request ExecutionRequest, _ func(ExecutionEvent)) (ExecutionResult, error) {
-	executor.mu.Lock()
-	defer executor.mu.Unlock()
+func (executor *refreshingCodexAuthExecutor) Execute(ctx context.Context, request ExecutionRequest, _ func(ExecutionEvent)) (ExecutionResult, error) {
 	home := strings.TrimSpace(request.Environment["CODEX_HOME"])
 	path := filepath.Join(home, "auth.json")
 	data, err := os.ReadFile(path)
@@ -68,7 +74,6 @@ func (executor *refreshingCodexAuthExecutor) Execute(_ context.Context, request
 	}
 	tokens, _ := raw["tokens"].(map[string]any)
 	token, _ := tokens["access_token"].(string)
-	executor.seenTokens = append(executor.seenTokens, token)
 	tokens["access_token"] = executor.refreshedToken
 	tokens["refresh_token"] = "refreshed-refresh-token"
 	updated, err := json.Marshal(raw)
@@ -78,6 +83,19 @@ func (executor *refreshingCodexAuthExecutor) Execute(_ context.Context, request
 	if err := os.WriteFile(path, updated, 0o600); err != nil {
 		return ExecutionResult{}, err
 	}
+	executor.mu.Lock()
+	executor.seenTokens = append(executor.seenTokens, token)
+	executor.mu.Unlock()
+	if executor.refreshed != nil {
+		executor.refreshedOnce.Do(func() { close(executor.refreshed) })
+	}
+	if executor.blockUntilCancel {
+		<-ctx.Done()
+		return ExecutionResult{}, ctx.Err()
+	}
+	if executor.failErr != nil {
+		return ExecutionResult{}, executor.failErr
+	}
 	return ExecutionResult{Reply: "官方账号回答"}, nil
 }
 
@@ -87,6 +105,40 @@ func (executor *refreshingCodexAuthExecutor) tokens() []string {
 	return append([]string(nil), executor.seenTokens...)
 }
 
+// newCodexChatFixture wires a ready official account, the real codexaccount
+// manager over a temp secret store, and an official unified model selection
+// with the pet disabled so only chat reaches the executor.
+func newCodexChatFixture(t *testing.T, executor Executor) (*petTaskFixture, *secrets.FileStore, string) {
+	t.Helper()
+	fixture := newPetTaskFixtureWithExecutor(t, executor)
+	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "codex-secrets.json"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	credentialRef := "codex/account/refresh"
+	const initialAuth = `{"tokens":{"access_token":"initial-access-token","refresh_token":"initial-refresh-token"}}`
+	if err := secretStore.PutMany(map[string]string{credentialRef: initialAuth}); err != nil {
+		t.Fatal(err)
+	}
+	fixture.service.SetCodexAccountManager(codexaccount.New(fixture.ctx, fixture.database, secretStore, "", "codex"))
+	now := fixture.service.now().UTC()
+	account := domain.CodexAccount{
+		ID: "account-refresh", Label: "Refresh Account", Status: "ready", CredentialConfigured: true,
+		CredentialRef:   credentialRef,
+		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-refresh", DisplayName: "GPT Refresh"}},
+		CreatedAt:       now, UpdatedAt: now,
+	}
+	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
+		t.Fatal(err)
+	}
+	disabled := petSettingsWithModel(domain.ModelSourceOfficial, "", "gpt-refresh", account.ID)
+	disabled.Enabled = false
+	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, disabled); err != nil {
+		t.Fatal(err)
+	}
+	return fixture, secretStore, credentialRef
+}
+
 func secondProviderModel(t *testing.T, fixture *petTaskFixture) domain.Model {
 	t.Helper()
 	model := domain.Model{
@@ -426,35 +478,7 @@ func TestAssistantChatDeletedUnifiedModelDoesNotFallBack(t *testing.T) {
 // its profile from that refreshed value instead of the original one.
 func TestAssistantChatPersistsRefreshedCodexCredential(t *testing.T) {
 	executor := &refreshingCodexAuthExecutor{refreshedToken: "refreshed-access-token"}
-	fixture := newPetTaskFixtureWithExecutor(t, executor)
-	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "codex-secrets.json"))
-	if err != nil {
-		t.Fatal(err)
-	}
-	credentialRef := "codex/account/refresh"
-	const initialAuth = `{"tokens":{"access_token":"initial-access-token","refresh_token":"initial-refresh-token"}}`
-	if err := secretStore.PutMany(map[string]string{credentialRef: initialAuth}); err != nil {
-		t.Fatal(err)
-	}
-	fixture.service.SetCodexAccountManager(codexaccount.New(fixture.ctx, fixture.database, secretStore, "", "codex"))
-	now := fixture.service.now().UTC()
-	account := domain.CodexAccount{
-		ID: "account-refresh", Label: "Refresh Account", Status: "ready", CredentialConfigured: true,
-		CredentialRef:   credentialRef,
-		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-refresh", DisplayName: "GPT Refresh"}},
-		CreatedAt:       now, UpdatedAt: now,
-	}
-	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
-		t.Fatal(err)
-	}
-	// The fixture uses one executor for both chat and the Pet Task. Disable the
-	// pet so only the two chat calls below reach it; the credential refresh is
-	// still exercised through the chat path.
-	disabled := petSettingsWithModel(domain.ModelSourceOfficial, "", "gpt-refresh", account.ID)
-	disabled.Enabled = false
-	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, disabled); err != nil {
-		t.Fatal(err)
-	}
+	fixture, secretStore, credentialRef := newCodexChatFixture(t, executor)
 
 	if _, err := fixture.service.AskOwnerAssistant(fixture.ctx, "第一次"); err != nil {
 		t.Fatal(err)
@@ -479,6 +503,59 @@ func TestAssistantChatPersistsRefreshedCodexCredential(t *testing.T) {
 	}
 }
 
+// A failed chat still refreshed the token before the backend error. The
+// refreshed credential must be written back, exactly like the success path.
+func TestAssistantChatPersistsRefreshedCodexCredentialOnFailure(t *testing.T) {
+	executor := &refreshingCodexAuthExecutor{
+		refreshedToken: "refreshed-after-error",
+		failErr:        errors.New("provider rejected the turn"),
+	}
+	fixture, secretStore, credentialRef := newCodexChatFixture(t, executor)
+	reply, err := fixture.service.AskOwnerAssistant(fixture.ctx, "会失败")
+	if err != nil {
+		t.Fatal(err)
+	}
+	if !strings.Contains(reply.Reply.Content, "我暂时没连上模型") {
+		t.Fatalf("failed chat reply=%q", reply.Reply.Content)
+	}
+	stored, ok := secretStore.Get(credentialRef)
+	if !ok || !strings.Contains(stored, "refreshed-after-error") {
+		t.Fatalf("refreshed credential after a failed chat was not written back: %q", stored)
+	}
+}
+
+// Canceling/timing out a chat must still write the refreshed credential back;
+// the normal Turn path syncs on every exit, and chat has to match.
+func TestAssistantChatPersistsRefreshedCodexCredentialOnCancel(t *testing.T) {
+	executor := &refreshingCodexAuthExecutor{
+		refreshedToken:   "refreshed-after-cancel",
+		blockUntilCancel: true,
+		refreshed:        make(chan struct{}),
+	}
+	fixture, secretStore, credentialRef := newCodexChatFixture(t, executor)
+	ctx, cancel := context.WithCancel(fixture.ctx)
+	done := make(chan struct{})
+	go func() {
+		defer close(done)
+		_, _ = fixture.service.AskOwnerAssistant(ctx, "会被取消")
+	}()
+	select {
+	case <-executor.refreshed:
+	case <-time.After(10 * time.Second):
+		t.Fatal("chat backend was never reached")
+	}
+	cancel()
+	select {
+	case <-done:
+	case <-time.After(10 * time.Second):
+		t.Fatal("canceled chat did not return")
+	}
+	stored, ok := secretStore.Get(credentialRef)
+	if !ok || !strings.Contains(stored, "refreshed-after-cancel") {
+		t.Fatalf("refreshed credential after cancellation was not written back: %q", stored)
+	}
+}
+
 // Concurrent saves and reads must never leave the two model columns disagreeing
 // or resurrect a model that was already replaced.
 func TestConcurrentPetModelSavesAndReadsStayUnified(t *testing.T) {
@@ -663,6 +740,165 @@ func TestProviderAvailabilityMatchesChatRuntime(t *testing.T) {
 	}
 }
 
+// A stale profile object must not roll the model columns back. The old store
+// method wrote model_id from the caller's snapshot; this test would fail then.
+func TestStaleProfileUpdateCannotRevertModel(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	second := secondProviderModel(t, fixture)
+	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
+		domain.ModelSourceProvider, fixture.model.ID, "", "",
+	)); err != nil {
+		t.Fatal(err)
+	}
+	stale := storedAssistant(t, fixture)
+	if err := fixture.database.UpdateOwnerPetSettings(fixture.ctx, stale.ID, true, second.ID, []string{"walk"}, fixture.service.now().UTC()); err != nil {
+		t.Fatal(err)
+	}
+	// The stale snapshot still names the old model; only its profile fields are
+	// meant to be written.
+	stale.Character = "deepseek_fish"
+	stale.UpdatedAt = fixture.service.now().UTC()
+	if err := fixture.database.UpdateOwnerAssistant(fixture.ctx, stale); err != nil {
+		t.Fatal(err)
+	}
+	stored := storedAssistant(t, fixture)
+	if stored.BrainModelID != second.ID || stored.ModelID != second.ID {
+		t.Fatalf("stale profile update reverted the model: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
+	}
+	if stored.Character != "deepseek_fish" {
+		t.Fatalf("profile update was not saved: character=%q", stored.Character)
+	}
+}
+
+func createProbeWorkspace(t *testing.T, fixture *petTaskFixture, id, locality, transport, authStatus string) {
+	t.Helper()
+	const projectID = "project-probe-workspaces"
+	if _, err := fixture.database.Project(fixture.ctx, projectID); err != nil {
+		now := fixture.service.now().UTC()
+		project := domain.Project{
+			ID: projectID, Name: "Probe Workspaces", ProjectType: "folder",
+			KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now,
+		}
+		if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
+			t.Fatal(err)
+		}
+	}
+	now := fixture.service.now().UTC()
+	workspace := domain.Workspace{
+		ID: id, ProjectID: projectID, Name: id, Locality: locality, Transport: transport,
+		RootPath: t.TempDir(), Health: "ready",
+		Capabilities: map[string]any{"claude": map[string]any{"status": "ready", "auth_status": authStatus}},
+		CreatedAt:    now, UpdatedAt: now,
+	}
+	if err := fixture.database.CreateWorkspace(fixture.ctx, workspace); err != nil {
+		t.Fatal(err)
+	}
+}
+
+func setPetWorkspaceProbe(t *testing.T, fixture *petTaskFixture, authStatus string) {
+	t.Helper()
+	workspace, err := fixture.service.ensurePetWorkspace(fixture.ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	workspace.Capabilities = map[string]any{"claude": map[string]any{"status": "ready", "auth_status": authStatus}}
+	if err := fixture.database.UpdateWorkspaceDetection(fixture.ctx, workspace); err != nil {
+		t.Fatal(err)
+	}
+}
+
+// M4: only the actual execution host's probe may gate local Claude chat. A
+// remote SSH/WSL Workspace's not_logged_in must never block a working local
+// account, regardless of list order.
+func TestClaudeNativeAvailabilityIgnoresRemoteProbe(t *testing.T) {
+	cases := []struct {
+		name  string
+		setup func(*testing.T, *petTaskFixture)
+		want  bool
+	}{
+		{
+			name: "remote not_logged_in before a working local probe",
+			setup: func(t *testing.T, fixture *petTaskFixture) {
+				createProbeWorkspace(t, fixture, "workspace-remote", "remote", "ssh", "not_logged_in")
+				createProbeWorkspace(t, fixture, "workspace-local", "local", "native", "logged_in")
+			},
+			want: true,
+		},
+		{
+			name: "working local probe before a remote negative",
+			setup: func(t *testing.T, fixture *petTaskFixture) {
+				createProbeWorkspace(t, fixture, "workspace-local", "local", "native", "logged_in")
+				createProbeWorkspace(t, fixture, "workspace-remote", "remote", "ssh", "not_logged_in")
+			},
+			want: true,
+		},
+		{
+			name: "only a remote negative probe",
+			setup: func(t *testing.T, fixture *petTaskFixture) {
+				createProbeWorkspace(t, fixture, "workspace-remote", "remote", "ssh", "not_logged_in")
+			},
+			want: true,
+		},
+		{
+			name: "local negative with a remote positive",
+			setup: func(t *testing.T, fixture *petTaskFixture) {
+				createProbeWorkspace(t, fixture, "workspace-remote", "remote", "ssh", "logged_in")
+				createProbeWorkspace(t, fixture, "workspace-local", "local", "native", "not_logged_in")
+			},
+			want: false,
+		},
+		{
+			name: "WSL is a different login",
+			setup: func(t *testing.T, fixture *petTaskFixture) {
+				createProbeWorkspace(t, fixture, "workspace-wsl", "local", "wsl", "not_logged_in")
+			},
+			want: true,
+		},
+		{
+			name: "unprobed pet workspace falls back to the local native probe",
+			setup: func(t *testing.T, fixture *petTaskFixture) {
+				if _, err := fixture.service.ensurePetWorkspace(fixture.ctx); err != nil {
+					t.Fatal(err)
+				}
+				createProbeWorkspace(t, fixture, "workspace-remote", "remote", "ssh", "not_logged_in")
+				createProbeWorkspace(t, fixture, "workspace-local", "local", "native", "logged_in")
+			},
+			want: true,
+		},
+		{
+			name: "pet workspace probe is authoritative for its execution host",
+			setup: func(t *testing.T, fixture *petTaskFixture) {
+				setPetWorkspaceProbe(t, fixture, "not_logged_in")
+				createProbeWorkspace(t, fixture, "workspace-local", "local", "native", "logged_in")
+			},
+			want: false,
+		},
+		{
+			name: "unprobed pet workspace ignores remote-only negatives",
+			setup: func(t *testing.T, fixture *petTaskFixture) {
+				if _, err := fixture.service.ensurePetWorkspace(fixture.ctx); err != nil {
+					t.Fatal(err)
+				}
+				createProbeWorkspace(t, fixture, "workspace-remote", "remote", "ssh", "not_logged_in")
+			},
+			want: true,
+		},
+	}
+	for _, testCase := range cases {
+		t.Run(testCase.name, func(t *testing.T) {
+			fixture := newPetTaskFixture(t)
+			testCase.setup(t, fixture)
+			available, reason := fixture.service.claudeNativeAvailability(fixture.ctx)
+			if available != testCase.want {
+				t.Fatalf("available=%t reason=%q, want %t", available, reason, testCase.want)
+			}
+			if !testCase.want && !strings.Contains(reason, "尚未登录") {
+				t.Fatalf("unavailable reason=%q", reason)
+			}
+		})
+	}
+}
+
 // Claude native availability follows the Workspace probe when there is one, and
 // stays unknown (available) when no probe exists so the CLI reports the real
 // authentication result.
~~~~

- delta 字节数：19418
- delta sha256：5288c7a32bc2310097ee9171540fa981652438c887c596b83104c02d9d41ef19
