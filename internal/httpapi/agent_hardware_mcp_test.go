package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func hardwareMCPURL(h *coreMCPHarness) string {
	return h.server.URL + "/api/v1/agent/hardware/mcp"
}

type hardwareMCPFixture struct {
	h      *coreMCPHarness
	task   domain.Task
	turn   domain.Turn
	token  string
	serial []int64
}

func newHardwareMCPFixture(t *testing.T) *hardwareMCPFixture {
	t.Helper()
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	now := time.Now().UTC()
	if err := h.database.ReplaceHardwareGroups(h.ctx, task.ID, []domain.HardwareGroup{
		{
			TaskID: task.ID, ID: "board", Position: 0, Description: "Main board",
			Mode:     domain.HardwareModeSerial,
			Serial:   domain.HardwareSerialConfig{Device: "/dev/ttyUSB7", Baudrate: 115200},
			Username: "root", CredentialRef: "vault:hw-secret", PasswordConfigured: true,
			Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now,
		},
		{
			TaskID: task.ID, ID: "router", Position: 1, Description: "Lab router",
			Mode: domain.HardwareModeNetwork,
			Network: domain.HardwareNetworkConfig{
				Host: "10.20.30.40", Port: 22, Protocol: domain.HardwareProtocolSSH,
				SSHAuth: domain.HardwareSSHAuthPassword,
			},
			Username: "admin", CredentialRef: "vault:net-secret", PasswordConfigured: true,
			Access: domain.HardwareAccessReadOnly, CreatedAt: now, UpdatedAt: now,
		},
	}); err != nil {
		t.Fatal(err)
	}
	turn := createCoreTurn(t, h.database, task, "main", "batch-hardware-mcp")
	token := h.issue(task.ID, "main", turn.ID)
	fixture := &hardwareMCPFixture{h: h, task: task, turn: turn, token: token}
	for _, data := range []string{"boot ok", "ready", "tick 1", "tick 2", "TERMINAL-OUTPUT-MARKER-51"} {
		sequence, err := h.database.AppendHardwareIO(h.ctx, domain.HardwareIOEvent{
			ID: domain.NewID("hardware_io"), TaskID: task.ID, HardwareID: "board",
			Transport: domain.HardwareTransportSerial, Direction: "rx", Data: data,
			Encoding: "text", Source: "terminal", CreatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		fixture.serial = append(fixture.serial, sequence)
	}
	return fixture
}

func (f *hardwareMCPFixture) call(t *testing.T, token, name string, arguments any) coreMCPToolCall {
	t.Helper()
	return coreMCPCallTool(t, hardwareMCPURL(f.h), token, name, arguments)
}

func waitForHardwareMCPAudit(t *testing.T, path, tool string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var data string
		err := db.QueryRowContext(context.Background(),
			`SELECT data_json FROM audit_events WHERE action='agent.hardware_mcp.call' AND data_json LIKE ? ORDER BY sequence DESC LIMIT 1`,
			`%"tool":"`+tool+`"%`,
		).Scan(&data)
		if err == nil && data != "" {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("no hardware MCP audit row: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAgentHardwareMCPRequiresCapabilityAndDomainGate(t *testing.T) {
	f := newHardwareMCPFixture(t)
	url := hardwareMCPURL(f.h)

	response := agentRequest(t, url, http.MethodPost, "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous initialize status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = agentRequest(t, url, http.MethodPost, "not-a-real-token", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid capability status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	// A Task with no hardware binding is refused by the domain gate even with
	// a valid capability for a running Turn.
	unbound := createHardwareAPITask(t, f.h.database)
	unboundTurn := createCoreTurn(t, f.h.database, unbound, "main", "batch-hardware-mcp-unbound")
	unboundToken := f.h.issue(unbound.ID, "main", unboundTurn.ID)
	response = agentRequest(t, url, http.MethodPost, unboundToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("un-injected initialize status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = agentRequest(t, url, http.MethodPost, f.token, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("initialize status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	var initialized struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	decodeResponse(t, response, &initialized)
	if initialized.Result.ServerInfo.Name != "aha2-hardware" {
		t.Fatalf("serverInfo=%#v", initialized.Result.ServerInfo)
	}

	forwarded, err := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		t.Fatal(err)
	}
	forwarded.Header.Set("Authorization", "Bearer "+f.token)
	forwarded.Header.Set("Forwarded", "for=10.0.0.1")
	forwardedResponse, err := http.DefaultClient.Do(forwarded)
	if err != nil {
		t.Fatal(err)
	}
	if forwardedResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("forwarded status=%d body=%s", forwardedResponse.StatusCode, readBody(t, forwardedResponse))
	}
	forwardedResponse.Body.Close()

	oversized := agentRequestRaw(t, url, http.MethodPost, f.token,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","padding":"`+strings.Repeat("a", 1<<20)+`"}`))
	if oversized.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d body=%s", oversized.StatusCode, readBody(t, oversized))
	}
	oversized.Body.Close()

	nullBody := agentRequestRaw(t, url, http.MethodPost, f.token, []byte(`[]`))
	if nullBody.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-object body status=%d body=%s", nullBody.StatusCode, readBody(t, nullBody))
	}
	nullBody.Body.Close()
}

func TestAgentHardwareMCPToolsListSchemas(t *testing.T) {
	f := newHardwareMCPFixture(t)
	status, names, schemas := coreMCPToolsList(t, hardwareMCPURL(f.h), f.token)
	if status != http.StatusOK {
		t.Fatalf("tools/list status=%d", status)
	}
	want := []string{"hardware_list", "hardware_terminal_read"}
	sort.Strings(names)
	if len(names) != len(want) {
		t.Fatalf("tools=%v", names)
	}
	for index, name := range want {
		if names[index] != name {
			t.Fatalf("tools=%v", names)
		}
	}
	for _, name := range want {
		if schemas[name]["additionalProperties"] != false {
			t.Fatalf("%s schema=%#v", name, schemas[name])
		}
	}
	terminal := schemas["hardware_terminal_read"]
	properties := terminal["properties"].(map[string]any)
	if properties["transport"].(map[string]any)["enum"].([]any)[0] != "serial" {
		t.Fatalf("terminal schema=%#v", terminal)
	}
	if maximum, _ := properties["limit"].(map[string]any)["maximum"].(float64); maximum != 1000 {
		t.Fatalf("terminal limit=%#v", properties["limit"])
	}
	for _, forbidden := range []string{"hardware_connect", "hardware_send", "hardware_login", "hardware_adb_exec", "hardware_port_forward"} {
		if _, ok := schemas[forbidden]; ok {
			t.Fatalf("%s must not be exposed", forbidden)
		}
	}
}

func TestAgentHardwareMCPListOnlyExposesBoundNonSensitiveFields(t *testing.T) {
	f := newHardwareMCPFixture(t)
	call := f.call(t, f.token, "hardware_list", map[string]any{})
	if call.Status != http.StatusOK || call.IsError {
		t.Fatalf("hardware_list=%#v", call)
	}
	for _, secret := range []string{"/dev/ttyUSB7", "10.20.30.40", "vault:hw-secret", "vault:net-secret", `"username"`, `"credential_ref"`, `"password_configured"`, `"serial_device"`, `"network_host"`} {
		if strings.Contains(call.Text, secret) {
			t.Fatalf("hardware_list leaked %q: %s", secret, call.Text)
		}
	}
	var payload struct {
		Hardware []struct {
			HardwareID string `json:"hardware_id"`
			Mode       string `json:"mode"`
			Access     string `json:"access"`
			ReadOnly   bool   `json:"read_only"`
			Transports []struct {
				Transport string `json:"transport"`
				Status    string `json:"status"`
			} `json:"transports"`
		} `json:"hardware"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(call.Text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Total != 2 || len(payload.Hardware) != 2 {
		t.Fatalf("hardware=%s", call.Text)
	}
	byID := map[string]int{}
	for index, item := range payload.Hardware {
		byID[item.HardwareID] = index
	}
	board := payload.Hardware[byID["board"]]
	if board.Mode != domain.HardwareModeSerial || board.Access != domain.HardwareAccessReadWrite || board.ReadOnly ||
		len(board.Transports) != 1 || board.Transports[0].Transport != domain.HardwareTransportSerial {
		t.Fatalf("board=%#v", board)
	}
	router := payload.Hardware[byID["router"]]
	if router.Mode != domain.HardwareModeNetwork || router.Access != domain.HardwareAccessReadOnly || !router.ReadOnly ||
		len(router.Transports) != 1 || router.Transports[0].Transport != domain.HardwareTransportNetwork {
		t.Fatalf("router=%#v", router)
	}

	// A read-only resource stays read-only through the MCP facade: the list
	// exposes no connect, send or config surface at all.
	for _, forbidden := range []string{"connect", "disconnect", "send", "login", "forward", "firmware"} {
		if strings.Contains(call.Text, `"`+forbidden+`"`) {
			t.Fatalf("hardware_list exposed %q: %s", forbidden, call.Text)
		}
	}

	audit := waitForHardwareMCPAudit(t, f.h.dbPath, "hardware_list")
	for _, needle := range []string{"vault:hw-secret", "/dev/ttyUSB7", "10.20.30.40", "admin"} {
		if strings.Contains(audit, needle) {
			t.Fatalf("hardware_list audit leaked %q: %s", needle, audit)
		}
	}
}

func TestAgentHardwareMCPTerminalReadPagingAndCaps(t *testing.T) {
	f := newHardwareMCPFixture(t)
	if len(f.serial) != 5 {
		t.Fatalf("serial sequences=%v", f.serial)
	}

	// Default limit reads the most recent page without truncation.
	all := f.call(t, f.token, "hardware_terminal_read", map[string]any{"hardware_id": "board"})
	if all.IsError {
		t.Fatalf("default read=%#v", all)
	}
	var allPayload struct {
		HardwareID string `json:"hardware_id"`
		Transport  string `json:"transport"`
		Items      []struct {
			Sequence int64  `json:"sequence"`
			Data     string `json:"data"`
		} `json:"items"`
		Truncated bool  `json:"truncated"`
		NextAfter int64 `json:"next_after"`
	}
	if err := json.Unmarshal([]byte(all.Text), &allPayload); err != nil {
		t.Fatal(err)
	}
	if allPayload.HardwareID != "board" || allPayload.Transport != domain.HardwareTransportSerial ||
		len(allPayload.Items) != 5 || allPayload.Truncated {
		t.Fatalf("default read=%s", all.Text)
	}
	if allPayload.Items[0].Sequence != f.serial[0] || allPayload.NextAfter != f.serial[4] {
		t.Fatalf("default read=%s", all.Text)
	}

	// after + limit returns the following window and flags that more remains.
	window := f.call(t, f.token, "hardware_terminal_read", map[string]any{
		"hardware_id": "board", "after": f.serial[1], "limit": 2,
	})
	if window.IsError {
		t.Fatalf("window read=%#v", window)
	}
	var windowPayload struct {
		Items     []struct{ Sequence int64 } `json:"items"`
		Truncated bool                       `json:"truncated"`
		NextAfter int64                      `json:"next_after"`
	}
	if err := json.Unmarshal([]byte(window.Text), &windowPayload); err != nil {
		t.Fatal(err)
	}
	if len(windowPayload.Items) != 2 || windowPayload.Items[0].Sequence != f.serial[2] ||
		windowPayload.Items[1].Sequence != f.serial[3] {
		t.Fatalf("window=%s", window.Text)
	}
	if !windowPayload.Truncated || windowPayload.NextAfter != f.serial[3] {
		t.Fatalf("window=%s", window.Text)
	}

	// limit=1000 is the cap; 1001, 0 with a negative cursor, an unknown
	// transport and a non--bound ID all fail before touching the store.
	for label, arguments := range map[string]map[string]any{
		"limit over cap": {"hardware_id": "board", "limit": 1001},
		"limit negative": {"hardware_id": "board", "limit": -1},
		"after negative": {"hardware_id": "board", "after": -5},
		"bad transport":  {"hardware_id": "board", "transport": "usb"},
		"missing id":     {},
		"traversal id":   {"hardware_id": "../../etc/passwd"},
		"uppercase id":   {"hardware_id": "Board"},
		"unknown id":     {"hardware_id": "ghost"},
		"extra field":    {"hardware_id": "board", "command": "reboot"},
	} {
		call := f.call(t, f.token, "hardware_terminal_read", arguments)
		if !call.IsError {
			t.Fatalf("%s=%#v", label, call)
		}
		if label == "unknown id" {
			if !strings.HasPrefix(call.Text, "hardware_group_not_found:") {
				t.Fatalf("%s=%#v", label, call)
			}
			continue
		}
		if !strings.HasPrefix(call.Text, "invalid_arguments:") {
			t.Fatalf("%s=%#v", label, call)
		}
	}

	// transport must match the configured mode.
	badTransport := f.call(t, f.token, "hardware_terminal_read", map[string]any{
		"hardware_id": "board", "transport": domain.HardwareTransportNetwork,
	})
	if !badTransport.IsError || !strings.HasPrefix(badTransport.Text, "hardware_transport_invalid:") {
		t.Fatalf("transport mismatch=%#v", badTransport)
	}

	// Another Task's hardware is indistinguishable from unbound hardware.
	alien := createHardwareAPITask(t, f.h.database)
	if err := f.h.database.ReplaceHardwareGroups(f.h.ctx, alien.ID, []domain.HardwareGroup{{
		TaskID: alien.ID, ID: "alien", Mode: domain.HardwareModeSerial,
		Serial: domain.HardwareSerialConfig{Device: "/dev/ttyALIEN"}, Access: domain.HardwareAccessReadOnly,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	crossTask := f.call(t, f.token, "hardware_terminal_read", map[string]any{"hardware_id": "alien"})
	if !crossTask.IsError || !strings.HasPrefix(crossTask.Text, "hardware_group_not_found:") {
		t.Fatalf("cross task=%#v", crossTask)
	}
	if strings.Contains(crossTask.Text, "/dev/ttyALIEN") || strings.Contains(crossTask.Text, alien.ID) {
		t.Fatalf("cross task error leaked the other task: %s", crossTask.Text)
	}
}

func TestAgentHardwareMCPTerminalReadEnforcesResponseBudget(t *testing.T) {
	f := newHardwareMCPFixture(t)
	const marker = "OVERSIZED-EVENT-9d1"
	oversizedData := marker + strings.Repeat("A", 80<<10)
	sequence, err := f.h.database.AppendHardwareIO(f.h.ctx, domain.HardwareIOEvent{
		ID: domain.NewID("hardware_io"), TaskID: f.task.ID, HardwareID: "router",
		Transport: domain.HardwareTransportNetwork, Direction: "rx", Data: oversizedData,
		Encoding: "text", Source: "terminal", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	call := f.call(t, f.token, "hardware_terminal_read", map[string]any{
		"hardware_id": "router", "transport": domain.HardwareTransportNetwork, "limit": 1,
	})
	if call.IsError {
		t.Fatalf("oversized read=%#v", call)
	}
	if len(call.Text) > agentHardwareMCPMaxResponseBytes {
		t.Fatalf("response %d bytes exceeds the 64 KiB budget", len(call.Text))
	}
	var payload struct {
		Items []struct {
			Sequence int64  `json:"sequence"`
			Data     string `json:"data"`
		} `json:"items"`
		Truncated bool  `json:"truncated"`
		NextAfter int64 `json:"next_after"`
	}
	if err := json.Unmarshal([]byte(call.Text), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || !payload.Truncated || payload.NextAfter != sequence {
		t.Fatalf("oversized read=%#v", payload)
	}
	if len(payload.Items[0].Data) >= len(oversizedData) {
		t.Fatalf("oversized event was not trimmed: %d bytes", len(payload.Items[0].Data))
	}
	if !strings.HasPrefix(payload.Items[0].Data, marker) {
		t.Fatalf("trimmed event lost its head: %q", payload.Items[0].Data[:32])
	}
	for _, needle := range []string{marker, "OVERSIZED-EVENT"} {
		assertAuditActionLacks(t, f.h.dbPath, "agent.hardware_mcp.call", needle)
	}
}

func TestAgentHardwareMCPRejectsChannelBoundTurns(t *testing.T) {
	f := newHardwareMCPFixture(t)
	url := hardwareMCPURL(f.h)
	retireCoreTurn(t, f.h.database, f.turn)
	assertStatus := func(label, batchID string, want int) {
		t.Helper()
		turn := createCoreTurn(t, f.h.database, f.task, "main", batchID)
		token := f.h.issue(f.task.ID, "main", turn.ID)
		if batchID != "" {
			kind := "plain"
			if strings.Contains(batchID, "channel") {
				kind = "channel:task_route"
			}
			if strings.Contains(batchID, "broken") {
				kind = "raw:not-json"
			}
			seedKnowledgeMCPInbox(t, f.h.dbPath, f.task, turn, kind)
		}
		response := agentRequest(t, url, http.MethodPost, token, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize",
		})
		if response.StatusCode != want {
			t.Fatalf("%s status=%d body=%s", label, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
		retireCoreTurn(t, f.h.database, turn)
	}
	assertStatus("channel batch", "batch-hardware-mcp-channel", http.StatusForbidden)
	assertStatus("plain batch", "batch-hardware-mcp-plain", http.StatusOK)
	assertStatus("malformed batch", "batch-hardware-mcp-broken", http.StatusForbidden)
}
