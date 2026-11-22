package httpapi

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/ChinaKai/AHA2/internal/domain"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects(t *testing.T) {
	for _, pet := range []bool{false, true} {
		t.Run(fmt.Sprintf("pet=%v", pet), func(t *testing.T) {
			f := newHardwareAuthorizationFixture(t, pet)
			connected := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"})
			if connected.IsError || connected.Status != 200 {
				t.Fatal("fixture connect failed")
			}
			conn := f.connection(t)
			raw, err := sql.Open("sqlite", f.h.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			snapshot, err := f.h.database.RuntimeSnapshot(f.h.ctx, f.task.RuntimeConfigSnapshotID)
			if err != nil {
				t.Fatal(err)
			}
			readSnapshot := snapshot
			readSnapshot.ID = domain.NewID("runtime")
			readSnapshot.PermissionsJSON = `{"filesystem":"read-only","approval":"never"}`
			if err := f.h.database.CreateRuntimeSnapshot(f.h.ctx, readSnapshot); err != nil {
				t.Fatal(err)
			}
			if _, err := f.h.database.UpsertTaskAgent(f.h.ctx, domain.TaskAgent{TaskID: f.task.ID, AgentID: "sub", Role: "sub", Status: "idle", RuntimeConfigSnapshotID: snapshot.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			reset := func() {
				t.Helper()
				if _, err := raw.Exec(`UPDATE turns SET agent_id='main',runtime_config_snapshot_id=?,status='running' WHERE id=?`, snapshot.ID, f.turn.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := raw.Exec(`UPDATE tasks SET status=? WHERE id=?`, string(f.task.Status), f.task.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := raw.Exec(`DELETE FROM agent_inbox WHERE batch_id=?`, f.turn.InboxBatchID); err != nil {
					t.Fatal(err)
				}
				for _, resource := range []domain.HardwareResource{f.board, f.adb} {
					if err := f.h.database.UpsertHardwareResource(f.h.ctx, resource); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.h.database.ReplaceTaskHardwareBindings(f.h.ctx, f.task.ID, []domain.TaskHardwareBinding{{TaskID: f.task.ID, ID: "board", ResourceID: f.board.ID}, {TaskID: f.task.ID, ID: "device", ResourceID: f.adb.ID}}); err != nil {
					t.Fatal(err)
				}
				seedKnowledgeMCPInbox(t, f.h.dbPath, f.task, f.turn, "channel:task_route")
				f.token = f.h.issue(f.task.ID, "main", f.turn.ID)
				if pet {
					assistant, _ := f.h.database.EnsureOwnerAssistant(f.h.ctx, time.Now())
					if err := f.h.database.UpdateOwnerPetPermissions(f.h.ctx, assistant.ID, true, true, false, false, false, true, false, false, time.Now()); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, scenario := range []string{"non_main", "read_only_snapshot", "read_only_resource", "revoked_binding", "terminal_task", "revoked_pet"} {
				if scenario == "revoked_pet" && !pet {
					continue
				}
				t.Run(scenario, func(t *testing.T) {
					reset()
					defer reset()
					switch scenario {
					case "non_main":
						_, err = raw.Exec(`UPDATE turns SET agent_id='sub' WHERE id=?`, f.turn.ID)
						f.token = f.h.issue(f.task.ID, "sub", f.turn.ID)
					case "read_only_snapshot":
						_, err = raw.Exec(`UPDATE turns SET runtime_config_snapshot_id=? WHERE id=?`, readSnapshot.ID, f.turn.ID)
					case "read_only_resource":
						for _, resource := range []domain.HardwareResource{f.board, f.adb} {
							resource.Access = domain.HardwareAccessReadOnly
							if err = f.h.database.UpsertHardwareResource(f.h.ctx, resource); err != nil {
								t.Fatal(err)
							}
						}
					case "terminal_task":
						_, err = raw.Exec(`UPDATE tasks SET status='completed' WHERE id=?`, f.task.ID)
					case "revoked_pet":
						if status := f.raw(t, http.MethodPost, "/device/adb/exec", map[string]any{"args": []string{"shell", "id"}}); status != 200 {
							t.Fatal("pre-revocation HTTP ADB failed")
						}
						assistant, _ := f.h.database.EnsureOwnerAssistant(f.h.ctx, time.Now())
						err = f.h.database.UpdateOwnerPetPermissions(f.h.ctx, assistant.ID, false, false, false, false, false, false, false, false, time.Now())
					case "revoked_binding":
						err = f.h.database.ReplaceTaskHardwareBindings(f.h.ctx, f.task.ID, nil)
					}
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "read_only_snapshot" || scenario == "non_main" {
						assertControlCapabilities(t, f.server.URL, f.token, "hardware", true, false)
						status, names, _ := coreMCPToolsList(t, f.server.URL+"/api/v1/agent/hardware/mcp", f.token)
						if status != 200 || len(names) != 2 {
							t.Fatalf("read-only tools=%d %v", status, names)
						}
					}
					beforeAccepts, beforeExec := f.accepts.Load(), f.executions.Load()
					for _, op := range []struct {
						name, path string
						args       map[string]any
						body       any
					}{
						{"hardware_connect", "/board/connect", map[string]any{"hardware_id": "board"}, map[string]any{}},
						{"hardware_disconnect", "/board/disconnect", map[string]any{"hardware_id": "board"}, map[string]any{}},
						{"hardware_send", "/board/send", map[string]any{"hardware_id": "board", "data": "NO-EFFECT\n"}, map[string]any{"data": "NO-EFFECT\n"}},
						{"hardware_login", "/board/login", map[string]any{"hardware_id": "board", "wakeup": true}, map[string]any{"wakeup": true}},
						{"hardware_adb_exec", "/device/adb/exec", map[string]any{"hardware_id": "device", "args": []string{"shell", "id"}}, map[string]any{"args": []string{"shell", "id"}}},
					} {
						mcp := f.mcp(t, op.name, op.args)
						if mcp.Status != http.StatusForbidden {
							t.Fatalf("%s MCP status=%d", op.name, mcp.Status)
						}
						if status := f.raw(t, http.MethodPost, op.path, op.body); status != http.StatusForbidden {
							t.Fatalf("%s HTTP status=%d", op.name, status)
						}
					}

					_ = conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
					buffer := make([]byte, 128)
					if n, _ := conn.Read(buffer); n != 0 {
						t.Fatal("denied send/login reached fake transport")
					}
					if f.accepts.Load() != beforeAccepts || f.executions.Load() != beforeExec || !f.manager.Status(f.task.ID, "board", "network", false).Connected {
						t.Fatal("denied operation changed transport state")
					}
					if scenario == "revoked_pet" {
						if status := f.raw(t, http.MethodGet, "", nil); status != 403 {
							t.Fatalf("HTTP read after revocation status=%d", status)
						}
						if result := f.mcp(t, "hardware_list", map[string]any{}); result.Status != 403 {
							t.Fatal("MCP read after revocation succeeded")
						}
					}
				})
			}
		})
	}
}

func TestOwnerWebHardwarePolicyIsSeparateFromAgentSnapshot(t *testing.T) {
	f := newHardwareAuthorizationFixture(t, false)
	snapshot, err := f.h.database.RuntimeSnapshot(f.h.ctx, f.task.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.ID = domain.NewID("runtime")
	snapshot.PermissionsJSON = `{"filesystem":"read-only","approval":"never"}`
	if err := f.h.database.CreateRuntimeSnapshot(f.h.ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", f.h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`UPDATE turns SET runtime_config_snapshot_id=? WHERE id=?`, snapshot.ID, f.turn.ID); err != nil {
		t.Fatal(err)
	}
	if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"}); result.Status != 403 {
		t.Fatal("read-only Agent connected")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, f.server.URL)
	base := f.server.URL + "/api/v1/tasks/" + f.task.ID + "/hardware/board"
	response := requestJSON(t, client, http.MethodPost, base+"/connect", map[string]any{}, csrf)
	if response.StatusCode != 200 {
		t.Fatalf("owner connect status=%d", response.StatusCode)
	}
	response.Body.Close()
	conn := f.connection(t)
	response = requestJSON(t, client, http.MethodPost, base+"/send", map[string]any{"data": "OWNER-FIXTURE\n"}, csrf)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("owner send status=%d", response.StatusCode)
	}
	response.Body.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if line, err := bufio.NewReader(conn).ReadString('\n'); err != nil || line != "OWNER-FIXTURE\n" {
		t.Fatal("owner send did not reach fixture")
	}
	if f.raw(t, http.MethodPost, "/board/disconnect", map[string]any{}) != 403 || !f.manager.Status(f.task.ID, "board", "network", false).Connected {
		t.Fatal("read-only Agent disconnected owner transport")
	}
	response = requestJSON(t, client, http.MethodPost, base+"/disconnect", map[string]any{}, csrf)
	if response.StatusCode != 200 {
		t.Fatal("owner disconnect was tightened")
	}
	response.Body.Close()
	if f.manager.Status(f.task.ID, "board", "network", false).Connected {
		t.Fatal("owner disconnect did not close transport")
	}
}

func TestReadOnlyResourceCannotConnectOrDisconnectThroughEitherAgentSurface(t *testing.T) {
	f := newHardwareAuthorizationFixture(t, false)
	resource := f.board
	resource.Access = domain.HardwareAccessReadOnly
	if err := f.h.database.UpsertHardwareResource(f.h.ctx, resource); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"connect", "disconnect"} {
		if result := f.mcp(t, "hardware_"+path, map[string]any{"hardware_id": "board"}); result.Status != 403 {
			t.Fatalf("read-only %s MCP status=%d", path, result.Status)
		}
		if status := f.raw(t, http.MethodPost, "/board/"+path, map[string]any{}); status != 403 {
			t.Fatalf("read-only %s HTTP status=%d", path, status)
		}
	}
	if f.accepts.Load() != 0 || f.manager.Status(f.task.ID, "board", "network", false).Connected {
		t.Fatal("read-only resource opened a transport")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, f.server.URL)
	base := f.server.URL + "/api/v1/tasks/" + f.task.ID + "/hardware/board"
	response := requestJSON(t, client, http.MethodPost, base+"/connect", map[string]any{}, csrf)
	if response.StatusCode != 200 {
		t.Fatalf("owner read-only connection policy changed: %d", response.StatusCode)
	}
	response.Body.Close()
	_ = f.connection(t)
	if result := f.mcp(t, "hardware_disconnect", map[string]any{"hardware_id": "board"}); result.Status != 403 {
		t.Fatal("Agent detached read-only transport")
	}
	if f.raw(t, http.MethodPost, "/board/disconnect", map[string]any{}) != 403 || !f.manager.Status(f.task.ID, "board", "network", true).Connected {
		t.Fatal("HTTP Agent detached read-only transport")
	}
	response = requestJSON(t, client, http.MethodPost, base+"/send", map[string]any{"data": "FORBIDDEN"}, csrf)
	if response.StatusCode != 403 {
		t.Fatal("owner resource read-only policy was widened")
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, base+"/disconnect", map[string]any{}, csrf)
	if response.StatusCode != 200 {
		t.Fatal("owner disconnect policy changed")
	}
	response.Body.Close()
}

func TestAgentOperationReturnsOnlyFixedFailureCode(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/fixed", nil)
	for _, code := range []string{"ssh_host_key_unknown", "hardware_read_only", "UNTRUSTED-SECRET-MARKER"} {
		op := agentMCPOperation{Tool: mcpTool{InputSchema: mcpObject(map[string]any{})}, Method: http.MethodPost, Handler: func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": code, "message": "SECRET-DETAIL", "credential": "SECRET-DETAIL"})
		}}
		_, err := runAgentMCPOperation(request, op, json.RawMessage(`{}`))
		typed, ok := err.(*agentOperationError)
		if !ok || typed.Status != 403 || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("unsafe failure projection")
		}
		if code != "UNTRUSTED-SECRET-MARKER" && typed.Code != code {
			t.Fatal("known error lost its classification")
		}
	}
}
