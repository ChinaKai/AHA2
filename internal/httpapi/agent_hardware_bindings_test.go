package httpapi

import (
	"github.com/ChinaKai/AHA2/internal/hardware"
	"net/http"
	"net/http/cookiejar"
	"reflect"
	"testing"
)

func TestWebBindingReplacementPreservesConnectionsAndCleansOrphans(t *testing.T) {
	f := newHardwareAuthorizationFixture(t, false)
	if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"}); result.IsError || result.Status != 200 {
		t.Fatal("fixture connect failed")
	}
	_ = f.connection(t)
	group, err := f.h.database.HardwareGroup(f.h.ctx, f.task.ID, "board")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Connect(f.h.ctx, hardware.ConnectRequest{TaskID: f.task.ID, HardwareID: "orphan", Transport: "network", Group: group, Password: f.secret, ReadOnly: true}); err != nil {
		t.Fatal("orphan fixture failed")
	}
	before, err := f.h.database.TaskHardwareBindings(f.h.ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, f.server.URL)
	url := f.server.URL + "/api/v1/tasks/" + f.task.ID + "/hardware-bindings"
	response := requestJSON(t, client, http.MethodPut, url, map[string]any{"bindings": []map[string]any{{"id": "board", "resource_id": f.board.ID}, {"id": "device", "resource_id": f.adb.ID}}}, csrf)
	if response.StatusCode != 200 {
		t.Fatalf("Web unchanged binding status=%d", response.StatusCode)
	}
	response.Body.Close()
	after, _ := f.h.database.TaskHardwareBindings(f.h.ctx, f.task.ID)
	if !reflect.DeepEqual(before, after) || !f.manager.Status(f.task.ID, "board", "network", false).Connected || f.manager.Status(f.task.ID, "orphan", "network", false).Connected || f.accepts.Load() != 1 {
		t.Fatal("unchanged binding dropped a connection or retained an orphan")
	}
	response = requestJSON(t, client, http.MethodPut, url, map[string]any{"bindings": []map[string]any{{"id": "newalias", "resource_id": f.board.ID}}}, csrf)
	if response.StatusCode != 200 {
		t.Fatal("Web rebind failed")
	}
	response.Body.Close()
	if f.manager.Status(f.task.ID, "board", "network", false).Connected {
		t.Fatal("removed binding retained connection")
	}
	if f.raw(t, http.MethodPost, "/board/send", map[string]any{"data": "OLD-ID"}) != 404 {
		t.Fatal("removed binding remained addressable")
	}
	if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "newalias"}); result.IsError || result.Status != 200 {
		t.Fatal("new binding not usable")
	}
	_ = f.connection(t)
	response = requestJSON(t, client, http.MethodPut, url, map[string]any{"bindings": []map[string]any{{"id": "newalias", "resource_id": "missing"}}}, csrf)
	if response.StatusCode != 400 {
		t.Fatal("invalid Web binding accepted")
	}
	response.Body.Close()
	if !f.manager.Status(f.task.ID, "newalias", "network", false).Connected {
		t.Fatal("invalid Web update disconnected current device")
	}
}
