package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
)

func TestClassifyADBForward(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		action adbForwardAction
		local  string
		ok     bool
	}{
		{"list", []string{"--list"}, adbForwardList, "", true},
		{"uppercase_list", []string{"--LIST"}, adbForwardList, "", true},
		{"remove_all", []string{"--remove-all"}, adbForwardRemoveAll, "", true},
		{"uppercase_remove_all", []string{"--REMOVE-ALL"}, adbForwardRemoveAll, "", true},
		{"remove_all_with_trailing", []string{"--remove-all", "tcp:1"}, adbForwardRemoveAll, "", true},
		{"remove", []string{"--remove", "tcp:1"}, adbForwardRemove, "tcp:1", true},
		{"uppercase_remove", []string{"--REMOVE", "tcp:1"}, adbForwardRemove, "tcp:1", true},
		{"remove_missing_local", []string{"--remove"}, adbForwardInvalid, "", false},
		{"remove_extra", []string{"--remove", "tcp:1", "tcp:2"}, adbForwardInvalid, "", false},
		{"remove_option_local", []string{"--remove", "--remove-all"}, adbForwardInvalid, "", false},
		{"add", []string{"tcp:1", "tcp:2"}, adbForwardAdd, "tcp:1", true},
		{"add_missing_remote", []string{"tcp:1"}, adbForwardInvalid, "", false},
		{"add_extra", []string{"tcp:1", "tcp:2", "tcp:3"}, adbForwardInvalid, "", false},
		{"no_rebind", []string{"--no-rebind", "tcp:1", "tcp:2"}, adbForwardAdd, "tcp:1", true},
		{"no_rebind_missing_remote", []string{"--no-rebind", "tcp:1"}, adbForwardInvalid, "", false},
		{"unknown_option", []string{"--rebind", "tcp:1", "tcp:2"}, adbForwardInvalid, "", false},
		{"empty", nil, adbForwardInvalid, "", false},
		{"empty_local", []string{"", "tcp:2"}, adbForwardInvalid, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			action, local, ok := classifyADBForward(tc.args)
			if action != tc.action || local != tc.local || ok != tc.ok {
				t.Fatalf("classifyADBForward(%q)=(%v, %q, %v), want (%v, %q, %v)", tc.args, action, local, ok, tc.action, tc.local, tc.ok)
			}
		})
	}
}

type adbExecOutcome struct {
	allowed bool
	code    string
	message string
}

// callAgentADBExec issues one hardware_adb_exec request over HTTP or MCP and
// normalizes both answers: allowed means the handler reached the executor.
func callAgentADBExec(t *testing.T, serverURL, token string, group domain.HardwareGroup, args []string, mcp bool) adbExecOutcome {
	t.Helper()
	if mcp {
		result := coreMCPCallTool(t, serverURL+"/api/v1/agent/hardware/mcp", token, "hardware_adb_exec", map[string]any{"hardware_id": group.ID, "args": args})
		if result.Status != http.StatusOK {
			t.Fatalf("MCP status=%d args=%q", result.Status, args)
		}
		if result.IsError {
			code, message := result.Text, ""
			if index := strings.Index(result.Text, ": "); index >= 0 {
				code, message = result.Text[:index], result.Text[index+2:]
			}
			return adbExecOutcome{code: code, message: message}
		}
		var payload struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal([]byte(result.Text), &payload); err != nil {
			t.Fatalf("MCP result is not JSON: %v text=%q", err, result.Text)
		}
		return adbExecOutcome{allowed: payload.OK}
	}
	response := agentRequest(t, serverURL+"/api/v1/agent/hardware/"+group.ID+"/adb/exec", http.MethodPost, token, map[string]any{"args": args})
	var payload struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode == http.StatusOK && payload.OK {
		return adbExecOutcome{allowed: true}
	}
	return adbExecOutcome{code: payload.Error, message: payload.Message}
}

