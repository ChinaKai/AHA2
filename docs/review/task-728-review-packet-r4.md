# task-728 review packet r4：M4 unknown 与明确已登录分离

- 上一版：实现 `ee276b2`，材料 `7848fef`（r3，复审结论 REQUEST_CHANGES：只剩 M4）
- 本版：实现 `0455c0f`，分支 `aha/task_07933ff5b7797feffa4c`，worktree `.aha2-worktrees/task_07933ff5b7797feffa4c`
- 没有 push、没有 merge、没有 complete
- 本包覆盖 `ee276b2..0455c0f` 的源码 delta（不含 `docs/review`），5,369 字节，sha256 `92c4fa67b32482deff8868ef241657fbfdd350fa5c8589e379c1b6f0bbe2a183`
- 还原：取文末 `~~~~diff` 与 `~~~~` 之间的内容，就是 `git diff ee276b2 0455c0f -- . ':!docs/review'`（已用 `cmp` 核对）。

## M4：unknown 被当成“明确已登录”

- 根因：`claudeProbeAvailability` 对 `auth_status=unknown` 返回 `available=true`，聚合循环直接把它计入 `loggedIn`；于是“允许尝试 CLI”被当成了“明确已登录”，unknown 压过了同机另一份 `not_logged_in`。
- 修法：把探测结果拆成三态 `logged_in` / `not_logged_in` / `unknown`（`claudeProbeAuthState`）。
  - 普通本机 native 候选：只有显式 `logged_in` 计为正证据；`unknown` 不参与正负判断；存在显式 `not_logged_in` 且没有正证据时，聚合为不可用。
  - 宠物 Workspace 自身的探测仍是权威：`not_logged_in` 不可用；`logged_in` 和 `unknown` 都保持可用（unknown 交给 CLI 报真实结果），行为不变。
  - 远程/WSL 候选仍被排除；顺序不影响结果。
- 新增回归子用例（`TestClaudeNativeAvailabilityIgnoresRemoteProbe`，现共 12 个子用例）：
  - 宠物自身无探测 + 本机 `unknown` 在前、`not_logged_in` 在后 → 不可用；
  - 宠物自身无探测 + 本机 `not_logged_in` 在前、`unknown` 在后 → 不可用（两种排列）；
  - 宠物自身 `unknown` + 本机 `not_logged_in` → 可用（权威规则不变的对照）；
  - 只有本机 `unknown` → 可用（允许尝试 CLI 的对照）。
- 没有改动 M1/M2/M3；M2 仍按 owner 的完全访问口径。

## 命令与退出码（实跑）

| 命令 | 退出码 |
| --- | --- |
| `go build ./...` | 0 |
| `go vet ./internal/app` | 0 |
| `go test -c -o <scratch>/bin/app.test ./internal/app` | 0 |
| `env -u GOTMPDIR TMPDIR=/tmp/task-728-app <scratch>/bin/app.test -test.count=1 -test.run '^TestClaudeNativeAvailabilityIgnoresRemoteProbe$'` | 0（PASS，12 子用例） |
| app 全包：同一个测试二进制 `-test.timeout=25m` | 0（0 FAIL） |

## 未验证缺口

- 仍没有真实 Claude Code / Codex 账号；M4 的探测判定由 Workspace 夹具覆盖，真实远程与本地探测未实测。
- 评审沙箱不能启动 Go 测试；以上是 builder 本机实跑。

## 本版 delta（ee276b2..0455c0f，排除 docs/review）

~~~~diff
diff --git a/internal/app/assistant.go b/internal/app/assistant.go
index 379fd7a..0759b1c 100644
--- a/internal/app/assistant.go
+++ b/internal/app/assistant.go
@@ -311,30 +311,37 @@ func (s *Service) claudeNativeAvailability(ctx context.Context) (bool, string) {
 		return true, ""
 	}
 	// The Pet Workspace is where chat actually runs. If it has its own probe,
