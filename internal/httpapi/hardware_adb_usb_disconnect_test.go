//go:build !windows

package httpapi

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"github.com/ChinaKai/AHA2/internal/store"
)

// End to end through the real hardware.Manager and a fake adb: a Task's open
// USB terminal is disconnected when the Owner edits the USB resource, and
// again when the resource is deleted. A USB resource has no host, so this
// depends on Supports(network) recognising the serial.
func TestEditingOrDeletingUSBResourceDisconnectsOpenTerminal(t *testing.T) {
	adb := filepath.Join(t.TempDir(), "adb")
	script := `#!/bin/sh
if [ "$1" = "-s" ] && [ "$3" = "get-state" ]; then echo device; exit 0; fi
if [ "$1" = "-s" ] && [ "$3" = "get-serialno" ]; then echo "$2"; exit 0; fi
if [ "$1" = "-s" ] && [ "$3" = "shell" ]; then while IFS= read -r line; do echo "$line"; done; exit 0; fi
echo "unexpected arguments: $*" >&2
exit 2
`
	if err := os.WriteFile(adb, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AHA2_ADB_PATH", adb)

	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	manager := hardware.NewManager(database)
	defer manager.Close()
	server := httptest.NewServer(New(Config{
		Store: database, Auth: auth.NewService(database, "setup-test", time.Hour),
		App: app.NewService(database, nil, app.StubExecutor{}), Secrets: &fakeSecretStore{}, Hardware: manager,
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	usbNetwork := func(serial string) map[string]any {
		return map[string]any{"protocol": "adb", "adb_connection": "usb", "adb_serial": serial}
	}
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware-resources", map[string]any{
		"description": "USB board", "mode": "network", "access": "read_write", "network": usbNetwork("SER1"),
	}, csrf)
	var created struct {
		Resource domain.HardwareResource `json:"resource"`
	}
	decodeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create USB resource status=%d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+task.ID+"/hardware-bindings",
		map[string]any{"bindings": []map[string]any{{"resource_id": created.Resource.ID}}}, csrf)
	var bound struct {
		Groups []domain.HardwareGroup `json:"groups"`
	}
	decodeResponse(t, response, &bound)
	if response.StatusCode != http.StatusOK || len(bound.Groups) != 1 {
		t.Fatalf("bind status=%d groups=%#v", response.StatusCode, bound.Groups)
	}
	hardwareID := bound.Groups[0].ID
	connected := func() bool {
		return manager.Status(task.ID, hardwareID, domain.HardwareTransportNetwork, false).Connected
	}
	connect := func(wantEndpoint string) {
		t.Helper()
		response := requestJSON(t, client, http.MethodPost,
			server.URL+"/api/v1/tasks/"+task.ID+"/hardware/"+hardwareID+"/connect?transport=network", nil, csrf)
		var payload struct {
			Status domain.HardwareTerminalStatus `json:"status"`
		}
		decodeResponse(t, response, &payload)
		if response.StatusCode != http.StatusOK || !payload.Status.Connected || payload.Status.Endpoint != wantEndpoint || !connected() {
			t.Fatalf("connect status=%d payload=%#v", response.StatusCode, payload)
		}
	}
	waitDisconnected := func(step string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for connected() {
			if time.Now().After(deadline) {
				t.Fatalf("%s left the USB terminal connected", step)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	connect("USB SER1")
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/hardware-resources/"+created.Resource.ID, map[string]any{
		"description": "USB board", "mode": "network", "access": "read_write", "network": usbNetwork("SER2"),
	}, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update status=%d", response.StatusCode)
	}
	waitDisconnected("editing the USB resource")

	connect("USB SER2")
	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/hardware-resources/"+created.Resource.ID, nil, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete status=%d", response.StatusCode)
	}
	waitDisconnected("deleting the USB resource")
}