func expectAgentADBExec(t *testing.T, serverURL, token string, group domain.HardwareGroup, args []string, mcp bool, executions *atomic.Int32, wantAllowed bool, wantCode string) {
	t.Helper()
	before := executions.Load()
	outcome := callAgentADBExec(t, serverURL, token, group, args, mcp)
	if outcome.allowed != wantAllowed {
		t.Fatalf("mcp=%v args=%q allowed=%v code=%q message=%q, want allowed=%v", mcp, args, outcome.allowed, outcome.code, outcome.message, wantAllowed)
	}
	if !wantAllowed && outcome.code != wantCode {
		t.Fatalf("mcp=%v args=%q code=%q message=%q, want code=%q", mcp, args, outcome.code, outcome.message, wantCode)
	}
	wantExecutions := int32(0)
	if wantAllowed {
		wantExecutions = 1
	}
	if got := executions.Load() - before; got != wantExecutions {
		t.Fatalf("mcp=%v args=%q executions=%d, want %d", mcp, args, got, wantExecutions)
	}
}

// TestAgentADBForwardDeletePolicy covers the host-wide forward deletes over
// both transports: --remove-all is always refused, --remove is limited to the
// local names this Task created through the same hardware binding, and the
// add/list forms keep working.
func TestAgentADBForwardDeletePolicy(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	now := time.Now().UTC()
	groups := []domain.HardwareGroup{
		{ID: "usb", Network: domain.HardwareNetworkConfig{ADBConnection: "usb", ADBSerial: "123SERIAL"}},
		{ID: "tcp", Network: domain.HardwareNetworkConfig{ADBConnection: "tcp", Host: "192.0.2.10", Port: 5555}},
		{ID: "wireless", Network: domain.HardwareNetworkConfig{Host: "192.0.2.11", Port: 41234}},
	}
	for index := range groups {
		groups[index].Mode, groups[index].Access = domain.HardwareModeNetwork, domain.HardwareAccessReadWrite
		groups[index].Network.Protocol = domain.HardwareProtocolADB
		groups[index].CreatedAt, groups[index].UpdatedAt = now, now
	}
	if err := h.database.ReplaceHardwareGroups(h.ctx, task.ID, groups); err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	results := map[string]hardware.ADBExecResult{
		// adb prints the port it picked for tcp:0; only that port can be removed.
		"forward tcp:0 tcp:8080":     {Stdout: []byte("41234\r\n")},
		"forward tcp:22003 tcp:8080": {ExitCode: 1, Stderr: []byte("error: cannot bind listener")},
	}
	executor := func(_ context.Context, _ string, args []string, _ []byte, _ time.Duration) (hardware.ADBExecResult, error) {
		executions.Add(1)
		if result, ok := results[strings.Join(args, " ")]; ok {
			return result, nil
		}
		return hardware.ADBExecResult{Stdout: []byte("ok")}, nil
	}
	server := httptest.NewServer(New(Config{
		Store: h.database, App: h.service, AgentCapabilities: h.caps,
		ExecuteADB: executor, ExecuteADBTCP: executor, ExecuteADBUSB: executor,
	}).Handler())
	defer server.Close()
	turn := createCoreTurn(t, h.database, task, "main", "batch-adb-forward-delete-policy")
	token := h.issue(task.ID, "main", turn.ID)

	for _, group := range groups {
		target := group.Network.ADBSerial
		switch group.ID {
		case "tcp":
			target = "192.0.2.10:5555"
		case "wireless":
			target = "192.0.2.11:41234"
		}
		// A wireless binding cannot verify a caller-supplied serial at all, so
		// its -s forms stop at the resource boundary with the generic code.
		serialBlockedCode := "adb_forward_remove_all_not_allowed"
		if group.Network.ADBWireless() {
			serialBlockedCode = "adb_command_not_allowed"
		}
		for _, mcp := range []bool{false, true} {
			name := group.ID + "/http"
			if mcp {
				name = group.ID + "/mcp"
			}
			t.Run(name, func(t *testing.T) {
				// Add then remove the same local name: the first remove succeeds
				// and drops the record, so the second is refused.
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "tcp:22001", "tcp:8080"}, mcp, &executions, true, "")
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:22001"}, mcp, &executions, true, "")
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:22001"}, mcp, &executions, false, "adb_forward_remove_not_allowed")

				// --no-rebind is an add form and records its local name too.
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--no-rebind", "tcp:22004", "tcp:8080"}, mcp, &executions, true, "")
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:22004"}, mcp, &executions, true, "")

				// tcp:0 is recorded under the port adb actually picked.
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "tcp:0", "tcp:8080"}, mcp, &executions, true, "")
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:41234"}, mcp, &executions, true, "")
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:41234"}, mcp, &executions, false, "adb_forward_remove_not_allowed")

				// An unknown local name is never removable.
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:29999"}, mcp, &executions, false, "adb_forward_remove_not_allowed")

				// A failed add created nothing and must not be recorded.
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "tcp:22003", "tcp:8080"}, mcp, &executions, true, "")
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove", "tcp:22003"}, mcp, &executions, false, "adb_forward_remove_not_allowed")

				// --remove-all stays blocked in every spelling and prefix form.
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--remove-all"}, mcp, &executions, false, "adb_forward_remove_all_not_allowed")
				expectAgentADBExec(t, server.URL, token, group, []string{"FORWARD", "--REMOVE-ALL"}, mcp, &executions, false, "adb_forward_remove_all_not_allowed")
				expectAgentADBExec(t, server.URL, token, group, []string{"Forward", "--Remove-All", "tcp:1"}, mcp, &executions, false, "adb_forward_remove_all_not_allowed")
				expectAgentADBExec(t, server.URL, token, group, []string{"wait-for-device", "forward", "--remove-all"}, mcp, &executions, false, "adb_forward_remove_all_not_allowed")
				expectAgentADBExec(t, server.URL, token, group, []string{"--exit-on-write-error", "forward", "--remove-all"}, mcp, &executions, false, "adb_forward_remove_all_not_allowed")
				expectAgentADBExec(t, server.URL, token, group, []string{"-s", target, "forward", "--remove-all"}, mcp, &executions, false, serialBlockedCode)
				expectAgentADBExec(t, server.URL, token, group, []string{"-s" + target, "forward", "--remove-all"}, mcp, &executions, false, serialBlockedCode)

				// Unknown forward subcommands still fail closed without running.
				expectAgentADBExec(t, server.URL, token, group, []string{"forward", "--rebind", "tcp:1", "tcp:2"}, mcp, &executions, false, "adb_command_not_allowed")
			})
		}
	}

	// The registry is scoped by hardware binding: a local name created through
	// the USB binding is not removable through the TCP binding, even though
	// both hit the same adb server.
	usb, tcp := groups[0], groups[1]
	expectAgentADBExec(t, server.URL, token, usb, []string{"forward", "tcp:22005", "tcp:8080"}, false, &executions, true, "")
	expectAgentADBExec(t, server.URL, token, tcp, []string{"forward", "--remove", "tcp:22005"}, false, &executions, false, "adb_forward_remove_not_allowed")
	expectAgentADBExec(t, server.URL, token, usb, []string{"forward", "--remove", "tcp:22005"}, false, &executions, true, "")
}

