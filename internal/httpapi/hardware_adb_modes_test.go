package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"github.com/ChinaKai/AHA2/internal/store"
)

type adbExecCall struct{ executor, target string }

type adbExecRecorder struct {
	mu    sync.Mutex
	calls []adbExecCall
}

func (recorder *adbExecRecorder) executor(name string) func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error) {
	return func(_ context.Context, target string, _ []string, _ []byte, _ time.Duration) (hardware.ADBExecResult, error) {
		recorder.mu.Lock()
		recorder.calls = append(recorder.calls, adbExecCall{name, target})
		recorder.mu.Unlock()
		return hardware.ADBExecResult{Endpoint: target, Stdout: []byte("ok")}, nil
	}
}

func (recorder *adbExecRecorder) take() []adbExecCall {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	calls := recorder.calls
	recorder.calls = nil
	return calls
}

func TestAgentADBExecRoutesEachConnectionFormToTheBoundTarget(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	other := createHardwareAPITask(t, database)
	now := time.Now().UTC()
	adb := func(id string, network domain.HardwareNetworkConfig, access string) domain.HardwareGroup {
		network.Protocol, network.SSHAuth = domain.HardwareProtocolADB, domain.HardwareSSHAuthAuto
		return domain.HardwareGroup{ID: id, Mode: domain.HardwareModeNetwork, Network: network, Access: access, CreatedAt: now, UpdatedAt: now}
	}
	rw, ro := domain.HardwareAccessReadWrite, domain.HardwareAccessReadOnly
	if err := database.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{
		adb("legacy", domain.HardwareNetworkConfig{Host: "192.0.2.10", Port: 41234}, rw),
		adb("tv", domain.HardwareNetworkConfig{Host: "192.0.2.11", Port: 5555, ADBConnection: "tcp"}, rw),
		adb("tv-custom", domain.HardwareNetworkConfig{Host: "192.0.2.12", Port: 5601, ADBConnection: "tcp"}, rw),
		adb("board", domain.HardwareNetworkConfig{ADBConnection: "usb", ADBSerial: "SERIAL-A"}, rw),
		adb("board-ro", domain.HardwareNetworkConfig{ADBConnection: "usb", ADBSerial: "SERIAL-RO"}, ro),
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.ReplaceHardwareGroups(ctx, other.ID, []domain.HardwareGroup{
		adb("other-board", domain.HardwareNetworkConfig{ADBConnection: "usb", ADBSerial: "SERIAL-B"}, rw),
	}); err != nil {
		t.Fatal(err)
	}
	recorder := &adbExecRecorder{}
	capabilities := agentapi.NewCapabilities()
	server := httptest.NewServer(New(Config{
		Store: database, AgentCapabilities: capabilities,
		ExecuteADB:    recorder.executor("wireless"),
		ExecuteADBTCP: recorder.executor("tcp"),
		ExecuteADBUSB: recorder.executor("usb"),
		PairADB: func(context.Context, string, string) (string, error) {
			t.Fatal("Agent ADB exec must never pair")
			return "", nil
		},
	}).Handler())
	defer server.Close()
	token, err := capabilities.Issue(task.ID, "main", "turn-adb-modes", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer capabilities.Revoke(token)
	exec := func(hardwareID string, args ...string) int {
		response := agentRequest(t, server.URL+"/api/v1/agent/hardware/"+hardwareID+"/adb/exec", http.MethodPost, token, map[string]any{"args": args})
		response.Body.Close()
		return response.StatusCode
	}
	for _, testCase := range []struct {
		id   string
		want adbExecCall
	}{
		{"legacy", adbExecCall{"wireless", "192.0.2.10:41234"}},
		{"tv", adbExecCall{"tcp", "192.0.2.11:5555"}},
		{"tv-custom", adbExecCall{"tcp", "192.0.2.12:5601"}},
		{"board", adbExecCall{"usb", "SERIAL-A"}},
	} {
		if status := exec(testCase.id, "shell", "id"); status != http.StatusOK {
			t.Fatalf("%s exec status=%d", testCase.id, status)
		}
		if calls := recorder.take(); len(calls) != 1 || calls[0] != testCase.want {
			t.Fatalf("%s routed to %#v, want %#v", testCase.id, calls, testCase.want)
		}
	}
	// Read-only USB, another Task's USB serial, host-level commands and
	// option injection never reach an executor.
	for _, testCase := range []struct {
		id     string
		args   []string
		status int
	}{
		{"board-ro", []string{"shell", "id"}, http.StatusForbidden},
		{"other-board", []string{"shell", "id"}, http.StatusNotFound},
		{"board", []string{"devices"}, http.StatusBadRequest},
		{"board", []string{"connect", "192.0.2.99:5555"}, http.StatusBadRequest},
		{"board", []string{"-s", "SERIAL-B", "shell", "id"}, http.StatusBadRequest},
		{"tv", []string{"pair", "192.0.2.11:37000"}, http.StatusBadRequest},
		{"tv", []string{"kill-server"}, http.StatusBadRequest},
		{"board", []string{"tcpip", "5555"}, http.StatusBadRequest},
		{"legacy", []string{"TCPIP", "5555"}, http.StatusBadRequest},
		{"tv", []string{"tcpip", "5601"}, http.StatusBadRequest},
	} {
		if status := exec(testCase.id, testCase.args...); status != testCase.status {
			t.Fatalf("%s %v status=%d want %d", testCase.id, testCase.args, status, testCase.status)
		}
		if calls := recorder.take(); len(calls) != 0 {
			t.Fatalf("%s %v reached an executor: %#v", testCase.id, testCase.args, calls)
		}
	}
}

func TestOwnerADBResourceFormsPairingAndUSBEnumeration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	authService := auth.NewService(database, "setup-test", time.Hour)
	var pairMu sync.Mutex
	pairedEndpoints := []string{}
	listErr := error(nil)
	capabilities := agentapi.NewCapabilities()
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}), AgentCapabilities: capabilities,
		PairADB: func(_ context.Context, endpoint, _ string) (string, error) {
			pairMu.Lock()
			pairedEndpoints = append(pairedEndpoints, endpoint)
			pairMu.Unlock()
			return "Successfully paired", nil
		},
		ListADBUSBDevices: func(context.Context) (hardware.ADBUSBDeviceList, error) {
			if listErr != nil {
				return hardware.ADBUSBDeviceList{}, listErr
			}
			return hardware.ADBUSBDeviceList{Devices: []hardware.ADBUSBDevice{
				{Serial: "SERIAL-A", State: "device", Model: "Pixel", Selectable: true},
				{Serial: "SERIAL-B", State: "unauthorized", Selectable: true},
			}, OtherCount: 1}, nil
		},
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	create := func(network map[string]any) (int, domain.HardwareResource, string) {
		response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware-resources", map[string]any{
			"description": "device", "mode": "network", "access": "read_write", "network": network,
		}, csrf)
		var payload struct {
			Resource domain.HardwareResource `json:"resource"`
			Message  string                  `json:"message"`
		}
		decodeResponse(t, response, &payload)
		return response.StatusCode, payload.Resource, payload.Message
	}
	status, usb, _ := create(map[string]any{"host": "192.0.2.7", "port": 5555, "protocol": "adb", "adb_connection": "usb", "adb_serial": "SERIAL-A"})
	if status != http.StatusCreated || !usb.Network.ADBUSB() || usb.Network.Host != "" || usb.Network.Port != 0 || usb.Network.ADBSerial != "SERIAL-A" {
		t.Fatalf("usb create status=%d resource=%#v", status, usb.Network)
	}
	status, tcp, _ := create(map[string]any{"host": "192.0.2.8", "protocol": "adb", "adb_connection": "tcp"})
	if status != http.StatusCreated || !tcp.Network.ADBTCP() || tcp.Network.Port != 5555 || tcp.Network.Host != "192.0.2.8" {
		t.Fatalf("tcp create status=%d resource=%#v", status, tcp.Network)
	}
	// An older client that never sends adb_connection keeps wireless meaning.
	status, wireless, _ := create(map[string]any{"host": "192.0.2.9", "port": 41235, "protocol": "adb"})
	if status != http.StatusCreated || !wireless.Network.ADBWireless() || wireless.Network.ADBConnection != "wireless" || wireless.Network.Port != 41235 {
		t.Fatalf("legacy-client create status=%d resource=%#v", status, wireless.Network)
	}
	for _, serial := range []string{"192.0.2.4:5555", "emulator-5554", "-s"} {
		if status, _, message := create(map[string]any{"protocol": "adb", "adb_connection": "usb", "adb_serial": serial}); status != http.StatusBadRequest || !strings.Contains(message, "序列号") {
			t.Fatalf("invalid serial %q status=%d message=%q", serial, status, message)
		}
	}
	if status, _, _ := create(map[string]any{"protocol": "adb", "adb_connection": "usb"}); status != http.StatusBadRequest {
		t.Fatalf("usb without serial status=%d", status)
	}

	pair := func(id string) int {
		response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware-resources/"+id+"/adb/pair",
			map[string]any{"pairing_port": 37124, "pairing_code": "123456"}, csrf)
		response.Body.Close()
		return response.StatusCode
	}
	if pair(usb.ID) != http.StatusBadRequest || pair(tcp.ID) != http.StatusBadRequest {
		t.Fatal("USB direct and network direct resources must refuse pairing")
	}
	if len(pairedEndpoints) != 0 {
		t.Fatalf("pairing ran for a non-wireless resource: %q", pairedEndpoints)
	}
	if pair(wireless.ID) != http.StatusOK || len(pairedEndpoints) != 1 || pairedEndpoints[0] != "192.0.2.9:37124" {
		t.Fatalf("wireless pairing regressed: %q", pairedEndpoints)
	}
	stored, _ := database.HardwareResource(ctx, wireless.ID)
	if stored.ADBPairedAt == "" {
		t.Fatal("wireless pairing state was not saved")
	}
	// Same-host wireless edit keeps the pairing; switching to tcp drops it.
	update := func(id string, network map[string]any) domain.HardwareResource {
		response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/hardware-resources/"+id, map[string]any{
			"description": "device", "mode": "network", "access": "read_write", "network": network,
		}, csrf)
		var payload struct {
			Resource domain.HardwareResource `json:"resource"`
		}
		decodeResponse(t, response, &payload)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("update %s status=%d", id, response.StatusCode)
		}
		return payload.Resource
	}
	if kept := update(wireless.ID, map[string]any{"host": "192.0.2.9", "port": 41299, "protocol": "adb", "adb_connection": "wireless"}); kept.ADBPairedAt == "" {
		t.Fatal("same-host wireless edit dropped the pairing")
	}
	if switched := update(wireless.ID, map[string]any{"host": "192.0.2.9", "port": 5555, "protocol": "adb", "adb_connection": "tcp"}); switched.ADBPairedAt != "" || !switched.Network.ADBTCP() {
		t.Fatalf("switch to network direct kept pairing: %#v", switched)
	}
	if switched := update(tcp.ID, map[string]any{"host": "192.0.2.8", "port": 5555, "protocol": "adb", "adb_connection": "usb", "adb_serial": "SERIAL-B"}); !switched.Network.ADBUSB() || switched.Network.Host != "" || switched.Network.ADBSerial != "SERIAL-B" {
		t.Fatalf("switch tcp→usb: %#v", switched.Network)
	}

	// USB enumeration is Owner-session only and needs CSRF.
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware/adb/usb-devices", map[string]any{}, csrf)
	var listed struct {
		Devices    []hardware.ADBUSBDevice `json:"devices"`
		OtherCount int                     `json:"other_count"`
	}
	decodeResponse(t, response, &listed)
	if response.StatusCode != http.StatusOK || len(listed.Devices) != 2 || listed.Devices[0].Serial != "SERIAL-A" || listed.OtherCount != 1 {
		t.Fatalf("usb devices status=%d payload=%#v", response.StatusCode, listed)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware/adb/usb-devices", map[string]any{}, "")
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("usb devices without CSRF status=%d", response.StatusCode)
	}
	task := createHardwareAPITask(t, database)
	token, err := capabilities.Issue(task.ID, "main", "turn-usb-enum", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer capabilities.Revoke(token)
	response = agentRequest(t, server.URL+"/api/v1/hardware/adb/usb-devices", http.MethodPost, token, map[string]any{})
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Agent token reached USB enumeration: status=%d", response.StatusCode)
	}
	listErr = errors.New("ADB executable not found; install Android Platform Tools or set AHA2_ADB_PATH")
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware/adb/usb-devices", map[string]any{}, csrf)
	var failed map[string]any
	decodeResponse(t, response, &failed)
	if response.StatusCode != http.StatusConflict || !strings.Contains(failed["message"].(string), "找不到 ADB") {
		t.Fatalf("enumeration failure status=%d payload=%#v", response.StatusCode, failed)
	}
}