-	// that probe is authoritative even if it belongs to a remote host.
+	// that probe is authoritative even if it belongs to a remote host. An
+	// unknown result there stays "try the CLI", as it did before.
 	for _, workspace := range workspaces {
 		if workspace.ID != petWorkspaceID {
 			continue
 		}
-		if ok, available, reason := claudeProbeAvailability(workspace); ok {
-			return available, reason
+		if ok, state, reason := claudeProbeAuthState(workspace); ok {
+			if state == claudeAuthNotLoggedIn {
+				return false, reason
+			}
+			return true, ""
 		}
 	}
 	// Only local native Workspaces share the default Claude login with the
 	// local Pet Workspace. A remote Workspace's not_logged_in must never block
 	// a working local account, and list order must not decide it either.
+	// Only an explicit logged_in is positive evidence: "unknown" means "try the
+	// CLI", so it must not cancel an explicit not_logged_in.
 	loggedIn, notLoggedIn := false, false
 	for _, workspace := range workspaces {
 		if workspace.ID == petWorkspaceID || !claudeNativeHostWorkspace(workspace) {
 			continue
 		}
-		ok, available, _ := claudeProbeAvailability(workspace)
+		ok, state, _ := claudeProbeAuthState(workspace)
 		if !ok {
 			continue
 		}
-		if available {
+		switch state {
+		case claudeAuthLoggedIn:
 			loggedIn = true
-		} else {
+		case claudeAuthNotLoggedIn:
 			notLoggedIn = true
 		}
 	}
@@ -358,25 +365,31 @@ func claudeNativeHostWorkspace(workspace domain.Workspace) bool {
 	return workspace.Transport == "native"
 }
 
-// claudeProbeAvailability reads one Workspace's Claude probe. ok=false means
-// the Workspace has no successful probe and contributes no evidence.
-func claudeProbeAvailability(workspace domain.Workspace) (ok, available bool, reason string) {
+const (
+	claudeAuthLoggedIn    = "logged_in"
+	claudeAuthNotLoggedIn = "not_logged_in"
+	claudeAuthUnknown     = "unknown"
+)
+
+// claudeProbeAuthState reads one Workspace's Claude probe. ok=false means the
+// Workspace has no usable probe and contributes no evidence. The three states
+// are deliberately distinct: "unknown" may try the CLI, but it is not proof
+// that this host is logged in.
+func claudeProbeAuthState(workspace domain.Workspace) (ok bool, state, reason string) {
 	probe, found := workspace.Capabilities["claude"].(map[string]any)
 	if !found {
-		return false, true, ""
+		return false, "", ""
 	}
 	if status, _ := probe["status"].(string); status != "ready" {
-		return false, true, ""
+		return false, "", ""
 	}
 	switch authStatus, _ := probe["auth_status"].(string); authStatus {
-	case "not_logged_in":
-		return true, false, "尚未登录 Claude Code，请先执行 claude auth login"
-	case "logged_in":
-		return true, true, ""
+	case claudeAuthNotLoggedIn:
+		return true, claudeAuthNotLoggedIn, "尚未登录 Claude Code，请先执行 claude auth login"
+	case claudeAuthLoggedIn:
+		return true, claudeAuthLoggedIn, ""
 	default:
-		// "unknown" is not evidence of a failure. Keeping it available lets the
-		// CLI report the real authentication result.
-		return true, true, ""
+		return true, claudeAuthUnknown, ""
 	}
 }
 
diff --git a/internal/app/assistant_unified_model_test.go b/internal/app/assistant_unified_model_test.go
index 2acfc7f..f0367d9 100644
--- a/internal/app/assistant_unified_model_test.go
+++ b/internal/app/assistant_unified_model_test.go
@@ -883,6 +883,43 @@ func TestClaudeNativeAvailabilityIgnoresRemoteProbe(t *testing.T) {
 			},
 			want: true,
 		},
+		{
+			name: "local unknown before an explicit local negative",
+			setup: func(t *testing.T, fixture *petTaskFixture) {
+				if _, err := fixture.service.ensurePetWorkspace(fixture.ctx); err != nil {
+					t.Fatal(err)
+				}
+				createProbeWorkspace(t, fixture, "workspace-unknown", "local", "native", "unknown")
+				createProbeWorkspace(t, fixture, "workspace-negative", "local", "native", "not_logged_in")
+			},
+			want: false,
+		},
+		{
+			name: "local negative before a local unknown",
+			setup: func(t *testing.T, fixture *petTaskFixture) {
+				if _, err := fixture.service.ensurePetWorkspace(fixture.ctx); err != nil {
+					t.Fatal(err)
+				}
+				createProbeWorkspace(t, fixture, "workspace-negative", "local", "native", "not_logged_in")
+				createProbeWorkspace(t, fixture, "workspace-unknown", "local", "native", "unknown")
+			},
+			want: false,
+		},
+		{
+			name: "pet workspace unknown stays available",
+			setup: func(t *testing.T, fixture *petTaskFixture) {
+				setPetWorkspaceProbe(t, fixture, "unknown")
+				createProbeWorkspace(t, fixture, "workspace-negative", "local", "native", "not_logged_in")
+			},
+			want: true,
+		},
+		{
+			name: "only local unknown probes still allow the CLI",
+			setup: func(t *testing.T, fixture *petTaskFixture) {
+				createProbeWorkspace(t, fixture, "workspace-unknown", "local", "native", "unknown")
+			},
+			want: true,
+		},
 	}
 	for _, testCase := range cases {
 		t.Run(testCase.name, func(t *testing.T) {
~~~~

- delta 字节数：5369
- delta sha256：92c4fa67b32482deff8868ef241657fbfdd350fa5c8589e379c1b6f0bbe2a183