// adbForwardTestUSBGroup builds a read-write USB ADB binding. The serial is the
// configured target and therefore part of the registry scope.
func adbForwardTestUSBGroup(id, serial string) domain.HardwareGroup {
	now := time.Now().UTC()
	return domain.HardwareGroup{
		ID: id,
		Network: domain.HardwareNetworkConfig{
			Protocol:      domain.HardwareProtocolADB,
			ADBConnection: domain.HardwareADBConnectionUSB,
			ADBSerial:     serial,
		},
		Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadWrite,
		CreatedAt: now, UpdatedAt: now,
	}
}

// newADBForwardTestTask creates a Task with the given bindings plus a running
// turn token, which is what the Agent ADB endpoint authenticates against.
func newADBForwardTestTask(t *testing.T, h *coreMCPHarness, groups []domain.HardwareGroup, batchID string) (domain.Task, string) {
	t.Helper()
	task := createHardwareAPITask(t, h.database)
	if err := h.database.ReplaceHardwareGroups(h.ctx, task.ID, groups); err != nil {
		t.Fatal(err)
	}
	turn := createCoreTurn(t, h.database, task, "main", batchID)
	return task, h.issue(task.ID, "main", turn.ID)
}

// newADBForwardTestServer wires the Agent ADB executors to a fake adb that
// reports success unless the joined argv has an explicit result. Counted calls
// let each assertion tell "policy refused before execution" from "adb ran".
func newADBForwardTestServer(t *testing.T, h *coreMCPHarness, executions *atomic.Int32, results map[string]hardware.ADBExecResult) *httptest.Server {
	t.Helper()
	executor := func(_ context.Context, _ string, args []string, _ []byte, _ time.Duration) (hardware.ADBExecResult, error) {
		executions.Add(1)
		if result, ok := results[strings.Join(args, " ")]; ok {
			return result, nil
		}
		return hardware.ADBExecResult{Stdout: []byte("ok")}, nil
	}
	server := httptest.NewServer(New(Config{
		Store: h.database, App: h.service, AgentCapabilities: h.caps,
		ExecuteADB: executor, ExecuteADBTCP: executor, ExecuteADBUSB: executor,
	}).Handler())
	t.Cleanup(server.Close)
	return server
}

