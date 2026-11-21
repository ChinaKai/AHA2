# task-726 review packet diff-4-go-tests：Go 测试

`git diff 43624a4 336d47b -- internal/hardware/adb_modes_test.go internal/hardwareconfig/config_test.go internal/store/hardware_adb_test.go internal/httpapi/hardware_adb_modes_test.go internal/httpapi/takeover_adb_test.go internal/app/pet_hardware_adb_modes_test.go internal/app/pet_takeover_adb_test.go internal/prompt/hardware_adb_context_test.go` 原样输出。

~~~~diff
diff --git a/internal/app/pet_hardware_adb_modes_test.go b/internal/app/pet_hardware_adb_modes_test.go
new file mode 100644
index 0000000..12b111f
--- /dev/null
+++ b/internal/app/pet_hardware_adb_modes_test.go
@@ -0,0 +1,82 @@
+package app
+
+import (
+	"encoding/json"
+	"strings"
+	"testing"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+func TestPetHardwareResourcesADBConnectionForms(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	enablePetHardwareTestSettings(t, fixture, true)
+
+	usb := decodePetHardwareView(t, petHardwareToolCall(t, fixture, "aha_create_hardware_resource",
+		`{"id":"hw-usb","description":"USB 板","mode":"network","network":{"protocol":"adb","adb_connection":"usb","adb_serial":"R58M12345A","host":"192.0.2.3","port":5555}}`))
+	if !usb.Network.ADBUSB() || usb.Network.ADBSerial != "R58M12345A" || usb.Network.Host != "" || usb.Network.Port != 0 {
+		t.Fatalf("pet USB create: %#v", usb.Network)
+	}
+	tcp := decodePetHardwareView(t, petHardwareToolCall(t, fixture, "aha_create_hardware_resource",
+		`{"id":"hw-tcp","description":"电视","mode":"network","network":{"protocol":"adb","adb_connection":"tcp","host":"192.0.2.4"}}`))
+	if !tcp.Network.ADBTCP() || tcp.Network.Port != 5555 || tcp.Network.Host != "192.0.2.4" {
+		t.Fatalf("pet TCP create: %#v", tcp.Network)
+	}
+	legacy := decodePetHardwareView(t, petHardwareToolCall(t, fixture, "aha_create_hardware_resource",
+		`{"id":"hw-wireless","description":"手机","mode":"network","network":{"protocol":"adb","host":"192.0.2.5","port":41235}}`))
+	if !legacy.Network.ADBWireless() || legacy.Network.ADBConnection != domain.HardwareADBConnectionWireless {
+		t.Fatalf("pet create without adb_connection must be wireless: %#v", legacy.Network)
+	}
+
+	for _, args := range []string{
+		`{"id":"hw-bad1","mode":"network","network":{"protocol":"adb","adb_connection":"usb"}}`,
+		`{"id":"hw-bad2","mode":"network","network":{"protocol":"adb","adb_connection":"usb","adb_serial":"192.0.2.9:5555"}}`,
+		`{"id":"hw-bad3","mode":"network","network":{"protocol":"adb","adb_connection":"tcp"}}`,
+		`{"id":"hw-bad4","mode":"network","network":{"protocol":"adb","adb_connection":"bluetooth","host":"h"}}`,
+		`{"id":"hw-bad5","mode":"network","network":{"protocol":"adb","adb_connection":"usb","adb_serial":"S1","pairing_code":"123456"}}`,
+	} {
+		_, err := fixture.service.PetMCPTool(fixture.ctx, "aha_create_hardware_resource", json.RawMessage(args))
+		if err == nil {
+			t.Fatalf("invalid ADB resource was accepted: %s", args)
+		}
+		if strings.Contains(args, "pairing_code") && !strings.Contains(err.Error(), "hardware_credential_owner_only") {
+			t.Fatalf("pairing code must be refused as Owner-only: %v", err)
+		}
+	}
+
+	// Partial update: only adb_connection changes. Switching wireless→usb
+	// needs a serial; switching to usb with a serial drops host/port and the
+	// recorded pairing.
+	stored, err := fixture.database.HardwareResource(fixture.ctx, "hw-wireless")
+	if err != nil {
+		t.Fatal(err)
+	}
+	stored.ADBPairedAt = "2026-11-20T00:00:00Z"
+	if err := fixture.database.UpsertHardwareResource(fixture.ctx, stored); err != nil {
+		t.Fatal(err)
+	}
+	kept := decodePetHardwareView(t, petHardwareToolCall(t, fixture, "aha_update_hardware_resource",
+		`{"resource_id":"hw-wireless","description":"手机（改名）"}`))
+	if kept.ADBPairedAt == "" || !kept.Network.ADBWireless() || kept.Network.Port != 41235 {
+		t.Fatalf("unrelated pet edit changed the wireless target: %#v", kept)
+	}
+	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_update_hardware_resource",
+		json.RawMessage(`{"resource_id":"hw-wireless","network":{"adb_connection":"usb"}}`)); err == nil ||
+		!strings.Contains(err.Error(), "adb_serial") {
+		t.Fatalf("switch to USB without serial: %v", err)
+	}
+	switched := decodePetHardwareView(t, petHardwareToolCall(t, fixture, "aha_update_hardware_resource",
+		`{"resource_id":"hw-wireless","network":{"adb_connection":"usb","adb_serial":"SER2"}}`))
+	if !switched.Network.ADBUSB() || switched.Network.Host != "" || switched.ADBPairedAt != "" {
+		t.Fatalf("switch to USB: %#v", switched)
+	}
+	back := decodePetHardwareView(t, petHardwareToolCall(t, fixture, "aha_update_hardware_resource",
+		`{"resource_id":"hw-wireless","network":{"adb_connection":"tcp","host":"192.0.2.5"}}`))
+	if !back.Network.ADBTCP() || back.Network.ADBSerial != "" || back.Network.Port != 5555 {
+		t.Fatalf("switch USB→tcp: %#v", back.Network)
+	}
+	audits := auditDataByAction(t, fixture.databasePath)
+	if !strings.Contains(audits["pet.hardware_resource.update"], "network.adb_connection") {
+		t.Fatalf("connection form change was not audited: %s", audits["pet.hardware_resource.update"])
+	}
+}
diff --git a/internal/app/pet_takeover_adb_test.go b/internal/app/pet_takeover_adb_test.go
new file mode 100644
index 0000000..dce6273
--- /dev/null
+++ b/internal/app/pet_takeover_adb_test.go
@@ -0,0 +1,97 @@
+package app
+
+import (
+	"encoding/json"
+	"strings"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/hardwareconfig"
+	"github.com/ChinaKai/AHA2/internal/store"
+)
+
+// A remote USB serial belongs to the other machine: takeover must ask for a
+// local serial, and an omitted adb_connection follows the remote resource so
+// USB / network direct / wireless never silently swap meaning.
+func TestPetTakeoverKeepsADBConnectionForms(t *testing.T) {
+	fixture := newPetTaskFixture(t)
+	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
+		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
+		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
+		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
+		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
+		AllowManageModels: boolPointer(true),
+	}); err != nil {
+		t.Fatal(err)
+	}
+	now := time.Now().UTC()
+	project := domain.Project{ID: "project-adb-takeover", Name: "ADB 接管", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
+	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
+		t.Fatal(err)
+	}
+	localWorkspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{ProjectID: project.ID, Name: "本机", RootPath: t.TempDir()})
+	if err != nil {
+		t.Fatal(err)
+	}
+	remoteTask := domain.Task{
+		ID: "remote-adb-task", ProjectID: project.ID, WorkspaceID: localWorkspace.ID, Title: "远端 ADB",
+		OriginalRequest: "调试设备", Status: domain.TaskWaitingUser, CollaborationMode: "single", MaxAgents: 1,
+		KnowledgePolicy: project.KnowledgePolicy, CreatedAt: now, UpdatedAt: now,
+	}
+	taskPayload, _ := json.Marshal(remoteTask)
+	objects := []store.RemoteTaskObject{{OwnerDeviceID: "device-remote", ObjectType: "task", ObjectID: remoteTask.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: taskPayload}}
+	for _, group := range []domain.HardwareGroup{
+		{ID: "usb", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Protocol: "adb", SSHAuth: "auto", ADBConnection: "usb", ADBSerial: "REMOTE-SER"}},
+		{ID: "tv", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "10.0.0.9", Port: 5601, Protocol: "adb", SSHAuth: "auto", ADBConnection: "tcp"}},
+		// An older peer never sent adb_connection: it stays wireless.
+		{ID: "phone", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "10.0.0.10", Port: 41235, Protocol: "adb", SSHAuth: "auto"}},
+	} {
+		group.TaskID, group.Access, group.CreatedAt, group.UpdatedAt = remoteTask.ID, domain.HardwareAccessReadOnly, now, now
+		payload, _ := json.Marshal(group)
+		objects = append(objects, store.RemoteTaskObject{OwnerDeviceID: "device-remote", ObjectType: "hardware", ObjectID: group.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: payload})
+	}
+	for _, item := range objects {
+		if err := fixture.database.UpsertRemoteTaskObject(fixture.ctx, item); err != nil {
+			t.Fatal(err)
+		}
+	}
+	mirrors, err := fixture.service.PetRemoteTaskMirrors(fixture.ctx, project.ID)
+	if err != nil || len(mirrors) != 1 {
+		t.Fatalf("mirrors=%#v err=%v", mirrors, err)
+	}
+	groups := func(usbSerial string) []hardwareconfig.GroupPayload {
+		return []hardwareconfig.GroupPayload{
+			{ID: "usb", Network: domain.HardwareNetworkConfig{ADBSerial: usbSerial}},
+			{ID: "tv", Network: domain.HardwareNetworkConfig{Host: "10.0.0.19"}},
+			{ID: "phone", Network: domain.HardwareNetworkConfig{Host: "10.0.0.20"}},
+		}
+	}
+	if _, _, err := fixture.service.PetTakeoverRemoteTask(fixture.ctx, PetTakeoverTaskInput{
+		TaskID: mirrors[0].ID, WorkspaceID: localWorkspace.ID, ModelID: fixture.model.ID, Groups: groups(""),
+	}); err == nil || !strings.Contains(err.Error(), "USB 设备序列号") {
+		t.Fatalf("takeover without a local USB serial: %v", err)
+	}
+	_, taken, err := fixture.service.PetTakeoverRemoteTask(fixture.ctx, PetTakeoverTaskInput{
+		TaskID: mirrors[0].ID, WorkspaceID: localWorkspace.ID, ModelID: fixture.model.ID, Groups: groups("LOCAL-SER"),
+	})
+	if err != nil {
+		t.Fatal(err)
+	}
+	byID := map[string]domain.HardwareGroup{}
+	for _, group := range taken {
+		byID[group.ID] = group
+	}
+	if usb := byID["usb"].Network; !usb.ADBUSB() || usb.ADBSerial != "LOCAL-SER" || usb.Host != "" {
+		t.Fatalf("taken USB=%#v", usb)
+	}
+	if tv := byID["tv"].Network; !tv.ADBTCP() || tv.Host != "10.0.0.19" || tv.Port != 5601 {
+		t.Fatalf("taken network direct=%#v", tv)
+	}
+	if phone := byID["phone"].Network; !phone.ADBWireless() || phone.ADBConnection != "wireless" || phone.Port != 41235 {
+		t.Fatalf("taken wireless=%#v", phone)
+	}
+}
diff --git a/internal/hardware/adb_modes_test.go b/internal/hardware/adb_modes_test.go
new file mode 100644
index 0000000..af42f44
--- /dev/null
+++ b/internal/hardware/adb_modes_test.go
@@ -0,0 +1,352 @@
+//go:build !windows
+
+package hardware
+
+import (
+	"context"
+	"io"
+	"net"
+	"os"
+	"path/filepath"
+	"strings"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+// fakeADB writes a shell-script adb whose body handles the arguments and logs
+// every invocation (one line per call) so tests can assert which host-level
+// commands a connection form did or did not run.
+func fakeADB(t *testing.T, body string) (string, string) {
+	t.Helper()
+	directory := t.TempDir()
+	executable := filepath.Join(directory, "adb")
+	logPath := filepath.Join(directory, "calls.log")
+	script := "#!/bin/sh\necho \"$*\" >> \"" + logPath + "\"\n" + body + "\necho \"unexpected arguments: $*\" >&2\nexit 2\n"
+	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
+		t.Fatal(err)
+	}
+	return executable, logPath
+}
+
+func adbCalls(t *testing.T, logPath string) []string {
+	t.Helper()
+	data, err := os.ReadFile(logPath)
+	if err != nil && !os.IsNotExist(err) {
+		t.Fatal(err)
+	}
+	lines := []string{}
+	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
+		if line != "" {
+			lines = append(lines, line)
+		}
+	}
+	return lines
+}
+
+func assertNoHostADBCommands(t *testing.T, calls []string, forbidden ...string) {
+	t.Helper()
+	for _, call := range calls {
+		first := strings.Fields(call)
+		if len(first) == 0 {
+			continue
+		}
+		for _, command := range forbidden {
+			if first[0] == command {
+				t.Fatalf("unexpected host-level adb %s call: %q (all calls %q)", command, call, calls)
+			}
+		}
+	}
+}
+
+func TestParseADBDevicesListsOnlyUSBAndBlocksAmbiguousSerials(t *testing.T) {
+	t.Parallel()
+	output := strings.Join([]string{
+		"* daemon not running; starting now at tcp:5037",
+		"* daemon started successfully",
+		"List of devices attached",
+		"R58M12345A             device usb:1-1 product:beyond1 model:SM_G973F device:beyond1 transport_id:1",
+		"0123456789ABCDEF       unauthorized usb:1-2 transport_id:2",
+		"NOPERM01               no permissions (user in plugdev group; are your udev rules wrong?); see [http://developer.android.com/tools/device.html] usb:1-3 transport_id:3",
+		"192.168.1.5:5555       device product:tv model:BRAVIA device:tv transport_id:4",
+		"adb-R58M-abc._adb-tls-connect._tcp device product:x model:y device:z transport_id:5",
+		"emulator-5554          device product:sdk model:sdk device:emu transport_id:6",
+		"DUP1                   device usb:1-4 transport_id:7",
+		"DUP1                   device usb:1-5 transport_id:8",
+		"WIN-NO-USB-ATTR        device product:p model:Pixel_7 device:panther transport_id:9",
+		"",
+	}, "\n")
+	list := parseADBDevices(output)
+	if list.OtherCount != 3 {
+		t.Fatalf("network/mDNS/emulator transports must be counted but hidden: %#v", list)
+	}
+	byKey := map[string][]ADBUSBDevice{}
+	for _, device := range list.Devices {
+		byKey[device.Serial] = append(byKey[device.Serial], device)
+	}
+	if got := byKey["R58M12345A"]; len(got) != 1 || got[0].State != "device" || got[0].Model != "SM_G973F" || got[0].USB != "1-1" || !got[0].Selectable {
+		t.Fatalf("USB device parse: %#v", got)
+	}
+	if got := byKey["0123456789ABCDEF"]; len(got) != 1 || got[0].State != "unauthorized" || !got[0].Selectable {
+		t.Fatalf("unauthorized USB device parse: %#v", got)
+	}
+	if got := byKey["NOPERM01"]; len(got) != 1 || got[0].State != "no_permissions" || got[0].USB != "1-3" {
+		t.Fatalf("no-permissions USB device parse: %#v", got)
+	}
+	if got := byKey["DUP1"]; len(got) != 2 || got[0].Selectable || got[1].Selectable {
+		t.Fatalf("devices sharing a serial must not be selectable: %#v", got)
+	}
+	if got := byKey["WIN-NO-USB-ATTR"]; len(got) != 1 || !got[0].Selectable || got[0].Model != "Pixel_7" {
+		t.Fatalf("USB device without usb: attribute (Windows adb) parse: %#v", got)
+	}
+	for _, hidden := range []string{"192.168.1.5:5555", "adb-R58M-abc._adb-tls-connect._tcp", "emulator-5554"} {
+		if len(byKey[hidden]) != 0 {
+			t.Fatalf("non-USB transport %q was listed", hidden)
+		}
+	}
+	if empty := parseADBDevices("List of devices attached\n\n"); len(empty.Devices) != 0 || empty.Devices == nil {
+		t.Fatalf("empty device list must be a non-nil empty slice: %#v", empty)
+	}
+}
+
+func TestListADBUSBDevicesRunsOnlyDevicesList(t *testing.T) {
+	t.Parallel()
+	executable, logPath := fakeADB(t, `if [ "$1" = "devices" ] && [ "$2" = "-l" ]; then
+  echo "List of devices attached"
+  echo "SERIAL1 device usb:1-1 model:Phone transport_id:1"
+  exit 0
+fi`)
+	list, err := listADBUSBDevicesWithExecutable(context.Background(), executable)
+	if err != nil || len(list.Devices) != 1 || list.Devices[0].Serial != "SERIAL1" {
+		t.Fatalf("list=%#v err=%v", list, err)
+	}
+	if calls := adbCalls(t, logPath); len(calls) != 1 || calls[0] != "devices -l" {
+		t.Fatalf("enumeration must run exactly adb devices -l: %q", calls)
+	}
+}
+
+func TestExecuteADBUSBTargetsSerialWithoutNetworkCommands(t *testing.T) {
+	// Not parallel: the authorization wait is a package variable other tests shrink.
+	executable, logPath := fakeADB(t, `if [ "$1" = "-s" ] && [ "$2" = "R58M12345A" ] && [ "$3" = "get-state" ]; then
+  echo device
+  exit 0
+fi
+if [ "$1" = "-s" ] && [ "$2" = "R58M12345A" ] && [ "$3" = "exec-out" ] && [ "$4" = "screencap" ]; then
+  printf '\211PNG\r\n'
+  printf 'usb warning' >&2
+  exit 3
+fi`)
+	result, err := executeADBUSBWithExecutable(context.Background(), executable, "R58M12345A",
+		[]string{"exec-out", "screencap", "-p"}, nil, 5*time.Second)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if result.Endpoint != "R58M12345A" || result.ExitCode != 3 || string(result.Stdout) != "\x89PNG\r\n" || string(result.Stderr) != "usb warning" {
+		t.Fatalf("unexpected USB result: %#v", result)
+	}
+	calls := adbCalls(t, logPath)
+	assertNoHostADBCommands(t, calls, "connect", "pair", "mdns", "kill-server", "disconnect")
+	for _, call := range calls {
+		if !strings.HasPrefix(call, "-s R58M12345A ") {
+			t.Fatalf("USB command not pinned to the bound serial: %q", call)
+		}
+	}
+}
+
+func TestExecuteADBUSBReportsDeviceStates(t *testing.T) {
+	shrinkADBAuthorizeWait(t, 30*time.Millisecond)
+	cases := []struct {
+		name, body, want string
+	}{
+		{"missing", `if [ "$3" = "get-state" ]; then echo "error: device 'GONE1' not found" >&2; exit 1; fi`, "未在 AHA 宿主机上找到 USB 设备"},
+		{"ambiguous", `if [ "$3" = "get-state" ]; then echo "error: more than one device/emulator" >&2; exit 1; fi`, "多台设备使用同一序列号"},
+		{"offline", `if [ "$3" = "get-state" ]; then echo offline; exit 0; fi`, "离线"},
+		{"unauthorized", `if [ "$3" = "get-state" ]; then echo "error: device unauthorized." >&2; exit 1; fi`, "未授权"},
+		{"no_permissions", `if [ "$3" = "get-state" ]; then echo "error: insufficient permissions for device" >&2; exit 1; fi`, "没有访问 USB 设备"},
+	}
+	for _, testCase := range cases {
+		t.Run(testCase.name, func(t *testing.T) {
+			executable, logPath := fakeADB(t, testCase.body)
+			_, err := executeADBUSBWithExecutable(context.Background(), executable, "GONE1", []string{"shell", "id"}, nil, 2*time.Second)
+			if err == nil || !strings.Contains(err.Error(), testCase.want) {
+				t.Fatalf("err=%v want substring %q", err, testCase.want)
+			}
+			calls := adbCalls(t, logPath)
+			assertNoHostADBCommands(t, calls, "connect", "pair", "mdns", "kill-server")
+			for _, call := range calls {
+				if strings.Contains(call, " shell id") {
+					t.Fatalf("command ran on an unusable USB device: %q", calls)
+				}
+			}
+		})
+	}
+}
+
+func TestExecuteADBUSBWaitsForOwnerAuthorization(t *testing.T) {
+	shrinkADBAuthorizeWait(t, 2*time.Second)
+	directory := t.TempDir()
+	marker := filepath.Join(directory, "count")
+	executable, logPath := fakeADB(t, `if [ "$3" = "get-state" ]; then
+  count=$(cat "`+marker+`" 2>/dev/null || echo 0)
+  count=$((count+1))
+  echo $count > "`+marker+`"
+  if [ $count -lt 3 ]; then echo "error: device unauthorized." >&2; exit 1; fi
+  echo device; exit 0
+fi
+if [ "$3" = "shell" ]; then echo ok; exit 0; fi`)
+	result, err := executeADBUSBWithExecutable(context.Background(), executable, "SER1", []string{"shell", "true"}, nil, 5*time.Second)
+	if err != nil || string(result.Stdout) != "ok\n" {
+		t.Fatalf("result=%#v err=%v", result, err)
+	}
+	assertNoHostADBCommands(t, adbCalls(t, logPath), "kill-server", "connect", "pair")
+}
+
+func TestExecuteADBUSBRejectsNonUSBSerialsBeforeRunningADB(t *testing.T) {
+	executable, logPath := fakeADB(t, ``)
+	t.Setenv(adbExecutableEnvironment, executable)
+	for _, serial := range []string{"", "192.0.2.4:5555", "adb-x._adb-tls-connect._tcp", "emulator-5554", "-s", "model:Pixel", "a b", "usb:1-1", "x;rm"} {
+		if _, err := ExecuteADBUSB(context.Background(), serial, []string{"shell", "id"}, nil, time.Second); err == nil {
+			t.Fatalf("serial %q was accepted", serial)
+		}
+		if _, err := openADBUSBTerminal(context.Background(), serial); err == nil {
+			t.Fatalf("terminal serial %q was accepted", serial)
+		}
+	}
+	if calls := adbCalls(t, logPath); len(calls) != 0 {
+		t.Fatalf("invalid serials reached adb: %q", calls)
+	}
+}
+
+func TestOpenADBUSBTerminalStartsShellOnSerial(t *testing.T) {
+	// Not parallel: the authorization wait is a package variable other tests shrink.
+	executable, logPath := fakeADB(t, `if [ "$3" = "get-state" ]; then echo device; exit 0; fi
+if [ "$1" = "-s" ] && [ "$2" = "SER1" ] && [ "$3" = "shell" ] && [ "$4" = "-t" ] && [ "$5" = "-t" ]; then
+  echo "usb shell"
+  IFS= read -r line
+  exit 0
+fi`)
+	terminal, err := openADBUSBTerminalWithExecutable(context.Background(), executable, "SER1")
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer terminal.Close()
+	if resolved, ok := terminal.(*adbTerminal); !ok || resolved.ResolvedEndpoint() != "USB SER1" {
+		t.Fatalf("USB terminal endpoint: %#v", terminal)
+	}
+	buffer := make([]byte, 64)
+	count, _ := terminal.Read(buffer)
+	if !strings.Contains(string(buffer[:count]), "usb shell") {
+		t.Fatalf("USB shell output %q", buffer[:count])
+	}
+	assertNoHostADBCommands(t, adbCalls(t, logPath), "connect", "pair", "mdns", "kill-server")
+}
+
+// Network direct (classic adb tcpip): the explicit host:port is connected as
+// is even when mDNS would advertise a different port for the same host.
+func TestExecuteADBTCPUsesExplicitEndpointWithoutMDNSOrPairing(t *testing.T) {
+	// Not parallel: the authorization wait is a package variable other tests shrink.
+	executable, logPath := fakeADB(t, `if [ "$1" = "mdns" ]; then
+  echo "adb-test _adb-tls-connect._tcp 192.0.2.44:45678"
+  exit 0
+fi
+if [ "$1" = "connect" ] && [ "$2" = "192.0.2.44:5555" ]; then echo "connected to $2"; exit 0; fi
+if [ "$1" = "-s" ] && [ "$2" = "192.0.2.44:5555" ] && [ "$3" = "get-state" ]; then echo device; exit 0; fi
+if [ "$1" = "-s" ] && [ "$2" = "192.0.2.44:5555" ] && [ "$3" = "shell" ]; then echo tcp-ok; exit 0; fi`)
+	result, err := executeADBTCPWithExecutable(context.Background(), executable, "192.0.2.44:5555", []string{"shell", "id"}, nil, 5*time.Second)
+	if err != nil || result.Endpoint != "192.0.2.44:5555" || string(result.Stdout) != "tcp-ok\n" {
+		t.Fatalf("result=%#v err=%v", result, err)
+	}
+	calls := adbCalls(t, logPath)
+	assertNoHostADBCommands(t, calls, "mdns", "pair", "kill-server")
+	if len(calls) == 0 || calls[0] != "connect 192.0.2.44:5555" {
+		t.Fatalf("network direct must connect the explicit endpoint first: %q", calls)
+	}
+}
+
+func TestExecuteADBTCPCustomPortAndUnauthorizedDoesNotRestartServer(t *testing.T) {
+	shrinkADBAuthorizeWait(t, 30*time.Millisecond)
+	executable, logPath := fakeADB(t, `if [ "$1" = "connect" ] && [ "$2" = "192.0.2.50:5601" ]; then echo "connected to $2"; exit 0; fi
+if [ "$1" = "-s" ] && [ "$2" = "192.0.2.50:5601" ] && [ "$3" = "get-state" ]; then echo "error: device unauthorized." >&2; exit 1; fi`)
+	_, err := executeADBTCPWithExecutable(context.Background(), executable, "192.0.2.50:5601", []string{"shell", "id"}, nil, 2*time.Second)
+	if err == nil || !strings.Contains(err.Error(), "未授权") {
+		t.Fatalf("err=%v", err)
+	}
+	assertNoHostADBCommands(t, adbCalls(t, logPath), "kill-server", "mdns", "pair")
+}
+
+func TestOpenADBTCPTerminalKeepsExplicitEndpoint(t *testing.T) {
+	// Not parallel: the authorization wait is a package variable other tests shrink.
+	executable, logPath := fakeADB(t, `if [ "$1" = "mdns" ]; then echo "adb-test _adb-tls-connect._tcp 192.0.2.44:45678"; exit 0; fi
+if [ "$1" = "connect" ] && [ "$2" = "192.0.2.44:5555" ]; then echo "connected to $2"; exit 0; fi
+if [ "$3" = "get-state" ]; then echo device; exit 0; fi
+if [ "$1" = "-s" ] && [ "$2" = "192.0.2.44:5555" ] && [ "$3" = "shell" ]; then echo "tcp shell"; IFS= read -r line; exit 0; fi`)
+	terminal, err := openADBTCPTerminalWithExecutable(context.Background(), executable, "192.0.2.44:5555")
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer terminal.Close()
+	if resolved, ok := terminal.(*adbTerminal); !ok || resolved.ResolvedEndpoint() != "192.0.2.44:5555" {
+		t.Fatalf("TCP terminal endpoint: %#v", terminal)
+	}
+	assertNoHostADBCommands(t, adbCalls(t, logPath), "mdns", "pair", "kill-server")
+}
+
+func TestManagerRoutesEachADBConnectionFormToItsOpener(t *testing.T) {
+	t.Parallel()
+	type call struct{ opener, target string }
+	calls := make(chan call, 8)
+	manager := NewManager(&memoryRecorder{})
+	defer manager.Close()
+	pipe := func() io.ReadWriteCloser {
+		client, server := net.Pipe()
+		t.Cleanup(func() { server.Close() })
+		return client
+	}
+	manager.openADB = func(_ context.Context, endpoint string) (io.ReadWriteCloser, error) {
+		calls <- call{"wireless", endpoint}
+		return pipe(), nil
+	}
+	manager.openADBTCP = func(_ context.Context, endpoint string) (io.ReadWriteCloser, error) {
+		calls <- call{"tcp", endpoint}
+		return pipe(), nil
+	}
+	manager.openADBUSB = func(_ context.Context, serial string) (io.ReadWriteCloser, error) {
+		calls <- call{"usb", serial}
+		return pipe(), nil
+	}
+	groups := []struct {
+		group          domain.HardwareGroup
+		opener, target string
+		endpoint       string
+	}{
+		{domain.HardwareGroup{ID: "legacy", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "192.0.2.10", Port: 41234, Protocol: domain.HardwareProtocolADB}}, "wireless", "192.0.2.10:41234", "192.0.2.10:41234"},
+		{domain.HardwareGroup{ID: "tcp", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "192.0.2.11", Port: 5555, Protocol: domain.HardwareProtocolADB, ADBConnection: domain.HardwareADBConnectionTCP}}, "tcp", "192.0.2.11:5555", "192.0.2.11:5555"},
+		{domain.HardwareGroup{ID: "usb", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Protocol: domain.HardwareProtocolADB, ADBConnection: domain.HardwareADBConnectionUSB, ADBSerial: "SER9"}}, "usb", "SER9", "USB SER9"},
+	}
+	for _, item := range groups {
+		item.group.Access = domain.HardwareAccessReadWrite
+		status, err := manager.Connect(context.Background(), ConnectRequest{TaskID: "task-1", HardwareID: item.group.ID, Transport: "network", Group: item.group})
+		if err != nil || !status.Connected {
+			t.Fatalf("%s connect: %#v %v", item.group.ID, status, err)
+		}
+		got := <-calls
+		if got.opener != item.opener || got.target != item.target {
+			t.Fatalf("%s routed to %#v, want %s %s", item.group.ID, got, item.opener, item.target)
+		}
+		if status.Endpoint != item.endpoint {
+			t.Fatalf("%s status endpoint %q want %q", item.group.ID, status.Endpoint, item.endpoint)
+		}
+	}
+	// A USB group without a valid serial must not connect anywhere.
+	bad := domain.HardwareGroup{ID: "bad", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadWrite,
+		Network: domain.HardwareNetworkConfig{Protocol: domain.HardwareProtocolADB, ADBConnection: domain.HardwareADBConnectionUSB, ADBSerial: "192.0.2.4:5555"}}
+	if _, err := manager.Connect(context.Background(), ConnectRequest{TaskID: "task-1", HardwareID: "bad", Transport: "network", Group: bad}); err == nil {
+		t.Fatal("USB group with a network-style serial connected")
+	}
+	select {
+	case extra := <-calls:
+		t.Fatalf("invalid USB group reached an opener: %#v", extra)
+	default:
+	}
+}
diff --git a/internal/hardwareconfig/config_test.go b/internal/hardwareconfig/config_test.go
new file mode 100644
index 0000000..222eec3
--- /dev/null
+++ b/internal/hardwareconfig/config_test.go
@@ -0,0 +1,93 @@
+package hardwareconfig
+
+import (
+	"strings"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+func normalizeNetwork(t *testing.T, network domain.HardwareNetworkConfig) (domain.HardwareGroup, error) {
+	t.Helper()
+	return NormalizeGroup("", "res", 0, GroupPayload{Mode: domain.HardwareModeNetwork, Network: network}, domain.HardwareGroup{}, time.Unix(1, 0))
+}
+
+func TestNormalizeGroupADBConnectionForms(t *testing.T) {
+	t.Parallel()
+	// Legacy shape: protocol=adb without a connection form stays wireless with
+	// its host and port; it is never reinterpreted as network direct or USB.
+	legacy, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Host: "192.0.2.5", Port: 41235, Protocol: "adb", ADBSerial: "STRAY"})
+	if err != nil || legacy.Network.ADBConnection != domain.HardwareADBConnectionWireless ||
+		legacy.Network.Host != "192.0.2.5" || legacy.Network.Port != 41235 || legacy.Network.ADBSerial != "" {
+		t.Fatalf("legacy adb: %#v %v", legacy.Network, err)
+	}
+	wirelessDefault, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Host: "192.0.2.5", Protocol: "adb", ADBConnection: "wireless"})
+	if err != nil || wirelessDefault.Network.Port != 5555 {
+		t.Fatalf("wireless default port: %#v %v", wirelessDefault.Network, err)
+	}
+	tcp, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Host: "192.0.2.6", Protocol: "adb", ADBConnection: "tcp", ADBSerial: "STRAY"})
+	if err != nil || !tcp.Network.ADBTCP() || tcp.Network.Port != 5555 || tcp.Network.Host != "192.0.2.6" || tcp.Network.ADBSerial != "" {
+		t.Fatalf("tcp default 5555: %#v %v", tcp.Network, err)
+	}
+	tcpCustom, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Host: "192.0.2.6", Port: 5601, Protocol: "adb", ADBConnection: "tcp"})
+	if err != nil || tcpCustom.Network.Port != 5601 {
+		t.Fatalf("tcp custom port: %#v %v", tcpCustom.Network, err)
+	}
+	if _, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Protocol: "adb", ADBConnection: "tcp"}); err == nil {
+		t.Fatal("tcp without host was accepted")
+	}
+	// USB direct drops host/port that a client may still send and needs a serial.
+	usb, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Host: "192.0.2.7", Port: 5555, Protocol: "adb", ADBConnection: "usb", ADBSerial: " R58M12345A "})
+	if err != nil || !usb.Network.ADBUSB() || usb.Network.Host != "" || usb.Network.Port != 0 || usb.Network.ADBSerial != "R58M12345A" {
+		t.Fatalf("usb: %#v %v", usb.Network, err)
+	}
+	if !usb.Supports(domain.HardwareTransportNetwork) {
+		t.Fatal("USB group with a serial must support the network transport")
+	}
+	if _, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Protocol: "adb", ADBConnection: "usb"}); err == nil || !strings.Contains(err.Error(), "USB 设备序列号") {
+		t.Fatalf("usb without serial: %v", err)
+	}
+	for _, serial := range []string{"192.0.2.4:5555", "adb-R58M._adb-tls-connect._tcp", "emulator-5554", "-s", "model:Pixel", "usb:1-1", "a b", "x;rm -rf", strings.Repeat("a", 129)} {
+		if _, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Protocol: "adb", ADBConnection: "usb", ADBSerial: serial}); err == nil {
+			t.Fatalf("invalid USB serial %q was accepted", serial)
+		}
+	}
+	if _, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Host: "h", Protocol: "adb", ADBConnection: "bluetooth"}); err == nil {
+		t.Fatal("unknown ADB connection form was accepted")
+	}
+	// Other protocols never carry ADB fields.
+	ssh, err := NormalizeGroup("", "res", 0, GroupPayload{Mode: domain.HardwareModeNetwork, Username: "root",
+		Network: domain.HardwareNetworkConfig{Host: "h", Protocol: "ssh", ADBConnection: "usb", ADBSerial: "SER"}}, domain.HardwareGroup{}, time.Unix(1, 0))
+	if err != nil || ssh.Network.ADBConnection != "" || ssh.Network.ADBSerial != "" {
+		t.Fatalf("ssh kept ADB fields: %#v %v", ssh.Network, err)
+	}
+}
+
+func TestADBPairingStillValid(t *testing.T) {
+	t.Parallel()
+	wireless := domain.HardwareNetworkConfig{Host: "192.0.2.5", Port: 41235, Protocol: "adb", ADBConnection: "wireless"}
+	legacy := domain.HardwareNetworkConfig{Host: "192.0.2.5", Port: 5555, Protocol: "adb"}
+	network := domain.HardwareModeNetwork
+	if !ADBPairingStillValid(network, legacy, network, wireless) {
+		t.Fatal("pairing must survive an edit that keeps the same wireless host (legacy row)")
+	}
+	tcp := wireless
+	tcp.ADBConnection = "tcp"
+	usb := domain.HardwareNetworkConfig{Protocol: "adb", ADBConnection: "usb", ADBSerial: "SER"}
+	otherHost := wireless
+	otherHost.Host = "192.0.2.9"
+	ssh := wireless
+	ssh.Protocol = "ssh"
+	for name, after := range map[string]domain.HardwareNetworkConfig{"tcp": tcp, "usb": usb, "other host": otherHost, "ssh": ssh} {
+		if ADBPairingStillValid(network, wireless, network, after) {
+			t.Fatalf("pairing survived a switch to %s", name)
+		}
+	}
+	if ADBPairingStillValid(network, usb, network, wireless) || ADBPairingStillValid(network, tcp, network, wireless) {
+		t.Fatal("a non-wireless resource has no pairing to carry into wireless")
+	}
+	if ADBPairingStillValid(network, wireless, domain.HardwareModeSerial, wireless) {
+		t.Fatal("pairing survived a switch to serial mode")
+	}
+}
diff --git a/internal/httpapi/hardware_adb_modes_test.go b/internal/httpapi/hardware_adb_modes_test.go
new file mode 100644
index 0000000..e56bd9d
--- /dev/null
+++ b/internal/httpapi/hardware_adb_modes_test.go
@@ -0,0 +1,284 @@
+package httpapi
+
+import (
+	"context"
+	"errors"
+	"net/http"
+	"net/http/cookiejar"
+	"net/http/httptest"
+	"path/filepath"
+	"strings"
+	"sync"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/agentapi"
+	"github.com/ChinaKai/AHA2/internal/app"
+	"github.com/ChinaKai/AHA2/internal/auth"
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/hardware"
+	"github.com/ChinaKai/AHA2/internal/store"
+)
+
+type adbExecCall struct{ executor, target string }
+
+type adbExecRecorder struct {
+	mu    sync.Mutex
+	calls []adbExecCall
+}
+
+func (recorder *adbExecRecorder) executor(name string) func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error) {
+	return func(_ context.Context, target string, _ []string, _ []byte, _ time.Duration) (hardware.ADBExecResult, error) {
+		recorder.mu.Lock()
+		recorder.calls = append(recorder.calls, adbExecCall{name, target})
+		recorder.mu.Unlock()
+		return hardware.ADBExecResult{Endpoint: target, Stdout: []byte("ok")}, nil
+	}
+}
+
+func (recorder *adbExecRecorder) take() []adbExecCall {
+	recorder.mu.Lock()
+	defer recorder.mu.Unlock()
+	calls := recorder.calls
+	recorder.calls = nil
+	return calls
+}
+
+func TestAgentADBExecRoutesEachConnectionFormToTheBoundTarget(t *testing.T) {
+	ctx := context.Background()
+	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	task := createHardwareAPITask(t, database)
+	other := createHardwareAPITask(t, database)
+	now := time.Now().UTC()
+	adb := func(id string, network domain.HardwareNetworkConfig, access string) domain.HardwareGroup {
+		network.Protocol, network.SSHAuth = domain.HardwareProtocolADB, domain.HardwareSSHAuthAuto
+		return domain.HardwareGroup{ID: id, Mode: domain.HardwareModeNetwork, Network: network, Access: access, CreatedAt: now, UpdatedAt: now}
+	}
+	rw, ro := domain.HardwareAccessReadWrite, domain.HardwareAccessReadOnly
+	if err := database.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{
+		adb("legacy", domain.HardwareNetworkConfig{Host: "192.0.2.10", Port: 41234}, rw),
+		adb("tv", domain.HardwareNetworkConfig{Host: "192.0.2.11", Port: 5555, ADBConnection: "tcp"}, rw),
+		adb("tv-custom", domain.HardwareNetworkConfig{Host: "192.0.2.12", Port: 5601, ADBConnection: "tcp"}, rw),
+		adb("board", domain.HardwareNetworkConfig{ADBConnection: "usb", ADBSerial: "SERIAL-A"}, rw),
+		adb("board-ro", domain.HardwareNetworkConfig{ADBConnection: "usb", ADBSerial: "SERIAL-RO"}, ro),
+	}); err != nil {
+		t.Fatal(err)
+	}
+	if err := database.ReplaceHardwareGroups(ctx, other.ID, []domain.HardwareGroup{
+		adb("other-board", domain.HardwareNetworkConfig{ADBConnection: "usb", ADBSerial: "SERIAL-B"}, rw),
+	}); err != nil {
+		t.Fatal(err)
+	}
+	recorder := &adbExecRecorder{}
+	capabilities := agentapi.NewCapabilities()
+	server := httptest.NewServer(New(Config{
+		Store: database, AgentCapabilities: capabilities,
+		ExecuteADB:    recorder.executor("wireless"),
+		ExecuteADBTCP: recorder.executor("tcp"),
+		ExecuteADBUSB: recorder.executor("usb"),
+		PairADB: func(context.Context, string, string) (string, error) {
+			t.Fatal("Agent ADB exec must never pair")
+			return "", nil
+		},
+	}).Handler())
+	defer server.Close()
+	token, err := capabilities.Issue(task.ID, "main", "turn-adb-modes", time.Hour)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer capabilities.Revoke(token)
+	exec := func(hardwareID string, args ...string) int {
+		response := agentRequest(t, server.URL+"/api/v1/agent/hardware/"+hardwareID+"/adb/exec", http.MethodPost, token, map[string]any{"args": args})
+		response.Body.Close()
+		return response.StatusCode
+	}
+	for _, testCase := range []struct {
+		id   string
+		want adbExecCall
+	}{
+		{"legacy", adbExecCall{"wireless", "192.0.2.10:41234"}},
+		{"tv", adbExecCall{"tcp", "192.0.2.11:5555"}},
+		{"tv-custom", adbExecCall{"tcp", "192.0.2.12:5601"}},
+		{"board", adbExecCall{"usb", "SERIAL-A"}},
+	} {
+		if status := exec(testCase.id, "shell", "id"); status != http.StatusOK {
+			t.Fatalf("%s exec status=%d", testCase.id, status)
+		}
+		if calls := recorder.take(); len(calls) != 1 || calls[0] != testCase.want {
+			t.Fatalf("%s routed to %#v, want %#v", testCase.id, calls, testCase.want)
+		}
+	}
+	// Read-only USB, another Task's USB serial, host-level commands and
+	// option injection never reach an executor.
+	for _, testCase := range []struct {
+		id     string
+		args   []string
+		status int
+	}{
+		{"board-ro", []string{"shell", "id"}, http.StatusForbidden},
+		{"other-board", []string{"shell", "id"}, http.StatusNotFound},
+		{"board", []string{"devices"}, http.StatusBadRequest},
+		{"board", []string{"connect", "192.0.2.99:5555"}, http.StatusBadRequest},
+		{"board", []string{"-s", "SERIAL-B", "shell", "id"}, http.StatusBadRequest},
+		{"tv", []string{"pair", "192.0.2.11:37000"}, http.StatusBadRequest},
+		{"tv", []string{"kill-server"}, http.StatusBadRequest},
+	} {
+		if status := exec(testCase.id, testCase.args...); status != testCase.status {
+			t.Fatalf("%s %v status=%d want %d", testCase.id, testCase.args, status, testCase.status)
+		}
+		if calls := recorder.take(); len(calls) != 0 {
+			t.Fatalf("%s %v reached an executor: %#v", testCase.id, testCase.args, calls)
+		}
+	}
+}
+
+func TestOwnerADBResourceFormsPairingAndUSBEnumeration(t *testing.T) {
+	ctx := context.Background()
+	path := filepath.Join(t.TempDir(), "aha2.db")
+	database, err := store.Open(ctx, path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	authService := auth.NewService(database, "setup-test", time.Hour)
+	var pairMu sync.Mutex
+	pairedEndpoints := []string{}
+	listErr := error(nil)
+	capabilities := agentapi.NewCapabilities()
+	server := httptest.NewServer(New(Config{
+		Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}), AgentCapabilities: capabilities,
+		PairADB: func(_ context.Context, endpoint, _ string) (string, error) {
+			pairMu.Lock()
+			pairedEndpoints = append(pairedEndpoints, endpoint)
+			pairMu.Unlock()
+			return "Successfully paired", nil
+		},
+		ListADBUSBDevices: func(context.Context) (hardware.ADBUSBDeviceList, error) {
+			if listErr != nil {
+				return hardware.ADBUSBDeviceList{}, listErr
+			}
+			return hardware.ADBUSBDeviceList{Devices: []hardware.ADBUSBDevice{
+				{Serial: "SERIAL-A", State: "device", Model: "Pixel", Selectable: true},
+				{Serial: "SERIAL-B", State: "unauthorized", Selectable: true},
+			}, OtherCount: 1}, nil
+		},
+	}).Handler())
+	defer server.Close()
+	jar, _ := cookiejar.New(nil)
+	client := &http.Client{Jar: jar}
+	csrf := registerOwner(t, client, server.URL)
+
+	create := func(network map[string]any) (int, domain.HardwareResource, string) {
+		response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware-resources", map[string]any{
+			"description": "device", "mode": "network", "access": "read_write", "network": network,
+		}, csrf)
+		var payload struct {
+			Resource domain.HardwareResource `json:"resource"`
+			Message  string                  `json:"message"`
+		}
+		decodeResponse(t, response, &payload)
+		return response.StatusCode, payload.Resource, payload.Message
+	}
+	status, usb, _ := create(map[string]any{"host": "192.0.2.7", "port": 5555, "protocol": "adb", "adb_connection": "usb", "adb_serial": "SERIAL-A"})
+	if status != http.StatusCreated || !usb.Network.ADBUSB() || usb.Network.Host != "" || usb.Network.Port != 0 || usb.Network.ADBSerial != "SERIAL-A" {
+		t.Fatalf("usb create status=%d resource=%#v", status, usb.Network)
+	}
+	status, tcp, _ := create(map[string]any{"host": "192.0.2.8", "protocol": "adb", "adb_connection": "tcp"})
+	if status != http.StatusCreated || !tcp.Network.ADBTCP() || tcp.Network.Port != 5555 || tcp.Network.Host != "192.0.2.8" {
+		t.Fatalf("tcp create status=%d resource=%#v", status, tcp.Network)
+	}
+	// An older client that never sends adb_connection keeps wireless meaning.
+	status, wireless, _ := create(map[string]any{"host": "192.0.2.9", "port": 41235, "protocol": "adb"})
+	if status != http.StatusCreated || !wireless.Network.ADBWireless() || wireless.Network.ADBConnection != "wireless" || wireless.Network.Port != 41235 {
+		t.Fatalf("legacy-client create status=%d resource=%#v", status, wireless.Network)
+	}
+	for _, serial := range []string{"192.0.2.4:5555", "emulator-5554", "-s"} {
+		if status, _, message := create(map[string]any{"protocol": "adb", "adb_connection": "usb", "adb_serial": serial}); status != http.StatusBadRequest || !strings.Contains(message, "序列号") {
+			t.Fatalf("invalid serial %q status=%d message=%q", serial, status, message)
+		}
+	}
+	if status, _, _ := create(map[string]any{"protocol": "adb", "adb_connection": "usb"}); status != http.StatusBadRequest {
+		t.Fatalf("usb without serial status=%d", status)
+	}
+
+	pair := func(id string) int {
+		response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware-resources/"+id+"/adb/pair",
+			map[string]any{"pairing_port": 37124, "pairing_code": "123456"}, csrf)
+		response.Body.Close()
+		return response.StatusCode
+	}
+	if pair(usb.ID) != http.StatusBadRequest || pair(tcp.ID) != http.StatusBadRequest {
+		t.Fatal("USB direct and network direct resources must refuse pairing")
+	}
+	if len(pairedEndpoints) != 0 {
+		t.Fatalf("pairing ran for a non-wireless resource: %q", pairedEndpoints)
+	}
+	if pair(wireless.ID) != http.StatusOK || len(pairedEndpoints) != 1 || pairedEndpoints[0] != "192.0.2.9:37124" {
+		t.Fatalf("wireless pairing regressed: %q", pairedEndpoints)
+	}
+	stored, _ := database.HardwareResource(ctx, wireless.ID)
+	if stored.ADBPairedAt == "" {
+		t.Fatal("wireless pairing state was not saved")
+	}
+	// Same-host wireless edit keeps the pairing; switching to tcp drops it.
+	update := func(id string, network map[string]any) domain.HardwareResource {
+		response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/hardware-resources/"+id, map[string]any{
+			"description": "device", "mode": "network", "access": "read_write", "network": network,
+		}, csrf)
+		var payload struct {
+			Resource domain.HardwareResource `json:"resource"`
+		}
+		decodeResponse(t, response, &payload)
+		if response.StatusCode != http.StatusOK {
+			t.Fatalf("update %s status=%d", id, response.StatusCode)
+		}
+		return payload.Resource
+	}
+	if kept := update(wireless.ID, map[string]any{"host": "192.0.2.9", "port": 41299, "protocol": "adb", "adb_connection": "wireless"}); kept.ADBPairedAt == "" {
+		t.Fatal("same-host wireless edit dropped the pairing")
+	}
+	if switched := update(wireless.ID, map[string]any{"host": "192.0.2.9", "port": 5555, "protocol": "adb", "adb_connection": "tcp"}); switched.ADBPairedAt != "" || !switched.Network.ADBTCP() {
+		t.Fatalf("switch to network direct kept pairing: %#v", switched)
+	}
+	if switched := update(tcp.ID, map[string]any{"host": "192.0.2.8", "port": 5555, "protocol": "adb", "adb_connection": "usb", "adb_serial": "SERIAL-B"}); !switched.Network.ADBUSB() || switched.Network.Host != "" || switched.Network.ADBSerial != "SERIAL-B" {
+		t.Fatalf("switch tcp→usb: %#v", switched.Network)
+	}
+
+	// USB enumeration is Owner-session only and needs CSRF.
+	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware/adb/usb-devices", map[string]any{}, csrf)
+	var listed struct {
+		Devices    []hardware.ADBUSBDevice `json:"devices"`
+		OtherCount int                     `json:"other_count"`
+	}
+	decodeResponse(t, response, &listed)
+	if response.StatusCode != http.StatusOK || len(listed.Devices) != 2 || listed.Devices[0].Serial != "SERIAL-A" || listed.OtherCount != 1 {
+		t.Fatalf("usb devices status=%d payload=%#v", response.StatusCode, listed)
+	}
+	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware/adb/usb-devices", map[string]any{}, "")
+	response.Body.Close()
+	if response.StatusCode != http.StatusForbidden {
+		t.Fatalf("usb devices without CSRF status=%d", response.StatusCode)
+	}
+	task := createHardwareAPITask(t, database)
+	token, err := capabilities.Issue(task.ID, "main", "turn-usb-enum", time.Hour)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer capabilities.Revoke(token)
+	response = agentRequest(t, server.URL+"/api/v1/hardware/adb/usb-devices", http.MethodPost, token, map[string]any{})
+	response.Body.Close()
+	if response.StatusCode != http.StatusUnauthorized {
+		t.Fatalf("Agent token reached USB enumeration: status=%d", response.StatusCode)
+	}
+	listErr = errors.New("ADB executable not found; install Android Platform Tools or set AHA2_ADB_PATH")
+	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware/adb/usb-devices", map[string]any{}, csrf)
+	var failed map[string]any
+	decodeResponse(t, response, &failed)
+	if response.StatusCode != http.StatusConflict || !strings.Contains(failed["message"].(string), "找不到 ADB") {
+		t.Fatalf("enumeration failure status=%d payload=%#v", response.StatusCode, failed)
+	}
+}
diff --git a/internal/httpapi/takeover_adb_test.go b/internal/httpapi/takeover_adb_test.go
new file mode 100644
index 0000000..64499b3
--- /dev/null
+++ b/internal/httpapi/takeover_adb_test.go
@@ -0,0 +1,120 @@
+package httpapi
+
+import (
+	"context"
+	"encoding/json"
+	"net/http"
+	"net/http/cookiejar"
+	"net/http/httptest"
+	"path/filepath"
+	"strings"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/app"
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/store"
+)
+
+// The Web takeover endpoint mirrors the pet path: a remote USB serial belongs
+// to the other machine, and an omitted adb_connection follows the remote
+// resource so USB / network direct / wireless never swap meaning.
+func TestTaskTakeoverKeepsADBConnectionForms(t *testing.T) {
+	ctx := context.Background()
+	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	now := time.Now().UTC()
+	project := domain.Project{ID: "adb-takeover-project", Name: "ADB", CreatedAt: now, UpdatedAt: now}
+	if err := database.CreateProject(ctx, project); err != nil {
+		t.Fatal(err)
+	}
+	local := domain.Workspace{ID: "adb-local", ProjectID: project.ID, Name: "Local", Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now}
+	if err := database.CreateWorkspace(ctx, local); err != nil {
+		t.Fatal(err)
+	}
+	env := domain.EnvGroup{ID: "adb-env", Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
+	model := domain.Model{ID: "adb-model", DisplayName: "Model", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
+	if err := database.UpsertEnvGroup(ctx, env); err != nil {
+		t.Fatal(err)
+	}
+	if err := database.UpsertModel(ctx, model); err != nil {
+		t.Fatal(err)
+	}
+	remoteTask := domain.Task{ID: "adb-source-task", ProjectID: project.ID, WorkspaceID: "source-workspace", Title: "Remote ADB", OriginalRequest: "debug", Status: domain.TaskWaitingUser, CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "inherit", OwnerDeviceID: "device-remote", ReadOnly: true, CreatedAt: now, UpdatedAt: now}
+	taskPayload, _ := json.Marshal(remoteTask)
+	objects := []store.RemoteTaskObject{{OwnerDeviceID: "device-remote", ObjectType: "task", ObjectID: remoteTask.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: taskPayload, CreatedAt: now, UpdatedAt: now}}
+	for _, group := range []domain.HardwareGroup{
+		{ID: "usb", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Protocol: "adb", SSHAuth: "auto", ADBConnection: "usb", ADBSerial: "REMOTE-SER"}},
+		{ID: "tv", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "10.0.0.9", Port: 5601, Protocol: "adb", SSHAuth: "auto", ADBConnection: "tcp"}},
+		// An older peer never sent adb_connection: it stays wireless.
+		{ID: "phone", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "10.0.0.10", Port: 41235, Protocol: "adb", SSHAuth: "auto"}},
+	} {
+		group.TaskID, group.Access, group.CreatedAt, group.UpdatedAt = remoteTask.ID, domain.HardwareAccessReadOnly, now, now
+		payload, _ := json.Marshal(group)
+		objects = append(objects, store.RemoteTaskObject{OwnerDeviceID: "device-remote", ObjectType: "hardware", ObjectID: "device-remote:" + remoteTask.ID + ":" + group.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: payload, CreatedAt: now, UpdatedAt: now})
+	}
+	for _, item := range objects {
+		if err := database.UpsertRemoteTaskObject(ctx, item); err != nil {
+			t.Fatal(err)
+		}
+	}
+	mirrors, err := database.RemoteTaskMirrors(ctx, project.ID)
+	if err != nil || len(mirrors) != 1 {
+		t.Fatalf("mirrors=%#v err=%v", mirrors, err)
+	}
+	server := httptest.NewServer(New(Config{Store: database, Auth: authServiceForTest(database), App: app.NewService(database, nil, app.StubExecutor{}), Secrets: &fakeSecretStore{}}).Handler())
+	defer server.Close()
+	jar, _ := cookiejar.New(nil)
+	client := &http.Client{Jar: jar}
+	csrf := registerOwner(t, client, server.URL)
+	takeover := func(usbNetwork map[string]any) *http.Response {
+		return requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+mirrors[0].Task.ID+"/takeover", map[string]any{
+			"workspace_id": local.ID, "model_id": model.ID, "collaboration_mode": "single",
+			"groups": []map[string]any{
+				{"id": "usb", "mode": "network", "network": usbNetwork},
+				{"id": "tv", "mode": "network", "network": map[string]any{"host": "10.0.0.19", "protocol": "adb"}},
+				{"id": "phone", "mode": "network", "network": map[string]any{"host": "10.0.0.20", "protocol": "adb"}},
+			},
+		}, csrf)
+	}
+	response := takeover(map[string]any{"protocol": "adb"})
+	var failed map[string]any
+	decodeResponse(t, response, &failed)
+	if response.StatusCode != http.StatusBadRequest || failed["error"] != "local_hardware_fields_required" || !strings.Contains(failed["message"].(string), "USB 设备序列号") {
+		t.Fatalf("takeover without a local USB serial status=%d payload=%#v", response.StatusCode, failed)
+	}
+	response = takeover(map[string]any{"protocol": "adb", "adb_serial": "192.0.2.4:5555"})
+	response.Body.Close()
+	if response.StatusCode != http.StatusBadRequest {
+		t.Fatalf("takeover with a network-style USB serial status=%d", response.StatusCode)
+	}
+	response = takeover(map[string]any{"protocol": "adb", "adb_serial": "LOCAL-SER"})
+	var created struct {
+		Task     domain.Task            `json:"task"`
+		Hardware []domain.HardwareGroup `json:"hardware"`
+	}
+	decodeResponse(t, response, &created)
+	if response.StatusCode != http.StatusCreated {
+		t.Fatalf("takeover status=%d payload=%#v", response.StatusCode, created)
+	}
+	groups, err := database.HardwareGroups(ctx, created.Task.ID)
+	if err != nil || len(groups) != 3 {
+		t.Fatalf("local groups=%#v err=%v", groups, err)
+	}
+	byID := map[string]domain.HardwareGroup{}
+	for _, group := range groups {
+		byID[group.ID] = group
+	}
+	if usb := byID["usb"].Network; !usb.ADBUSB() || usb.ADBSerial != "LOCAL-SER" || usb.Host != "" {
+		t.Fatalf("taken USB=%#v", usb)
+	}
+	if tv := byID["tv"].Network; !tv.ADBTCP() || tv.Host != "10.0.0.19" || tv.Port != 5601 {
+		t.Fatalf("taken network direct=%#v", tv)
+	}
+	if phone := byID["phone"].Network; !phone.ADBWireless() || phone.ADBConnection != "wireless" || phone.Port != 41235 {
+		t.Fatalf("taken wireless=%#v", phone)
+	}
+}
diff --git a/internal/prompt/hardware_adb_context_test.go b/internal/prompt/hardware_adb_context_test.go
new file mode 100644
index 0000000..d0933a9
--- /dev/null
+++ b/internal/prompt/hardware_adb_context_test.go
@@ -0,0 +1,39 @@
+package prompt
+
+import (
+	"strings"
+	"testing"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+func TestHardwareContextShowsADBConnectionForms(t *testing.T) {
+	t.Parallel()
+	groups := hardwareContextGroups([]domain.HardwareGroup{
+		{ID: "legacy", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadWrite,
+			Network: domain.HardwareNetworkConfig{Host: "192.0.2.10", Port: 41234, Protocol: domain.HardwareProtocolADB}},
+		{ID: "tv", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadWrite,
+			Network: domain.HardwareNetworkConfig{Host: "192.0.2.11", Port: 5555, Protocol: domain.HardwareProtocolADB, ADBConnection: domain.HardwareADBConnectionTCP}},
+		{ID: "board", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadOnly,
+			Network: domain.HardwareNetworkConfig{Protocol: domain.HardwareProtocolADB, ADBConnection: domain.HardwareADBConnectionUSB, ADBSerial: "SERIAL-A"}},
+	})
+	for _, name := range []string{"templates/context-hardware.md", "templates/zh-CN/context-hardware.md"} {
+		content, err := templateFiles.ReadFile(name)
+		if err != nil {
+			t.Fatal(err)
+		}
+		rendered, err := renderTemplate(name, string(content), templateData{HardwareGroups: groups})
+		if err != nil {
+			t.Fatalf("%s: %v", name, err)
+		}
+		for _, want := range []string{"192.0.2.10:41234", "192.0.2.11:5555", "SERIAL-A"} {
+			if !strings.Contains(rendered, want) {
+				t.Fatalf("%s missing %q:\n%s", name, want, rendered)
+			}
+		}
+		// A USB group has no host:port; the template must not print an empty one.
+		if strings.Contains(rendered, ":0") || strings.Contains(rendered, " :") {
+			t.Fatalf("%s printed an empty network endpoint for USB:\n%s", name, rendered)
+		}
+	}
+}
diff --git a/internal/store/hardware_adb_test.go b/internal/store/hardware_adb_test.go
new file mode 100644
index 0000000..213b9e6
--- /dev/null
+++ b/internal/store/hardware_adb_test.go
@@ -0,0 +1,135 @@
+package store
+
+import (
+	"context"
+	"database/sql"
+	"path/filepath"
+	"testing"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+func TestHardwareResourceADBConnectionFormsPersistAndLegacyStaysWireless(t *testing.T) {
+	t.Parallel()
+	ctx := context.Background()
+	path := filepath.Join(t.TempDir(), "aha2.db")
+	database, err := Open(ctx, path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	// A row written before v156 (no connection form) that was already paired.
+	if _, err := database.db.ExecContext(ctx, `
+		INSERT INTO hardware_resources(id,description,mode,serial_device,serial_baudrate,network_host,network_port,
+			network_protocol,ssh_auth,username,credential_ref,password_configured,adb_paired_at,access,created_at,updated_at)
+		VALUES('legacy','Phone','network','',115200,'192.0.2.5',41235,'adb','auto','','',0,'2026-11-01T00:00:00Z','read_write',
+			'2026-11-01T00:00:00Z','2026-11-01T00:00:00Z')`); err != nil {
+		t.Fatal(err)
+	}
+	for _, resource := range []domain.HardwareResource{
+		{ID: "tcp", Description: "TV", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadWrite,
+			Network: domain.HardwareNetworkConfig{Host: "192.0.2.6", Port: 5555, Protocol: "adb", SSHAuth: "auto", ADBConnection: "tcp"}},
+		{ID: "usb", Description: "Board", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadOnly,
+			Network: domain.HardwareNetworkConfig{Protocol: "adb", SSHAuth: "auto", ADBConnection: "usb", ADBSerial: "R58M12345A"}},
+		// A stray serial on a non-ADB resource is never stored.
+		{ID: "ssh", Description: "Router", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadWrite,
+			Network: domain.HardwareNetworkConfig{Host: "192.0.2.1", Port: 22, Protocol: "ssh", SSHAuth: "auto", ADBConnection: "usb", ADBSerial: "X"}},
+	} {
+		if err := database.UpsertHardwareResource(ctx, resource); err != nil {
+			t.Fatal(err)
+		}
+	}
+	database.Close()
+
+	// Reopen: v156 must be reentrant and must not rewrite existing rows.
+	database, err = Open(ctx, path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	check := func(resource domain.HardwareResource) {
+		t.Helper()
+		switch resource.ID {
+		case "legacy":
+			if !resource.Network.ADBWireless() || resource.Network.ADBConnection != "wireless" ||
+				resource.Network.Host != "192.0.2.5" || resource.Network.Port != 41235 || resource.ADBPairedAt == "" {
+				t.Fatalf("legacy ADB row changed meaning: %#v", resource)
+			}
+		case "tcp":
+			if !resource.Network.ADBTCP() || resource.Network.Host != "192.0.2.6" || resource.Network.Port != 5555 || resource.Network.ADBSerial != "" {
+				t.Fatalf("tcp row: %#v", resource.Network)
+			}
+		case "usb":
+			if !resource.Network.ADBUSB() || resource.Network.ADBSerial != "R58M12345A" || resource.Network.Host != "" {
+				t.Fatalf("usb row: %#v", resource.Network)
+			}
+			if !resource.Supports(domain.HardwareTransportNetwork) {
+				t.Fatal("USB resource must support the network transport")
+			}
+		case "ssh":
+			if resource.Network.ADBConnection != "" || resource.Network.ADBSerial != "" {
+				t.Fatalf("ssh row kept ADB fields: %#v", resource.Network)
+			}
+		}
+	}
+	list, err := database.HardwareResources(ctx)
+	if err != nil || len(list) != 4 {
+		t.Fatalf("list=%#v err=%v", list, err)
+	}
+	for _, resource := range list {
+		check(resource)
+		single, err := database.HardwareResource(ctx, resource.ID)
+		if err != nil {
+			t.Fatal(err)
+		}
+		check(single)
+	}
+	raw, err := sql.Open("sqlite", path)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer raw.Close()
+	var storedConnection string
+	if err := raw.QueryRowContext(ctx, `SELECT adb_connection FROM hardware_resources WHERE id='legacy'`).Scan(&storedConnection); err != nil {
+		t.Fatal(err)
+	}
+	if storedConnection != "" {
+		t.Fatalf("migration rewrote the legacy row's stored connection form to %q", storedConnection)
+	}
+	var recorded int
+	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=156`).Scan(&recorded); err != nil || recorded != 1 {
+		t.Fatalf("schema v156 recorded=%d err=%v", recorded, err)
+	}
+}
+
+// The legacy Task-local "both" shape splits into a serial and a network
+// resource; a USB direct ADB side must survive the split.
+func TestReplaceHardwareGroupsKeepsUSBNetworkSideOfBothMode(t *testing.T) {
+	t.Parallel()
+	ctx := context.Background()
+	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	task := createStoreTestTask(t, database)
+	if err := database.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{{
+		ID: "board", Mode: domain.HardwareModeBoth, Access: domain.HardwareAccessReadWrite,
+		Serial:  domain.HardwareSerialConfig{Device: "/dev/ttyUSB0", Baudrate: 115200},
+		Network: domain.HardwareNetworkConfig{Protocol: "adb", SSHAuth: "auto", ADBConnection: "usb", ADBSerial: "SERIAL-A"},
+	}}); err != nil {
+		t.Fatal(err)
+	}
+	groups, err := database.HardwareGroups(ctx, task.ID)
+	if err != nil || len(groups) != 2 {
+		t.Fatalf("groups=%#v err=%v", groups, err)
+	}
+	var usb domain.HardwareGroup
+	for _, group := range groups {
+		if group.Mode == domain.HardwareModeNetwork {
+			usb = group
+		}
+	}
+	if !usb.Network.ADBUSB() || usb.Network.ADBSerial != "SERIAL-A" || !usb.Supports(domain.HardwareTransportNetwork) {
+		t.Fatalf("USB side of a both-mode group was lost: %#v", groups)
+	}
+}
~~~~
