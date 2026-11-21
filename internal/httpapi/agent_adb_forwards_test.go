package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