// TestADBForwardScopeSeparatesTaskGroupAndTarget pins the registry key itself.
// Each component is a separate isolation boundary, so a key that silently
// drops one of them must make this test fail rather than merely weaken a
// behavior test elsewhere.
func TestADBForwardScopeSeparatesTaskGroupAndTarget(t *testing.T) {
	base := adbForwardScope("task-a", "usb", "SERIAL-A")
	for _, tc := range []struct {
		name  string
		scope string
	}{
		{"task", adbForwardScope("task-b", "usb", "SERIAL-A")},
		{"group", adbForwardScope("task-a", "usb-copy", "SERIAL-A")},
		{"target", adbForwardScope("task-a", "usb", "SERIAL-B")},
	} {
		if tc.scope == base {
			t.Fatalf("scope for a different %s equals the base scope %q", tc.name, base)
		}
	}
}

// TestADBForwardRegistryAddTransfersOwnership covers the rebind rule at the
// registry boundary: one host-wide listener table keyed by local name, so a
// successful add by another scope must take that local name away. The
// unrelated local name proves the transfer is not a wholesale clear.
func TestADBForwardRegistryAddTransfersOwnership(t *testing.T) {
	registry := newADBForwardRegistry()
	scopeA := adbForwardScope("task-a", "usb", "SERIAL-A")
	scopeB := adbForwardScope("task-b", "usb", "SERIAL-B")

	registry.add(scopeA, "tcp:8080")
	registry.add(scopeA, "tcp:8081")
	registry.add(scopeB, "tcp:8080")

	if registry.allows(scopeA, "tcp:8080") {
		t.Fatal("the previous owner can still remove a local name that was rebound")
	}
	if !registry.allows(scopeB, "tcp:8080") {
		t.Fatal("the binding that just added the local name cannot remove it")
	}
	if !registry.allows(scopeA, "tcp:8081") {
		t.Fatal("an unrelated local name was dropped by another scope's add")
	}

	registry.remove(scopeB, "tcp:8080")
	if registry.allows(scopeB, "tcp:8080") {
		t.Fatal("a successful remove left its own record in place")
	}
}

// TestAgentADBForwardRebindInvalidatesPreviousOwner is the S2 regression: Task
// A records tcp:22100, Task B plain-adds the same local name (AOSP rebinds the
// listener to B), and A's later remove must not reach adb. Both transports are
// exercised because they share agentADBExec but have separate error mapping.
func TestAgentADBForwardRebindInvalidatesPreviousOwner(t *testing.T) {
	h := newCoreMCPHarness(t)
	var executions atomic.Int32
	server := newADBForwardTestServer(t, h, &executions, nil)

	// Same group ID, connection and target; only the Task differs, which is
	// exactly the cross-Task collision from the review.
	_, tokenA := newADBForwardTestTask(t, h, []domain.HardwareGroup{adbForwardTestUSBGroup("usb", "SHARED-SERIAL")}, "batch-forward-rebind-a")
	_, tokenB := newADBForwardTestTask(t, h, []domain.HardwareGroup{adbForwardTestUSBGroup("usb", "SHARED-SERIAL")}, "batch-forward-rebind-b")
	groupA, groupB := adbForwardTestUSBGroup("usb", "SHARED-SERIAL"), adbForwardTestUSBGroup("usb", "SHARED-SERIAL")

	for _, mcp := range []bool{false, true} {
		name, basePort := "http", 22100
		if mcp {
			name, basePort = "mcp", 22110
		}
		t.Run(name, func(t *testing.T) {
			local := fmt.Sprintf("tcp:%d", basePort)
			other := fmt.Sprintf("tcp:%d", basePort+1)
			// A creates the listener plus an unrelated one of its own.
			expectAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", local, "tcp:9000"}, mcp, &executions, true, "")
			expectAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", other, "tcp:9001"}, mcp, &executions, true, "")
			// B plain-adds the same local name. adb reports success after
			// rebinding the existing listener to B's transport.
			expectAgentADBExec(t, server.URL, tokenB, groupB, []string{"forward", local, "tcp:9000"}, mcp, &executions, true, "")
			// A's record is stale: allowing this remove would delete B's
			// listener, so it must be refused before the executor runs.
			expectAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", "--remove", local}, mcp, &executions, false, "adb_forward_remove_not_allowed")
			// B owns the rebound listener and can still clean it up.
			expectAgentADBExec(t, server.URL, tokenB, groupB, []string{"forward", "--remove", local}, mcp, &executions, true, "")
			// Invalidation is per local name: A's other listener is untouched.
			expectAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", "--remove", other}, mcp, &executions, true, "")
		})
	}
}

// TestAgentADBForwardMutationsAreSerialized closes the authorize-then-execute
// race: A's remove passes the ownership check and enters the executor, then B
// plain-adds the same local name. Without serialization B's add would rebind
// and invalidate A's record while A's already-authorized remove was still in
// flight, so the remove would delete B's listener. The blocking executor makes
// that interleaving observable: B's add must not reach the executor until A's
// remove has returned.
func TestAgentADBForwardMutationsAreSerialized(t *testing.T) {
	h := newCoreMCPHarness(t)
	_, tokenA := newADBForwardTestTask(t, h, []domain.HardwareGroup{adbForwardTestUSBGroup("usb", "SERIAL-A")}, "batch-forward-serialized-a")
	_, tokenB := newADBForwardTestTask(t, h, []domain.HardwareGroup{adbForwardTestUSBGroup("usb", "SERIAL-B")}, "batch-forward-serialized-b")
	groupA, groupB := adbForwardTestUSBGroup("usb", "SERIAL-A"), adbForwardTestUSBGroup("usb", "SERIAL-B")

	var removeStateMu sync.Mutex
	firstRemove := true
	removeStarted := make(chan struct{})
	releaseRemove := make(chan struct{})
	calls := make(chan []string, 8)
	executor := func(_ context.Context, _ string, args []string, _ []byte, _ time.Duration) (hardware.ADBExecResult, error) {
		calls <- append([]string(nil), args...)
		if len(args) == 3 && args[0] == "forward" && args[1] == "--remove" {
			removeStateMu.Lock()
			block := firstRemove
			firstRemove = false
			removeStateMu.Unlock()
			if block {
				close(removeStarted)
				<-releaseRemove
			}
		}
		return hardware.ADBExecResult{Stdout: []byte("ok")}, nil
	}
	server := httptest.NewServer(New(Config{
		Store: h.database, App: h.service, AgentCapabilities: h.caps,
		ExecuteADB: executor, ExecuteADBTCP: executor, ExecuteADBUSB: executor,
	}).Handler())
	t.Cleanup(server.Close)

	local := "tcp:22130"
	if outcome := callAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", local, "tcp:9000"}, false); !outcome.allowed {
		t.Fatalf("setup add was refused: %+v", outcome)
	}
	<-calls // drain the setup add call

	removeDone := make(chan adbExecOutcome, 1)
	go func() {
		removeDone <- callAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", "--remove", local}, false)
	}()
	select {
	case <-removeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the authorized remove never reached the executor")
	}
	<-calls // drain the remove call

	addDone := make(chan adbExecOutcome, 1)
	go func() {
		addDone <- callAgentADBExec(t, server.URL, tokenB, groupB, []string{"forward", local, "tcp:9000"}, false)
	}()
	select {
	case interleaved := <-calls:
		close(releaseRemove)
		t.Fatalf("the rebinding add ran while the authorized remove was still in flight: %v", interleaved)
	case <-time.After(500 * time.Millisecond):
	}
	close(releaseRemove)
	select {
	case <-calls: // B's add, now serialized after A's remove
	case <-time.After(5 * time.Second):
		t.Fatal("the rebinding add never reached the executor after the remove finished")
	}
	if outcome := <-removeDone; !outcome.allowed {
		t.Fatalf("the owning remove was refused: %+v", outcome)
	}
	if outcome := <-addDone; !outcome.allowed {
		t.Fatalf("the rebinding add was refused: %+v", outcome)
	}

	// B owns the name after the serialized sequence; A's record is gone.
	if outcome := callAgentADBExec(t, server.URL, tokenA, groupA, []string{"forward", "--remove", local}, false); outcome.allowed || outcome.code != "adb_forward_remove_not_allowed" {
		t.Fatalf("stale owner remove outcome=%+v, want adb_forward_remove_not_allowed", outcome)
	}
	if outcome := callAgentADBExec(t, server.URL, tokenB, groupB, []string{"forward", "--remove", local}, false); !outcome.allowed {
		t.Fatalf("rebinding owner remove was refused: %+v", outcome)
	}
}

// TestAgentADBForwardBindingIsolation covers the other half of "records must
// not cross": two bindings on one Task with different serials, and the same
// binding re-pointed at another serial (the configured target is part of the
// scope). A key that drops target or group would let these removes through.
func TestAgentADBForwardBindingIsolation(t *testing.T) {
	h := newCoreMCPHarness(t)
	var executions atomic.Int32
	server := newADBForwardTestServer(t, h, &executions, nil)

	task, token := newADBForwardTestTask(t, h, []domain.HardwareGroup{
		adbForwardTestUSBGroup("usb-a", "SERIAL-A"),
		adbForwardTestUSBGroup("usb-b", "SERIAL-B"),
	}, "batch-forward-binding-isolation")
	groupA, groupB := adbForwardTestUSBGroup("usb-a", "SERIAL-A"), adbForwardTestUSBGroup("usb-b", "SERIAL-B")

	// A local name recorded through one serial is not removable through a
	// sibling binding on the same Task.
	expectAgentADBExec(t, server.URL, token, groupA, []string{"forward", "tcp:22120", "tcp:8080"}, false, &executions, true, "")
	expectAgentADBExec(t, server.URL, token, groupB, []string{"forward", "--remove", "tcp:22120"}, false, &executions, false, "adb_forward_remove_not_allowed")
	expectAgentADBExec(t, server.URL, token, groupA, []string{"forward", "--remove", "tcp:22120"}, false, &executions, true, "")

	// Re-point the same binding at another serial: the old record was made
	// against SERIAL-A and must not authorize a remove through SERIAL-C.
	expectAgentADBExec(t, server.URL, token, groupA, []string{"forward", "tcp:22121", "tcp:8080"}, false, &executions, true, "")
	repointed := adbForwardTestUSBGroup("usb-a", "SERIAL-C")
	if err := h.database.ReplaceHardwareGroups(h.ctx, task.ID, []domain.HardwareGroup{repointed}); err != nil {
		t.Fatal(err)
	}
	expectAgentADBExec(t, server.URL, token, repointed, []string{"forward", "--remove", "tcp:22121"}, false, &executions, false, "adb_forward_remove_not_allowed")
	// The new target owns its own names normally.
	expectAgentADBExec(t, server.URL, token, repointed, []string{"forward", "tcp:22122", "tcp:8080"}, false, &executions, true, "")
	expectAgentADBExec(t, server.URL, token, repointed, []string{"forward", "--remove", "tcp:22122"}, false, &executions, true, "")
}
